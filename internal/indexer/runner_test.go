package indexer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeSource struct {
	mu           sync.Mutex
	latest       uint64
	blocks       map[uint64]Block
	receipts     map[string]Receipt
	blockFails   int
	receiptFails int
}

func (f *fakeSource) LatestBlock(context.Context) (uint64, error) { return f.latest, nil }
func (f *fakeSource) BlockWithTransactions(_ context.Context, n uint64) (Block, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.blockFails > 0 {
		f.blockFails--
		return Block{}, errors.New("temporary block error")
	}
	b, ok := f.blocks[n]
	if !ok {
		return Block{}, fmt.Errorf("missing block %d", n)
	}
	return b, nil
}
func (f *fakeSource) Receipt(_ context.Context, h string) (Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.receiptFails > 0 {
		f.receiptFails--
		return Receipt{}, errors.New("temporary receipt error")
	}
	r, ok := f.receipts[h]
	if !ok {
		return Receipt{}, fmt.Errorf("missing receipt %s", h)
	}
	return r, nil
}

type memoryRepo struct {
	mu       sync.Mutex
	blocks   map[uint64]Block
	failSave bool
}

func (m *memoryRepo) Checkpoint(_ context.Context, from uint64) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := from
	for {
		if _, ok := m.blocks[next]; !ok {
			return next, nil
		}
		next++
	}
}
func (m *memoryRepo) SaveBlock(_ context.Context, b Block) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failSave {
		return errors.New("db unavailable")
	}
	m.blocks[b.Number] = b
	return nil
}
func fixture() (*fakeSource, *memoryRepo) {
	s := &fakeSource{latest: 2, blocks: map[uint64]Block{}, receipts: map[string]Receipt{}}
	for n := uint64(0); n <= 2; n++ {
		hash := fmt.Sprintf("tx-%d", n)
		s.blocks[n] = Block{Number: n, Hash: fmt.Sprintf("block-%d", n), ParentHash: fmt.Sprintf("block-%d", n-1), TimestampHex: "0x65a00000", Transactions: []Transaction{{Hash: hash, From: "0xfrom", To: "0xto", Value: "0x10", Gas: "0x5208", GasPrice: "0x1"}}}
		s.receipts[hash] = Receipt{TransactionHash: hash, StatusHex: "0x1", Logs: []Log{{Address: "0xcontract", Topics: []string{"0xtopic"}, Data: "0x", Index: 0}}}
	}
	return s, &memoryRepo{blocks: map[uint64]Block{}}
}
func TestRunStoresBlocksTransactionsAndLogs(t *testing.T) {
	s, repo := fixture()
	r := Runner{Source: s, Repo: repo, Config: Config{StartBlock: 0, EndBlock: 2, Workers: 3}, RetryDelay: time.Millisecond}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.blocks) != 3 {
		t.Fatalf("got %d blocks, want 3", len(repo.blocks))
	}
	for n, b := range repo.blocks {
		if len(b.Transactions) != 1 || b.Transactions[0].Status != 1 || len(b.Transactions[0].Logs) != 1 {
			t.Fatalf("block %d not fully indexed: %+v", n, b)
		}
	}
}
func TestRunRetriesTransientRPCFailure(t *testing.T) {
	s, repo := fixture()
	s.blockFails = 1
	s.receiptFails = 1
	r := Runner{Source: s, Repo: repo, Config: Config{StartBlock: 0, EndBlock: 0, Workers: 1}, RetryLimit: 3, RetryDelay: time.Millisecond}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.blocks) != 1 {
		t.Fatalf("got %d blocks", len(repo.blocks))
	}
}
func TestRunDoesNotAdvanceCheckpointOnFailedReceipt(t *testing.T) {
	s, repo := fixture()
	delete(s.receipts, "tx-0")
	r := Runner{Source: s, Repo: repo, Config: Config{StartBlock: 0, EndBlock: 0, Workers: 1}, RetryLimit: 1, RetryDelay: time.Millisecond}
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("expected receipt error")
	}
	if len(repo.blocks) != 0 {
		t.Fatalf("partial block persisted: %+v", repo.blocks)
	}
}
func TestRunResumesAtFirstMissingBlock(t *testing.T) {
	s, repo := fixture()
	repo.blocks[0] = s.blocks[0]
	r := Runner{Source: s, Repo: repo, Config: Config{StartBlock: 0, EndBlock: 2, Workers: 2}, RetryDelay: time.Millisecond}
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.blocks) != 3 {
		t.Fatalf("got %d blocks", len(repo.blocks))
	}
}
func TestRunRequiresDependencies(t *testing.T) {
	if err := (&Runner{}).Run(context.Background()); err == nil {
		t.Fatal("expected dependency validation error")
	}
}

func TestRunRejectsMalformedReceiptStatus(t *testing.T) {
	s, repo := fixture()
	rcp := s.receipts["tx-0"]
	rcp.StatusHex = "invalid"
	s.receipts["tx-0"] = rcp
	r := Runner{Source: s, Repo: repo, Config: Config{StartBlock: 0, EndBlock: 0, Workers: 1}, RetryLimit: 1}
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("expected malformed receipt status to be rejected")
	}
	if len(repo.blocks) != 0 {
		t.Fatal("should not persist malformed receipt")
	}
}
