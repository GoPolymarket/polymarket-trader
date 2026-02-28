package paper

import (
	"math"
	"testing"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"
)

func boolPtr(v bool) *bool { return &v }

func sampleBook() ws.OrderbookEvent {
	return ws.OrderbookEvent{
		AssetID: "asset-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "500"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "500"}},
	}
}

func TestExecuteMarketBuyDeductsBalanceAndFees(t *testing.T) {
	sim := NewSimulator(Config{
		InitialBalanceUSDC: 1000,
		FeeBps:             10, // 0.10%
		SlippageBps:        20, // 0.20%
	})

	fill, err := sim.ExecuteMarket("asset-1", "BUY", 100, sampleBook())
	if err != nil {
		t.Fatalf("ExecuteMarket: %v", err)
	}
	if !fill.Filled {
		t.Fatal("expected market order to be filled")
	}

	snap := sim.Snapshot()
	if math.Abs(snap.BalanceUSDC-899.9) > 1e-6 {
		t.Fatalf("expected balance 899.9, got %f", snap.BalanceUSDC)
	}
	if snap.FeesPaidUSDC <= 0 {
		t.Fatalf("expected positive fee paid, got %f", snap.FeesPaidUSDC)
	}
}

func TestExecuteLimitOnlyFillsWhenCrossed(t *testing.T) {
	sim := NewSimulator(Config{
		InitialBalanceUSDC: 1000,
		FeeBps:             10,
		SlippageBps:        0,
	})

	noFill, err := sim.ExecuteLimit("asset-1", "BUY", 0.51, 100, sampleBook())
	if err != nil {
		t.Fatalf("ExecuteLimit noFill: %v", err)
	}
	if noFill.Filled {
		t.Fatal("expected buy limit below best ask to remain unfilled")
	}
	if noFill.Status != "LIVE" {
		t.Fatalf("expected unfilled order status LIVE, got %s", noFill.Status)
	}
	if noFill.Price != 0.51 {
		t.Fatalf("expected unfilled order price 0.51, got %f", noFill.Price)
	}
	if noFill.AmountUSDC != 100 {
		t.Fatalf("expected unfilled amount 100, got %f", noFill.AmountUSDC)
	}
	if noFill.Size <= 0 {
		t.Fatalf("expected unfilled order to retain positive size, got %f", noFill.Size)
	}

	fill, err := sim.ExecuteLimit("asset-1", "BUY", 0.53, 100, sampleBook())
	if err != nil {
		t.Fatalf("ExecuteLimit fill: %v", err)
	}
	if !fill.Filled {
		t.Fatal("expected buy limit above best ask to fill")
	}
}

func TestExecuteMarketRejectsInsufficientBalance(t *testing.T) {
	sim := NewSimulator(Config{
		InitialBalanceUSDC: 50,
		FeeBps:             10,
	})

	if _, err := sim.ExecuteMarket("asset-1", "BUY", 100, sampleBook()); err == nil {
		t.Fatal("expected insufficient balance error for oversized BUY")
	}
}

func TestExecuteMarketRejectsInvalidSide(t *testing.T) {
	sim := NewSimulator(Config{
		InitialBalanceUSDC: 1000,
		FeeBps:             10,
	})

	if _, err := sim.ExecuteMarket("asset-1", "HOLD", 10, sampleBook()); err == nil {
		t.Fatal("expected invalid side to return error")
	}
}

func TestExecuteMarketSellAllowedByDefault(t *testing.T) {
	sim := NewSimulator(Config{
		InitialBalanceUSDC: 1000,
		FeeBps:             0,
		SlippageBps:        0,
	})

	if _, err := sim.ExecuteMarket("asset-1", "SELL", 10, sampleBook()); err != nil {
		t.Fatalf("expected SELL without inventory to be allowed by default, got: %v", err)
	}
}

func TestExecuteMarketSellRequiresInventoryWhenShortDisabled(t *testing.T) {
	sim := NewSimulator(Config{
		InitialBalanceUSDC: 1000,
		FeeBps:             0,
		SlippageBps:        0,
		AllowShort:         boolPtr(false),
	})

	// Buy 100 units at ask 0.52 -> amount 52 USDC.
	if _, err := sim.ExecuteMarket("asset-1", "BUY", 52, sampleBook()); err != nil {
		t.Fatalf("buy inventory setup failed: %v", err)
	}

	// Sell 100 units at bid 0.50 -> amount 50 USDC.
	if _, err := sim.ExecuteMarket("asset-1", "SELL", 50, sampleBook()); err != nil {
		t.Fatalf("expected SELL with inventory to succeed: %v", err)
	}

	// Additional sell should fail (inventory now exhausted).
	if _, err := sim.ExecuteMarket("asset-1", "SELL", 5, sampleBook()); err == nil {
		t.Fatal("expected SELL without remaining inventory to fail when allow_short=false")
	}
}

func TestSnapshotIncludesInventoryByAsset(t *testing.T) {
	sim := NewSimulator(Config{
		InitialBalanceUSDC: 1000,
		FeeBps:             0,
		SlippageBps:        0,
		AllowShort:         boolPtr(false),
	})

	// Buy 100 units at ask 0.52 -> amount 52 USDC.
	if _, err := sim.ExecuteMarket("asset-1", "BUY", 52, sampleBook()); err != nil {
		t.Fatalf("buy inventory setup failed: %v", err)
	}

	snap := sim.Snapshot()
	size, ok := snap.InventoryByAsset["asset-1"]
	if !ok {
		t.Fatal("expected inventory entry for asset-1")
	}
	if math.Abs(size-100) > 1e-6 {
		t.Fatalf("expected inventory size 100, got %f", size)
	}
}
