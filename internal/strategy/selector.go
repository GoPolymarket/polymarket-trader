package strategy

import (
	"sort"
	"strconv"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/clobtypes"
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
