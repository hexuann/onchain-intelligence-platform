package store

import (
	"context"
	"os"
	"testing"
	"time"

	"example.com/onchain-intelligence-platform/internal/indexer"
)

// Set TEST_DATABASE_URL to run against a real PostgreSQL instance. The test creates isolated tables in a transaction-like temporary schema.
func TestPostgresSaveAndCheckpoint(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration test")
	}
	ctx := context.Background()
	p, err := Open(ctx, url, 987654321)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	_, err = p.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS blocks(chain_id BIGINT NOT NULL,number BIGINT NOT NULL,hash TEXT NOT NULL,parent_hash TEXT NOT NULL,timestamp TIMESTAMPTZ NOT NULL,PRIMARY KEY(chain_id,number)); CREATE TABLE IF NOT EXISTS transactions(chain_id BIGINT NOT NULL,hash TEXT NOT NULL,block_number BIGINT NOT NULL,from_address TEXT NOT NULL,to_address TEXT,value NUMERIC(78,0) NOT NULL DEFAULT 0,gas NUMERIC(78,0) NOT NULL DEFAULT 0,gas_price NUMERIC(78,0) NOT NULL DEFAULT 0,status BIGINT NOT NULL DEFAULT 0,PRIMARY KEY(chain_id,hash),FOREIGN KEY(chain_id,block_number) REFERENCES blocks(chain_id,number) ON DELETE CASCADE); CREATE TABLE IF NOT EXISTS event_logs(chain_id BIGINT NOT NULL,tx_hash TEXT NOT NULL,log_index BIGINT NOT NULL,address TEXT NOT NULL,topics TEXT[] NOT NULL DEFAULT '{}',data TEXT NOT NULL,block_number BIGINT NOT NULL,PRIMARY KEY(chain_id,tx_hash,log_index),FOREIGN KEY(chain_id,tx_hash) REFERENCES transactions(chain_id,hash) ON DELETE CASCADE)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = p.Pool.Exec(ctx, `DELETE FROM event_logs WHERE chain_id=$1; DELETE FROM transactions WHERE chain_id=$1; DELETE FROM blocks WHERE chain_id=$1`, p.ChainID)
	}()
	b := indexer.Block{Number: 991, Hash: "integration-block", ParentHash: "parent", TimestampHex: time.Now().Format("2006-01-02"), Transactions: []indexer.Transaction{{Hash: "integration-tx", From: "0xfrom", To: "0xto", Value: "0x1", Gas: "0x5208", GasPrice: "0x2", Status: 1, Logs: []indexer.Log{{Address: "0xcafe", Topics: []string{"0xabc"}, Data: "0x1234", Index: 0}}}}}
	// Store expects an Ethereum hex timestamp; use a known Unix timestamp.
	b.TimestampHex = "0x65a00000"
	if err := p.SaveBlock(ctx, b); err != nil {
		t.Fatal(err)
	}
	next, err := p.Checkpoint(ctx, 991)
	if err != nil {
		t.Fatal(err)
	}
	if next != 992 {
		t.Fatalf("checkpoint=%d, want 992", next)
	}
	var txCount, logCount int
	if err = p.Pool.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE chain_id=$1 AND hash=$2`, p.ChainID, "integration-tx").Scan(&txCount); err != nil {
		t.Fatal(err)
	}
	if err = p.Pool.QueryRow(ctx, `SELECT count(*) FROM event_logs WHERE chain_id=$1 AND tx_hash=$2`, p.ChainID, "integration-tx").Scan(&logCount); err != nil {
		t.Fatal(err)
	}
	if txCount != 1 || logCount != 1 {
		t.Fatalf("transactions=%d logs=%d", txCount, logCount)
	}
	// A reorg rollback deletes the canonical block and all dependent rows.
	if err := p.RollbackFrom(ctx, 991); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"blocks", "transactions", "event_logs"} {
		var count int
		// Table names are fixed, controlled test identifiers.
		if err := p.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE chain_id=$1", p.ChainID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s count after reorg rollback=%d; want zero", table, count)
		}
	}
	b.Hash = "integration-replacement-block"
	if err := p.SaveBlock(ctx, b); err != nil {
		t.Fatalf("save replacement block: %v", err)
	}

}
