package engine

import (
	"fmt"

	"github.com/gurshaan17/apex/internal/book"
	"github.com/gurshaan17/apex/internal/order"
)

// Engine is the matching engine. It owns an OrderBook and coordinates order
// lifecycle transitions and trade generation. The engine is single-symbol by
// design and is not safe for concurrent use.
type Engine struct {
	book        *book.OrderBook
	orders      map[order.OrderID]*order.Order
	nextTradeID uint64
}

// NewEngine returns an empty matching engine.
func NewEngine() *Engine {
	return &Engine{
		book:   book.NewOrderBook(),
		orders: make(map[order.OrderID]*order.Order),
	}
}

// SubmitOrder validates an order, matches it against the opposite side using
// price-time priority, and rests any limit-order residual in the book.
//
// Every trade is executed at the resting order's price.
//
// Market orders never rest: they consume the best available liquidity across
// price levels until filled or until liquidity runs out, and any unfilled
// remainder is cancelled.
//
// SubmitOrder copies the caller's order; use GetOrder or the returned
// SubmitResult to observe the outcome.
func (e *Engine) SubmitOrder(o order.Order) (SubmitResult, error) {
	if err := o.Validate(); err != nil {
		return SubmitResult{}, err
	}
	if _, ok := e.orders[o.ID]; ok {
		return SubmitResult{}, ErrDuplicateOrder
	}
	if err := o.Open(); err != nil {
		return SubmitResult{}, err
	}

	p := &o
	e.orders[p.ID] = p

	var trades []Trade
	for p.Remaining() > 0 {
		resting, ok := e.bestOpposite(p)
		if !ok {
			break
		}
		qty := minQuantity(p.Remaining(), resting.Remaining())

		if err := resting.Fill(qty); err != nil {
			return SubmitResult{}, err
		}
		if err := p.Fill(qty); err != nil {
			return SubmitResult{}, err
		}

		e.nextTradeID++
		buyID, sellID := tradeParties(p, resting)
		trades = append(trades, Trade{
			TradeID:     e.nextTradeID,
			Symbol:      p.Symbol,
			BuyOrderID:  buyID,
			SellOrderID: sellID,
			Price:       resting.Price,
			Qty:         qty,
			Timestamp:   p.Time,
		})

		if resting.IsFullyFilled() {
			if err := e.book.Remove(resting.ID); err != nil {
				return SubmitResult{}, err
			}
		}
	}

	if p.Remaining() > 0 {
		switch p.Type {
		case order.Market:
			// A market order cannot rest: insufficient liquidity means the
			// unfilled remainder is cancelled, preserving filled quantity.
			if err := p.Cancel(); err != nil {
				return SubmitResult{}, err
			}
		default:
			if err := e.book.Place(p); err != nil {
				return SubmitResult{}, err
			}
		}
	}

	return SubmitResult{
		OrderID:     p.ID,
		OriginalQty: p.Qty,
		FilledQty:   p.Filled,
		Remaining:   p.Remaining(),
		Status:      p.Status,
		Trades:      trades,
	}, nil
}

// CancelOrder cancels a resting order: it removes it from the book and
// transitions it to CANCELLED. Filled and already-cancelled orders return
// typed errors instead.
func (e *Engine) CancelOrder(id order.OrderID) error {
	o, ok := e.orders[id]
	if !ok {
		return ErrOrderNotFound
	}
	switch o.Status {
	case order.Filled:
		return ErrAlreadyFilled
	case order.Cancelled:
		return ErrAlreadyCancelled
	case order.New:
		return order.ErrCannotCancel
	}
	if err := e.book.Remove(id); err != nil {
		return err
	}
	return o.Cancel()
}

// GetOrder returns a copy of a submitted order so callers can inspect status
// and quantities without mutating engine-owned state.
func (e *Engine) GetOrder(id order.OrderID) (*order.Order, error) {
	o, ok := e.orders[id]
	if !ok {
		return nil, ErrOrderNotFound
	}
	cp := *o
	return &cp, nil
}

// GetOrderBookSnapshot returns a read-only view of the current book. All
// orders returned are copies; mutating the result never affects the engine.
func (e *Engine) GetOrderBookSnapshot() book.Snapshot {
	return e.book.Snapshot()
}

// CheckInvariants verifies the aggregate engine invariants: quantity
// accounting for every submitted order, exact book occupancy by status, and
// the book's own structural invariants. It is intended for diagnostics and
// tests; production paths do not pay this cost.
func (e *Engine) CheckInvariants() error {
	if err := e.book.CheckInvariants(); err != nil {
		return fmt.Errorf("book: %w", err)
	}

	inBook := make(map[order.OrderID]bool)
	snap := e.book.Snapshot()
	for _, lvl := range snap.Bids {
		seen := make(map[order.OrderID]bool)
		for _, o := range lvl.Orders {
			if seen[o.ID] {
				return fmt.Errorf("order %d appears twice in bid levels", o.ID)
			}
			seen[o.ID] = true
			inBook[o.ID] = true
			if o.Remaining() <= 0 {
				return fmt.Errorf("resting order %d has non-positive remaining", o.ID)
			}
		}
	}
	for _, lvl := range snap.Asks {
		seen := make(map[order.OrderID]bool)
		for _, o := range lvl.Orders {
			if seen[o.ID] {
				return fmt.Errorf("order %d appears twice in ask levels", o.ID)
			}
			seen[o.ID] = true
			inBook[o.ID] = true
			if o.Remaining() <= 0 {
				return fmt.Errorf("resting order %d has non-positive remaining", o.ID)
			}
		}
	}

	for id, o := range e.orders {
		if o.ID != id {
			return fmt.Errorf("registry key %d maps to order %d", id, o.ID)
		}
		if o.Qty <= 0 {
			return fmt.Errorf("order %d has non-positive original qty", id)
		}
		if o.Filled < 0 {
			return fmt.Errorf("order %d has negative filled qty", id)
		}
		if o.Remaining() < 0 {
			return fmt.Errorf("order %d has negative remaining qty", id)
		}
		if o.Filled > o.Qty {
			return fmt.Errorf("order %d filled %d exceeds original %d", id, o.Filled, o.Qty)
		}
		if o.Filled+o.Remaining() != o.Qty {
			return fmt.Errorf("order %d accounting broken: filled %d + remaining %d != original %d", id, o.Filled, o.Remaining(), o.Qty)
		}
		switch o.Status {
		case order.Open, order.PartiallyFilled:
			if !inBook[id] {
				return fmt.Errorf("active order %d (%s) not present in book", id, o.Status)
			}
		case order.Filled, order.Cancelled:
			if inBook[id] {
				return fmt.Errorf("terminal order %d (%s) still present in book", id, o.Status)
			}
		default:
			return fmt.Errorf("order %d in unexpected status %s", id, o.Status)
		}
	}
	return nil
}

// bestOpposite returns the highest-priority opposite-side order that the given
// order can trade against, or ok=false if no executable liquidity exists.
// Market orders cross regardless of price; limit orders only cross when their
// price meets or beats the best opposite price.
func (e *Engine) bestOpposite(o *order.Order) (*order.Order, bool) {
	var price order.Price
	var opposite order.Side

	switch o.Side {
	case order.Buy:
		price = e.book.BestAsk()
		opposite = order.Sell
		if price == 0 {
			return nil, false
		}
		if o.Type != order.Market && o.Price < price {
			return nil, false
		}
	case order.Sell:
		price = e.book.BestBid()
		opposite = order.Buy
		if price == 0 {
			return nil, false
		}
		if o.Type != order.Market && o.Price > price {
			return nil, false
		}
	default:
		return nil, false
	}

	resting, err := e.book.FrontOrder(opposite, price)
	if err != nil {
		return nil, false
	}
	return resting, true
}

// tradeParties assigns the buy and sell order IDs for a trade between the
// incoming and resting orders, in either role.
func tradeParties(incoming, resting *order.Order) (buyID, sellID order.OrderID) {
	if incoming.Side == order.Buy {
		return incoming.ID, resting.ID
	}
	return resting.ID, incoming.ID
}

func minQuantity(a, b order.Quantity) order.Quantity {
	if a < b {
		return a
	}
	return b
}
