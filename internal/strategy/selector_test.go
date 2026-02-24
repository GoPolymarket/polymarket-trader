package strategy

import (
	"context"
	"testing"
	"time"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/clobtypes"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/gamma"
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

// mockGammaClient implements gamma.Client for testing.
type mockGammaClient struct {
	gamma.Client // embed to satisfy interface; panics if unused methods are called
	markets      []gamma.Market
	err          error
}

func (m *mockGammaClient) Markets(_ context.Context, _ *gamma.MarketsRequest) ([]gamma.Market, error) {
	return m.markets, m.err
}

func TestGammaSelectorScoring(t *testing.T) {
	endDate := time.Now().Add(60 * 24 * time.Hour).Format(time.RFC3339)
	mock := &mockGammaClient{
		markets: []gamma.Market{
			{
				ID: "m1", Question: "Market 1", ConditionID: "cond1",
				Volume24hr: "5000", Liquidity: "10000", Spread: "0.05", EndDate: endDate,
				Tokens: []gamma.Token{{TokenID: "t1", Outcome: "Yes"}},
				Active: true,
			},
			{
				ID: "m2", Question: "Market 2", ConditionID: "cond2",
				Volume24hr: "1000", Liquidity: "5000", Spread: "0.03", EndDate: endDate,
				Tokens: []gamma.Token{{TokenID: "t2", Outcome: "Yes"}},
				Active: true,
			},
		},
	}

	s := NewGammaSelector(mock, SelectorConfig{
		MinLiquidity:  500,
		MinVolume24hr: 500,
		MaxSpread:     0.10,
		MinDaysToEnd:  2,
	})

	candidates, err := s.Select(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	// Market 1 should score higher: more volume, more liquidity.
	if candidates[0].TokenID != "t1" {
		t.Fatalf("expected t1 first (higher score), got %s", candidates[0].TokenID)
	}
	if candidates[0].Score <= 0 {
		t.Fatal("expected positive score")
	}
}

func TestGammaSelectorFilters(t *testing.T) {
	endDate := time.Now().Add(60 * 24 * time.Hour).Format(time.RFC3339)
	nearEnd := time.Now().Add(12 * time.Hour).Format(time.RFC3339)
	mock := &mockGammaClient{
		markets: []gamma.Market{
			{
				ID: "low-liq", Volume24hr: "1000", Liquidity: "100", Spread: "0.05", EndDate: endDate,
				Tokens: []gamma.Token{{TokenID: "t-low-liq"}}, Active: true,
			},
			{
				ID: "low-vol", Volume24hr: "10", Liquidity: "5000", Spread: "0.05", EndDate: endDate,
				Tokens: []gamma.Token{{TokenID: "t-low-vol"}}, Active: true,
			},
			{
				ID: "wide-spread", Volume24hr: "1000", Liquidity: "5000", Spread: "0.20", EndDate: endDate,
				Tokens: []gamma.Token{{TokenID: "t-wide"}}, Active: true,
			},
			{
				ID: "near-end", Volume24hr: "1000", Liquidity: "5000", Spread: "0.05", EndDate: nearEnd,
				Tokens: []gamma.Token{{TokenID: "t-near"}}, Active: true,
			},
			{
				ID: "good", Volume24hr: "1000", Liquidity: "5000", Spread: "0.05", EndDate: endDate,
				Tokens: []gamma.Token{{TokenID: "t-good"}}, Active: true,
			},
		},
	}

	s := NewGammaSelector(mock, SelectorConfig{
		MinLiquidity:  1000,
		MinVolume24hr: 500,
		MaxSpread:     0.10,
		MinDaysToEnd:  2,
	})

	candidates, err := s.Select(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate after filtering, got %d", len(candidates))
	}
	if candidates[0].TokenID != "t-good" {
		t.Fatalf("expected t-good, got %s", candidates[0].TokenID)
	}
}

func TestGammaSelectorTopN(t *testing.T) {
	endDate := time.Now().Add(60 * 24 * time.Hour).Format(time.RFC3339)
	mock := &mockGammaClient{
		markets: []gamma.Market{
			{ID: "m1", Volume24hr: "5000", Liquidity: "10000", Spread: "0.05", EndDate: endDate, Tokens: []gamma.Token{{TokenID: "t1"}}, Active: true},
			{ID: "m2", Volume24hr: "3000", Liquidity: "8000", Spread: "0.04", EndDate: endDate, Tokens: []gamma.Token{{TokenID: "t2"}}, Active: true},
			{ID: "m3", Volume24hr: "1000", Liquidity: "5000", Spread: "0.03", EndDate: endDate, Tokens: []gamma.Token{{TokenID: "t3"}}, Active: true},
		},
	}

	s := NewGammaSelector(mock, SelectorConfig{MinLiquidity: 500, MinVolume24hr: 500, MaxSpread: 0.10, MinDaysToEnd: 2})
	candidates, _ := s.Select(context.Background(), 2)
	if len(candidates) != 2 {
		t.Fatalf("expected 2 (topN), got %d", len(candidates))
	}
}

func TestGammaSelectorEmpty(t *testing.T) {
	mock := &mockGammaClient{markets: nil}
	s := NewGammaSelector(mock, SelectorConfig{MinLiquidity: 1000, MinVolume24hr: 500})
	candidates, err := s.Select(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected 0 candidates, got %d", len(candidates))
	}
}
