package indexer

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// ChainRepository supports canonical-chain verification and transactional rollback.
// A production continuous sync must use this interface, not just Repository.
type ChainRepository interface {
	Repository
	Tip(context.Context) (uint64, string, bool, error)
	BlockHash(context.Context, uint64) (string, bool, error)
	RollbackFrom(context.Context, uint64) error
}

// RunContinuous syncs new blocks until ctx is canceled. It checks the current
// canonical head on each poll, including when there are no new blocks.
// ReorgLookback limits the maximum rollback distance and prevents silent loss
// of history when the common ancestor cannot be established.
func (r *Runner) RunContinuous(ctx context.Context, pollInterval time.Duration, reorgLookback uint64) error {
	if r.Source == nil || r.Repo == nil {
		return fmt.Errorf("source and repository are required")
	}
	repo, ok := r.Repo.(ChainRepository)
	if !ok {
		return fmt.Errorf("continuous sync requires a reorg-aware repository")
	}
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	if reorgLookback == 0 {
		reorgLookback = 64
	}
	if err := r.syncOnce(ctx, repo, reorgLookback); err != nil {
		return err
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := r.syncOnce(ctx, repo, reorgLookback); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				log.Printf("sync cycle failed (retry next poll): %v", err)
			}
		}
	}
}

func (r *Runner) syncOnce(ctx context.Context, repo ChainRepository, lookback uint64) error {
	latest, err := r.Source.LatestBlock(ctx)
	if err != nil {
		return fmt.Errorf("read chain head: %w", err)
	}
	height, _, exists, err := repo.Tip(ctx)
	if err != nil {
		return err
	}
	if exists {
		compare := height
		if latest < compare {
			compare = latest
		}
		// Validate overlap even if height has not advanced: this catches reorgs at the tip.
		localHash, found, err := repo.BlockHash(ctx, compare)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("missing stored block at height %d; repair gap before continuous sync", compare)
		}
		remote, err := r.Source.BlockWithTransactions(ctx, compare)
		if err != nil {
			return err
		}
		if strings.EqualFold(localHash, remote.Hash) && height > latest {
			// The RPC head moved behind our stored tip. Keep the matching
			// height and roll back only the blocks beyond the current head.
			if height-latest > lookback {
				return fmt.Errorf("stored tip exceeds RPC head by %d blocks (lookback=%d)", height-latest, lookback)
			}
			if err := repo.RollbackFrom(ctx, latest+1); err != nil {
				return err
			}
			log.Printf("RPC head moved backward: rolled back from height %d", latest+1)
		} else if !strings.EqualFold(localHash, remote.Hash) {
			// Find a common ancestor within the configured bound, without touching data
			// when the depth is too large or the node is inconsistent.
			cursor := compare
			foundAncestor := false
			for traversed := uint64(0); traversed < lookback; traversed++ {
				if cursor == 0 {
					break
				}
				cursor--
				stored, ok, e := repo.BlockHash(ctx, cursor)
				if e != nil {
					return e
				}
				if !ok {
					return fmt.Errorf("missing stored ancestor %d", cursor)
				}
				upstream, e := r.Source.BlockWithTransactions(ctx, cursor)
				if e != nil {
					return e
				}
				if strings.EqualFold(stored, upstream.Hash) {
					foundAncestor = true
					break
				}
			}
			if !foundAncestor {
				return fmt.Errorf("reorg exceeds lookback=%d or ancestor unavailable; refusing destructive rollback", lookback)
			}
			rollbackHeight := cursor + 1
			if err := repo.RollbackFrom(ctx, rollbackHeight); err != nil {
				return fmt.Errorf("rollback from %d: %w", rollbackHeight, err)
			}
			log.Printf("reorg detected: rolled back from height %d", rollbackHeight)
		}
	}
	// Run uses the repository checkpoint and preserves ordered transactional saves.
	// Fetch the current canonical head after reconciliation. Parent validation is
	// enforced by the PostgreSQL store's SaveBlock method.
	next, err := repo.Checkpoint(ctx, r.Config.StartBlock)
	if err != nil {
		return err
	}
	if next > latest {
		return nil
	}
	run := *r
	run.Config.StartBlock = next
	run.Config.EndBlock = latest
	run.Config.Latest = false
	if err := run.Run(ctx); err != nil {
		return err
	}
	return nil
}
