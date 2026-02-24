package strategy

import (
	"testing"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"
)

func TestMakerQuote(t *testing.T) {
	m := NewMaker(MakerConfig{
		MinSpreadBps:     20,
		SpreadMultiplier: 1.5,
		OrderSizeUSDC:    25,
	})

	book := ws.OrderbookEvent{
		AssetID: "token-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "100"}},
	}

	quote, err := m.ComputeQuote(book)
	if err != nil {
		t.Fatal(err)
	}
	if quote.AssetID != "token-1" {
		t.Fatalf("expected token-1, got %s", quote.AssetID)
	}
	if quote.BuyPrice >= quote.SellPrice {
		t.Fatalf("buy %f should be less than sell %f", quote.BuyPrice, quote.SellPrice)
	}
	if quote.Size != 25 {
		t.Fatalf("expected size 25, got %f", quote.Size)
	}
}

func TestMakerSkipsEmptyBook(t *testing.T) {
	m := NewMaker(MakerConfig{MinSpreadBps: 20, SpreadMultiplier: 1.5, OrderSizeUSDC: 25})
	book := ws.OrderbookEvent{AssetID: "token-1"}
	_, err := m.ComputeQuote(book)
	if err == nil {
		t.Fatal("expected error on empty book")
	}
}

func TestMakerMinSpreadEnforced(t *testing.T) {
	m := NewMaker(MakerConfig{MinSpreadBps: 100, SpreadMultiplier: 1.0, OrderSizeUSDC: 25})
	book := ws.OrderbookEvent{
		AssetID: "token-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.505", Size: "100"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.506", Size: "100"}},
	}
	quote, err := m.ComputeQuote(book)
	if err != nil {
		t.Fatal(err)
	}
	mid := (0.505 + 0.506) / 2
	minHalfSpread := mid * 50 / 10000
	actualHalf := (quote.SellPrice - quote.BuyPrice) / 2
	if actualHalf < minHalfSpread-0.0001 {
		t.Fatalf("half spread %f less than min %f", actualHalf, minHalfSpread)
	}
}
