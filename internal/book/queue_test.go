package book

import (
	"errors"
	"testing"

	"github.com/gurshaan17/apex/internal/order"
)

func newLimitOrder(t *testing.T, id order.OrderID, price order.Price) *order.Order {
	t.Helper()
	o := order.NewOrder(id, "AAPL", order.Buy, order.Limit, price, 100)
	if err := o.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := o.Open(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return &o
}

func mustCheck(t *testing.T, v interface {
	checkInvariants() error
}) {
	t.Helper()
	if err := v.checkInvariants(); err != nil {
		t.Fatalf("invariants violated: %v", err)
	}
}

func collectOrders(q *OrderQueue) []*order.Order {
	var out []*order.Order
	for n := q.head; n != nil; n = n.next {
		out = append(out, n.Order)
	}
	return out
}

func orderIDs(orders []*order.Order) []order.OrderID {
	ids := make([]order.OrderID, len(orders))
	for i, o := range orders {
		ids[i] = o.ID
	}
	return ids
}

func TestOrderQueueEmpty(t *testing.T) {
	q := NewOrderQueue()
	if q.Len() != 0 {
		t.Fatalf("expected len 0, got %d", q.Len())
	}
	if !q.IsEmpty() {
		t.Fatal("expected empty queue")
	}
	if q.Front() != nil {
		t.Fatal("expected nil front")
	}
	if q.FrontNode() != nil {
		t.Fatal("expected nil front node")
	}
	if q.Pop() != nil {
		t.Fatal("expected nil pop")
	}
	mustCheck(t, q)
}

func TestOrderQueueSingleInsertion(t *testing.T) {
	q := NewOrderQueue()
	a := newLimitOrder(t, 1, 10025)
	n, err := q.Push(a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.Len() != 1 {
		t.Fatalf("expected len 1, got %d", q.Len())
	}
	if q.IsEmpty() {
		t.Fatal("expected non-empty queue")
	}
	if q.Front() != a {
		t.Fatal("expected front to be pushed order")
	}
	if q.FrontNode() != n {
		t.Fatal("expected front node to be returned node")
	}
	mustCheck(t, q)
}

func TestOrderQueueFIFOOrdering(t *testing.T) {
	q := NewOrderQueue()
	a := newLimitOrder(t, 1, 10025)
	b := newLimitOrder(t, 2, 10025)
	c := newLimitOrder(t, 3, 10025)
	for _, o := range []*order.Order{a, b, c} {
		if _, err := q.Push(o); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	mustCheck(t, q)

	if got := q.Front(); got != a {
		t.Fatalf("expected front A, got order %d", got.ID)
	}
	if got := orderIDs(collectOrders(q)); !equalIDs(got, []order.OrderID{1, 2, 3}) {
		t.Fatalf("expected [1 2 3], got %v", got)
	}

	if got := q.Pop(); got != a {
		t.Fatalf("expected first pop A, got order %d", got.ID)
	}
	if got := q.Pop(); got != b {
		t.Fatalf("expected second pop B, got order %d", got.ID)
	}
	if got := q.Pop(); got != c {
		t.Fatalf("expected third pop C, got order %d", got.ID)
	}
	if !q.IsEmpty() {
		t.Fatal("expected empty queue after pops")
	}
	mustCheck(t, q)
}

func TestOrderQueueRemoveHead(t *testing.T) {
	q := NewOrderQueue()
	a := newLimitOrder(t, 1, 10025)
	b := newLimitOrder(t, 2, 10025)
	c := newLimitOrder(t, 3, 10025)
	_, _ = q.Push(a)
	nb, _ := q.Push(b)
	_, _ = q.Push(c)

	if err := q.Remove(nb); err != nil { // sanity: not head yet
		t.Fatalf("unexpected error: %v", err)
	}
	mustCheck(t, q)

	// rebuild: A B C -> remove head A
	q = NewOrderQueue()
	na, _ := q.Push(a)
	_, _ = q.Push(b)
	_, _ = q.Push(c)
	if err := q.Remove(na); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.Front() != b {
		t.Fatal("expected front B after removing head")
	}
	if q.Len() != 2 {
		t.Fatalf("expected len 2, got %d", q.Len())
	}
	mustCheck(t, q)
	if q.head.prev != nil {
		t.Fatal("expected new head.prev == nil")
	}
	if nb.prev != nil || nb.next != nil || nb.q != nil {
		t.Fatal("removed node must have links cleared")
	}
}

func TestOrderQueueRemoveMiddle(t *testing.T) {
	q := NewOrderQueue()
	a := newLimitOrder(t, 1, 10025)
	b := newLimitOrder(t, 2, 10025)
	c := newLimitOrder(t, 3, 10025)
	_, _ = q.Push(a)
	nb, _ := q.Push(b)
	_, _ = q.Push(c)

	if err := q.Remove(nb); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := orderIDs(collectOrders(q)); !equalIDs(got, []order.OrderID{1, 3}) {
		t.Fatalf("expected [1 3] after removing B, got %v", got)
	}
	if q.Len() != 2 {
		t.Fatalf("expected len 2, got %d", q.Len())
	}
	mustCheck(t, q)

	// Cleanup removed node pointers.
	if nb.prev != nil || nb.next != nil || nb.q != nil {
		t.Fatal("removed node must have links cleared")
	}
}

func TestOrderQueueRemoveTail(t *testing.T) {
	q := NewOrderQueue()
	a := newLimitOrder(t, 1, 10025)
	b := newLimitOrder(t, 2, 10025)
	c := newLimitOrder(t, 3, 10025)
	_, _ = q.Push(a)
	_, _ = q.Push(b)
	nc, _ := q.Push(c)

	if err := q.Remove(nc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := orderIDs(collectOrders(q)); !equalIDs(got, []order.OrderID{1, 2}) {
		t.Fatalf("expected [1 2] after removing tail, got %v", got)
	}
	if q.tail.Order.ID != b.ID {
		t.Fatal("expected new tail to be B")
	}
	if q.tail.next != nil {
		t.Fatal("expected new tail.next == nil")
	}
	mustCheck(t, q)
}

func TestOrderQueueRemoveOnlyElement(t *testing.T) {
	q := NewOrderQueue()
	a := newLimitOrder(t, 1, 10025)
	n, _ := q.Push(a)

	if err := q.Remove(n); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.Len() != 0 || !q.IsEmpty() {
		t.Fatal("expected empty queue after removing only element")
	}
	if q.head != nil || q.tail != nil {
		t.Fatal("expected head and tail to be nil")
	}
	mustCheck(t, q)
}

func TestOrderQueueRemoveMissing(t *testing.T) {
	q := NewOrderQueue()
	a := newLimitOrder(t, 1, 10025)
	_, _ = q.Push(a)

	t.Run("nil node", func(t *testing.T) {
		if err := q.Remove(nil); !errors.Is(err, ErrNodeNotFound) {
			t.Fatalf("expected ErrNodeNotFound, got %v", err)
		}
	})

	t.Run("node from another queue", func(t *testing.T) {
		other := NewOrderQueue()
		x := newLimitOrder(t, 9, 10025)
		nx, _ := other.Push(x)
		if err := q.Remove(nx); !errors.Is(err, ErrNodeNotFound) {
			t.Fatalf("expected ErrNodeNotFound, got %v", err)
		}
	})

	t.Run("double removal", func(t *testing.T) {
		q2 := NewOrderQueue()
		n, _ := q2.Push(newLimitOrder(t, 1, 10025))
		if err := q2.Remove(n); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := q2.Remove(n); !errors.Is(err, ErrNodeNotFound) {
			t.Fatalf("expected ErrNodeNotFound on double removal, got %v", err)
		}
	})

	t.Run("pop then remove", func(t *testing.T) {
		q3 := NewOrderQueue()
		x := newLimitOrder(t, 2, 10025)
		nx, _ := q3.Push(x)
		if got := q3.Pop(); got != x {
			t.Fatal("expected pop to return the order")
		}
		if err := q3.Remove(nx); !errors.Is(err, ErrNodeNotFound) {
			t.Fatalf("expected ErrNodeNotFound after pop, got %v", err)
		}
	})

	mustCheck(t, q)
}

func TestOrderQueueRepeatedOperations(t *testing.T) {
	q := NewOrderQueue()
	orders := make([]*order.Order, 6)
	nodes := make([]*Node, 6)
	for i := 0; i < 6; i++ {
		orders[i] = newLimitOrder(t, order.OrderID(i+1), 10025)
		n, err := q.Push(orders[i])
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		nodes[i] = n
		mustCheck(t, q)
	}

	if got := q.Pop(); got != orders[0] {
		t.Fatalf("expected pop A, got %d", got.ID)
	}
	mustCheck(t, q)

	if _, err := q.Push(newLimitOrder(t, 7, 10025)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustCheck(t, q)

	// Remove middle: node 3 (C in 1..7, currently B=2,C=3,...,G=7).
	if err := q.Remove(nodes[2]); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustCheck(t, q)

	if err := q.Remove(nodes[1]); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustCheck(t, q)

	if got := q.Pop(); got != orders[3] {
		t.Fatalf("expected pop D, got %d", got.ID)
	}
	if got := q.Pop(); got != orders[4] {
		t.Fatalf("expected pop E, got %d", got.ID)
	}
	mustCheck(t, q)

	if got := orderIDs(collectOrders(q)); !equalIDs(got, []order.OrderID{6, 7}) {
		t.Fatalf("expected [6 7], got %v", got)
	}

	if got := q.Pop(); got != orders[5] {
		t.Fatalf("expected pop F, got %d", got.ID)
	}
	if got := q.Pop(); got == nil {
		t.Fatal("expected pop G")
	}
	if !q.IsEmpty() {
		t.Fatal("expected empty queue")
	}
	mustCheck(t, q)
}

func TestOrderQueuePushNil(t *testing.T) {
	q := NewOrderQueue()
	if _, err := q.Push(nil); !errors.Is(err, ErrNilOrder) {
		t.Fatalf("expected ErrNilOrder, got %v", err)
	}
	if q.Len() != 0 {
		t.Fatalf("expected len 0, got %d", q.Len())
	}
}

func equalIDs(a, b []order.OrderID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
