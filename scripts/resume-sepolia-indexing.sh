#!/usr/bin/env bash
# 从现有 PostgreSQL 检查点继续采集 Sepolia 区块。
# 先下载 Go 依赖并运行测试；测试成功后才启动采集器。
set -Eeuo pipefail

: "${DATABASE_URL:?请先设置 DATABASE_URL，指向已有采集数据的 PostgreSQL 数据库}"
: "${RPC_URL:?请先设置 RPC_URL，指向可用的 Ethereum Sepolia JSON-RPC 节点}"

export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export CHAIN_ID="${CHAIN_ID:-11155111}"
export WORKERS="${WORKERS:-2}"
export END_BLOCK="${END_BLOCK:-0}" # 0 表示采集到 RPC 节点当前最新区块

if [[ ! "$CHAIN_ID" =~ ^[0-9]+$ ]]; then
  echo "CHAIN_ID 必须是数字" >&2
  exit 2
fi

# 项目一的迁移脚本使用 CREATE TABLE IF NOT EXISTS，不会清空现有区块数据。
if command -v psql >/dev/null 2>&1; then
  echo "[1/4] 检查并补齐项目一所需的数据库表..."
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/001_init.sql
  latest_block="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atqc \
    "SELECT COALESCE(MAX(number), -1) FROM blocks WHERE chain_id = ${CHAIN_ID}")"
  if [[ "$latest_block" == "-1" && -z "${START_BLOCK:-}" ]]; then
    echo "数据库中没有该链的区块。为避免意外从创世区块开始长时间采集，请先显式设置 START_BLOCK。" >&2
    exit 2
  fi
  if [[ -z "${START_BLOCK:-}" ]]; then
    export START_BLOCK="$((latest_block + 1))"
  fi
else
  if [[ -z "${START_BLOCK:-}" ]]; then
    echo "未找到 psql，且未设置 START_BLOCK；为避免从创世区块意外开始采集，请显式设置 START_BLOCK。" >&2
    exit 2
  fi
  echo "未找到 psql，跳过自动数据库迁移；请确认 blocks、transactions、event_logs 表已存在。"
fi

export START_BLOCK

echo "[2/4] 使用 GOPROXY=$GOPROXY 下载依赖..."
go mod download github.com/jackc/pgx/v5@v5.7.2
go mod tidy

echo "[3/4] 运行 Go 测试..."
go test ./...

echo "[4/4] 从数据库检查点继续采集。CHAIN_ID=$CHAIN_ID START_BLOCK=$START_BLOCK END_BLOCK=$END_BLOCK WORKERS=$WORKERS"
go run ./cmd/indexer
