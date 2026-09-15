# Order Book

This document describes the order book implemented in Step 4. It maintains
resting orders across multiple price levels on both sides with price-time
priority, but does **not** perform matching yet.

## Architecture

```
                    ORDER BOOK

 BUY / BIDS                  SELL / ASKS

 105 → A → B                106 → X
 104 → C                    107 → Y → Z
 103 → D → E                108 → W

 best bid = 105              best ask = 106
 (highest price)             (lowest price)
```

- **Bids** are ordered by price descending: a higher price is a better price.
- **Asks** are ordered by price ascending: a lower price is a better price.
- Within a price level, orders are in strict FIFO insertion order.

## Representation

Each side of the book (`bookSide`) maintains:

```
levels: map[order.Price]*PriceLevel   — O(1) lookup of a price level
prices: *list.List                     — price levels sorted best-first
elems:  map[order.Price]*list.Element  — price → its position in the sorted list
```

`PriceLevel` (Step 3) holds the FIFO `OrderQueue` of orders at that price.

The whole book also maintains an order index:

```
byID: map[order.OrderID]orderLocation
orderLocation = { side, price, node }
```

The index lets any order in the book be located and removed in O(1) without
scanning price levels.

## Best Bid / Best Ask

`BestBid()` returns the front of the bid price list (highest). `BestAsk()`
returns the front of the ask price list (lowest). Both are O(1). An empty side
returns price `0`, which is never a valid resting price.

## Price level lifecycle

- **Insert**: a new price creates a `PriceLevel`, inserts it into the sorted
  list at the correct position (bids: before the first lower price; asks:
  before the first higher price), and records its list element.
- **Remove**: when the last order at a price is removed, the price level is
  dropped from the map and the sorted list. Empty price levels never remain,
  so `BestBid`/`BestAsk` always reflect only populated levels.

## Operations

| Operation     | Complexity | Notes                                        |
|---------------|------------|----------------------------------------------|
| `Add(o)`      | O(1)       | Market orders rejected; NEW → OPEN transition|
| `Remove(id)`  | O(1)       | Via the byID index + node reference          |
| `Get(id)`     | O(1)       | Via the byID index                           |
| `BestBid()`   | O(1)       | Front of sorted bid list                     |
| `BestAsk()`   | O(1)       | Front of sorted ask list                     |
| `Snapshot()`  | O(n)       | n = resting orders                           |
| Insert a new price level | O(P) | P = distinct prices on that side            |
| `Len()`       | O(1)       | Number of resting orders                     |

The `byID` index stores `{side, price, node}` — exactly what future phases
need to cancel an order (Step 7) or consume it during matching (Step 5)
without any scanning.

## Invariants

Verified by `checkInvariants` (used by tests):

1. **Bid ordering**: the bid price list is strictly descending.
2. **Ask ordering**: the ask price list is strictly ascending.
3. **Price-level consistency**: every order in a level carries that level's
   price.
4. **Order uniqueness**: an order ID exists at exactly one location;
   `byID` and the level queues agree on every order.
5. **No empty levels**: every level in the book is non-empty; removed levels
   disappear from all bookkeeping.
6. **Quantity**: level quantities are sums of remaining quantities and are
   never negative.
7. **Crossing allowed**: the book does not reject or match crossed books
   (`Bid 101` with `Ask 100` resting at once). Matching is Step 5.

## Snapshot

`Snapshot()` returns a read-only view:

```go
type LevelSnapshot struct {
    Price  order.Price
    Qty    order.Quantity    // total resting quantity at the level
    Orders []*order.Order    // orders in FIFO order (copies)
}
type Snapshot struct {
    Bids []LevelSnapshot
    Asks []LevelSnapshot
}
```

Each side is best-first. Orders in the snapshot are copies, so mutating a
snapshot can never corrupt the book.

## Design decisions & tradeoffs

- **Sorted linked list of prices** (via `container/list`) gives O(1)
  `BestBid`/`BestAsk` and O(1) price-level removal, and O(P) insertion of a
  brand-new price. A heap would give O(log P) insertion but O(P) (or lazy)
  removal of an arbitrary price; a balanced tree would give O(log P) for both
  at the cost of hand-rolled complexity. For this codebase the sorted list is
  the simplest structure that keeps all hot paths (best price, add, remove,
  lookup) at O(1) while price insertion is rare relative to order churn.
- **The byID index stores the node**, not just the order, so a future
  cancellation is a map lookup followed by an O(1) unlink — no price-level
  scan, no ordering reshuffle.
- **`Add` rejects market orders**: a market order has no resting price, so it
  cannot occupy a price level. Market-order handling is deferred to a later
  phase.
- **`Add` transitions NEW → OPEN**: an order is considered OPEN (accepted into
  the book) once it is resting. Removing an order from the book does **not**
  change its status; cancellation semantics are a later phase.

## Out of scope (later phases)

Matching, trade generation, partial fills, cancellation status transitions,
and market orders are intentionally not part of this step.