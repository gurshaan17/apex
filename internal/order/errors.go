package order

import "errors"

var (
	ErrInvalidOrderID  = errors.New("invalid order: zero ID")
	ErrEmptySymbol     = errors.New("invalid order: empty symbol")
	ErrInvalidSide     = errors.New("invalid order: unknown side")
	ErrInvalidType     = errors.New("invalid order: unknown type")
	ErrInvalidQuantity = errors.New("invalid order: quantity must be positive")
	ErrInvalidPrice    = errors.New("invalid order: limit order requires a positive price")
	ErrMarketPriceSet  = errors.New("invalid order: market order must not have a price")
	ErrOverfill        = errors.New("fill exceeds remaining quantity")
	ErrInvalidState    = errors.New("invalid state transition")
	ErrCannotCancel    = errors.New("order cannot be cancelled in current state")
)
