package book

import (
	"fmt"

	"github.com/gurshaan17/apex/internal/order"
)

// Node is a single element in an OrderQueue. It holds a reference to the
// order plus the linked-list links. The node itself is the removal handle:
// the caller receives it from Push and passes it back to Remove for O(1)
// removal without scanning.
type Node struct {
	Order *order.Order
	prev  *Node
	next  *Node
	q     *OrderQueue
}

// OrderQueue is a FIFO queue of orders implemented as a doubly linked list.
// A doubly linked list is chosen because later cancellation requires
// efficient removal of an order from the middle of a price level, which the
// node reference makes O(1).
type OrderQueue struct {
	head *Node
	tail *Node
	len  int
}

// NewOrderQueue returns an empty OrderQueue.
func NewOrderQueue() *OrderQueue {
	return &OrderQueue{}
}

// Len returns the number of orders in the queue.
func (q *OrderQueue) Len() int {
	return q.len
}

// IsEmpty reports whether the queue contains no orders.
func (q *OrderQueue) IsEmpty() bool {
	return q.len == 0
}

// Push appends an order to the tail of the queue and returns the new node.
// Returning the node is important: the future order book can store it to
// cancel the order later in O(1) time.
func (q *OrderQueue) Push(o *order.Order) (*Node, error) {
	if o == nil {
		return nil, ErrNilOrder
	}
	n := &Node{Order: o, q: q}
	if q.tail == nil {
		q.head = n
	} else {
		q.tail.next = n
		n.prev = q.tail
	}
	q.tail = n
	q.len++
	return n, nil
}

// Front returns the order at the head of the queue without removing it.
// It returns nil if the queue is empty.
func (q *OrderQueue) Front() *order.Order {
	if q.head == nil {
		return nil
	}
	return q.head.Order
}

// FrontNode returns the head node of the queue without removing it.
// It returns nil if the queue is empty.
func (q *OrderQueue) FrontNode() *Node {
	return q.head
}

// Pop removes and returns the order at the head of the queue (FIFO order).
// It returns nil if the queue is empty.
func (q *OrderQueue) Pop() *order.Order {
	if q.head == nil {
		return nil
	}
	n := q.head
	o := n.Order
	q.unlink(n)
	return o
}

// Remove unlinks an arbitrary node from the queue in O(1) time. The node
// must belong to this queue; otherwise ErrNodeNotFound is returned and the
// queue is left unmodified.
func (q *OrderQueue) Remove(n *Node) error {
	if n == nil || n.q != q {
		return ErrNodeNotFound
	}
	q.unlink(n)
	return nil
}

// unlink removes n from the queue and clears its links so a removed node
// cannot be double-removed or leak back into the structure.
func (q *OrderQueue) unlink(n *Node) {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		q.head = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		q.tail = n.prev
	}
	n.prev = nil
	n.next = nil
	n.q = nil
	q.len--
}

// checkInvariants verifies the structural invariants of the queue. It is
// used by tests; production paths are O(1) and do not pay this cost.
func (q *OrderQueue) checkInvariants() error {
	if q.len < 0 {
		return fmt.Errorf("invariant violated: negative length %d", q.len)
	}
	if q.len == 0 {
		if q.head != nil || q.tail != nil {
			return fmt.Errorf("invariant violated: empty queue has head=%p tail=%p", q.head, q.tail)
		}
		return nil
	}
	if q.head == nil || q.tail == nil {
		return fmt.Errorf("invariant violated: non-empty queue missing head or tail")
	}
	if q.head.prev != nil {
		return fmt.Errorf("invariant violated: head.prev is not nil")
	}
	if q.tail.next != nil {
		return fmt.Errorf("invariant violated: tail.next is not nil")
	}
	seen := make(map[*Node]struct{}, q.len)
	count := 0
	for n := q.head; n != nil; n = n.next {
		if _, dup := seen[n]; dup {
			return fmt.Errorf("invariant violated: cycle detected at node %p", n)
		}
		seen[n] = struct{}{}
		if n.q != q {
			return fmt.Errorf("invariant violated: node %p owned by another queue", n)
		}
		if n.Order == nil {
			return fmt.Errorf("invariant violated: node %p has nil order", n)
		}
		count++
	}
	if count != q.len {
		return fmt.Errorf("invariant violated: traversal visited %d nodes but length is %d", count, q.len)
	}
	return nil
}
