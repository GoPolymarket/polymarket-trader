package api

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/GoPolymarket/polymarket-trader/internal/execution"
	"github.com/GoPolymarket/polymarket-trader/internal/paper"
	"github.com/GoPolymarket/polymarket-trader/internal/risk"
)

const builderStaleAfter = 30 * time.Minute

// AppState exposes the trading app's state for the API layer.
type AppState interface {
	Stats() (orders int, fills int, pnl float64)
	IsRunning() bool
	IsDryRun() bool
	MonitoredAssets() []string
	SetEmergencyStop(stop bool)
	RecentFills(limit int) []execution.Fill
	ActiveOrders() []execution.OrderState
	TrackedPositions() map[string]execution.Position
	UnrealizedPnL() float64
	RiskSnapshot() risk.Snapshot
	TradingMode() string
	PaperSnapshot() paper.Snapshot
}

// PortfolioProvider exposes portfolio data (nil if unavailable).
type PortfolioProvider interface {
	TotalValue() float64
	LastSync() time.Time
}

// BuilderProvider exposes builder volume data (nil if unavailable).
type BuilderProvider interface {
	DailyVolumeJSON() interface{}
	LeaderboardJSON() interface{}
	LastSync() time.Time
}

// Server is a lightweight HTTP API for the trading dashboard.
type Server struct {
	httpServer *http.Server
	appState   AppState
	portfolio  PortfolioProvider
	builder    BuilderProvider
	startedAt  time.Time
}

// NewServer creates a new API server bound to addr.
func NewServer(addr string, appState AppState, portfolio PortfolioProvider, builder BuilderProvider) *Server {
	s := &Server{
		appState:  appState,
		portfolio: portfolio,
		builder:   builder,
		startedAt: time.Now(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/ready", s.handleReady)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/positions", s.handlePositions)
	mux.HandleFunc("/api/pnl", s.handlePnL)
	mux.HandleFunc("/api/perf", s.handlePerf)
	mux.HandleFunc("/api/coach", s.handleCoach)
	mux.HandleFunc("/api/sizing", s.handleSizing)
	mux.HandleFunc("/api/insights", s.handleInsights)
	mux.HandleFunc("/api/execution-quality", s.handleExecutionQuality)
	mux.HandleFunc("/api/daily-report", s.handleDailyReport)
	mux.HandleFunc("/api/stage-report", s.handleStageReport)
	mux.HandleFunc("/api/grant-package", s.handleGrantPackage)
	mux.HandleFunc("/api/grant-report", s.handleGrantReport)
	mux.HandleFunc("/api/trades", s.handleTrades)
	mux.HandleFunc("/api/orders", s.handleOrders)
	mux.HandleFunc("/api/markets", s.handleMarkets)
	mux.HandleFunc("/api/builder", s.handleBuilder)
	mux.HandleFunc("/api/risk", s.handleRisk)
	mux.HandleFunc("/api/paper", s.handlePaper)
	mux.HandleFunc("/api/emergency-stop", s.handleEmergencyStop)

	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Start begins serving HTTP requests.
func (s *Server) Start(_ context.Context) error {
	ln, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}
	log.Printf("api server listening on %s", s.httpServer.Addr)
	go func() {
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("api server: %v", err)
		}
	}()
	return nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// GET /api/health — liveness probe.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, map[string]interface{}{
		"ok":       true,
		"uptime_s": time.Since(s.startedAt).Seconds(),
	})
}

// GET /api/ready — readiness probe.
func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	ready := s.appState.IsRunning()
	resp := map[string]interface{}{
		"ready":        ready,
		"trading_mode": s.appState.TradingMode(),
		"uptime_s":     time.Since(s.startedAt).Seconds(),
	}
	if !ready {
		resp["reason"] = "app_not_running"
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	s.writeJSON(w, resp)
}

// GET /api/status — overall system status.
func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	orders, fills, pnl := s.appState.Stats()
	resp := map[string]interface{}{
		"running":      s.appState.IsRunning(),
		"dry_run":      s.appState.IsDryRun(),
		"trading_mode": s.appState.TradingMode(),
		"uptime_s":     time.Since(s.startedAt).Seconds(),
		"orders":       orders,
		"fills":        fills,
		"pnl":          pnl,
		"assets":       s.appState.MonitoredAssets(),
	}
	if s.portfolio != nil {
		resp["portfolio_value"] = s.portfolio.TotalValue()
		resp["portfolio_sync"] = s.portfolio.LastSync()
	}
	s.writeJSON(w, resp)
}

// GET /api/positions — current tracked positions.
func (s *Server) handlePositions(w http.ResponseWriter, _ *http.Request) {
	positions := s.appState.TrackedPositions()
	type positionEntry struct {
		AssetID       string  `json:"asset_id"`
		NetSize       float64 `json:"net_size"`
		AvgEntryPrice float64 `json:"avg_entry_price"`
		RealizedPnL   float64 `json:"realized_pnl"`
		TotalFills    int     `json:"total_fills"`
	}
	var entries []positionEntry
	for id, p := range positions {
		if p.NetSize == 0 && p.RealizedPnL == 0 {
			continue
		}
		entries = append(entries, positionEntry{
			AssetID:       id,
			NetSize:       p.NetSize,
			AvgEntryPrice: p.AvgEntryPrice,
			RealizedPnL:   p.RealizedPnL,
			TotalFills:    p.TotalFills,
		})
	}
	s.writeJSON(w, map[string]interface{}{"positions": entries})
}

// GET /api/pnl — realized + unrealized PnL.
func (s *Server) handlePnL(w http.ResponseWriter, _ *http.Request) {
	_, _, realized := s.appState.Stats()
	unrealized := s.appState.UnrealizedPnL()
	resp := map[string]interface{}{
		"realized_pnl":   realized,
		"unrealized_pnl": unrealized,
		"total_pnl":      realized + unrealized,
	}
	if s.portfolio != nil {
		resp["portfolio_value"] = s.portfolio.TotalValue()
	}
	s.writeJSON(w, resp)
}

// GET /api/perf — high-level performance metrics.
func (s *Server) handlePerf(w http.ResponseWriter, _ *http.Request) {
	orders, fills, realized := s.appState.Stats()
	unrealized := s.appState.UnrealizedPnL()
	total := realized + unrealized
	pnlPerFill := 0.0
	if fills > 0 {
		pnlPerFill = total / float64(fills)
	}

	mode := s.appState.TradingMode()
	paperSnap := s.appState.PaperSnapshot()
	fees := 0.0
	var estimatedEquity interface{}
	if mode == "paper" {
		fees = paperSnap.FeesPaidUSDC
		estimatedEquity = paperSnap.InitialBalanceUSDC + total - fees
	}

	s.writeJSON(w, map[string]interface{}{
		"trading_mode":            mode,
		"orders":                  orders,
		"fills":                   fills,
		"realized_pnl_usdc":       realized,
		"unrealized_pnl_usdc":     unrealized,
		"total_pnl_usdc":          total,
		"pnl_per_fill_usdc":       pnlPerFill,
		"fees_paid_usdc":          fees,
		"net_pnl_after_fees_usdc": total - fees,
		"estimated_equity_usdc":   estimatedEquity,
	})
}

// GET /api/grant-report — aggregated metrics for builder/grant review.
func (s *Server) handleGrantReport(w http.ResponseWriter, r *http.Request) {
	generatedAt := time.Now().UTC()
	_, fills, realized := s.appState.Stats()
	unrealized := s.appState.UnrealizedPnL()
	total := realized + unrealized
	mode := s.appState.TradingMode()
	paperSnap := s.appState.PaperSnapshot()
	fees := 0.0
	var estimatedEquity interface{}
	if mode == "paper" {
		fees = paperSnap.FeesPaidUSDC
		estimatedEquity = paperSnap.InitialBalanceUSDC + total - fees
	}

	builderConfigured := false
	builderDailyVolumeCount := 0
	builderLeaderboardCount := 0
	var builderLastSync interface{}
	var builderLastSyncAgeSeconds interface{}
	builderNeverSynced := true
	builderStale := false
	builderData := map[string]interface{}{
		"configured":         builderConfigured,
		"daily_volume_count": builderDailyVolumeCount,
		"leaderboard_count":  builderLeaderboardCount,
		"last_sync":          builderLastSync,
		"last_sync_age_s":    builderLastSyncAgeSeconds,
		"never_synced":       builderNeverSynced,
		"stale":              builderStale,
		"stale_after_s":      builderStaleAfter.Seconds(),
	}
	if s.builder != nil {
		dailyVolume := s.builder.DailyVolumeJSON()
		leaderboard := s.builder.LeaderboardJSON()
		lastSync := s.builder.LastSync()
		builderConfigured = true
		builderDailyVolumeCount = countEntries(dailyVolume)
		builderLeaderboardCount = countEntries(leaderboard)
		builderLastSync = lastSync
		builderNeverSynced = lastSync.IsZero()
		builderStale = builderNeverSynced
		builderLastSyncAgeSeconds = nil
		if !builderNeverSynced {
			age := time.Since(lastSync)
			if age < 0 {
				age = 0
			}
			builderLastSyncAgeSeconds = age.Seconds()
			builderStale = age > builderStaleAfter
		}
		builderData = map[string]interface{}{
			"configured":         builderConfigured,
			"daily_volume_count": builderDailyVolumeCount,
			"leaderboard_count":  builderLeaderboardCount,
			"last_sync":          builderLastSync,
			"last_sync_age_s":    builderLastSyncAgeSeconds,
			"never_synced":       builderNeverSynced,
			"stale":              builderStale,
			"stale_after_s":      builderStaleAfter.Seconds(),
		}
	}

	snap := s.appState.RiskSnapshot()
	rs := buildRiskStatus(snap)
	canTrade := rs.canTrade
	builderFresh := builderConfigured && !builderNeverSynced && !builderStale
	hasTradingActivity := fills > 0
	readinessScore := 0
	if builderFresh {
		readinessScore += 40
	}
	if canTrade {
		readinessScore += 30
	}
	if hasTradingActivity {
		readinessScore += 30
	}

	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("format")), "csv") {
		s.writeGrantReportCSV(w, generatedAt, mode, fills, realized, unrealized, total, fees, estimatedEquity, builderConfigured, builderDailyVolumeCount, builderLeaderboardCount, builderLastSyncAgeSeconds, builderNeverSynced, builderStale, canTrade, snap.DailyPnL, rs.usagePct, snap.ConsecutiveLosses, snap.InCooldown, rs.blockedReasons, builderFresh, hasTradingActivity, readinessScore)
		return
	}

	s.writeJSON(w, map[string]interface{}{
		"generated_at": generatedAt,
		"trading_mode": mode,
		"performance": map[string]interface{}{
			"fills":                   fills,
			"realized_pnl_usdc":       realized,
			"unrealized_pnl_usdc":     unrealized,
			"total_pnl_usdc":          total,
			"fees_paid_usdc":          fees,
			"net_pnl_after_fees_usdc": total - fees,
			"estimated_equity_usdc":   estimatedEquity,
		},
		"builder": builderData,
		"risk": map[string]interface{}{
			"emergency_stop":            snap.EmergencyStop,
			"daily_pnl":                 snap.DailyPnL,
			"daily_loss_limit_usdc":     snap.DailyLossLimitUSDC,
			"daily_loss_used_pct":       rs.usagePct,
			"daily_loss_remaining_usdc": rs.remainingUSDC,
			"daily_loss_remaining_pct":  rs.remainingPct,
			"can_trade":                 canTrade,
			"blocked_reasons":           rs.blockedReasons,
			"consecutive_losses":        snap.ConsecutiveLosses,
			"max_consecutive_losses":    snap.MaxConsecutiveLosses,
			"in_cooldown":               snap.InCooldown,
			"cooldown_remaining_s":      snap.CooldownRemaining.Seconds(),
		},
		"readiness": map[string]interface{}{
			"builder_fresh":        builderFresh,
			"risk_tradable":        canTrade,
			"has_trading_activity": hasTradingActivity,
			"score":                readinessScore,
		},
	})
}

func (s *Server) writeGrantReportCSV(
	w http.ResponseWriter,
	generatedAt time.Time,
	mode string,
	fills int,
	realized float64,
	unrealized float64,
	total float64,
	fees float64,
	estimatedEquity interface{},
	builderConfigured bool,
	builderDailyVolumeCount int,
	builderLeaderboardCount int,
	builderLastSyncAgeSeconds interface{},
	builderNeverSynced bool,
	builderStale bool,
	canTrade bool,
	dailyPnL float64,
	dailyLossUsedPct float64,
	consecutiveLosses int,
	inCooldown bool,
	blockedReasons []string,
	builderFresh bool,
	hasTradingActivity bool,
	readinessScore int,
) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	cw := csv.NewWriter(w)
	header := []string{
		"generated_at",
		"trading_mode",
		"fills",
		"realized_pnl_usdc",
		"unrealized_pnl_usdc",
		"total_pnl_usdc",
		"fees_paid_usdc",
		"net_pnl_after_fees_usdc",
		"estimated_equity_usdc",
		"builder_configured",
		"builder_daily_volume_count",
		"builder_leaderboard_count",
		"builder_last_sync_age_s",
		"builder_never_synced",
		"builder_stale",
		"risk_can_trade",
		"risk_daily_pnl",
		"risk_daily_loss_used_pct",
		"risk_consecutive_losses",
		"risk_in_cooldown",
		"risk_blocked_reasons",
		"readiness_builder_fresh",
		"readiness_risk_tradable",
		"readiness_has_trading_activity",
		"readiness_score",
	}
	record := []string{
		generatedAt.Format(time.RFC3339),
		mode,
		strconv.Itoa(fills),
		fmt.Sprintf("%.6f", realized),
		fmt.Sprintf("%.6f", unrealized),
		fmt.Sprintf("%.6f", total),
		fmt.Sprintf("%.6f", fees),
		fmt.Sprintf("%.6f", total-fees),
		formatCSVNumber(estimatedEquity),
		strconv.FormatBool(builderConfigured),
		strconv.Itoa(builderDailyVolumeCount),
		strconv.Itoa(builderLeaderboardCount),
		formatCSVNumber(builderLastSyncAgeSeconds),
		strconv.FormatBool(builderNeverSynced),
		strconv.FormatBool(builderStale),
		strconv.FormatBool(canTrade),
		fmt.Sprintf("%.6f", dailyPnL),
		fmt.Sprintf("%.6f", dailyLossUsedPct),
		strconv.Itoa(consecutiveLosses),
		strconv.FormatBool(inCooldown),
		strings.Join(blockedReasons, ";"),
		strconv.FormatBool(builderFresh),
		strconv.FormatBool(canTrade),
		strconv.FormatBool(hasTradingActivity),
		strconv.Itoa(readinessScore),
	}
	if err := cw.Write(header); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := cw.Write(record); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func formatCSVNumber(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case float64:
		return fmt.Sprintf("%.6f", t)
	case float32:
		return fmt.Sprintf("%.6f", t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// GET /api/trades?limit=50 — recent trade fills.
func (s *Server) handleTrades(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	fills := s.appState.RecentFills(limit)
	type tradeEntry struct {
		TradeID   string    `json:"trade_id"`
		AssetID   string    `json:"asset_id"`
		Side      string    `json:"side"`
		Price     float64   `json:"price"`
		Size      float64   `json:"size"`
		Timestamp time.Time `json:"timestamp"`
	}
	entries := make([]tradeEntry, len(fills))
	for i, f := range fills {
		entries[i] = tradeEntry{
			TradeID:   f.TradeID,
			AssetID:   f.AssetID,
			Side:      f.Side,
			Price:     f.Price,
			Size:      f.Size,
			Timestamp: f.Timestamp,
		}
	}
	s.writeJSON(w, map[string]interface{}{"trades": entries, "count": len(entries)})
}

// GET /api/orders — active (LIVE) orders.
func (s *Server) handleOrders(w http.ResponseWriter, _ *http.Request) {
	orders := s.appState.ActiveOrders()
	type orderEntry struct {
		ID         string    `json:"id"`
		AssetID    string    `json:"asset_id"`
		Market     string    `json:"market"`
		Side       string    `json:"side"`
		Price      float64   `json:"price"`
		OrigSize   float64   `json:"orig_size"`
		FilledSize float64   `json:"filled_size"`
		CreatedAt  time.Time `json:"created_at"`
	}
	entries := make([]orderEntry, len(orders))
	for i, o := range orders {
		entries[i] = orderEntry{
			ID:         o.ID,
			AssetID:    o.AssetID,
			Market:     o.Market,
			Side:       o.Side,
			Price:      o.Price,
			OrigSize:   o.OrigSize,
			FilledSize: o.FilledSize,
			CreatedAt:  o.CreatedAt,
		}
	}
	s.writeJSON(w, map[string]interface{}{"orders": entries, "count": len(entries)})
}

// GET /api/markets — monitored markets.
func (s *Server) handleMarkets(w http.ResponseWriter, _ *http.Request) {
	assets := s.appState.MonitoredAssets()
	s.writeJSON(w, map[string]interface{}{"assets": assets, "count": len(assets)})
}

// GET /api/builder — builder volume and leaderboard data.
func (s *Server) handleBuilder(w http.ResponseWriter, _ *http.Request) {
	if s.builder == nil {
		s.writeJSON(w, map[string]interface{}{
			"status":             "not_configured",
			"configured":         false,
			"daily_volume_count": 0,
			"leaderboard_count":  0,
			"last_sync_age_s":    nil,
			"never_synced":       true,
			"stale":              false,
			"stale_after_s":      builderStaleAfter.Seconds(),
		})
		return
	}
	dailyVolume := s.builder.DailyVolumeJSON()
	leaderboard := s.builder.LeaderboardJSON()
	lastSync := s.builder.LastSync()
	neverSynced := lastSync.IsZero()
	var lastSyncAgeS interface{}
	stale := neverSynced
	if !neverSynced {
		age := time.Since(lastSync)
		if age < 0 {
			age = 0
		}
		lastSyncAgeS = age.Seconds()
		stale = age > builderStaleAfter
	}
	s.writeJSON(w, map[string]interface{}{
		"configured":         true,
		"daily_volume":       dailyVolume,
		"daily_volume_count": countEntries(dailyVolume),
		"leaderboard":        leaderboard,
		"leaderboard_count":  countEntries(leaderboard),
		"last_sync":          lastSync,
		"last_sync_age_s":    lastSyncAgeS,
		"never_synced":       neverSynced,
		"stale":              stale,
		"stale_after_s":      builderStaleAfter.Seconds(),
	})
}

func countEntries(v interface{}) int {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return 0
	}
	switch rv.Kind() {
	case reflect.Array, reflect.Slice, reflect.Map, reflect.String:
		return rv.Len()
	default:
		return 0
	}
}

type riskStatus struct {
	usagePct       float64
	remainingUSDC  float64
	remainingPct   float64
	blockedReasons []string
	canTrade       bool
}

type builderStatus struct {
	configured         bool
	dailyVolumeCount   int
	leaderboardCount   int
	lastSyncAgeSeconds interface{}
	neverSynced        bool
	stale              bool
	fresh              bool
}

func (s *Server) currentBuilderStatus() builderStatus {
	if s.builder == nil {
		return builderStatus{
			configured:         false,
			dailyVolumeCount:   0,
			leaderboardCount:   0,
			lastSyncAgeSeconds: nil,
			neverSynced:        true,
			stale:              false,
			fresh:              false,
		}
	}

	lastSync := s.builder.LastSync()
	neverSynced := lastSync.IsZero()
	stale := neverSynced
	var lastSyncAgeSeconds interface{}
	if !neverSynced {
		age := time.Since(lastSync)
		if age < 0 {
			age = 0
		}
		lastSyncAgeSeconds = age.Seconds()
		stale = age > builderStaleAfter
	}
	fresh := !neverSynced && !stale
	return builderStatus{
		configured:         true,
		dailyVolumeCount:   countEntries(s.builder.DailyVolumeJSON()),
		leaderboardCount:   countEntries(s.builder.LeaderboardJSON()),
		lastSyncAgeSeconds: lastSyncAgeSeconds,
		neverSynced:        neverSynced,
		stale:              stale,
		fresh:              fresh,
	}
}

func buildRiskStatus(snap risk.Snapshot) riskStatus {
	st := riskStatus{
		usagePct:       0,
		remainingUSDC:  0,
		remainingPct:   0,
		blockedReasons: make([]string, 0, 3),
	}
	if snap.EmergencyStop {
		st.blockedReasons = append(st.blockedReasons, "emergency_stop")
	}
	if snap.DailyLossLimitUSDC > 0 {
		st.usagePct = (-snap.DailyPnL / snap.DailyLossLimitUSDC) * 100
		if st.usagePct < 0 {
			st.usagePct = 0
		}
		st.remainingUSDC = snap.DailyLossLimitUSDC + snap.DailyPnL
		if st.remainingUSDC < 0 {
			st.remainingUSDC = 0
		}
		st.remainingPct = 100 - st.usagePct
		if st.remainingPct < 0 {
			st.remainingPct = 0
		}
		if snap.DailyPnL <= -snap.DailyLossLimitUSDC {
			st.blockedReasons = append(st.blockedReasons, "daily_loss_limit_reached")
		}
	}
	if snap.InCooldown {
		st.blockedReasons = append(st.blockedReasons, "loss_cooldown_active")
	}
	st.canTrade = len(st.blockedReasons) == 0
	return st
}

type coachAction struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type marketSnapshot struct {
	assetID string
	pnlUSDC float64
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func averageFillNotional(fills []execution.Fill) float64 {
	if len(fills) == 0 {
		return 0
	}
	total := 0.0
	for _, f := range fills {
		total += f.Price * f.Size
	}
	return total / float64(len(fills))
}

func chooseSizingMode(canTrade bool, usagePct, totalPnL float64, consecutiveLosses, maxConsecutiveLosses int) (string, float64) {
	if !canTrade {
		return "pause", 0
	}
	nearLossStreak := maxConsecutiveLosses > 1 && consecutiveLosses >= maxConsecutiveLosses-1
	if usagePct >= 80 || totalPnL < 0 || nearLossStreak {
		return "defensive", 0.5
	}
	return "normal", 1.0
}

func buildCoachActions(
	canTrade bool,
	blockedReasons []string,
	inCooldown bool,
	cooldownRemaining time.Duration,
	usagePct float64,
	fills int,
	pnlPerFill float64,
	profitableMarkets int,
	best marketSnapshot,
) []coachAction {
	actions := make([]coachAction, 0, 6)
	if !canTrade {
		actions = append(actions, coachAction{
			Code:     "pause_trading",
			Severity: "critical",
			Message:  fmt.Sprintf("Trading blocked by risk rules: %s", strings.Join(blockedReasons, ",")),
		})
	}
	if inCooldown {
		actions = append(actions, coachAction{
			Code:     "wait_cooldown",
			Severity: "warn",
			Message:  fmt.Sprintf("Wait %.0fs before resuming new risk.", cooldownRemaining.Seconds()),
		})
	}
	if usagePct >= 80 {
		actions = append(actions, coachAction{
			Code:     "reduce_size",
			Severity: "warn",
			Message:  "Daily loss budget usage is high; cut per-order size by 50%.",
		})
	}
	if fills < 20 {
		actions = append(actions, coachAction{
			Code:     "increase_sample_size",
			Severity: "info",
			Message:  "Collect at least 20 fills before increasing size.",
		})
	}
	if fills >= 10 && pnlPerFill <= 0 {
		actions = append(actions, coachAction{
			Code:     "fix_edge_before_scaling",
			Severity: "warn",
			Message:  "Current PnL per fill is non-positive; improve edge before scaling.",
		})
	}
	if profitableMarkets > 0 && best.assetID != "" {
		actions = append(actions, coachAction{
			Code:     "focus_profitable_markets",
			Severity: "info",
			Message:  fmt.Sprintf("Allocate attention to %s where realized PnL is strongest.", best.assetID),
		})
	}
	if len(actions) == 0 {
		actions = append(actions, coachAction{
			Code:     "maintain_plan",
			Severity: "info",
			Message:  "Risk and performance are healthy; keep current sizing discipline.",
		})
	}
	return actions
}

type marketScore struct {
	AssetID         string  `json:"asset_id"`
	RealizedPnLUSDC float64 `json:"realized_pnl_usdc"`
	Fills           int     `json:"fills"`
	PnLPerFillUSDC  float64 `json:"pnl_per_fill_usdc"`
	FillSharePct    float64 `json:"fill_share_pct"`
	Score           float64 `json:"score"`
	Bucket          string  `json:"bucket"`
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func marketScoreValue(realizedPnL float64, fills int, fillSharePct float64) float64 {
	score := 50.0
	if fills > 0 {
		score += clamp((realizedPnL/float64(fills))*40, -30, 30)
	}
	score += clamp(realizedPnL*6, -20, 20)
	if fills >= 10 {
		score += 10
	} else if fills < 3 {
		score -= 5
	}
	if fillSharePct >= 50 && realizedPnL < 0 {
		score -= 15
	}
	return clamp(score, 0, 100)
}

func marketBucket(score float64) string {
	if score >= 70 {
		return "focus"
	}
	if score <= 40 {
		return "deprioritize"
	}
	return "monitor"
}

func buildMarketScores(positions map[string]execution.Position) []marketScore {
	totalFills := 0
	for _, pos := range positions {
		if pos.TotalFills > 0 {
			totalFills += pos.TotalFills
		}
	}

	scores := make([]marketScore, 0, len(positions))
	for assetID, pos := range positions {
		if pos.TotalFills <= 0 {
			continue
		}
		fillSharePct := 0.0
		if totalFills > 0 {
			fillSharePct = float64(pos.TotalFills) / float64(totalFills) * 100
		}
		pnlPerFill := pos.RealizedPnL / float64(pos.TotalFills)
		score := marketScoreValue(pos.RealizedPnL, pos.TotalFills, fillSharePct)
		scores = append(scores, marketScore{
			AssetID:         assetID,
			RealizedPnLUSDC: pos.RealizedPnL,
			Fills:           pos.TotalFills,
			PnLPerFillUSDC:  round2(pnlPerFill),
			FillSharePct:    round2(fillSharePct),
			Score:           round2(score),
			Bucket:          marketBucket(score),
		})
	}

	sort.Slice(scores, func(i, j int) bool {
		if scores[i].Score == scores[j].Score {
			return scores[i].Fills > scores[j].Fills
		}
		return scores[i].Score > scores[j].Score
	})
	return scores
}

func buildInsightRecommendations(
	canTrade bool,
	blockedReasons []string,
	fills int,
	pnlPerFill float64,
	scores []marketScore,
) []coachAction {
	recs := make([]coachAction, 0, 6)
	if !canTrade {
		recs = append(recs, coachAction{
			Code:     "pause_trading",
			Severity: "critical",
			Message:  fmt.Sprintf("Trading blocked by risk rules: %s", strings.Join(blockedReasons, ",")),
		})
	}
	if len(scores) == 0 {
		recs = append(recs, coachAction{
			Code:     "collect_more_data",
			Severity: "info",
			Message:  "No market-level sample yet; collect fills before ranking markets.",
		})
		return recs
	}

	top := scores[0]
	recs = append(recs, coachAction{
		Code:     "focus_top_market",
		Severity: "info",
		Message:  fmt.Sprintf("Focus on %s (score %.2f) until edge decays.", top.AssetID, top.Score),
	})

	worst := scores[len(scores)-1]
	if worst.Bucket == "deprioritize" {
		recs = append(recs, coachAction{
			Code:     "deprioritize_worst_market",
			Severity: "warn",
			Message:  fmt.Sprintf("Reduce exposure on %s (score %.2f).", worst.AssetID, worst.Score),
		})
	}
	if fills < 20 {
		recs = append(recs, coachAction{
			Code:     "increase_sample_size",
			Severity: "info",
			Message:  "Run at least 20 fills before scaling market concentration.",
		})
	}
	if fills >= 10 && pnlPerFill <= 0 {
		recs = append(recs, coachAction{
			Code:     "improve_edge_before_scaling",
			Severity: "warn",
			Message:  "PnL/fill is non-positive; improve signal quality before increasing size.",
		})
	}
	return recs
}

type executionQualityMetrics struct {
	Fills               int     `json:"fills"`
	TradesConsidered    int     `json:"trades_considered"`
	VolumeUSDC          float64 `json:"volume_usdc"`
	AvgFillNotionalUSDC float64 `json:"avg_fill_notional_usdc"`
	GrossPnLUSDC        float64 `json:"gross_pnl_usdc"`
	FeesPaidUSDC        float64 `json:"fees_paid_usdc"`
	NetPnLAfterFeesUSDC float64 `json:"net_pnl_after_fees_usdc"`
	PnLPerFillUSDC      float64 `json:"pnl_per_fill_usdc"`
	FeePerTradeUSDC     float64 `json:"fee_per_trade_usdc"`
	GrossEdgeBps        float64 `json:"gross_edge_bps"`
	NetEdgeBps          float64 `json:"net_edge_bps"`
	FeeRateBps          float64 `json:"fee_rate_bps"`
	FrictionBps         float64 `json:"friction_bps"`
	QualityScore        float64 `json:"quality_score"`
}

type executionLossBreakdown struct {
	FeeDragBps          float64 `json:"fee_drag_bps"`
	SlippageProxyBps    float64 `json:"slippage_proxy_bps"`
	SelectivityLossBps  float64 `json:"selectivity_loss_bps"`
	AvoidableLossBps    float64 `json:"avoidable_loss_bps"`
	TotalLossBps        float64 `json:"total_loss_bps"`
	FeeSharePct         float64 `json:"fee_share_pct"`
	SlippageSharePct    float64 `json:"slippage_share_pct"`
	SelectivitySharePct float64 `json:"selectivity_share_pct"`
}

func sumFillNotional(fills []execution.Fill) float64 {
	total := 0.0
	for _, f := range fills {
		total += f.Price * f.Size
	}
	return total
}

func calculateExecutionQualityMetrics(
	mode string,
	fills int,
	totalPnL float64,
	paperSnap paper.Snapshot,
	recentFills []execution.Fill,
) executionQualityMetrics {
	fees := 0.0
	volume := 0.0
	trades := fills
	if mode == "paper" {
		fees = paperSnap.FeesPaidUSDC
		volume = paperSnap.TotalVolumeUSDC
		if paperSnap.TotalTrades > 0 {
			trades = paperSnap.TotalTrades
		}
	}

	fallbackVolume := sumFillNotional(recentFills)
	if volume <= 0 {
		volume = fallbackVolume
	}

	pnlPerFill := 0.0
	if fills > 0 {
		pnlPerFill = totalPnL / float64(fills)
	}
	feePerTrade := 0.0
	if trades > 0 {
		feePerTrade = fees / float64(trades)
	}
	netPnL := totalPnL - fees
	grossEdgeBps := 0.0
	netEdgeBps := 0.0
	feeRateBps := 0.0
	if volume > 0 {
		grossEdgeBps = totalPnL / volume * 10000
		netEdgeBps = netPnL / volume * 10000
		feeRateBps = fees / volume * 10000
	}

	score := 50.0
	switch {
	case netEdgeBps > 10:
		score += 20
	case netEdgeBps > 0:
		score += 10
	case netEdgeBps < 0:
		score -= 15
	}
	if fills >= 10 {
		if pnlPerFill > 0 {
			score += 10
		} else {
			score -= 10
		}
	}
	if feeRateBps >= 30 {
		score -= 10
	} else if feeRateBps > 0 && feeRateBps <= 15 {
		score += 10
	}

	return executionQualityMetrics{
		Fills:               fills,
		TradesConsidered:    trades,
		VolumeUSDC:          round2(volume),
		AvgFillNotionalUSDC: round2(averageFillNotional(recentFills)),
		GrossPnLUSDC:        round2(totalPnL),
		FeesPaidUSDC:        round2(fees),
		NetPnLAfterFeesUSDC: round2(netPnL),
		PnLPerFillUSDC:      round2(pnlPerFill),
		FeePerTradeUSDC:     round2(feePerTrade),
		GrossEdgeBps:        round2(grossEdgeBps),
		NetEdgeBps:          round2(netEdgeBps),
		FeeRateBps:          round2(feeRateBps),
		FrictionBps:         round2(grossEdgeBps - netEdgeBps),
		QualityScore:        round2(clamp(score, 0, 100)),
	}
}

func calculateExecutionLossBreakdown(metrics executionQualityMetrics) executionLossBreakdown {
	slippage := (100 - metrics.QualityScore) / 20
	switch {
	case metrics.AvgFillNotionalUSDC <= 0:
		slippage += 2
	case metrics.AvgFillNotionalUSDC < 10:
		slippage += 1
	}
	if metrics.Fills < 10 {
		slippage += 0.5
	}
	slippage = round2(clamp(slippage, 0, 12))

	selectivity := 0.0
	if metrics.NetEdgeBps < 0 {
		selectivity = round2(-metrics.NetEdgeBps)
	}

	feeDrag := round2(metrics.FeeRateBps)
	avoidable := round2(slippage + selectivity)
	total := round2(feeDrag + avoidable)
	feeShare := 0.0
	slippageShare := 0.0
	selectivityShare := 0.0
	if total > 0 {
		feeShare = round2((feeDrag / total) * 100)
		slippageShare = round2((slippage / total) * 100)
		selectivityShare = round2((selectivity / total) * 100)
	}

	return executionLossBreakdown{
		FeeDragBps:          feeDrag,
		SlippageProxyBps:    slippage,
		SelectivityLossBps:  selectivity,
		AvoidableLossBps:    avoidable,
		TotalLossBps:        total,
		FeeSharePct:         feeShare,
		SlippageSharePct:    slippageShare,
		SelectivitySharePct: selectivityShare,
	}
}

func buildExecutionQualityRecommendations(
	canTrade bool,
	blockedReasons []string,
	metrics executionQualityMetrics,
	breakdown executionLossBreakdown,
) []coachAction {
	recs := make([]coachAction, 0, 6)
	if !canTrade {
		recs = append(recs, coachAction{
			Code:     "pause_trading",
			Severity: "critical",
			Message:  fmt.Sprintf("Trading blocked by risk rules: %s", strings.Join(blockedReasons, ",")),
		})
	}
	if metrics.Fills < 20 {
		recs = append(recs, coachAction{
			Code:     "increase_sample_size",
			Severity: "info",
			Message:  "Collect at least 20 fills before increasing strategy throughput.",
		})
	}
	if metrics.FeeRateBps > 0 && metrics.GrossEdgeBps <= metrics.FeeRateBps {
		recs = append(recs, coachAction{
			Code:     "reduce_churn",
			Severity: "warn",
			Message:  "Execution costs consume gross edge; reduce turnover and be more selective.",
		})
	}
	if breakdown.FeeDragBps >= 20 {
		recs = append(recs, coachAction{
			Code:     "reduce_fee_drag",
			Severity: "warn",
			Message:  "Fee drag is high; prioritize lower-cost execution paths.",
		})
	}
	if breakdown.SlippageProxyBps >= 3 {
		recs = append(recs, coachAction{
			Code:     "reduce_slippage",
			Severity: "warn",
			Message:  "Slippage proxy is elevated; prefer tighter quotes and smaller clips.",
		})
	}
	if metrics.NetEdgeBps <= 0 && metrics.Fills >= 10 {
		recs = append(recs, coachAction{
			Code:     "improve_selectivity",
			Severity: "warn",
			Message:  "Net edge is non-positive; tighten entry filters before scaling size.",
		})
	}
	if metrics.NetEdgeBps > metrics.FeeRateBps && metrics.NetEdgeBps > 0 {
		recs = append(recs, coachAction{
			Code:     "edge_above_friction",
			Severity: "info",
			Message:  "Net edge exceeds execution friction; current execution quality is healthy.",
		})
	}
	if len(recs) == 0 {
		recs = append(recs, coachAction{
			Code:     "maintain_execution_discipline",
			Severity: "info",
			Message:  "Execution metrics look stable; keep discipline and monitor drift.",
		})
	}
	return recs
}

type sizingAllocation struct {
	AssetID   string  `json:"asset_id"`
	WeightPct float64 `json:"weight_pct"`
	Score     float64 `json:"score"`
	Bucket    string  `json:"bucket"`
}

func buildSizingAllocation(scores []marketScore, limit int) []sizingAllocation {
	if limit <= 0 || len(scores) == 0 {
		return nil
	}
	if len(scores) < limit {
		limit = len(scores)
	}

	top := scores[:limit]
	weightBase := 0.0
	for _, s := range top {
		weightBase += math.Max(1, s.Score)
	}
	if weightBase <= 0 {
		return nil
	}

	out := make([]sizingAllocation, 0, limit)
	for _, s := range top {
		weight := (math.Max(1, s.Score) / weightBase) * 100
		out = append(out, sizingAllocation{
			AssetID:   s.AssetID,
			WeightPct: round2(weight),
			Score:     s.Score,
			Bucket:    s.Bucket,
		})
	}
	return out
}

func calculateSizingBudget(
	rs riskStatus,
	riskMode string,
	sizeMultiplier float64,
	metrics executionQualityMetrics,
	recentFills []execution.Fill,
) (riskBudgetUSDC float64, perTradeUSDC float64, suggestedMaxOrderUSDC float64, recommendedTrades int) {
	if !rs.canTrade || riskMode == "pause" {
		return 0, 0, 0, 0
	}

	remaining := rs.remainingUSDC
	if remaining <= 0 {
		remaining = math.Max(10, averageFillNotional(recentFills)*5)
	}

	riskBudget := remaining * 0.20
	if riskMode == "defensive" {
		riskBudget *= 0.5
	}

	qualityAdj := 0.6 + metrics.QualityScore/250.0
	if metrics.NetEdgeBps <= 0 {
		qualityAdj *= 0.6
	} else if metrics.NetEdgeBps >= 20 {
		qualityAdj *= 1.1
	}
	riskBudget *= qualityAdj
	if riskBudget > remaining {
		riskBudget = remaining
	}
	if riskBudget < 0 {
		riskBudget = 0
	}

	trades := 5
	if riskMode == "defensive" {
		trades = 3
	}
	if metrics.Fills < 10 && trades > 3 {
		trades = 3
	}

	perTrade := 0.0
	if trades > 0 {
		perTrade = riskBudget / float64(trades)
	}
	avgNotional := averageFillNotional(recentFills)
	if avgNotional <= 0 {
		avgNotional = 5
	}
	orderCap := avgNotional * math.Max(0.8, 1.2*sizeMultiplier)
	suggested := perTrade
	if orderCap > 0 && suggested > orderCap {
		suggested = orderCap
	}
	if suggested > riskBudget {
		suggested = riskBudget
	}

	return riskBudget, perTrade, suggested, trades
}

func buildSizingActions(
	canTrade bool,
	blockedReasons []string,
	riskMode string,
	metrics executionQualityMetrics,
	allocation []sizingAllocation,
) []coachAction {
	actions := make([]coachAction, 0, 6)
	if !canTrade {
		actions = append(actions, coachAction{
			Code:     "pause_trading",
			Severity: "critical",
			Message:  fmt.Sprintf("Trading blocked by risk rules: %s", strings.Join(blockedReasons, ",")),
		})
	}
	if riskMode == "defensive" {
		actions = append(actions, coachAction{
			Code:     "reduce_size",
			Severity: "warn",
			Message:  "Run defensive size mode until risk usage and edge recover.",
		})
	}
	if metrics.NetEdgeBps <= 0 && metrics.Fills >= 10 {
		actions = append(actions, coachAction{
			Code:     "improve_selectivity",
			Severity: "warn",
			Message:  "Net edge is non-positive; tighten entry filters before scaling.",
		})
	}
	if len(allocation) > 0 {
		actions = append(actions, coachAction{
			Code:     "focus_high_score_markets",
			Severity: "info",
			Message:  "Concentrate size on top-scoring markets in the allocation plan.",
		})
	} else {
		actions = append(actions, coachAction{
			Code:     "collect_market_data",
			Severity: "info",
			Message:  "Market sample is insufficient; gather more fills before concentration.",
		})
	}
	if len(actions) == 0 {
		actions = append(actions, coachAction{
			Code:     "maintain_plan",
			Severity: "info",
			Message:  "Sizing inputs are healthy; keep current discipline.",
		})
	}
	return uniqueCoachActions(actions)
}

func pnlOutcome(netPnL float64) string {
	if netPnL > 0 {
		return "profit"
	}
	if netPnL < 0 {
		return "loss"
	}
	return "flat"
}

func uniqueCoachActions(actions []coachAction) []coachAction {
	if len(actions) == 0 {
		return actions
	}
	seen := make(map[string]struct{}, len(actions))
	out := make([]coachAction, 0, len(actions))
	for _, a := range actions {
		if _, ok := seen[a.Code]; ok {
			continue
		}
		seen[a.Code] = struct{}{}
		out = append(out, a)
	}
	return out
}

func topFocusAssets(scores []marketScore, limit int) []string {
	if limit <= 0 {
		return nil
	}
	out := make([]string, 0, limit)
	for _, s := range scores {
		if len(out) >= limit {
			break
		}
		if s.Bucket == "focus" || len(out) == 0 {
			out = append(out, s.AssetID)
		}
	}
	return out
}

func buildDailyReportActions(
	outcome string,
	riskMode string,
	rs riskStatus,
	metrics executionQualityMetrics,
	scores []marketScore,
) []coachAction {
	actions := make([]coachAction, 0, 10)
	breakdown := calculateExecutionLossBreakdown(metrics)
	actions = append(actions, buildExecutionQualityRecommendations(rs.canTrade, rs.blockedReasons, metrics, breakdown)...)

	if len(scores) > 0 {
		top := scores[0]
		actions = append(actions, coachAction{
			Code:     "focus_top_market",
			Severity: "info",
			Message:  fmt.Sprintf("Tomorrow focus market: %s (score %.2f).", top.AssetID, top.Score),
		})
		worst := scores[len(scores)-1]
		if worst.Bucket == "deprioritize" {
			actions = append(actions, coachAction{
				Code:     "deprioritize_worst_market",
				Severity: "warn",
				Message:  fmt.Sprintf("Tomorrow de-prioritize %s (score %.2f).", worst.AssetID, worst.Score),
			})
		}
	}

	if outcome == "loss" || riskMode != "normal" || rs.usagePct >= 80 {
		actions = append(actions, coachAction{
			Code:     "reduce_size_tomorrow",
			Severity: "warn",
			Message:  "Start next cycle with smaller size until edge stabilizes.",
		})
	}

	return uniqueCoachActions(actions)
}

func calcReadinessScore(builderFresh, canTrade, hasTradingActivity bool) int {
	score := 0
	if builderFresh {
		score += 40
	}
	if canTrade {
		score += 30
	}
	if hasTradingActivity {
		score += 30
	}
	return score
}

type stageWindow struct {
	label string
	days  int
}

type stageEvidence struct {
	ID                  string
	ChecksumSHA256      string
	ChecksumGeneratedAt time.Time
}

type grantArtifact struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Purpose  string `json:"purpose"`
	Required bool   `json:"required"`
}

type grantMilestone struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	EvidenceRef string `json:"evidence_ref"`
}

func parseStageWindow(raw string) stageWindow {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "30", "30d", "30day", "30days":
		return stageWindow{label: "30d", days: 30}
	case "7", "7d", "7day", "7days", "":
		return stageWindow{label: "7d", days: 7}
	default:
		return stageWindow{label: "7d", days: 7}
	}
}

func buildStageEvidence(
	generatedAt time.Time,
	mode string,
	window stageWindow,
	grantReadinessScore int,
	readinessScore int,
	qualityScore float64,
	fills int,
	totalPnL float64,
	netPnLAfterFees float64,
	netEdgeBps float64,
	feeRateBps float64,
	builderFresh bool,
	riskTradable bool,
	hasTradingActivity bool,
) stageEvidence {
	payload := strings.Join([]string{
		"stage-report-v2",
		generatedAt.UTC().Format(time.RFC3339),
		mode,
		window.label,
		strconv.Itoa(window.days),
		strconv.Itoa(grantReadinessScore),
		strconv.Itoa(readinessScore),
		fmt.Sprintf("%.6f", qualityScore),
		strconv.Itoa(fills),
		fmt.Sprintf("%.6f", totalPnL),
		fmt.Sprintf("%.6f", netPnLAfterFees),
		fmt.Sprintf("%.6f", netEdgeBps),
		fmt.Sprintf("%.6f", feeRateBps),
		strconv.FormatBool(builderFresh),
		strconv.FormatBool(riskTradable),
		strconv.FormatBool(hasTradingActivity),
	}, "|")
	sum := sha256.Sum256([]byte(payload))
	checksum := hex.EncodeToString(sum[:])
	id := fmt.Sprintf("stage-%s-%s-%s", window.label, generatedAt.UTC().Format("20060102T150405Z"), checksum[:12])
	return stageEvidence{
		ID:                  id,
		ChecksumSHA256:      checksum,
		ChecksumGeneratedAt: generatedAt.UTC(),
	}
}

func buildGrantArtifacts(window stageWindow) []grantArtifact {
	windowQ := "?window=" + window.label
	return []grantArtifact{
		{
			Name:     "stage_report_json",
			Path:     "/api/stage-report" + windowQ,
			Purpose:  "Primary stage scorecard and evidence snapshot",
			Required: true,
		},
		{
			Name:     "stage_report_markdown",
			Path:     "/api/stage-report" + windowQ + "&format=markdown",
			Purpose:  "Human-readable stage narrative for reviewer",
			Required: true,
		},
		{
			Name:     "stage_report_csv",
			Path:     "/api/stage-report" + windowQ + "&format=csv",
			Purpose:  "Tabular KPI export for reviewer spreadsheet",
			Required: true,
		},
		{
			Name:     "grant_report_json",
			Path:     "/api/grant-report",
			Purpose:  "Builder + risk + performance aggregate payload",
			Required: true,
		},
		{
			Name:     "grant_report_csv",
			Path:     "/api/grant-report?format=csv",
			Purpose:  "Raw grant metrics table",
			Required: true,
		},
		{
			Name:     "daily_report_json",
			Path:     "/api/daily-report",
			Purpose:  "Actionable diagnosis and next-day plan",
			Required: false,
		},
		{
			Name:     "execution_quality_json",
			Path:     "/api/execution-quality",
			Purpose:  "Execution friction and edge attribution evidence",
			Required: false,
		},
	}
}

func buildGrantMilestones() []grantMilestone {
	return []grantMilestone{
		{
			Name:        "builder_attribution_ready",
			Status:      "completed",
			EvidenceRef: "/api/builder",
		},
		{
			Name:        "risk_guardrails_enforced",
			Status:      "completed",
			EvidenceRef: "/api/risk",
		},
		{
			Name:        "performance_and_quality_reporting",
			Status:      "completed",
			EvidenceRef: "/api/execution-quality",
		},
		{
			Name:        "stage_review_bundle_exportable",
			Status:      "completed",
			EvidenceRef: "/api/stage-report",
		},
	}
}

func buildGrantManifestChecksum(
	packageID string,
	window stageWindow,
	evidence stageEvidence,
	artifacts []grantArtifact,
	milestones []grantMilestone,
) string {
	parts := make([]string, 0, 5+len(artifacts)+len(milestones))
	parts = append(parts, "grant-package-v1", packageID, window.label, strconv.Itoa(window.days), evidence.ID, evidence.ChecksumSHA256)
	for _, a := range artifacts {
		parts = append(parts, a.Name+"|"+a.Path+"|"+a.Purpose+"|"+strconv.FormatBool(a.Required))
	}
	for _, m := range milestones {
		parts = append(parts, m.Name+"|"+m.Status+"|"+m.EvidenceRef)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "||")))
	return hex.EncodeToString(sum[:])
}

func buildStageNarrative(
	builder builderStatus,
	rs riskStatus,
	metrics executionQualityMetrics,
	fills int,
) (strengths []string, risks []string) {
	strengths = make([]string, 0, 5)
	risks = make([]string, 0, 5)

	if builder.fresh {
		strengths = append(strengths, "Builder sync is fresh and attribution evidence is available.")
	} else {
		risks = append(risks, "Builder sync is stale or missing; attribution evidence may be weak.")
	}
	if rs.canTrade {
		strengths = append(strengths, "Risk guardrails allow trading and system is operational.")
	} else {
		risks = append(risks, fmt.Sprintf("Risk blocks trading: %s.", strings.Join(rs.blockedReasons, ",")))
	}
	if metrics.NetEdgeBps > 0 {
		strengths = append(strengths, fmt.Sprintf("Net execution edge stays positive (%.2f bps).", metrics.NetEdgeBps))
	} else {
		risks = append(risks, fmt.Sprintf("Net execution edge is non-positive (%.2f bps).", metrics.NetEdgeBps))
	}
	if metrics.FeeRateBps > 25 {
		risks = append(risks, fmt.Sprintf("Execution friction is elevated (%.2f bps fees).", metrics.FeeRateBps))
	} else if metrics.FeeRateBps > 0 {
		strengths = append(strengths, fmt.Sprintf("Execution friction is controlled (%.2f bps fees).", metrics.FeeRateBps))
	}
	if fills >= 20 {
		strengths = append(strengths, fmt.Sprintf("Sample size is sufficient for evaluation (%d fills).", fills))
	} else {
		risks = append(risks, fmt.Sprintf("Sample size is limited (%d fills); confidence is lower.", fills))
	}
	return strengths, risks
}

func renderStageReportMarkdown(
	generatedAt time.Time,
	tradingMode string,
	windowLabel string,
	evidence stageEvidence,
	grantReadinessScore int,
	readinessScore int,
	qualityScore float64,
	kpis map[string]interface{},
	strengths []string,
	risks []string,
	actions []coachAction,
) string {
	totalPnL, _ := kpis["total_pnl_usdc"].(float64)
	netPnL, _ := kpis["net_pnl_after_fees_usdc"].(float64)
	netEdge, _ := kpis["net_edge_bps"].(float64)
	feeRate, _ := kpis["fee_rate_bps"].(float64)

	var b strings.Builder
	b.WriteString("# Polymarket Trader Stage Report\n\n")
	b.WriteString(fmt.Sprintf("- Generated At (UTC): %s\n", generatedAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("- Trading Mode: %s\n", tradingMode))
	b.WriteString(fmt.Sprintf("- Window: %s\n", windowLabel))
	b.WriteString(fmt.Sprintf("- Grant Readiness Score: %d\n", grantReadinessScore))
	b.WriteString(fmt.Sprintf("- Readiness Score: %d\n", readinessScore))
	b.WriteString(fmt.Sprintf("- Execution Quality Score: %.2f\n", qualityScore))
	b.WriteString(fmt.Sprintf("- Evidence ID: %s\n", evidence.ID))
	b.WriteString(fmt.Sprintf("- Checksum (SHA256): %s\n\n", evidence.ChecksumSHA256))

	b.WriteString("## KPI Snapshot\n")
	b.WriteString(fmt.Sprintf("- Fills: %v\n", kpis["fills"]))
	b.WriteString(fmt.Sprintf("- Total PnL (USDC): %.2f\n", totalPnL))
	b.WriteString(fmt.Sprintf("- Net PnL After Fees (USDC): %.2f\n", netPnL))
	b.WriteString(fmt.Sprintf("- Net Edge (bps): %.2f\n", netEdge))
	b.WriteString(fmt.Sprintf("- Fee Rate (bps): %.2f\n\n", feeRate))

	b.WriteString("## Strengths\n")
	for _, s := range strengths {
		b.WriteString("- " + s + "\n")
	}
	if len(strengths) == 0 {
		b.WriteString("- None detected.\n")
	}

	b.WriteString("\n## Risks\n")
	for _, r := range risks {
		b.WriteString("- " + r + "\n")
	}
	if len(risks) == 0 {
		b.WriteString("- No major risk detected.\n")
	}

	b.WriteString("\n## Next Actions\n")
	for _, a := range actions {
		b.WriteString(fmt.Sprintf("- [%s] %s\n", a.Code, a.Message))
	}
	if len(actions) == 0 {
		b.WriteString("- Keep current plan and continue monitoring.\n")
	}
	return b.String()
}

func renderGrantPackageMarkdown(
	generatedAt time.Time,
	packageID string,
	window stageWindow,
	mode string,
	grantReadinessScore int,
	evidence stageEvidence,
	artifacts []grantArtifact,
	milestones []grantMilestone,
	manifestChecksum string,
) string {
	var b strings.Builder
	b.WriteString("# Polymarket Grant Submission Package\n\n")
	b.WriteString(fmt.Sprintf("- Generated At (UTC): %s\n", generatedAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("- Package ID: %s\n", packageID))
	b.WriteString(fmt.Sprintf("- Trading Mode: %s\n", mode))
	b.WriteString(fmt.Sprintf("- Window: %s (%d days)\n", window.label, window.days))
	b.WriteString(fmt.Sprintf("- Grant Readiness Score: %d\n", grantReadinessScore))
	b.WriteString(fmt.Sprintf("- Stage Evidence ID: %s\n", evidence.ID))
	b.WriteString(fmt.Sprintf("- Stage Checksum (SHA256): %s\n", evidence.ChecksumSHA256))
	b.WriteString(fmt.Sprintf("- Manifest Checksum (SHA256): %s\n\n", manifestChecksum))

	b.WriteString("## Milestones\n")
	for _, m := range milestones {
		b.WriteString(fmt.Sprintf("- [%s] %s (%s)\n", m.Status, m.Name, m.EvidenceRef))
	}
	if len(milestones) == 0 {
		b.WriteString("- No milestones listed.\n")
	}

	b.WriteString("\n## Artifacts\n")
	for _, a := range artifacts {
		required := "optional"
		if a.Required {
			required = "required"
		}
		b.WriteString(fmt.Sprintf("- `%s` (%s): %s — %s\n", a.Name, required, a.Path, a.Purpose))
	}
	if len(artifacts) == 0 {
		b.WriteString("- No artifacts listed.\n")
	}

	return b.String()
}

func (s *Server) writeStageReportCSV(
	w http.ResponseWriter,
	generatedAt time.Time,
	mode string,
	window stageWindow,
	evidence stageEvidence,
	grantReadinessScore int,
	readinessScore int,
	qualityScore float64,
	fills int,
	totalPnL float64,
	netPnLAfterFees float64,
	netEdgeBps float64,
	feeRateBps float64,
	builderFresh bool,
	riskTradable bool,
	hasTradingActivity bool,
	actionsCount int,
) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	cw := csv.NewWriter(w)
	header := []string{
		"generated_at",
		"window_label",
		"window_days",
		"trading_mode",
		"evidence_id",
		"checksum_sha256",
		"checksum_generated_at",
		"grant_readiness_score",
		"readiness_score",
		"quality_score",
		"fills",
		"total_pnl_usdc",
		"net_pnl_after_fees_usdc",
		"net_edge_bps",
		"fee_rate_bps",
		"builder_fresh",
		"risk_tradable",
		"has_trading_activity",
		"next_actions_count",
	}
	record := []string{
		generatedAt.Format(time.RFC3339),
		window.label,
		strconv.Itoa(window.days),
		mode,
		evidence.ID,
		evidence.ChecksumSHA256,
		evidence.ChecksumGeneratedAt.Format(time.RFC3339),
		strconv.Itoa(grantReadinessScore),
		strconv.Itoa(readinessScore),
		fmt.Sprintf("%.2f", qualityScore),
		strconv.Itoa(fills),
		fmt.Sprintf("%.6f", totalPnL),
		fmt.Sprintf("%.6f", netPnLAfterFees),
		fmt.Sprintf("%.6f", netEdgeBps),
		fmt.Sprintf("%.6f", feeRateBps),
		strconv.FormatBool(builderFresh),
		strconv.FormatBool(riskTradable),
		strconv.FormatBool(hasTradingActivity),
		strconv.Itoa(actionsCount),
	}
	if err := cw.Write(header); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := cw.Write(record); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// GET /api/risk — current risk guardrail status.
func (s *Server) handleRisk(w http.ResponseWriter, _ *http.Request) {
	snap := s.appState.RiskSnapshot()
	rs := buildRiskStatus(snap)
	s.writeJSON(w, map[string]interface{}{
		"emergency_stop":            snap.EmergencyStop,
		"daily_pnl":                 snap.DailyPnL,
		"daily_loss_limit_usdc":     snap.DailyLossLimitUSDC,
		"daily_loss_used_pct":       rs.usagePct,
		"daily_loss_remaining_usdc": rs.remainingUSDC,
		"daily_loss_remaining_pct":  rs.remainingPct,
		"can_trade":                 rs.canTrade,
		"blocked_reasons":           rs.blockedReasons,
		"consecutive_losses":        snap.ConsecutiveLosses,
		"max_consecutive_losses":    snap.MaxConsecutiveLosses,
		"in_cooldown":               snap.InCooldown,
		"cooldown_remaining_s":      snap.CooldownRemaining.Seconds(),
	})
}

// GET /api/coach — actionable coaching for sizing and capital protection.
func (s *Server) handleCoach(w http.ResponseWriter, _ *http.Request) {
	generatedAt := time.Now().UTC()
	_, fills, realized := s.appState.Stats()
	unrealized := s.appState.UnrealizedPnL()
	totalPnL := realized + unrealized
	pnlPerFill := 0.0
	if fills > 0 {
		pnlPerFill = totalPnL / float64(fills)
	}

	snap := s.appState.RiskSnapshot()
	rs := buildRiskStatus(snap)
	positions := s.appState.TrackedPositions()

	profitableMarkets := 0
	losingMarkets := 0
	grossProfit := 0.0
	grossLoss := 0.0
	trackedMarkets := 0
	best := marketSnapshot{pnlUSDC: math.Inf(-1)}
	worst := marketSnapshot{pnlUSDC: math.Inf(1)}

	for assetID, pos := range positions {
		if pos.NetSize == 0 && pos.RealizedPnL == 0 && pos.TotalFills == 0 {
			continue
		}
		trackedMarkets++
		pnl := pos.RealizedPnL
		if pnl > 0 {
			profitableMarkets++
			grossProfit += pnl
		}
		if pnl < 0 {
			losingMarkets++
			grossLoss += -pnl
		}
		if pnl > best.pnlUSDC {
			best = marketSnapshot{assetID: assetID, pnlUSDC: pnl}
		}
		if pnl < worst.pnlUSDC {
			worst = marketSnapshot{assetID: assetID, pnlUSDC: pnl}
		}
	}

	var profitFactor interface{}
	if grossLoss > 0 {
		profitFactor = grossProfit / grossLoss
	}

	riskMode, sizeMultiplier := chooseSizingMode(
		rs.canTrade,
		rs.usagePct,
		totalPnL,
		snap.ConsecutiveLosses,
		snap.MaxConsecutiveLosses,
	)

	recentFills := s.appState.RecentFills(50)
	baseOrderUSDC := averageFillNotional(recentFills)
	if baseOrderUSDC <= 0 {
		baseOrderUSDC = 5
	}
	if rs.remainingUSDC > 0 {
		riskCap := rs.remainingUSDC * 0.10
		if riskCap > 0 && riskCap < baseOrderUSDC {
			baseOrderUSDC = riskCap
		}
	}
	suggestedOrderUSDC := 0.0
	if riskMode != "pause" {
		suggestedOrderUSDC = baseOrderUSDC * sizeMultiplier
	}

	actions := buildCoachActions(
		rs.canTrade,
		rs.blockedReasons,
		snap.InCooldown,
		snap.CooldownRemaining,
		rs.usagePct,
		fills,
		pnlPerFill,
		profitableMarkets,
		best,
	)

	var bestMarket interface{}
	var worstMarket interface{}
	if best.assetID != "" {
		bestMarket = map[string]interface{}{
			"asset_id":          best.assetID,
			"realized_pnl_usdc": best.pnlUSDC,
		}
	}
	if worst.assetID != "" {
		worstMarket = map[string]interface{}{
			"asset_id":          worst.assetID,
			"realized_pnl_usdc": worst.pnlUSDC,
		}
	}

	s.writeJSON(w, map[string]interface{}{
		"generated_at":              generatedAt,
		"trading_mode":              s.appState.TradingMode(),
		"can_trade":                 rs.canTrade,
		"blocked_reasons":           rs.blockedReasons,
		"daily_loss_used_pct":       rs.usagePct,
		"daily_loss_remaining_usdc": rs.remainingUSDC,
		"consecutive_losses":        snap.ConsecutiveLosses,
		"max_consecutive_losses":    snap.MaxConsecutiveLosses,
		"cooldown_remaining_s":      snap.CooldownRemaining.Seconds(),
		"fills":                     fills,
		"realized_pnl_usdc":         realized,
		"unrealized_pnl_usdc":       unrealized,
		"total_pnl_usdc":            totalPnL,
		"pnl_per_fill_usdc":         pnlPerFill,
		"market_stats": map[string]interface{}{
			"tracked_markets":    trackedMarkets,
			"profitable_markets": profitableMarkets,
			"losing_markets":     losingMarkets,
			"gross_profit_usdc":  grossProfit,
			"gross_loss_usdc":    grossLoss,
			"profit_factor":      profitFactor,
			"best_market":        bestMarket,
			"worst_market":       worstMarket,
		},
		"sizing": map[string]interface{}{
			"risk_mode":                riskMode,
			"size_multiplier":          sizeMultiplier,
			"base_order_usdc":          round2(baseOrderUSDC),
			"suggested_max_order_usdc": round2(suggestedOrderUSDC),
		},
		"actions": actions,
	})
}

// GET /api/sizing — risk-budget-based position sizing guidance.
func (s *Server) handleSizing(w http.ResponseWriter, _ *http.Request) {
	generatedAt := time.Now().UTC()
	mode := s.appState.TradingMode()
	_, fills, realized := s.appState.Stats()
	unrealized := s.appState.UnrealizedPnL()
	totalPnL := realized + unrealized

	snap := s.appState.RiskSnapshot()
	rs := buildRiskStatus(snap)
	riskMode, sizeMultiplier := chooseSizingMode(
		rs.canTrade,
		rs.usagePct,
		totalPnL,
		snap.ConsecutiveLosses,
		snap.MaxConsecutiveLosses,
	)

	recentFills := s.appState.RecentFills(200)
	metrics := calculateExecutionQualityMetrics(
		mode,
		fills,
		totalPnL,
		s.appState.PaperSnapshot(),
		recentFills,
	)
	scores := buildMarketScores(s.appState.TrackedPositions())
	allocation := buildSizingAllocation(scores, 3)
	riskBudget, perTrade, suggestedMaxOrder, recommendedTrades := calculateSizingBudget(
		rs,
		riskMode,
		sizeMultiplier,
		metrics,
		recentFills,
	)
	actions := buildSizingActions(rs.canTrade, rs.blockedReasons, riskMode, metrics, allocation)

	s.writeJSON(w, map[string]interface{}{
		"generated_at":    generatedAt,
		"trading_mode":    mode,
		"can_trade":       rs.canTrade,
		"blocked_reasons": rs.blockedReasons,
		"risk_mode":       riskMode,
		"size_multiplier": sizeMultiplier,
		"inputs": map[string]interface{}{
			"daily_loss_limit_usdc":     snap.DailyLossLimitUSDC,
			"daily_loss_remaining_usdc": rs.remainingUSDC,
			"daily_loss_used_pct":       rs.usagePct,
			"fills":                     fills,
			"total_pnl_usdc":            round2(totalPnL),
			"pnl_per_fill_usdc":         round2(metrics.PnLPerFillUSDC),
			"net_edge_bps":              round2(metrics.NetEdgeBps),
			"quality_score":             round2(metrics.QualityScore),
		},
		"budget": map[string]interface{}{
			"risk_budget_usdc":           round2(riskBudget),
			"recommended_trades":         recommendedTrades,
			"recommended_per_trade_usdc": round2(perTrade),
			"suggested_max_order_usdc":   round2(suggestedMaxOrder),
		},
		"allocation": allocation,
		"actions":    actions,
	})
}

// GET /api/insights — market-level profitability ranking and actionable focus hints.
func (s *Server) handleInsights(w http.ResponseWriter, _ *http.Request) {
	generatedAt := time.Now().UTC()
	_, fills, realized := s.appState.Stats()
	unrealized := s.appState.UnrealizedPnL()
	totalPnL := realized + unrealized
	pnlPerFill := 0.0
	if fills > 0 {
		pnlPerFill = totalPnL / float64(fills)
	}

	snap := s.appState.RiskSnapshot()
	rs := buildRiskStatus(snap)
	riskMode, sizeMultiplier := chooseSizingMode(
		rs.canTrade,
		rs.usagePct,
		totalPnL,
		snap.ConsecutiveLosses,
		snap.MaxConsecutiveLosses,
	)

	marketScores := buildMarketScores(s.appState.TrackedPositions())
	recommendations := buildInsightRecommendations(
		rs.canTrade,
		rs.blockedReasons,
		fills,
		pnlPerFill,
		marketScores,
	)

	s.writeJSON(w, map[string]interface{}{
		"generated_at":    generatedAt,
		"trading_mode":    s.appState.TradingMode(),
		"can_trade":       rs.canTrade,
		"blocked_reasons": rs.blockedReasons,
		"market_scores":   marketScores,
		"recommendations": recommendations,
		"summary": map[string]interface{}{
			"fills":               fills,
			"realized_pnl_usdc":   realized,
			"unrealized_pnl_usdc": unrealized,
			"total_pnl_usdc":      totalPnL,
			"pnl_per_fill_usdc":   round2(pnlPerFill),
			"risk_mode":           riskMode,
			"size_multiplier":     sizeMultiplier,
			"daily_loss_used_pct": round2(rs.usagePct),
		},
	})
}

// GET /api/execution-quality — execution friction attribution and action hints.
func (s *Server) handleExecutionQuality(w http.ResponseWriter, _ *http.Request) {
	generatedAt := time.Now().UTC()
	mode := s.appState.TradingMode()
	_, fills, realized := s.appState.Stats()
	unrealized := s.appState.UnrealizedPnL()
	totalPnL := realized + unrealized

	snap := s.appState.RiskSnapshot()
	rs := buildRiskStatus(snap)
	recentFills := s.appState.RecentFills(200)
	metrics := calculateExecutionQualityMetrics(
		mode,
		fills,
		totalPnL,
		s.appState.PaperSnapshot(),
		recentFills,
	)
	breakdown := calculateExecutionLossBreakdown(metrics)
	recommendations := buildExecutionQualityRecommendations(
		rs.canTrade,
		rs.blockedReasons,
		metrics,
		breakdown,
	)

	s.writeJSON(w, map[string]interface{}{
		"generated_at":    generatedAt,
		"trading_mode":    mode,
		"can_trade":       rs.canTrade,
		"blocked_reasons": rs.blockedReasons,
		"metrics":         metrics,
		"breakdown":       breakdown,
		"recommendations": recommendations,
	})
}

// GET /api/daily-report — day-close diagnosis and next-cycle action plan.
func (s *Server) handleDailyReport(w http.ResponseWriter, _ *http.Request) {
	generatedAt := time.Now().UTC()
	mode := s.appState.TradingMode()
	_, fills, realized := s.appState.Stats()
	unrealized := s.appState.UnrealizedPnL()
	totalPnL := realized + unrealized

	snap := s.appState.RiskSnapshot()
	rs := buildRiskStatus(snap)
	riskMode, sizeMultiplier := chooseSizingMode(
		rs.canTrade,
		rs.usagePct,
		totalPnL,
		snap.ConsecutiveLosses,
		snap.MaxConsecutiveLosses,
	)

	recentFills := s.appState.RecentFills(200)
	metrics := calculateExecutionQualityMetrics(
		mode,
		fills,
		totalPnL,
		s.appState.PaperSnapshot(),
		recentFills,
	)
	netPnLAfterFees := round2(totalPnL - metrics.FeesPaidUSDC)
	outcome := pnlOutcome(netPnLAfterFees)
	scores := buildMarketScores(s.appState.TrackedPositions())
	actions := buildDailyReportActions(outcome, riskMode, rs, metrics, scores)

	reasons := []string{
		fmt.Sprintf("net_edge_bps=%.2f", metrics.NetEdgeBps),
		fmt.Sprintf("fee_rate_bps=%.2f", metrics.FeeRateBps),
	}
	if !rs.canTrade {
		reasons = append(reasons, fmt.Sprintf("risk_blocked=%s", strings.Join(rs.blockedReasons, ",")))
	}
	if len(scores) > 0 {
		reasons = append(reasons, fmt.Sprintf("top_market=%s", scores[0].AssetID))
	}

	s.writeJSON(w, map[string]interface{}{
		"generated_at":    generatedAt,
		"trading_mode":    mode,
		"can_trade":       rs.canTrade,
		"blocked_reasons": rs.blockedReasons,
		"summary": map[string]interface{}{
			"fills":                   fills,
			"realized_pnl_usdc":       round2(realized),
			"unrealized_pnl_usdc":     round2(unrealized),
			"total_pnl_usdc":          round2(totalPnL),
			"fees_paid_usdc":          round2(metrics.FeesPaidUSDC),
			"net_pnl_after_fees_usdc": netPnLAfterFees,
			"pnl_per_fill_usdc":       round2(metrics.PnLPerFillUSDC),
			"gross_edge_bps":          round2(metrics.GrossEdgeBps),
			"net_edge_bps":            round2(metrics.NetEdgeBps),
			"fee_rate_bps":            round2(metrics.FeeRateBps),
		},
		"diagnosis": map[string]interface{}{
			"outcome":       outcome,
			"quality_score": metrics.QualityScore,
			"reasons":       reasons,
		},
		"market_scores": scores,
		"tomorrow_plan": map[string]interface{}{
			"risk_mode":       riskMode,
			"size_multiplier": sizeMultiplier,
			"focus_assets":    topFocusAssets(scores, 2),
		},
		"next_actions": actions,
	})
}

// GET /api/stage-report — grant-facing evidence bundle for current stage.
func (s *Server) handleStageReport(w http.ResponseWriter, r *http.Request) {
	generatedAt := time.Now().UTC()
	window := parseStageWindow(r.URL.Query().Get("window"))
	mode := s.appState.TradingMode()
	_, fills, realized := s.appState.Stats()
	unrealized := s.appState.UnrealizedPnL()
	totalPnL := realized + unrealized
	paperSnap := s.appState.PaperSnapshot()

	snap := s.appState.RiskSnapshot()
	rs := buildRiskStatus(snap)
	builder := s.currentBuilderStatus()
	hasTradingActivity := fills > 0
	readinessScore := calcReadinessScore(builder.fresh, rs.canTrade, hasTradingActivity)

	recentFills := s.appState.RecentFills(200)
	metrics := calculateExecutionQualityMetrics(mode, fills, totalPnL, paperSnap, recentFills)
	grantReadinessScore := int(math.Round(float64(readinessScore)*0.6 + metrics.QualityScore*0.4))
	if grantReadinessScore < 0 {
		grantReadinessScore = 0
	}
	if grantReadinessScore > 100 {
		grantReadinessScore = 100
	}

	kpis := map[string]interface{}{
		"fills":                   fills,
		"realized_pnl_usdc":       round2(realized),
		"unrealized_pnl_usdc":     round2(unrealized),
		"total_pnl_usdc":          round2(totalPnL),
		"fees_paid_usdc":          round2(metrics.FeesPaidUSDC),
		"net_pnl_after_fees_usdc": round2(metrics.NetPnLAfterFeesUSDC),
		"net_edge_bps":            round2(metrics.NetEdgeBps),
		"fee_rate_bps":            round2(metrics.FeeRateBps),
	}

	strengths, risks := buildStageNarrative(builder, rs, metrics, fills)
	riskMode, _ := chooseSizingMode(
		rs.canTrade,
		rs.usagePct,
		totalPnL,
		snap.ConsecutiveLosses,
		snap.MaxConsecutiveLosses,
	)
	scores := buildMarketScores(s.appState.TrackedPositions())
	actions := buildDailyReportActions(
		pnlOutcome(metrics.NetPnLAfterFeesUSDC),
		riskMode,
		rs,
		metrics,
		scores,
	)
	evidence := buildStageEvidence(
		generatedAt,
		mode,
		window,
		grantReadinessScore,
		readinessScore,
		metrics.QualityScore,
		fills,
		round2(totalPnL),
		round2(metrics.NetPnLAfterFeesUSDC),
		round2(metrics.NetEdgeBps),
		round2(metrics.FeeRateBps),
		builder.fresh,
		rs.canTrade,
		hasTradingActivity,
	)

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "csv" {
		s.writeStageReportCSV(
			w,
			generatedAt,
			mode,
			window,
			evidence,
			grantReadinessScore,
			readinessScore,
			metrics.QualityScore,
			fills,
			round2(totalPnL),
			round2(metrics.NetPnLAfterFeesUSDC),
			round2(metrics.NetEdgeBps),
			round2(metrics.FeeRateBps),
			builder.fresh,
			rs.canTrade,
			hasTradingActivity,
			len(actions),
		)
		return
	}

	if format == "markdown" || format == "md" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		md := renderStageReportMarkdown(
			generatedAt,
			mode,
			window.label,
			evidence,
			grantReadinessScore,
			readinessScore,
			metrics.QualityScore,
			kpis,
			strengths,
			risks,
			actions,
		)
		if _, err := w.Write([]byte(md)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	s.writeJSON(w, map[string]interface{}{
		"generated_at":    generatedAt,
		"trading_mode":    mode,
		"window":          map[string]interface{}{"label": window.label, "days": window.days},
		"can_trade":       rs.canTrade,
		"blocked_reasons": rs.blockedReasons,
		"scorecard": map[string]interface{}{
			"grant_readiness_score": grantReadinessScore,
			"readiness_score":       readinessScore,
			"quality_score":         metrics.QualityScore,
			"builder_fresh":         builder.fresh,
			"risk_tradable":         rs.canTrade,
			"has_trading_activity":  hasTradingActivity,
			"positive_net_edge":     metrics.NetEdgeBps > 0,
		},
		"kpis": kpis,
		"builder": map[string]interface{}{
			"configured":         builder.configured,
			"daily_volume_count": builder.dailyVolumeCount,
			"leaderboard_count":  builder.leaderboardCount,
			"last_sync_age_s":    builder.lastSyncAgeSeconds,
			"never_synced":       builder.neverSynced,
			"stale":              builder.stale,
		},
		"narrative": map[string]interface{}{
			"strengths": strengths,
			"risks":     risks,
		},
		"next_actions": actions,
		"evidence": map[string]interface{}{
			"evidence_id":           evidence.ID,
			"checksum_sha256":       evidence.ChecksumSHA256,
			"checksum_generated_at": evidence.ChecksumGeneratedAt,
			"endpoints": []string{
				"/api/grant-report",
				"/api/coach",
				"/api/insights",
				"/api/execution-quality",
				"/api/daily-report",
			},
			"version": "stage-report-v2",
		},
	})
}

// GET /api/grant-package — reviewer-friendly package manifest for grant submission.
func (s *Server) handleGrantPackage(w http.ResponseWriter, r *http.Request) {
	generatedAt := time.Now().UTC()
	window := parseStageWindow(r.URL.Query().Get("window"))
	mode := s.appState.TradingMode()
	_, fills, realized := s.appState.Stats()
	unrealized := s.appState.UnrealizedPnL()
	totalPnL := realized + unrealized

	snap := s.appState.RiskSnapshot()
	rs := buildRiskStatus(snap)
	builder := s.currentBuilderStatus()
	hasTradingActivity := fills > 0
	readinessScore := calcReadinessScore(builder.fresh, rs.canTrade, hasTradingActivity)

	recentFills := s.appState.RecentFills(200)
	metrics := calculateExecutionQualityMetrics(mode, fills, totalPnL, s.appState.PaperSnapshot(), recentFills)
	grantReadinessScore := int(math.Round(float64(readinessScore)*0.6 + metrics.QualityScore*0.4))
	if grantReadinessScore < 0 {
		grantReadinessScore = 0
	}
	if grantReadinessScore > 100 {
		grantReadinessScore = 100
	}

	evidence := buildStageEvidence(
		generatedAt,
		mode,
		window,
		grantReadinessScore,
		readinessScore,
		metrics.QualityScore,
		fills,
		round2(totalPnL),
		round2(metrics.NetPnLAfterFeesUSDC),
		round2(metrics.NetEdgeBps),
		round2(metrics.FeeRateBps),
		builder.fresh,
		rs.canTrade,
		hasTradingActivity,
	)
	packageID := fmt.Sprintf("grant-%s-%s-%s", window.label, generatedAt.Format("20060102T150405Z"), evidence.ChecksumSHA256[:10])
	artifacts := buildGrantArtifacts(window)
	milestones := buildGrantMilestones()
	manifestChecksum := buildGrantManifestChecksum(packageID, window, evidence, artifacts, milestones)

	if format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format"))); format == "markdown" || format == "md" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		md := renderGrantPackageMarkdown(
			generatedAt,
			packageID,
			window,
			mode,
			grantReadinessScore,
			evidence,
			artifacts,
			milestones,
			manifestChecksum,
		)
		if _, err := w.Write([]byte(md)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	s.writeJSON(w, map[string]interface{}{
		"generated_at": generatedAt,
		"package_id":   packageID,
		"trading_mode": mode,
		"window": map[string]interface{}{
			"label": window.label,
			"days":  window.days,
		},
		"scorecard": map[string]interface{}{
			"grant_readiness_score": grantReadinessScore,
			"readiness_score":       readinessScore,
			"quality_score":         metrics.QualityScore,
			"builder_fresh":         builder.fresh,
			"risk_tradable":         rs.canTrade,
			"has_trading_activity":  hasTradingActivity,
		},
		"milestones": milestones,
		"artifacts":  artifacts,
		"manifest": map[string]interface{}{
			"checksum_sha256":       manifestChecksum,
			"generated_at":          generatedAt,
			"stage_evidence_id":     evidence.ID,
			"stage_checksum_sha256": evidence.ChecksumSHA256,
			"artifacts_count":       len(artifacts),
			"milestones_count":      len(milestones),
			"version":               "grant-package-v1",
		},
	})
}

// GET /api/paper — paper-trading account snapshot.
func (s *Server) handlePaper(w http.ResponseWriter, _ *http.Request) {
	snap := s.appState.PaperSnapshot()
	_, _, realized := s.appState.Stats()
	unrealized := s.appState.UnrealizedPnL()
	estimatedEquity := snap.InitialBalanceUSDC + realized + unrealized - snap.FeesPaidUSDC
	s.writeJSON(w, map[string]interface{}{
		"trading_mode":          s.appState.TradingMode(),
		"initial_balance_usdc":  snap.InitialBalanceUSDC,
		"balance_usdc":          snap.BalanceUSDC,
		"fees_paid_usdc":        snap.FeesPaidUSDC,
		"total_volume_usdc":     snap.TotalVolumeUSDC,
		"total_trades":          snap.TotalTrades,
		"allow_short":           snap.AllowShort,
		"inventory_by_asset":    snap.InventoryByAsset,
		"realized_pnl_usdc":     realized,
		"unrealized_pnl_usdc":   unrealized,
		"estimated_equity_usdc": estimatedEquity,
	})
}

// POST /api/emergency-stop — trigger emergency stop.
func (s *Server) handleEmergencyStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.appState.SetEmergencyStop(true)
	s.writeJSON(w, map[string]string{"status": "emergency_stop_activated"})
}
