package config

import (
	"fmt"
	"strings"
)

// Validate checks high-impact runtime configuration constraints.
func (c Config) Validate() error {
	mode := strings.ToLower(strings.TrimSpace(c.TradingMode))
	if mode != "" && mode != "paper" && mode != "live" {
		return fmt.Errorf("trading_mode must be 'paper' or 'live', got %q", c.TradingMode)
	}

	if c.Paper.InitialBalanceUSDC <= 0 {
		return fmt.Errorf("paper.initial_balance_usdc must be > 0, got %f", c.Paper.InitialBalanceUSDC)
	}
	if c.Paper.FeeBps < 0 {
		return fmt.Errorf("paper.fee_bps must be >= 0, got %f", c.Paper.FeeBps)
	}
	if c.Paper.SlippageBps < 0 {
		return fmt.Errorf("paper.slippage_bps must be >= 0, got %f", c.Paper.SlippageBps)
	}

	if c.Risk.MaxDailyLossPct < 0 || c.Risk.MaxDailyLossPct > 1 {
		return fmt.Errorf("risk.max_daily_loss_pct must be within [0,1], got %f", c.Risk.MaxDailyLossPct)
	}
	if c.Risk.MaxDrawdownPct < 0 || c.Risk.MaxDrawdownPct > 1 {
		return fmt.Errorf("risk.max_drawdown_pct must be within [0,1], got %f", c.Risk.MaxDrawdownPct)
	}

	return nil
}
