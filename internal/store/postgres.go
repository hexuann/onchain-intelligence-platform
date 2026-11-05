package store

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"example.com/onchain-intelligence-platform/internal/indexer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Postgres struct {
	Pool    *pgxpool.Pool
	ChainID int64
}

func Open(ctx context.Context, url string, chainID int64) (*Postgres, error) {
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return &Postgres{Pool: p, ChainID: chainID}, nil
}
func (p *Postgres) Close() { p.Pool.Close() }
func (p *Postgres) Checkpoint(ctx context.Context, from uint64) (uint64, error) {
	var n int64
	err := p.Pool.QueryRow(ctx, `SELECT COALESCE(MAX(number) + 1, $2::bigint) FROM blocks WHERE chain_id=$1`, p.ChainID, int64(from)).Scan(&n)
	if err != nil {
		return 0, err
	}
	return uint64(n), nil
}

// Tip returns the highest stored block for this chain.
func (p *Postgres) Tip(ctx context.Context) (uint64, string, bool, error) {
	var number int64
	var hash string
	err := p.Pool.QueryRow(ctx, `SELECT number,hash FROM blocks WHERE chain_id=$1 ORDER BY number DESC LIMIT 1`, p.ChainID).Scan(&number, &hash)
	if err == pgx.ErrNoRows {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	return uint64(number), hash, true, nil
}
func (p *Postgres) BlockHash(ctx context.Context, n uint64) (string, bool, error) {
	var hash string
	err := p.Pool.QueryRow(ctx, `SELECT hash FROM blocks WHERE chain_id=$1 AND number=$2`, p.ChainID, int64(n)).Scan(&hash)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	return hash, err == nil, err
}
func (p *Postgres) RollbackFrom(ctx context.Context, n uint64) error {
	// Cascading FKs delete transactions and event_logs together.
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `DELETE FROM blocks WHERE chain_id=$1 AND number >= $2`, p.ChainID, int64(n)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func decimalQuantity(s string) (string, error) {
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return "0", nil
	}
	n, ok := new(big.Int).SetString(s, 16)
	if !ok {
		return "", fmt.Errorf("invalid hex quantity %q", s)
	}
	return n.String(), nil
}
func (p *Postgres) SaveBlock(ctx context.Context, b indexer.Block) error {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var timestamp int64
	if len(b.TimestampHex) > 2 {
		_, err = fmt.Sscanf(b.TimestampHex, "0x%x", &timestamp)
		if err != nil {
			return fmt.Errorf("parse block timestamp: %w", err)
		}
	}
	// Never replace a conflicting canonical block silently.
	var storedHash string
	e := tx.QueryRow(ctx, `SELECT hash FROM blocks WHERE chain_id=$1 AND number=$2 FOR UPDATE`, p.ChainID, int64(b.Number)).Scan(&storedHash)
	if e != nil && e != pgx.ErrNoRows {
		return e
	}
	if e == nil && !strings.EqualFold(storedHash, b.Hash) {
		return fmt.Errorf("block %d hash conflict: rollback required", b.Number)
	}
	if b.Number > 0 {
		var parent string
		e = tx.QueryRow(ctx, `SELECT hash FROM blocks WHERE chain_id=$1 AND number=$2`, p.ChainID, int64(b.Number-1)).Scan(&parent)
		if e != nil && e != pgx.ErrNoRows {
			return e
		}
		if e == nil && !strings.EqualFold(parent, b.ParentHash) {
			return fmt.Errorf("block %d parent hash mismatch: reorg or stale RPC response", b.Number)
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO blocks(chain_id,number,hash,parent_hash,timestamp) VALUES($1,$2,$3,$4,to_timestamp($5)) ON CONFLICT(chain_id,number) DO UPDATE SET hash=EXCLUDED.hash,parent_hash=EXCLUDED.parent_hash,timestamp=EXCLUDED.timestamp`, p.ChainID, int64(b.Number), b.Hash, b.ParentHash, timestamp)
	if err != nil {
		return err
	}
	for _, t := range b.Transactions {
		value, e := decimalQuantity(t.Value)
		if e != nil {
			return e
		}
		gas, e := decimalQuantity(t.Gas)
		if e != nil {
			return e
		}
		gasPrice, e := decimalQuantity(t.GasPrice)
		if e != nil {
			return e
		}
		_, err = tx.Exec(ctx, `INSERT INTO transactions(chain_id,hash,block_number,from_address,to_address,value,gas,gas_price,status) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8,$9) ON CONFLICT(chain_id,hash) DO UPDATE SET block_number=EXCLUDED.block_number,from_address=EXCLUDED.from_address,to_address=EXCLUDED.to_address,value=EXCLUDED.value,gas=EXCLUDED.gas,gas_price=EXCLUDED.gas_price,status=EXCLUDED.status`, p.ChainID, t.Hash, int64(b.Number), t.From, t.To, value, gas, gasPrice, int64(t.Status))
		if err != nil {
			return err
		}
		for _, l := range t.Logs {
			topics := l.Topics
			_, err = tx.Exec(ctx, `INSERT INTO event_logs(chain_id,tx_hash,log_index,address,topics,data,block_number) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(chain_id,tx_hash,log_index) DO UPDATE SET address=EXCLUDED.address,topics=EXCLUDED.topics,data=EXCLUDED.data,block_number=EXCLUDED.block_number`, p.ChainID, t.Hash, int64(l.Index), l.Address, topics, l.Data, int64(b.Number))
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}
