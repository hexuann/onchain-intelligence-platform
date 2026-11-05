# Test Report

This report separates **verified tests in the current revision** from historical local observations and checks that require a network/database environment. It does not claim production latency measurements or full live-Sepolia validation.

## Current revision — 2026-11-05

| Check | Status | Evidence / constraint |
| --- | --- | --- |
| Go formatting (`gofmt`) | PASS | Reformatted modified Go source files |
| Indexer logic + new reorg/concurrency tests (`go test -race` with explicit package files) | PASS | Local test execution; see command below |
| Entire repository (`go test ./...`) | NOT RUN SUCCESSFULLY | `pgx/v5` missing from local module cache; sandbox cannot resolve external Go module proxy |
| PostgreSQL integration | NOT RUN | No accessible running PostgreSQL test instance in this environment |
| Live Sepolia reorg simulation | NOT RUN | Requires a controllable RPC node and real chain data |
| End-to-end latency / throughput benchmark | NOT MEASURED | Depends on RPC provider, data volume and database |

### Verified race-enabled indexer tests

From the repository root:

```bash
go test -race \
  internal/indexer/runner.go \
  internal/indexer/types.go \
  internal/indexer/continuous.go \
  internal/indexer/runner_test.go \
  internal/indexer/continuous_test.go
```

Observed result:

```text
ok   command-line-arguments   1.080s
```

Tests exercised: transactional-save orchestration with in-memory repository, retries, malformed receipt handling, resumable checkpoint behavior, bounded concurrent receipt requests, same-height head reorganization, deep-reorg refusal, and context-controlled continuous sync shutdown. **This test isolates the indexer package and does not validate the real PostgreSQL implementation.**

### Full repository test blocker

`go test ./...` failed before compiling the application because `github.com/jackc/pgx/v5` checksum entries were absent and dependencies were not installed. Attempting `go mod tidy` could not resolve the Go module proxy (`proxy.golang.org` DNS/network access denied). This is an **environment/dependency validation blocker**, not evidence that all application packages compile successfully. Run the following on a connected machine:

```bash
go mod tidy
go test ./...
go test -race ./...
go vet ./...
```

Commit the generated `go.sum`, and verify all test suites before tagging a release.

## Historical testnet observations (previous development session)

The earlier project version was exercised against Ubuntu 24.04 / WSL2, PostgreSQL 16 and Sepolia (`chain_id=11155111`). These results describe a **previous revision** and must not be presented as fresh test results of the updated continuous indexer.

| Metric | Previously recorded |
| --- | ---: |
| Example block range | 11954648–11954650 |
| Stored blocks | 3 |
| Stored transactions | 449 |
| Stored event logs | 517 |

Previous API exercise: the async address analysis endpoint returned `202 Accepted`; querying the task returned `200 OK` with `completed`. One address-flow example contained no native-coin edges, because the implementation did not yet decode ERC-20 Transfer events into graph edges. A historical `/healthz` request also timed out once; no SLA was established.

## Repeatable verification on your machine

1. Export a valid `DATABASE_URL`, `RPC_URL`, and `CHAIN_ID=11155111`.
2. Run migrations only against a compatible database. Use a **separate test database** for integration tests.
3. Run the full Go suite and `go test -race ./...`; record toolchain version and results.
4. Import a short known block range. Compare stored block hashes and counts to the RPC node.
5. Start the indexer with `CONTINUOUS=true`, `POLL_INTERVAL_MS=2000`, `WORKERS=2`, `RECEIPT_WORKERS=4` and confirm the tip moves when a new Sepolia block arrives.
6. Test reorg handling using a deterministic mock node, or a disposable test chain. Verify old transactions and logs are deleted with their blocks, and the canonical replacement is ingested.
7. Benchmark response latency **separately** for RPC head polling, block+receipt retrieval, database commit, and HTTP API queries; record p50/p95/p99, workload size and hardware before claiming performance targets.

## Known limitations and follow-up

- Full integration and performance testing must be rerun with a reachable Go module proxy and PostgreSQL database.
- The current design intentionally rejects reorgs deeper than the configured ancestor-search window.
- Old historical gaps are not automatically discovered or repaired.
- Multiple writers to the same chain are not coordinated with advisory locks.
- Token-based fund-flow decoding and internal traces are not yet implemented.
