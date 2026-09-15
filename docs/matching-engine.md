# Matching Engine

This document describes the matching engine implemented in Steps 5–7. It
coordinates the order book from Step 4 to execute limit orders with
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
        trades generated at            any remaining qty rests
        resting order's price          via book.Place
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
   matches when `sell price <= best bid`. If the incoming order does not cross
   the best opposite price (or the opposite side is empty), matching stops.
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

## Order lifecycle

Only these transitions can be produced by the engine (enforced by the order
domain):

- `NEW → OPEN`            — on `SubmitOrder`
- `OPEN → PARTIALLY_FILLED / FILLED / CANCELLED`
- `PARTIALLY_FILLED → PARTIALLY_FILLED / FILLED / CANCELLED`

The engine owns every submitted `*order.Order` in a registry (`Engine.orders`)
and hands the same pointer to the book, so the book, the registry, and `GetOrder`
always agree on status and quantities.

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

## Timestamps and determinism

- A resting order keeps the `Time` it was submitted with.
- `Trade.Timestamp` is the **incoming order's `Time`**, not an engine-side
  wall clock. The engine performs **no** time mutation.

Given identical input (same order values, including `Time`), the engine
produces byte-identical output: trade IDs are a monotonic counter, trades are
emitted in execution order, and no map iteration influences priority. A
`TestDeterminism` test replays a scenario twice and compares full results.

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
  value, so callers cannot mutate a resting order by accident through their own
  copy. The outcome is delivered through `SubmitResult` and `GetOrder`.
- **Explicit errors, not result structs.** The spec sketches
  `CancelOrder → CancelResult`; this Go implementation returns `error` (typed,
  comparable with `errors.Is`) instead, which is the idiomatic form.
- **Registry over book-only lookup.** The book's byID index forgets filled and
  cancelled orders; the engine registry remembers them so the three error cases
  (`unknown`, `already filled`, `already cancelled`) are distinguishable.
- **Trade timestamp from the order, not `time.Now()`.** The engine stays free
  of a clock so replays are deterministic; only callers can introduce time.

## Out of scope (later phases)

Market orders, order cancellation round-trips through a transport layer,
locking/concurrency, persistence, trade/clearing, risk, and market-data
publication are explicitly deferred.