package engine

import "errors"

var (
	// ErrOrderNotFound is returned when an order ID is unknown to the engine.
	ErrOrderNotFound = errors.New("engine: order not found")

	// ErrAlreadyFilled is returned when trying to cancel a filled order.
	ErrAlreadyFilled = errors.New("engine: order already filled")

	// ErrAlreadyCancelled is returned when trying to cancel a cancelled order.
	ErrAlreadyCancelled = errors.New("engine: order already cancelled")

	// ErrDuplicateOrder is returned when an order ID is submitted twice.
	ErrDuplicateOrder = errors.New("engine: duplicate order ID")
)
