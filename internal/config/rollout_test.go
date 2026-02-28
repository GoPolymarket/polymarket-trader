package config

import "testing"

func TestApplyRolloutPhasePaper(t *testing.T) {
	cfg := Default()
	cfg.TradingMode = "live"
	cfg.DryRun = true

	if err := ApplyRolloutPhase(&cfg, "paper"); err != nil {
		t.Fatalf("ApplyRolloutPhase: %v", err)
	}
	if cfg.TradingMode != "paper" {
		t.Fatalf("expected paper mode, got %q", cfg.TradingMode)
	}
	if cfg.DryRun {
		t.Fatal("expected dry_run=false for paper phase")
	}
}

func TestApplyRolloutPhaseShadow(t *testing.T) {
	cfg := Default()
	cfg.TradingMode = "paper"
	cfg.DryRun = false

	if err := ApplyRolloutPhase(&cfg, "shadow"); err != nil {
		t.Fatalf("ApplyRolloutPhase: %v", err)
	}
	if cfg.TradingMode != "live" {
		t.Fatalf("expected live mode, got %q", cfg.TradingMode)
	}
	if !cfg.DryRun {
		t.Fatal("expected dry_run=true for shadow phase")
	}
}

func TestApplyRolloutPhaseLiveSmallClamps(t *testing.T) {
	cfg := Default()
	cfg.Risk.MaxOpenOrders = 50
	cfg.Maker.OrderSizeUSDC = 10
	cfg.Taker.AmountUSDC = 12
	cfg.Risk.MaxPositionPerMarket = 30
	cfg.Risk.MaxDailyLossPct = 0.05

	if err := ApplyRolloutPhase(&cfg, "live-small"); err != nil {
		t.Fatalf("ApplyRolloutPhase: %v", err)
	}
	if cfg.TradingMode != "live" {
		t.Fatalf("expected live mode, got %q", cfg.TradingMode)
	}
	if cfg.DryRun {
		t.Fatal("expected dry_run=false for live-small phase")
	}
	if cfg.Risk.MaxOpenOrders != 4 {
		t.Fatalf("expected max_open_orders=4, got %d", cfg.Risk.MaxOpenOrders)
	}
	if cfg.Maker.OrderSizeUSDC != 1 {
		t.Fatalf("expected maker order size=1, got %f", cfg.Maker.OrderSizeUSDC)
	}
	if cfg.Taker.AmountUSDC != 1 {
		t.Fatalf("expected taker amount=1, got %f", cfg.Taker.AmountUSDC)
	}
	if cfg.Risk.MaxPositionPerMarket != 3 {
		t.Fatalf("expected max position per market=3, got %f", cfg.Risk.MaxPositionPerMarket)
	}
	if cfg.Risk.MaxDailyLossPct != 0.01 {
		t.Fatalf("expected max daily loss pct=0.01, got %f", cfg.Risk.MaxDailyLossPct)
	}
}

func TestApplyRolloutPhaseLive(t *testing.T) {
	cfg := Default()
	cfg.TradingMode = "paper"
	cfg.DryRun = true

	if err := ApplyRolloutPhase(&cfg, "live"); err != nil {
		t.Fatalf("ApplyRolloutPhase: %v", err)
	}
	if cfg.TradingMode != "live" {
		t.Fatalf("expected live mode, got %q", cfg.TradingMode)
	}
	if cfg.DryRun {
		t.Fatal("expected dry_run=false for live phase")
	}
}

func TestApplyRolloutPhaseUnknown(t *testing.T) {
	cfg := Default()
	if err := ApplyRolloutPhase(&cfg, "unknown-phase"); err == nil {
		t.Fatal("expected error for unknown rollout phase")
	}
}
