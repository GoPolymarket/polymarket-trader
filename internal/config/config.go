package config

import (
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	PrivateKey        string `yaml:"private_key"`
	APIKey            string `yaml:"api_key"`
	APISecret         string `yaml:"api_secret"`
	APIPassphrase     string `yaml:"api_passphrase"`
	BuilderKey        string `yaml:"builder_key"`
	BuilderSecret     string `yaml:"builder_secret"`
	BuilderPassphrase string `yaml:"builder_passphrase"`

	ScanInterval time.Duration `yaml:"scan_interval"`
	DryRun       bool          `yaml:"dry_run"`
	LogLevel     string        `yaml:"log_level"`

	Maker    MakerConfig    `yaml:"maker"`
	Taker    TakerConfig    `yaml:"taker"`
	Risk     RiskConfig     `yaml:"risk"`
	Selector SelectorConfig `yaml:"selector"`
}

type MakerConfig struct {
	Enabled            bool          `yaml:"enabled"`
	Markets            []string      `yaml:"markets"`
	AutoSelectTop      int           `yaml:"auto_select_top"`
	MinSpreadBps       float64       `yaml:"min_spread_bps"`
	SpreadMultiplier   float64       `yaml:"spread_multiplier"`
	OrderSizeUSDC      float64       `yaml:"order_size_usdc"`
	RefreshInterval    time.Duration `yaml:"refresh_interval"`
	MaxOrdersPerMarket int           `yaml:"max_orders_per_market"`

	InventorySkewBps     float64 `yaml:"inventory_skew_bps"`
	InventoryWidenFactor float64 `yaml:"inventory_widen_factor"`
	MinOrderSizeUSDC     float64 `yaml:"min_order_size_usdc"`
}

type TakerConfig struct {
	Enabled          bool          `yaml:"enabled"`
	Markets          []string      `yaml:"markets"`
	MinImbalance     float64       `yaml:"min_imbalance"`
	DepthLevels      int           `yaml:"depth_levels"`
	AmountUSDC       float64       `yaml:"amount_usdc"`
	MaxSlippageBps   float64       `yaml:"max_slippage_bps"`
	Cooldown         time.Duration `yaml:"cooldown"`
	MinConfidenceBps float64       `yaml:"min_confidence_bps"`

	FlowWeight        float64       `yaml:"flow_weight"`
	ImbalanceWeight   float64       `yaml:"imbalance_weight"`
	ConvergenceWeight float64       `yaml:"convergence_weight"`
	MinConvergenceBps float64       `yaml:"min_convergence_bps"`
	FlowWindow        time.Duration `yaml:"flow_window"`
	MinCompositeScore float64       `yaml:"min_composite_score"`
}

type SelectorConfig struct {
	RescanInterval time.Duration `yaml:"rescan_interval"`
	MinLiquidity   float64       `yaml:"min_liquidity"`
	MinVolume24hr  float64       `yaml:"min_volume_24hr"`
	MaxSpread      float64       `yaml:"max_spread"`
	MinDaysToEnd   int           `yaml:"min_days_to_end"`
}

type RiskConfig struct {
	MaxOpenOrders        int           `yaml:"max_open_orders"`
	MaxDailyLossUSDC     float64       `yaml:"max_daily_loss_usdc"`
	MaxPositionPerMarket float64       `yaml:"max_position_per_market"`
	EmergencyStop        bool          `yaml:"emergency_stop"`
	StopLossPerMarket    float64       `yaml:"stop_loss_per_market"`
	MaxDrawdownPct       float64       `yaml:"max_drawdown_pct"`
	RiskSyncInterval     time.Duration `yaml:"risk_sync_interval"`
}

func Default() Config {
	return Config{
		ScanInterval: 10 * time.Second,
		DryRun:       true,
		LogLevel:     "info",
		Maker: MakerConfig{
			Enabled:              true,
			AutoSelectTop:        2,
			MinSpreadBps:         20,
			SpreadMultiplier:     1.5,
			OrderSizeUSDC:        2,
			RefreshInterval:      5 * time.Second,
			MaxOrdersPerMarket:   2,
			InventorySkewBps:     30,
			InventoryWidenFactor: 0.5,
			MinOrderSizeUSDC:     1,
		},
		Taker: TakerConfig{
			Enabled:           true,
			MinImbalance:      0.15,
			DepthLevels:       3,
			AmountUSDC:        2,
			MaxSlippageBps:    30,
			Cooldown:          60 * time.Second,
			MinConfidenceBps:  25,
			FlowWeight:        0.3,
			ImbalanceWeight:   0.5,
			ConvergenceWeight: 0.2,
			MinConvergenceBps: 50,
			FlowWindow:        2 * time.Minute,
			MinCompositeScore: 0.3,
		},
		Risk: RiskConfig{
			MaxOpenOrders:        6,
			MaxDailyLossUSDC:     2,
			MaxPositionPerMarket: 3,
			StopLossPerMarket:    1,
			MaxDrawdownPct:       0.30,
			RiskSyncInterval:     5 * time.Second,
		},
		Selector: SelectorConfig{
			RescanInterval: 5 * time.Minute,
			MinLiquidity:   1000,
			MinVolume24hr:  500,
			MaxSpread:      0.10,
			MinDaysToEnd:   2,
		},
	}
}

func LoadFile(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c *Config) ApplyEnv() {
	if v := os.Getenv("POLYMARKET_PK"); v != "" {
		c.PrivateKey = v
	}
	if v := os.Getenv("POLYMARKET_API_KEY"); v != "" {
		c.APIKey = v
	}
	if v := os.Getenv("POLYMARKET_API_SECRET"); v != "" {
		c.APISecret = v
	}
	if v := os.Getenv("POLYMARKET_API_PASSPHRASE"); v != "" {
		c.APIPassphrase = v
	}
	if v := os.Getenv("BUILDER_KEY"); v != "" {
		c.BuilderKey = v
	}
	if v := os.Getenv("BUILDER_SECRET"); v != "" {
		c.BuilderSecret = v
	}
	if v := os.Getenv("BUILDER_PASSPHRASE"); v != "" {
		c.BuilderPassphrase = v
	}
	if v := os.Getenv("TRADER_DRY_RUN"); v != "" {
		c.DryRun = strings.EqualFold(v, "true") || v == "1"
	}
}
