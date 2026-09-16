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
prices: []order.Price                  — distinct prices sorted best-first
better: func(a, b) bool                — strict total order (bids: a>b, asks: a<b)
```

`prices[0]` is always the best price, `prices[1]` the next, and so on, so
bids are descending and asks ascending. `PriceLevel` (Step 3) holds the FIFO
`OrderQueue` of orders at that price.

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

- **Insert**: a new price creates a `PriceLevel`, finds its position with a
  binary search (`sort.Search` over the `better` predicate, O(log P)), and
  shifts the slice to make room (a single cache-friendly `memmove`, O(P)
  worst case).
- **Remove**: when the last order at a price is removed, the price level is
  dropped from the map and removed from the slice at its found position.
  Empty price levels never remain, so `BestBid`/`BestAsk` always reflect only
  populated levels.

## Operations

| Operation     | Complexity | Notes                                        |
|---------------|------------|----------------------------------------------|
| `Add(o)`      | O(1) amortized | Market orders rejected; NEW → OPEN transition |
| `Remove(id)`  | O(1)       | Via the byID index + node reference          |
| `Get(id)`     | O(1)       | Via the byID index                           |
| `BestBid()`   | O(1)       | `prices[0]` of the bid side                  |
| `BestAsk()`   | O(1)       | `prices[0]` of the ask side                  |
| `Snapshot()`  | O(n)       | n = resting orders                           |
| Insert a new price level | O(log P) comparisons + O(P) shift | P = distinct prices on that side |
| Remove a price level | O(log P) comparisons + O(P) shift | P = distinct prices on that side |
| `Len()`       | O(1)       | Number of resting orders

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

- **Sorted slice of prices instead of a sorted linked list.** The original
  implementation kept prices in a `container/list` sorted list plus a
  `map[price]*list.Element` index. Inserting a **new** price scanned the list
  from the front comparing every existing price (O(P)) — a linked list cannot
  be binary-searched, so every new level paid a full pointer-chasing walk.
  The V1 benchmark `BenchmarkSubmitNonMatchingNewLevel` exposed this as a
  ~270× slowdown versus submitting onto an existing price level (~114 µs vs
  ~0.4 µs per submission).

  The replacement keeps the `levels` map (O(1) level lookup, unchanged) and
  stores the distinct prices in a sort-maintained `[]order.Price`, best-first.
  Finding an insert/remove position is now a binary search (`sort.Search`,
  O(log P)); the subsequent in-place shift is a single contiguous `memmove`
  (O(P) worst case but a few cache-friendly nanoseconds for realistic books).
  This makes `BestBid`/`BestAsk` a plain `prices[0]` (O(1), unchanged),
  keeps snapshots best-first, and removes the `elems` map and per-element list
  books and allocations (the `insertPrice` path drops from 6 to 4 allocations).

  Tradeoff: removing an empty price level went from an O(1) list unlink to
  O(log P) search + O(P) shift. The shift is shared, contiguous memory rather
  than scattered heap nodes, and in every interleaved A/B benchmark round the
  cancellation, matching, snapshot, and invariant-check paths were unchanged
  within measurement noise. Alternatives considered, and rejected:

  - **Balanced tree / skip list** would give O(log P) insert *and* delete but
    requires hand-rolled code, allocates a node per level, and makes
    best-price access O(log P) unless a separate cached pointer is kept.
  - **Heap with lazy deletion** would give O(1) best and O(log P) insert, but
    deleting an arbitrary price is O(P) and tombstoned prices break the
    "no empty levels in the book" invariant.
  - **Lazy sorted views (`maps.Sorted` on demand)** keeps the store simple but
    makes every snapshot O(P log P) and loses O(1) best-price access.

  For a book where distinct prices (P) are far smaller than resting order
  count (n), the sorted slice is the simplest structure that keeps the hot
  matching paths (best price, lookup, add, remove-by-id) at O(1) with
  comparison-free O(P) worst-case shuffling on the rare new-level path.
- **The byID index stores the node**, not just the order, so a future
  cancellation is a map lookup followed by an O(1) unlink — no price-level
  scan, no ordering reshuffle.
- **`Add` rejects market orders**: a market order has no resting price, so it
  cannot occupy a price level. Market-order handling is deferred to a later
  phase.
- **`Add` transitions NEW → OPEN**: an order is considered OPEN (accepted into
  the book) once it is resting. Removing an order from the book does **not**
  change its status; cancellation semantics are a later phase.

## Performance investigation: new price-level insertion

During V1 benchmarking, `BenchmarkSubmitNonMatchingNewLevel` was ~270× slower
than `BenchmarkSubmitNonMatching` (~114 µs vs ~0.4 µs). This was investigated
and fixed before V1 was considered complete.

### Root cause

`bookSide.insertPrice` walked the sorted linked list of prices from the front
(the `container/list` implementation), comparing `price` against every resting
price until it found the correct slot. Because a linked list has no random
access, a brand-new price level cost O(P) pointer-chasing comparisons, and in
the benchmark's degenerate pattern (each new ask price is worse than all
existing ones) the walk reached the end of the list on every insert.

### Original approach

```
levels: map[order.Price]*PriceLevel   — O(1) lookup
prices: *list.List                     — sorted best-first
elems:  map[order.Price]*list.Element  — price → its list element
```

| Operation | Complexity |
|-----------|------------|
| New price level insert | O(P) comparisons, pointer chasing |
| Price level remove     | O(1) via stored `*list.Element` |
| `BestBid` / `BestAsk`  | O(1) `list.Front()` |
| Price level lookup     | O(1) via `levels` map |

### New approach

```
levels: map[order.Price]*PriceLevel   — O(1) lookup
prices: []order.Price                  — sorted best-first (bids: descending, asks: ascending)
```

`sort.Search` over the `better` predicate locates any price with a binary
search; insertion and removal then shift the slice in place.

| Operation | Complexity |
|-----------|------------|
| New price level insert | O(log P) comparisons + O(P) `memmove` shift |
| Price level remove     | O(log P) comparisons + O(P) `memmove` shift |
| `BestBid` / `BestAsk`  | O(1) `prices[0]` |
| Price level lookup     | O(1) via `levels` map |

### Benchmark comparison

Engine benchmarks (`go test -bench=. -benchmem ./...`, Apple M2), interleaved
new-vs-baseline over three rounds to control for machine load drift. ns/op;
"baseline" is the linked-list implementation, "new" is the sorted slice.

| Benchmark | baseline | new | change |
|-----------|---------:|-----|-------:|
| SubmitNonMatching (existing level) | 114,229 | 616 | **~185× faster** |
| SubmitNonMatching (existing level, R2) | 106,297 | 782 | **~136× faster** |
| SubmitNonMatching (existing level, R3) | 100,219 | 796 | **~126× faster** |
| SubmitNonMatching (same price) | 544 | 447 | within noise |
| SubmitImmediateMatch | 1212 | 646 | within noise |
| SubmitPartialFill | 623 | 588 | within noise |
| SubmitMultiLevelMatch | 2664 | 1892 | within noise |
| CancelOrder | 629 | 446 | within noise |
| LargeOrderStream (500 orders) | 159,701 | 101,247 | within noise |
| GetOrderBookSnapshot | 252,761 | 208,914 | within noise |
| CheckInvariants | 1,594,224 | 1,285,931 | within noise |

The new-level insert allocation count dropped from 6 to 4 (the `elems` map
entries and list elements are gone), and the mixed-order stream dropped from
1680 to 1371 allocations/op. No benchmark showed a consistent regression
across all three rounds: the remaining differences are within the machine's
measurement noise, which spanned ±50% on the larger snapshot/invariant
benchmarks between rounds.

## Out of scope (later phases)

Matching, trade generation, partial fills, cancellation status transitions,
and market orders are intentionally not part of this step.