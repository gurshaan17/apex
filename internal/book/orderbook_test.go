package book

import (
	"errors"
	"testing"

	"github.com/gurshaan17/apex/internal/order"
)

func bookBid(t *testing.T, id order.OrderID, price order.Price, qty order.Quantity) *order.Order {
	t.Helper()
	o := order.NewOrder(id, "AAPL", order.Buy, order.Limit, price, qty)
	if err := o.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return &o
}

func bookAsk(t *testing.T, id order.OrderID, price order.Price, qty order.Quantity) *order.Order {
	t.Helper()
	o := order.NewOrder(id, "AAPL", order.Sell, order.Limit, price, qty)
	if err := o.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return &o
}

func mustAdd(t *testing.T, ob *OrderBook, o *order.Order) {
	t.Helper()
	if err := ob.Add(o); err != nil {
		t.Fatalf("Add(%d) failed: %v", o.ID, err)
	}
}

func mustRemove(t *testing.T, ob *OrderBook, id order.OrderID) {
	t.Helper()
	if err := ob.Remove(id); err != nil {
		t.Fatalf("Remove(%d) failed: %v", id, err)
	}
}

func snapshotPrices(snaps []LevelSnapshot) []order.Price {
	out := make([]order.Price, len(snaps))
	for i, s := range snaps {
		out[i] = s.Price
	}
	return out
}

func snapshotOrderIDs(snaps []LevelSnapshot) [][]order.OrderID {
	out := make([][]order.OrderID, len(snaps))
	for i, s := range snaps {
		for _, o := range s.Orders {
			out[i] = append(out[i], o.ID)
		}
	}
	return out
}

func mustCheckBook(t *testing.T, ob *OrderBook) {
	t.Helper()
	if err := ob.checkInvariants(); err != nil {
		t.Fatalf("invariants violated: %v", err)
	}
}

func TestBookAddFirstBid(t *testing.T) {
	ob := NewOrderBook()
	o := bookBid(t, 1, 10500, 100)
	mustAdd(t, ob, o)
	if ob.Len() != 1 {
		t.Fatalf("expected len 1, got %d", ob.Len())
	}
	if ob.BestBid() != 10500 {
		t.Fatalf("expected best bid 10500, got %d", ob.BestBid())
	}
	if o.Status != order.Open {
		t.Fatalf("expected order to be OPEN after Add, got %s", o.Status)
	}
	mustCheckBook(t, ob)
}

func TestBookAddMultipleBidsAndAsks(t *testing.T) {
	ob := NewOrderBook()
	mustAdd(t, ob, bookBid(t, 1, 10400, 100))
	mustAdd(t, ob, bookBid(t, 2, 10500, 100))
	mustAdd(t, ob, bookBid(t, 3, 10300, 100))
	mustAdd(t, ob, bookAsk(t, 4, 10600, 100))
	mustAdd(t, ob, bookAsk(t, 5, 10800, 100))
	mustAdd(t, ob, bookAsk(t, 6, 10700, 100))

	if ob.Len() != 6 {
		t.Fatalf("expected len 6, got %d", ob.Len())
	}
	if ob.BestBid() != 10500 {
		t.Fatalf("expected best bid 10500, got %d", ob.BestBid())
	}
	if ob.BestAsk() != 10600 {
		t.Fatalf("expected best ask 10600, got %d", ob.BestAsk())
	}
	mustCheckBook(t, ob)

	snap := ob.Snapshot()
	if got := snapshotPrices(snap.Bids); !equalPrice(got, []order.Price{10500, 10400, 10300}) {
		t.Fatalf("expected bid prices [10500 10400 10300], got %v", got)
	}
	if got := snapshotPrices(snap.Asks); !equalPrice(got, []order.Price{10600, 10700, 10800}) {
		t.Fatalf("expected ask prices [10600 10700 10800], got %v", got)
	}
}

func TestBookSamePriceFIFO(t *testing.T) {
	ob := NewOrderBook()
	mustAdd(t, ob, bookBid(t, 1, 10500, 10))
	mustAdd(t, ob, bookBid(t, 2, 10500, 20))
	mustAdd(t, ob, bookBid(t, 3, 10500, 30))

	snap := ob.Snapshot()
	if got := snapshotOrderIDs(snap.Bids); !equalNestedIDs(got, [][]order.OrderID{{1, 2, 3}}) {
		t.Fatalf("expected FIFO [1 2 3] at 10500, got %v", got)
	}
	if sum := snap.Bids[0].Qty; sum != 60 {
		t.Fatalf("expected level qty 60, got %d", sum)
	}
	mustCheckBook(t, ob)
}

func TestBookBestBidAndBestAskEmpty(t *testing.T) {
	ob := NewOrderBook()
	if ob.BestBid() != 0 {
		t.Fatalf("expected empty best bid 0, got %d", ob.BestBid())
	}
	if ob.BestAsk() != 0 {
		t.Fatalf("expected empty best ask 0, got %d", ob.BestAsk())
	}

	mustAdd(t, ob, bookBid(t, 1, 10500, 100))
	if ob.BestAsk() != 0 {
		t.Fatalf("expected empty best ask 0 with only bids, got %d", ob.BestAsk())
	}

	ob2 := NewOrderBook()
	mustAdd(t, ob2, bookAsk(t, 2, 10600, 100))
	if ob2.BestBid() != 0 {
		t.Fatalf("expected empty best bid 0 with only asks, got %d", ob2.BestBid())
	}
}

func TestBookOrdering(t *testing.T) {
	ob := NewOrderBook()
	mustAdd(t, ob, bookBid(t, 1, 10300, 100))
	mustAdd(t, ob, bookBid(t, 2, 10500, 100))
	mustAdd(t, ob, bookBid(t, 3, 10400, 100))

	snapB := ob.Snapshot().Bids
	for i := 1; i < len(snapB); i++ {
		if snapB[i-1].Price < snapB[i].Price {
			t.Fatalf("bid prices not descending: %v", snapshotPrices(snapB))
		}
	}

	ob2 := NewOrderBook()
	mustAdd(t, ob2, bookAsk(t, 1, 10800, 100))
	mustAdd(t, ob2, bookAsk(t, 2, 10600, 100))
	mustAdd(t, ob2, bookAsk(t, 3, 10700, 100))

	snapA := ob2.Snapshot().Asks
	for i := 1; i < len(snapA); i++ {
		if snapA[i-1].Price > snapA[i].Price {
			t.Fatalf("ask prices not ascending: %v", snapshotPrices(snapA))
		}
	}
}

func TestBookRemoval(t *testing.T) {
	t.Run("remove head", func(t *testing.T) {
		ob := NewOrderBook()
		mustAdd(t, ob, bookBid(t, 1, 10500, 10))
		mustAdd(t, ob, bookBid(t, 2, 10500, 20))
		mustAdd(t, ob, bookBid(t, 3, 10500, 30))
		mustRemove(t, ob, 1)
		if got := snapshotOrderIDs(ob.Snapshot().Bids); !equalNestedIDs(got, [][]order.OrderID{{2, 3}}) {
			t.Fatalf("expected [2 3] after removing head, got %v", got)
		}
		mustCheckBook(t, ob)
	})

	t.Run("remove middle", func(t *testing.T) {
		ob := NewOrderBook()
		mustAdd(t, ob, bookBid(t, 1, 10500, 10))
		mustAdd(t, ob, bookBid(t, 2, 10500, 20))
		mustAdd(t, ob, bookBid(t, 3, 10500, 30))
		mustRemove(t, ob, 2)
		if got := snapshotOrderIDs(ob.Snapshot().Bids); !equalNestedIDs(got, [][]order.OrderID{{1, 3}}) {
			t.Fatalf("expected [1 3] after removing middle, got %v", got)
		}
		mustCheckBook(t, ob)
	})

	t.Run("remove tail", func(t *testing.T) {
		ob := NewOrderBook()
		mustAdd(t, ob, bookBid(t, 1, 10500, 10))
		mustAdd(t, ob, bookBid(t, 2, 10500, 20))
		mustAdd(t, ob, bookBid(t, 3, 10500, 30))
		mustRemove(t, ob, 3)
		if got := snapshotOrderIDs(ob.Snapshot().Bids); !equalNestedIDs(got, [][]order.OrderID{{1, 2}}) {
			t.Fatalf("expected [1 2] after removing tail, got %v", got)
		}
		mustCheckBook(t, ob)
	})

	t.Run("remove final order removes level", func(t *testing.T) {
		ob := NewOrderBook()
		mustAdd(t, ob, bookBid(t, 1, 10500, 10))
		mustAdd(t, ob, bookBid(t, 2, 10400, 10))
		mustRemove(t, ob, 1)
		if ob.BestBid() != 10400 {
			t.Fatalf("expected best bid 10400, got %d", ob.BestBid())
		}
		if got := snapshotPrices(ob.Snapshot().Bids); !equalPrice(got, []order.Price{10400}) {
			t.Fatalf("expected only price 10400 remaining, got %v", got)
		}
		mustCheckBook(t, ob)
	})

	t.Run("remove entire price level", func(t *testing.T) {
		ob := NewOrderBook()
		mustAdd(t, ob, bookBid(t, 1, 10500, 10))
		mustAdd(t, ob, bookBid(t, 2, 10500, 20))
		mustAdd(t, ob, bookBid(t, 3, 10400, 10))
		mustRemove(t, ob, 1)
		mustRemove(t, ob, 2)
		if ob.BestBid() != 10400 {
			t.Fatalf("expected best bid 10400 after removing both 10500 orders, got %d", ob.BestBid())
		}
		mustCheckBook(t, ob)
	})

	t.Run("remove best-bid level exposes next best", func(t *testing.T) {
		ob := NewOrderBook()
		mustAdd(t, ob, bookBid(t, 1, 10500, 10))
		mustAdd(t, ob, bookBid(t, 2, 10400, 10))
		mustAdd(t, ob, bookBid(t, 3, 10000, 10))
		mustRemove(t, ob, 1)
		if ob.BestBid() != 10400 {
			t.Fatalf("expected best bid 10400, got %d", ob.BestBid())
		}
		mustRemove(t, ob, 2)
		if ob.BestBid() != 10000 {
			t.Fatalf("expected best bid 10000, got %d", ob.BestBid())
		}
		mustCheckBook(t, ob)
	})
}

func TestBookLookup(t *testing.T) {
	ob := NewOrderBook()
	mustAdd(t, ob, bookBid(t, 1, 10500, 100))
	mustAdd(t, ob, bookAsk(t, 2, 10600, 100))

	o, err := ob.Get(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.ID != 1 || o.Price != 10500 || o.Side != order.Buy {
		t.Fatalf("unexpected order: %+v", o)
	}

	loc, ok := ob.byID[order.OrderID(1)]
	if !ok {
		t.Fatal("order 1 should be in the index")
	}
	if loc.side != order.Buy || loc.price != 10500 || loc.node == nil {
		t.Fatalf("unexpected index location: %+v", loc)
	}
	if loc.node.Order.ID != 1 {
		t.Fatal("index node should hold order 1")
	}

	o2, err := ob.Get(2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o2.Side != order.Sell {
		t.Fatalf("expected SELL order, got %s", o2.Side)
	}
}

func TestBookIndexUpdatedAfterRemoval(t *testing.T) {
	ob := NewOrderBook()
	mustAdd(t, ob, bookBid(t, 1, 10500, 100))
	mustRemove(t, ob, 1)

	if _, err := ob.Get(1); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("expected ErrOrderNotFound, got %v", err)
	}
	if _, ok := ob.byID[order.OrderID(1)]; ok {
		t.Fatal("index should no longer contain removed order")
	}
	mustCheckBook(t, ob)
}

func TestBookDuplicateID(t *testing.T) {
	ob := NewOrderBook()
	o := bookBid(t, 1, 10500, 100)
	mustAdd(t, ob, o)
	if err := ob.Add(bookBid(t, 1, 10600, 100)); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("expected ErrDuplicateID, got %v", err)
	}
	if ob.Len() != 1 {
		t.Fatalf("expected len 1, got %d", ob.Len())
	}
}

func TestBookUnknownRemoval(t *testing.T) {
	ob := NewOrderBook()
	mustAdd(t, ob, bookBid(t, 1, 10500, 100))
	if err := ob.Remove(99); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("expected ErrOrderNotFound, got %v", err)
	}
}

func TestBookEmptyBook(t *testing.T) {
	ob := NewOrderBook()
	if ob.Len() != 0 {
		t.Fatalf("expected len 0, got %d", ob.Len())
	}
	snap := ob.Snapshot()
	if len(snap.Bids) != 0 || len(snap.Asks) != 0 {
		t.Fatalf("expected empty snapshot, got bids=%d asks=%d", len(snap.Bids), len(snap.Asks))
	}
	mustCheckBook(t, ob)
}

func TestBookInvalidOrder(t *testing.T) {
	ob := NewOrderBook()

	t.Run("nil order", func(t *testing.T) {
		if err := ob.Add(nil); !errors.Is(err, ErrNilOrder) {
			t.Fatalf("expected ErrNilOrder, got %v", err)
		}
	})

	t.Run("invalid price", func(t *testing.T) {
		o := order.NewOrder(1, "AAPL", order.Buy, order.Limit, 0, 100)
		if err := ob.Add(&o); !errors.Is(err, order.ErrInvalidPrice) {
			t.Fatalf("expected ErrInvalidPrice, got %v", err)
		}
	})

	t.Run("market order", func(t *testing.T) {
		o := order.NewOrder(2, "AAPL", order.Buy, order.Market, 0, 100)
		if err := ob.Add(&o); !errors.Is(err, ErrMarketOrderUnsupported) {
			t.Fatalf("expected ErrMarketOrderUnsupported, got %v", err)
		}
	})
}

func TestBookCrossedAllowed(t *testing.T) {
	// Crossing orders must NOT be matched or rejected at this stage.
	ob := NewOrderBook()
	mustAdd(t, ob, bookBid(t, 1, 10100, 100))
	mustAdd(t, ob, bookAsk(t, 2, 10000, 100))
	if ob.BestBid() != 10100 {
		t.Fatalf("expected best bid 10100, got %d", ob.BestBid())
	}
	if ob.BestAsk() != 10000 {
		t.Fatalf("expected best ask 10000, got %d", ob.BestAsk())
	}
	mustCheckBook(t, ob)
}

func TestBookSnapshotIsReadOnly(t *testing.T) {
	ob := NewOrderBook()
	mustAdd(t, ob, bookBid(t, 1, 10500, 100))
	snap := ob.Snapshot()

	// Mutating the snapshot must not affect the book.
	snap.Bids[0].Orders[0].Qty = 999
	snap.Bids[0].Orders[0].Price = 99999
	snap.Bids[0].Price = 123
	snap.Bids = append(snap.Bids, LevelSnapshot{})

	if ob.BestBid() != 10500 {
		t.Fatalf("book best bid must be unaffected by snapshot mutation, got %d", ob.BestBid())
	}
	got, _ := ob.Get(1)
	if got.Qty != 100 || got.Price != 10500 {
		t.Fatalf("book order must be unaffected, got qty=%d price=%d", got.Qty, got.Price)
	}
	if len(ob.Snapshot().Bids) != 1 {
		t.Fatal("book must still have exactly one bid level")
	}
}

func TestBookRemovalDoesNotChangeOrderStatus(t *testing.T) {
	ob := NewOrderBook()
	o := bookBid(t, 1, 10500, 100)
	mustAdd(t, ob, o)
	mustRemove(t, ob, 1)
	if o.Status != order.Open {
		t.Fatalf("expected status still OPEN after book removal, got %s", o.Status)
	}
}

func equalPrice(a, b []order.Price) bool {
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

func equalNestedIDs(a, b [][]order.OrderID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalIDs(a[i], b[i]) {
			return false
		}
	}
	return true
}
