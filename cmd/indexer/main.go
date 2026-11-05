package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"example.com/onchain-intelligence-platform/internal/indexer"
	"example.com/onchain-intelligence-platform/internal/rpcclient"
	"example.com/onchain-intelligence-platform/internal/store"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func uintEnv(key string, fallback uint64) uint64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		log.Fatalf("invalid %s: %v", key, err)
	}
	return n
}
func main() {
	rpcURL := env("RPC_URL", "")
	dbURL := env("DATABASE_URL", "")
	if rpcURL == "" || dbURL == "" {
		log.Fatal("RPC_URL and DATABASE_URL are required")
	}
	chainID := uintEnv("CHAIN_ID", 11155111)
	start := uintEnv("START_BLOCK", 0)
	end := uintEnv("END_BLOCK", 0)
	workers := int(uintEnv("WORKERS", 2))
	receiptWorkers := int(uintEnv("RECEIPT_WORKERS", 4))
	continuous := env("CONTINUOUS", "false") == "true"
	pollMS := uintEnv("POLL_INTERVAL_MS", 2000)
	lookback := uintEnv("REORG_LOOKBACK", 64)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := store.Open(ctx, dbURL, int64(chainID))
	if err != nil {
		log.Fatalf("connect database: %v", err)
	}
	defer db.Close()
	runner := indexer.Runner{Source: rpcclient.New(rpcURL), Repo: db, Config: indexer.Config{StartBlock: start, EndBlock: end, Latest: end == 0, Workers: workers}, RetryLimit: 4, RetryDelay: 250 * time.Millisecond, ReceiptWorkers: receiptWorkers}
	if continuous {
		if err = runner.RunContinuous(ctx, time.Duration(pollMS)*time.Millisecond, lookback); err != nil {
			log.Fatalf("continuous indexing failed: %v", err)
		}
		return
	}
	if err = runner.Run(ctx); err != nil {
		log.Fatalf("indexing failed: %v", err)
	}
	log.Println("indexing completed successfully")
}
