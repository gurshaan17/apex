package engine

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gurshaan17/apex/internal/book"
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

func mkMarket(id order.OrderID, side order.Side, qty order.Quantity) order.Order {
	return order.NewOrder(id, "AAPL", side, order.Market, 0, qty)
}

func TestMarketBuyAcrossMultipleLevels(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Sell, 100, 50))
	mustSubmit(t, e, mkLimit(2, order.Sell, 101, 100))
	mustSubmit(t, e, mkLimit(3, order.Sell, 102, 200))

	r := mustSubmit(t, e, mkMarket(4, order.Buy, 120))
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

func TestMarketSellAcrossMultipleLevels(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Buy, 102, 50))
	mustSubmit(t, e, mkLimit(2, order.Buy, 101, 100))
	mustSubmit(t, e, mkLimit(3, order.Buy, 100, 200))

	r := mustSubmit(t, e, mkMarket(4, order.Sell, 120))
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

func TestMarketBuyExactFill(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Sell, 100, 100))
	r := mustSubmit(t, e, mkMarket(2, order.Buy, 100))

	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 2, 1, 100, 100)
	if r.Status != order.Filled {
		t.Errorf("market buy status = %s, want FILLED", r.Status)
	}
	if o := mustGet(t, e, 1); o.Status != order.Filled {
		t.Errorf("resting sell status = %s, want FILLED", o.Status)
	}
}

func TestMarketSellExactFill(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Buy, 100, 80))
	r := mustSubmit(t, e, mkMarket(2, order.Sell, 80))

	if len(r.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 1, 2, 100, 80)
	if r.Status != order.Filled {
		t.Errorf("market sell status = %s, want FILLED", r.Status)
	}
}

func TestMarketOrderInsufficientLiquidity(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Sell, 100, 50))
	mustSubmit(t, e, mkLimit(2, order.Sell, 101, 30))

	r := mustSubmit(t, e, mkMarket(3, order.Buy, 120))
	if len(r.Trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(r.Trades))
	}
	assertTrade(t, r.Trades[0], 1, 3, 1, 100, 50)
	assertTrade(t, r.Trades[1], 2, 3, 2, 101, 30)

	if r.Status != order.Cancelled || r.FilledQty != 80 || r.Remaining != 40 {
		t.Errorf("market buy = %s filled=%d remaining=%d, want CANCELLED 80/40", r.Status, r.FilledQty, r.Remaining)
	}
	o3 := mustGet(t, e, 3)
	if o3.Status != order.Cancelled || o3.Filled != 80 {
		t.Errorf("order 3 = %s filled=%d, want CANCELLED 80", o3.Status, o3.Filled)
	}
	// The market order must never rest, and the ask side is now empty.
	if bids, asks := e.GetOrderBookSnapshot().Bids, e.GetOrderBookSnapshot().Asks; len(bids) != 0 || len(asks) != 0 {
		t.Errorf("book must be empty, got bids=%d asks=%d", len(bids), len(asks))
	}
}

func TestMarketOrderNoLiquidity(t *testing.T) {
	e := NewEngine()
	r := mustSubmit(t, e, mkMarket(1, order.Buy, 100))

	if len(r.Trades) != 0 {
		t.Fatalf("expected no trades, got %d", len(r.Trades))
	}
	if r.Status != order.Cancelled || r.FilledQty != 0 || r.Remaining != 100 {
		t.Errorf("market buy = %s filled=%d remaining=%d, want CANCELLED 0/100", r.Status, r.FilledQty, r.Remaining)
	}
	if o := mustGet(t, e, 1); o.Status != order.Cancelled {
		t.Errorf("order 1 status = %s, want CANCELLED", o.Status)
	}
	if e.GetOrderBookSnapshot().Bids != nil && len(e.GetOrderBookSnapshot().Bids) != 0 {
		t.Errorf("book must be empty, got %+v", e.GetOrderBookSnapshot().Bids)
	}
}

func TestMarketOrderPartialFillHitsRestingPrices(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Sell, 100, 50))
	r := mustSubmit(t, e, mkMarket(2, order.Buy, 20))

	assertTrade(t, r.Trades[0], 1, 2, 1, 100, 20)
	sell := mustGet(t, e, 1)
	if sell.Status != order.PartiallyFilled || sell.Filled != 20 || sell.Remaining() != 30 {
		t.Errorf("resting sell = %s filled=%d remaining=%d, want PARTIAL 20/30", sell.Status, sell.Filled, sell.Remaining())
	}
}

func TestMarketOrderWithPriceRejected(t *testing.T) {
	e := NewEngine()
	mkt := order.NewOrder(1, "AAPL", order.Buy, order.Market, 100, 100)
	if _, err := e.SubmitOrder(mkt); !errors.Is(err, order.ErrMarketPriceSet) {
		t.Errorf("SubmitOrder(market with price) err = %v, want %v", err, order.ErrMarketPriceSet)
	}
}

func TestMarketOrderAfterLimitSweep(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Sell, 101, 10))
	mustSubmit(t, e, mkLimit(2, order.Sell, 103, 20))
	mustSubmit(t, e, mkLimit(3, order.Sell, 105, 30))
	r := mustSubmit(t, e, mkMarket(4, order.Buy, 45))

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

// snapshotString produces a canonical representation of a book snapshot for
// deterministic comparison. The snapshot is already best-first / FIFO.
func snapshotString(snap book.Snapshot) string {
	side := func(levels []book.LevelSnapshot) string {
		parts := make([]string, 0, len(levels))
		for _, lvl := range levels {
			var ids []string
			for _, o := range lvl.Orders {
				ids = append(ids, fmt.Sprintf("%d(r%d)", o.ID, o.Remaining()))
			}
			parts = append(parts, fmt.Sprintf("%d={%s|%d}", lvl.Price, strings.Join(ids, ","), lvl.Qty))
		}
		return "[" + strings.Join(parts, " ") + "]"
	}
	return side(snap.Bids) + " : " + side(snap.Asks)
}

type determinismResult struct {
	trades     []Trade
	statuses   map[order.OrderID]order.Status
	cancelErrs []error
	book       string
}

func TestDeterminism(t *testing.T) {
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

	run := func() determinismResult {
		e := NewEngine()
		var trades []Trade
		for _, o := range orders {
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
		for id := range orders {
			oid := order.OrderID(id + 1)
			statuses[oid] = mustGet(t, e, oid).Status
		}
		return determinismResult{
			trades:     trades,
			statuses:   statuses,
			cancelErrs: cancelErrs,
			book:       snapshotString(e.GetOrderBookSnapshot()),
		}
	}

	r1 := run()
	r2 := run()

	if len(r1.trades) == 0 {
		t.Fatal("test scenario produced no trades")
	}
	if len(r1.trades) != len(r2.trades) {
		t.Fatalf("trade count differs: %d vs %d", len(r1.trades), len(r2.trades))
	}
	for i := range r1.trades {
		if r1.trades[i] != r2.trades[i] {
			t.Errorf("trade %d differs:\n  run1: %+v\n  run2: %+v", i, r1.trades[i], r2.trades[i])
		}
	}
	for id, st := range r1.statuses {
		if r2.statuses[id] != st {
			t.Errorf("order %d status differs: %s vs %s", id, st, r2.statuses[id])
		}
	}
	for i := range r1.cancelErrs {
		if (r1.cancelErrs[i] == nil) != (r2.cancelErrs[i] == nil) {
			t.Errorf("cancel %d result differs: %v vs %v", i, r1.cancelErrs[i], r2.cancelErrs[i])
		}
	}
	if r1.book != r2.book {
		t.Errorf("final book differs:\n  run1: %s\n  run2: %s", r1.book, r2.book)
	}
}

// --- API & result-model tests ---

func TestSubmitResultFields(t *testing.T) {
	e := NewEngine()
	r := mustSubmit(t, e, mkLimit(1, order.Buy, 100, 100))
	if r.OrderID != 1 || r.OriginalQty != 100 || r.FilledQty != 0 || r.Remaining != 100 || r.Status != order.Open {
		t.Errorf("resting result = %+v", r)
	}
	if len(r.Trades) != 0 {
		t.Errorf("resting should have 0 trades, got %d", len(r.Trades))
	}

	r2 := mustSubmit(t, e, mkLimit(2, order.Sell, 100, 40))
	if r2.OrderID != 2 || r2.OriginalQty != 40 || r2.FilledQty != 40 || r2.Remaining != 0 || r2.Status != order.Filled {
		t.Errorf("fill result = %+v", r2)
	}
	if len(r2.Trades) != 1 {
		t.Errorf("fill result should have 1 trade, got %d", len(r2.Trades))
	}
}

func TestGetOrderReturnsCopy(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Buy, 100, 100))

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

func TestGetOrderNotFound(t *testing.T) {
	e := NewEngine()
	if _, err := e.GetOrder(99); !errors.Is(err, ErrOrderNotFound) {
		t.Errorf("GetOrder(99) err = %v, want %v", err, ErrOrderNotFound)
	}
}

func TestSnapshotIsImmutable(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Buy, 100, 50))
	mustSubmit(t, e, mkLimit(2, order.Buy, 100, 50))

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

func TestTradeResultIsValueCopy(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Sell, 100, 100))
	r := mustSubmit(t, e, mkLimit(2, order.Buy, 101, 50))

	// Mutate the returned trade copy.
	r.Trades[0].Qty = 1
	r.Trades[0].Price = 0

	sell := mustGet(t, e, 1)
	if sell.Filled != 50 || sell.Remaining() != 50 {
		t.Errorf("mutating returned trade leaked into engine: filled=%d remaining=%d", sell.Filled, sell.Remaining())
	}
}

// --- Invariant & CheckInvariants tests ---

func TestCheckInvariantsClean(t *testing.T) {
	e := NewEngine()
	runRichScenario(t, e)
	// Exercise a market order too (insufficient liquidity path).
	mustSubmit(t, e, mkMarket(8, order.Buy, 500))

	if err := e.CheckInvariants(); err != nil {
		t.Fatalf("invariants violated: %v", err)
	}
}

func TestCheckInvariantsDetectsFilledOvercount(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Buy, 100, 100))
	e.orders[1].Filled = e.orders[1].Qty + 1
	if err := e.CheckInvariants(); err == nil {
		t.Error("expected filled>original to be detected")
	}
}

func TestCheckInvariantsDetectsTerminalInBook(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Buy, 100, 50))
	e.orders[1].Status = order.Filled
	if err := e.CheckInvariants(); err == nil {
		t.Error("expected terminal-order-in-book to be detected")
	}
}

func TestCheckInvariantsDetectsActiveNotInBook(t *testing.T) {
	e := NewEngine()
	mustSubmit(t, e, mkLimit(1, order.Buy, 100, 50))
	e.orders[1].Status = order.Open
	// Remove from book directly via engine internals (white-box test).
	e.book.Remove(1)
	if err := e.CheckInvariants(); err == nil {
		t.Error("expected active-order-not-in-book to be detected")
	}
}

func TestBookStructuralInvariantsAfterRichScenario(t *testing.T) {
	e := NewEngine()
	runRichScenario(t, e)
	mustSubmit(t, e, mkMarket(8, order.Sell, 200)) // sweeps bids
	mustCancel(t, e, 5)                            // resting ask @102
	if err := e.book.CheckInvariants(); err != nil {
		t.Fatalf("book invariants violated: %v", err)
	}
}
