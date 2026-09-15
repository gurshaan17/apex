package order

import "time"

// Side represents the direction of an order.
type Side int

const (
	SideUnknown Side = iota
	Buy
	Sell
)

func (s Side) String() string {
	switch s {
	case Buy:
		return "BUY"
	case Sell:
		return "SELL"
	default:
		return "UNKNOWN"
	}
}

// ParseSide converts a string to a Side.
func ParseSide(s string) (Side, error) {
	switch s {
	case "BUY":
		return Buy, nil
	case "SELL":
		return Sell, nil
	default:
		return SideUnknown, ErrInvalidSide
	}
}

// Type represents the kind of order.
type Type int

const (
	TypeUnknown Type = iota
	Limit
	Market
)

func (t Type) String() string {
	switch t {
	case Limit:
		return "LIMIT"
	case Market:
		return "MARKET"
	default:
		return "UNKNOWN"
	}
}

// ParseType converts a string to an Order Type.
func ParseType(s string) (Type, error) {
	switch s {
	case "LIMIT":
		return Limit, nil
	case "MARKET":
		return Market, nil
	default:
		return TypeUnknown, ErrInvalidType
	}
}

// Status represents the current state of an order in its lifecycle.
type Status int

const (
	StatusUnknown Status = iota
	New
	Open
	PartiallyFilled
	Filled
	Cancelled
)

func (s Status) String() string {
	switch s {
	case New:
		return "NEW"
	case Open:
		return "OPEN"
	case PartiallyFilled:
		return "PARTIALLY_FILLED"
	case Filled:
		return "FILLED"
	case Cancelled:
		return "CANCELLED"
	default:
		return "UNKNOWN"
	}
}

// validTransitions defines which status transitions are allowed.
var validTransitions = map[Status][]Status{
	New:             {Open},
	Open:            {PartiallyFilled, Filled, Cancelled},
	PartiallyFilled: {PartiallyFilled, Filled, Cancelled},
}

// Price is an integer representation of price in the smallest currency unit.
// For example, with Indian Rupees: ₹100.25 is stored as 10025 (paise).
// This avoids floating-point precision issues common in financial systems.
type Price int64

// Quantity is an integer number of units. No fractional quantities in this phase.
type Quantity int64

// OrderID is a unique identifier for an order.
type OrderID int64

// Order represents a single order in the system.
type Order struct {
	ID     OrderID
	Symbol string
	Side   Side
	Type   Type
	Price  Price
	Qty    Quantity
	Filled Quantity
	Status Status
	Time   time.Time
}

// NewOrder creates a new order in NEW status with zero fill.
func NewOrder(id OrderID, symbol string, side Side, typ Type, price Price, qty Quantity) Order {
	return Order{
		ID:     id,
		Symbol: symbol,
		Side:   side,
		Type:   typ,
		Price:  price,
		Qty:    qty,
		Status: New,
		Time:   time.Now(),
	}
}

// Remaining returns the unfilled quantity.
func (o *Order) Remaining() Quantity {
	return o.Qty - o.Filled
}

// IsFullyFilled returns true if the order has been completely filled.
func (o *Order) IsFullyFilled() bool {
	return o.Filled >= o.Qty
}

// CanCancel returns true if the order is in a cancellable state.
func (o *Order) CanCancel() bool {
	return o.Status == Open || o.Status == PartiallyFilled
}

// Validate checks that the order has all required fields set to valid values.
func (o Order) Validate() error {
	if o.ID == 0 {
		return ErrInvalidOrderID
	}
	if o.Symbol == "" {
		return ErrEmptySymbol
	}
	if o.Side != Buy && o.Side != Sell {
		return ErrInvalidSide
	}
	if o.Type != Limit && o.Type != Market {
		return ErrInvalidType
	}
	if o.Qty <= 0 {
		return ErrInvalidQuantity
	}
	if o.Type == Limit && o.Price <= 0 {
		return ErrInvalidPrice
	}
	if o.Type == Market && o.Price != 0 {
		return ErrMarketPriceSet
	}
	return nil
}

// Fill applies a fill of the given quantity to the order.
// It returns an error if the fill would overfill the order or if the order
// is not in a fillable state.
func (o *Order) Fill(qty Quantity) error {
	if o.Status != Open && o.Status != PartiallyFilled {
		return ErrInvalidState
	}
	if qty <= 0 {
		return ErrInvalidQuantity
	}
	if qty > o.Remaining() {
		return ErrOverfill
	}

	o.Filled += qty

	switch {
	case o.IsFullyFilled():
		o.Status = Filled
	case o.Filled > 0:
		o.Status = PartiallyFilled
	}

	return nil
}

// Cancel transitions the order to CANCELLED if it is in a cancellable state.
func (o *Order) Cancel() error {
	if !o.CanCancel() {
		return ErrCannotCancel
	}
	o.Status = Cancelled
	return nil
}

// Open transitions the order from NEW to OPEN.
func (o *Order) Open() error {
	return o.transitionTo(Open)
}

// transitionTo attempts a status transition, returning an error if it is invalid.
func (o *Order) transitionTo(to Status) error {
	allowed := validTransitions[o.Status]
	for _, s := range allowed {
		if s == to {
			o.Status = to
			return nil
		}
	}
	return ErrInvalidState
}
