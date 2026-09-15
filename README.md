# Apex

A low-latency exchange matching engine written in Go.

## Overview

Apex is an in-memory exchange simulator designed for speed and correctness.
The long-term goal is a full matching engine with price-time-priority order
matching, risk checks, clearing, event logging, crash recovery, and market-data
distribution.

## Current Status

**V1 - Step 3: FIFO Queue + Price Level**

This step adds the core data structures that underpin the future order book:
an O(1)-amortised FIFO order queue backed by a doubly linked list, and a
`PriceLevel` abstraction that groups all orders resting at a single price.
All operations maintain strict time priority; cancellation of a specific order
is O(1) via a node reference (see below). Matching functionality has **not**
been implemented yet.

## Order Structure

An order carries the fields required by the future matching engine:

| Field | Type                 | Description                                   |
|-------|----------------------|-----------------------------------------------|
| `ID`  | `OrderID` (int64)    | Unique identifier                            |
| `Symbol` | `string`          | Instrument symbol (e.g. `AAPL`)              |
| `Side` | `Side` (enum)       | `BUY` or `SELL`                              |
| `Type` | `Type` (enum)       | `LIMIT` or `MARKET`                          |
| `Price` | `Price` (int64)    | Integer price in the smallest currency unit  |
| `Qty` | `Quantity` (int64)  | Integer quantity                             |
| `Filled` | `Quantity` (int64)| Quantity filled so far                       |
| `Status` | `Status` (enum)    | Lifecycle state                              |
| `Time` | `time.Time`         | Order submission time                        |

### Conventions

- **Price** — stored as an integer in the smallest currency unit (e.g.
  ₹100.25 → `10025` paise). This avoids floating-point precision problems.
- **Quantity** — integer units only; no fractional quantities in this phase.
- **Sides** — typed enum (`Buy`/`Sell`), never raw strings.
- **Types** — typed enum (`Limit`/`Market`). Market-order **matching** is a
  later phase; the type exists now as part of the domain model.
- **Statuses** — typed enum: `New`, `Open`, `PartiallyFilled`, `Filled`,
  `Cancelled`.

## Lifecycle

```
New
 ↓
Open
 ├──→ PartiallyFilled
 │          ↓
 │       Filled
 │
 ├──→ Filled
 │
 └──→ Cancelled
```

Rules:

- An order is created in `NEW` and must transition to `OPEN` before it can be filled.
- Fills move an order to `PartiallyFilled` or straight to `Filled`.
- `Open` and `PartiallyFilled` orders can be cancelled.
- `Filled` and `Cancelled` orders are terminal: no further fills or cancellations.

## Validation Rules

An order is valid only if all of the following hold:

- `ID != 0`
- `Symbol` is non-empty
- `Side` is `Buy` or `Sell`
- `Type` is `Limit` or `Market`
- `Qty > 0`
- `Limit` orders require `Price > 0`
- `Market` orders must have `Price == 0` (no limit price)

Rejected orders return typed, sentinel errors:

| Error                | Meaning                                |
|----------------------|----------------------------------------|
| `ErrInvalidOrderID`  | Zero order ID                          |
| `ErrEmptySymbol`     | Empty symbol                           |
| `ErrInvalidSide`     | Unknown side                           |
| `ErrInvalidType`     | Unknown order type                     |
| `ErrInvalidQuantity` | Quantity ≤ 0, or fill with quantity ≤ 0 |
| `ErrInvalidPrice`    | Limit order with price ≤ 0             |
| `ErrMarketPriceSet`  | Market order carrying a price          |
| `ErrOverfill`        | Fill exceeds remaining quantity        |
| `ErrInvalidState`    | Illegal lifecycle transition or fill   |
| `ErrCannotCancel`    | Order not cancellable in current state |

## Fill Invariants

A fill is applied via `Fill(qty)` and must satisfy:

```
filled + remaining == original quantity
filled ≤ original quantity
quantity > 0
```

Fills that would violate these invariants return an error and leave the order
unmodified. Applying a fill never uses floating-point arithmetic.

## Domain Methods

| Method                | Purpose                                          |
|-----------------------|--------------------------------------------------|
| `NewOrder(...)`       | Construct a `NEW` order                          |
| `Validate()`          | Validate all required fields                     |
| `Open()`              | Transition `NEW → OPEN`                          |
| `Fill(qty)`           | Apply a fill, updating filled/remaining/status   |
| `Remaining()`         | Return `Qty - Filled`                            |
| `IsFullyFilled()`     | Whether the order has been completely filled     |
| `CanCancel()`         | Whether the order is in a cancellable state      |
| `Cancel()`            | Transition `Open/PartiallyFilled → Cancelled`    |

## FIFO Order Queue & Price Level

### Why FIFO

Price-time-priority matching requires that, at each price level, orders are
served in the exact order they arrived. A sell order that can trade at ₹100
must fill the first resting buy order at ₹100 before the second, regardless
of quantity or any other attribute.

### Data structure

The queue is a **doubly linked list** (`internal/book`). A doubly linked
list was chosen because:

- Removing an order from the middle (cancellation) is O(1) when the caller
  holds the `*Node` returned by `Push`.
- Append (new order), peek (best order at a level), and pop (consume first
  order) are all O(1).
- The future order book can maintain an `OrderID → *Node` map, making
  cancel-by-ID O(1) without scanning.

### Operation complexity

| Operation        | Complexity | Notes                                   |
|------------------|------------|-----------------------------------------|
| `Push`           | O(1)       | Append to tail; returns `*Node` handle  |
| `Front` / `Peek` | O(1)      | Dereference head                        |
| `Pop`            | O(1)       | Unlink head                             |
| `Remove(node)`   | O(1)       | Direct reference + owner check          |
| `Len`            | O(1)       | Maintained counter                      |
| `IsEmpty`        | O(1)       | `len == 0`                              |

### PriceLevel

A `PriceLevel` binds a price to an `OrderQueue`. All orders in the level
must share exactly that price; adding an order with a different price is
rejected with `ErrWrongPrice`. Market orders (price 0) are also rejected
at any positive price level.

### Invariants

- `Len >= 0`; `Len == 0` iff `Head == nil` and `Tail == nil`.
- `Head.prev == nil` and `Tail.next == nil` always.
- Traversal from `Head` to `Tail` visits exactly `Len` distinct nodes: no
  cycles, no orphans.
- Every node in the queue is owned by that queue and carries a non-nil order.
- In a `PriceLevel`, every order's price equals the level's price.

Full complexity analysis and design rationale are in
[docs/data-structures.md](docs/data-structures.md).

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
- **Error handling** — Return errors explicitly with sentinel `errors.New`
  values, matched via `errors.Is`. Do not panic in library code.
- **Domain isolation** — Keep domain logic independent from transport and storage
  layers. Packages under `internal/` should not depend on specific protocols or
  databases.
- **Numerics** — Prices and quantities are integers only. No floats in trading
  domain code.

## License

Not yet determined.