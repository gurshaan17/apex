# Apex

A low-latency exchange matching engine written in Go.

## Overview

Apex is an in-memory exchange simulator designed for speed and correctness.
The long-term goal is a full matching engine with price-time-priority order
matching, risk checks, clearing, event logging, crash recovery, and market-data
distribution.

## Current Status

**V1 - Step 1: Project Foundation**

This step establishes the repository structure, build tooling, and documentation.
Matching functionality has **not** been implemented yet.

## Planned V1 Phases

| #  | Phase                        |
|----|------------------------------|
| 01 | Project Foundation           |
| 02 | Order Domain                 |
| 03 | FIFO Queue + Price Level     |
| 04 | Order Book                   |
| 05 | Limit Matching               |
| 06 | Partial Fills                |
| 07 | Cancellation                 |
| 08 | Market Orders                |
| 09 | Engine API                   |
| 10 | Invariants + Determinism     |
| 11 | Benchmarks                   |
| 12 | Profiling + Optimization     |

## Development Commands

```bash
# Run all tests
go test ./...

# Vet all packages
go vet ./...

# Run the application
go run ./cmd/matching-engine

# Format all Go files
gofmt -w .

# Makefile shortcuts
make test
make vet
make run
make check
```

## Engineering Conventions

- **Formatting** — All Go code must be formatted with `gofmt`.
- **Testing** — Tests live alongside the code they test (unit tests) or under
  `tests/` (integration tests). Use `go test` and the standard `testing` package.
- **Error handling** — Return errors explicitly. Do not panic in library code.
- **Domain isolation** — Keep domain logic independent from transport and storage
  layers. Packages under `internal/` should not depend on specific protocols or
  databases.

## License

Not yet determined.
