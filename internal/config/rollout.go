package config

import (
	"fmt"
	"strings"
)

// ApplyRolloutPhase applies a staged rollout preset to the config.
// Supported phases:
// - paper:       paper mode, real paper fills (dry_run=false)
// - shadow:      live mode, dry-run only (no order placement)
// - live-small:  live mode with conservative small-size caps
// - live:        live mode using configured values
func ApplyRolloutPhase(cfg *Config, phase string) error {
	p := strings.ToLower(strings.TrimSpace(phase))
	if p == "" {
		return nil
	}

	switch p {
	case "paper":
		cfg.TradingMode = "paper"
		cfg.DryRun = false
	case "shadow", "live-dryrun", "live-dry-run":
		cfg.TradingMode = "live"
		cfg.DryRun = true
	case "live-small", "small":
		cfg.TradingMode = "live"
		cfg.DryRun = false

		clampMaxInt(&cfg.Risk.MaxOpenOrders, 4)
		clampMaxFloat(&cfg.Maker.OrderSizeUSDC, 1)
		clampMaxFloat(&cfg.Taker.AmountUSDC, 1)
		clampMaxFloat(&cfg.Risk.MaxPositionPerMarket, 3)
		clampMaxFloat(&cfg.Risk.MaxDailyLossPct, 0.01)
		if cfg.Risk.AccountCapitalUSDC <= 0 {
			cfg.Risk.AccountCapitalUSDC = 1000
		}
	case "live":
		cfg.TradingMode = "live"
		cfg.DryRun = false
	default:
		return fmt.Errorf("unknown rollout phase %q (supported: paper|shadow|live-small|live)", phase)
	}

	return nil
}

func clampMaxFloat(v *float64, max float64) {
	if max <= 0 {
		return
	}
	if *v <= 0 || *v > max {
		*v = max
	}
}

func clampMaxInt(v *int, max int) {
	if max <= 0 {
		return
	}
	if *v <= 0 || *v > max {
		*v = max
	}
}
