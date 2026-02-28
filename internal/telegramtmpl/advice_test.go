package telegramtmpl

import (
	"strings"
	"testing"
	"time"
)

func TestBuildDailyActions(t *testing.T) {
	actions := BuildDailyActions(DailyAdviceInput{
		CanTrade:        true,
		RiskMode:        "normal",
		Fills:           30,
		NetPnLAfterFees: 1.2,
		BestMarket:      "asset-1",
	})
	if len(actions) == 0 {
		t.Fatal("expected actions")
	}
	if !strings.Contains(actions[0], "strongest market") {
		t.Fatalf("expected focus market action, got %v", actions)
	}
}

func TestBuildRiskHints(t *testing.T) {
	hints := BuildRiskHints(DailyAdviceInput{
		CanTrade:          false,
		RiskUsagePct:      85,
		BlockedReasons:    []string{"emergency_stop"},
		CooldownRemaining: 2 * time.Minute,
	})
	if len(hints) < 3 {
		t.Fatalf("expected multiple risk hints, got %v", hints)
	}
}

func TestBuildWeeklyHighlightsWarnings(t *testing.T) {
	highlights, warnings := BuildWeeklyHighlightsWarnings(WeeklyAdviceInput{
		NetEdgeBps:       -1.5,
		TopMarket:        "asset-top",
		TopMarketScore:   74,
		FeeDragBps:       25,
		SlippageProxyBps: 4,
		CanTrade:         false,
	})
	if len(highlights) == 0 || len(warnings) == 0 {
		t.Fatalf("expected highlights and warnings, got highlights=%v warnings=%v", highlights, warnings)
	}
}
