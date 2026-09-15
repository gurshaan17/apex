package book

import (
	"fmt"

	"github.com/gurshaan17/apex/internal/order"
)

// PriceLevel holds every order resting at a single price, in FIFO order.
// A price level knows nothing about other prices, bids, asks, or the book.
type PriceLevel struct {
	price order.Price
	queue *OrderQueue
}

// NewPriceLevel returns an empty price level for the given price.
// Price uses the integer representation established in the order domain
// (smallest currency unit, e.g. ₹100.25 → 10025).
func NewPriceLevel(price order.Price) *PriceLevel {
	return &PriceLevel{
		price: price,
		queue: NewOrderQueue(),
	}
}

// Price returns the price of the level.
func (pl *PriceLevel) Price() order.Price {
	return pl.price
}

// Add appends an order to the level. The order must carry exactly this
// level's price; a price mismatch is rejected with ErrWrongPrice and the
// level is left unmodified.
func (pl *PriceLevel) Add(o *order.Order) (*Node, error) {
	if o == nil {
		return nil, ErrNilOrder
	}
	if o.Price != pl.price {
		return nil, ErrWrongPrice
	}
	return pl.queue.Push(o)
}

// Front returns the order at the front of the level without removing it,
// or nil if empty.
func (pl *PriceLevel) Front() *order.Order {
	return pl.queue.Front()
}

// FrontNode returns the front node without removing it, or nil if empty.
func (pl *PriceLevel) FrontNode() *Node {
	return pl.queue.FrontNode()
}

// Pop removes and returns the front order (FIFO), or nil if empty.
func (pl *PriceLevel) Pop() *order.Order {
	return pl.queue.Pop()
}

// Remove unlinks a specific order node from the level in O(1) time.
func (pl *PriceLevel) Remove(n *Node) error {
	return pl.queue.Remove(n)
}

// Len returns the number of orders resting at this level.
func (pl *PriceLevel) Len() int {
	return pl.queue.Len()
}

// IsEmpty reports whether the level has no orders.
func (pl *PriceLevel) IsEmpty() bool {
	return pl.queue.IsEmpty()
}

// checkInvariants verifies the level's structural invariants, including
// that every order carries exactly the level's price. Used by tests.
func (pl *PriceLevel) checkInvariants() error {
	if err := pl.queue.checkInvariants(); err != nil {
		return err
	}
	for n := pl.queue.head; n != nil; n = n.next {
		if n.Order.Price != pl.price {
			return fmt.Errorf("invariant violated: order %d at price %d in level %d",
				n.Order.ID, n.Order.Price, pl.price)
		}
	}
	return nil
}
