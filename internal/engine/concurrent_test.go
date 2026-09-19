package engine

import (
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/gurshaan17/apex/internal/book"
	"github.com/gurshaan17/apex/internal/order"
)

// testEngine is the narrow interface shared by Engine and ConcurrentEngine so
// the concurrent suite drives either wrapper with identical scenario code.
type testEngine interface {
	SubmitOrder(order.Order) (SubmitResult, error)
	CancelOrder(order.OrderID) error
	GetOrder(order.OrderID) (*order.Order, error)
	GetOrderBookSnapshot() book.Snapshot
	CheckInvariants() error
}

// newConcurrent builds a ConcurrentEngine with the default buffer and
// registers Shutdown as a cleanup so the engine loop is never leaked, even on
// test failure.
func newConcurrent(t *testing.T) *ConcurrentEngine {
	t.Helper()
	ce := NewConcurrentEngine(0)
	t.Cleanup(ce.Shutdown)
	return ce
}

func mustSubmitC(t *testing.T, e testEngine, o order.Order) SubmitResult {
	t.Helper()
	res, err := e.SubmitOrder(o)
	if err != nil {
		t.Fatalf("SubmitOrder(%d) failed: %v", o.ID, err)
	}
	return res
}

func mustCancelC(t *testing.T, e testEngine, id order.OrderID) {
	t.Helper()
	if err := e.CancelOrder(id); err != nil {
		t.Fatalf("CancelOrder(%d) failed: %v", id, err)
	}
}

func mustGetC(t *testing.T, e testEngine, id order.OrderID) *order.Order {
	t.Helper()
	o, err := e.GetOrder(id)
	if err != nil {
		t.Fatalf("GetOrder(%d) failed: %v", id, err)
	}
	return o
}

func mustGetErrC(t *testing.T, e testEngine, id order.OrderID, want error) {
	t.Helper()
	if _, err := e.GetOrder(id); err == nil {
		t.Fatalf("GetOrder(%d): expected error %v, got nil", id, want)
	}
}

// captureBookC is the interface-based twin of captureBook (engine_test.go).
func captureBookC(t *testing.T, e testEngine) bookLevels {
	t.Helper()
	snap := e.GetOrderBookSnapshot()
	out := bookLevels{}
	for _, lvl := range snap.Bids {
		for _, o := range lvl.Orders {
			out.bids = append(out.bids, o.ID)
		}
	}
	for _, lvl := range snap.Asks {
		for _, o := range lvl.Orders {
			out.asks = append(out.asks, o.ID)
		}
	}
	return out
}

// runRichScenarioC is the interface-based twin of runRichScenario
// (engine_test.go) and drives the same mixed sequence.
func runRichScenarioC(t *testing.T, e testEngine) {
	t.Helper()
	mustSubmitC(t, e, mkLimit(1, order.Buy, 100, 100))  // rests
	mustSubmitC(t, e, mkLimit(2, order.Buy, 101, 100))  // rests
	mustSubmitC(t, e, mkLimit(3, order.Sell, 100, 150)) // fills 2 (100) then 1 (50)
	mustSubmitC(t, e, mkLimit(4, order.Buy, 99, 50))    // rests
	mustCancelC(t, e, 1)                                // partially filled -> cancelled
	mustSubmitC(t, e, mkLimit(5, order.Sell, 102, 100)) // rests
	mustSubmitC(t, e, mkLimit(6, order.Buy, 100, 120))  // rests (100 < best ask 102)
	mustSubmitC(t, e, mkLimit(7, order.Sell, 101, 80))  // fills 6 (80)
}

func TestConcurrentSubmitExactMatch(t *testing.T) {
	e := newConcurrent(t)
	subBuy := mustSubmitC(t, e, mkLimit(1, order.Buy, 100, 100))
	subSell := mustSubmitC(t, e, mkLimit(2, order.Sell, 100, 100))

	if subBuy.Status != order.Open {
		t.Fatalf("first order should rest as OPEN, got %s", subBuy.Status)
	}
	if len(subSell.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(subSell.Trades))
	}
	assertTrade(t, subSell.Trades[0], 1, 1, 2, 100, 100)

	if o := mustGetC(t, e, 1); o.Status != order.Filled {
		t.Errorf("buy order status = %s, want FILLED", o.Status)
	}
	if o := mustGetC(t, e, 2); o.Status != order.Filled {
		t.Errorf("sell order status = %s, want FILLED", o.Status)
	}

	snap := e.GetOrderBookSnapshot()
	if len(snap.Bids) != 0 || len(snap.Asks) != 0 {
		t.Errorf("book must be empty after exact match, got bids=%d asks=%d", len(snap.Bids), len(snap.Asks))
	}
}

func TestConcurrentSubmitBuyCrossesSell(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Sell, 100, 100))
	r := mustSubmitC(t, e, mkLimit(2, order.Buy, 101, 50))

	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 2, 1, 100, 50)

	sell := mustGetC(t, e, 1)
	if sell.Status != order.PartiallyFilled || sell.Filled != 50 || sell.Remaining() != 50 {
		t.Errorf("sell order = %s filled=%d remaining=%d, want PARTIAL 50/50", sell.Status, sell.Filled, sell.Remaining())
	}
	if lvl := e.GetOrderBookSnapshot().Asks; len(lvl) != 1 || lvl[0].Qty != 50 {
		t.Errorf("ask side should hold 50 remaining, got %+v", lvl)
	}
}

func TestConcurrentSubmitSellCrossesBuy(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Buy, 104, 100))
	r := mustSubmitC(t, e, mkLimit(2, order.Sell, 100, 40))

	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 1, 2, 104, 40)

	buy := mustGetC(t, e, 1)
	if buy.Status != order.PartiallyFilled || buy.Filled != 40 || buy.Remaining() != 60 {
		t.Errorf("buy order = %s filled=%d remaining=%d, want PARTIAL 40/60", buy.Status, buy.Filled, buy.Remaining())
	}
}

func TestConcurrentNonCrossingBuyRests(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Sell, 100, 100))
	r := mustSubmitC(t, e, mkLimit(2, order.Buy, 99, 100))

	if len(r.Trades) != 0 {
		t.Fatalf("expected no trades, got %d", len(r.Trades))
	}
	if r.Status != order.Open || r.Remaining != 100 {
		t.Errorf("buy result = %s remaining=%d, want OPEN 100", r.Status, r.Remaining)
	}
}

func TestConcurrentNonCrossingSellRests(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Buy, 100, 100))
	r := mustSubmitC(t, e, mkLimit(2, order.Sell, 101, 100))

	if len(r.Trades) != 0 {
		t.Fatalf("expected no trades, got %d", len(r.Trades))
	}
	if r.Status != order.Open || r.Remaining != 100 {
		t.Errorf("sell result = %s remaining=%d, want OPEN 100", r.Status, r.Remaining)
	}
}

func TestConcurrentPricePriorityBids(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Buy, 101, 10))
	mustSubmitC(t, e, mkLimit(2, order.Buy, 100, 10))
	mustSubmitC(t, e, mkLimit(3, order.Buy, 99, 10))

	r := mustSubmitC(t, e, mkLimit(4, order.Sell, 100, 10))
	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 1, 4, 101, 10)
}

func TestConcurrentPricePriorityAsks(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Sell, 100, 10))
	mustSubmitC(t, e, mkLimit(2, order.Sell, 101, 10))
	mustSubmitC(t, e, mkLimit(3, order.Sell, 102, 10))

	r := mustSubmitC(t, e, mkLimit(4, order.Buy, 102, 10))
	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 4, 1, 100, 10)
}

func TestConcurrentTimePriorityFIFO(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Buy, 100, 100))
	mustSubmitC(t, e, mkLimit(2, order.Buy, 100, 100))
	mustSubmitC(t, e, mkLimit(3, order.Buy, 100, 100))

	r := mustSubmitC(t, e, mkLimit(4, order.Sell, 100, 250))
	if len(r.Trades) != 3 {
		t.Fatalf("expected 3 trades, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 1, 4, 100, 100)
	assertTrade(t, r.Trades[1], 2, 2, 4, 100, 100)
	assertTrade(t, r.Trades[2], 3, 3, 4, 100, 50)

	o3 := mustGetC(t, e, 3)
	if o3.Status != order.PartiallyFilled || o3.Remaining() != 50 {
		t.Errorf("order 3 = %s remaining=%d, want PARTIAL 50", o3.Status, o3.Remaining())
	}
	lvl := e.GetOrderBookSnapshot().Bids
	if len(lvl) != 1 || lvl[0].Qty != 50 {
		t.Errorf("bid side should hold 50 remaining, got %+v", lvl)
	}
}

func TestConcurrentPartialFillIncomingSmaller(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Sell, 100, 100))
	r := mustSubmitC(t, e, mkLimit(2, order.Buy, 100, 40))

	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 2, 1, 100, 40)
	if r.FilledQty != 40 || r.Remaining != 0 || r.Status != order.Filled {
		t.Errorf("buy result filled=%d remaining=%d status=%s, want 40/0 FILLED", r.FilledQty, r.Remaining, r.Status)
	}
	sell := mustGetC(t, e, 1)
	if sell.Filled != 40 || sell.Remaining() != 60 || sell.Status != order.PartiallyFilled {
		t.Errorf("sell order = %s filled=%d remaining=%d, want PARTIAL 40/60", sell.Status, sell.Filled, sell.Remaining())
	}
}

func TestConcurrentPartialFillIncomingLarger(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Sell, 100, 40))
	r := mustSubmitC(t, e, mkLimit(2, order.Buy, 100, 100))

	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 2, 1, 100, 40)
	if o := mustGetC(t, e, 1); o.Status != order.Filled {
		t.Errorf("sell order status = %s, want FILLED", o.Status)
	}
	if r.Status != order.PartiallyFilled || r.Remaining != 60 {
		t.Errorf("buy result status=%s remaining=%d, want PARTIAL 60", r.Status, r.Remaining)
	}
	buy := mustGetC(t, e, 2)
	if buy.Filled != 40 || buy.Remaining() != 60 {
		t.Errorf("buy order filled=%d remaining=%d, want 40/60", buy.Filled, buy.Remaining())
	}
	lvl := e.GetOrderBookSnapshot().Bids
	if len(lvl) != 1 || lvl[0].Price != 100 || lvl[0].Qty != 60 {
		t.Errorf("bid side should hold 60 @100, got %+v", lvl)
	}
}

func TestConcurrentMultiplePriceLevels(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Sell, 100, 50))
	mustSubmitC(t, e, mkLimit(2, order.Sell, 101, 100))
	mustSubmitC(t, e, mkLimit(3, order.Sell, 102, 200))

	r := mustSubmitC(t, e, mkLimit(4, order.Buy, 102, 250))
	if len(r.Trades) != 3 {
		t.Fatalf("expected 3 trades, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 4, 1, 100, 50)
	assertTrade(t, r.Trades[1], 2, 4, 2, 101, 100)
	assertTrade(t, r.Trades[2], 3, 4, 3, 102, 100)
	if r.Status != order.Filled {
		t.Errorf("buy order status = %s, want FILLED", r.Status)
	}

	lvl := e.GetOrderBookSnapshot().Asks
	if len(lvl) != 1 || lvl[0].Price != 102 || lvl[0].Qty != 100 {
		t.Errorf("ask side should hold 100 @102, got %+v", lvl)
	}
}

func TestConcurrentFIFOConsumptionAfterPartialFill(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Buy, 100, 100))
	mustSubmitC(t, e, mkLimit(2, order.Buy, 100, 100))
	mustSubmitC(t, e, mkLimit(3, order.Buy, 100, 100))
	mustSubmitC(t, e, mkLimit(4, order.Sell, 100, 150))

	// Book now: order 2 (remaining 50) -> order 3 (100).
	r := mustSubmitC(t, e, mkLimit(5, order.Sell, 100, 100))
	if len(r.Trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 3, 2, 5, 100, 50)
	assertTrade(t, r.Trades[1], 4, 3, 5, 100, 50)

	o3 := mustGetC(t, e, 3)
	if o3.Remaining() != 50 {
		t.Errorf("order 3 remaining = %d, want 50", o3.Remaining())
	}
	o5 := mustGetC(t, e, 5)
	if o5.Status != order.Filled || o5.Filled != 100 {
		t.Errorf("order 5 = %s filled=%d, want FILLED 100", o5.Status, o5.Filled)
	}
	if bid := e.GetOrderBookSnapshot().Bids; len(bid) != 1 || bid[0].Qty != 50 {
		t.Errorf("bid side should hold 50 remaining, got %+v", bid)
	}
	if ask := e.GetOrderBookSnapshot().Asks; len(ask) != 0 {
		t.Errorf("ask side should be empty, got %+v", ask)
	}
}

func TestConcurrentCancelPositions(t *testing.T) {
	tests := []struct {
		name   string
		submit []order.OrderID
		cancel order.OrderID
		want   []order.OrderID
	}{
		{"head", []order.OrderID{1, 2, 3}, 1, []order.OrderID{2, 3}},
		{"middle", []order.OrderID{1, 2, 3}, 2, []order.OrderID{1, 3}},
		{"tail", []order.OrderID{1, 2, 3}, 3, []order.OrderID{1, 2}},
		{"only", []order.OrderID{1}, 1, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newConcurrent(t)
			for _, id := range tt.submit {
				mustSubmitC(t, e, mkLimit(id, order.Buy, 100, 100))
			}
			mustCancelC(t, e, tt.cancel)

			got := captureBookC(t, e).bids
			if len(got) != len(tt.want) {
				t.Fatalf("bid ordering = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("bid ordering = %v, want %v", got, tt.want)
					break
				}
			}
			oc := mustGetC(t, e, tt.cancel)
			if oc.Status != order.Cancelled {
				t.Errorf("cancelled order status = %s, want CANCELLED", oc.Status)
			}
		})
	}
}

func TestConcurrentCancelPartiallyFilled(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Sell, 100, 100))
	mustSubmitC(t, e, mkLimit(2, order.Buy, 100, 40))

	mustCancelC(t, e, 1)

	o1 := mustGetC(t, e, 1)
	if o1.Status != order.Cancelled || o1.Filled != 40 || o1.Remaining() != 60 {
		t.Errorf("cancelled order = %s filled=%d remaining=%d, want CANCELLED 40/60", o1.Status, o1.Filled, o1.Remaining())
	}
	if ask := e.GetOrderBookSnapshot().Asks; len(ask) != 0 {
		t.Errorf("ask side should be empty after cancel, got %+v", ask)
	}
}

func TestConcurrentCancelFilled(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Buy, 100, 100))
	mustSubmitC(t, e, mkLimit(2, order.Sell, 100, 100))

	for _, id := range []order.OrderID{1, 2} {
		if err := e.CancelOrder(id); !errors.Is(err, ErrAlreadyFilled) {
			t.Errorf("CancelOrder(%d) err = %v, want %v", id, err, ErrAlreadyFilled)
		}
	}
}

func TestConcurrentCancelUnknown(t *testing.T) {
	e := newConcurrent(t)
	if err := e.CancelOrder(99); !errors.Is(err, ErrOrderNotFound) {
		t.Errorf("CancelOrder(99) err = %v, want %v", err, ErrOrderNotFound)
	}
	mustGetErrC(t, e, 99, ErrOrderNotFound)
}

func TestConcurrentCancelAlreadyCancelled(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Buy, 100, 100))
	mustCancelC(t, e, 1)

	if err := e.CancelOrder(1); !errors.Is(err, ErrAlreadyCancelled) {
		t.Errorf("CancelOrder(1) err = %v, want %v", err, ErrAlreadyCancelled)
	}
}

func TestConcurrentCancelThenAddKeepsFIFO(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Buy, 100, 100))
	mustSubmitC(t, e, mkLimit(2, order.Buy, 100, 100))
	mustSubmitC(t, e, mkLimit(3, order.Buy, 100, 100))
	mustCancelC(t, e, 2)
	mustSubmitC(t, e, mkLimit(4, order.Buy, 100, 100))

	got := captureBookC(t, e).bids
	want := []order.OrderID{1, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("bid ordering = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("bid ordering = %v, want %v", got, want)
			break
		}
	}
}

func TestConcurrentMarketBuyAcrossMultipleLevels(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Sell, 100, 50))
	mustSubmitC(t, e, mkLimit(2, order.Sell, 101, 100))
	mustSubmitC(t, e, mkLimit(3, order.Sell, 102, 200))

	r := mustSubmitC(t, e, mkMarket(4, order.Buy, 120))
	if len(r.Trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 4, 1, 100, 50)
	assertTrade(t, r.Trades[1], 2, 4, 2, 101, 70)

	if r.Status != order.Filled || r.FilledQty != 120 || r.Remaining != 0 {
		t.Errorf("market buy = %s filled=%d remaining=%d, want FILLED 120/0", r.Status, r.FilledQty, r.Remaining)
	}
	if asks := e.GetOrderBookSnapshot().Asks; len(asks) != 2 || asks[0].Qty != 30 {
		t.Errorf("asks should hold 30 @101 and 200 @102, got %+v", asks)
	}
}

func TestConcurrentMarketSellAcrossMultipleLevels(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Buy, 102, 50))
	mustSubmitC(t, e, mkLimit(2, order.Buy, 101, 100))
	mustSubmitC(t, e, mkLimit(3, order.Buy, 100, 200))

	r := mustSubmitC(t, e, mkMarket(4, order.Sell, 120))
	if len(r.Trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 1, 4, 102, 50)
	assertTrade(t, r.Trades[1], 2, 2, 4, 101, 70)

	if r.Status != order.Filled || r.FilledQty != 120 || r.Remaining != 0 {
		t.Errorf("market sell = %s filled=%d remaining=%d, want FILLED 120/0", r.Status, r.FilledQty, r.Remaining)
	}
	if bids := e.GetOrderBookSnapshot().Bids; len(bids) != 2 || bids[0].Qty != 30 {
		t.Errorf("bids should hold 30 @101 and 200 @100, got %+v", bids)
	}
}

func TestConcurrentMarketBuyExactFill(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Sell, 100, 100))
	r := mustSubmitC(t, e, mkMarket(2, order.Buy, 100))

	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 2, 1, 100, 100)
	if r.Status != order.Filled {
		t.Errorf("market buy status = %s, want FILLED", r.Status)
	}
	if o := mustGetC(t, e, 1); o.Status != order.Filled {
		t.Errorf("resting sell status = %s, want FILLED", o.Status)
	}
}

func TestConcurrentMarketSellExactFill(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Buy, 100, 80))
	r := mustSubmitC(t, e, mkMarket(2, order.Sell, 80))

	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 1, 2, 100, 80)
	if r.Status != order.Filled {
		t.Errorf("market sell status = %s, want FILLED", r.Status)
	}
}

func TestConcurrentMarketOrderInsufficientLiquidity(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Sell, 100, 50))
	mustSubmitC(t, e, mkLimit(2, order.Sell, 101, 30))

	r := mustSubmitC(t, e, mkMarket(3, order.Buy, 120))
	if len(r.Trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 3, 1, 100, 50)
	assertTrade(t, r.Trades[1], 2, 3, 2, 101, 30)

	if r.Status != order.Cancelled || r.FilledQty != 80 || r.Remaining != 40 {
		t.Errorf("market buy = %s filled=%d remaining=%d, want CANCELLED 80/40", r.Status, r.FilledQty, r.Remaining)
	}
	o3 := mustGetC(t, e, 3)
	if o3.Status != order.Cancelled || o3.Filled != 80 {
		t.Errorf("order 3 = %s filled=%d, want CANCELLED 80", o3.Status, o3.Filled)
	}
	// The market order must never rest, and the ask side is now empty.
	if bids, asks := e.GetOrderBookSnapshot().Bids, e.GetOrderBookSnapshot().Asks; len(bids) != 0 || len(asks) != 0 {
		t.Errorf("book must be empty, got bids=%d asks=%d", len(bids), len(asks))
	}
}

func TestConcurrentMarketOrderNoLiquidity(t *testing.T) {
	e := newConcurrent(t)
	r := mustSubmitC(t, e, mkMarket(1, order.Buy, 100))

	if len(r.Trades) != 0 {
		t.Fatalf("expected no trades, got %d", len(r.Trades))
	}
	if r.Status != order.Cancelled || r.FilledQty != 0 || r.Remaining != 100 {
		t.Errorf("market buy = %s filled=%d remaining=%d, want CANCELLED 0/100", r.Status, r.FilledQty, r.Remaining)
	}
	if o := mustGetC(t, e, 1); o.Status != order.Cancelled {
		t.Errorf("order 1 status = %s, want CANCELLED", o.Status)
	}
	if bid := e.GetOrderBookSnapshot().Bids; bid != nil && len(bid) != 0 {
		t.Errorf("book must be empty, got %+v", bid)
	}
}

func TestConcurrentMarketOrderPartialFillHitsRestingPrices(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Sell, 100, 50))
	r := mustSubmitC(t, e, mkMarket(2, order.Buy, 20))

	assertTrade(t, r.Trades[0], 1, 2, 1, 100, 20)
	sell := mustGetC(t, e, 1)
	if sell.Status != order.PartiallyFilled || sell.Filled != 20 || sell.Remaining() != 30 {
		t.Errorf("resting sell = %s filled=%d remaining=%d, want PARTIAL 20/30", sell.Status, sell.Filled, sell.Remaining())
	}
}

func TestConcurrentMarketOrderWithPriceRejected(t *testing.T) {
	e := newConcurrent(t)
	mkt := order.NewOrder(1, "AAPL", order.Buy, order.Market, 100, 100)
	if _, err := e.SubmitOrder(mkt); !errors.Is(err, order.ErrMarketPriceSet) {
		t.Errorf("SubmitOrder(market with price) err = %v, want %v", err, order.ErrMarketPriceSet)
	}
}

func TestConcurrentMarketOrderAfterLimitSweep(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Sell, 101, 10))
	mustSubmitC(t, e, mkLimit(2, order.Sell, 103, 20))
	mustSubmitC(t, e, mkLimit(3, order.Sell, 105, 30))
	r := mustSubmitC(t, e, mkMarket(4, order.Buy, 45))

	if len(r.Trades) != 3 {
		t.Fatalf("expected 3 trades, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 4, 1, 101, 10)
	assertTrade(t, r.Trades[1], 2, 4, 2, 103, 20)
	assertTrade(t, r.Trades[2], 3, 4, 3, 105, 15)
	if asks := e.GetOrderBookSnapshot().Asks; len(asks) != 1 || asks[0].Price != 105 || asks[0].Qty != 15 {
		t.Errorf("asks should hold 15 @105, got %+v", asks)
	}
}

func TestConcurrentInvalidOrderRejected(t *testing.T) {
	e := newConcurrent(t)
	bad := order.NewOrder(0, "AAPL", order.Buy, order.Limit, 100, 100)
	if _, err := e.SubmitOrder(bad); !errors.Is(err, order.ErrInvalidOrderID) {
		t.Errorf("SubmitOrder(invalid) err = %v, want %v", err, order.ErrInvalidOrderID)
	}
}

func TestConcurrentDuplicateOrderRejected(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Buy, 100, 100))
	if _, err := e.SubmitOrder(mkLimit(1, order.Buy, 100, 100)); !errors.Is(err, ErrDuplicateOrder) {
		t.Errorf("SubmitOrder(duplicate) err = %v, want %v", err, ErrDuplicateOrder)
	}
}

func TestConcurrentTradePriceIsRestingPrice(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Sell, 100, 100))
	r := mustSubmitC(t, e, mkLimit(2, order.Buy, 105, 50))

	assertTrade(t, r.Trades[0], 1, 2, 1, 100, 50)
}

func TestConcurrentRestingOrderPreservesTime(t *testing.T) {
	e := newConcurrent(t)
	t1 := time.Unix(5000, 0)
	t2 := time.Unix(6000, 0)
	o1 := mkLimit(1, order.Buy, 100, 100)
	o1.Time = t1
	o2 := mkLimit(2, order.Sell, 100, 40)
	o2.Time = t2
	mustSubmitC(t, e, o1)
	r := mustSubmitC(t, e, o2)

	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	if got := mustGetC(t, e, 1).Time; !got.Equal(t1) {
		t.Errorf("resting order time = %v, want %v", got, t1)
	}
	if got := r.Trades[0].Timestamp; !got.Equal(t2) {
		t.Errorf("trade timestamp = %v, want incoming order time %v", got, t2)
	}
}

func TestConcurrentSubmitResultFields(t *testing.T) {
	e := newConcurrent(t)
	r := mustSubmitC(t, e, mkLimit(1, order.Buy, 100, 100))
	if r.OrderID != 1 || r.OriginalQty != 100 || r.FilledQty != 0 || r.Remaining != 100 || r.Status != order.Open {
		t.Errorf("resting result = %+v", r)
	}
	if len(r.Trades) != 0 {
		t.Errorf("resting should have 0 trades, got %d", len(r.Trades))
	}

	r2 := mustSubmitC(t, e, mkLimit(2, order.Sell, 100, 40))
	if r2.OrderID != 2 || r2.OriginalQty != 40 || r2.FilledQty != 40 || r2.Remaining != 0 || r2.Status != order.Filled {
		t.Errorf("fill result = %+v", r2)
	}
	if len(r2.Trades) != 1 {
		t.Errorf("fill result should have 1 trade, got %d", len(r2.Trades))
	}
}

func TestConcurrentGetOrderReturnsCopy(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Buy, 100, 100))

	o, err := e.GetOrder(1)
	if err != nil {
		t.Fatal(err)
	}
	o.Filled = 999
	o.Status = order.Filled
	o.Price = 0

	got, err := e.GetOrder(1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Filled != 0 || got.Status != order.Open || got.Price != 100 {
		t.Errorf("mutating GetOrder copy leaked into engine: %+v", got)
	}
	if lvl := e.GetOrderBookSnapshot().Bids; len(lvl) != 1 || lvl[0].Qty != 100 {
		t.Errorf("book also affected: %+v", lvl)
	}
}

func TestConcurrentGetOrderNotFound(t *testing.T) {
	e := newConcurrent(t)
	if _, err := e.GetOrder(99); !errors.Is(err, ErrOrderNotFound) {
		t.Errorf("GetOrder(99) err = %v, want %v", err, ErrOrderNotFound)
	}
}

func TestConcurrentSnapshotIsImmutable(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Buy, 100, 50))
	mustSubmitC(t, e, mkLimit(2, order.Buy, 100, 50))

	snap := e.GetOrderBookSnapshot()
	// Mutate every snapshot-level copy.
	for lvlIdx := range snap.Bids {
		lvl := &snap.Bids[lvlIdx]
		lvl.Price = 1
		lvl.Qty = 1
		for _, o := range lvl.Orders {
			o.ID = 999
			o.Qty = 1
			o.Filled = 1
		}
	}
	snap.Asks = append(snap.Asks, book.LevelSnapshot{Price: 1, Qty: 1})

	// Fresh snapshot must be unaffected.
	again := e.GetOrderBookSnapshot()
	if len(again.Bids) != 1 {
		t.Fatalf("bid levels = %d, want 1", len(again.Bids))
	}
	lvl := again.Bids[0]
	if lvl.Price != 100 || lvl.Qty != 100 {
		t.Errorf("bid level = price=%d qty=%d, want 100/100", lvl.Price, lvl.Qty)
	}
	if len(lvl.Orders) != 2 || lvl.Orders[0].ID != 1 || lvl.Orders[1].ID != 2 {
		t.Errorf("bid orders = %+v, want IDs 1,2", lvl.Orders)
	}
	if len(again.Asks) != 0 {
		t.Errorf("ask side should be empty, got %+v", again.Asks)
	}
}

func TestConcurrentTradeResultIsValueCopy(t *testing.T) {
	e := newConcurrent(t)
	mustSubmitC(t, e, mkLimit(1, order.Sell, 100, 100))
	r := mustSubmitC(t, e, mkLimit(2, order.Buy, 101, 50))

	// Mutate the returned trade copy.
	r.Trades[0].Qty = 1
	r.Trades[0].Price = 0

	sell := mustGetC(t, e, 1)
	if sell.Filled != 50 || sell.Remaining() != 50 {
		t.Errorf("mutating returned trade leaked into engine: filled=%d remaining=%d", sell.Filled, sell.Remaining())
	}
}

// --- Invariant checks over the loop ---
//
// The white-box corruption tests in engine_test.go (e.g.
// TestCheckInvariantsDetectsFilledOvercount) reach into Engine internals to
// force a violation and are not applicable to ConcurrentEngine: the wrapped
// Engine is private to the engine loop by design, which is the very guarantee
// under test here. The aggregate invariant check itself is exercised through
// the loop instead.

func TestConcurrentCheckInvariantsClean(t *testing.T) {
	e := newConcurrent(t)
	runRichScenarioC(t, e)
	mustSubmitC(t, e, mkMarket(8, order.Buy, 500)) // insufficient liquidity path

	if err := e.CheckInvariants(); err != nil {
		t.Fatalf("invariants violated: %v", err)
	}
}

func TestConcurrentInvariantsAfterOps(t *testing.T) {
	e := newConcurrent(t)
	runRichScenarioC(t, e)
	// Mirrors TestQuantityInvariantsAfterOps and
	// TestBookMembershipConsistencyAfterOps via the same aggregate check the
	// V1 engine exposes (quantity accounting + book membership + structure).
	if err := e.CheckInvariants(); err != nil {
		t.Fatalf("invariants violated: %v", err)
	}
	mustCancelC(t, e, 5)
	mustSubmitC(t, e, mkMarket(8, order.Sell, 200)) // sweeps bids
	if err := e.CheckInvariants(); err != nil {
		t.Fatalf("invariants violated after cancels/market sweep: %v", err)
	}
}

// --- Determinism & wrapper equivalence ---

// deterministicSeries is the same mixed script TestDeterminism replays:
// resting, crossing, partial fills, multi-level sweep, a cancelled-fill market
// order, and more resting, with fixed timestamps.
func deterministicSeries() []order.Order {
	orders := []order.Order{
		mkLimit(1, order.Buy, 100, 100),
		mkLimit(2, order.Buy, 101, 100),
		mkLimit(3, order.Sell, 100, 150), // fills buy2 (100) then buy1 (50); buy1 rem 50
		mkLimit(4, order.Sell, 102, 100), // rests ask 102
		mkMarket(5, order.Buy, 120),      // fills ask 4 (100); remaining 20 cancelled
		mkLimit(6, order.Buy, 99, 50),    // rests bid 99
		mkLimit(7, order.Sell, 103, 60),  // rests ask 103 (no cross: 103 > best bid 100)
		mkMarket(8, order.Sell, 40),      // fills buy1 (40 of 50)
	}
	fixed := time.Unix(1000, 0)
	for i := range orders {
		orders[i].Time = fixed.Add(time.Duration(i) * time.Second)
	}
	return orders
}

// runDeterministic replays deterministicSeries plus its two cancellations on
// any testEngine and captures every observable outcome.
func runDeterministic(t *testing.T, e testEngine) determinismResult {
	t.Helper()
	var trades []Trade
	for _, o := range deterministicSeries() {
		res, err := e.SubmitOrder(o)
		if err != nil {
			t.Fatalf("submit %d failed: %v", o.ID, err)
		}
		trades = append(trades, res.Trades...)
	}
	cancelErrs := []error{
		e.CancelOrder(6), // resting → nil
		e.CancelOrder(3), // filled → ErrAlreadyFilled
	}
	statuses := make(map[order.OrderID]order.Status)
	for id := range deterministicSeries() {
		statuses[order.OrderID(id+1)] = mustGetC(t, e, order.OrderID(id+1)).Status
	}
	return determinismResult{
		trades:     trades,
		statuses:   statuses,
		cancelErrs: cancelErrs,
		book:       snapshotString(e.GetOrderBookSnapshot()),
	}
}

// compareDeterminism mirrors the comparison loops in TestDeterminism.
func compareDeterminism(t *testing.T, a, b determinismResult) {
	t.Helper()
	if len(a.trades) == 0 {
		t.Fatal("test scenario produced no trades")
	}
	if len(a.trades) != len(b.trades) {
		t.Fatalf("trade count differs: %d vs %d", len(a.trades), len(b.trades))
	}
	for i := range a.trades {
		if a.trades[i] != b.trades[i] {
			t.Errorf("trade %d differs:\n  run1: %+v\n  run2: %+v", i, a.trades[i], b.trades[i])
		}
	}
	for id, st := range a.statuses {
		if b.statuses[id] != st {
			t.Errorf("order %d status differs: %s vs %s", id, st, b.statuses[id])
		}
	}
	for i := range a.cancelErrs {
		if (a.cancelErrs[i] == nil) != (b.cancelErrs[i] == nil) {
			t.Errorf("cancel %d result differs: %v vs %v", i, a.cancelErrs[i], b.cancelErrs[i])
		}
	}
	if a.book != b.book {
		t.Errorf("final book differs:\n  run1: %s\n  run2: %s", a.book, b.book)
	}
}

// TestConcurrentDeterminism replays the deterministic script on two separate
// ConcurrentEngine instances and asserts identical output, mirroring V1's
// TestDeterminism against the wrapped pipeline.
func TestConcurrentDeterminism(t *testing.T) {
	r1 := runDeterministic(t, newConcurrent(t))
	r2 := runDeterministic(t, newConcurrent(t))
	compareDeterminism(t, r1, r2)
}

// TestConcurrentMatchesSingleThreaded replays a mixed sequence once through
// the direct single-threaded Engine and once through ConcurrentEngine and
// asserts every observable outcome — each submission's trades, every final
// order status, cancellation results, and the final order book snapshot —
// matches. This proves the single-writer wrapper changes no outcomes.
func TestConcurrentMatchesSingleThreaded(t *testing.T) {
	direct := runDeterministic(t, NewEngine())
	wrapped := runDeterministic(t, newConcurrent(t))
	compareDeterminism(t, direct, wrapped)
}

// TestConcurrentSnapshotMatchesDirect drives several mixed scenarios through
// both engines and compares only the final book snapshot produced by the same
// order sequence, per the phase 2A acceptance check.
func TestConcurrentSnapshotMatchesDirect(t *testing.T) {
	scenarios := []struct {
		name string
		run  func(t *testing.T, e testEngine)
	}{
		{"rich-mixed", runRichScenarioC},
		{
			name: "market-sweeps",
			run: func(t *testing.T, e testEngine) {
				mustSubmitC(t, e, mkLimit(1, order.Sell, 100, 50))
				mustSubmitC(t, e, mkLimit(2, order.Sell, 101, 100))
				mustSubmitC(t, e, mkLimit(3, order.Sell, 102, 200))
				mustSubmitC(t, e, mkMarket(4, order.Buy, 120))
				mustSubmitC(t, e, mkLimit(5, order.Buy, 102, 250))  // fills ask2 (30), ask3 (200); rests 20 @102
				mustSubmitC(t, e, mkLimit(6, order.Sell, 102, 300)) // fills bid5 (20); rests 280 @102
				mustCancelC(t, e, 6)
			},
		},
		{
			name: "fifo-cancels",
			run: func(t *testing.T, e testEngine) {
				mustSubmitC(t, e, mkLimit(1, order.Buy, 100, 100))
				mustSubmitC(t, e, mkLimit(2, order.Buy, 100, 100))
				mustSubmitC(t, e, mkLimit(3, order.Buy, 100, 100))
				mustCancelC(t, e, 2)
				mustSubmitC(t, e, mkLimit(4, order.Sell, 100, 150))
				mustSubmitC(t, e, mkLimit(5, order.Buy, 100, 100))
			},
		},
		{
			name: "insufficient-liquidity-market",
			run: func(t *testing.T, e testEngine) {
				mustSubmitC(t, e, mkLimit(1, order.Buy, 102, 50))
				mustSubmitC(t, e, mkLimit(2, order.Buy, 101, 100))
				mustSubmitC(t, e, mkMarket(3, order.Sell, 200)) // 150 available, 50 cancelled
			},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			direct := NewEngine()
			sc.run(t, direct)
			want := snapshotString(direct.GetOrderBookSnapshot())

			concurrent := newConcurrent(t)
			sc.run(t, concurrent)
			got := snapshotString(concurrent.GetOrderBookSnapshot())

			if got != want {
				t.Errorf("snapshot mismatch:\n  direct:    %s\n  concurrent: %s", want, got)
			}
		})
	}
}

// --- Constructor & Shutdown ---

func TestNewConcurrentEngineDefaultBuffer(t *testing.T) {
	ce := NewConcurrentEngine(0)
	if got := cap(ce.requests); got != defaultRequestBuffer {
		t.Errorf("request buffer cap = %d, want default %d", got, defaultRequestBuffer)
	}
	ce.Shutdown()
}

func TestNewConcurrentEngineCustomBuffer(t *testing.T) {
	ce := NewConcurrentEngine(4)
	if got := cap(ce.requests); got != 4 {
		t.Errorf("request buffer cap = %d, want 4", got)
	}
	ce.Shutdown()
}

func TestShutdownIsIdempotent(t *testing.T) {
	ce := NewConcurrentEngine(0)
	mustSubmitC(t, ce, mkLimit(1, order.Buy, 100, 100))
	ce.Shutdown()
	// A second Shutdown must not panic (the sync.Once guards the close).
	ce.Shutdown()
}

// TestShutdownStopsEngineLoop verifies Shutdown does not leak the engine loop
// goroutine. After Shutdown returns, the loop has necessarily exited (Shutdown
// blocks on its WaitGroup), so we assert the goroutine count settles back to
// the pre-construction baseline. We poll instead of comparing once, because
// unrelated transient goroutines (e.g. a GC worker) can briefly push the count
// above baseline; a genuinely leaked loop goroutine would keep it above
// baseline until the polling deadline.
func TestShutdownStopsEngineLoop(t *testing.T) {
	baseline := runtime.NumGoroutine()

	ce := NewConcurrentEngine(16)
	// A few round-trips guarantee the loop goroutine has started and parked
	// on the request channel before we shut down.
	mustSubmitC(t, ce, mkLimit(1, order.Buy, 100, 100))
	mustSubmitC(t, ce, mkLimit(2, order.Sell, 101, 100))
	mustCancelC(t, ce, 1)

	ce.Shutdown()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() == baseline {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: %d goroutines after Shutdown, baseline %d",
		runtime.NumGoroutine(), baseline)
}
