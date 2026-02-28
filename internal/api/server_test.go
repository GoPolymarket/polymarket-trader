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
	lastSync time.Time
}

func (m *mockBuilder) DailyVolumeJSON() interface{} { return []string{} }
func (m *mockBuilder) LeaderboardJSON() interface{} { return []string{} }
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
	builder := &mockBuilder{lastSync: time.Now()}
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
	if resp["in_cooldown"].(bool) != true {
		t.Fatalf("expected in_cooldown=true, got %v", resp["in_cooldown"])
	}
}

func TestHandlePaper(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		paperSnapshot: paper.Snapshot{
			InitialBalanceUSDC: 1000,
			BalanceUSDC:        995.5,
			FeesPaidUSDC:       0.5,
			AllowShort:         false,
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
}
