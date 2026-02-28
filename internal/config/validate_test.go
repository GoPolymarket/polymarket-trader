package config

import "testing"

func TestValidateDefaultConfig(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected default config to be valid, got: %v", err)
	}
}

func TestValidateInvalidTradingMode(t *testing.T) {
	cfg := Default()
	cfg.TradingMode = "invalid-mode"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid trading_mode to fail validation")
	}
}

func TestValidateInvalidPaperConfig(t *testing.T) {
	cfg := Default()
	cfg.Paper.InitialBalanceUSDC = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected non-positive paper.initial_balance_usdc to fail validation")
	}

	cfg = Default()
	cfg.Paper.FeeBps = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative paper.fee_bps to fail validation")
	}
}

func TestValidateInvalidRiskPct(t *testing.T) {
	cfg := Default()
	cfg.Risk.MaxDailyLossPct = 1.5
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected risk.max_daily_loss_pct > 1 to fail validation")
	}

	cfg = Default()
	cfg.Risk.MaxDrawdownPct = -0.1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative risk.max_drawdown_pct to fail validation")
	}
}
