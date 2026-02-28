package telegramtmpl

import (
	"strings"
	"testing"
)

func TestRenderDailyHTML(t *testing.T) {
	data := BuildDailyData(
		"paper",
		true,
		"normal",
		3.5,
		12,
		[]string{"Focus top market", "Keep defensive size"},
		[]string{"Daily loss usage is high"},
	)
	msg := RenderDailyHTML(data)

	if !strings.Contains(msg, "Daily Trading Coach") {
		t.Fatalf("expected daily title, got %q", msg)
	}
	if !strings.Contains(msg, "Top Actions") {
		t.Fatalf("expected actions section, got %q", msg)
	}
	if !strings.Contains(msg, "Risk Hints") {
		t.Fatalf("expected risk hints section, got %q", msg)
	}
	if !strings.Contains(msg, "Mode: PAPER") {
		t.Fatalf("expected uppercased mode, got %q", msg)
	}
}

func TestRenderWeeklyHTML(t *testing.T) {
	data := BuildWeeklyData(
		"paper",
		"7d",
		7,
		8.1,
		7.7,
		30,
		24,
		78,
		[]string{"Net edge positive"},
		[]string{"Fee drag elevated"},
	)
	msg := RenderWeeklyHTML(data)

	if !strings.Contains(msg, "Weekly Trading Review") {
		t.Fatalf("expected weekly title, got %q", msg)
	}
	if !strings.Contains(msg, "Highlights") {
		t.Fatalf("expected highlights section, got %q", msg)
	}
	if !strings.Contains(msg, "Warnings") {
		t.Fatalf("expected warnings section, got %q", msg)
	}
	if !strings.Contains(msg, "Mode: PAPER") {
		t.Fatalf("expected uppercased mode, got %q", msg)
	}
}

func TestBuildDailyDataLimitsActions(t *testing.T) {
	data := BuildDailyData(
		"live",
		false,
		"defensive",
		-1.2,
		5,
		[]string{"a1", "a2", "a3", "a4"},
		nil,
	)
	if len(data.Actions) != 3 {
		t.Fatalf("expected actions limited to 3, got %d", len(data.Actions))
	}
	if data.Status != "PAUSE" {
		t.Fatalf("expected status PAUSE, got %s", data.Status)
	}
}

func TestRenderDailyHTMLIncludesProfitFocus(t *testing.T) {
	data := BuildDailyData(
		"paper",
		true,
		"normal",
		2.1,
		28,
		[]string{"Keep discipline"},
		nil,
	)
	data.PriorityActionCode = "reduce_fee_drag"
	data.EstimatedUpliftUSDC = 1.23
	data.ModelConfidence = "medium"

	msg := RenderDailyHTML(data)
	if !strings.Contains(msg, "Profit Focus") {
		t.Fatalf("expected profit focus section, got %q", msg)
	}
	if !strings.Contains(msg, "reduce_fee_drag") {
		t.Fatalf("expected priority action in message, got %q", msg)
	}
	if !strings.Contains(msg, "1.23 USDC") {
		t.Fatalf("expected uplift amount in message, got %q", msg)
	}
}
