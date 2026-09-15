package book

import (
	"container/list"
	"errors"

	"github.com/gurshaan17/apex/internal/order"
)

// ErrDuplicateID is returned when an order with an ID already present in the
// book is added.
var ErrDuplicateID = errors.New("book: order ID already present")

// ErrOrderNotFound is returned when an order ID cannot be found in the book.
var ErrOrderNotFound = errors.New("book: order not found")

// ErrMarketOrderUnsupported is returned when a market order is added to the
// book. Market orders have no resting price and execution is a later phase.
var ErrMarketOrderUnsupported = errors.New("book: market orders are not supported yet")

// orderLocation records where an order lives in the book so it can be found
// and removed without scanning. It is the value stored in the byID index.
type orderLocation struct {
	side  order.Side
	price order.Price
	node  *Node
}

// bookSide holds every price level on one side of the book (bids or asks)
// plus the sorted list of prices used to answer BestBid/BestAsk.
type bookSide struct {
	levels map[order.Price]*PriceLevel
	prices *list.List                    // sorted by priority: best price first
	elems  map[order.Price]*list.Element // price → its list element
	better func(a, b order.Price) bool   // true if a has higher priority than b
}

func newBookSide(better func(a, b order.Price) bool) *bookSide {
	return &bookSide{
		levels: make(map[order.Price]*PriceLevel),
		prices: list.New(),
		elems:  make(map[order.Price]*list.Element),
		better: better,
	}
}

// best returns the best price on the side, or 0 if the side is empty.
func (s *bookSide) best() order.Price {
	if e := s.prices.Front(); e != nil {
		return e.Value.(order.Price)
	}
	return 0
}

// insertPrice inserts a new price level, keeping the side sorted by priority.
func (s *bookSide) insertPrice(price order.Price, pl *PriceLevel) {
	if _, ok := s.levels[price]; ok {
		return
	}
	s.levels[price] = pl
	var elem *list.Element
	for e := s.prices.Front(); e != nil; e = e.Next() {
		p := e.Value.(order.Price)
		if s.better(price, p) {
			elem = s.prices.InsertBefore(price, e)
			break
		}
	}
	if elem == nil {
		elem = s.prices.PushBack(price)
	}
	s.elems[price] = elem
}

// removePrice drops a price level from the side entirely.
func (s *bookSide) removePrice(price order.Price) {
	if el, ok := s.elems[price]; ok {
		s.prices.Remove(el)
		delete(s.elems, price)
	}
	delete(s.levels, price)
}

// levelAt returns the price level for the given price, if present.
func (s *bookSide) levelAt(price order.Price) (*PriceLevel, bool) {
	pl, ok := s.levels[price]
	return pl, ok
}

// OrderBook maintains resting orders on the bid and ask sides across multiple
// price levels, with price-time priority. It does not perform matching.
type OrderBook struct {
	bids *bookSide
	asks *bookSide
	byID map[order.OrderID]orderLocation
}

// NewOrderBook returns an empty OrderBook.
func NewOrderBook() *OrderBook {
	return &OrderBook{
		bids: newBookSide(func(a, b order.Price) bool { return a > b }),
		asks: newBookSide(func(a, b order.Price) bool { return a < b }),
		byID: make(map[order.OrderID]orderLocation),
	}
}

// Add places an order into the book at its price on its side. The order must
// be valid, must not already exist, and must be a limit order. Adding an order
// transitions it from NEW to OPEN as it is accepted into the book.
func (ob *OrderBook) Add(o *order.Order) error {
	if o == nil {
		return ErrNilOrder
	}
	if err := o.Validate(); err != nil {
		return err
	}
	if o.Type == order.Market {
		return ErrMarketOrderUnsupported
	}
	if _, ok := ob.byID[o.ID]; ok {
		return ErrDuplicateID
	}
	if err := o.Open(); err != nil {
		return err
	}
	side := ob.side(o.Side)
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

// Remove removes the order with the given ID from the book. Price levels that
// become empty are dropped from the book. The order's status is not changed.
func (ob *OrderBook) Remove(id order.OrderID) error {
	loc, ok := ob.byID[id]
	if !ok {
		return ErrOrderNotFound
	}
	side := ob.side(loc.side)
	pl, ok := side.levelAt(loc.price)
	if !ok {
		return ErrOrderNotFound
	}
	if err := pl.Remove(loc.node); err != nil {
		return err
	}
	delete(ob.byID, id)
	if pl.IsEmpty() {
		side.removePrice(loc.price)
	}
	return nil
}

// Get returns the order with the given ID.
func (ob *OrderBook) Get(id order.OrderID) (*order.Order, error) {
	loc, ok := ob.byID[id]
	if !ok {
		return nil, ErrOrderNotFound
	}
	return loc.node.Order, nil
}

// BestBid returns the highest bid price, or 0 if there are no bids.
func (ob *OrderBook) BestBid() order.Price {
	return ob.bids.best()
}

// BestAsk returns the lowest ask price, or 0 if there are no asks.
func (ob *OrderBook) BestAsk() order.Price {
	return ob.asks.best()
}

// Len returns the number of resting orders in the book.
func (ob *OrderBook) Len() int {
	return len(ob.byID)
}

// Snapshot returns a read-only view of the book: price levels best-first on
// each side, with each level's total quantity and its orders in FIFO order.
func (ob *OrderBook) Snapshot() Snapshot {
	return Snapshot{
		Bids: ob.sideSnapshot(ob.bids),
		Asks: ob.sideSnapshot(ob.asks),
	}
}

func (ob *OrderBook) sideSnapshot(s *bookSide) []LevelSnapshot {
	out := make([]LevelSnapshot, 0, s.prices.Len())
	for e := s.prices.Front(); e != nil; e = e.Next() {
		price := e.Value.(order.Price)
		pl, _ := s.levelAt(price)
		var qty order.Quantity
		orders := make([]*order.Order, 0, pl.Len())
		for n := pl.queue.head; n != nil; n = n.next {
			copy := *n.Order
			orders = append(orders, &copy)
			qty += copy.Remaining()
		}
		out = append(out, LevelSnapshot{Price: price, Qty: qty, Orders: orders})
	}
	return out
}

func (ob *OrderBook) side(side order.Side) *bookSide {
	switch side {
	case order.Buy:
		return ob.bids
	case order.Sell:
		return ob.asks
	}
	return nil
}
