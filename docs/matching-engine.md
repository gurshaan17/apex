# Matching Engine

This document describes the matching engine implemented in Steps 5–10. It
coordinates the order book from Step 4 to execute limit and market orders with
price-time priority, generate trades, handle partial fills, and support
cancellation. It lives in [`internal/engine`](../internal/engine).

## Overview

An order goes through exactly one `SubmitOrder` call:

```
        ┌─────────────┐   validate   ┌──────────────┐
incoming│  SubmitOrder │─────────────▶│  Open (NEW→OPEN) │
        └──────┬──────┘              └──────┬───────┘
               │                            │
               ▼                            ▼
        match against opposite side    while remaining > 0
        (best opposite order first)        │
               │                            │
               ▼                            ▼
        trades generated at            limit: residual rests
        resting order's price          market: residual cancelled
               │                            │
               ▼                            ▼
        SubmitResult                    book snapshot
```

The engine is **single-symbol by design**: it holds one book and matches all of
its orders against each other. Callers are responsible for keeping symbols
consistent; the engine does not enforce it.

## Matching algorithm

For each order, while the order has remaining quantity:

1. **Crossing check.** A BUY matches when `buy price >= best ask`; a SELL
   matches when `sell price <= best bid`. Market orders always cross: they
   have no limit price, so the check is skipped. If the incoming order does
   not cross the best opposite price (or the opposite side is empty),
   matching stops.
2. **Pick the resting order.** The engine takes the front order at the best
   opposite price (`book.FrontOrder`). Because each price level is a strict
   FIFO queue, this is the *oldest* order at the *best* price.
3. **Size the fill.** `qty = min(incoming remaining, resting remaining)`.
4. **Apply the fill to both orders** via `order.Fill`, which updates `Filled`
   and transitions status (OPEN/PARTIALLY_FILLED → PARTIALLY_FILLED/FILLED).
5. **Record the trade.** Execution price is the **resting order's price**.
6. **Sweep.** A fully filled resting order is removed from the book; a
   partially filled one keeps its position at the head of its queue.

The loop repeats, re-reading the best opposite price each time, until the
incoming order is fully filled, no executable liquidity remains, or its price
no longer crosses.

The engine never walks the book's internal structures directly: it only uses
`BestAsk`/`BestBid`, `FrontOrder`, `Remove`, and `Place`. This keeps locking
and correctness reasoning local to the OrderBook.

## Price-time priority

Priority is enforced by two existing structures plus the engine's selection
order:

- **Price priority**: the engine always matches against `BestAsk` (for BUYs)
  or `BestBid` (for SELLs), so a more aggressive price is always consumed
  before a less aggressive one.
- **Time priority**: within a price level, the FIFO queue returns the oldest
  order first. The engine consumes order A before B before C at the same
  price, regardless of quantity.

Priority is unaffected by partial fills and cancellations: a partially filled
order retains its position, and a cancelled order is simply unlinked, so the
remaining orders keep their relative order.

## Trade price rule

Every trade executes at the **resting order's price**, never the aggressor's:

- Resting SELL `100` @ `100`, incoming BUY `100` @ `105` → trade at `100`.
- Resting BUY `104` @ `104`, incoming SELL `40` @ `100` → trade at `104`.

This is the conventional maker-taker rule: the resting (maker) order's price
is the transaction price.

## Full fills

When `qty >= incoming remaining`, the incoming order is fully consumed and
transitions to `FILLED`. It never rests. When `qty >= resting remaining`, the
resting order is fully consumed and removed from the book.

Example (exact match):

```
BUY  100 @ 100   → rests
SELL 100 @ 100   → 1 trade 100 @ 100; book empty; both FILLED
```

## Partial fills

When `qty <` one side's remaining quantity, that side becomes
`PARTIALLY_FILLED`.

- If the **resting** order is only partially filled, it stays at the head of
  its FIFO queue with its reduced `Remaining()` and keeps its time priority.
- If the **incoming** order is only partially filled, what is left over rests
  in the book via `Place`, inserted at the tail of its price level as a new
  order with its **original arrival time** unchanged.

Example 1 — incoming smaller:

```
SELL 100 @ 100
BUY   40 @ 100   → trade 40 @ 100; SELL stays rest, PARTIAL 40/60
```

Example 2 — incoming larger:

```
SELL 40 @ 100
BUY 100 @ 100    → trade 40 @ 100; SELL FILLED; BUY rests 60 @ 100, PARTIAL
```

## Multiple price-level execution

An aggressive order walks through price levels best-first:

```
SELL 50  @ 100
SELL 100 @ 101
SELL 200 @ 102
BUY 250 @ 102  → trades 50@100, 100@101, 100@102; SELL 100 @102 remains
```

The book never contains a crossed state after a submit: any crossing quantity
is consumed before the residual rests.

## Market orders

A `MARKET` order (price `0`, validated by the order domain) consumes available
opposite-side liquidity starting at the best price and walking across levels
until it is fully filled or liquidity runs out. All trades execute at the
resting order's price.

```
ASKS
100 → 50
101 → 100
102 → 200

MARKET BUY 120  →  50 @ 100, 70 @ 101
```

Key rules:

- **No resting.** A market order never enters the book. If it fills exactly,
  its status is `FILLED`.
- **Insufficient liquidity.** If the market order consumes every opposite-side
  order and still has quantity left, the **unfilled remainder is cancelled**
  (`CANCELLED`), preserving the filled quantity. This is deterministic and
  documented; the market order never transitions to `PARTIALLY_FILLED` as a
  resting state.
- **Empty side.** A market order with no opposite liquidity fills nothing and
  is cancelled (`CANCELLED`, filled `0`).
- **No floats.** Both fill sizes and prices stay integers throughout.

The available-liquidity behavior applies to both directions:

```
SELL 50 @ 100
SELL 100 @ 101
MARKET BUY 120  →  trades 50 @ 100, 70 @ 101; SELL 30 @ 101 remains

BUY  50 @ 102
BUY  100 @ 101
MARKET SELL 120 →  trades 50 @ 102, 70 @ 101; BUY 30 @ 101 remains
```

## Public API & result model

The public surface is intentionally small:

| Operation            | Signature                                   | Notes                              |
|----------------------|---------------------------------------------|------------------------------------|
| `SubmitOrder`        | `(order.Order) (SubmitResult, error)`       | Order taken **by value**           |
| `CancelOrder`        | `(order.OrderID) error`                     | Typed errors for bad targets       |
| `GetOrderBookSnapshot` | `() book.Snapshot`                        | Read-only, orders are copies       |
| `GetOrder`           | `(order.OrderID) (*order.Order, error)`     | Convenience; returns a **copy**    |
| `CheckInvariants`    | `() error`                                  | Diagnostics (Step 10)              |

Callers never receive internal structures: queues, nodes, the price-level
maps, and the byID index are all unexported, and `SubmitOrder` copies the
order at the boundary. `SubmitResult` reports everything a caller needs about
one submission — order ID, final status, original/filled/remaining quantities,
and the trades generated:

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

Errors are returned explicitly as typed sentinels matched with `errors.Is`. The
API depends only on the `order`, `book`, and `time` packages — no networking,
transport, or persistence types leak in.

## Order lifecycle

Only these transitions can be produced by the engine (enforced by the order
domain):

- `NEW → OPEN`            — on `SubmitOrder`
- `OPEN → PARTIALLY_FILLED / FILLED / CANCELLED`
- `PARTIALLY_FILLED → PARTIALLY_FILLED / FILLED / CANCELLED`

The engine owns every submitted `*order.Order` in a registry (`Engine.orders`)
and hands the same pointer to the book internally. Externally, `GetOrder`
returns a copy, so outside observers can only read state.

## Cancellation

`CancelOrder(id)`:

1. If the ID is unknown → `ErrOrderNotFound`.
2. If the order is `FILLED` → `ErrAlreadyFilled` (do not throw away executed
   quantity); `CANCELLED` → `ErrAlreadyCancelled`; `NEW` →
   `order.ErrCannotCancel` (an order in flight inside a submit is never exposed
   to cancellation).
3. Otherwise the order is resting: remove it from the book (`book.Remove`,
   O(1) via the byID index) and transition it to `CANCELLED`.

A cancelled order keeps its executor side — the cancelled quantity is dropped,
never credited back as liquidity.

Example — cancel a partially filled order:

```
SELL 100 @ 100
BUY   40 @ 100   → SELL rest, PARTIAL 40/60
CancelOrder(SELL) → CANCELLED, filled 40, remaining 60; book SELL side empty
```

## Determinism

- A resting order keeps the `Time` it was submitted with.
- `Trade.Timestamp` is the **incoming order's `Time`**, not an engine-side
  wall clock. The engine performs **no** time mutation.

Given identical input (same order values, including `Time`), the engine
produces identical output: trade IDs are a monotonic counter, trades are
emitted in execution order, and **no Go map iteration anywhere influences
priority or matching** (books use linked-oriented price lists and FIFO queues;
the registry is only used for O(1) lookup). Deterministic scenario tests replay
a fixed sequence — including limits, markets, partial fills, and cancellations
— twice and compare the full trade list, every order state, cancellation
results, and the final book.

## Invariants

`Engine.CheckInvariants()` verifies, for every submitted order:

- `filled + remaining == original quantity`
- `filled <= original quantity`
- `remaining >= 0`
- resting orders have positive remaining
- active orders (`OPEN`/`PARTIALLY_FILLED`) are present in the book exactly
  once (and never duplicated across levels)
- filled and cancelled orders are absent from the book
- registry key ↔ order identity consistency

It delegates book-structure checks (price priority ordering, FIFO queue
integrity, byID index ↔ level agreement, no empty levels) to
`book.CheckInvariants()`. Production paths never pay this cost; tests and
diagnostics use it, including corruption-detection tests that break an
invariant and assert the checker catches it.

## Data structures & complexity

The engine adds two pieces of state on top of the Step 4 book:

```
Engine.orders     map[order.OrderID]*order.Order   — live view of every submitted order
Engine.nextTrade  uint64                            — monotonic trade ID
```

| Operation     | Complexity                  | Notes                          |
|---------------|-----------------------------|--------------------------------|
| `SubmitOrder` | O(k) where k = matches      | each match is O(1) book work   |
| `CancelOrder` | O(1)                        | index lookup + unlink + status |
| `GetOrder`    | O(1)                        | index lookup                   |
| `Snapshot`    | O(n)                        | n = resting orders             |

Within matching, every step is O(1): `BestAsk`/`BestBid` read the front of a
sorted list; `FrontOrder` reads the head of a queue; `Remove` unlinks a node
held in the byID index. The only non-O(1) book operation is inserting a
brand-new price level, which is O(P) and rare.

`Engine.orders` grows with every submitted order (active or terminal). That is
a deliberate cost so cancellation can distinguish `FILLED`/`CANCELLED`/unknown
IDs in O(1); a persistence phase can later move terminal history out of
memory.

## Book primitives added in this step

`OrderBook` exposes two focused primitives for the engine (see
`internal/book/place.go`):

- `Place(o)` — rests an already-OPEN or PARTIALLY_FILLED order at its price
  level without changing status. `Add` (Step 4) is reserved for NEW orders;
  `Add` would reject a partially filled order.
- `FrontOrder(side, price)` — returns the oldest order resting at a price
  without removing it.

## Design decisions & tradeoffs

- **Registry = pointer.** The registry and the book share the same order
  pointer, so every structure observing an order sees identical state with no
  copying. Ownership of all orders is the engine's.
- **Value semantics at the boundary.** `SubmitOrder` takes an `order.Order` by
  value, `GetOrder` returns a copy, and snapshots copy every order, so callers
  cannot mutate resting state through their own handle. The engine pays the
  copy cost at the API edge and keeps exclusive ownership inside.
- **Market-order remainder → CANCELLED.** Instead of resting (impossible for
  a price-less order) or silently dropping fills, an unfilled remainder is
  explicitly cancelled so quantity accounting stays exact and observable.
- **Explicit errors, not result structs.** The spec sketches
  `CancelOrder → CancelResult`; this Go implementation returns `error` (typed,
  comparable with `errors.Is`) instead, which is the idiomatic form.
- **Registry over book-only lookup.** The book's byID index forgets filled and
  cancelled orders; the engine registry remembers them so the three error cases
  (`unknown`, `already filled`, `already cancelled`) are distinguishable.
- **Trade timestamp from the order, not `time.Now()`.** The engine stays free
  of a clock so replays are deterministic; only callers can introduce time.

## Out of scope (later phases)

Order cancellation round-trips through a transport layer, locking/concurrency,
persistence, trade/clearing, risk, and market-data publication are explicitly
deferred.