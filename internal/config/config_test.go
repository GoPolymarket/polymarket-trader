package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg := Default()
	if cfg.Maker.MinSpreadBps <= 0 {
		t.Fatal("expected positive min spread bps")
	}
	if cfg.Risk.MaxOpenOrders <= 0 {
		t.Fatal("expected positive max open orders")
	}
	if cfg.ScanInterval <= 0 {
		t.Fatal("expected positive scan interval")
	}
	if !cfg.DryRun {
		t.Fatal("expected dry run true by default")
	}
	if cfg.Risk.MaxDailyLossPct <= 0 {
		t.Fatal("expected positive max_daily_loss_pct by default")
	}
	if cfg.Risk.AccountCapitalUSDC <= 0 {
		t.Fatal("expected positive account_capital_usdc by default")
	}
	if cfg.Risk.MaxConsecutiveLosses <= 0 {
		t.Fatal("expected positive max_consecutive_losses by default")
	}
	if cfg.TradingMode != "paper" {
		t.Fatalf("expected trading_mode=paper by default, got %q", cfg.TradingMode)
	}
	if cfg.BuilderSyncInterval != 10*time.Minute {
		t.Fatalf("expected builder_sync_interval=10m by default, got %v", cfg.BuilderSyncInterval)
	}
	if cfg.Paper.InitialBalanceUSDC <= 0 {
		t.Fatal("expected positive paper initial_balance_usdc by default")
	}
	if !cfg.Paper.AllowShort {
		t.Fatal("expected paper allow_short=true by default")
	}
}

func TestLoadFromYAML(t *testing.T) {
	yaml := `
scan_interval: 30s
maker:
  enabled: false
  order_size_usdc: 50
taker:
  min_imbalance: 0.2
risk:
  max_daily_loss_usdc: 200
  max_daily_loss_pct: 0.03
  account_capital_usdc: 1500
  max_consecutive_losses: 4
  consecutive_loss_cooldown: 45m
trading_mode: live
builder_sync_interval: 2m
paper:
  initial_balance_usdc: 2000
  fee_bps: 12
  slippage_bps: 8
  allow_short: false
`
	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cfg, err := LoadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Maker.Enabled {
		t.Fatal("expected maker disabled")
	}
	if cfg.Maker.OrderSizeUSDC != 50 {
		t.Fatalf("expected order size 50, got %f", cfg.Maker.OrderSizeUSDC)
	}
	if cfg.Taker.MinImbalance != 0.2 {
		t.Fatalf("expected min imbalance 0.2, got %f", cfg.Taker.MinImbalance)
	}
	if cfg.Risk.MaxDailyLossUSDC != 200 {
		t.Fatalf("expected max daily loss 200, got %f", cfg.Risk.MaxDailyLossUSDC)
	}
	if cfg.Risk.MaxDailyLossPct != 0.03 {
		t.Fatalf("expected max daily loss pct 0.03, got %f", cfg.Risk.MaxDailyLossPct)
	}
	if cfg.Risk.AccountCapitalUSDC != 1500 {
		t.Fatalf("expected account capital 1500, got %f", cfg.Risk.AccountCapitalUSDC)
	}
	if cfg.Risk.MaxConsecutiveLosses != 4 {
		t.Fatalf("expected max consecutive losses 4, got %d", cfg.Risk.MaxConsecutiveLosses)
	}
	if cfg.Risk.ConsecutiveLossCooldown != 45*time.Minute {
		t.Fatalf("expected consecutive loss cooldown 45m, got %v", cfg.Risk.ConsecutiveLossCooldown)
	}
	if cfg.TradingMode != "live" {
		t.Fatalf("expected trading_mode live, got %q", cfg.TradingMode)
	}
	if cfg.BuilderSyncInterval != 2*time.Minute {
		t.Fatalf("expected builder_sync_interval 2m, got %v", cfg.BuilderSyncInterval)
	}
	if cfg.Paper.InitialBalanceUSDC != 2000 {
		t.Fatalf("expected paper initial balance 2000, got %f", cfg.Paper.InitialBalanceUSDC)
	}
	if cfg.Paper.FeeBps != 12 {
		t.Fatalf("expected paper fee_bps 12, got %f", cfg.Paper.FeeBps)
	}
	if cfg.Paper.SlippageBps != 8 {
		t.Fatalf("expected paper slippage_bps 8, got %f", cfg.Paper.SlippageBps)
	}
	if cfg.Paper.AllowShort {
		t.Fatal("expected paper allow_short=false from yaml")
	}
	if cfg.ScanInterval != 30*time.Second {
		t.Fatalf("expected 30s scan interval, got %v", cfg.ScanInterval)
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("TRADER_DRY_RUN", "false")
	cfg := Default()
	cfg.ApplyEnv()
	if cfg.DryRun {
		t.Fatal("expected dry run false from env")
	}
}

func TestLoadFileInvalidPath(t *testing.T) {
	_, err := LoadFile("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestLoadFileInvalidYAML(t *testing.T) {
	f, err := os.CreateTemp("", "bad-config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write([]byte("{{invalid yaml")); err != nil {
		t.Fatal(err)
	}
	f.Close()

	_, err = LoadFile(f.Name())
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestApplyEnvAllVars(t *testing.T) {
	t.Setenv("POLYMARKET_PK", "test-pk")
	t.Setenv("POLYMARKET_API_KEY", "test-key")
	t.Setenv("POLYMARKET_API_SECRET", "test-secret")
	t.Setenv("POLYMARKET_API_PASSPHRASE", "test-pass")
	t.Setenv("BUILDER_KEY", "builder-key")
	t.Setenv("BUILDER_SECRET", "builder-secret")
	t.Setenv("BUILDER_PASSPHRASE", "builder-pass")
	t.Setenv("TRADER_DRY_RUN", "1")
	t.Setenv("TRADER_PAPER_ALLOW_SHORT", "false")

	cfg := Default()
	cfg.ApplyEnv()

	if cfg.PrivateKey != "test-pk" {
		t.Fatalf("expected PrivateKey test-pk, got %s", cfg.PrivateKey)
	}
	if cfg.APIKey != "test-key" {
		t.Fatalf("expected APIKey test-key, got %s", cfg.APIKey)
	}
	if cfg.APISecret != "test-secret" {
		t.Fatalf("expected APISecret test-secret, got %s", cfg.APISecret)
	}
	if cfg.APIPassphrase != "test-pass" {
		t.Fatalf("expected APIPassphrase test-pass, got %s", cfg.APIPassphrase)
	}
	if cfg.BuilderKey != "builder-key" {
		t.Fatalf("expected BuilderKey builder-key, got %s", cfg.BuilderKey)
	}
	if cfg.BuilderSecret != "builder-secret" {
		t.Fatalf("expected BuilderSecret builder-secret, got %s", cfg.BuilderSecret)
	}
	if cfg.BuilderPassphrase != "builder-pass" {
		t.Fatalf("expected BuilderPassphrase builder-pass, got %s", cfg.BuilderPassphrase)
	}
	if !cfg.DryRun {
		t.Fatal("expected DryRun true from env '1'")
	}
	if cfg.Paper.AllowShort {
		t.Fatal("expected Paper.AllowShort false from env")
	}
}

func TestApplyEnvDryRunTrue(t *testing.T) {
	t.Setenv("TRADER_DRY_RUN", "true")
	cfg := Default()
	cfg.DryRun = false
	cfg.ApplyEnv()
	if !cfg.DryRun {
		t.Fatal("expected DryRun true from env 'true'")
	}
}

func TestApplyEnvTradingMode(t *testing.T) {
	t.Setenv("TRADER_TRADING_MODE", "LIVE")
	cfg := Default()
	cfg.ApplyEnv()
	if cfg.TradingMode != "live" {
		t.Fatalf("expected trading mode from env to be live, got %q", cfg.TradingMode)
	}
}

func TestApplyEnvPaperAllowShort(t *testing.T) {
	t.Setenv("TRADER_PAPER_ALLOW_SHORT", "1")
	cfg := Default()
	cfg.Paper.AllowShort = false
	cfg.ApplyEnv()
	if !cfg.Paper.AllowShort {
		t.Fatal("expected Paper.AllowShort true from env '1'")
	}
}
