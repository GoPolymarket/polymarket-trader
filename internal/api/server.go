package api

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"github.com/GoPolymarket/polymarket-trader/internal/execution"
	"github.com/GoPolymarket/polymarket-trader/internal/paper"
	"github.com/GoPolymarket/polymarket-trader/internal/risk"
)

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
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/positions", s.handlePositions)
	mux.HandleFunc("/api/pnl", s.handlePnL)
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
		})
		return
	}
	dailyVolume := s.builder.DailyVolumeJSON()
	leaderboard := s.builder.LeaderboardJSON()
	lastSync := s.builder.LastSync()
	lastSyncAgeS := 0.0
	if !lastSync.IsZero() {
		lastSyncAgeS = time.Since(lastSync).Seconds()
	}
	s.writeJSON(w, map[string]interface{}{
		"configured":         true,
		"daily_volume":       dailyVolume,
		"daily_volume_count": countEntries(dailyVolume),
		"leaderboard":        leaderboard,
		"leaderboard_count":  countEntries(leaderboard),
		"last_sync":          lastSync,
		"last_sync_age_s":    lastSyncAgeS,
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

// GET /api/risk — current risk guardrail status.
func (s *Server) handleRisk(w http.ResponseWriter, _ *http.Request) {
	snap := s.appState.RiskSnapshot()
	usagePct := 0.0
	remainingUSDC := 0.0
	remainingPct := 0.0
	blockedReasons := make([]string, 0, 3)
	if snap.EmergencyStop {
		blockedReasons = append(blockedReasons, "emergency_stop")
	}
	if snap.DailyLossLimitUSDC > 0 {
		usagePct = (-snap.DailyPnL / snap.DailyLossLimitUSDC) * 100
		if usagePct < 0 {
			usagePct = 0
		}
		remainingUSDC = snap.DailyLossLimitUSDC + snap.DailyPnL
		if remainingUSDC < 0 {
			remainingUSDC = 0
		}
		remainingPct = 100 - usagePct
		if remainingPct < 0 {
			remainingPct = 0
		}
		if snap.DailyPnL <= -snap.DailyLossLimitUSDC {
			blockedReasons = append(blockedReasons, "daily_loss_limit_reached")
		}
	}
	if snap.InCooldown {
		blockedReasons = append(blockedReasons, "loss_cooldown_active")
	}
	s.writeJSON(w, map[string]interface{}{
		"emergency_stop":            snap.EmergencyStop,
		"daily_pnl":                 snap.DailyPnL,
		"daily_loss_limit_usdc":     snap.DailyLossLimitUSDC,
		"daily_loss_used_pct":       usagePct,
		"daily_loss_remaining_usdc": remainingUSDC,
		"daily_loss_remaining_pct":  remainingPct,
		"can_trade":                 len(blockedReasons) == 0,
		"blocked_reasons":           blockedReasons,
		"consecutive_losses":        snap.ConsecutiveLosses,
		"max_consecutive_losses":    snap.MaxConsecutiveLosses,
		"in_cooldown":               snap.InCooldown,
		"cooldown_remaining_s":      snap.CooldownRemaining.Seconds(),
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
