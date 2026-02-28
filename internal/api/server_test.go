package api

import (
	"encoding/csv"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestHandlePerfPaper(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		orders:      1,
		fills:       4,
		pnl:         2.0,
		unrealPnL:   1.5,
		paperSnapshot: paper.Snapshot{
			InitialBalanceUSDC: 1000,
			FeesPaidUSDC:       0.5,
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/perf", nil)
	w := httptest.NewRecorder()
	s.handlePerf(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["trading_mode"] != "paper" {
		t.Fatalf("expected trading_mode=paper, got %v", resp["trading_mode"])
	}
	if int(resp["fills"].(float64)) != 4 {
		t.Fatalf("expected fills=4, got %v", resp["fills"])
	}
	if resp["total_pnl_usdc"].(float64) != 3.5 {
		t.Fatalf("expected total_pnl_usdc=3.5, got %v", resp["total_pnl_usdc"])
	}
	if resp["pnl_per_fill_usdc"].(float64) != 0.875 {
		t.Fatalf("expected pnl_per_fill_usdc=0.875, got %v", resp["pnl_per_fill_usdc"])
	}
	if resp["fees_paid_usdc"].(float64) != 0.5 {
		t.Fatalf("expected fees_paid_usdc=0.5, got %v", resp["fees_paid_usdc"])
	}
	if resp["net_pnl_after_fees_usdc"].(float64) != 3.0 {
		t.Fatalf("expected net_pnl_after_fees_usdc=3.0, got %v", resp["net_pnl_after_fees_usdc"])
	}
	if resp["estimated_equity_usdc"].(float64) != 1003.0 {
		t.Fatalf("expected estimated_equity_usdc=1003.0, got %v", resp["estimated_equity_usdc"])
	}
}

func TestHandlePerfLive(t *testing.T) {
	state := &mockAppState{
		tradingMode: "live",
		fills:       2,
		pnl:         5.0,
		unrealPnL:   -1.0,
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/perf", nil)
	w := httptest.NewRecorder()
	s.handlePerf(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["trading_mode"] != "live" {
		t.Fatalf("expected trading_mode=live, got %v", resp["trading_mode"])
	}
	if resp["estimated_equity_usdc"] != nil {
		t.Fatalf("expected estimated_equity_usdc=nil in live mode, got %v", resp["estimated_equity_usdc"])
	}
}

func TestHandleGrantReport(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       3,
		pnl:         1.2,
		unrealPnL:   -0.2,
		riskSnapshot: risk.Snapshot{
			DailyPnL:             -5,
			DailyLossLimitUSDC:   20,
			ConsecutiveLosses:    1,
			MaxConsecutiveLosses: 3,
		},
		paperSnapshot: paper.Snapshot{
			InitialBalanceUSDC: 1000,
			FeesPaidUSDC:       0.1,
		},
	}
	builder := &mockBuilder{
		lastSync:    time.Now().Add(-5 * time.Minute),
		dailyVolume: []string{"v1", "v2", "v3"},
		leaderboard: []string{"l1"},
	}
	s := NewServer(":0", state, nil, builder)

	req := httptest.NewRequest(http.MethodGet, "/api/grant-report", nil)
	w := httptest.NewRecorder()
	s.handleGrantReport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["generated_at"] == nil {
		t.Fatal("expected generated_at")
	}
	if resp["trading_mode"] != "paper" {
		t.Fatalf("expected trading_mode paper, got %v", resp["trading_mode"])
	}

	perf, ok := resp["performance"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected performance object, got %T", resp["performance"])
	}
	if int(perf["fills"].(float64)) != 3 {
		t.Fatalf("expected performance.fills=3, got %v", perf["fills"])
	}

	builderObj, ok := resp["builder"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected builder object, got %T", resp["builder"])
	}
	if builderObj["configured"] != true {
		t.Fatalf("expected builder.configured=true, got %v", builderObj["configured"])
	}
	if int(builderObj["daily_volume_count"].(float64)) != 3 {
		t.Fatalf("expected builder.daily_volume_count=3, got %v", builderObj["daily_volume_count"])
	}

	riskObj, ok := resp["risk"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected risk object, got %T", resp["risk"])
	}
	if riskObj["can_trade"] != true {
		t.Fatalf("expected risk.can_trade=true, got %v", riskObj["can_trade"])
	}

	readiness, ok := resp["readiness"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected readiness object, got %T", resp["readiness"])
	}
	if readiness["builder_fresh"] != true {
		t.Fatalf("expected readiness.builder_fresh=true, got %v", readiness["builder_fresh"])
	}
	if readiness["risk_tradable"] != true {
		t.Fatalf("expected readiness.risk_tradable=true, got %v", readiness["risk_tradable"])
	}
	if readiness["has_trading_activity"] != true {
		t.Fatalf("expected readiness.has_trading_activity=true, got %v", readiness["has_trading_activity"])
	}
	if int(readiness["score"].(float64)) != 100 {
		t.Fatalf("expected readiness.score=100, got %v", readiness["score"])
	}
}

func TestHandleGrantReportCSV(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       2,
		pnl:         1.0,
		unrealPnL:   0.5,
		riskSnapshot: risk.Snapshot{
			DailyPnL:           -1,
			DailyLossLimitUSDC: 20,
		},
		paperSnapshot: paper.Snapshot{
			InitialBalanceUSDC: 1000,
			FeesPaidUSDC:       0.1,
		},
	}
	builder := &mockBuilder{
		lastSync:    time.Now().Add(-2 * time.Minute),
		dailyVolume: []string{"v1"},
		leaderboard: []string{"l1"},
	}
	s := NewServer(":0", state, nil, builder)

	req := httptest.NewRequest(http.MethodGet, "/api/grant-report?format=csv", nil)
	w := httptest.NewRecorder()
	s.handleGrantReport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); !strings.Contains(got, "text/csv") {
		t.Fatalf("expected text/csv content type, got %q", got)
	}

	rows, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 csv rows (header+data), got %d", len(rows))
	}
	header := rows[0]
	row := rows[1]
	if len(header) != len(row) {
		t.Fatalf("csv header/data length mismatch: %d vs %d", len(header), len(row))
	}

	col := make(map[string]string, len(header))
	for i, k := range header {
		col[k] = row[i]
	}
	if col["trading_mode"] != "paper" {
		t.Fatalf("expected trading_mode=paper, got %q", col["trading_mode"])
	}
	if col["fills"] != "2" {
		t.Fatalf("expected fills=2, got %q", col["fills"])
	}
	if col["builder_daily_volume_count"] != "1" {
		t.Fatalf("expected builder_daily_volume_count=1, got %q", col["builder_daily_volume_count"])
	}
	if col["risk_can_trade"] != "true" {
		t.Fatalf("expected risk_can_trade=true, got %q", col["risk_can_trade"])
	}
	if col["readiness_score"] != "100" {
		t.Fatalf("expected readiness_score=100, got %q", col["readiness_score"])
	}
}

func TestHandleStageReportJSON(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       18,
		pnl:         4.5,
		unrealPnL:   -0.5,
		riskSnapshot: risk.Snapshot{
			DailyPnL:             -2,
			DailyLossLimitUSDC:   20,
			ConsecutiveLosses:    0,
			MaxConsecutiveLosses: 3,
		},
		paperSnapshot: paper.Snapshot{
			InitialBalanceUSDC: 1000,
			FeesPaidUSDC:       0.6,
			TotalVolumeUSDC:    300,
			TotalTrades:        18,
		},
		positions: map[string]execution.Position{
			"asset-1": {AssetID: "asset-1", RealizedPnL: 3.0, TotalFills: 10},
			"asset-2": {AssetID: "asset-2", RealizedPnL: -0.8, TotalFills: 8},
		},
	}
	builder := &mockBuilder{
		lastSync:    time.Now().Add(-5 * time.Minute),
		dailyVolume: []string{"v1", "v2", "v3"},
		leaderboard: []string{"l1"},
	}
	s := NewServer(":0", state, nil, builder)

	req := httptest.NewRequest(http.MethodGet, "/api/stage-report", nil)
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["generated_at"] == nil {
		t.Fatal("expected generated_at")
	}
	if resp["trading_mode"] != "paper" {
		t.Fatalf("expected trading_mode=paper, got %v", resp["trading_mode"])
	}

	scorecard, ok := resp["scorecard"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected scorecard object, got %T", resp["scorecard"])
	}
	if scorecard["grant_readiness_score"].(float64) <= 0 {
		t.Fatalf("expected positive grant_readiness_score, got %v", scorecard["grant_readiness_score"])
	}
	if scorecard["builder_fresh"] != true {
		t.Fatalf("expected builder_fresh=true, got %v", scorecard["builder_fresh"])
	}

	kpis, ok := resp["kpis"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected kpis object, got %T", resp["kpis"])
	}
	if int(kpis["fills"].(float64)) != 18 {
		t.Fatalf("expected fills=18, got %v", kpis["fills"])
	}
	if kpis["net_edge_bps"] == nil {
		t.Fatal("expected net_edge_bps")
	}

	narrative, ok := resp["narrative"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected narrative object, got %T", resp["narrative"])
	}
	strengths, ok := narrative["strengths"].([]interface{})
	if !ok || len(strengths) == 0 {
		t.Fatalf("expected non-empty strengths, got %v", narrative["strengths"])
	}

	evidence, ok := resp["evidence"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected evidence object, got %T", resp["evidence"])
	}
	if evidence["evidence_id"] == "" {
		t.Fatal("expected evidence_id")
	}
	if evidence["checksum_sha256"] == "" {
		t.Fatal("expected checksum_sha256")
	}
	if evidence["checksum_generated_at"] == nil {
		t.Fatal("expected checksum_generated_at")
	}
}

func TestHandleStageReportMarkdown(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       12,
		pnl:         1.5,
		unrealPnL:   0,
		riskSnapshot: risk.Snapshot{
			DailyPnL:           -1,
			DailyLossLimitUSDC: 20,
		},
		paperSnapshot: paper.Snapshot{
			FeesPaidUSDC:    0.2,
			TotalVolumeUSDC: 180,
			TotalTrades:     12,
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/stage-report?format=markdown", nil)
	w := httptest.NewRecorder()
	s.handleStageReport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); !strings.Contains(got, "text/markdown") {
		t.Fatalf("expected markdown content type, got %q", got)
	}
	body := w.Body.String()
	if !strings.Contains(body, "# Polymarket Trader Stage Report") {
		t.Fatalf("expected markdown title, got %q", body)
	}
	if !strings.Contains(body, "Grant Readiness Score:") {
		t.Fatalf("expected readiness line, got %q", body)
	}
	if !strings.Contains(body, "Evidence ID:") {
		t.Fatalf("expected evidence id line, got %q", body)
	}
}

func TestHandleStageReportWindowOverride(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       8,
		pnl:         1.0,
		riskSnapshot: risk.Snapshot{
			DailyPnL:           -1,
			DailyLossLimitUSDC: 20,
		},
		paperSnapshot: paper.Snapshot{
			FeesPaidUSDC:    0.1,
			TotalVolumeUSDC: 80,
			TotalTrades:     8,
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/stage-report?window=30d", nil)
	w := httptest.NewRecorder()
	s.handleStageReport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	window, ok := resp["window"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected window object, got %T", resp["window"])
	}
	if window["label"] != "30d" {
		t.Fatalf("expected window.label=30d, got %v", window["label"])
	}
	if int(window["days"].(float64)) != 30 {
		t.Fatalf("expected window.days=30, got %v", window["days"])
	}
}

func TestHandleStageReportCSV(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       10,
		pnl:         2,
		unrealPnL:   0.5,
		riskSnapshot: risk.Snapshot{
			DailyPnL:           -1,
			DailyLossLimitUSDC: 20,
		},
		paperSnapshot: paper.Snapshot{
			FeesPaidUSDC:    0.2,
			TotalVolumeUSDC: 150,
			TotalTrades:     10,
		},
	}
	builder := &mockBuilder{
		lastSync:    time.Now().Add(-2 * time.Minute),
		dailyVolume: []string{"v1"},
		leaderboard: []string{"l1"},
	}
	s := NewServer(":0", state, nil, builder)

	req := httptest.NewRequest(http.MethodGet, "/api/stage-report?format=csv&window=30d", nil)
	w := httptest.NewRecorder()
	s.handleStageReport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); !strings.Contains(got, "text/csv") {
		t.Fatalf("expected text/csv content type, got %q", got)
	}

	rows, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 csv rows, got %d", len(rows))
	}
	header := rows[0]
	row := rows[1]
	if len(header) != len(row) {
		t.Fatalf("csv header/data mismatch: %d vs %d", len(header), len(row))
	}
	col := make(map[string]string, len(header))
	for i, k := range header {
		col[k] = row[i]
	}
	if col["window_days"] != "30" {
		t.Fatalf("expected window_days=30, got %q", col["window_days"])
	}
	if col["trading_mode"] != "paper" {
		t.Fatalf("expected trading_mode=paper, got %q", col["trading_mode"])
	}
	if col["builder_fresh"] != "true" {
		t.Fatalf("expected builder_fresh=true, got %q", col["builder_fresh"])
	}
	if col["grant_readiness_score"] == "" {
		t.Fatal("expected grant_readiness_score")
	}
	if col["evidence_id"] == "" {
		t.Fatal("expected evidence_id")
	}
	if col["checksum_sha256"] == "" {
		t.Fatal("expected checksum_sha256")
	}
}

func TestHandleGrantPackageJSON(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       20,
		pnl:         5.0,
		unrealPnL:   -1.0,
		riskSnapshot: risk.Snapshot{
			DailyPnL:           -2,
			DailyLossLimitUSDC: 25,
		},
		paperSnapshot: paper.Snapshot{
			FeesPaidUSDC:    0.5,
			TotalVolumeUSDC: 320,
			TotalTrades:     20,
		},
	}
	builder := &mockBuilder{
		lastSync:    time.Now().Add(-3 * time.Minute),
		dailyVolume: []string{"v1", "v2"},
		leaderboard: []string{"l1"},
	}
	s := NewServer(":0", state, nil, builder)

	req := httptest.NewRequest(http.MethodGet, "/api/grant-package?window=30d", nil)
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["package_id"] == "" {
		t.Fatal("expected package_id")
	}
	window, ok := resp["window"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected window object, got %T", resp["window"])
	}
	if window["label"] != "30d" {
		t.Fatalf("expected window.label=30d, got %v", window["label"])
	}

	artifacts, ok := resp["artifacts"].([]interface{})
	if !ok || len(artifacts) == 0 {
		t.Fatalf("expected non-empty artifacts list, got %v", resp["artifacts"])
	}
	if !containsArtifactName(artifacts, "stage_report_markdown") {
		t.Fatalf("expected stage_report_markdown artifact, got %v", artifacts)
	}
	if !containsArtifactName(artifacts, "grant_report_csv") {
		t.Fatalf("expected grant_report_csv artifact, got %v", artifacts)
	}

	milestones, ok := resp["milestones"].([]interface{})
	if !ok || len(milestones) == 0 {
		t.Fatalf("expected milestones list, got %v", resp["milestones"])
	}

	manifest, ok := resp["manifest"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected manifest object, got %T", resp["manifest"])
	}
	if manifest["checksum_sha256"] == "" {
		t.Fatal("expected manifest.checksum_sha256")
	}
}

func TestHandleGrantPackageMarkdown(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       10,
		pnl:         2.0,
		unrealPnL:   0.2,
		riskSnapshot: risk.Snapshot{
			DailyPnL:           -1,
			DailyLossLimitUSDC: 20,
		},
		paperSnapshot: paper.Snapshot{
			FeesPaidUSDC:    0.2,
			TotalVolumeUSDC: 120,
			TotalTrades:     10,
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/grant-package?format=markdown", nil)
	w := httptest.NewRecorder()
	s.handleGrantPackage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); !strings.Contains(got, "text/markdown") {
		t.Fatalf("expected markdown content type, got %q", got)
	}
	body := w.Body.String()
	if !strings.Contains(body, "# Polymarket Grant Submission Package") {
		t.Fatalf("expected markdown title, got %q", body)
	}
	if !strings.Contains(body, "Package ID:") {
		t.Fatalf("expected package id line, got %q", body)
	}
	if !strings.Contains(body, "## Artifacts") {
		t.Fatalf("expected artifacts section, got %q", body)
	}
}

func TestHandleTelegramTemplates(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       22,
		pnl:         4.0,
		unrealPnL:   -0.5,
		riskSnapshot: risk.Snapshot{
			DailyPnL:             -2.0,
			DailyLossLimitUSDC:   25.0,
			ConsecutiveLosses:    0,
			MaxConsecutiveLosses: 3,
		},
		paperSnapshot: paper.Snapshot{
			FeesPaidUSDC:    0.4,
			TotalVolumeUSDC: 300.0,
			TotalTrades:     22,
		},
		positions: map[string]execution.Position{
			"asset-a": {AssetID: "asset-a", RealizedPnL: 3.2, TotalFills: 12},
			"asset-b": {AssetID: "asset-b", RealizedPnL: -0.8, TotalFills: 10},
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/telegram-templates?window=7d", nil)
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	daily, ok := resp["daily_template"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected daily_template object, got %T", resp["daily_template"])
	}
	if daily["can_trade"] != true {
		t.Fatalf("expected daily.can_trade=true, got %v", daily["can_trade"])
	}
	textDaily, _ := daily["text_html"].(string)
	if !strings.Contains(textDaily, "Daily Trading Coach") {
		t.Fatalf("expected daily template title, got %q", textDaily)
	}

	weekly, ok := resp["weekly_template"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected weekly_template object, got %T", resp["weekly_template"])
	}
	textWeekly, _ := weekly["text_html"].(string)
	if !strings.Contains(textWeekly, "Weekly Trading Review") {
		t.Fatalf("expected weekly template title, got %q", textWeekly)
	}
}

func TestHandleTelegramTemplatesRiskBlocked(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       8,
		pnl:         -1.0,
		riskSnapshot: risk.Snapshot{
			EmergencyStop:      true,
			DailyPnL:           -15.0,
			DailyLossLimitUSDC: 10.0,
			InCooldown:         true,
		},
		paperSnapshot: paper.Snapshot{
			FeesPaidUSDC:    0.3,
			TotalVolumeUSDC: 120.0,
			TotalTrades:     8,
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/telegram-templates", nil)
	w := httptest.NewRecorder()
	s.handleTelegramTemplates(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	daily := resp["daily_template"].(map[string]interface{})
	if daily["can_trade"] != false {
		t.Fatalf("expected daily.can_trade=false, got %v", daily["can_trade"])
	}
	textDaily, _ := daily["text_html"].(string)
	if !strings.Contains(strings.ToUpper(textDaily), "PAUSE") {
		t.Fatalf("expected pause warning in daily template, got %q", textDaily)
	}
}

func TestHandleSizingNormal(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       25,
		pnl:         6.0,
		unrealPnL:   0.0,
		riskSnapshot: risk.Snapshot{
			DailyPnL:             -2.0,
			DailyLossLimitUSDC:   20.0,
			ConsecutiveLosses:    0,
			MaxConsecutiveLosses: 3,
		},
		paperSnapshot: paper.Snapshot{
			FeesPaidUSDC:    0.4,
			TotalVolumeUSDC: 280.0,
			TotalTrades:     25,
		},
		positions: map[string]execution.Position{
			"asset-top":  {AssetID: "asset-top", RealizedPnL: 4.0, TotalFills: 12},
			"asset-mid":  {AssetID: "asset-mid", RealizedPnL: 1.0, TotalFills: 8},
			"asset-loss": {AssetID: "asset-loss", RealizedPnL: -0.8, TotalFills: 5},
		},
		recentFills: []execution.Fill{
			{AssetID: "asset-top", Side: "BUY", Price: 0.50, Size: 20},  // 10 usdc
			{AssetID: "asset-mid", Side: "SELL", Price: 0.40, Size: 15}, // 6 usdc
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sizing", nil)
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["can_trade"] != true {
		t.Fatalf("expected can_trade=true, got %v", resp["can_trade"])
	}
	if resp["risk_mode"] != "normal" {
		t.Fatalf("expected risk_mode=normal, got %v", resp["risk_mode"])
	}

	budget, ok := resp["budget"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected budget object, got %T", resp["budget"])
	}
	if budget["risk_budget_usdc"].(float64) <= 0 {
		t.Fatalf("expected positive risk_budget_usdc, got %v", budget["risk_budget_usdc"])
	}
	if budget["suggested_max_order_usdc"].(float64) <= 0 {
		t.Fatalf("expected positive suggested_max_order_usdc, got %v", budget["suggested_max_order_usdc"])
	}

	allocation, ok := resp["allocation"].([]interface{})
	if !ok || len(allocation) == 0 {
		t.Fatalf("expected non-empty allocation, got %v", resp["allocation"])
	}
	top := allocation[0].(map[string]interface{})
	if top["asset_id"] != "asset-top" {
		t.Fatalf("expected top allocation asset-top, got %v", top["asset_id"])
	}

	actions, ok := resp["actions"].([]interface{})
	if !ok {
		t.Fatalf("expected actions list, got %T", resp["actions"])
	}
	if !containsActionCode(actions, "focus_high_score_markets") {
		t.Fatalf("expected focus_high_score_markets action, got %v", actions)
	}
}

func TestHandleSizingDefensive(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       12,
		pnl:         -1.0,
		unrealPnL:   0,
		riskSnapshot: risk.Snapshot{
			DailyPnL:             -18.0,
			DailyLossLimitUSDC:   20.0,
			ConsecutiveLosses:    1,
			MaxConsecutiveLosses: 3,
		},
		paperSnapshot: paper.Snapshot{
			FeesPaidUSDC:    0.3,
			TotalVolumeUSDC: 150.0,
			TotalTrades:     12,
		},
		positions: map[string]execution.Position{
			"asset-a": {AssetID: "asset-a", RealizedPnL: 0.5, TotalFills: 7},
			"asset-b": {AssetID: "asset-b", RealizedPnL: -1.2, TotalFills: 5},
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sizing", nil)
	w := httptest.NewRecorder()
	s.handleSizing(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["risk_mode"] != "defensive" {
		t.Fatalf("expected risk_mode=defensive, got %v", resp["risk_mode"])
	}
	if resp["size_multiplier"].(float64) != 0.5 {
		t.Fatalf("expected size_multiplier=0.5, got %v", resp["size_multiplier"])
	}
	actions := resp["actions"].([]interface{})
	if !containsActionCode(actions, "reduce_size") {
		t.Fatalf("expected reduce_size action, got %v", actions)
	}
}

func TestHandleSizingPaused(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       8,
		pnl:         1.0,
		riskSnapshot: risk.Snapshot{
			EmergencyStop:        true,
			DailyPnL:             -30.0,
			DailyLossLimitUSDC:   20.0,
			ConsecutiveLosses:    3,
			MaxConsecutiveLosses: 3,
			InCooldown:           true,
			CooldownRemaining:    3 * time.Minute,
		},
		paperSnapshot: paper.Snapshot{
			FeesPaidUSDC:    0.2,
			TotalVolumeUSDC: 100,
			TotalTrades:     8,
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sizing", nil)
	w := httptest.NewRecorder()
	s.handleSizing(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["can_trade"] != false {
		t.Fatalf("expected can_trade=false, got %v", resp["can_trade"])
	}
	if resp["risk_mode"] != "pause" {
		t.Fatalf("expected risk_mode=pause, got %v", resp["risk_mode"])
	}
	budget := resp["budget"].(map[string]interface{})
	if budget["risk_budget_usdc"].(float64) != 0 {
		t.Fatalf("expected risk_budget_usdc=0, got %v", budget["risk_budget_usdc"])
	}
	actions := resp["actions"].([]interface{})
	if !containsActionCode(actions, "pause_trading") {
		t.Fatalf("expected pause_trading action, got %v", actions)
	}
}

func TestHandleCoachNormal(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       30,
		pnl:         12.0,
		unrealPnL:   -2.0,
		riskSnapshot: risk.Snapshot{
			DailyPnL:             -1.0,
			DailyLossLimitUSDC:   20.0,
			ConsecutiveLosses:    0,
			MaxConsecutiveLosses: 3,
		},
		positions: map[string]execution.Position{
			"asset-win":  {AssetID: "asset-win", RealizedPnL: 4.0, TotalFills: 10},
			"asset-loss": {AssetID: "asset-loss", RealizedPnL: -1.0, TotalFills: 6},
		},
		recentFills: []execution.Fill{
			{AssetID: "asset-win", Side: "BUY", Price: 0.60, Size: 20},
			{AssetID: "asset-loss", Side: "BUY", Price: 0.40, Size: 10},
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/coach", nil)
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["can_trade"] != true {
		t.Fatalf("expected can_trade=true, got %v", resp["can_trade"])
	}

	marketStats, ok := resp["market_stats"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected market_stats object, got %T", resp["market_stats"])
	}
	if int(marketStats["profitable_markets"].(float64)) != 1 {
		t.Fatalf("expected profitable_markets=1, got %v", marketStats["profitable_markets"])
	}
	if int(marketStats["losing_markets"].(float64)) != 1 {
		t.Fatalf("expected losing_markets=1, got %v", marketStats["losing_markets"])
	}

	sizing, ok := resp["sizing"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected sizing object, got %T", resp["sizing"])
	}
	if sizing["risk_mode"] != "normal" {
		t.Fatalf("expected sizing.risk_mode=normal, got %v", sizing["risk_mode"])
	}
	if sizing["size_multiplier"].(float64) != 1.0 {
		t.Fatalf("expected sizing.size_multiplier=1.0, got %v", sizing["size_multiplier"])
	}
	if sizing["suggested_max_order_usdc"].(float64) <= 0 {
		t.Fatalf("expected positive suggested_max_order_usdc, got %v", sizing["suggested_max_order_usdc"])
	}

	actions, ok := resp["actions"].([]interface{})
	if !ok {
		t.Fatalf("expected actions list, got %T", resp["actions"])
	}
	if !containsActionCode(actions, "focus_profitable_markets") {
		t.Fatalf("expected focus_profitable_markets action, got %v", actions)
	}
}

func TestHandleCoachDefensiveMode(t *testing.T) {
	state := &mockAppState{
		fills: 10,
		pnl:   -2.0,
		riskSnapshot: risk.Snapshot{
			DailyPnL:             -18.0,
			DailyLossLimitUSDC:   20.0,
			ConsecutiveLosses:    1,
			MaxConsecutiveLosses: 3,
		},
		recentFills: []execution.Fill{
			{AssetID: "asset-1", Side: "BUY", Price: 0.50, Size: 10},
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/coach", nil)
	w := httptest.NewRecorder()
	s.handleCoach(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	sizing, ok := resp["sizing"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected sizing object, got %T", resp["sizing"])
	}
	if sizing["risk_mode"] != "defensive" {
		t.Fatalf("expected sizing.risk_mode=defensive, got %v", sizing["risk_mode"])
	}
	if sizing["size_multiplier"].(float64) != 0.5 {
		t.Fatalf("expected sizing.size_multiplier=0.5, got %v", sizing["size_multiplier"])
	}

	actions, ok := resp["actions"].([]interface{})
	if !ok {
		t.Fatalf("expected actions list, got %T", resp["actions"])
	}
	if !containsActionCode(actions, "reduce_size") {
		t.Fatalf("expected reduce_size action, got %v", actions)
	}
}

func TestHandleCoachPausedByRisk(t *testing.T) {
	state := &mockAppState{
		fills: 5,
		riskSnapshot: risk.Snapshot{
			EmergencyStop:        true,
			DailyPnL:             -25.0,
			DailyLossLimitUSDC:   20.0,
			ConsecutiveLosses:    3,
			MaxConsecutiveLosses: 3,
			InCooldown:           true,
			CooldownRemaining:    5 * time.Minute,
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/coach", nil)
	w := httptest.NewRecorder()
	s.handleCoach(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["can_trade"] != false {
		t.Fatalf("expected can_trade=false, got %v", resp["can_trade"])
	}

	sizing, ok := resp["sizing"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected sizing object, got %T", resp["sizing"])
	}
	if sizing["risk_mode"] != "pause" {
		t.Fatalf("expected sizing.risk_mode=pause, got %v", sizing["risk_mode"])
	}
	if sizing["size_multiplier"].(float64) != 0.0 {
		t.Fatalf("expected sizing.size_multiplier=0, got %v", sizing["size_multiplier"])
	}
	if sizing["suggested_max_order_usdc"].(float64) != 0.0 {
		t.Fatalf("expected suggested_max_order_usdc=0, got %v", sizing["suggested_max_order_usdc"])
	}

	actions, ok := resp["actions"].([]interface{})
	if !ok {
		t.Fatalf("expected actions list, got %T", resp["actions"])
	}
	if !containsActionCode(actions, "pause_trading") {
		t.Fatalf("expected pause_trading action, got %v", actions)
	}
}

func containsActionCode(actions []interface{}, code string) bool {
	for _, raw := range actions {
		obj, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if obj["code"] == code {
			return true
		}
	}
	return false
}

func containsArtifactName(artifacts []interface{}, name string) bool {
	for _, raw := range artifacts {
		obj, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if obj["name"] == name {
			return true
		}
	}
	return false
}

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

func TestHandleInsightsScores(t *testing.T) {
	state := &mockAppState{
		fills:     22,
		pnl:       6.0,
		unrealPnL: -1.0,
		riskSnapshot: risk.Snapshot{
			DailyPnL:             -3.0,
			DailyLossLimitUSDC:   30.0,
			ConsecutiveLosses:    1,
			MaxConsecutiveLosses: 3,
		},
		positions: map[string]execution.Position{
			"asset-alpha": {AssetID: "asset-alpha", RealizedPnL: 5.0, TotalFills: 10},
			"asset-beta":  {AssetID: "asset-beta", RealizedPnL: -2.5, TotalFills: 8},
			"asset-gamma": {AssetID: "asset-gamma", RealizedPnL: 0.5, TotalFills: 4},
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/insights", nil)
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp["generated_at"] == nil {
		t.Fatal("expected generated_at")
	}
	if resp["can_trade"] != true {
		t.Fatalf("expected can_trade=true, got %v", resp["can_trade"])
	}

	scores, ok := resp["market_scores"].([]interface{})
	if !ok {
		t.Fatalf("expected market_scores list, got %T", resp["market_scores"])
	}
	if len(scores) != 3 {
		t.Fatalf("expected 3 market scores, got %d", len(scores))
	}

	first := scores[0].(map[string]interface{})
	if first["asset_id"] != "asset-alpha" {
		t.Fatalf("expected top asset asset-alpha, got %v", first["asset_id"])
	}
	if first["bucket"] != "focus" {
		t.Fatalf("expected top bucket focus, got %v", first["bucket"])
	}
	if first["score"].(float64) <= 0 {
		t.Fatalf("expected positive score, got %v", first["score"])
	}

	recs, ok := resp["recommendations"].([]interface{})
	if !ok {
		t.Fatalf("expected recommendations list, got %T", resp["recommendations"])
	}
	if !containsActionCode(recs, "focus_top_market") {
		t.Fatalf("expected focus_top_market recommendation, got %v", recs)
	}
	if !containsActionCode(recs, "deprioritize_worst_market") {
		t.Fatalf("expected deprioritize_worst_market recommendation, got %v", recs)
	}
}

func TestHandleInsightsNoData(t *testing.T) {
	state := &mockAppState{
		fills: 0,
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/insights", nil)
	w := httptest.NewRecorder()
	s.handleInsights(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	scores, ok := resp["market_scores"].([]interface{})
	if !ok {
		t.Fatalf("expected market_scores list, got %T", resp["market_scores"])
	}
	if len(scores) != 0 {
		t.Fatalf("expected empty market_scores, got %d", len(scores))
	}

	recs, ok := resp["recommendations"].([]interface{})
	if !ok {
		t.Fatalf("expected recommendations list, got %T", resp["recommendations"])
	}
	if !containsActionCode(recs, "collect_more_data") {
		t.Fatalf("expected collect_more_data recommendation, got %v", recs)
	}
}

func TestHandleInsightsRiskBlocked(t *testing.T) {
	state := &mockAppState{
		fills: 7,
		riskSnapshot: risk.Snapshot{
			EmergencyStop:      true,
			DailyPnL:           -12,
			DailyLossLimitUSDC: 10,
		},
		positions: map[string]execution.Position{
			"asset-a": {AssetID: "asset-a", RealizedPnL: 1.2, TotalFills: 7},
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/insights", nil)
	w := httptest.NewRecorder()
	s.handleInsights(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["can_trade"] != false {
		t.Fatalf("expected can_trade=false, got %v", resp["can_trade"])
	}
	recs, ok := resp["recommendations"].([]interface{})
	if !ok {
		t.Fatalf("expected recommendations list, got %T", resp["recommendations"])
	}
	if !containsActionCode(recs, "pause_trading") {
		t.Fatalf("expected pause_trading recommendation, got %v", recs)
	}
}

func TestHandleExecutionQualityPaper(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       10,
		pnl:         3.0,
		unrealPnL:   -1.0,
		riskSnapshot: risk.Snapshot{
			DailyPnL:           -2.0,
			DailyLossLimitUSDC: 20.0,
		},
		recentFills: []execution.Fill{
			{AssetID: "asset-a", Side: "BUY", Price: 0.50, Size: 20},  // 10 USDC
			{AssetID: "asset-b", Side: "SELL", Price: 0.25, Size: 20}, // 5 USDC
		},
		paperSnapshot: paper.Snapshot{
			FeesPaidUSDC:    0.5,
			TotalVolumeUSDC: 200.0,
			TotalTrades:     10,
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/execution-quality", nil)
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["trading_mode"] != "paper" {
		t.Fatalf("expected trading_mode=paper, got %v", resp["trading_mode"])
	}
	if resp["can_trade"] != true {
		t.Fatalf("expected can_trade=true, got %v", resp["can_trade"])
	}

	metrics, ok := resp["metrics"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected metrics object, got %T", resp["metrics"])
	}
	if !approxEqual(metrics["gross_edge_bps"].(float64), 100.0) {
		t.Fatalf("expected gross_edge_bps=100, got %v", metrics["gross_edge_bps"])
	}
	if !approxEqual(metrics["net_edge_bps"].(float64), 75.0) {
		t.Fatalf("expected net_edge_bps=75, got %v", metrics["net_edge_bps"])
	}
	if !approxEqual(metrics["fee_rate_bps"].(float64), 25.0) {
		t.Fatalf("expected fee_rate_bps=25, got %v", metrics["fee_rate_bps"])
	}
	if !approxEqual(metrics["friction_bps"].(float64), 25.0) {
		t.Fatalf("expected friction_bps=25, got %v", metrics["friction_bps"])
	}
	if !approxEqual(metrics["avg_fill_notional_usdc"].(float64), 7.5) {
		t.Fatalf("expected avg_fill_notional_usdc=7.5, got %v", metrics["avg_fill_notional_usdc"])
	}
	breakdown, ok := resp["breakdown"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected breakdown object, got %T", resp["breakdown"])
	}
	if !approxEqual(breakdown["fee_drag_bps"].(float64), 25.0) {
		t.Fatalf("expected fee_drag_bps=25, got %v", breakdown["fee_drag_bps"])
	}
	if !approxEqual(breakdown["slippage_proxy_bps"].(float64), 2.0) {
		t.Fatalf("expected slippage_proxy_bps=2, got %v", breakdown["slippage_proxy_bps"])
	}
	if !approxEqual(breakdown["selectivity_loss_bps"].(float64), 0.0) {
		t.Fatalf("expected selectivity_loss_bps=0, got %v", breakdown["selectivity_loss_bps"])
	}
	uplift, ok := resp["profit_uplift"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected profit_uplift object, got %T", resp["profit_uplift"])
	}
	lossUSDC, ok := uplift["estimated_loss_usdc"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected estimated_loss_usdc object, got %T", uplift["estimated_loss_usdc"])
	}
	if !approxEqual(lossUSDC["fee_drag_usdc"].(float64), 0.5) {
		t.Fatalf("expected fee_drag_usdc=0.5, got %v", lossUSDC["fee_drag_usdc"])
	}
	if uplift["priority_action_code"] != "reduce_fee_drag" {
		t.Fatalf("expected priority_action_code=reduce_fee_drag, got %v", uplift["priority_action_code"])
	}
	scenarios, ok := uplift["scenarios"].([]interface{})
	if !ok {
		t.Fatalf("expected scenarios list, got %T", uplift["scenarios"])
	}
	if len(scenarios) == 0 {
		t.Fatalf("expected non-empty scenarios, got %v", scenarios)
	}

	recs, ok := resp["recommendations"].([]interface{})
	if !ok {
		t.Fatalf("expected recommendations list, got %T", resp["recommendations"])
	}
	if !containsActionCode(recs, "edge_above_friction") {
		t.Fatalf("expected edge_above_friction recommendation, got %v", recs)
	}
}

func TestHandleExecutionQualityLowEdge(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       20,
		pnl:         0.2,
		unrealPnL:   0.0,
		riskSnapshot: risk.Snapshot{
			DailyPnL:           -1.0,
			DailyLossLimitUSDC: 20.0,
		},
		paperSnapshot: paper.Snapshot{
			FeesPaidUSDC:    0.6,
			TotalVolumeUSDC: 1000.0,
			TotalTrades:     20,
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/execution-quality", nil)
	w := httptest.NewRecorder()
	s.handleExecutionQuality(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	metrics := resp["metrics"].(map[string]interface{})
	if metrics["net_edge_bps"].(float64) >= 0 {
		t.Fatalf("expected negative net_edge_bps, got %v", metrics["net_edge_bps"])
	}
	breakdown, ok := resp["breakdown"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected breakdown object, got %T", resp["breakdown"])
	}
	if !approxEqual(breakdown["selectivity_loss_bps"].(float64), 4.0) {
		t.Fatalf("expected selectivity_loss_bps=4, got %v", breakdown["selectivity_loss_bps"])
	}
	if !approxEqual(breakdown["slippage_proxy_bps"].(float64), 4.25) {
		t.Fatalf("expected slippage_proxy_bps=4.25, got %v", breakdown["slippage_proxy_bps"])
	}
	uplift, ok := resp["profit_uplift"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected profit_uplift object, got %T", resp["profit_uplift"])
	}
	if uplift["priority_action_code"] == "" {
		t.Fatalf("expected priority_action_code, got %v", uplift["priority_action_code"])
	}

	recs, ok := resp["recommendations"].([]interface{})
	if !ok {
		t.Fatalf("expected recommendations list, got %T", resp["recommendations"])
	}
	if !containsActionCode(recs, "reduce_churn") {
		t.Fatalf("expected reduce_churn recommendation, got %v", recs)
	}
	if !containsActionCode(recs, "improve_selectivity") {
		t.Fatalf("expected improve_selectivity recommendation, got %v", recs)
	}
	if !containsActionCode(recs, "reduce_slippage") {
		t.Fatalf("expected reduce_slippage recommendation, got %v", recs)
	}
}

func TestHandleExecutionQualityRiskBlocked(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       5,
		pnl:         1.0,
		riskSnapshot: risk.Snapshot{
			EmergencyStop:      true,
			DailyPnL:           -15.0,
			DailyLossLimitUSDC: 10.0,
		},
		paperSnapshot: paper.Snapshot{
			FeesPaidUSDC:    0.1,
			TotalVolumeUSDC: 100.0,
			TotalTrades:     5,
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/execution-quality", nil)
	w := httptest.NewRecorder()
	s.handleExecutionQuality(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["can_trade"] != false {
		t.Fatalf("expected can_trade=false, got %v", resp["can_trade"])
	}
	recs := resp["recommendations"].([]interface{})
	if !containsActionCode(recs, "pause_trading") {
		t.Fatalf("expected pause_trading recommendation, got %v", recs)
	}
}

func TestBuildExecutionProfitUplift(t *testing.T) {
	uplift := buildExecutionProfitUplift(
		executionQualityMetrics{
			Fills:               25,
			VolumeUSDC:          1000,
			NetPnLAfterFeesUSDC: -3,
		},
		executionLossBreakdown{
			FeeDragBps:         6,
			SlippageProxyBps:   4.25,
			SelectivityLossBps: 4,
			AvoidableLossBps:   8.25,
			TotalLossBps:       14.25,
		},
	)
	if !approxEqual(uplift.EstimatedLossUSDC.FeeDragUSDC, 0.6) {
		t.Fatalf("expected fee drag 0.6, got %v", uplift.EstimatedLossUSDC.FeeDragUSDC)
	}
	if !approxEqual(uplift.EstimatedLossUSDC.SlippageProxyUSDC, 0.43) {
		t.Fatalf("expected slippage 0.43, got %v", uplift.EstimatedLossUSDC.SlippageProxyUSDC)
	}
	if !approxEqual(uplift.EstimatedLossUSDC.SelectivityLossUSDC, 0.4) {
		t.Fatalf("expected selectivity 0.4, got %v", uplift.EstimatedLossUSDC.SelectivityLossUSDC)
	}
	if len(uplift.Scenarios) != 3 {
		t.Fatalf("expected 3 scenarios, got %d", len(uplift.Scenarios))
	}
	if uplift.Scenarios[0].Code != "improve_selectivity_50pct" {
		t.Fatalf("expected top scenario improve_selectivity_50pct, got %s", uplift.Scenarios[0].Code)
	}
	if uplift.PriorityActionCode != "improve_selectivity" {
		t.Fatalf("expected priority action improve_selectivity, got %s", uplift.PriorityActionCode)
	}
	if !approxEqual(uplift.TotalPotentialUpliftUSDC, 0.55) {
		t.Fatalf("expected total uplift 0.55, got %v", uplift.TotalPotentialUpliftUSDC)
	}
	if uplift.ModelConfidence != "medium" {
		t.Fatalf("expected model confidence medium, got %s", uplift.ModelConfidence)
	}
}

func TestHandleDailyReportProfit(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       30,
		pnl:         8.0,
		unrealPnL:   -1.0,
		riskSnapshot: risk.Snapshot{
			DailyPnL:             -4.0,
			DailyLossLimitUSDC:   50.0,
			ConsecutiveLosses:    0,
			MaxConsecutiveLosses: 3,
		},
		paperSnapshot: paper.Snapshot{
			FeesPaidUSDC:    1.0,
			TotalVolumeUSDC: 500.0,
			TotalTrades:     30,
		},
		positions: map[string]execution.Position{
			"asset-win":  {AssetID: "asset-win", RealizedPnL: 6.0, TotalFills: 15},
			"asset-loss": {AssetID: "asset-loss", RealizedPnL: -1.5, TotalFills: 10},
			"asset-mid":  {AssetID: "asset-mid", RealizedPnL: 0.6, TotalFills: 5},
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/daily-report", nil)
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["can_trade"] != true {
		t.Fatalf("expected can_trade=true, got %v", resp["can_trade"])
	}

	diag, ok := resp["diagnosis"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected diagnosis object, got %T", resp["diagnosis"])
	}
	if diag["outcome"] != "profit" {
		t.Fatalf("expected diagnosis.outcome=profit, got %v", diag["outcome"])
	}

	summary, ok := resp["summary"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected summary object, got %T", resp["summary"])
	}
	if !approxEqual(summary["net_pnl_after_fees_usdc"].(float64), 6.0) {
		t.Fatalf("expected summary.net_pnl_after_fees_usdc=6.0, got %v", summary["net_pnl_after_fees_usdc"])
	}

	actions, ok := resp["next_actions"].([]interface{})
	if !ok {
		t.Fatalf("expected next_actions list, got %T", resp["next_actions"])
	}
	if !containsActionCode(actions, "focus_top_market") {
		t.Fatalf("expected focus_top_market action, got %v", actions)
	}
}

func TestHandleDailyReportLoss(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       25,
		pnl:         -4.0,
		unrealPnL:   0.0,
		riskSnapshot: risk.Snapshot{
			DailyPnL:             -18.0,
			DailyLossLimitUSDC:   20.0,
			ConsecutiveLosses:    2,
			MaxConsecutiveLosses: 3,
		},
		paperSnapshot: paper.Snapshot{
			FeesPaidUSDC:    0.8,
			TotalVolumeUSDC: 400.0,
			TotalTrades:     25,
		},
		positions: map[string]execution.Position{
			"asset-a": {AssetID: "asset-a", RealizedPnL: -3.0, TotalFills: 12},
			"asset-b": {AssetID: "asset-b", RealizedPnL: -1.2, TotalFills: 8},
			"asset-c": {AssetID: "asset-c", RealizedPnL: 0.4, TotalFills: 5},
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/daily-report", nil)
	w := httptest.NewRecorder()
	s.handleDailyReport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	diag := resp["diagnosis"].(map[string]interface{})
	if diag["outcome"] != "loss" {
		t.Fatalf("expected diagnosis.outcome=loss, got %v", diag["outcome"])
	}
	plan := resp["tomorrow_plan"].(map[string]interface{})
	if plan["risk_mode"] != "defensive" {
		t.Fatalf("expected tomorrow_plan.risk_mode=defensive, got %v", plan["risk_mode"])
	}

	actions := resp["next_actions"].([]interface{})
	if !containsActionCode(actions, "reduce_size_tomorrow") {
		t.Fatalf("expected reduce_size_tomorrow action, got %v", actions)
	}
	if !containsActionCode(actions, "improve_selectivity") {
		t.Fatalf("expected improve_selectivity action, got %v", actions)
	}
}

func TestHandleDailyReportRiskBlocked(t *testing.T) {
	state := &mockAppState{
		tradingMode: "paper",
		fills:       6,
		pnl:         -1.0,
		riskSnapshot: risk.Snapshot{
			EmergencyStop:        true,
			DailyPnL:             -12.0,
			DailyLossLimitUSDC:   10.0,
			ConsecutiveLosses:    3,
			MaxConsecutiveLosses: 3,
			InCooldown:           true,
			CooldownRemaining:    2 * time.Minute,
		},
		paperSnapshot: paper.Snapshot{
			FeesPaidUSDC:    0.2,
			TotalVolumeUSDC: 120.0,
			TotalTrades:     6,
		},
	}
	s := NewServer(":0", state, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/daily-report", nil)
	w := httptest.NewRecorder()
	s.handleDailyReport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["can_trade"] != false {
		t.Fatalf("expected can_trade=false, got %v", resp["can_trade"])
	}

	actions := resp["next_actions"].([]interface{})
	if !containsActionCode(actions, "pause_trading") {
		t.Fatalf("expected pause_trading action, got %v", actions)
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

func TestHandleHealth(t *testing.T) {
	s := NewServer(":0", &mockAppState{}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	s.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", resp["ok"])
	}
	if resp["uptime_s"] == nil {
		t.Fatal("expected uptime_s in response")
	}
}

func TestHandleReady(t *testing.T) {
	t.Run("running app is ready", func(t *testing.T) {
		s := NewServer(":0", &mockAppState{running: true}, nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/ready", nil)
		w := httptest.NewRecorder()
		s.handleReady(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp["ready"] != true {
			t.Fatalf("expected ready=true, got %v", resp["ready"])
		}
	})

	t.Run("stopped app is not ready", func(t *testing.T) {
		s := NewServer(":0", &mockAppState{running: false}, nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/ready", nil)
		w := httptest.NewRecorder()
		s.handleReady(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", w.Code)
		}
		var resp map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp["ready"] != false {
			t.Fatalf("expected ready=false, got %v", resp["ready"])
		}
		if resp["reason"] != "app_not_running" {
			t.Fatalf("expected reason=app_not_running, got %v", resp["reason"])
		}
	})
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
	if resp["never_synced"] != false {
		t.Errorf("expected never_synced=false, got %v", resp["never_synced"])
	}
	if resp["stale"] != false {
		t.Errorf("expected stale=false, got %v", resp["stale"])
	}
	if int(resp["stale_after_s"].(float64)) != 1800 {
		t.Errorf("expected stale_after_s=1800, got %v", resp["stale_after_s"])
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
	if resp["never_synced"] != true {
		t.Errorf("expected never_synced=true, got %v", resp["never_synced"])
	}
	if resp["stale"] != false {
		t.Errorf("expected stale=false, got %v", resp["stale"])
	}
	if int(resp["stale_after_s"].(float64)) != 1800 {
		t.Errorf("expected stale_after_s=1800, got %v", resp["stale_after_s"])
	}
}

func TestHandleBuilderStaleAndNeverSynced(t *testing.T) {
	t.Run("stale when sync too old", func(t *testing.T) {
		builder := &mockBuilder{
			lastSync:    time.Now().Add(-31 * time.Minute),
			dailyVolume: []string{"v1"},
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
		if resp["never_synced"] != false {
			t.Fatalf("expected never_synced=false, got %v", resp["never_synced"])
		}
		if resp["stale"] != true {
			t.Fatalf("expected stale=true, got %v", resp["stale"])
		}
	})

	t.Run("never synced marked stale", func(t *testing.T) {
		builder := &mockBuilder{
			dailyVolume: []string{},
			leaderboard: []string{},
		}
		s := NewServer(":0", &mockAppState{}, nil, builder)

		req := httptest.NewRequest(http.MethodGet, "/api/builder", nil)
		w := httptest.NewRecorder()
		s.handleBuilder(w, req)

		var resp map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp["never_synced"] != true {
			t.Fatalf("expected never_synced=true, got %v", resp["never_synced"])
		}
		if resp["stale"] != true {
			t.Fatalf("expected stale=true, got %v", resp["stale"])
		}
		if resp["last_sync_age_s"] != nil {
			t.Fatalf("expected nil last_sync_age_s when never synced, got %v", resp["last_sync_age_s"])
		}
	})
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
