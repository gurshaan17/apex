package order

import (
	"errors"
	"testing"
)

func TestValidOrders(t *testing.T) {
	tests := []struct {
		name  string
		typ   Type
		side  Side
		price Price
	}{
		{"valid limit BUY", Limit, Buy, 10025},
		{"valid limit SELL", Limit, Sell, 10025},
		{"valid market BUY", Market, Buy, 0},
		{"valid market SELL", Market, Sell, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := NewOrder(1, "AAPL", tt.side, tt.typ, tt.price, 100)
			if err := o.Validate(); err != nil {
				t.Fatalf("expected valid order, got error: %v", err)
			}
		})
	}
}

func TestInvalidOrders(t *testing.T) {
	tests := []struct {
		name    string
		id      OrderID
		symbol  string
		side    Side
		typ     Type
		price   Price
		qty     Quantity
		wantErr error
	}{
		{"zero ID", 0, "AAPL", Buy, Limit, 10025, 100, ErrInvalidOrderID},
		{"empty symbol", 1, "", Buy, Limit, 10025, 100, ErrEmptySymbol},
		{"invalid side", 1, "AAPL", SideUnknown, Limit, 10025, 100, ErrInvalidSide},
		{"invalid type", 1, "AAPL", Buy, TypeUnknown, 10025, 100, ErrInvalidType},
		{"zero quantity", 1, "AAPL", Buy, Limit, 10025, 0, ErrInvalidQuantity},
		{"negative quantity", 1, "AAPL", Buy, Limit, 10025, -10, ErrInvalidQuantity},
		{"zero limit price", 1, "AAPL", Buy, Limit, 0, 100, ErrInvalidPrice},
		{"negative limit price", 1, "AAPL", Buy, Limit, -10025, 100, ErrInvalidPrice},
		{"market order with price", 1, "AAPL", Buy, Market, 10025, 100, ErrMarketPriceSet},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := NewOrder(tt.id, tt.symbol, tt.side, tt.typ, tt.price, tt.qty)
			err := o.Validate()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestFill(t *testing.T) {
	t.Run("partial then full fill", func(t *testing.T) {
		o := NewOrder(1, "AAPL", Buy, Limit, 10025, 100)
		mustOpen(t, &o)

		if err := o.Fill(30); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.Filled != 30 {
			t.Fatalf("expected filled=30, got %d", o.Filled)
		}
		if o.Remaining() != 70 {
			t.Fatalf("expected remaining=70, got %d", o.Remaining())
		}
		if o.Status != PartiallyFilled {
			t.Fatalf("expected status PARTIALLY_FILLED, got %s", o.Status)
		}

		if err := o.Fill(70); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.Filled != 100 {
			t.Fatalf("expected filled=100, got %d", o.Filled)
		}
		if o.Remaining() != 0 {
			t.Fatalf("expected remaining=0, got %d", o.Remaining())
		}
		if o.Status != Filled {
			t.Fatalf("expected status FILLED, got %s", o.Status)
		}
	})

	t.Run("single fill to completion", func(t *testing.T) {
		o := NewOrder(1, "AAPL", Sell, Limit, 10025, 100)
		mustOpen(t, &o)

		if err := o.Fill(100); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.Status != Filled {
			t.Fatalf("expected status FILLED, got %s", o.Status)
		}
	})

	t.Run("overfill rejected", func(t *testing.T) {
		o := NewOrder(1, "AAPL", Buy, Limit, 10025, 100)
		mustOpen(t, &o)

		if err := o.Fill(101); !errors.Is(err, ErrOverfill) {
			t.Fatalf("expected ErrOverfill, got %v", err)
		}
		if o.Filled != 0 {
			t.Fatalf("overfill must not mutate order, got filled=%d", o.Filled)
		}
		if o.Status != Open {
			t.Fatalf("overfill must not mutate status, got %s", o.Status)
		}
	})

	t.Run("zero fill rejected", func(t *testing.T) {
		o := NewOrder(1, "AAPL", Buy, Limit, 10025, 100)
		mustOpen(t, &o)

		if err := o.Fill(0); !errors.Is(err, ErrInvalidQuantity) {
			t.Fatalf("expected ErrInvalidQuantity, got %v", err)
		}
	})

	t.Run("negative fill rejected", func(t *testing.T) {
		o := NewOrder(1, "AAPL", Buy, Limit, 10025, 100)
		mustOpen(t, &o)

		if err := o.Fill(-5); !errors.Is(err, ErrInvalidQuantity) {
			t.Fatalf("expected ErrInvalidQuantity, got %v", err)
		}
	})

	t.Run("fill on filled order rejected", func(t *testing.T) {
		o := NewOrder(1, "AAPL", Buy, Limit, 10025, 100)
		mustOpen(t, &o)
		mustFill(t, &o, 100)

		if err := o.Fill(1); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("expected ErrInvalidState, got %v", err)
		}
	})

	t.Run("fill on cancelled order rejected", func(t *testing.T) {
		o := NewOrder(1, "AAPL", Buy, Limit, 10025, 100)
		mustOpen(t, &o)
		if err := o.Cancel(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if err := o.Fill(1); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("expected ErrInvalidState, got %v", err)
		}
	})
}

func TestLifecycle(t *testing.T) {
	t.Run("new to open", func(t *testing.T) {
		o := NewOrder(1, "AAPL", Buy, Limit, 10025, 100)
		if o.Status != New {
			t.Fatalf("expected NEW, got %s", o.Status)
		}
		if err := o.Open(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.Status != Open {
			t.Fatalf("expected OPEN, got %s", o.Status)
		}
	})

	t.Run("open straight to filled", func(t *testing.T) {
		o := NewOrder(1, "AAPL", Buy, Limit, 10025, 100)
		mustOpen(t, &o)
		mustFill(t, &o, 100)
		if o.Status != Filled {
			t.Fatalf("expected FILLED, got %s", o.Status)
		}
	})

	t.Run("new cannot be cancelled", func(t *testing.T) {
		o := NewOrder(1, "AAPL", Buy, Limit, 10025, 100)
		if o.CanCancel() {
			t.Fatal("NEW order must not be cancellable")
		}
		if err := o.Cancel(); !errors.Is(err, ErrCannotCancel) {
			t.Fatalf("expected ErrCannotCancel, got %v", err)
		}
	})

	t.Run("open can be cancelled", func(t *testing.T) {
		o := NewOrder(1, "AAPL", Buy, Limit, 10025, 100)
		mustOpen(t, &o)
		if err := o.Cancel(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.Status != Cancelled {
			t.Fatalf("expected CANCELLED, got %s", o.Status)
		}
	})

	t.Run("partially filled can be cancelled", func(t *testing.T) {
		o := NewOrder(1, "AAPL", Buy, Limit, 10025, 100)
		mustOpen(t, &o)
		mustFill(t, &o, 30)
		if err := o.Cancel(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.Status != Cancelled {
			t.Fatalf("expected CANCELLED, got %s", o.Status)
		}
	})

	t.Run("filled cannot be cancelled", func(t *testing.T) {
		o := NewOrder(1, "AAPL", Buy, Limit, 10025, 100)
		mustOpen(t, &o)
		mustFill(t, &o, 100)
		if o.CanCancel() {
			t.Fatal("FILLED order must not be cancellable")
		}
		if err := o.Cancel(); !errors.Is(err, ErrCannotCancel) {
			t.Fatalf("expected ErrCannotCancel, got %v", err)
		}
	})

	t.Run("cancelled cannot be re-cancelled", func(t *testing.T) {
		o := NewOrder(1, "AAPL", Buy, Limit, 10025, 100)
		mustOpen(t, &o)
		mustCancel(t, &o)
		if err := o.Cancel(); !errors.Is(err, ErrCannotCancel) {
			t.Fatalf("expected ErrCannotCancel, got %v", err)
		}
	})

	t.Run("new cannot fill before open", func(t *testing.T) {
		o := NewOrder(1, "AAPL", Buy, Limit, 10025, 100)
		if err := o.Fill(10); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("expected ErrInvalidState, got %v", err)
		}
	})
}

func mustOpen(t *testing.T, o *Order) {
	t.Helper()
	if err := o.Open(); err != nil {
		t.Fatalf("failed to open order: %v", err)
	}
}

func mustFill(t *testing.T, o *Order, qty Quantity) {
	t.Helper()
	if err := o.Fill(qty); err != nil {
		t.Fatalf("failed to fill order: %v", err)
	}
}

func mustCancel(t *testing.T, o *Order) {
	t.Helper()
	if err := o.Cancel(); err != nil {
		t.Fatalf("failed to cancel order: %v", err)
	}
}

func TestSideString(t *testing.T) {
	if Buy.String() != "BUY" {
		t.Fatalf("expected BUY, got %s", Buy.String())
	}
	if Sell.String() != "SELL" {
		t.Fatalf("expected SELL, got %s", Sell.String())
	}
	if SideUnknown.String() != "UNKNOWN" {
		t.Fatalf("expected UNKNOWN, got %s", SideUnknown.String())
	}
}

func TestParseSide(t *testing.T) {
	s, err := ParseSide("BUY")
	if err != nil || s != Buy {
		t.Fatalf("expected Buy, got %v err %v", s, err)
	}
	s, err = ParseSide("SELL")
	if err != nil || s != Sell {
		t.Fatalf("expected Sell, got %v err %v", s, err)
	}
	if _, err := ParseSide("HOLD"); !errors.Is(err, ErrInvalidSide) {
		t.Fatalf("expected ErrInvalidSide, got %v", err)
	}
}

func TestParseType(t *testing.T) {
	ty, err := ParseType("LIMIT")
	if err != nil || ty != Limit {
		t.Fatalf("expected Limit, got %v err %v", ty, err)
	}
	ty, err = ParseType("MARKET")
	if err != nil || ty != Market {
		t.Fatalf("expected Market, got %v err %v", ty, err)
	}
	if _, err := ParseType("STOP"); !errors.Is(err, ErrInvalidType) {
		t.Fatalf("expected ErrInvalidType, got %v", err)
	}
}

func TestTypeString(t *testing.T) {
	if Limit.String() != "LIMIT" {
		t.Fatalf("expected LIMIT, got %s", Limit.String())
	}
	if Market.String() != "MARKET" {
		t.Fatalf("expected MARKET, got %s", Market.String())
	}
	if TypeUnknown.String() != "UNKNOWN" {
		t.Fatalf("expected UNKNOWN, got %s", TypeUnknown.String())
	}
}

func TestStatusString(t *testing.T) {
	cases := map[Status]string{
		New:             "NEW",
		Open:            "OPEN",
		PartiallyFilled: "PARTIALLY_FILLED",
		Filled:          "FILLED",
		Cancelled:       "CANCELLED",
		StatusUnknown:   "UNKNOWN",
	}
	for status, want := range cases {
		if got := status.String(); got != want {
			t.Fatalf("expected %s, got %s", want, got)
		}
	}
}
