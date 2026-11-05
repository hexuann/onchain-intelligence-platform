package indexer

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type chainMemoryRepo struct {
	mu        sync.Mutex
	blocks    map[uint64]Block
	rollbacks []uint64
}

func (m *chainMemoryRepo) Checkpoint(_ context.Context, from uint64) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for {
		if _, ok := m.blocks[from]; !ok {
			return from, nil
		}
		from++
	}
}
func (m *chainMemoryRepo) SaveBlock(_ context.Context, b Block) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b.Number > 0 {
		if parent, ok := m.blocks[b.Number-1]; ok && parent.Hash != b.ParentHash {
			return fmt.Errorf("bad parent")
		}
	}
	m.blocks[b.Number] = b
	return nil
}
func (m *chainMemoryRepo) Tip(_ context.Context) (uint64, string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n uint64
	found := false
	for k := range m.blocks {
		if !found || k > n {
			n = k
			found = true
		}
	}
	if !found {
		return 0, "", false, nil
	}
	return n, m.blocks[n].Hash, true, nil
}
func (m *chainMemoryRepo) BlockHash(_ context.Context, n uint64) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blocks[n]
	return b.Hash, ok, nil
}
func (m *chainMemoryRepo) RollbackFrom(_ context.Context, n uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.blocks {
		if k >= n {
			delete(m.blocks, k)
		}
	}
	m.rollbacks = append(m.rollbacks, n)
	return nil
}
func TestSyncOnceDetectsReorgAtSameHeight(t *testing.T) {
	src, _ := fixture()
	repo := &chainMemoryRepo{blocks: map[uint64]Block{}}
	runner := Runner{Source: src, Repo: repo, Config: Config{StartBlock: 0, Workers: 2}, RetryDelay: time.Millisecond}
	if err := runner.syncOnce(context.Background(), repo, 8); err != nil {
		t.Fatal(err)
	}
	old := src.blocks[2]
	old.Hash = "replacement-2"
	src.blocks[2] = old
	if err := runner.syncOnce(context.Background(), repo, 8); err != nil {
		t.Fatal(err)
	}
	if repo.blocks[2].Hash != "replacement-2" || len(repo.rollbacks) != 1 || repo.rollbacks[0] != 2 {
		t.Fatalf("unexpected rollback %+v / blocks %+v", repo.rollbacks, repo.blocks)
	}
}
func TestReorgLookbackRejectsDeepChangesWithoutDeletion(t *testing.T) {
	src, _ := fixture()
	repo := &chainMemoryRepo{blocks: map[uint64]Block{}}
	runner := Runner{Source: src, Repo: repo, Config: Config{StartBlock: 0, Workers: 2}, RetryDelay: time.Millisecond}
	if err := runner.syncOnce(context.Background(), repo, 8); err != nil {
		t.Fatal(err)
	}
	for n := uint64(0); n <= 2; n++ {
		b := src.blocks[n]
		b.Hash = fmt.Sprintf("other-%d", n)
		src.blocks[n] = b
	}
	if err := runner.syncOnce(context.Background(), repo, 1); err == nil {
		t.Fatal("expected depth error")
	}
	if len(repo.rollbacks) != 0 || len(repo.blocks) != 3 {
		t.Fatal("unsafe rollback occurred")
	}
}
func TestContinuousStopsOnCancel(t *testing.T) {
	src, _ := fixture()
	repo := &chainMemoryRepo{blocks: map[uint64]Block{}}
	runner := Runner{Source: src, Repo: repo, Config: Config{StartBlock: 0, Workers: 2}, RetryDelay: time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := runner.RunContinuous(ctx, 5*time.Millisecond, 8); err != nil {
		t.Fatal(err)
	}
	if len(repo.blocks) != 3 {
		t.Fatal("missing blocks")
	}
}

type receiptSource struct {
	*fakeSource
	mu                sync.Mutex
	active, maxActive int
}

func (s *receiptSource) Receipt(ctx context.Context, h string) (Receipt, error) {
	s.mu.Lock()
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.active--; s.mu.Unlock() }()
	select {
	case <-ctx.Done():
		return Receipt{}, ctx.Err()
	case <-time.After(12 * time.Millisecond):
	}
	return s.fakeSource.Receipt(ctx, h)
}
func TestReceiptsFetchedConcurrentlyWithinBound(t *testing.T) {
	src, repo := fixture()
	for i := 0; i < 8; i++ {
		h := fmt.Sprintf("extra-%d", i)
		b := src.blocks[0]
		b.Transactions = append(b.Transactions, Transaction{Hash: h})
		src.blocks[0] = b
		src.receipts[h] = Receipt{TransactionHash: h, StatusHex: "0x1"}
	}
	wrapped := &receiptSource{fakeSource: src}
	runner := Runner{Source: wrapped, Repo: repo, Config: Config{StartBlock: 0, EndBlock: 0, Workers: 1}, ReceiptWorkers: 3}
	if err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if wrapped.maxActive < 2 || wrapped.maxActive > 3 {
		t.Fatalf("max receipt concurrency=%d", wrapped.maxActive)
	}
}
