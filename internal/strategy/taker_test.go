package strategy

import (
	"math"
	"testing"
	"time"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"
)

func TestTakerSignal(t *testing.T) {
	tk := NewTaker(TakerConfig{
		MinImbalance:   0.15,
		DepthLevels:    2,
		AmountUSDC:     20,
		MaxSlippageBps: 30,
		Cooldown:       1 * time.Second,
	})

	book := ws.OrderbookEvent{
		AssetID: "token-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "300"}, {Price: "0.49", Size: "200"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "50"}, {Price: "0.53", Size: "50"}},
	}

	sig, err := tk.Evaluate(book)
	if err != nil {
		t.Fatal(err)
	}
	if sig == nil {
		t.Fatal("expected signal")
		return
	}
	if sig.Side != "BUY" {
		t.Fatalf("expected BUY, got %s", sig.Side)
	}
	if sig.AmountUSDC != 20 {
		t.Fatalf("expected amount 20, got %f", sig.AmountUSDC)
	}
}

func TestTakerNoSignalLowImbalance(t *testing.T) {
	tk := NewTaker(TakerConfig{
		MinImbalance: 0.15,
		DepthLevels:  2,
		AmountUSDC:   20,
		Cooldown:     1 * time.Second,
	})

	book := ws.OrderbookEvent{
		AssetID: "token-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}, {Price: "0.49", Size: "100"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "100"}, {Price: "0.53", Size: "100"}},
	}

	sig, err := tk.Evaluate(book)
	if err != nil {
		t.Fatal(err)
	}
	if sig != nil {
		t.Fatal("expected no signal on balanced book")
	}
}

func TestTakerCooldown(t *testing.T) {
	tk := NewTaker(TakerConfig{
		MinImbalance: 0.10,
		DepthLevels:  1,
		AmountUSDC:   20,
		Cooldown:     100 * time.Millisecond,
	})

	book := ws.OrderbookEvent{
		AssetID: "token-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "300"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "50"}},
	}

	sig1, _ := tk.Evaluate(book)
	if sig1 == nil {
		t.Fatal("expected first signal")
	}
	tk.RecordTrade("token-1")

	sig2, _ := tk.Evaluate(book)
	if sig2 != nil {
		t.Fatal("expected cooldown block")
	}

	time.Sleep(150 * time.Millisecond)
	sig3, _ := tk.Evaluate(book)
	if sig3 == nil {
		t.Fatal("expected signal after cooldown")
	}
}

func TestTakerSellSignal(t *testing.T) {
	tk := NewTaker(TakerConfig{
		MinImbalance: 0.15,
		DepthLevels:  1,
		AmountUSDC:   20,
		Cooldown:     1 * time.Second,
	})

	book := ws.OrderbookEvent{
		AssetID: "token-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "50"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "300"}},
	}

	sig, _ := tk.Evaluate(book)
	if sig == nil {
		t.Fatal("expected signal")
		return
	}
	if sig.Side != "SELL" {
		t.Fatalf("expected SELL, got %s", sig.Side)
	}
}

func TestFlowTrackerNetFlow(t *testing.T) {
	ft := NewFlowTracker(1 * time.Minute)

	ft.Record("asset-1", "BUY", 100, 0.50)
	ft.Record("asset-1", "BUY", 50, 0.51)
	ft.Record("asset-1", "SELL", 50, 0.49)

	nf := ft.NetFlow("asset-1")
	// (150 - 50) / 200 = 0.5
	if math.Abs(nf-0.5) > 1e-9 {
		t.Fatalf("expected net flow 0.5, got %f", nf)
	}

	// VWAP: (100*0.50 + 50*0.51 + 50*0.49) / 200 = (50+25.5+24.5)/200 = 0.50
	vwap := ft.VWAP("asset-1")
	if math.Abs(vwap-0.50) > 1e-9 {
		t.Fatalf("expected VWAP 0.50, got %f", vwap)
	}
}

func TestFlowTrackerWindowExpiry(t *testing.T) {
	ft := NewFlowTracker(50 * time.Millisecond)

	ft.Record("asset-1", "BUY", 100, 0.50)

	// Within window.
	nf := ft.NetFlow("asset-1")
	if nf != 1.0 {
		t.Fatalf("expected 1.0 within window, got %f", nf)
	}

	time.Sleep(80 * time.Millisecond)

	// After window expires.
	nf = ft.NetFlow("asset-1")
	if nf != 0 {
		t.Fatalf("expected 0 after window expiry, got %f", nf)
	}
}

func TestConvergenceDetection(t *testing.T) {
	tk := NewTaker(TakerConfig{MinConvergenceBps: 50})

	// Sum = 1.01 → 100 bps overpriced.
	sig, edge := tk.DetectConvergence(0.55, 0.46)
	if sig == "" {
		t.Fatal("expected convergence signal")
	}
	if edge < 50 {
		t.Fatalf("expected edge >= 50 bps, got %f", edge)
	}

	// Sum = 0.99 → not enough edge (only 100 bps → should trigger at >= 50).
	sig2, _ := tk.DetectConvergence(0.50, 0.49)
	if sig2 == "" {
		t.Fatal("expected convergence signal for 100 bps deviation")
	}

	// Sum = 1.004 → only 40 bps, below threshold.
	sig3, _ := tk.DetectConvergence(0.502, 0.502)
	if sig3 != "" {
		t.Fatal("expected no signal below threshold")
	}
}

func TestCompositeSignalStrong(t *testing.T) {
	tk := NewTaker(TakerConfig{
		MinImbalance:      0.05,
		DepthLevels:       1,
		AmountUSDC:        20,
		MaxSlippageBps:    30,
		Cooldown:          100 * time.Millisecond,
		ImbalanceWeight:   0.5,
		FlowWeight:        0.3,
		ConvergenceWeight: 0.2,
		MinCompositeScore: 0.2,
		MinConvergenceBps: 50,
	})

	ft := NewFlowTracker(1 * time.Minute)
	ft.Record("asset-1", "BUY", 100, 0.50)

	book := ws.OrderbookEvent{
		AssetID: "asset-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "300"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "50"}},
	}

	sig, err := tk.EvaluateEnhanced(book, ft, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sig == nil {
		t.Fatal("expected strong composite signal")
		return
	}
	if sig.Side != "BUY" {
		t.Fatalf("expected BUY (strong bid imbalance + buy flow), got %s", sig.Side)
	}
}

func TestCompositeSignalWeak(t *testing.T) {
	tk := NewTaker(TakerConfig{
		MinImbalance:      0.05,
		DepthLevels:       1,
		AmountUSDC:        20,
		MaxSlippageBps:    30,
		Cooldown:          100 * time.Millisecond,
		ImbalanceWeight:   0.5,
		FlowWeight:        0.3,
		ConvergenceWeight: 0.2,
		MinCompositeScore: 0.9, // Very high threshold.
	})

	book := ws.OrderbookEvent{
		AssetID: "asset-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "120"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "100"}},
	}

	sig, _ := tk.EvaluateEnhanced(book, nil, 0)
	if sig != nil {
		t.Fatal("expected no signal with weak composite")
	}
}

func TestAdaptiveSizing(t *testing.T) {
	tk := NewTaker(TakerConfig{
		MinImbalance:      0.05,
		DepthLevels:       1,
		AmountUSDC:        20,
		MaxSlippageBps:    30,
		Cooldown:          100 * time.Millisecond,
		ImbalanceWeight:   0.5,
		FlowWeight:        0.3,
		ConvergenceWeight: 0.2,
		MinCompositeScore: 0.1,
	})

	ft := NewFlowTracker(1 * time.Minute)
	ft.Record("asset-1", "BUY", 1000, 0.50)

	// Very strong imbalance + flow should size up.
	book := ws.OrderbookEvent{
		AssetID: "asset-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "500"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "10"}},
	}

	sig, _ := tk.EvaluateEnhanced(book, ft, 0)
	if sig == nil {
		t.Fatal("expected signal")
		return
	}
	// With very high composite score, amount should be > base amount.
	if sig.AmountUSDC <= 20 {
		t.Fatalf("expected adaptive sizing > 20, got %f", sig.AmountUSDC)
	}
	// Max is 1.5x.
	if sig.AmountUSDC > 30+0.01 {
		t.Fatalf("expected max 30 (1.5x), got %f", sig.AmountUSDC)
	}
}

func TestEvaluateEnhancedBackwardCompat(t *testing.T) {
	// With no flow tracker and no counterpart price, should still work.
	tk := NewTaker(TakerConfig{
		MinImbalance:      0.05,
		DepthLevels:       1,
		AmountUSDC:        20,
		MaxSlippageBps:    30,
		Cooldown:          100 * time.Millisecond,
		ImbalanceWeight:   0.5,
		FlowWeight:        0.3,
		ConvergenceWeight: 0.2,
		MinCompositeScore: 0.1,
	})

	book := ws.OrderbookEvent{
		AssetID: "asset-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "300"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "50"}},
	}

	sig, err := tk.EvaluateEnhanced(book, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sig == nil {
		t.Fatal("expected signal with imbalance-only")
		return
	}
}
