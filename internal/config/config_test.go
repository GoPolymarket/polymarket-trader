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
	f.Write([]byte(yaml))
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
