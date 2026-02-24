package strategy

import (
	"math"
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

func TestMakerQuoteZeroInventory(t *testing.T) {
	m := NewMaker(MakerConfig{
		MinSpreadBps:         20,
		SpreadMultiplier:     1.5,
		OrderSizeUSDC:        25,
		InventorySkewBps:     30,
		InventoryWidenFactor: 0.5,
		MinOrderSizeUSDC:     5,
	})

	book := ws.OrderbookEvent{
		AssetID: "token-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "100"}},
	}

	// Zero inventory should behave identically to no inventory.
	quoteNoInv, _ := m.ComputeQuote(book)
	quoteZero, _ := m.ComputeQuote(book, InventoryState{NetPosition: 0, MaxPosition: 50})

	if math.Abs(quoteNoInv.BuyPrice-quoteZero.BuyPrice) > 1e-9 {
		t.Fatalf("zero inventory buy price differs: %f vs %f", quoteNoInv.BuyPrice, quoteZero.BuyPrice)
	}
	if math.Abs(quoteNoInv.SellPrice-quoteZero.SellPrice) > 1e-9 {
		t.Fatalf("zero inventory sell price differs: %f vs %f", quoteNoInv.SellPrice, quoteZero.SellPrice)
	}
	if math.Abs(quoteNoInv.Size-quoteZero.Size) > 1e-9 {
		t.Fatalf("zero inventory size differs: %f vs %f", quoteNoInv.Size, quoteZero.Size)
	}
}

func TestMakerSkewsWhenLong(t *testing.T) {
	m := NewMaker(MakerConfig{
		MinSpreadBps:         20,
		SpreadMultiplier:     1.5,
		OrderSizeUSDC:        25,
		InventorySkewBps:     30,
		InventoryWidenFactor: 0, // Isolate skew effect.
		MinOrderSizeUSDC:     5,
	})

	book := ws.OrderbookEvent{
		AssetID: "token-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "100"}},
	}

	quoteFlat, _ := m.ComputeQuote(book, InventoryState{NetPosition: 0, MaxPosition: 50})
	quoteLong, _ := m.ComputeQuote(book, InventoryState{NetPosition: 25, MaxPosition: 50})

	flatMid := (quoteFlat.BuyPrice + quoteFlat.SellPrice) / 2
	longMid := (quoteLong.BuyPrice + quoteLong.SellPrice) / 2

	// When long, midpoint should shift down to encourage selling.
	if longMid >= flatMid {
		t.Fatalf("long skew should lower midpoint: long=%f flat=%f", longMid, flatMid)
	}
}

func TestMakerSkewsWhenShort(t *testing.T) {
	m := NewMaker(MakerConfig{
		MinSpreadBps:         20,
		SpreadMultiplier:     1.5,
		OrderSizeUSDC:        25,
		InventorySkewBps:     30,
		InventoryWidenFactor: 0, // Isolate skew effect.
		MinOrderSizeUSDC:     5,
	})

	book := ws.OrderbookEvent{
		AssetID: "token-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "100"}},
	}

	quoteFlat, _ := m.ComputeQuote(book, InventoryState{NetPosition: 0, MaxPosition: 50})
	quoteShort, _ := m.ComputeQuote(book, InventoryState{NetPosition: -25, MaxPosition: 50})

	flatMid := (quoteFlat.BuyPrice + quoteFlat.SellPrice) / 2
	shortMid := (quoteShort.BuyPrice + quoteShort.SellPrice) / 2

	// When short, midpoint should shift up to encourage buying.
	if shortMid <= flatMid {
		t.Fatalf("short skew should raise midpoint: short=%f flat=%f", shortMid, flatMid)
	}
}

func TestMakerWidensAtMaxInventory(t *testing.T) {
	m := NewMaker(MakerConfig{
		MinSpreadBps:         20,
		SpreadMultiplier:     1.5,
		OrderSizeUSDC:        25,
		InventorySkewBps:     0, // No skew to isolate widening effect.
		InventoryWidenFactor: 0.5,
		MinOrderSizeUSDC:     5,
	})

	book := ws.OrderbookEvent{
		AssetID: "token-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "100"}},
	}

	quoteFlat, _ := m.ComputeQuote(book, InventoryState{NetPosition: 0, MaxPosition: 50})
	quoteFull, _ := m.ComputeQuote(book, InventoryState{NetPosition: 50, MaxPosition: 50})

	flatSpread := quoteFlat.SellPrice - quoteFlat.BuyPrice
	fullSpread := quoteFull.SellPrice - quoteFull.BuyPrice

	// At max inventory, spread should be 1.5x the flat spread.
	expectedRatio := 1.5
	actualRatio := fullSpread / flatSpread
	if math.Abs(actualRatio-expectedRatio) > 0.01 {
		t.Fatalf("expected spread ratio ~1.5, got %f (flat=%f, full=%f)", actualRatio, flatSpread, fullSpread)
	}
}

func TestMakerReducesSize(t *testing.T) {
	m := NewMaker(MakerConfig{
		MinSpreadBps:         20,
		SpreadMultiplier:     1.5,
		OrderSizeUSDC:        100,
		InventorySkewBps:     0,
		InventoryWidenFactor: 0,
		MinOrderSizeUSDC:     5,
	})

	book := ws.OrderbookEvent{
		AssetID: "token-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "100"}},
	}

	quoteFlat, _ := m.ComputeQuote(book, InventoryState{NetPosition: 0, MaxPosition: 50})
	quoteHalf, _ := m.ComputeQuote(book, InventoryState{NetPosition: 25, MaxPosition: 50})
	quoteFull, _ := m.ComputeQuote(book, InventoryState{NetPosition: 50, MaxPosition: 50})

	if quoteFlat.Size != 100 {
		t.Fatalf("flat size should be 100, got %f", quoteFlat.Size)
	}
	// At 50% inventory: size = 100 * (1 - 0.5*0.5) = 75
	if math.Abs(quoteHalf.Size-75) > 1e-9 {
		t.Fatalf("half inventory size should be 75, got %f", quoteHalf.Size)
	}
	// At 100% inventory: size = 100 * (1 - 1*0.5) = 50
	if math.Abs(quoteFull.Size-50) > 1e-9 {
		t.Fatalf("full inventory size should be 50, got %f", quoteFull.Size)
	}
}

func TestMakerMinSizeFloor(t *testing.T) {
	m := NewMaker(MakerConfig{
		MinSpreadBps:         20,
		SpreadMultiplier:     1.5,
		OrderSizeUSDC:        8, // Small base size
		InventorySkewBps:     0,
		InventoryWidenFactor: 0,
		MinOrderSizeUSDC:     5,
	})

	book := ws.OrderbookEvent{
		AssetID: "token-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "100"}},
	}

	// At max inventory: size = 8 * (1 - 1*0.5) = 4 → floor to 5
	quote, _ := m.ComputeQuote(book, InventoryState{NetPosition: 50, MaxPosition: 50})
	if quote.Size != 5 {
		t.Fatalf("expected min size floor 5, got %f", quote.Size)
	}
}
