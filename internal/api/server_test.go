package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/GoPolymarket/polymarket-trader/internal/execution"
	"github.com/GoPolymarket/polymarket-trader/internal/paper"
	"github.com/GoPolymarket/polymarket-trader/internal/risk"
)

type mockAppState struct {
	running       bool
	dryRun        bool
	orders        int
	fills         int
	pnl           float64
	assets        []string
	positions     map[string]execution.Position
	unrealPnL     float64
	recentFills   []execution.Fill
	activeOrders  []execution.OrderState
	riskSnapshot  risk.Snapshot
	tradingMode   string
	paperSnapshot paper.Snapshot
}

func (m *mockAppState) Stats() (int, int, float64)                      { return m.orders, m.fills, m.pnl }
func (m *mockAppState) IsRunning() bool                                 { return m.running }
func (m *mockAppState) IsDryRun() bool                                  { return m.dryRun }
func (m *mockAppState) MonitoredAssets() []string                       { return m.assets }
func (m *mockAppState) SetEmergencyStop(_ bool)                         {}
func (m *mockAppState) RecentFills(limit int) []execution.Fill          { return m.recentFills }
func (m *mockAppState) ActiveOrders() []execution.OrderState            { return m.activeOrders }
func (m *mockAppState) TrackedPositions() map[string]execution.Position { return m.positions }
func (m *mockAppState) UnrealizedPnL() float64                          { return m.unrealPnL }
func (m *mockAppState) RiskSnapshot() risk.Snapshot                     { return m.riskSnapshot }
func (m *mockAppState) TradingMode() string                             { return m.tradingMode }
func (m *mockAppState) PaperSnapshot() paper.Snapshot                   { return m.paperSnapshot }

type mockPortfolio struct {
	value    float64
	lastSync time.Time
}

func (m *mockPortfolio) TotalValue() float64 { return m.value }
func (m *mockPortfolio) LastSync() time.Time { return m.lastSync }

type mockBuilder struct {
	lastSync    time.Time
	dailyVolume interface{}
	leaderboard interface{}
}

func (m *mockBuilder) DailyVolumeJSON() interface{} { return m.dailyVolume }
func (m *mockBuilder) LeaderboardJSON() interface{} { return m.leaderboard }
func (m *mockBuilder) LastSync() time.Time          { return m.lastSync }

func TestHandleStatus(t *testing.T) {
	state := &mockAppState{
		running:     true,
		dryRun:      true,
		orders:      5,
		fills:       10,
		pnl:         1.23,
		assets:      []string{"asset-1", "asset-2"},
		tradingMode: "paper",
	}
	portfolio := &mockPortfolio{value: 100.50, lastSync: time.Now()}
	s := NewServer(":0", state, portfolio, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp["running"] != true {
		t.Error("expected running=true")
	}
	if resp["dry_run"] != true {
		t.Error("expected dry_run=true")
	}
	if int(resp["orders"].(float64)) != 5 {
		t.Errorf("expected orders=5, got %v", resp["orders"])
	}
	if int(resp["fills"].(float64)) != 10 {
		t.Errorf("expected fills=10, got %v", resp["fills"])
	}
	if resp["portfolio_value"].(float64) != 100.50 {
		t.Errorf("expected portfolio_value=100.50, got %v", resp["portfolio_value"])
	}
	if resp["trading_mode"] != "paper" {
		t.Errorf("expected trading_mode=paper, got %v", resp["trading_mode"])
	}
}

func TestHandlePositions(t *testing.T) {
	state := &mockAppState{
		positions: map[string]execution.Position{
			"asset-1": {AssetID: "asset-1", NetSize: 10.5, AvgEntryPrice: 0.55, RealizedPnL: 1.2, TotalFills: 3},
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/positions", nil)
	w := httptest.NewRecorder()
	s.handlePositions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	positions := resp["positions"].([]interface{})
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}
}

func TestHandlePnL(t *testing.T) {
	state := &mockAppState{pnl: 5.0, unrealPnL: 2.5}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/pnl", nil)
	w := httptest.NewRecorder()
	s.handlePnL(w, req)

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp["realized_pnl"].(float64) != 5.0 {
		t.Errorf("expected realized_pnl=5.0, got %v", resp["realized_pnl"])
	}
	if resp["unrealized_pnl"].(float64) != 2.5 {
		t.Errorf("expected unrealized_pnl=2.5, got %v", resp["unrealized_pnl"])
	}
	if resp["total_pnl"].(float64) != 7.5 {
		t.Errorf("expected total_pnl=7.5, got %v", resp["total_pnl"])
	}
}

func TestHandleTrades(t *testing.T) {
	state := &mockAppState{
		recentFills: []execution.Fill{
			{TradeID: "t1", AssetID: "asset-1", Side: "BUY", Price: 0.50, Size: 10, Timestamp: time.Now()},
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/trades?limit=10", nil)
	w := httptest.NewRecorder()
	s.handleTrades(w, req)

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if int(resp["count"].(float64)) != 1 {
		t.Errorf("expected count=1, got %v", resp["count"])
	}
}

func TestHandleOrders(t *testing.T) {
	state := &mockAppState{
		activeOrders: []execution.OrderState{
			{ID: "o1", AssetID: "asset-1", Side: "BUY", Price: 0.50, OrigSize: 10},
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
	w := httptest.NewRecorder()
	s.handleOrders(w, req)

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if int(resp["count"].(float64)) != 1 {
		t.Errorf("expected count=1, got %v", resp["count"])
	}
}

func TestHandleEmergencyStopMethodNotAllowed(t *testing.T) {
	s := NewServer(":0", &mockAppState{}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/emergency-stop", nil)
	w := httptest.NewRecorder()
	s.handleEmergencyStop(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleBuilder(t *testing.T) {
	builder := &mockBuilder{
		lastSync:    time.Now().Add(-2 * time.Minute),
		dailyVolume: []string{"v1", "v2"},
		leaderboard: []string{"b1"},
	}
	s := NewServer(":0", &mockAppState{}, nil, builder)

	req := httptest.NewRequest(http.MethodGet, "/api/builder", nil)
	w := httptest.NewRecorder()
	s.handleBuilder(w, req)

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["last_sync"] == nil {
		t.Error("expected last_sync")
	}
	if resp["configured"] != true {
		t.Errorf("expected configured=true, got %v", resp["configured"])
	}
	if int(resp["daily_volume_count"].(float64)) != 2 {
		t.Errorf("expected daily_volume_count=2, got %v", resp["daily_volume_count"])
	}
	if int(resp["leaderboard_count"].(float64)) != 1 {
		t.Errorf("expected leaderboard_count=1, got %v", resp["leaderboard_count"])
	}
	if resp["last_sync_age_s"] == nil {
		t.Error("expected last_sync_age_s")
	}
}

func TestHandleBuilderNotConfigured(t *testing.T) {
	s := NewServer(":0", &mockAppState{}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/builder", nil)
	w := httptest.NewRecorder()
	s.handleBuilder(w, req)

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "not_configured" {
		t.Errorf("expected status=not_configured, got %v", resp["status"])
	}
	if resp["configured"] != false {
		t.Errorf("expected configured=false, got %v", resp["configured"])
	}
	if int(resp["daily_volume_count"].(float64)) != 0 {
		t.Errorf("expected daily_volume_count=0, got %v", resp["daily_volume_count"])
	}
	if int(resp["leaderboard_count"].(float64)) != 0 {
		t.Errorf("expected leaderboard_count=0, got %v", resp["leaderboard_count"])
	}
}

func TestHandleMarkets(t *testing.T) {
	state := &mockAppState{assets: []string{"a1", "a2", "a3"}}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/markets", nil)
	w := httptest.NewRecorder()
	s.handleMarkets(w, req)

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if int(resp["count"].(float64)) != 3 {
		t.Errorf("expected count=3, got %v", resp["count"])
	}
}

func TestHandleRisk(t *testing.T) {
	state := &mockAppState{
		riskSnapshot: risk.Snapshot{
			DailyPnL:             -10,
			DailyLossLimitUSDC:   20,
			ConsecutiveLosses:    2,
			MaxConsecutiveLosses: 3,
			InCooldown:           true,
			CooldownRemaining:    90 * time.Second,
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/risk", nil)
	w := httptest.NewRecorder()
	s.handleRisk(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["daily_loss_limit_usdc"].(float64) != 20 {
		t.Fatalf("expected daily_loss_limit_usdc=20, got %v", resp["daily_loss_limit_usdc"])
	}
	if resp["daily_loss_remaining_usdc"].(float64) != 10 {
		t.Fatalf("expected daily_loss_remaining_usdc=10, got %v", resp["daily_loss_remaining_usdc"])
	}
	if resp["daily_loss_remaining_pct"].(float64) != 50 {
		t.Fatalf("expected daily_loss_remaining_pct=50, got %v", resp["daily_loss_remaining_pct"])
	}
	if resp["in_cooldown"].(bool) != true {
		t.Fatalf("expected in_cooldown=true, got %v", resp["in_cooldown"])
	}
	if resp["can_trade"].(bool) != false {
		t.Fatalf("expected can_trade=false, got %v", resp["can_trade"])
	}
	reasons, ok := resp["blocked_reasons"].([]interface{})
	if !ok {
		t.Fatalf("expected blocked_reasons array, got %T", resp["blocked_reasons"])
	}
	if len(reasons) != 1 || reasons[0] != "loss_cooldown_active" {
		t.Fatalf("expected blocked_reasons=[loss_cooldown_active], got %v", reasons)
	}
}

func TestHandleRiskBlockedReasonsMultiple(t *testing.T) {
	state := &mockAppState{
		riskSnapshot: risk.Snapshot{
			EmergencyStop:      true,
			DailyPnL:           -25,
			DailyLossLimitUSDC: 20,
			InCooldown:         true,
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/risk", nil)
	w := httptest.NewRecorder()
	s.handleRisk(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp["can_trade"].(bool) != false {
		t.Fatalf("expected can_trade=false, got %v", resp["can_trade"])
	}
	reasons, ok := resp["blocked_reasons"].([]interface{})
	if !ok {
		t.Fatalf("expected blocked_reasons array, got %T", resp["blocked_reasons"])
	}
	if len(reasons) != 3 {
		t.Fatalf("expected 3 blocked reasons, got %v", reasons)
	}
	if reasons[0] != "emergency_stop" || reasons[1] != "daily_loss_limit_reached" || reasons[2] != "loss_cooldown_active" {
		t.Fatalf("unexpected blocked_reasons order/content: %v", reasons)
	}
}

func TestHandlePaper(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		pnl:         2.0,
		unrealPnL:   1.5,
		paperSnapshot: paper.Snapshot{
			InitialBalanceUSDC: 1000,
			BalanceUSDC:        995.5,
			FeesPaidUSDC:       0.5,
			AllowShort:         false,
			InventoryByAsset: map[string]float64{
				"asset-1": 12.5,
			},
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/paper", nil)
	w := httptest.NewRecorder()
	s.handlePaper(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["trading_mode"] != "paper" {
		t.Fatalf("expected trading_mode paper, got %v", resp["trading_mode"])
	}
	if resp["initial_balance_usdc"].(float64) != 1000 {
		t.Fatalf("expected initial balance 1000, got %v", resp["initial_balance_usdc"])
	}
	if resp["allow_short"].(bool) != false {
		t.Fatalf("expected allow_short false, got %v", resp["allow_short"])
	}
	inv, ok := resp["inventory_by_asset"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected inventory_by_asset object, got %T", resp["inventory_by_asset"])
	}
	if inv["asset-1"].(float64) != 12.5 {
		t.Fatalf("expected inventory asset-1=12.5, got %v", inv["asset-1"])
	}
	if resp["realized_pnl_usdc"].(float64) != 2.0 {
		t.Fatalf("expected realized_pnl_usdc 2.0, got %v", resp["realized_pnl_usdc"])
	}
	if resp["unrealized_pnl_usdc"].(float64) != 1.5 {
		t.Fatalf("expected unrealized_pnl_usdc 1.5, got %v", resp["unrealized_pnl_usdc"])
	}
	// 1000 + 2.0 + 1.5 - 0.5 = 1003.0
	if resp["estimated_equity_usdc"].(float64) != 1003.0 {
		t.Fatalf("expected estimated_equity_usdc 1003.0, got %v", resp["estimated_equity_usdc"])
	}
}
