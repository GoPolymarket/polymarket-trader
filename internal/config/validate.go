package config

import (
	"fmt"
	"net"
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
	if c.BuilderSyncInterval <= 0 {
		return fmt.Errorf("builder_sync_interval must be > 0, got %s", c.BuilderSyncInterval)
	}
	if c.API.Enabled {
		addr := strings.TrimSpace(c.API.Addr)
		if addr == "" {
			return fmt.Errorf("api.addr must be set when api.enabled=true")
		}
		if strings.TrimSpace(c.API.Token) == "" && !isLoopbackAddr(addr) {
			return fmt.Errorf("api.token is required when api.enabled=true and api.addr is not loopback")
		}
	}

	if c.Risk.MaxOpenOrders <= 0 {
		return fmt.Errorf("risk.max_open_orders must be > 0, got %d", c.Risk.MaxOpenOrders)
	}
	if c.Risk.MaxDailyLossUSDC < 0 {
		return fmt.Errorf("risk.max_daily_loss_usdc must be >= 0, got %f", c.Risk.MaxDailyLossUSDC)
	}
	if c.Risk.AccountCapitalUSDC < 0 {
		return fmt.Errorf("risk.account_capital_usdc must be >= 0, got %f", c.Risk.AccountCapitalUSDC)
	}
	if c.Risk.MaxPositionPerMarket <= 0 {
		return fmt.Errorf("risk.max_position_per_market must be > 0, got %f", c.Risk.MaxPositionPerMarket)
	}
	if c.Risk.MaxDailyLossPct < 0 || c.Risk.MaxDailyLossPct > 1 {
		return fmt.Errorf("risk.max_daily_loss_pct must be within [0,1], got %f", c.Risk.MaxDailyLossPct)
	}
	if c.Risk.MaxDrawdownPct < 0 || c.Risk.MaxDrawdownPct > 1 {
		return fmt.Errorf("risk.max_drawdown_pct must be within [0,1], got %f", c.Risk.MaxDrawdownPct)
	}
	if c.Risk.RiskSyncInterval <= 0 {
		return fmt.Errorf("risk.risk_sync_interval must be > 0, got %s", c.Risk.RiskSyncInterval)
	}
	if c.Risk.MaxConsecutiveLosses < 0 {
		return fmt.Errorf("risk.max_consecutive_losses must be >= 0, got %d", c.Risk.MaxConsecutiveLosses)
	}
	if c.Risk.ConsecutiveLossCooldown < 0 {
		return fmt.Errorf("risk.consecutive_loss_cooldown must be >= 0, got %s", c.Risk.ConsecutiveLossCooldown)
	}

	return nil
}

func isLoopbackAddr(addr string) bool {
	host := strings.TrimSpace(addr)
	if strings.HasPrefix(host, ":") {
		return false
	}

	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
