package app

import (
	"math"
	"strings"
	"sync"
	"time"
)

const (
	kpiWindow30d                  = 30 * 24 * time.Hour
	defaultTakerRealizationWindow = 5 * time.Minute
)

type kpiRiskSample struct {
	at       time.Time
	canTrade bool
}

type kpiPnLSample struct {
	at       time.Time
	realized float64
	total    float64
	net      float64
}

type kpiPendingTakerSignal struct {
	assetID    string
	side       string
	triggerMid float64
	dueAt      time.Time
}

type kpiCollector struct {
	mu sync.RWMutex

	dayStartUTC time.Time
	lastUpdated time.Time

	makerSignalCountDaily int
	takerSignalCountDaily int
	submittedOrdersDaily  int
	filledOrdersDaily     int

	riskBlockEventsDaily         int
	riskBlockEventsDailyByReason map[string]int
	riskBlockLastReason          string

	cooldownTriggerCountDaily int

	emergencyStopActive                bool
	emergencyStopActiveSinceUTC        time.Time
	emergencyStopActiveDurationDaily   time.Duration
	makerSpreadCaptureBpsSumDaily      float64
	makerSpreadCaptureSamplesDaily     int
	takerRealizationCorrectDaily       int
	takerRealizationEvaluatedDaily     int
	takerRealizationWindowMinutes      int
	pendingTakerSignals                []kpiPendingTakerSignal
	riskComplianceSamples              []kpiRiskSample
	pnlSamples                         []kpiPnLSample
	currentRealizedPnL                 float64
	currentTotalPnL                    float64
	currentNetPnLAfterFees             float64
	dailyBaselineSet                   bool
	dailyBaselineRealizedPnL           float64
	dailyBaselineTotalPnL              float64
	dailyBaselineNetPnLAfterFees       float64
	netPnL30dWindowEffectiveDaysCached int
}

func newKPICollector() *kpiCollector {
	now := time.Now().UTC()
	return &kpiCollector{
		dayStartUTC:                   startOfUTCDay(now),
		lastUpdated:                   now,
		riskBlockEventsDailyByReason:  make(map[string]int),
		takerRealizationWindowMinutes: int(defaultTakerRealizationWindow / time.Minute),
	}
}

func startOfUTCDay(t time.Time) time.Time {
	utc := t.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (c *kpiCollector) ensureDayLocked(now time.Time) {
	day := startOfUTCDay(now)
	if day.Equal(c.dayStartUTC) {
		return
	}

	if c.emergencyStopActive {
		activeSince := c.emergencyStopActiveSinceUTC
		if activeSince.Before(c.dayStartUTC) {
			activeSince = c.dayStartUTC
		}
		if day.After(activeSince) {
			c.emergencyStopActiveDurationDaily += day.Sub(activeSince)
		}
		c.emergencyStopActiveSinceUTC = day
	}

	c.dayStartUTC = day
	c.makerSignalCountDaily = 0
	c.takerSignalCountDaily = 0
	c.submittedOrdersDaily = 0
	c.filledOrdersDaily = 0
	c.riskBlockEventsDaily = 0
	c.riskBlockEventsDailyByReason = make(map[string]int)
	c.riskBlockLastReason = ""
	c.cooldownTriggerCountDaily = 0
	c.emergencyStopActiveDurationDaily = 0
	c.makerSpreadCaptureBpsSumDaily = 0
	c.makerSpreadCaptureSamplesDaily = 0
	c.takerRealizationCorrectDaily = 0
	c.takerRealizationEvaluatedDaily = 0
	c.pendingTakerSignals = nil

	c.dailyBaselineRealizedPnL = c.currentRealizedPnL
	c.dailyBaselineTotalPnL = c.currentTotalPnL
	c.dailyBaselineNetPnLAfterFees = c.currentNetPnLAfterFees
	c.dailyBaselineSet = true
}

func (c *kpiCollector) pruneLocked(now time.Time) {
	cutoff := now.Add(-kpiWindow30d)

	for len(c.riskComplianceSamples) > 0 && c.riskComplianceSamples[0].at.Before(cutoff) {
		c.riskComplianceSamples = c.riskComplianceSamples[1:]
	}

	for len(c.pnlSamples) > 2 && c.pnlSamples[1].at.Before(cutoff) {
		c.pnlSamples = c.pnlSamples[1:]
	}

	filtered := c.pendingTakerSignals[:0]
	for _, pending := range c.pendingTakerSignals {
		if pending.dueAt.Before(cutoff) {
			continue
		}
		filtered = append(filtered, pending)
	}
	c.pendingTakerSignals = filtered
}

func normalizeRiskReason(reason string) string {
	clean := strings.ToLower(strings.TrimSpace(reason))
	if clean == "" {
		return "unknown"
	}
	switch clean {
	case "open_orders", "daily_loss", "cooldown", "emergency_stop", "position_limit":
		return clean
	default:
		return "unknown"
	}
}

func normalizeSide(side string) string {
	upper := strings.ToUpper(strings.TrimSpace(side))
	if upper == "BUY" || upper == "SELL" {
		return upper
	}
	return ""
}

func round6(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

func (c *kpiCollector) recordMakerSignal(now time.Time, spreadCaptureBps float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureDayLocked(now)
	c.makerSignalCountDaily++
	if !math.IsNaN(spreadCaptureBps) && !math.IsInf(spreadCaptureBps, 0) {
		c.makerSpreadCaptureBpsSumDaily += spreadCaptureBps
		c.makerSpreadCaptureSamplesDaily++
	}
	c.lastUpdated = now
}

func (c *kpiCollector) recordTakerSignal(now time.Time, assetID, side string, triggerMid float64, horizon time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureDayLocked(now)
	c.takerSignalCountDaily++
	if horizon <= 0 {
		horizon = defaultTakerRealizationWindow
	}
	side = normalizeSide(side)
	if side != "" && assetID != "" && triggerMid > 0 {
		c.pendingTakerSignals = append(c.pendingTakerSignals, kpiPendingTakerSignal{
			assetID:    assetID,
			side:       side,
			triggerMid: triggerMid,
			dueAt:      now.Add(horizon),
		})
		c.takerRealizationWindowMinutes = int(horizon / time.Minute)
	}
	c.lastUpdated = now
}

func (c *kpiCollector) evaluateTakerRealization(now time.Time, assetID string, currentMid float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureDayLocked(now)
	if assetID == "" || currentMid <= 0 || len(c.pendingTakerSignals) == 0 {
		return
	}

	filtered := c.pendingTakerSignals[:0]
	for _, pending := range c.pendingTakerSignals {
		if pending.assetID != assetID {
			filtered = append(filtered, pending)
			continue
		}
		if now.Before(pending.dueAt) {
			filtered = append(filtered, pending)
			continue
		}

		c.takerRealizationEvaluatedDaily++
		if (pending.side == "BUY" && currentMid > pending.triggerMid) ||
			(pending.side == "SELL" && currentMid < pending.triggerMid) {
			c.takerRealizationCorrectDaily++
		}
	}
	c.pendingTakerSignals = filtered
	c.lastUpdated = now
}

func (c *kpiCollector) recordOrderSubmitted(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureDayLocked(now)
	c.submittedOrdersDaily++
	c.lastUpdated = now
}

func (c *kpiCollector) recordFill(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureDayLocked(now)
	c.filledOrdersDaily++
	c.lastUpdated = now
}

func (c *kpiCollector) recordRiskBlock(now time.Time, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureDayLocked(now)
	c.riskBlockEventsDaily++
	reason = normalizeRiskReason(reason)
	c.riskBlockEventsDailyByReason[reason]++
	c.riskBlockLastReason = reason
	c.lastUpdated = now
}

func (c *kpiCollector) recordCooldownTrigger(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureDayLocked(now)
	c.cooldownTriggerCountDaily++
	c.lastUpdated = now
}

func (c *kpiCollector) setEmergencyStop(now time.Time, active bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureDayLocked(now)
	if c.emergencyStopActive == active {
		return
	}
	if active {
		c.emergencyStopActive = true
		c.emergencyStopActiveSinceUTC = now
	} else {
		activeSince := c.emergencyStopActiveSinceUTC
		if activeSince.Before(c.dayStartUTC) {
			activeSince = c.dayStartUTC
		}
		if now.After(activeSince) {
			c.emergencyStopActiveDurationDaily += now.Sub(activeSince)
		}
		c.emergencyStopActive = false
		c.emergencyStopActiveSinceUTC = time.Time{}
	}
	c.lastUpdated = now
}

func (c *kpiCollector) recordRiskCompliance(now time.Time, canTrade bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureDayLocked(now)
	c.riskComplianceSamples = append(c.riskComplianceSamples, kpiRiskSample{at: now, canTrade: canTrade})
	c.pruneLocked(now)
	c.lastUpdated = now
}

func (c *kpiCollector) recordPnLSample(now time.Time, realizedPnL, totalPnL, feesPaid float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureDayLocked(now)

	net := totalPnL - feesPaid
	c.currentRealizedPnL = realizedPnL
	c.currentTotalPnL = totalPnL
	c.currentNetPnLAfterFees = net
	if !c.dailyBaselineSet {
		c.dailyBaselineRealizedPnL = realizedPnL
		c.dailyBaselineTotalPnL = totalPnL
		c.dailyBaselineNetPnLAfterFees = net
		c.dailyBaselineSet = true
	}

	c.pnlSamples = append(c.pnlSamples, kpiPnLSample{
		at:       now,
		realized: realizedPnL,
		total:    totalPnL,
		net:      net,
	})
	c.pruneLocked(now)
	c.lastUpdated = now
}

func (c *kpiCollector) snapshot(now time.Time) map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureDayLocked(now)
	c.pruneLocked(now)

	totalSignals := c.makerSignalCountDaily + c.takerSignalCountDaily
	makerSpreadCaptureBps := 0.0
	if c.makerSpreadCaptureSamplesDaily > 0 {
		makerSpreadCaptureBps = c.makerSpreadCaptureBpsSumDaily / float64(c.makerSpreadCaptureSamplesDaily)
	}
	takerSignalRealizationRate := 0.0
	if c.takerRealizationEvaluatedDaily > 0 {
		takerSignalRealizationRate = float64(c.takerRealizationCorrectDaily) / float64(c.takerRealizationEvaluatedDaily)
	}
	emergencyDuration := c.emergencyStopActiveDurationDaily
	if c.emergencyStopActive {
		activeSince := c.emergencyStopActiveSinceUTC
		if activeSince.Before(c.dayStartUTC) {
			activeSince = c.dayStartUTC
		}
		if now.After(activeSince) {
			emergencyDuration += now.Sub(activeSince)
		}
	}

	riskSamplesTotal := len(c.riskComplianceSamples)
	riskSamplesTradable := 0
	for _, sample := range c.riskComplianceSamples {
		if sample.canTrade {
			riskSamplesTradable++
		}
	}
	riskCompliance30d := 0.0
	if riskSamplesTotal > 0 {
		riskCompliance30d = float64(riskSamplesTradable) / float64(riskSamplesTotal)
	}

	netPnL30dRealized := 0.0
	netPnL30dTotal := 0.0
	netPnL30dAfterFees := 0.0
	windowDays := 0
	if len(c.pnlSamples) > 0 {
		latest := c.pnlSamples[len(c.pnlSamples)-1]
		base := c.pnlSamples[0]
		netPnL30dRealized = latest.realized - base.realized
		netPnL30dTotal = latest.total - base.total
		netPnL30dAfterFees = latest.net - base.net
		windowStart := base.at
		cutoff := now.Add(-kpiWindow30d)
		if windowStart.Before(cutoff) {
			windowStart = cutoff
		}
		if latest.at.After(windowStart) {
			windowDays = int(math.Ceil(latest.at.Sub(windowStart).Hours() / 24))
		}
		if windowDays <= 0 {
			windowDays = 1
		}
	}
	c.netPnL30dWindowEffectiveDaysCached = windowDays

	dailyNet := 0.0
	dailyRealized := 0.0
	dailyTotal := 0.0
	if c.dailyBaselineSet {
		dailyNet = c.currentNetPnLAfterFees - c.dailyBaselineNetPnLAfterFees
		dailyRealized = c.currentRealizedPnL - c.dailyBaselineRealizedPnL
		dailyTotal = c.currentTotalPnL - c.dailyBaselineTotalPnL
	}

	byReason := make(map[string]interface{}, len(c.riskBlockEventsDailyByReason))
	for reason, count := range c.riskBlockEventsDailyByReason {
		byReason[reason] = count
	}

	var emergencyActiveSince interface{}
	if c.emergencyStopActive && !c.emergencyStopActiveSinceUTC.IsZero() {
		emergencyActiveSince = c.emergencyStopActiveSinceUTC.UTC().Format(time.RFC3339)
	}

	return map[string]interface{}{
		"signal_count_daily":                      totalSignals,
		"maker_signal_count_daily":                c.makerSignalCountDaily,
		"taker_signal_count_daily":                c.takerSignalCountDaily,
		"submitted_orders_daily":                  c.submittedOrdersDaily,
		"filled_orders_daily":                     c.filledOrdersDaily,
		"risk_block_events_daily":                 c.riskBlockEventsDaily,
		"risk_block_events_daily_by_reason":       byReason,
		"risk_block_last_reason":                  c.riskBlockLastReason,
		"cooldown_trigger_count_daily":            c.cooldownTriggerCountDaily,
		"emergency_stop_active_duration_s_daily":  round6(emergencyDuration.Seconds()),
		"emergency_stop_is_active":                c.emergencyStopActive,
		"emergency_stop_active_started_at_utc":    emergencyActiveSince,
		"maker_spread_capture_bps":                round6(makerSpreadCaptureBps),
		"maker_spread_capture_samples_daily":      c.makerSpreadCaptureSamplesDaily,
		"taker_signal_realization_rate":           round6(takerSignalRealizationRate),
		"taker_signal_realization_window_minutes": c.takerRealizationWindowMinutes,
		"risk_compliance_30d":                     round6(clampFloat(riskCompliance30d, 0, 1)),
		"risk_compliance_samples_30d":             riskSamplesTotal,
		"risk_compliance_tradable_samples_30d":    riskSamplesTradable,
		"net_pnl_30d_realized_usdc":               round6(netPnL30dRealized),
		"net_pnl_30d_total_usdc":                  round6(netPnL30dTotal),
		"net_pnl_30d_after_fees_usdc":             round6(netPnL30dAfterFees),
		"net_pnl_30d_window_effective_days":       windowDays,
		"net_pnl_daily_realized_usdc":             round6(dailyRealized),
		"net_pnl_daily_total_usdc":                round6(dailyTotal),
		"net_pnl_daily_usdc":                      round6(dailyNet),
		"last_updated_at_utc":                     now.UTC().Format(time.RFC3339),
	}
}
