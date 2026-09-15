# Data Structures

This document describes the FIFO order queue and price level implemented in
Step 3, and the reasoning behind the design.

## Why FIFO

Price-time-priority matching requires that, at a given price, orders are
served strictly in the order they arrived. Consider three resting buy orders at
price 100:

```
Head
 ↓
Order A → Order B → Order C
                         ↑
                        Tail
```

A sell order that can trade at 100 must fill A first, then B, then C. When a
new order D arrives at price 100 it must join at the tail, behind C. This
ordering guarantees determinism: the same sequence of incoming orders always
produces the same sequence of trades.

## Chosen data structure: doubly linked list

The queue is a doubly linked list of `Node` values with `Head`, and `Tail`
pointers and a `len` counter.

A doubly linked list was chosen over an array/slice because:

1. **Cancel-from-middle is the hard requirement.** Orders resting at a price
   are frequently cancelled. With a slice, removing an element from the middle
   is O(n) (shift) or requires tombstones. With a doubly linked list plus a
   node reference, removal is O(1).
2. **Append/front/pop are the hot matching-engine operations** and are O(1) in
   a linked list, same as a slice queue.
3. Slice-backed queues also need `head`/`tail` index bookkeeping to avoid
   excess copying, which is equivalent complexity with worse removal behavior.

### Node reference for O(1) cancellation

`Push` returns the new `*Node`. The future order book will maintain a map from
`OrderID` → `*Node`, so cancelling order B (`A → B → C → D` → `A → C → D`) is
a map lookup followed by `Remove(node)` with no scanning.

## Complexity

| Operation            | Complexity | Notes                                        |
|----------------------|------------|----------------------------------------------|
| `Append` / `Push`    | O(1)       | Append to tail, returns `*Node`              |
| `Front` / `Peek`     | O(1)       | Dereference head                             |
| `Pop`                | O(1)       | Unlink head                                  |
| `Remove(node)`       | O(1)       | Direct node reference + owner check          |
| `Len`                | O(1)       | Maintained counter                           |
| `IsEmpty`            | O(1)       | `len == 0`                                   |

`Remove` by order ID **without** a node reference would be O(n). This is
intentionally not offered by the queue: the calling layer (order book, next
phase) is expected to keep the `OrderID → *Node` map so cancellation stays
O(1).

## Ownership and mutation

- Each `Node` owns a back-reference to its queue. `Remove` verifies the node's
  owner matches the queue before unlinking; a nil node, a node from another
  queue, or an already-removed node yields `ErrNodeNotFound` and leaves the
  queue unmodified.
- `unlink` clears the removed node's `prev`, `next`, and owner links, so a
  node cannot be double-removed and cannot hold stale links into a live list.
- `Node` holds a reference to the order (`*order.Order`); the queue does not
  own the order's lifecycle, only its position. Callers retain ownership of
  the order value and must not mutate ordering-relevant fields while it rests
  in the book.

## Invariants

For the queue, `checkInvariants` (exercised by tests):

1. `len >= 0`.
2. `len == 0` ⇒ `head == nil` and `tail == nil`.
3. `len > 0` ⇒ both `head` and `tail` are non-nil and valid.
4. `head.prev == nil`.
5. `tail.next == nil`.
6. Traversing `head → tail` visits exactly `len` distinct nodes ⇒ no cycles.
7. Every visited node's owner is this queue and carries a non-nil order.

For the price level, additionally:

8. Every order's price equals the level's price (`order.Price == level.Price()`).

An empty queue yielding `head == nil && tail == nil`, and every mutation
preserving invariants, is asserted after each operation in the tests.

## PriceLevel

A `PriceLevel` bundles:

```
level.Price
level.queue   (OrderQueue, FIFO)
```

A price level only knows its own price and the orders resting there; it has no
knowledge of bids, asks, or other price levels (all later phases).

Operations: `Add` (rejects wrong-price orders with `ErrWrongPrice`), `Front`,
`FrontNode`, `Pop`, `Remove`, `Len`, `IsEmpty`.

Prices use the integer representation established in the order domain
(₹100.25 → `10025`); no floating-point values appear anywhere.