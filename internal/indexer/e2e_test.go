package indexer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"example.com/onchain-intelligence-platform/internal/indexer"
	"example.com/onchain-intelligence-platform/internal/rpcclient"
)

type repo struct{ blocks map[uint64]indexer.Block }

func (r *repo) Checkpoint(_ context.Context, from uint64) (uint64, error) {
	n := from
	for {
		if _, ok := r.blocks[n]; !ok {
			return n, nil
		}
		n++
	}
}
func (r *repo) SaveBlock(_ context.Context, b indexer.Block) error {
	r.blocks[b.Number] = b
	return nil
}
func TestEndToEndMockJSONRPCToRepository(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var q struct {
			ID     int               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(req.Body).Decode(&q); err != nil {
			t.Fatal(err)
		}
		var result any
		switch q.Method {
		case "eth_blockNumber":
			result = "0x2"
		case "eth_getBlockByNumber":
			var num string
			_ = json.Unmarshal(q.Params[0], &num)
			result = map[string]any{"hash": fmt.Sprintf("block-%s", num), "parentHash": "parent", "timestamp": "0x65a00000", "transactions": []any{map[string]any{"hash": fmt.Sprintf("tx-%s", num), "from": "0xfrom", "to": "0xto", "value": "0x2a", "gas": "0x5208", "gasPrice": "0x3"}}}
		case "eth_getTransactionReceipt":
			var hash string
			_ = json.Unmarshal(q.Params[0], &hash)
			result = map[string]any{"transactionHash": hash, "status": "0x1", "logs": []any{map[string]any{"address": "0xcafe", "topics": []string{"0xabc"}, "data": "0x1234", "logIndex": "0x0"}}}
		default:
			t.Errorf("unexpected RPC method %s", q.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": result})
	}))
	defer srv.Close()
	database := &repo{blocks: map[uint64]indexer.Block{}}
	runner := indexer.Runner{Source: rpcclient.New(srv.URL), Repo: database, Config: indexer.Config{StartBlock: 0, EndBlock: 2, Workers: 2}, RetryDelay: time.Millisecond}
	if err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(database.blocks) != 3 {
		t.Fatalf("saved %d blocks, want 3", len(database.blocks))
	}
	for n, b := range database.blocks {
		if len(b.Transactions) != 1 {
			t.Fatalf("block %d tx count=%d", n, len(b.Transactions))
		}
		tx := b.Transactions[0]
		if tx.Status != 1 || len(tx.Logs) != 1 || tx.Logs[0].Data != "0x1234" {
			t.Fatalf("block %d not fully parsed: %+v", n, tx)
		}
	}
}
