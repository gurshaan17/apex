package engine

import (
	"testing"

	"github.com/gurshaan17/apex/internal/order"
)

const (
	benchPrice order.Price    = 100_00 // ₹100.00
	benchQty   order.Quantity = 100
)

func benchLimit(id int64, side order.Side, price order.Price, qty order.Quantity) order.Order {
	return order.NewOrder(order.OrderID(id), "AAPL", side, order.Limit, price, qty)
}

func benchMarket(id int64, side order.Side, qty order.Quantity) order.Order {
	return order.NewOrder(order.OrderID(id), "AAPL", side, order.Market, 0, qty)
}

// prebuiltOrders builds n orders with unique positive IDs before the timed
// region (time.Now() and ID bookkeeping stay out of the measurement).
func prebuiltOrders(n int, f func(id int64) order.Order) []order.Order {
	out := make([]order.Order, n)
	for i := range out {
		out[i] = f(int64(i + 1))
	}
	return out
}

// seedBook fills an engine with `perLevel` resting orders on each of `levels`
// prices per side around a tight spread, using negative order IDs so
// benchmark orders never collide.
func seedBook(e *Engine, levels, perLevel int) {
	id := int64(0)
	for l := 0; l < levels; l++ {
		bid := benchPrice - order.Price(l)
		ask := benchPrice + 1 + order.Price(l)
		for i := 0; i < perLevel; i++ {
			id--
			_, _ = e.SubmitOrder(benchLimit(id, order.Buy, bid, benchQty))
			id--
			_, _ = e.SubmitOrder(benchLimit(id, order.Sell, ask, benchQty))
		}
	}
}

// BenchmarkSubmitNonMatching measures submitting an order that rests on an
// existing price level without matching anything.
func BenchmarkSubmitNonMatching(b *testing.B) {
	b.ReportAllocs()
	e := NewEngine()
	seedBook(e, 8, 8)
	orders := prebuiltOrders(b.N, func(id int64) order.Order {
		return benchLimit(id, order.Sell, benchPrice+1, benchQty)
	})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.SubmitOrder(orders[i]); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSubmitNonMatchingNewLevel measures the worst case for resting: every
// order inserts a brand-new, distinct price level (O(distinct prices) scan).
func BenchmarkSubmitNonMatchingNewLevel(b *testing.B) {
	b.ReportAllocs()
	e := NewEngine()
	seedBook(e, 8, 8)
	orders := prebuiltOrders(b.N, func(id int64) order.Order {
		return benchLimit(id, order.Sell, benchPrice+10_00+order.Price(id), benchQty)
	})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.SubmitOrder(orders[i]); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSubmitImmediateMatch measures a full match round-trip: one resting
// ask plus one buy that consumes it completely. Both orders are sized so the
// book returns to its prior state each iteration.
func BenchmarkSubmitImmediateMatch(b *testing.B) {
	b.ReportAllocs()
	e := NewEngine()
	seedBook(e, 8, 8)
	sells := prebuiltOrders(b.N, func(id int64) order.Order {
		return benchLimit(id*2-1, order.Sell, benchPrice+1, 50)
	})
	buys := prebuiltOrders(b.N, func(id int64) order.Order {
		return benchLimit(id*2, order.Buy, benchPrice+1, 50)
	})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.SubmitOrder(sells[i]); err != nil {
			b.Fatal(err)
		}
		if _, err := e.SubmitOrder(buys[i]); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSubmitPartialFill measures a partial fill: a resting ask is
// consumed in 25-unit chunks. Sizes divide evenly (100 % 25 == 0) so the
// resting order is exactly refilled each cycle and the book stays stable.
func BenchmarkSubmitPartialFill(b *testing.B) {
	b.ReportAllocs()
	e := NewEngine()
	seedBook(e, 8, 8)

	buys := prebuiltOrders(b.N, func(id int64) order.Order {
		return benchLimit(id, order.Buy, benchPrice+1, 25)
	})
	var sellID int64 = -1_000_000
	remaining := benchQty
	refill := func() {
		sellID--
		_, _ = e.SubmitOrder(benchLimit(sellID, order.Sell, benchPrice+1, benchQty))
		remaining = benchQty
	}
	refill() // initial resting ask to consume

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.SubmitOrder(buys[i]); err != nil {
			b.Fatal(err)
		}
		remaining -= 25
		if remaining == 0 {
			refill()
		}
	}
}

// BenchmarkSubmitMultiLevelMatch measures an aggressive order walking three
// price levels. The sizes are chosen so the triggered levels are exactly
// consumed, keeping the book stable across iterations.
func BenchmarkSubmitMultiLevelMatch(b *testing.B) {
	b.ReportAllocs()
	e := NewEngine()
	seedBook(e, 8, 8)

	var id int64 = -2_000_000
	buys := prebuiltOrders(b.N, func(id int64) order.Order {
		return benchLimit(id, order.Buy, benchPrice+3, 150)
	})
	level := func() {
		id--
		_, _ = e.SubmitOrder(benchLimit(id, order.Sell, benchPrice+1, 50))
		id--
		_, _ = e.SubmitOrder(benchLimit(id, order.Sell, benchPrice+2, 100))
		id--
		_, _ = e.SubmitOrder(benchLimit(id, order.Sell, benchPrice+3, 100))
	}
	level()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.SubmitOrder(buys[i]); err != nil {
			b.Fatal(err)
		}
		level()
	}
}

// BenchmarkCancelOrder measures cancelling a resting order. Each iteration
// cancels the front bid at the benchmark level and re-adds one at the tail,
// keeping the book at constant size.
func BenchmarkCancelOrder(b *testing.B) {
	b.ReportAllocs()
	e := NewEngine()
	seedBook(e, 8, 8)

	var id int64 = -3_000_000
	const ring = 64
	ringIDs := make([]order.OrderID, ring)
	for i := range ringIDs {
		id--
		if _, err := e.SubmitOrder(benchLimit(id, order.Buy, benchPrice, benchQty)); err != nil {
			b.Fatal(err)
		}
		ringIDs[i] = order.OrderID(id)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := i % ring
		if err := e.CancelOrder(ringIDs[idx]); err != nil {
			b.Fatal(err)
		}
		id--
		if _, err := e.SubmitOrder(benchLimit(id, order.Buy, benchPrice, benchQty)); err != nil {
			b.Fatal(err)
		}
		ringIDs[idx] = order.OrderID(id)
	}
}

// BenchmarkLargeOrderStream processes a long, deterministic script of mixed
// order activity (resting, immediate matches, partial fills, multi-level
// sweeps, and market orders) per iteration. ns/op is per script (500 orders).
func BenchmarkLargeOrderStream(b *testing.B) {
	b.ReportAllocs()
	script := buildMixedStream(500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := NewEngine()
		runStream(e, script)
	}
}

// buildMixedStream returns a deterministic mix of order activity.
func buildMixedStream(n int) []order.Order {
	out := make([]order.Order, 0, n)
	id := int64(0)
	submit := func(o order.Order) {
		id++
		o.ID = order.OrderID(id)
		out = append(out, o)
	}
	p := benchPrice
	for len(out) < n {
		submit(benchLimit(0, order.Buy, p, 100))    // rests: bid @10000
		submit(benchLimit(0, order.Sell, p+1, 100)) // rests: ask @10001
		submit(benchLimit(0, order.Buy, p+1, 100))  // immediate match on ask @10001
		submit(benchLimit(0, order.Buy, p, 60))     // rests: bid @10000
		submit(benchLimit(0, order.Sell, p, 120))   // part-fills bid @10000 (120 of 160)
		submit(benchMarket(0, order.Buy, 80))       // market sweep / insufficient liquidity
		submit(benchLimit(0, order.Buy, p+3, 300))  // rests: bid @10003
		submit(benchLimit(0, order.Sell, p+2, 200)) // multi-level partial (consumes 200 of bid @10003)
		submit(benchLimit(0, order.Sell, p+3, 70))  // continues consuming bid @10003
		submit(benchMarket(0, order.Sell, 200))     // market sweep across levels
	}
	return out[:n]
}

func runStream(e *Engine, orders []order.Order) {
	for _, o := range orders {
		_, _ = e.SubmitOrder(o)
	}
}

// BenchmarkGetOrderBookSnapshot measures snapshotting a 2000-order book.
func BenchmarkGetOrderBookSnapshot(b *testing.B) {
	b.ReportAllocs()
	e := NewEngine()
	seedBook(e, 8, 250) // 8 levels x 250 orders/side = 4000 resting orders
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = e.GetOrderBookSnapshot()
	}
}

// BenchmarkCheckInvariants measures the diagnostics-only invariant check.
func BenchmarkCheckInvariants(b *testing.B) {
	e := NewEngine()
	seedBook(e, 8, 250)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := e.CheckInvariants(); err != nil {
			b.Fatal(err)
		}
	}
}
