package telegramtmpl

import (
	"fmt"
	"strings"
	"time"
)

// DailyAdviceInput describes inputs for generating daily actions and risk hints.
type DailyAdviceInput struct {
	CanTrade          bool
	RiskMode          string
	Fills             int
	NetPnLAfterFees   float64
	BestMarket        string
	RiskUsagePct      float64
	BlockedReasons    []string
	CooldownRemaining time.Duration
}

// WeeklyAdviceInput describes inputs for generating weekly highlights and warnings.
type WeeklyAdviceInput struct {
	NetEdgeBps       float64
	TopMarket        string
	TopMarketScore   float64
	FeeDragBps       float64
	SlippageProxyBps float64
	CanTrade         bool
}

// BuildDailyActions generates prioritized daily actions shared by API and app paths.
func BuildDailyActions(in DailyAdviceInput) []string {
	actions := make([]string, 0, 5)
	riskMode := strings.ToUpper(strings.TrimSpace(in.RiskMode))
	if !in.CanTrade {
		actions = append(actions, "Pause new trades until risk blockers clear.")
	} else if riskMode == "DEFENSIVE" {
		actions = append(actions, "Run defensive size mode (50%) for next cycle.")
	}
	if in.Fills < 20 {
		actions = append(actions, "Collect at least 20 fills before scaling size.")
	}
	if in.NetPnLAfterFees <= 0 {
		actions = append(actions, "Improve selectivity: tighten entry filters.")
	}
	if strings.TrimSpace(in.BestMarket) != "" {
		actions = append(actions, fmt.Sprintf("Focus allocation on strongest market: %s.", in.BestMarket))
	}
	if len(actions) == 0 {
		actions = append(actions, "Keep current execution discipline and monitor drift.")
	}
	if len(actions) > 3 {
		actions = actions[:3]
	}
	return actions
}

// BuildRiskHints generates risk hints shared by API and app template paths.
func BuildRiskHints(in DailyAdviceInput) []string {
	hints := make([]string, 0, 4)
	if !in.CanTrade {
		hints = append(hints, "PAUSE: risk guardrails are blocking new trades.")
	}
	if in.RiskUsagePct >= 80 {
		hints = append(hints, fmt.Sprintf("Daily loss usage is high (%.1f%%).", in.RiskUsagePct))
	}
	if len(in.BlockedReasons) > 0 {
		hints = append(hints, "Blocked reasons: "+strings.Join(in.BlockedReasons, ","))
	}
	if in.CooldownRemaining > 0 {
		hints = append(hints, fmt.Sprintf("Cooldown remaining: %.0fs.", in.CooldownRemaining.Seconds()))
	}
	return hints
}

// BuildWeeklyHighlightsWarnings generates weekly review highlights and warnings.
func BuildWeeklyHighlightsWarnings(in WeeklyAdviceInput) (highlights []string, warnings []string) {
	highlights = make([]string, 0, 3)
	warnings = make([]string, 0, 4)
	if in.NetEdgeBps > 0 {
		highlights = append(highlights, fmt.Sprintf("Net edge remains positive at %.2f bps.", in.NetEdgeBps))
	} else {
		warnings = append(warnings, fmt.Sprintf("Net edge is non-positive at %.2f bps.", in.NetEdgeBps))
	}
	if strings.TrimSpace(in.TopMarket) != "" {
		if in.TopMarketScore > 0 {
			highlights = append(highlights, fmt.Sprintf("Top market this period: %s (score %.2f).", in.TopMarket, in.TopMarketScore))
		} else {
			highlights = append(highlights, fmt.Sprintf("Best realized market: %s.", in.TopMarket))
		}
	}
	if in.FeeDragBps >= 20 {
		warnings = append(warnings, fmt.Sprintf("Fee drag is elevated (%.2f bps).", in.FeeDragBps))
	}
	if in.SlippageProxyBps >= 3 {
		warnings = append(warnings, fmt.Sprintf("Slippage proxy is elevated (%.2f bps).", in.SlippageProxyBps))
	}
	if !in.CanTrade {
		warnings = append(warnings, "Trading is currently paused by risk guardrails.")
	}
	return highlights, warnings
}
