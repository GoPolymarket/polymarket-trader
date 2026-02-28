package telegramtmpl

import (
	"fmt"
	"strings"
)

// DailyData describes the data required to render a daily Telegram coaching message.
type DailyData struct {
	Mode                string
	Status              string
	RiskMode            string
	NetPnLAfterFeesUSDC float64
	Fills               int
	Actions             []string
	RiskHints           []string
	PriorityActionCode  string
	EstimatedUpliftUSDC float64
	ModelConfidence     string
}

// WeeklyData describes the data required to render a weekly Telegram review message.
type WeeklyData struct {
	Mode                string
	WindowLabel         string
	WindowDays          int
	TotalPnLUSDC        float64
	NetPnLAfterFeesUSDC float64
	Fills               int
	NetEdgeBps          float64
	QualityScore        float64
	Highlights          []string
	Warnings            []string
}

// BuildDailyData normalizes daily template inputs into a renderable payload.
func BuildDailyData(
	mode string,
	canTrade bool,
	riskMode string,
	netPnLAfterFeesUSDC float64,
	fills int,
	actions []string,
	riskHints []string,
) DailyData {
	status := "ACTIVE"
	if !canTrade {
		status = "PAUSE"
	}
	if len(actions) > 3 {
		actions = actions[:3]
	}
	return DailyData{
		Mode:                strings.ToUpper(strings.TrimSpace(mode)),
		Status:              status,
		RiskMode:            strings.ToUpper(strings.TrimSpace(riskMode)),
		NetPnLAfterFeesUSDC: netPnLAfterFeesUSDC,
		Fills:               fills,
		Actions:             actions,
		RiskHints:           riskHints,
	}
}

// BuildWeeklyData normalizes weekly template inputs into a renderable payload.
func BuildWeeklyData(
	mode string,
	windowLabel string,
	windowDays int,
	totalPnLUSDC float64,
	netPnLAfterFeesUSDC float64,
	fills int,
	netEdgeBps float64,
	qualityScore float64,
	highlights []string,
	warnings []string,
) WeeklyData {
	label := strings.TrimSpace(windowLabel)
	if label == "" && windowDays > 0 {
		label = fmt.Sprintf("%dd", windowDays)
	}
	return WeeklyData{
		Mode:                strings.ToUpper(strings.TrimSpace(mode)),
		WindowLabel:         label,
		WindowDays:          windowDays,
		TotalPnLUSDC:        totalPnLUSDC,
		NetPnLAfterFeesUSDC: netPnLAfterFeesUSDC,
		Fills:               fills,
		NetEdgeBps:          netEdgeBps,
		QualityScore:        qualityScore,
		Highlights:          highlights,
		Warnings:            warnings,
	}
}

// RenderDailyHTML renders a daily Telegram coaching template in HTML parse mode.
func RenderDailyHTML(d DailyData) string {
	var b strings.Builder
	b.WriteString("<b>Daily Trading Coach</b>\n")
	b.WriteString(fmt.Sprintf("Mode: %s\nStatus: %s\nRisk Mode: %s\n", d.Mode, d.Status, d.RiskMode))
	b.WriteString(fmt.Sprintf("Net PnL After Fees: %.2f USDC\nFills: %d\n", d.NetPnLAfterFeesUSDC, d.Fills))
	if len(d.Actions) > 0 {
		b.WriteString("\n<b>Top Actions</b>\n")
		for _, a := range d.Actions {
			b.WriteString("- " + a + "\n")
		}
	}
	if len(d.RiskHints) > 0 {
		b.WriteString("\n<b>Risk Hints</b>\n")
		for _, h := range d.RiskHints {
			b.WriteString("- " + h + "\n")
		}
	}
	if strings.TrimSpace(d.PriorityActionCode) != "" {
		b.WriteString("\n<b>Profit Focus</b>\n")
		b.WriteString("- Priority Action: " + strings.TrimSpace(d.PriorityActionCode) + "\n")
		b.WriteString(fmt.Sprintf("- Estimated Uplift: %.2f USDC\n", d.EstimatedUpliftUSDC))
		if strings.TrimSpace(d.ModelConfidence) != "" {
			b.WriteString("- Confidence: " + strings.TrimSpace(d.ModelConfidence) + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// RenderWeeklyHTML renders a weekly Telegram review template in HTML parse mode.
func RenderWeeklyHTML(w WeeklyData) string {
	var b strings.Builder
	b.WriteString("<b>Weekly Trading Review</b>\n")
	if w.WindowDays > 0 {
		b.WriteString(fmt.Sprintf("Window: %s (%d days)\n", w.WindowLabel, w.WindowDays))
	} else {
		b.WriteString(fmt.Sprintf("Window: %s\n", w.WindowLabel))
	}
	if w.Mode != "" {
		b.WriteString(fmt.Sprintf("Mode: %s\n", w.Mode))
	}
	b.WriteString(fmt.Sprintf("Total PnL: %.2f USDC\nNet PnL After Fees: %.2f USDC\n", w.TotalPnLUSDC, w.NetPnLAfterFeesUSDC))
	b.WriteString(fmt.Sprintf("Fills: %d\nNet Edge: %.2f bps\nQuality Score: %.2f\n", w.Fills, w.NetEdgeBps, w.QualityScore))
	if len(w.Highlights) > 0 {
		b.WriteString("\n<b>Highlights</b>\n")
		for _, h := range w.Highlights {
			b.WriteString("- " + h + "\n")
		}
	}
	if len(w.Warnings) > 0 {
		b.WriteString("\n<b>Warnings</b>\n")
		for _, warn := range w.Warnings {
			b.WriteString("- " + warn + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}
