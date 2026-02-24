package strategy

import (
	"testing"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/clobtypes"
)

func TestSelectTopMarkets(t *testing.T) {
	markets := []clobtypes.Market{
		{ID: "m1", Tokens: []clobtypes.MarketToken{{TokenID: "t1", Price: 0.50}}, Active: true},
		{ID: "m2", Tokens: []clobtypes.MarketToken{{TokenID: "t2", Price: 0.90}}, Active: true},
		{ID: "m3", Tokens: []clobtypes.MarketToken{{TokenID: "t3", Price: 0.50}}, Active: true},
		{ID: "m4", Tokens: []clobtypes.MarketToken{{TokenID: "t4", Price: 0.50}}, Active: false},
	}

	books := map[string]clobtypes.OrderBook{
		"t1": {Bids: []clobtypes.PriceLevel{{Price: "0.49", Size: "500"}}, Asks: []clobtypes.PriceLevel{{Price: "0.51", Size: "500"}}},
		"t2": {Bids: []clobtypes.PriceLevel{{Price: "0.89", Size: "10"}}, Asks: []clobtypes.PriceLevel{{Price: "0.91", Size: "10"}}},
		"t3": {Bids: []clobtypes.PriceLevel{{Price: "0.49", Size: "200"}}, Asks: []clobtypes.PriceLevel{{Price: "0.51", Size: "200"}}},
	}

	selected := SelectMarkets(markets, books, 2, 50)
	if len(selected) != 2 {
		t.Fatalf("expected 2 selected, got %d", len(selected))
	}
	if selected[0] != "t1" {
		t.Fatalf("expected t1 first, got %s", selected[0])
	}
	if selected[1] != "t3" {
		t.Fatalf("expected t3 second, got %s", selected[1])
	}
}

func TestSelectMarketsEmpty(t *testing.T) {
	selected := SelectMarkets(nil, nil, 5, 50)
	if len(selected) != 0 {
		t.Fatalf("expected 0, got %d", len(selected))
	}
}
