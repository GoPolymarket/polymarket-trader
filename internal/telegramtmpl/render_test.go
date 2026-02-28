package telegramtmpl

import (
	"strings"
	"testing"
)

func TestRenderDailyHTML(t *testing.T) {
	msg := RenderDailyHTML(DailyData{
		Mode:                "paper",
		Status:              "ACTIVE",
		RiskMode:            "NORMAL",
		NetPnLAfterFeesUSDC: 3.5,
		Fills:               12,
		Actions:             []string{"Focus top market", "Keep defensive size"},
		RiskHints:           []string{"Daily loss usage is high"},
	})

	if !strings.Contains(msg, "Daily Trading Coach") {
		t.Fatalf("expected daily title, got %q", msg)
	}
	if !strings.Contains(msg, "Top Actions") {
		t.Fatalf("expected actions section, got %q", msg)
	}
	if !strings.Contains(msg, "Risk Hints") {
		t.Fatalf("expected risk hints section, got %q", msg)
	}
}

func TestRenderWeeklyHTML(t *testing.T) {
	msg := RenderWeeklyHTML(WeeklyData{
		Mode:                "paper",
		WindowLabel:         "7d",
		WindowDays:          7,
		TotalPnLUSDC:        8.1,
		NetPnLAfterFeesUSDC: 7.7,
		Fills:               30,
		NetEdgeBps:          24,
		QualityScore:        78,
		Highlights:          []string{"Net edge positive"},
		Warnings:            []string{"Fee drag elevated"},
	})

	if !strings.Contains(msg, "Weekly Trading Review") {
		t.Fatalf("expected weekly title, got %q", msg)
	}
	if !strings.Contains(msg, "Highlights") {
		t.Fatalf("expected highlights section, got %q", msg)
	}
	if !strings.Contains(msg, "Warnings") {
		t.Fatalf("expected warnings section, got %q", msg)
	}
}
