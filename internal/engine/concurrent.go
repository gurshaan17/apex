package engine

import (
	"sync"

	"github.com/gurshaan17/apex/internal/book"
	"github.com/gurshaan17/apex/internal/order"
)

// ConcurrentEngine wraps the single-threaded Engine in a single-writer
// pipeline. A dedicated goroutine — the engine loop — is started by
// NewConcurrentEngine and is the sole owner of the underlying Engine and of
// the OrderBook that Engine owns. Because exactly one goroutine ever reads
// or writes matching state, no locks are needed: there is simply no other
// writer.
//
// Every public operation (submit, cancel, snapshot, order lookup, invariant
// check) is expressed as a request value sent over a bounded channel into the
// loop. The loop processes requests one at a time, in FIFO order, calling the
// same unmodified Engine methods the direct API does, and replies on a
// private per-request response channel. Callers therefore get a
// synchronous-feeling API (they block until the loop answers) while all
// matching state stays confined to one goroutine: the order book is never
// touched outside the engine loop.
//
// The single-threaded Engine remains available and unchanged, so V1 behavior
// and benchmarks can later be compared against the wrapped pipeline.
type ConcurrentEngine struct {
	engine   *Engine
	requests chan request
	once     sync.Once
	wg       sync.WaitGroup
}

// defaultRequestBuffer is the fallback request channel capacity when a
// non-positive buffer size is passed to NewConcurrentEngine.
const defaultRequestBuffer = 1024

// request is the sum type carried over the request channel: exactly one of
// the concrete request structs below.
//
// We chose a sealed interface with a type switch over a single tagged struct
// because each operation carries only its own payload (an Order, an ID, or
// nothing) and its own response channel type, so one operation can never see
// another operation's fields or receive the wrong result type. The cost is a
// small dynamic type dispatch per request, which is negligible next to the
// matching work the loop performs. The unexported marker method seals the set
// of request kinds to this package. The loop's default case panics on any
// kind that is not wired up, turning a missed case into an immediate program
// error instead of a swallowed request.
type request interface {
	isRequest()
}

// submitRequest carries an order submitted through the loop. The order is
// passed by value, matching Engine.SubmitOrder's value semantics.
type submitRequest struct {
	order      order.Order
	responseCh chan submitResponse
}

type submitResponse struct {
	result SubmitResult
	err    error
}

// cancelRequest carries a cancellation whose result is either an error or
// nil on success, mirroring Engine.CancelOrder's plain error return.
type cancelRequest struct {
	id         order.OrderID
	responseCh chan error
}

// snapshotRequest asks the loop for a consistent read of the book.
type snapshotRequest struct {
	responseCh chan book.Snapshot
}

// getOrderRequest asks the loop for a copy of a submitted order.
type getOrderRequest struct {
	id         order.OrderID
	responseCh chan getOrderResponse
}

type getOrderResponse struct {
	order *order.Order
	err   error
}

// invariantsRequest asks the loop to run the diagnostics-only invariant
// check on the engine it owns.
type invariantsRequest struct {
	responseCh chan error
}

func (submitRequest) isRequest()     {}
func (cancelRequest) isRequest()     {}
func (snapshotRequest) isRequest()   {}
func (getOrderRequest) isRequest()   {}
func (invariantsRequest) isRequest() {}

// NewConcurrentEngine starts a matching engine wrapped in the single-writer
// pipeline and launches its engine loop goroutine. bufferSize is the capacity
// of the bounded request channel; a non-positive value selects
// defaultRequestBuffer (1024). The returned engine must be shut down with
// Shutdown when no longer needed.
func NewConcurrentEngine(bufferSize int) *ConcurrentEngine {
	if bufferSize <= 0 {
		bufferSize = defaultRequestBuffer
	}
	ce := &ConcurrentEngine{
		engine:   NewEngine(),
		requests: make(chan request, bufferSize),
	}
	ce.wg.Add(1)
	go ce.run()
	return ce
}

// run is the engine loop: the sole reader of the request channel and the only
// goroutine that ever calls into the wrapped Engine. Requests are handled in
// receive (FIFO) order, so a single caller's operations execute in the order
// they were submitted.
func (ce *ConcurrentEngine) run() {
	defer ce.wg.Done()
	for req := range ce.requests {
		switch r := req.(type) {
		case submitRequest:
			res, err := ce.engine.SubmitOrder(r.order)
			r.responseCh <- submitResponse{result: res, err: err}
		case cancelRequest:
			r.responseCh <- ce.engine.CancelOrder(r.id)
		case snapshotRequest:
			r.responseCh <- ce.engine.GetOrderBookSnapshot()
		case getOrderRequest:
			o, err := ce.engine.GetOrder(r.id)
			r.responseCh <- getOrderResponse{order: o, err: err}
		case invariantsRequest:
			r.responseCh <- ce.engine.CheckInvariants()
		default:
			// Unreachable: the marker method seals the request set.
			panic("engine: unknown request kind")
		}
	}
}

// SubmitOrder enqueues an order submission into the engine loop and blocks
// until the loop has applied the (unmodified) Engine.SubmitOrder logic and
// replied with the result. It behaves and returns identically to
// Engine.SubmitOrder.
func (ce *ConcurrentEngine) SubmitOrder(o order.Order) (SubmitResult, error) {
	ch := make(chan submitResponse)
	ce.requests <- submitRequest{order: o, responseCh: ch}
	resp := <-ch
	return resp.result, resp.err
}

// CancelOrder enqueues a cancellation into the engine loop and blocks until
// the loop has applied the (unmodified) Engine.CancelOrder logic and replied.
// It behaves and returns identically to Engine.CancelOrder.
func (ce *ConcurrentEngine) CancelOrder(id order.OrderID) error {
	ch := make(chan error)
	ce.requests <- cancelRequest{id: id, responseCh: ch}
	return <-ch
}

// GetOrderBookSnapshot returns a read-only view of the current book. Like
// every other operation it is routed through the engine loop so the snapshot
// reflects a consistent state observed at a single point in the mutation
// sequence; a separate read path could observe the book mid-way through a
// multi-level match. Snapshot orders are copies, exactly as with
// Engine.GetOrderBookSnapshot.
func (ce *ConcurrentEngine) GetOrderBookSnapshot() book.Snapshot {
	ch := make(chan book.Snapshot)
	ce.requests <- snapshotRequest{responseCh: ch}
	return <-ch
}

// GetOrder returns a copy of a submitted order, resolved by the engine loop.
// It behaves and returns identically to Engine.GetOrder.
func (ce *ConcurrentEngine) GetOrder(id order.OrderID) (*order.Order, error) {
	ch := make(chan getOrderResponse)
	ce.requests <- getOrderRequest{id: id, responseCh: ch}
	resp := <-ch
	return resp.order, resp.err
}

// CheckInvariants runs the aggregate invariant check on the wrapped engine
// inside the engine loop and returns its result. It behaves identically to
// Engine.CheckInvariants.
func (ce *ConcurrentEngine) CheckInvariants() error {
	ch := make(chan error)
	ce.requests <- invariantsRequest{responseCh: ch}
	return <-ch
}

// Shutdown closes the request channel and blocks until the engine loop has
// drained every already-queued request — replying to each in-flight caller —
// and exited, so the loop goroutine never leaks. Calling Shutdown more than
// once is safe. SubmitOrder, CancelOrder, GetOrder, GetOrderBookSnapshot, and
// CheckInvariants must not be called after Shutdown: the wrapped Engine
// becomes unreachable once the loop stops.
func (ce *ConcurrentEngine) Shutdown() {
	ce.once.Do(func() {
		close(ce.requests)
	})
	ce.wg.Wait()
}
