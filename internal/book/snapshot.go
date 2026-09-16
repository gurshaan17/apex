package book

import (
	"fmt"

	"github.com/gurshaan17/apex/internal/order"
)

// LevelSnapshot is a read-only view of a single price level: its price, the
// total resting (unfilled) quantity, and the orders resting there in FIFO
// order. Orders are copies, so mutating a snapshot can never affect the book.
type LevelSnapshot struct {
	Price  order.Price
	Qty    order.Quantity
	Orders []*order.Order
}

// Snapshot is a read-only view of the whole book, with price levels ordered
// best-first on each side.
type Snapshot struct {
	Bids []LevelSnapshot
	Asks []LevelSnapshot
}

// CheckInvariants verifies the structural invariants of the book. It is the
// public entry point used by engine-level diagnostics; production paths do not
// pay this cost.
func (ob *OrderBook) CheckInvariants() error {
	return ob.checkInvariants()
}

// checkInvariants verifies the structural invariants of the book. It is used
// by tests; production paths do not pay this cost.
func (ob *OrderBook) checkInvariants() error {
	if err := ob.checkSideInvariants(ob.bids, order.Buy); err != nil {
		return fmt.Errorf("bid side: %w", err)
	}
	if err := ob.checkSideInvariants(ob.asks, order.Sell); err != nil {
		return fmt.Errorf("ask side: %w", err)
	}
	for id, loc := range ob.byID {
		side := ob.side(loc.side)
		if side == nil {
			return fmt.Errorf("invariant violated: order %d indexed with unknown side", id)
		}
		pl, ok := side.levelAt(loc.price)
		if !ok {
			return fmt.Errorf("invariant violated: order %d indexed under missing price level %d", id, loc.price)
		}
		if loc.node.q != pl.queue {
			return fmt.Errorf("invariant violated: order %d node not owned by its level queue", id)
		}
		if loc.node.Order.ID != id {
			return fmt.Errorf("invariant violated: index entry %d maps to order %d", id, loc.node.Order.ID)
		}
	}
	return nil
}

func (ob *OrderBook) checkSideInvariants(s *bookSide, side order.Side) error {
	if len(s.levels) != len(s.prices) {
		return fmt.Errorf("level map and price slice disagree (%d/%d)",
			len(s.levels), len(s.prices))
	}
	for i := 0; i+1 < len(s.prices); i++ {
		if !s.better(s.prices[i], s.prices[i+1]) {
			return fmt.Errorf("price slice out of order: %d before %d", s.prices[i], s.prices[i+1])
		}
	}
	for _, price := range s.prices {
		pl, ok := s.levelAt(price)
		if !ok {
			return fmt.Errorf("price list contains %d with no level", price)
		}
		if pl.IsEmpty() {
			return fmt.Errorf("empty price level %d remains in book", price)
		}
		if pl.Price() != price {
			return fmt.Errorf("level price mismatch: level=%d list=%d", pl.Price(), price)
		}
		if err := pl.checkInvariants(); err != nil {
			return err
		}
		for n := pl.queue.head; n != nil; n = n.next {
			if n.Order.Side != side {
				return fmt.Errorf("order %d on wrong side", n.Order.ID)
			}
			loc, ok := ob.byID[n.Order.ID]
			if !ok {
				return fmt.Errorf("order %d resting but not indexed", n.Order.ID)
			}
			if loc.node != n {
				return fmt.Errorf("order %d index node mismatch", n.Order.ID)
			}
		}
	}
	return nil
}
