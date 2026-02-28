package risk

import (
	"testing"
	"time"

	"github.com/GoPolymarket/polymarket-trader/internal/execution"
)

func TestAllowOrderBasic(t *testing.T) {
	m := New(Config{MaxOpenOrders: 5, MaxDailyLossUSDC: 100, MaxPositionPerMarket: 50})
	if err := m.Allow("token-1", 25); err != nil {
		t.Fatalf("expected allow, got %v", err)
	}
}

func TestBlockOnMaxOrders(t *testing.T) {
	m := New(Config{MaxOpenOrders: 2, MaxDailyLossUSDC: 100, MaxPositionPerMarket: 50})
	m.SetOpenOrders(2)
	if err := m.Allow("token-1", 25); err == nil {
		t.Fatal("expected block on max orders")
	}
}

func TestBlockOnDailyLoss(t *testing.T) {
	m := New(Config{MaxOpenOrders: 20, MaxDailyLossUSDC: 100, MaxPositionPerMarket: 50})
	m.RecordPnL(-101)
	if err := m.Allow("token-1", 25); err == nil {
		t.Fatal("expected block on daily loss")
	}
}

func TestBlockOnPositionLimit(t *testing.T) {
	m := New(Config{MaxOpenOrders: 20, MaxDailyLossUSDC: 100, MaxPositionPerMarket: 50})
	m.AddPosition("token-1", 30)
	if err := m.Allow("token-1", 25); err == nil {
		t.Fatal("expected block on position limit")
	}
}

func TestEmergencyStop(t *testing.T) {
	m := New(Config{MaxOpenOrders: 20, MaxDailyLossUSDC: 100, MaxPositionPerMarket: 50})
	m.SetEmergencyStop(true)
	if err := m.Allow("token-1", 10); err == nil {
		t.Fatal("expected block on emergency stop")
	}
}

func TestRecordPnLAndReset(t *testing.T) {
	m := New(Config{MaxOpenOrders: 20, MaxDailyLossUSDC: 100, MaxPositionPerMarket: 50})
	m.RecordPnL(-50)
	m.RecordPnL(-40)
	if m.DailyPnL() != -90 {
		t.Fatalf("expected -90, got %f", m.DailyPnL())
	}
	m.ResetDaily()
	if m.DailyPnL() != 0 {
		t.Fatalf("expected 0 after reset, got %f", m.DailyPnL())
	}
}

func TestRemovePosition(t *testing.T) {
	m := New(Config{MaxOpenOrders: 20, MaxDailyLossUSDC: 100, MaxPositionPerMarket: 50})
	m.AddPosition("token-1", 30)
	m.RemovePosition("token-1", 10)

	if err := m.Allow("token-1", 25); err != nil {
		t.Fatalf("expected allow: 20+25 <= 50, got %v", err)
	}
	if err := m.Allow("token-1", 31); err == nil {
		t.Fatal("expected block: 20+31 > 50")
	}
}

func TestRemovePositionDeletesAtZero(t *testing.T) {
	m := New(Config{MaxOpenOrders: 20, MaxDailyLossUSDC: 100, MaxPositionPerMarket: 50})
	m.AddPosition("token-1", 30)
	m.RemovePosition("token-1", 30)

	if err := m.Allow("token-1", 50); err != nil {
		t.Fatalf("expected allow after full removal, got %v", err)
	}
}

func TestRemovePositionBelowZero(t *testing.T) {
	m := New(Config{MaxOpenOrders: 20, MaxDailyLossUSDC: 100, MaxPositionPerMarket: 50})
	m.AddPosition("token-1", 10)
	m.RemovePosition("token-1", 20)

	if err := m.Allow("token-1", 50); err != nil {
		t.Fatalf("expected allow after over-removal, got %v", err)
	}
}

func TestSetOpenOrders(t *testing.T) {
	m := New(Config{MaxOpenOrders: 5, MaxDailyLossUSDC: 100, MaxPositionPerMarket: 50})
	m.SetOpenOrders(3)
	if err := m.Allow("token-1", 10); err != nil {
		t.Fatalf("expected allow at 3/5 orders, got %v", err)
	}
	m.SetOpenOrders(5)
	if err := m.Allow("token-1", 10); err == nil {
		t.Fatal("expected block at 5/5 orders")
	}
}

func TestEmergencyStopToggle(t *testing.T) {
	m := New(Config{MaxOpenOrders: 20, MaxDailyLossUSDC: 100, MaxPositionPerMarket: 50})
	m.SetEmergencyStop(true)
	if err := m.Allow("token-1", 10); err == nil {
		t.Fatal("expected block on emergency stop")
	}
	m.SetEmergencyStop(false)
	if err := m.Allow("token-1", 10); err != nil {
		t.Fatalf("expected allow after emergency stop cleared, got %v", err)
	}
}

func TestSyncFromTracker(t *testing.T) {
	m := New(Config{MaxOpenOrders: 5, MaxDailyLossUSDC: 100, MaxPositionPerMarket: 50})

	positions := map[string]execution.Position{
		"asset-1": {AssetID: "asset-1", NetSize: 10, AvgEntryPrice: 0.50},
		"asset-2": {AssetID: "asset-2", NetSize: 5, AvgEntryPrice: 0.60},
	}

	m.SyncFromTracker(3, positions, -15.5)

	if m.DailyPnL() != -15.5 {
		t.Fatalf("expected daily PnL -15.5, got %f", m.DailyPnL())
	}
	// Open orders synced.
	if err := m.Allow("x", 10); err != nil {
		t.Fatalf("expected allow at 3/5 orders, got %v", err)
	}
	m.SetOpenOrders(5)
	if err := m.Allow("x", 10); err == nil {
		t.Fatal("expected block at 5/5 orders")
	}
}

func TestStopLossTriggered(t *testing.T) {
	m := New(Config{StopLossPerMarket: 20})
	pos := execution.Position{
		AssetID:       "asset-1",
		NetSize:       100,
		AvgEntryPrice: 0.50,
		RealizedPnL:   0,
	}
	// Unrealized: (0.30 - 0.50) * 100 = -20 → triggers stop-loss at -20.
	if !m.EvaluateStopLoss("asset-1", pos, 0.30) {
		t.Fatal("expected stop-loss to trigger")
	}
}

func TestStopLossNotTriggered(t *testing.T) {
	m := New(Config{StopLossPerMarket: 20})
	pos := execution.Position{
		AssetID:       "asset-1",
		NetSize:       100,
		AvgEntryPrice: 0.50,
		RealizedPnL:   0,
	}
	// Unrealized: (0.45 - 0.50) * 100 = -5 → not triggered.
	if m.EvaluateStopLoss("asset-1", pos, 0.45) {
		t.Fatal("expected stop-loss NOT to trigger")
	}
}

func TestEmergencyOnDrawdown(t *testing.T) {
	m := New(Config{MaxDrawdownPct: 0.15})
	// Capital = 100, realized PnL = -10, unrealized = -6 → total = -16, 16% > 15%
	if !m.EvaluateDrawdown(-10, -6, 100) {
		t.Fatal("expected drawdown trigger")
	}
	// Total = -14 → 14% < 15%
	if m.EvaluateDrawdown(-10, -4, 100) {
		t.Fatal("expected no drawdown trigger")
	}
}

func TestDailyReset(t *testing.T) {
	m := New(Config{MaxOpenOrders: 20, MaxDailyLossUSDC: 100, MaxPositionPerMarket: 50})
	m.RecordPnL(-50)
	if m.DailyPnL() != -50 {
		t.Fatalf("expected -50, got %f", m.DailyPnL())
	}
	m.ResetDaily()
	if m.DailyPnL() != 0 {
		t.Fatalf("expected 0 after reset, got %f", m.DailyPnL())
	}
}

func TestDailyLossLimitFromCapitalPct(t *testing.T) {
	m := New(Config{
		MaxOpenOrders:        20,
		MaxDailyLossUSDC:     0,
		MaxPositionPerMarket: 50,
		AccountCapitalUSDC:   1000,
		MaxDailyLossPct:      0.02,
	})

	if got := m.DailyLossLimitUSDC(); got != 20 {
		t.Fatalf("expected derived daily loss limit 20, got %f", got)
	}

	m.RecordPnL(-20)
	if err := m.Allow("token-1", 1); err == nil {
		t.Fatal("expected block once derived daily loss limit is reached")
	}
}

func TestConsecutiveLossCooldown(t *testing.T) {
	m := New(Config{
		MaxOpenOrders:           20,
		MaxDailyLossUSDC:        100,
		MaxPositionPerMarket:    50,
		MaxConsecutiveLosses:    3,
		ConsecutiveLossCooldown: time.Minute,
	})

	m.RecordTradeResult(-1)
	m.RecordTradeResult(-0.5)
	m.RecordTradeResult(-0.25)

	if got := m.ConsecutiveLosses(); got != 3 {
		t.Fatalf("expected 3 consecutive losses, got %d", got)
	}
	if !m.InCooldown() {
		t.Fatal("expected cooldown to be active after 3 consecutive losses")
	}
	if err := m.Allow("token-1", 1); err == nil {
		t.Fatal("expected allow to block while cooldown is active")
	}
}

func TestConsecutiveLossResetOnProfit(t *testing.T) {
	m := New(Config{
		MaxOpenOrders:           20,
		MaxDailyLossUSDC:        100,
		MaxPositionPerMarket:    50,
		MaxConsecutiveLosses:    3,
		ConsecutiveLossCooldown: time.Minute,
	})

	m.RecordTradeResult(-1)
	if got := m.ConsecutiveLosses(); got != 1 {
		t.Fatalf("expected 1 consecutive loss, got %d", got)
	}

	m.RecordTradeResult(0.5)
	if got := m.ConsecutiveLosses(); got != 0 {
		t.Fatalf("expected consecutive losses reset to 0 after profit, got %d", got)
	}
	if m.InCooldown() {
		t.Fatal("expected cooldown to remain inactive after streak reset")
	}
}

func TestConsecutiveLossCooldownResetsAfterExpiry(t *testing.T) {
	m := New(Config{
		MaxOpenOrders:           20,
		MaxDailyLossUSDC:        100,
		MaxPositionPerMarket:    50,
		MaxConsecutiveLosses:    2,
		ConsecutiveLossCooldown: time.Minute,
	})

	m.RecordTradeResult(-1)
	m.RecordTradeResult(-0.5)
	if !m.InCooldown() {
		t.Fatal("expected cooldown after reaching consecutive loss threshold")
	}

	// Simulate cooldown elapsed without waiting in test.
	m.cooldownUntil = time.Now().Add(-time.Second)

	// New loss should start a fresh streak, not immediately re-trigger cooldown.
	m.RecordTradeResult(-0.1)
	if m.InCooldown() {
		t.Fatal("expected cooldown to stay inactive after elapsed window and first new loss")
	}
	if got := m.ConsecutiveLosses(); got != 1 {
		t.Fatalf("expected fresh streak count 1 after cooldown expiry, got %d", got)
	}
}
