package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/gurshaan17/apex/internal/order"
)

func mkLimit(id order.OrderID, side order.Side, price order.Price, qty order.Quantity) order.Order {
	return order.NewOrder(id, "AAPL", side, order.Limit, price, qty)
}

func mustSubmit(t *testing.T, e *Engine, o order.Order) SubmitResult {
	t.Helper()
	res, err := e.SubmitOrder(o)
	if err != nil {
		t.Fatalf("SubmitOrder(%d) failed: %v", o.ID, err)
	}
	return res
}

func mustCancel(t *testing.T, e *Engine, id order.OrderID) {
	t.Helper()
	if err := e.CancelOrder(id); err != nil {
		t.Fatalf("CancelOrder(%d) failed: %v", id, err)
	}
}

func mustGet(t *testing.T, e *Engine, id order.OrderID) *order.Order {
	t.Helper()
	o, err := e.GetOrder(id)
	if err != nil {
		t.Fatalf("GetOrder(%d) failed: %v", id, err)
	}
	return o
}

func mustGetErr(t *testing.T, e *Engine, id order.OrderID, want error) {
	t.Helper()
	if _, err := e.GetOrder(id); err == nil {
		t.Fatalf("GetOrder(%d): expected error %v, got nil", id, want)
	}
}

func assertTrade(t *testing.T, tr Trade, tradeID uint64, buy, sell order.OrderID, price order.Price, qty order.Quantity) {
	t.Helper()
	if tr.TradeID != tradeID {
		t.Errorf("trade %d: expected TradeID %d, got %d", buy, tradeID, tr.TradeID)
	}
	if tr.BuyOrderID != buy || tr.SellOrderID != sell {
		t.Errorf("trade: expected buy=%d sell=%d, got buy=%d sell=%d", buy, sell, tr.BuyOrderID, tr.SellOrderID)
	}
	if tr.Price != price {
		t.Errorf("trade: expected price %d, got %d", price, tr.Price)
	}
	if tr.Qty != qty {
		t.Errorf("trade: expected qty %d, got %d", qty, tr.Qty)
	}
}

// bookLevels is a tiny test helper wrapping order book snapshots so tests can
// inspect the FIFO order of resting orders without drilling into the book API.
type bookLevels struct {
	bids []order.OrderID
	asks []order.OrderID
}

func captureBook(t *testing.T, e *Engine) bookLevels {
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

func TestSubmitExactMatch(t *testing.T) {
	e := NewEngine()
	subBuy := mustSubmit(t, e, mkLimit(1, order.Buy, 100, 100))
	subSell := mustSubmit(t, e, mkLimit(2, order.Sell, 100, 100))

	if subBuy.Status != order.Open {
		t.Fatalf("first order should rest as OPEN, got %s", subBuy.Status)
	}
	if len(subSell.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(subSell.Trades))
	}
	assertTrade(t, subSell.Trades[0], 1, 1, 2, 100, 100)

	if o := mustGet(t, e, 1); o.Status != order.Filled {
		t.Errorf("buy order status = %s, want FILLED", o.Status)
	}
	if o := mustGet(t, e, 2); o.Status != order.Filled {
		t.Errorf("sell order status = %s, want FILLED", o.Status)
	}

	snap := e.GetOrderBookSnapshot()
	if len(snap.Bids) != 0 || len(snap.Asks) != 0 {
		t.Errorf("book must be empty after exact match, got bids=%d asks=%d", len(snap.Bids), len(snap.Asks))
	}
}

func TestSubmitBuyCrossesSell(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Sell, 100, 100))
	r := mustSubmit(t, e, mkLimit(2, order.Buy, 101, 50))

	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 2, 1, 100, 50)

	sell := mustGet(t, e, 1)
	if sell.Status != order.PartiallyFilled || sell.Filled != 50 || sell.Remaining() != 50 {
		t.Errorf("sell order = %s filled=%d remaining=%d, want PARTIAL 50/50", sell.Status, sell.Filled, sell.Remaining())
	}
	if lvl := e.GetOrderBookSnapshot().Asks; len(lvl) != 1 || lvl[0].Qty != 50 {
		t.Errorf("ask side should hold 50 remaining, got %+v", lvl)
	}
}

func TestSubmitSellCrossesBuy(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Buy, 104, 100))
	r := mustSubmit(t, e, mkLimit(2, order.Sell, 100, 40))

	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 1, 2, 104, 40)

	buy := mustGet(t, e, 1)
	if buy.Status != order.PartiallyFilled || buy.Filled != 40 || buy.Remaining() != 60 {
		t.Errorf("buy order = %s filled=%d remaining=%d, want PARTIAL 40/60", buy.Status, buy.Filled, buy.Remaining())
	}
}

func TestNonCrossingBuyRests(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Sell, 100, 100))
	r := mustSubmit(t, e, mkLimit(2, order.Buy, 99, 100))

	if len(r.Trades) != 0 {
		t.Fatalf("expected no trades, got %d", len(r.Trades))
	}
	if r.Status != order.Open || r.Remaining != 100 {
		t.Errorf("buy result = %s remaining=%d, want OPEN 100", r.Status, r.Remaining)
	}
}

func TestNonCrossingSellRests(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Buy, 100, 100))
	r := mustSubmit(t, e, mkLimit(2, order.Sell, 101, 100))

	if len(r.Trades) != 0 {
		t.Fatalf("expected no trades, got %d", len(r.Trades))
	}
	if r.Status != order.Open || r.Remaining != 100 {
		t.Errorf("sell result = %s remaining=%d, want OPEN 100", r.Status, r.Remaining)
	}
}

func TestPricePriorityBids(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Buy, 101, 10))
	mustSubmit(t, e, mkLimit(2, order.Buy, 100, 10))
	mustSubmit(t, e, mkLimit(3, order.Buy, 99, 10))

	r := mustSubmit(t, e, mkLimit(4, order.Sell, 100, 10))
	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 1, 4, 101, 10)
}

func TestPricePriorityAsks(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Sell, 100, 10))
	mustSubmit(t, e, mkLimit(2, order.Sell, 101, 10))
	mustSubmit(t, e, mkLimit(3, order.Sell, 102, 10))

	r := mustSubmit(t, e, mkLimit(4, order.Buy, 102, 10))
	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 4, 1, 100, 10)
}

func TestTimePriorityFIFO(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Buy, 100, 100))
	mustSubmit(t, e, mkLimit(2, order.Buy, 100, 100))
	mustSubmit(t, e, mkLimit(3, order.Buy, 100, 100))

	r := mustSubmit(t, e, mkLimit(4, order.Sell, 100, 250))
	if len(r.Trades) != 3 {
		t.Fatalf("expected 3 trades, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 1, 4, 100, 100)
	assertTrade(t, r.Trades[1], 2, 2, 4, 100, 100)
	assertTrade(t, r.Trades[2], 3, 3, 4, 100, 50)

	o3 := mustGet(t, e, 3)
	if o3.Status != order.PartiallyFilled || o3.Remaining() != 50 {
		t.Errorf("order 3 = %s remaining=%d, want PARTIAL 50", o3.Status, o3.Remaining())
	}
	lvl := e.GetOrderBookSnapshot().Bids
	if len(lvl) != 1 || lvl[0].Qty != 50 {
		t.Errorf("bid side should hold 50 remaining, got %+v", lvl)
	}
}

func TestPartialFillIncomingSmaller(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Sell, 100, 100))
	r := mustSubmit(t, e, mkLimit(2, order.Buy, 100, 40))

	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 2, 1, 100, 40)
	if r.FilledQty != 40 || r.Remaining != 0 || r.Status != order.Filled {
		t.Errorf("buy result filled=%d remaining=%d status=%s, want 40/0 FILLED", r.FilledQty, r.Remaining, r.Status)
	}
	sell := mustGet(t, e, 1)
	if sell.Filled != 40 || sell.Remaining() != 60 || sell.Status != order.PartiallyFilled {
		t.Errorf("sell order = %s filled=%d remaining=%d, want PARTIAL 40/60", sell.Status, sell.Filled, sell.Remaining())
	}
}

func TestPartialFillIncomingLarger(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Sell, 100, 40))
	r := mustSubmit(t, e, mkLimit(2, order.Buy, 100, 100))

	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 2, 1, 100, 40)
	if o := mustGet(t, e, 1); o.Status != order.Filled {
		t.Errorf("sell order status = %s, want FILLED", o.Status)
	}
	if r.Status != order.PartiallyFilled || r.Remaining != 60 {
		t.Errorf("buy result status=%s remaining=%d, want PARTIAL 60", r.Status, r.Remaining)
	}
	buy := mustGet(t, e, 2)
	if buy.Filled != 40 || buy.Remaining() != 60 {
		t.Errorf("buy order filled=%d remaining=%d, want 40/60", buy.Filled, buy.Remaining())
	}
	lvl := e.GetOrderBookSnapshot().Bids
	if len(lvl) != 1 || lvl[0].Price != 100 || lvl[0].Qty != 60 {
		t.Errorf("bid side should hold 60 @100, got %+v", lvl)
	}
}

func TestMultiplePriceLevels(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Sell, 100, 50))
	mustSubmit(t, e, mkLimit(2, order.Sell, 101, 100))
	mustSubmit(t, e, mkLimit(3, order.Sell, 102, 200))

	r := mustSubmit(t, e, mkLimit(4, order.Buy, 102, 250))
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

func TestFIFOConsumptionAfterPartialFill(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Buy, 100, 100))
	mustSubmit(t, e, mkLimit(2, order.Buy, 100, 100))
	mustSubmit(t, e, mkLimit(3, order.Buy, 100, 100))
	mustSubmit(t, e, mkLimit(4, order.Sell, 100, 150))

	// Book now: order 2 (remaining 50) -> order 3 (100).
	r := mustSubmit(t, e, mkLimit(5, order.Sell, 100, 100))
	if len(r.Trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 3, 2, 5, 100, 50)
	assertTrade(t, r.Trades[1], 4, 3, 5, 100, 50)

	o3 := mustGet(t, e, 3)
	if o3.Remaining() != 50 {
		t.Errorf("order 3 remaining = %d, want 50", o3.Remaining())
	}
	o5 := mustGet(t, e, 5)
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

func TestCancelPositions(t *testing.T) {
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
			e := NewEngine()
			for _, id := range tt.submit {
				mustSubmit(t, e, mkLimit(id, order.Buy, 100, 100))
			}
			mustCancel(t, e, tt.cancel)

			got := captureBook(t, e).bids
			if len(got) != len(tt.want) {
				t.Fatalf("bid ordering = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("bid ordering = %v, want %v", got, tt.want)
					break
				}
			}
			oc := mustGet(t, e, tt.cancel)
			if oc.Status != order.Cancelled {
				t.Errorf("cancelled order status = %s, want CANCELLED", oc.Status)
			}
		})
	}
}

func TestCancelPartiallyFilled(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Sell, 100, 100))
	mustSubmit(t, e, mkLimit(2, order.Buy, 100, 40))

	mustCancel(t, e, 1)

	o1 := mustGet(t, e, 1)
	if o1.Status != order.Cancelled || o1.Filled != 40 || o1.Remaining() != 60 {
		t.Errorf("cancelled order = %s filled=%d remaining=%d, want CANCELLED 40/60", o1.Status, o1.Filled, o1.Remaining())
	}
	if ask := e.GetOrderBookSnapshot().Asks; len(ask) != 0 {
		t.Errorf("ask side should be empty after cancel, got %+v", ask)
	}
}

func TestCancelFilled(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Buy, 100, 100))
	mustSubmit(t, e, mkLimit(2, order.Sell, 100, 100))

	for _, id := range []order.OrderID{1, 2} {
		if err := e.CancelOrder(id); !errors.Is(err, ErrAlreadyFilled) {
			t.Errorf("CancelOrder(%d) err = %v, want %v", id, err, ErrAlreadyFilled)
		}
	}
}

func TestCancelUnknown(t *testing.T) {
	e := NewEngine()
	if err := e.CancelOrder(99); !errors.Is(err, ErrOrderNotFound) {
		t.Errorf("CancelOrder(99) err = %v, want %v", err, ErrOrderNotFound)
	}
	mustGetErr(t, e, 99, ErrOrderNotFound)
}

func TestCancelAlreadyCancelled(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Buy, 100, 100))
	mustCancel(t, e, 1)

	if err := e.CancelOrder(1); !errors.Is(err, ErrAlreadyCancelled) {
		t.Errorf("CancelOrder(1) err = %v, want %v", err, ErrAlreadyCancelled)
	}
}

func TestCancelThenAddKeepsFIFO(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Buy, 100, 100))
	mustSubmit(t, e, mkLimit(2, order.Buy, 100, 100))
	mustSubmit(t, e, mkLimit(3, order.Buy, 100, 100))
	mustCancel(t, e, 2)
	mustSubmit(t, e, mkLimit(4, order.Buy, 100, 100))

	got := captureBook(t, e).bids
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

func TestMarketOrderRejected(t *testing.T) {
	e := NewEngine()
	mkt := order.NewOrder(1, "AAPL", order.Buy, order.Market, 0, 100)
	if _, err := e.SubmitOrder(mkt); !errors.Is(err, ErrUnsupportedOrderType) {
		t.Errorf("SubmitOrder(market) err = %v, want %v", err, ErrUnsupportedOrderType)
	}
}

func TestInvalidOrderRejected(t *testing.T) {
	e := NewEngine()
	bad := order.NewOrder(0, "AAPL", order.Buy, order.Limit, 100, 100)
	if _, err := e.SubmitOrder(bad); !errors.Is(err, order.ErrInvalidOrderID) {
		t.Errorf("SubmitOrder(invalid) err = %v, want %v", err, order.ErrInvalidOrderID)
	}
}

func TestDuplicateOrderRejected(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Buy, 100, 100))
	if _, err := e.SubmitOrder(mkLimit(1, order.Buy, 100, 100)); !errors.Is(err, ErrDuplicateOrder) {
		t.Errorf("SubmitOrder(duplicate) err = %v, want %v", err, ErrDuplicateOrder)
	}
}

func TestTradePriceIsRestingPrice(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Sell, 100, 100))
	r := mustSubmit(t, e, mkLimit(2, order.Buy, 105, 50))

	assertTrade(t, r.Trades[0], 1, 2, 1, 100, 50)
}

func TestRestingOrderPreservesTime(t *testing.T) {
	e := NewEngine()
	t1 := time.Unix(5000, 0)
	t2 := time.Unix(6000, 0)
	o1 := mkLimit(1, order.Buy, 100, 100)
	o1.Time = t1
	o2 := mkLimit(2, order.Sell, 100, 40)
	o2.Time = t2
	mustSubmit(t, e, o1)
	r := mustSubmit(t, e, o2)

	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	if got := mustGet(t, e, 1).Time; !got.Equal(t1) {
		t.Errorf("resting order time = %v, want %v", got, t1)
	}
	if got := r.Trades[0].Timestamp; !got.Equal(t2) {
		t.Errorf("trade timestamp = %v, want incoming order time %v", got, t2)
	}
}

// runRichScenario drives a mixed sequence of submissions and cancellations so
// the invariant tests can assert aggregate consistency afterwards.
func runRichScenario(t *testing.T, e *Engine) {
	t.Helper()
	mustSubmit(t, e, mkLimit(1, order.Buy, 100, 100))  // rests
	mustSubmit(t, e, mkLimit(2, order.Buy, 101, 100))  // rests
	mustSubmit(t, e, mkLimit(3, order.Sell, 100, 150)) // fills 2 (100) then 1 (50)
	mustSubmit(t, e, mkLimit(4, order.Buy, 99, 50))    // rests
	mustCancel(t, e, 1)                                // partially filled -> cancelled
	mustSubmit(t, e, mkLimit(5, order.Sell, 102, 100)) // rests
	mustSubmit(t, e, mkLimit(6, order.Buy, 100, 120))  // rests (100 < best ask 102)
	mustSubmit(t, e, mkLimit(7, order.Sell, 101, 80))  // fills 6 (80)
}

func TestQuantityInvariantsAfterOps(t *testing.T) {
	e := NewEngine()
	runRichScenario(t, e)

	for _, o := range e.orders {
		if o.Filled < 0 || o.Remaining() < 0 {
			t.Errorf("order %d: negative filled=%d remaining=%d", o.ID, o.Filled, o.Remaining())
		}
		if o.Filled > o.Qty {
			t.Errorf("order %d: filled %d > original %d", o.ID, o.Filled, o.Qty)
		}
		if o.Filled+o.Remaining() != o.Qty {
			t.Errorf("order %d: filled %d + remaining %d != original %d", o.ID, o.Filled, o.Remaining(), o.Qty)
		}
	}
}

func TestBookMembershipConsistencyAfterOps(t *testing.T) {
	e := NewEngine()
	runRichScenario(t, e)

	inBook := map[order.OrderID]bool{}
	snap := captureBook(t, e)
	for _, id := range snap.bids {
		inBook[id] = true
	}
	for _, id := range snap.asks {
		inBook[id] = true
	}

	for id, o := range e.orders {
		_, present := inBook[id]
		switch o.Status {
		case order.Open, order.PartiallyFilled:
			if !present {
				t.Errorf("order %d is active (%s) but absent from book", id, o.Status)
			}
		case order.Filled, order.Cancelled:
			if present {
				t.Errorf("order %d is terminal (%s) but still in book", id, o.Status)
			}
		default:
			t.Errorf("order %d in unexpected status %s", id, o.Status)
		}
	}
}

func TestDeterminism(t *testing.T) {
	orders := []order.Order{
		mkLimit(1, order.Buy, 100, 100),
		mkLimit(2, order.Buy, 101, 100),
		mkLimit(3, order.Sell, 100, 150),
		mkLimit(4, order.Sell, 102, 100),
		mkLimit(5, order.Buy, 101, 200),
	}
	fixed := time.Unix(1000, 0)
	for i := range orders {
		orders[i].Time = fixed.Add(time.Duration(i) * time.Second)
	}

	run := func() ([]Trade, map[order.OrderID]order.Status) {
		e := NewEngine()
		var trades []Trade
		for _, o := range orders {
			res, err := e.SubmitOrder(o)
			if err != nil {
				t.Fatalf("submit %d failed: %v", o.ID, err)
			}
			trades = append(trades, res.Trades...)
		}
		statuses := make(map[order.OrderID]order.Status)
		for _, o := range orders {
			statuses[o.ID] = mustGet(t, e, o.ID).Status
		}
		return trades, statuses
	}

	t1, s1 := run()
	t2, s2 := run()

	if len(t1) != len(t2) {
		t.Fatalf("trade count differs between runs: %d vs %d", len(t1), len(t2))
	}
	for i := range t1 {
		if t1[i] != t2[i] {
			t.Errorf("trade %d differs between runs:\n run1: %+v\n run2: %+v", i, t1[i], t2[i])
		}
	}
	for id, st := range s1 {
		if s2[id] != st {
			t.Errorf("order %d status differs between runs: %s vs %s", id, st, s2[id])
		}
	}
	if len(t1) == 0 {
		t.Fatal("test scenario produced no trades")
	}
}
