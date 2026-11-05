// api 服务入口：复用项目一数据库，提供查询接口与资金流分析任务接口。
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"example.com/onchain-intelligence-platform/internal/analysis"
	"example.com/onchain-intelligence-platform/internal/api"
	"github.com/jackc/pgx/v5/pgxpool"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func main() {
	dbURL := env("DATABASE_URL", "")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	chainID, err := strconv.ParseInt(env("CHAIN_ID", "11155111"), 10, 64)
	if err != nil {
		log.Fatalf("invalid CHAIN_ID: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("create database pool: %v", err)
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		log.Fatalf("connect database: %v", err)
	}
	defer pool.Close()
	analyzer := &analysis.Analyzer{DB: pool}
	app := &api.Server{DB: pool, Analyzer: analyzer, DefaultChainID: chainID}
	srv := &http.Server{Addr: env("HTTP_ADDR", ":8080"), Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Printf("API listening on %s (default chain_id=%d)", srv.Addr, chainID)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server: %v", err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown: %v", err)
	}
}
