package book

import (
	"github.com/gurshaan17/apex/internal/order"
)

// Place adds an order that is already in OPEN or PARTIALLY_FILLED state to the
// book at its price level, without changing its status. It is used by the
// matching engine to rest an order that survived matching with remaining
// quantity. Orders in NEW state should be added with Add; filled or cancelled
// orders cannot rest.
func (ob *OrderBook) Place(o *order.Order) error {
	if o == nil {
		return ErrNilOrder
	}
	if o.Type == order.Market {
		return ErrMarketOrderUnsupported
	}
	if o.Status != order.Open && o.Status != order.PartiallyFilled {
		return order.ErrInvalidState
	}
	if _, ok := ob.byID[o.ID]; ok {
		return ErrDuplicateID
	}
	side := ob.side(o.Side)
	if side == nil {
		return order.ErrInvalidSide
	}
	pl, ok := side.levelAt(o.Price)
	if !ok {
		pl = NewPriceLevel(o.Price)
		side.insertPrice(o.Price, pl)
	}
	node, err := pl.Add(o)
	if err != nil {
		if !ok {
			side.removePrice(o.Price)
		}
		return err
	}
	ob.byID[o.ID] = orderLocation{side: o.Side, price: o.Price, node: node}
	return nil
}

// FrontOrder returns the oldest order resting at the given price on the given
// side without removing it. It returns ErrOrderNotFound if no orders rest at
// that price.
func (ob *OrderBook) FrontOrder(side order.Side, price order.Price) (*order.Order, error) {
	s := ob.side(side)
	if s == nil {
		return nil, order.ErrInvalidSide
	}
	pl, ok := s.levelAt(price)
	if !ok {
		return nil, ErrOrderNotFound
	}
	o := pl.Front()
	if o == nil {
		return nil, ErrOrderNotFound
	}
	return o, nil
}
