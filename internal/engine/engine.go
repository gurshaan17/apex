package engine

import (
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
// price-time priority, and rests any remaining quantity in the book. Every
// trade is executed at the resting order's price. The caller's order is copied;
// use GetOrder or the returned SubmitResult to observe the outcome.
func (e *Engine) SubmitOrder(o order.Order) (SubmitResult, error) {
	if err := o.Validate(); err != nil {
		return SubmitResult{}, err
	}
	if o.Type != order.Limit {
		return SubmitResult{}, ErrUnsupportedOrderType
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
		if err := e.book.Place(p); err != nil {
			return SubmitResult{}, err
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

// GetOrder returns the engine's live view of a submitted order. The returned
// pointer is owned by the engine; mutating it bypasses engine invariants.
func (e *Engine) GetOrder(id order.OrderID) (*order.Order, error) {
	o, ok := e.orders[id]
	if !ok {
		return nil, ErrOrderNotFound
	}
	return o, nil
}

// GetOrderBookSnapshot returns a read-only snapshot of the current book.
func (e *Engine) GetOrderBookSnapshot() book.Snapshot {
	return e.book.Snapshot()
}

// bestOpposite returns the highest-priority opposite-side order that the given
// order can trade against, or ok=false if no executable liquidity exists.
func (e *Engine) bestOpposite(o *order.Order) (*order.Order, bool) {
	var price order.Price
	var opposite order.Side

	switch o.Side {
	case order.Buy:
		price = e.book.BestAsk()
		opposite = order.Sell
		if price == 0 || o.Price < price {
			return nil, false
		}
	case order.Sell:
		price = e.book.BestBid()
		opposite = order.Buy
		if price == 0 || o.Price > price {
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
