package book

import (
	"errors"
	"testing"

	"github.com/gurshaan17/apex/internal/order"
)

func TestPriceLevelCreate(t *testing.T) {
	pl := NewPriceLevel(10025)
	if pl.Price() != 10025 {
		t.Fatalf("expected price 10025, got %d", pl.Price())
	}
	if pl.Len() != 0 {
		t.Fatalf("expected len 0, got %d", pl.Len())
	}
	if !pl.IsEmpty() {
		t.Fatal("expected empty price level")
	}
	if pl.Front() != nil {
		t.Fatal("expected nil front")
	}
	if pl.Pop() != nil {
		t.Fatal("expected nil pop on empty level")
	}
	mustCheck(t, pl)
}

func TestPriceLevelAddOrders(t *testing.T) {
	pl := NewPriceLevel(10025)
	a := newLimitOrder(t, 1, 10025)
	b := newLimitOrder(t, 2, 10025)
	c := newLimitOrder(t, 3, 10025)

	na, err := pl.Add(a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if na.Order != a {
		t.Fatal("expected Add to return node wrapping the order")
	}
	if _, err := pl.Add(b); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := pl.Add(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pl.Len() != 3 {
		t.Fatalf("expected len 3, got %d", pl.Len())
	}
	if pl.Front() != a {
		t.Fatal("expected front order A")
	}
	mustCheck(t, pl)
}

func TestPriceLevelRejectWrongPrice(t *testing.T) {
	pl := NewPriceLevel(10025)
	a := newLimitOrder(t, 1, 10025)
	if _, err := pl.Add(a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Run("different higher price", func(t *testing.T) {
		wrong := newLimitOrder(t, 2, 10100)
		if _, err := pl.Add(wrong); !errors.Is(err, ErrWrongPrice) {
			t.Fatalf("expected ErrWrongPrice, got %v", err)
		}
	})

	t.Run("different lower price", func(t *testing.T) {
		wrong := newLimitOrder(t, 3, 10000)
		if _, err := pl.Add(wrong); !errors.Is(err, ErrWrongPrice) {
			t.Fatalf("expected ErrWrongPrice, got %v", err)
		}
	})

	t.Run("market order (zero price)", func(t *testing.T) {
		mkt := order.NewOrder(4, "AAPL", order.Buy, order.Market, 0, 100)
		if _, err := pl.Add(&mkt); !errors.Is(err, ErrWrongPrice) {
			t.Fatalf("expected ErrWrongPrice for market order, got %v", err)
		}
	})

	t.Run("nil order", func(t *testing.T) {
		if _, err := pl.Add(nil); !errors.Is(err, ErrNilOrder) {
			t.Fatalf("expected ErrNilOrder, got %v", err)
		}
	})

	if pl.Len() != 1 {
		t.Fatalf("expected len unchanged at 1, got %d", pl.Len())
	}
	mustCheck(t, pl)
}

func TestPriceLevelFIFO(t *testing.T) {
	pl := NewPriceLevel(10025)
	a := newLimitOrder(t, 1, 10025)
	b := newLimitOrder(t, 2, 10025)
	c := newLimitOrder(t, 3, 10025)
	for _, o := range []*order.Order{a, b, c} {
		if _, err := pl.Add(o); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if got := pl.Pop(); got != a {
		t.Fatalf("expected pop A, got %d", got.ID)
	}
	if got := pl.Pop(); got != b {
		t.Fatalf("expected pop B, got %d", got.ID)
	}
	if got := pl.Pop(); got != c {
		t.Fatalf("expected pop C, got %d", got.ID)
	}
	if !pl.IsEmpty() {
		t.Fatal("expected empty level after pops")
	}
	mustCheck(t, pl)
}

func TestPriceLevelRemoveOrder(t *testing.T) {
	pl := NewPriceLevel(10025)
	a := newLimitOrder(t, 1, 10025)
	b := newLimitOrder(t, 2, 10025)
	c := newLimitOrder(t, 3, 10025)
	_, _ = pl.Add(a)
	nb, _ := pl.Add(b)
	_, _ = pl.Add(c)

	if err := pl.Remove(nb); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Front() != a {
		t.Fatal("expected front A after removing B")
	}
	if pl.Len() != 2 {
		t.Fatalf("expected len 2, got %d", pl.Len())
	}
	mustCheck(t, pl)

	if got := pl.Pop(); got != a {
		t.Fatalf("expected pop A, got %d", got.ID)
	}
	if got := pl.Pop(); got != c {
		t.Fatalf("expected pop C, got %d", got.ID)
	}
	if !pl.IsEmpty() {
		t.Fatal("expected empty level")
	}
	mustCheck(t, pl)
}

func TestPriceLevelRemoveForeignNode(t *testing.T) {
	pl := NewPriceLevel(10025)
	other := NewPriceLevel(10025)
	_, _ = pl.Add(newLimitOrder(t, 1, 10025))
	no, _ := other.Add(newLimitOrder(t, 2, 10025))

	if err := pl.Remove(no); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("expected ErrNodeNotFound, got %v", err)
	}
	if pl.Len() != 1 {
		t.Fatalf("expected len 1, got %d", pl.Len())
	}
	mustCheck(t, pl)
	mustCheck(t, other)
}

func TestPriceLevelEmptyAfterRemoveAll(t *testing.T) {
	pl := NewPriceLevel(10025)
	a := newLimitOrder(t, 1, 10025)
	b := newLimitOrder(t, 2, 10025)
	na, _ := pl.Add(a)
	nb, _ := pl.Add(b)

	if err := pl.Remove(na); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.IsEmpty() {
		t.Fatal("expected non-empty after removing one")
	}
	if err := pl.Remove(nb); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pl.IsEmpty() {
		t.Fatal("expected empty level after removing all")
	}
	if pl.Front() != nil {
		t.Fatal("expected nil front on empty level")
	}
	mustCheck(t, pl)
}

func TestPriceLevelFrontPreservesNode(t *testing.T) {
	pl := NewPriceLevel(10025)
	a := newLimitOrder(t, 1, 10025)
	na, _ := pl.Add(a)

	if pl.FrontNode() != na {
		t.Fatal("expected FrontNode to return added node")
	}
	// Front must not remove.
	if pl.Front() != a || pl.Len() != 1 {
		t.Fatal("Front must be non-destructive")
	}
	mustCheck(t, pl)
}
