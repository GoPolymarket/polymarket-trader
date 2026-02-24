package strategy

import (
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
	}
	if sig.Side != "SELL" {
		t.Fatalf("expected SELL, got %s", sig.Side)
	}
}
