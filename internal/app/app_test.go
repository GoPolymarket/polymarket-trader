package app

import (
	"context"
	"testing"
	"time"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"

	"github.com/GoPolymarket/polymarket-trader/internal/config"
)

func testConfig() config.Config {
	cfg := config.Default()
	cfg.DryRun = true
	cfg.Maker.Enabled = true
	cfg.Taker.Enabled = true
	cfg.Maker.Markets = []string{"asset-1"}
	return cfg
}

func TestNewApp(t *testing.T) {
	cfg := testConfig()
	a := New(cfg, nil, nil, nil)
	if a == nil {
		t.Fatal("expected non-nil app")
	}
	if a.activeOrders == nil {
		t.Fatal("expected initialized activeOrders map")
	}
}

func TestHandleBookEventDryRunMaker(t *testing.T) {
	cfg := testConfig()
	cfg.Maker.Enabled = true
	cfg.Taker.Enabled = false

	a := New(cfg, nil, nil, nil)

	event := ws.OrderbookEvent{
		AssetID: "asset-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "100"}},
	}

	a.HandleBookEvent(context.Background(), event)

	orders, fills, _ := a.Stats()
	if orders != 0 {
		t.Fatalf("dry run should produce 0 orders, got %d", orders)
	}
	if fills != 0 {
		t.Fatalf("dry run should produce 0 fills, got %d", fills)
	}

	book, ok := a.books.Get("asset-1")
	if !ok {
		t.Fatal("expected book snapshot for asset-1")
	}
	if len(book.Bids) != 1 {
		t.Fatalf("expected 1 bid level, got %d", len(book.Bids))
	}
}

func TestHandleBookEventDryRunTaker(t *testing.T) {
	cfg := testConfig()
	cfg.Maker.Enabled = false
	cfg.Taker.Enabled = true
	cfg.Taker.MinImbalance = 0.10

	a := New(cfg, nil, nil, nil)

	event := ws.OrderbookEvent{
		AssetID: "asset-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "300"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "50"}},
	}

	a.HandleBookEvent(context.Background(), event)

	_, fills, _ := a.Stats()
	if fills != 0 {
		t.Fatalf("dry run should produce 0 fills, got %d", fills)
	}
}

func TestHandleBookEventEmptyBook(t *testing.T) {
	cfg := testConfig()
	a := New(cfg, nil, nil, nil)

	event := ws.OrderbookEvent{AssetID: "asset-1"}
	a.HandleBookEvent(context.Background(), event)

	orders, fills, _ := a.Stats()
	if orders != 0 || fills != 0 {
		t.Fatal("empty book should produce no orders or fills")
	}
}

func TestStats(t *testing.T) {
	cfg := testConfig()
	a := New(cfg, nil, nil, nil)

	orders, fills, pnl := a.Stats()
	if orders != 0 || fills != 0 || pnl != 0 {
		t.Fatalf("expected zeroed stats, got orders=%d fills=%d pnl=%f", orders, fills, pnl)
	}
}

func TestShutdownDryRun(t *testing.T) {
	cfg := testConfig()
	cfg.DryRun = true
	a := New(cfg, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	a.Shutdown(ctx)
}

func TestHandleBookEventMultipleUpdates(t *testing.T) {
	cfg := testConfig()
	cfg.Maker.Enabled = true
	cfg.Taker.Enabled = false

	a := New(cfg, nil, nil, nil)

	for i := 0; i < 5; i++ {
		event := ws.OrderbookEvent{
			AssetID: "asset-1",
			Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}},
			Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "100"}},
		}
		a.HandleBookEvent(context.Background(), event)
	}

	orders, _, _ := a.Stats()
	if orders != 0 {
		t.Fatalf("dry run should produce 0 orders, got %d", orders)
	}
}
