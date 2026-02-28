package app

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"

	"github.com/GoPolymarket/polymarket-trader/internal/config"
)

type mockNotifier struct {
	riskCooldownCalls   int
	lastConsecutive     int
	lastMax             int
	lastCooldown        time.Duration
	dailyTemplateCalls  int
	weeklyTemplateCalls int
	lastDailyTemplate   string
	lastWeeklyTemplate  string
}

func (m *mockNotifier) NotifyFill(_ context.Context, _ string, _ string, _ float64, _ float64) error {
	return nil
}

func (m *mockNotifier) NotifyStopLoss(_ context.Context, _ string, _ float64) error {
	return nil
}

func (m *mockNotifier) NotifyEmergencyStop(_ context.Context) error {
	return nil
}

func (m *mockNotifier) NotifyDailySummary(_ context.Context, _ float64, _ int, _ float64) error {
	return nil
}

func (m *mockNotifier) NotifyRiskCooldown(_ context.Context, consecutiveLosses, maxConsecutiveLosses int, cooldownRemaining time.Duration) error {
	m.riskCooldownCalls++
	m.lastConsecutive = consecutiveLosses
	m.lastMax = maxConsecutiveLosses
	m.lastCooldown = cooldownRemaining
	return nil
}

func (m *mockNotifier) NotifyDailyCoachTemplate(_ context.Context, textHTML string) error {
	m.dailyTemplateCalls++
	m.lastDailyTemplate = textHTML
	return nil
}

func (m *mockNotifier) NotifyWeeklyReviewTemplate(_ context.Context, textHTML string) error {
	m.weeklyTemplateCalls++
	m.lastWeeklyTemplate = textHTML
	return nil
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.DryRun = true
	cfg.Maker.Enabled = true
	cfg.Taker.Enabled = true
	cfg.Maker.Markets = []string{"asset-1"}
	return cfg
}

func TestNewApp(t *testing.T) {
	cfg := testConfig()
	a := New(cfg, nil, nil, nil, nil, nil, nil)
	if a == nil {
		t.Fatal("expected non-nil app")
		return
	}
	if a.activeOrders == nil {
		t.Fatal("expected initialized activeOrders map")
	}
	if a.tracker == nil {
		t.Fatal("expected initialized tracker")
	}
}

func TestHandleBookEventDryRunMaker(t *testing.T) {
	cfg := testConfig()
	cfg.Maker.Enabled = true
	cfg.Taker.Enabled = false

	a := New(cfg, nil, nil, nil, nil, nil, nil)

	event := ws.OrderbookEvent{
		AssetID: "asset-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "100"}},
	}

	a.HandleBookEvent(context.Background(), event)

	orders, fills, _ := a.Stats()
	if orders != 0 {
		t.Fatalf("dry run should produce 0 orders, got %d", orders)
	}
	if fills != 0 {
		t.Fatalf("dry run should produce 0 fills, got %d", fills)
	}

	book, ok := a.books.Get("asset-1")
	if !ok {
		t.Fatal("expected book snapshot for asset-1")
	}
	if len(book.Bids) != 1 {
		t.Fatalf("expected 1 bid level, got %d", len(book.Bids))
	}
}

func TestHandleBookEventDryRunTaker(t *testing.T) {
	cfg := testConfig()
	cfg.Maker.Enabled = false
	cfg.Taker.Enabled = true
	cfg.Taker.MinImbalance = 0.10

	a := New(cfg, nil, nil, nil, nil, nil, nil)

	event := ws.OrderbookEvent{
		AssetID: "asset-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "300"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "50"}},
	}

	a.HandleBookEvent(context.Background(), event)

	_, fills, _ := a.Stats()
	if fills != 0 {
		t.Fatalf("dry run should produce 0 fills, got %d", fills)
	}
}

func TestHandleBookEventEmptyBook(t *testing.T) {
	cfg := testConfig()
	a := New(cfg, nil, nil, nil, nil, nil, nil)

	event := ws.OrderbookEvent{AssetID: "asset-1"}
	a.HandleBookEvent(context.Background(), event)

	orders, fills, _ := a.Stats()
	if orders != 0 || fills != 0 {
		t.Fatal("empty book should produce no orders or fills")
	}
}

func TestStats(t *testing.T) {
	cfg := testConfig()
	a := New(cfg, nil, nil, nil, nil, nil, nil)

	orders, fills, pnl := a.Stats()
	if orders != 0 || fills != 0 || pnl != 0 {
		t.Fatalf("expected zeroed stats, got orders=%d fills=%d pnl=%f", orders, fills, pnl)
	}
}

func TestShutdownDryRun(t *testing.T) {
	cfg := testConfig()
	cfg.DryRun = true
	a := New(cfg, nil, nil, nil, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	a.Shutdown(ctx)
}

func TestHandleBookEventMultipleUpdates(t *testing.T) {
	cfg := testConfig()
	cfg.Maker.Enabled = true
	cfg.Taker.Enabled = false

	a := New(cfg, nil, nil, nil, nil, nil, nil)

	for i := 0; i < 5; i++ {
		event := ws.OrderbookEvent{
			AssetID: "asset-1",
			Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}},
			Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "100"}},
		}
		a.HandleBookEvent(context.Background(), event)
	}

	orders, _, _ := a.Stats()
	if orders != 0 {
		t.Fatalf("dry run should produce 0 orders, got %d", orders)
	}
}

func TestRiskSyncTracksRealizedDeltas(t *testing.T) {
	cfg := testConfig()
	cfg.Risk.MaxConsecutiveLosses = 2
	cfg.Risk.ConsecutiveLossCooldown = time.Minute
	cfg.Risk.MaxDailyLossUSDC = 500
	cfg.Risk.AccountCapitalUSDC = 1000
	cfg.Risk.MaxDailyLossPct = 0.05

	a := New(cfg, nil, nil, nil, nil, nil, nil)

	// First realized loss.
	a.tracker.ProcessTradeEvent(ws.TradeEvent{ID: "b-1", AssetID: "asset-1", Side: "BUY", Price: "0.60", Size: "10"})
	a.tracker.ProcessTradeEvent(ws.TradeEvent{ID: "s-1", AssetID: "asset-1", Side: "SELL", Price: "0.50", Size: "10"})
	a.riskSync(context.Background())

	if got := a.riskMgr.ConsecutiveLosses(); got != 1 {
		t.Fatalf("expected one consecutive loss after first negative delta, got %d", got)
	}
	if a.riskMgr.InCooldown() {
		t.Fatal("did not expect cooldown after first loss")
	}

	// Second realized loss should trigger cooldown.
	a.tracker.ProcessTradeEvent(ws.TradeEvent{ID: "b-2", AssetID: "asset-1", Side: "BUY", Price: "0.70", Size: "10"})
	a.tracker.ProcessTradeEvent(ws.TradeEvent{ID: "s-2", AssetID: "asset-1", Side: "SELL", Price: "0.60", Size: "10"})
	a.riskSync(context.Background())

	if !a.riskMgr.InCooldown() {
		t.Fatal("expected cooldown after second consecutive realized loss")
	}
	if err := a.riskMgr.Allow("asset-1", 1); err == nil {
		t.Fatal("expected risk manager to block new orders during cooldown")
	}
}

func TestRiskSyncSendsCooldownNotification(t *testing.T) {
	cfg := testConfig()
	cfg.Risk.MaxConsecutiveLosses = 2
	cfg.Risk.ConsecutiveLossCooldown = time.Minute
	cfg.Risk.MaxDailyLossUSDC = 500
	cfg.Risk.AccountCapitalUSDC = 1000
	cfg.Risk.MaxDailyLossPct = 0.05

	a := New(cfg, nil, nil, nil, nil, nil, nil)
	mockN := &mockNotifier{}
	a.notifier = mockN

	// First realized loss.
	a.tracker.ProcessTradeEvent(ws.TradeEvent{ID: "b-1", AssetID: "asset-1", Side: "BUY", Price: "0.60", Size: "10"})
	a.tracker.ProcessTradeEvent(ws.TradeEvent{ID: "s-1", AssetID: "asset-1", Side: "SELL", Price: "0.50", Size: "10"})
	a.riskSync(context.Background())

	// Second realized loss should trigger cooldown + notification.
	a.tracker.ProcessTradeEvent(ws.TradeEvent{ID: "b-2", AssetID: "asset-1", Side: "BUY", Price: "0.70", Size: "10"})
	a.tracker.ProcessTradeEvent(ws.TradeEvent{ID: "s-2", AssetID: "asset-1", Side: "SELL", Price: "0.60", Size: "10"})
	a.riskSync(context.Background())

	if mockN.riskCooldownCalls != 1 {
		t.Fatalf("expected 1 cooldown notification, got %d", mockN.riskCooldownCalls)
	}
	if mockN.lastConsecutive != 2 || mockN.lastMax != 2 {
		t.Fatalf("unexpected cooldown notification payload: consecutive=%d max=%d", mockN.lastConsecutive, mockN.lastMax)
	}
	if mockN.lastCooldown <= 0 {
		t.Fatalf("expected positive cooldown remaining, got %v", mockN.lastCooldown)
	}
}

func TestSendScheduledTelegramReportsDailyAndWeekly(t *testing.T) {
	cfg := testConfig()
	a := New(cfg, nil, nil, nil, nil, nil, nil)
	mockN := &mockNotifier{}
	a.notifier = mockN

	// Seed some simple realized PnL so templates include non-zero values.
	a.tracker.ProcessTradeEvent(ws.TradeEvent{ID: "b-1", AssetID: "asset-1", Side: "BUY", Price: "0.40", Size: "10"})
	a.tracker.ProcessTradeEvent(ws.TradeEvent{ID: "s-1", AssetID: "asset-1", Side: "SELL", Price: "0.50", Size: "10"})

	monday := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC) // Monday
	a.sendScheduledTelegramReports(context.Background(), monday)

	if mockN.dailyTemplateCalls != 1 {
		t.Fatalf("expected 1 daily template call, got %d", mockN.dailyTemplateCalls)
	}
	if mockN.weeklyTemplateCalls != 1 {
		t.Fatalf("expected 1 weekly template call on Monday, got %d", mockN.weeklyTemplateCalls)
	}
	if mockN.lastDailyTemplate == "" || !strings.Contains(mockN.lastDailyTemplate, "Daily Trading Coach") {
		t.Fatalf("expected daily template text, got %q", mockN.lastDailyTemplate)
	}
	if !strings.Contains(mockN.lastDailyTemplate, "Profit Focus") {
		t.Fatalf("expected profit focus in daily template, got %q", mockN.lastDailyTemplate)
	}
	if mockN.lastWeeklyTemplate == "" || !strings.Contains(mockN.lastWeeklyTemplate, "Weekly Trading Review") {
		t.Fatalf("expected weekly template text, got %q", mockN.lastWeeklyTemplate)
	}
}

func TestSendScheduledTelegramReportsDailyOnly(t *testing.T) {
	cfg := testConfig()
	a := New(cfg, nil, nil, nil, nil, nil, nil)
	mockN := &mockNotifier{}
	a.notifier = mockN

	tuesday := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC) // Tuesday
	a.sendScheduledTelegramReports(context.Background(), tuesday)

	if mockN.dailyTemplateCalls != 1 {
		t.Fatalf("expected 1 daily template call, got %d", mockN.dailyTemplateCalls)
	}
	if mockN.weeklyTemplateCalls != 0 {
		t.Fatalf("expected 0 weekly template calls on Tuesday, got %d", mockN.weeklyTemplateCalls)
	}
}

func TestHandleBookEventPaperMode(t *testing.T) {
	cfg := testConfig()
	cfg.DryRun = false
	cfg.TradingMode = "paper"
	cfg.Maker.Enabled = false
	cfg.Taker.Enabled = false
	cfg.Paper.InitialBalanceUSDC = 1000
	cfg.Paper.FeeBps = 10
	cfg.Paper.SlippageBps = 0

	a := New(cfg, nil, nil, nil, nil, nil, nil)

	event := ws.OrderbookEvent{
		AssetID: "asset-1",
		Market:  "market-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "300"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "50"}},
	}

	a.books.Update(event)
	resp := a.placeMarket(context.Background(), "asset-1", "BUY", 20)
	if resp.ID == "" {
		t.Fatal("expected non-empty paper order id")
	}

	_, fills, _ := a.Stats()
	if fills == 0 {
		t.Fatal("expected at least one synthetic fill in paper mode")
	}
	if a.TradingMode() != "paper" {
		t.Fatalf("expected trading mode paper, got %s", a.TradingMode())
	}
	if a.PaperSnapshot().InitialBalanceUSDC != 1000 {
		t.Fatalf("expected paper initial balance 1000, got %f", a.PaperSnapshot().InitialBalanceUSDC)
	}
}

func TestPaperModeMakerReplacesLiveOrdersWithoutLeak(t *testing.T) {
	cfg := testConfig()
	cfg.DryRun = false
	cfg.TradingMode = "paper"
	cfg.Maker.Enabled = true
	cfg.Taker.Enabled = false
	cfg.Maker.OrderSizeUSDC = 1
	cfg.Paper.InitialBalanceUSDC = 1000
	cfg.Paper.SlippageBps = 0
	cfg.Paper.FeeBps = 0

	a := New(cfg, nil, nil, nil, nil, nil, nil)

	event := ws.OrderbookEvent{
		AssetID: "asset-1",
		Market:  "market-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "100"}},
	}

	a.HandleBookEvent(context.Background(), event)
	firstOpen := len(a.ActiveOrders())
	if firstOpen == 0 {
		t.Fatal("expected maker to create live paper orders")
	}

	a.HandleBookEvent(context.Background(), event)
	secondOpen := len(a.ActiveOrders())
	if secondOpen != firstOpen {
		t.Fatalf("expected open paper orders to stay at %d after refresh, got %d", firstOpen, secondOpen)
	}
}

func TestPlacePaperLimitUnfilledKeepsOrderMetadata(t *testing.T) {
	cfg := testConfig()
	cfg.DryRun = false
	cfg.TradingMode = "paper"
	cfg.Maker.Enabled = false
	cfg.Taker.Enabled = false

	a := New(cfg, nil, nil, nil, nil, nil, nil)
	a.books.Update(ws.OrderbookEvent{
		AssetID: "asset-1",
		Market:  "market-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "100"}},
	})

	resp := a.placeLimit(context.Background(), "asset-1", "BUY", 0.51, 20)
	if resp.ID == "" || resp.Status != "LIVE" {
		t.Fatalf("expected LIVE paper order, got id=%q status=%q", resp.ID, resp.Status)
	}

	orders := a.ActiveOrders()
	if len(orders) != 1 {
		t.Fatalf("expected 1 active order, got %d", len(orders))
	}
	if math.Abs(orders[0].Price-0.51) > 1e-9 {
		t.Fatalf("expected order price 0.51, got %f", orders[0].Price)
	}
	if math.Abs(orders[0].OrigSize-20) > 1e-9 {
		t.Fatalf("expected order orig size 20, got %f", orders[0].OrigSize)
	}
}
