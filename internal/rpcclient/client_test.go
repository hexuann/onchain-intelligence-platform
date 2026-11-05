package rpcclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRPCMethodsDecodeJSONRPC(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q request
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch q.Method {
		case "eth_blockNumber":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": "0xa"})
		case "eth_getBlockByNumber":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": map[string]any{"hash": "0xblock", "parentHash": "0xparent", "timestamp": "0x65a00000", "transactions": []any{map[string]any{"hash": "0xtx", "from": "0xfrom", "to": "0xto", "value": "0x1", "gas": "0x5208", "gasPrice": "0x2"}}}})
		case "eth_getTransactionReceipt":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": map[string]any{"transactionHash": "0xtx", "status": "0x1", "logs": []any{map[string]any{"address": "0xcontract", "topics": []string{"0xtopic"}, "data": "0x", "logIndex": "0x0"}}}})
		default:
			t.Errorf("unexpected method %s", q.Method)
		}
	}))
	defer server.Close()
	c := New(server.URL)
	ctx := context.Background()
	latest, err := c.LatestBlock(ctx)
	if err != nil || latest != 10 {
		t.Fatalf("LatestBlock=%d, err=%v", latest, err)
	}
	b, err := c.BlockWithTransactions(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if b.Number != 7 || len(b.Transactions) != 1 || b.Transactions[0].BlockNum != 7 {
		t.Fatalf("unexpected block: %+v", b)
	}
	rec, err := c.Receipt(ctx, "0xtx")
	if err != nil {
		t.Fatal(err)
	}
	if rec.StatusHex != "0x1" || len(rec.Logs) != 1 || rec.Logs[0].Index != 0 {
		t.Fatalf("unexpected receipt: %+v", rec)
	}
}
func TestRPCErrorPropagates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": -32000, "message": "rate limited"}})
	}))
	defer server.Close()
	if _, err := New(server.URL).LatestBlock(context.Background()); err == nil {
		t.Fatal("expected RPC error")
	}
}
