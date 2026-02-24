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
