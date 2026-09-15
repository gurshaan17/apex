package engine

import (
	"time"

	"github.com/gurshaan17/apex/internal/order"
)

// Trade is a single executed fill between a resting order and an incoming
// order. The execution price is always the resting order's price.
type Trade struct {
	TradeID     uint64
	Symbol      string
	BuyOrderID  order.OrderID
	SellOrderID order.OrderID
	Price       order.Price
	Qty         order.Quantity
	Timestamp   time.Time
}

// SubmitResult reports the outcome of SubmitOrder. Trades contains every
// fill that occurred during this single submission.
type SubmitResult struct {
	OrderID     order.OrderID
	OriginalQty order.Quantity
	FilledQty   order.Quantity
	Remaining   order.Quantity
	Status      order.Status
	Trades      []Trade
}
