package strategy

import (
	"fmt"
	"log"
	"math"
	"sync"
	"time"
)

// CryptoPriceUpdate is a simplified crypto price event.
type CryptoPriceUpdate struct {
	Symbol    string
	Price     float64
	Timestamp time.Time
}

// CryptoSignal represents a trading signal derived from crypto price movements.
type CryptoSignal struct {
	MarketAssetID string  // Polymarket asset to trade
	Side          string  // BUY or SELL
	AmountUSDC    float64 // suggested trade size
	Reason        string  // human-readable reason
	CryptoSymbol  string  // which crypto triggered the signal
	PriceChangePct float64 // magnitude of the crypto price change
}

// CryptoSignalConfig configures the crypto-correlated strategy.
type CryptoSignalConfig struct {
	MinPriceChangePct float64       // minimum % move to trigger signal (e.g., 0.02 = 2%)
	Cooldown          time.Duration // minimum time between signals for same market
	DefaultAmountUSDC float64       // base trade amount
}

// CryptoSignalTracker monitors crypto prices and generates signals for correlated prediction markets.
type CryptoSignalTracker struct {
	cfg CryptoSignalConfig
	mu  sync.RWMutex

	// marketMapping maps crypto symbol → list of correlated Polymarket asset IDs.
	// Example: "BTCUSDT" → ["asset123_btc_100k_yes", "asset456_btc_100k_no"]
	marketMapping map[string][]string

	// lastPrices tracks rolling price data per symbol.
	lastPrices map[string]*priceWindow

	// lastSignals tracks cooldowns.
	lastSignals map[string]time.Time
}

type priceWindow struct {
	prices    []pricePoint
	maxPoints int
}

type pricePoint struct {
	price     float64
	timestamp time.Time
}

// NewCryptoSignalTracker creates a tracker for crypto-correlated trading.
func NewCryptoSignalTracker(cfg CryptoSignalConfig) *CryptoSignalTracker {
	if cfg.MinPriceChangePct == 0 {
		cfg.MinPriceChangePct = 0.02 // 2% default
	}
	if cfg.Cooldown == 0 {
		cfg.Cooldown = 5 * time.Minute
	}
	if cfg.DefaultAmountUSDC == 0 {
		cfg.DefaultAmountUSDC = 1
	}
	return &CryptoSignalTracker{
		cfg:           cfg,
		marketMapping: make(map[string][]string),
		lastPrices:    make(map[string]*priceWindow),
		lastSignals:   make(map[string]time.Time),
	}
}

// SetMapping sets the crypto symbol → Polymarket asset mapping.
func (t *CryptoSignalTracker) SetMapping(mapping map[string][]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.marketMapping = mapping
}

// AddMapping associates a crypto symbol with Polymarket asset IDs.
func (t *CryptoSignalTracker) AddMapping(cryptoSymbol string, assetIDs []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.marketMapping[cryptoSymbol] = assetIDs
}

// ProcessPrice updates price data and returns any triggered signals.
func (t *CryptoSignalTracker) ProcessPrice(update CryptoPriceUpdate) []CryptoSignal {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Update price window.
	pw, ok := t.lastPrices[update.Symbol]
	if !ok {
		pw = &priceWindow{maxPoints: 60} // keep ~1 minute of ticks
		t.lastPrices[update.Symbol] = pw
	}
	pw.prices = append(pw.prices, pricePoint{price: update.Price, timestamp: update.Timestamp})
	if len(pw.prices) > pw.maxPoints {
		pw.prices = pw.prices[len(pw.prices)-pw.maxPoints:]
	}

	// Need at least 2 points to detect movement.
	if len(pw.prices) < 2 {
		return nil
	}

	// Check correlated markets.
	assetIDs, ok := t.marketMapping[update.Symbol]
	if !ok || len(assetIDs) == 0 {
		return nil
	}

	// Calculate price change from oldest point in window.
	oldPrice := pw.prices[0].price
	if oldPrice == 0 {
		return nil
	}
	changePct := (update.Price - oldPrice) / oldPrice

	if math.Abs(changePct) < t.cfg.MinPriceChangePct {
		return nil
	}

	// Generate signals for correlated markets.
	var signals []CryptoSignal
	for _, assetID := range assetIDs {
		// Check cooldown.
		if last, exists := t.lastSignals[assetID]; exists && time.Since(last) < t.cfg.Cooldown {
			continue
		}

		// Direction: if crypto goes up, bet YES on upside markets.
		// The specific side depends on whether the asset is a YES or NO token,
		// which we determine by convention: first asset in list = YES direction.
		side := "BUY"
		if changePct < 0 {
			side = "SELL"
		}

		// Scale amount by magnitude of move (capped at 2x).
		scale := math.Min(math.Abs(changePct)/t.cfg.MinPriceChangePct, 2.0)
		amount := t.cfg.DefaultAmountUSDC * scale

		signals = append(signals, CryptoSignal{
			MarketAssetID:  assetID,
			Side:           side,
			AmountUSDC:     amount,
			Reason:         update.Symbol + " moved " + formatPct(changePct),
			CryptoSymbol:   update.Symbol,
			PriceChangePct: changePct,
		})

		t.lastSignals[assetID] = time.Now()
		log.Printf("crypto signal: %s %s (%.2f%% move in %s)", side, assetID, changePct*100, update.Symbol)
	}

	return signals
}

// TrackedSymbols returns the crypto symbols being tracked.
func (t *CryptoSignalTracker) TrackedSymbols() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var symbols []string
	for s := range t.marketMapping {
		symbols = append(symbols, s)
	}
	return symbols
}

func formatPct(v float64) string {
	sign := "+"
	if v < 0 {
		sign = ""
	}
	return sign + fmt.Sprintf("%.2f%%", v*100)
}
