package execution

import (
	"math"
	"sync/atomic"
	"testing"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"
)

func TestRegisterAndTrack(t *testing.T) {
	tr := NewTracker()
	tr.RegisterOrder("ord-1", "asset-1", "market-1", "BUY", 0.55, 100)

	if tr.OpenOrderCount() != 1 {
		t.Fatalf("expected 1 open order, got %d", tr.OpenOrderCount())
	}

	// Update via WS event — still LIVE.
	tr.ProcessOrderEvent(ws.OrderEvent{
		ID: "ord-1", AssetID: "asset-1", Market: "market-1",
		Side: "BUY", Price: "0.55", OriginalSize: "100", SizeMatched: "0",
		Status: "LIVE",
	})
	if tr.OpenOrderCount() != 1 {
		t.Fatalf("expected 1 open order after LIVE update, got %d", tr.OpenOrderCount())
	}
}

func TestFillUpdatesPosition(t *testing.T) {
	tr := NewTracker()
	tr.ProcessTradeEvent(ws.TradeEvent{
		ID: "t-1", AssetID: "asset-1", Side: "BUY", Price: "0.50", Size: "10",
	})

	pos := tr.Position("asset-1")
	if pos == nil {
		t.Fatal("expected position")
	}
	if pos.NetSize != 10 {
		t.Fatalf("expected net size 10, got %f", pos.NetSize)
	}
	if pos.AvgEntryPrice != 0.50 {
		t.Fatalf("expected avg entry 0.50, got %f", pos.AvgEntryPrice)
	}
	if pos.TotalFills != 1 {
		t.Fatalf("expected 1 fill, got %d", pos.TotalFills)
	}
}

func TestMultipleFillsAverageEntry(t *testing.T) {
	tr := NewTracker()
	tr.ProcessTradeEvent(ws.TradeEvent{
		ID: "t-1", AssetID: "asset-1", Side: "BUY", Price: "0.40", Size: "10",
	})
	tr.ProcessTradeEvent(ws.TradeEvent{
		ID: "t-2", AssetID: "asset-1", Side: "BUY", Price: "0.60", Size: "10",
	})

	pos := tr.Position("asset-1")
	if pos == nil {
		t.Fatal("expected position")
	}
	if pos.NetSize != 20 {
		t.Fatalf("expected net size 20, got %f", pos.NetSize)
	}
	// avg = (0.40*10 + 0.60*10) / 20 = 10/20 = 0.50
	if math.Abs(pos.AvgEntryPrice-0.50) > 1e-9 {
		t.Fatalf("expected avg entry 0.50, got %f", pos.AvgEntryPrice)
	}
}

func TestSellRealizePnL(t *testing.T) {
	tr := NewTracker()
	// Buy 10 at 0.40
	tr.ProcessTradeEvent(ws.TradeEvent{
		ID: "t-1", AssetID: "asset-1", Side: "BUY", Price: "0.40", Size: "10",
	})
	// Sell 10 at 0.60 — realize (0.60-0.40)*10 = 2.0
	tr.ProcessTradeEvent(ws.TradeEvent{
		ID: "t-2", AssetID: "asset-1", Side: "SELL", Price: "0.60", Size: "10",
	})

	pos := tr.Position("asset-1")
	if pos == nil {
		t.Fatal("expected position")
	}
	if math.Abs(pos.RealizedPnL-2.0) > 1e-9 {
		t.Fatalf("expected realized PnL 2.0, got %f", pos.RealizedPnL)
	}
	if pos.NetSize != 0 {
		t.Fatalf("expected net size 0 after full close, got %f", pos.NetSize)
	}
	if math.Abs(tr.TotalRealizedPnL()-2.0) > 1e-9 {
		t.Fatalf("expected total realized PnL 2.0, got %f", tr.TotalRealizedPnL())
	}
}

func TestCancelRemovesFromOpen(t *testing.T) {
	tr := NewTracker()
	tr.RegisterOrder("ord-1", "asset-1", "market-1", "BUY", 0.55, 100)
	if tr.OpenOrderCount() != 1 {
		t.Fatalf("expected 1 open, got %d", tr.OpenOrderCount())
	}

	tr.ProcessOrderEvent(ws.OrderEvent{
		ID: "ord-1", Status: "CANCELED",
	})
	if tr.OpenOrderCount() != 0 {
		t.Fatalf("expected 0 open after cancel, got %d", tr.OpenOrderCount())
	}
}

func TestPartialFill(t *testing.T) {
	tr := NewTracker()
	// Buy 20 at 0.50
	tr.ProcessTradeEvent(ws.TradeEvent{
		ID: "t-1", AssetID: "asset-1", Side: "BUY", Price: "0.50", Size: "20",
	})
	// Sell 5 at 0.60 — partial close, realize (0.60-0.50)*5 = 0.50
	tr.ProcessTradeEvent(ws.TradeEvent{
		ID: "t-2", AssetID: "asset-1", Side: "SELL", Price: "0.60", Size: "5",
	})

	pos := tr.Position("asset-1")
	if pos.NetSize != 15 {
		t.Fatalf("expected 15, got %f", pos.NetSize)
	}
	if math.Abs(pos.RealizedPnL-0.50) > 1e-9 {
		t.Fatalf("expected realized 0.50, got %f", pos.RealizedPnL)
	}
	if math.Abs(pos.AvgEntryPrice-0.50) > 1e-9 {
		t.Fatalf("expected avg entry still 0.50, got %f", pos.AvgEntryPrice)
	}
}

func TestCallbackOnFill(t *testing.T) {
	tr := NewTracker()
	var called atomic.Int32
	tr.OnFill = func(f Fill) {
		called.Add(1)
		if f.AssetID != "asset-1" {
			t.Errorf("expected asset-1 in callback, got %s", f.AssetID)
		}
	}
	tr.ProcessTradeEvent(ws.TradeEvent{
		ID: "t-1", AssetID: "asset-1", Side: "BUY", Price: "0.50", Size: "10",
	})
	if called.Load() != 1 {
		t.Fatalf("expected callback called once, got %d", called.Load())
	}
}

func TestTotalFillsCount(t *testing.T) {
	tr := NewTracker()
	tr.ProcessTradeEvent(ws.TradeEvent{ID: "t-1", AssetID: "a", Side: "BUY", Price: "0.50", Size: "10"})
	tr.ProcessTradeEvent(ws.TradeEvent{ID: "t-2", AssetID: "b", Side: "BUY", Price: "0.60", Size: "5"})

	if tr.TotalFills() != 2 {
		t.Fatalf("expected 2 total fills, got %d", tr.TotalFills())
	}
}

func TestPositionsSnapshot(t *testing.T) {
	tr := NewTracker()
	tr.ProcessTradeEvent(ws.TradeEvent{ID: "t-1", AssetID: "a", Side: "BUY", Price: "0.50", Size: "10"})
	tr.ProcessTradeEvent(ws.TradeEvent{ID: "t-2", AssetID: "b", Side: "BUY", Price: "0.60", Size: "5"})

	positions := tr.Positions()
	if len(positions) != 2 {
		t.Fatalf("expected 2 positions, got %d", len(positions))
	}
	if positions["a"].NetSize != 10 {
		t.Fatalf("expected net size 10 for a, got %f", positions["a"].NetSize)
	}
}

func TestOrderIDsFilter(t *testing.T) {
	tr := NewTracker()
	tr.RegisterOrder("o1", "asset-1", "m1", "BUY", 0.5, 10)
	tr.RegisterOrder("o2", "asset-1", "m1", "SELL", 0.6, 10)
	tr.RegisterOrder("o3", "asset-2", "m2", "BUY", 0.5, 10)

	ids := tr.OrderIDs("asset-1", "LIVE")
	if len(ids) != 2 {
		t.Fatalf("expected 2 live orders for asset-1, got %d", len(ids))
	}
}

func TestZeroSizeTradeIgnored(t *testing.T) {
	tr := NewTracker()
	tr.ProcessTradeEvent(ws.TradeEvent{ID: "t-1", AssetID: "a", Side: "BUY", Price: "0.50", Size: "0"})
	if tr.TotalFills() != 0 {
		t.Fatalf("expected 0 fills for zero-size trade, got %d", tr.TotalFills())
	}
	if tr.Position("a") != nil {
		t.Fatal("expected nil position for zero-size trade")
	}
}
