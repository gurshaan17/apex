package book

import "errors"

var (
	// ErrWrongPrice is returned when an order whose price does not match the
	// price level's price is added to it.
	ErrWrongPrice = errors.New("book: order price does not match price level")

	// ErrNilOrder is returned when a nil order is pushed into a queue.
	ErrNilOrder = errors.New("book: nil order")

	// ErrNodeNotFound is returned when removing a node that does not belong
	// to this queue (for example a nil node, an already-removed node, or a
	// node owned by another queue).
	ErrNodeNotFound = errors.New("book: node not found in this queue")
)
