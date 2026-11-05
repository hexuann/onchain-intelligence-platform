package indexer

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Runner struct {
	Source         Source
	Repo           Repository
	Config         Config
	RetryLimit     int
	RetryDelay     time.Duration
	ReceiptWorkers int
}
type result struct {
	number uint64
	block  Block
	err    error
}

func (r *Runner) retry(ctx context.Context, fn func() error) error {
	limit := r.RetryLimit
	if limit <= 0 {
		limit = 3
	}
	delay := r.RetryDelay
	if delay <= 0 {
		delay = 100 * time.Millisecond
	}
	var err error
	for attempt := 0; attempt < limit; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		if attempt+1 < limit {
			timer := time.NewTimer(delay * time.Duration(1<<attempt))
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return err
}
func (r *Runner) fetch(ctx context.Context, n uint64) (Block, error) {
	var b Block
	if err := r.retry(ctx, func() error { var e error; b, e = r.Source.BlockWithTransactions(ctx, n); return e }); err != nil {
		return b, fmt.Errorf("fetch block %d: %w", n, err)
	}
	// A bounded worker pool fetches receipts concurrently without unbounded goroutines.
	receiptWorkers := r.ReceiptWorkers
	if receiptWorkers <= 0 {
		receiptWorkers = 4
	}
	if receiptWorkers > 16 {
		receiptWorkers = 16
	}
	if receiptWorkers > len(b.Transactions) {
		receiptWorkers = len(b.Transactions)
	}
	if receiptWorkers == 0 {
		return b, nil
	}
	receiptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error
	for w := 0; w < receiptWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				tx := &b.Transactions[i]
				var rec Receipt
				err := r.retry(receiptCtx, func() error {
					var e error
					rec, e = r.Source.Receipt(receiptCtx, tx.Hash)
					return e
				})
				if err == nil && !strings.EqualFold(rec.TransactionHash, tx.Hash) {
					err = fmt.Errorf("receipt transaction hash mismatch: requested %s, received %s", tx.Hash, rec.TransactionHash)
				}
				var status uint64
				if err == nil {
					status, err = parseHex(rec.StatusHex)
				}
				if err != nil {
					once.Do(func() { firstErr = fmt.Errorf("receipt for %s: %w", tx.Hash, err); cancel() })
					continue
				}
				tx.BlockNum = n
				tx.Status = status
				tx.Logs = rec.Logs
			}
		}()
	}
dispatch:
	for i := range b.Transactions {
		select {
		case <-receiptCtx.Done():
			break dispatch
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return b, firstErr
	}
	if err := ctx.Err(); err != nil {
		return b, err
	}

	return b, nil
}
func (r *Runner) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if r.Source == nil || r.Repo == nil {
		return fmt.Errorf("source and repository are required")
	}
	workers := r.Config.Workers
	if workers <= 0 {
		workers = 4
	}
	end := r.Config.EndBlock
	if r.Config.Latest {
		var err error
		end, err = r.Source.LatestBlock(ctx)
		if err != nil {
			return err
		}
	}
	start := r.Config.StartBlock
	nextBlock, err := r.Repo.Checkpoint(ctx, start)
	if err != nil {
		return fmt.Errorf("read checkpoint: %w", err)
	}
	if nextBlock > start {
		start = nextBlock
	}
	if start > end {
		return nil
	}
	jobs := make(chan uint64)
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				b, e := r.fetch(ctx, n)
				select {
				case results <- result{number: n, block: b, err: e}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for n := start; n <= end; n++ {
			select {
			case jobs <- n:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	pending := make(map[uint64]result)
	next := start
	var first error
	for res := range results {
		pending[res.number] = res
		for {
			item, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			if item.err != nil {
				if first == nil {
					first = item.err
				}
				cancel()
				return first
			}
			if err := r.Repo.SaveBlock(ctx, item.block); err != nil {
				return fmt.Errorf("save block %d: %w", next, err)
			}
			next++
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if next <= end {
		if first != nil {
			return first
		}
		return fmt.Errorf("incomplete run: next block %d of %d", next, end)
	}
	return nil
}
func parseHex(s string) (uint64, error) {
	if !strings.HasPrefix(s, "0x") || len(s) < 3 {
		return 0, fmt.Errorf("invalid hex quantity %q", s)
	}
	return strconv.ParseUint(s[2:], 16, 64)
}
