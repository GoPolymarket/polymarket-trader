package risk

import "testing"

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
