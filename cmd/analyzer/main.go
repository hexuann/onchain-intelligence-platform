// analyzer 是可选的命令行分析入口，便于不经过 HTTP API 单独验证资金流分析。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"example.com/onchain-intelligence-platform/internal/analysis"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	chainID := flag.Int64("chain-id", 11155111, "链 ID，默认 Sepolia")
	address := flag.String("address", "", "需要分析的起始地址")
	depth := flag.Int("depth", 1, "分析深度，范围 1-5")
	flag.Parse()
	if *address == "" {
		log.Fatal("-address is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	result, err := (&analysis.Analyzer{DB: pool}).Run(ctx, *chainID, *address, *depth)
	if err != nil {
		log.Fatal(err)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(data))
}
