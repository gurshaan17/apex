# Apex

A low-latency exchange matching engine written in Go.

## Overview

Apex is an in-memory exchange simulator designed for speed and correctness.
The long-term goal is a full matching engine with price-time-priority order
matching, risk checks, clearing, event logging, crash recovery, and market-data
distribution.

## Current Status

**V1 - Steps 8–10: Market Orders, Engine API & Result Model, Determinism & Invariants**

The matching engine now supports both limit and market orders with price-time
priority, partial fills, and cancellation. The public API is small and
immutable-safe (callers never touch internal structures), and correctness is
backed by invariant and determinism guarantees. Transport, concurrency,
persistence, risk, clearing, and market data remain future phases.

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

## Order Book

The order book (`internal/book`) maintains resting orders across multiple
price levels with **price-time priority**: bids sorted by price descending,
asks ascending, FIFO within each price.

```
                    ORDER BOOK

 BUY / BIDS                  SELL / ASKS

 105 → A → B                106 → X
 104 → C                    107 → Y → Z
 103 → D → E                108 → W

 best bid = 105              best ask = 106
```

Key points:

- **Best bid/ask** — O(1) via the front of each side's sorted price list.
- **Order lookup/removal** — an `OrderID → {side, price, node}` index makes
  `Get`, `Remove`, and future cancellation O(1) without scanning.
- **Empty levels removed** — when the last order at a price is removed, the
  price level disappears, so best prices always reflect populated levels.
- **Snapshot** — `Snapshot()` returns a read-only, best-first view with price,
  total quantity, and FIFO orders per level; snapshot orders are copies.
- **Market orders rejected** — a market order has no resting price; execution
  is a later phase.
- **Crossed books allowed** — matching is a later phase (Step 5).

Full details: [docs/order-book.md](docs/order-book.md).

## Matching Engine

The matching engine (`internal/engine`) is everything between an order's
inbox and its resting (or terminal) state. It owns an order book and a
registry of every submitted order.

```go
e := engine.NewEngine()

res, err := e.SubmitOrder(limitOrMarketOrder) // order.Order by value
//  res: SubmitResult{ OrderID, OriginalQty, FilledQty, Remaining, Status, Trades }

err := e.CancelOrder(orderID)         // typed error for filled / cancelled / unknown
o,  err := e.GetOrder(orderID)        // copy of a submitted order (never live state)
snap   := e.GetOrderBookSnapshot()    // read-only book view (orders are copies)
err    := e.CheckInvariants()         // diagnostics: queues/indexes never exposed
```

Behavior:

- **Price-time priority** — against the best opposite price, oldest order
  first (FIFO within each price level).
- **Trade price = resting order's price** — the maker always sets the price.
- **Partial fills** — a residual limit order rests in the book with its
  original arrival time; a partially filled resting order keeps its queue
  position.
- **Multiple levels** — an aggressive order walks price levels best-first
  until filled.
- **Market orders** — consume the best available opposite-side liquidity
  across price levels at resting prices. They never rest: an unfilled
  remainder (insufficient liquidity) is cancelled, preserving the filled
  quantity. A market order against an empty side fills nothing and is
  cancelled.
- **Deterministic** — trade IDs are a monotonic counter, trades are emitted in
  execution order, and no map iteration influences priority;
  `Trade.Timestamp` is the incoming order's time, so identical input replays
  identically.
- **Error sentinels** — `ErrOrderNotFound`, `ErrAlreadyFilled`,
  `ErrAlreadyCancelled`, `ErrDuplicateOrder`, plus the order-domain
  validation errors.

### Public API & result model

The callers are kept away from all internal structures — queues, nodes,
price-level maps, and the byID index stay unexported. `SubmitOrder` takes an
`order.Order` **by value**, `GetOrder` returns a **copy**, and
`GetOrderBookSnapshot` returns **copied orders**. `SubmitResult` reports the
order ID, final status, original/filled/remaining quantities, and every trade
generated by that single submission:

```go
type SubmitResult struct {
    OrderID     order.OrderID
    OriginalQty order.Quantity
    FilledQty   order.Quantity
    Remaining   order.Quantity
    Status      order.Status
    Trades      []Trade
}

type Trade struct {
    TradeID     uint64
    Symbol      string
    BuyOrderID  order.OrderID
    SellOrderID order.OrderID
    Price       order.Price
    Qty         order.Quantity
    Timestamp   time.Time
}
```

### Determinism & invariants

`Engine.CheckInvariants()` verifies, for every submitted order:

- `filled + remaining == original quantity`
- `filled <= original quantity`
- `remaining >= 0`
- active orders (`OPEN`/`PARTIALLY_FILLED`) are present in the book exactly
  once; filled and cancelled orders are absent
- book structure (price priority, FIFO, index consistency) via
  `book.CheckInvariants()`

Multiple deterministic scenario tests replay a fixed order sequence and
assert identical trades, trade order, prices, quantities, order states, and
final book.

Full details: [docs/matching-engine.md](docs/matching-engine.md).

## V1 Progress

| #  | Phase                    | Status                     |
|----|--------------------------|----------------------------|
| 01 | Project Foundation       | ✓ Done (Step 1)            |
| 02 | Order Domain             | ✓ Done (Step 2)            |
| 03 | FIFO Queue + Price Level | ✓ Done (Step 3)            |
| 04 | Order Book               | ✓ Done (Step 4)            |
| 05 | Limit Matching           | ✓ Done (Step 5)            |
| 06 | Partial Fills            | ✓ Done (Step 6)            |
| 07 | Cancellation             | ✓ Done (Step 7)            |
| 08 | Market Orders            | ✓ Done (Step 8)            |
| 09 | Engine API & Result Model| ✓ Done (Step 9)            |
| 10 | Determinism & Invariants | ✓ Done (Step 10)           |
| 11 | Benchmarks               | Planned                    |
| 12 | Profiling + Optimization | Planned                    |

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