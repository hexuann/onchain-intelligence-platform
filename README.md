# On-chain Intelligence Platform

Go-based Ethereum JSON-RPC indexer and PostgreSQL-backed analysis API. The default deployment targets the **Sepolia testnet** (`chain_id=11155111`). It can fetch blocks, transactions, receipts and event logs, continuously follow the canonical head, and query stored data through an HTTP API.

## Features

- **Continuous indexing:** poll `eth_blockNumber` every two seconds by default and resume from the PostgreSQL checkpoint.
- **Bounded concurrency:** parallel block fetching and parallel transaction-receipt requests; block writes remain ordered.
- **Chain reorganization recovery:** compare the stored tip with the RPC node, locate a common ancestor within a configurable window, then roll back affected blocks, transactions, and logs in a PostgreSQL transaction.
- **Storage integrity:** per-chain keys, foreign keys, indexes, and transactional inserts.
- **Query/analysis:** REST endpoints for blocks, transactions, address history, stats, and asynchronous native-ETH address-flow analysis.

**Scope:** This is a testnet-oriented reference implementation, not an audited production Ethereum indexer. The analysis graph currently covers direct native-ETH value transfers; ERC-20 event-based token flows and internal traces are not modeled. Reorganizations deeper than `REORG_LOOKBACK` are rejected instead of silently deleting data. An RPC node is the authority for the canonical chain; use a trusted, consistent node.

## Architecture

```text
Ethereum Sepolia JSON-RPC
  ├── eth_blockNumber (head polling)
  ├── eth_getBlockByNumber (parallel block workers)
  └── eth_getTransactionReceipt (bounded receipt workers per block)
                 │
          internal/indexer
      ordered, transactional writes
                 │
             PostgreSQL
   blocks → transactions → event_logs
                 │
      HTTP API / CLI analyzer
```

Reorg checks run even when the head height has not advanced. On a mismatch, the indexer searches backward for a common ancestor and removes blocks above it. PostgreSQL `ON DELETE CASCADE` removes associated transactions/logs. The next sync cycle refetches canonical data. Concurrent runs targeting the **same chain/database are not supported**; run one writer per chain.

## Requirements

- Go 1.22 or newer
- PostgreSQL 16 (other recent PostgreSQL releases may work but are not verified here)
- A reachable Sepolia Ethereum JSON-RPC HTTP endpoint
- Linux/Ubuntu 24.04, including WSL2; Docker is not required

For memory-constrained environments (around 4 GB RAM or less), start with `WORKERS=2` and `RECEIPT_WORKERS=4`. Provider rate limits may require lower values.

## Quick start

### 1. Configure the database

```bash
sudo service postgresql start
pg_isready
sudo -u postgres psql
```

For a *new* database only, execute in `psql`:

```sql
CREATE ROLE chainindexer LOGIN PASSWORD 'replace_with_a_strong_password';
CREATE DATABASE chainindexer OWNER chainindexer;
\q
```

Set environment variables in your terminal (the application **does not load `.env` automatically**):

```bash
export DATABASE_URL='postgres://chainindexer:YOUR_PASSWORD@localhost:5432/chainindexer?sslmode=disable'
export RPC_URL='https://YOUR_SEPOLIA_RPC_ENDPOINT'
export CHAIN_ID=11155111
export WORKERS=2
export RECEIPT_WORKERS=4
```

Apply migrations (idempotent table/index creation; they do not reset the database):

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/001_init.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/002_analysis.sql
```

**Existing database:** back it up before schema changes or live indexing. Do not drop your existing `chainindexer` database. Run the migrations only if it uses the schema described here; arbitrary external schemas require an adapter.

### 2. Dependencies and tests

```bash
go mod tidy
go test ./...
go test -race ./...
```

Network access is needed the first time Go downloads `github.com/jackc/pgx/v5` and its dependencies. Commit the generated `go.sum` alongside `go.mod` when preparing your GitHub repository.

### 3. Continuous syncing (recommended)

For an existing database, the indexer resumes at its last stored block. For a new database, **set `START_BLOCK` explicitly** to a recent height so you do not inadvertently backfill the entire testnet:

```bash
export START_BLOCK=YOUR_CHOSEN_SEPOLIA_BLOCK_HEIGHT
export CONTINUOUS=true
export END_BLOCK=0
export POLL_INTERVAL_MS=2000
export REORG_LOOKBACK=64
go run ./cmd/indexer
```

With `CONTINUOUS=true`, `END_BLOCK` is ignored and the process follows new block heights until Ctrl+C/SIGTERM. In one-shot mode (`CONTINUOUS=false`, default), `END_BLOCK=0` means the current head when the run starts. Stop any other writer using the same chain tables before starting the continuous process.

**Continuity assumptions:** the stored block sequence must be contiguous near its tip; the indexer does not automatically repair old gaps. Parent hashes are checked when writing blocks. Blocks at the first imported height may have no locally stored parent. A deep reorg or a missing ancestor requires manual intervention and will not trigger an unbounded rollback.

### 4. Start the API

In a separate terminal, export `DATABASE_URL` and run:

```bash
go run ./cmd/api
curl -i http://localhost:8080/healthz
curl -i 'http://localhost:8080/api/v1/stats?chain_id=11155111'
```

Examples:

```bash
# Block and transaction lookup
curl 'http://localhost:8080/api/v1/blocks/11954648?chain_id=11155111'

# Asynchronous address-flow analysis (returns a task ID)
curl -X POST 'http://localhost:8080/api/v1/analysis/address-flow' \
  -H 'Content-Type: application/json' \
  -d '{"chain_id":11155111,"address":"0x090685e7f4665e9680c5b12f3c5adb0ba674ed53","depth":2}'

# Replace 1 with the returned task ID
curl 'http://localhost:8080/api/v1/analysis/tasks/1'
```

The main API routes are `GET /healthz`, `GET /api/v1/chains`, `GET /api/v1/blocks/{number}`, `GET /api/v1/transactions/{hash}`, `GET /api/v1/addresses/{address}/...`, `GET /api/v1/stats`, `POST /api/v1/analysis/address-flow`, and `GET /api/v1/analysis/tasks/{id}`. See `internal/api/server.go` for exact query parameters.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | required | PostgreSQL connection string |
| `RPC_URL` | required for indexer | Sepolia JSON-RPC HTTP endpoint |
| `CHAIN_ID` | `11155111` | Network/database chain identifier |
| `START_BLOCK` | `0` | Minimum block height when beginning ingestion |
| `END_BLOCK` | `0` | One-shot ending height; zero means current head |
| `CONTINUOUS` | `false` | Enable continuous polling and reorg recovery |
| `POLL_INTERVAL_MS` | `2000` | Time between sync checks |
| `WORKERS` | `2` | Concurrent block-fetch workers |
| `RECEIPT_WORKERS` | `4` | Concurrent receipts **per block**; capped at 16 |
| `REORG_LOOKBACK` | `64` | Maximum ancestor-search steps on reorg |
| `HTTP_ADDR` | `:8080` | API listen address |

The upper bound for simultaneous receipt RPC calls is approximately `WORKERS × RECEIPT_WORKERS`. Choose values within your node provider's request quota.

## Database schema

- `blocks(chain_id, number)` — primary key; block hash, parent hash and timestamp.
- `transactions(chain_id, hash)` — primary key; references a block and stores native value, gas and status.
- `event_logs(chain_id, tx_hash, log_index)` — primary key; references a transaction and stores contract address, topics and data.
- `analysis_tasks` — asynchronous analysis task persistence.

`SaveBlock` writes a full block and its derived records in one transaction. Different block hashes at the same height are rejected, rather than overwriting canonical data without a rollback. When the tip diverges from the RPC node, `RollbackFrom` performs a transaction-scoped delete from the divergence height onward; cascading foreign keys remove dependent rows.

## Testing

Run unit tests without a database using Go, or run the PostgreSQL integration suite with an isolated test database:

```bash
export TEST_DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/TEST_DATABASE?sslmode=disable'
go test ./internal/store -run TestPostgres -v
```

The integration tests create/verify shared table names in the specified database and use a dedicated test chain ID. **Do not point `TEST_DATABASE_URL` at a database containing valuable data without reviewing the tests.** See [TEST_REPORT.md](TEST_REPORT.md) for verified results, commands, and test limitations.

## Operational limits

- Polling every two seconds means the indexer can *detect* fresh blocks shortly after they become available, not guarantee finality or a two-second end-to-end response. Actual delay depends on RPC latency, receipt count, rate limits and database throughput.
- Reorg detection is at block granularity. Deep reorgs beyond the configured window fail safely and require operator attention.
- This implementation is single-writer; multiple indexer processes for the same chain may conflict.
- The analysis output excludes ERC-20 Transfer transfers and internal execution traces.
- Large backfills and provider-specific rate limiting may need adaptive request throttling, batch RPC, and additional observability.

## Repository layout

```text
cmd/                   CLI binaries (indexer, api, analyzer)
internal/indexer/      block orchestration, bounded receipts, reorg polling
internal/rpcclient/    Ethereum JSON-RPC client
internal/store/        PostgreSQL store and integration tests
internal/api/          HTTP handlers
internal/analysis/     native-value address-flow traversal
migrations/            SQL DDL
scripts/               one-shot resume helper
.github/workflows/      Go checks on GitHub Actions
TEST_REPORT.md         test evidence and limitations
```

## Security

Never commit RPC API keys, DB credentials, `.env` files or full database dumps. Use limited-access credentials and private local services in development. The repository's Go module path is currently the placeholder `example.com/onchain-intelligence-platform`; replace it and its imports when assigning your final GitHub repository URL.

## License

No license has been selected. Add a `LICENSE` file if you intend to grant others permission to reuse the code.
