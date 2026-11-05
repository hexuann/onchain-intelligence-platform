package rpcclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"example.com/onchain-intelligence-platform/internal/indexer"
)

type Client struct {
	URL  string
	HTTP *http.Client
}

func New(url string) *Client { return &Client{URL: url, HTTP: &http.Client{Timeout: 15 * time.Second}} }

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}
type response struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) call(ctx context.Context, method string, params []any, out any) error {
	body, err := json.Marshal(request{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := h.Do(req)
	if err != nil {
		return fmt.Errorf("rpc %s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("rpc %s HTTP %d: %s", method, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var envelope response
	if err = json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode rpc %s: %w", method, err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("rpc %s error %d: %s", method, envelope.Error.Code, envelope.Error.Message)
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return fmt.Errorf("rpc %s returned null", method)
	}
	if err = json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("decode rpc %s result: %w", method, err)
	}
	return nil
}
func quantity(s string) (uint64, error) {
	n, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid hex quantity %q: %w", s, err)
	}
	return n, nil
}
func hexNum(n uint64) string { return fmt.Sprintf("0x%x", n) }
func (c *Client) LatestBlock(ctx context.Context) (uint64, error) {
	var s string
	if err := c.call(ctx, "eth_blockNumber", []any{}, &s); err != nil {
		return 0, err
	}
	return quantity(s)
}
func (c *Client) BlockWithTransactions(ctx context.Context, n uint64) (indexer.Block, error) {
	var b indexer.Block
	if err := c.call(ctx, "eth_getBlockByNumber", []any{hexNum(n), true}, &b); err != nil {
		return b, err
	}
	parsed, err := quantity(b.TimestampHex)
	if err != nil {
		return b, err
	}
	_ = parsed
	b.Number = n
	for i := range b.Transactions {
		b.Transactions[i].BlockNum = n
	}
	return b, nil
}
func (c *Client) Receipt(ctx context.Context, hash string) (indexer.Receipt, error) {
	var r indexer.Receipt
	err := c.call(ctx, "eth_getTransactionReceipt", []any{hash}, &r)
	return r, err
}
