package paper

import (
	"math"
	"testing"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"
)

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
