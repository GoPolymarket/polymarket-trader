package strategy

import (
	"context"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/clobtypes"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/gamma"
)

type marketScore struct {
	tokenID    string
	totalDepth float64
}

func SelectMarkets(markets []clobtypes.Market, books map[string]clobtypes.OrderBook, topN int, minDepth float64) []string {
	var scores []marketScore

	for _, m := range markets {
		if !m.Active {
			continue
		}
		for _, tok := range m.Tokens {
			book, ok := books[tok.TokenID]
			if !ok || len(book.Bids) == 0 || len(book.Asks) == 0 {
				continue
			}
			var depth float64
			for _, lvl := range book.Bids {
				s, _ := strconv.ParseFloat(lvl.Size, 64)
				depth += s
			}
			for _, lvl := range book.Asks {
				s, _ := strconv.ParseFloat(lvl.Size, 64)
				depth += s
			}
			if depth >= minDepth {
				scores = append(scores, marketScore{tokenID: tok.TokenID, totalDepth: depth})
			}
		}
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i].totalDepth > scores[j].totalDepth
	})

	result := make([]string, 0, topN)
	for i := 0; i < topN && i < len(scores); i++ {
		result = append(result, scores[i].tokenID)
	}
	return result
}

// MarketCandidate is a scored market for selection.
type MarketCandidate struct {
	TokenID   string
	MarketID  string
	Question  string
	Volume24hr float64
	Liquidity  float64
	Spread     float64
	EndDate    time.Time
	Score      float64
}

// SelectorConfig controls Gamma-based market selection.
type SelectorConfig struct {
	RescanInterval time.Duration
	MinLiquidity   float64
	MinVolume24hr  float64
	MaxSpread      float64
	MinDaysToEnd   int
}

// GammaSelector uses the Gamma API to find the best markets.
type GammaSelector struct {
	gammaClient gamma.Client
	cfg         SelectorConfig
}

// NewGammaSelector creates a GammaSelector.
func NewGammaSelector(gammaClient gamma.Client, cfg SelectorConfig) *GammaSelector {
	return &GammaSelector{gammaClient: gammaClient, cfg: cfg}
}

// Select queries Gamma for active markets, filters and scores them, and returns the top N.
func (s *GammaSelector) Select(ctx context.Context, topN int) ([]MarketCandidate, error) {
	active := true
	closed := false
	markets, err := s.gammaClient.Markets(ctx, &gamma.MarketsRequest{
		Active: &active,
		Closed: &closed,
		Order:  "volume",
		Limit:  intPtr(100),
	})
	if err != nil {
		return nil, err
	}

	now := time.Now()
	var candidates []MarketCandidate

	for _, m := range markets {
		vol, _ := strconv.ParseFloat(m.Volume24hr, 64)
		liq, _ := strconv.ParseFloat(m.Liquidity, 64)
		sprd, _ := strconv.ParseFloat(m.Spread, 64)
		endDate, _ := time.Parse(time.RFC3339, m.EndDate)

		daysToEnd := endDate.Sub(now).Hours() / 24

		// Apply filters.
		if liq < s.cfg.MinLiquidity {
			continue
		}
		if vol < s.cfg.MinVolume24hr {
			continue
		}
		if sprd > s.cfg.MaxSpread && s.cfg.MaxSpread > 0 {
			continue
		}
		if daysToEnd < float64(s.cfg.MinDaysToEnd) {
			continue
		}

		// Time decay: penalize markets near resolution.
		timeDecay := math.Min(daysToEnd/30, 1.0)
		if timeDecay < 0 {
			timeDecay = 0
		}

		// Score: higher volume, higher liquidity, lower spread → better.
		score := vol * liq / (sprd + 0.001) * timeDecay

		tokens := m.ParsedTokens()
		for _, tok := range tokens {
			candidates = append(candidates, MarketCandidate{
				TokenID:    tok.TokenID,
				MarketID:   m.ConditionID,
				Question:   m.Question,
				Volume24hr: vol,
				Liquidity:  liq,
				Spread:     sprd,
				EndDate:    endDate,
				Score:      score,
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	if topN > len(candidates) {
		topN = len(candidates)
	}
	return candidates[:topN], nil
}

func intPtr(v int) *int { return &v }
