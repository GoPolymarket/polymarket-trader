package strategy

import (
	"testing"
	"time"
)

func TestCryptoSignalTrackerNoMapping(t *testing.T) {
	tracker := NewCryptoSignalTracker(CryptoSignalConfig{
		MinPriceChangePct: 0.02,
		DefaultAmountUSDC: 1,
	})

	signals := tracker.ProcessPrice(CryptoPriceUpdate{
		Symbol:    "BTCUSDT",
		Price:     100000,
		Timestamp: time.Now(),
	})

	if len(signals) != 0 {
		t.Fatalf("expected 0 signals without mapping, got %d", len(signals))
	}
}

func TestCryptoSignalTrackerSmallMove(t *testing.T) {
	tracker := NewCryptoSignalTracker(CryptoSignalConfig{
		MinPriceChangePct: 0.02, // 2%
		DefaultAmountUSDC: 1,
	})
	tracker.AddMapping("BTCUSDT", []string{"btc-asset-1"})

	// First price point.
	tracker.ProcessPrice(CryptoPriceUpdate{
		Symbol:    "BTCUSDT",
		Price:     100000,
		Timestamp: time.Now(),
	})

	// Small move (0.5%) — should not trigger.
	signals := tracker.ProcessPrice(CryptoPriceUpdate{
		Symbol:    "BTCUSDT",
		Price:     100500,
		Timestamp: time.Now(),
	})

	if len(signals) != 0 {
		t.Fatalf("expected 0 signals for small move, got %d", len(signals))
	}
}

func TestCryptoSignalTrackerLargeMove(t *testing.T) {
	tracker := NewCryptoSignalTracker(CryptoSignalConfig{
		MinPriceChangePct: 0.02, // 2%
		DefaultAmountUSDC: 1,
		Cooldown:          1 * time.Second,
	})
	tracker.AddMapping("BTCUSDT", []string{"btc-asset-1"})

	// First price point.
	tracker.ProcessPrice(CryptoPriceUpdate{
		Symbol:    "BTCUSDT",
		Price:     100000,
		Timestamp: time.Now(),
	})

	// Large move (3%) — should trigger BUY signal.
	signals := tracker.ProcessPrice(CryptoPriceUpdate{
		Symbol:    "BTCUSDT",
		Price:     103000,
		Timestamp: time.Now(),
	})

	if len(signals) != 1 {
		t.Fatalf("expected 1 signal for large move, got %d", len(signals))
	}
	if signals[0].Side != "BUY" {
		t.Errorf("expected BUY for upward move, got %s", signals[0].Side)
	}
	if signals[0].MarketAssetID != "btc-asset-1" {
		t.Errorf("expected btc-asset-1, got %s", signals[0].MarketAssetID)
	}
	if signals[0].CryptoSymbol != "BTCUSDT" {
		t.Errorf("expected BTCUSDT, got %s", signals[0].CryptoSymbol)
	}
}

func TestCryptoSignalTrackerDownwardMove(t *testing.T) {
	tracker := NewCryptoSignalTracker(CryptoSignalConfig{
		MinPriceChangePct: 0.02,
		DefaultAmountUSDC: 1,
		Cooldown:          1 * time.Second,
	})
	tracker.AddMapping("ETHUSDT", []string{"eth-asset-1"})

	tracker.ProcessPrice(CryptoPriceUpdate{
		Symbol:    "ETHUSDT",
		Price:     3000,
		Timestamp: time.Now(),
	})

	// 5% drop.
	signals := tracker.ProcessPrice(CryptoPriceUpdate{
		Symbol:    "ETHUSDT",
		Price:     2850,
		Timestamp: time.Now(),
	})

	if len(signals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(signals))
	}
	if signals[0].Side != "SELL" {
		t.Errorf("expected SELL for downward move, got %s", signals[0].Side)
	}
}

func TestCryptoSignalTrackerCooldown(t *testing.T) {
	tracker := NewCryptoSignalTracker(CryptoSignalConfig{
		MinPriceChangePct: 0.02,
		DefaultAmountUSDC: 1,
		Cooldown:          10 * time.Minute, // long cooldown
	})
	tracker.AddMapping("BTCUSDT", []string{"btc-asset-1"})

	tracker.ProcessPrice(CryptoPriceUpdate{Symbol: "BTCUSDT", Price: 100000, Timestamp: time.Now()})

	// First large move triggers.
	signals := tracker.ProcessPrice(CryptoPriceUpdate{Symbol: "BTCUSDT", Price: 103000, Timestamp: time.Now()})
	if len(signals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(signals))
	}

	// Second move should be blocked by cooldown.
	signals = tracker.ProcessPrice(CryptoPriceUpdate{Symbol: "BTCUSDT", Price: 106000, Timestamp: time.Now()})
	if len(signals) != 0 {
		t.Fatalf("expected 0 signals during cooldown, got %d", len(signals))
	}
}

func TestCryptoSignalTrackerTrackedSymbols(t *testing.T) {
	tracker := NewCryptoSignalTracker(CryptoSignalConfig{})
	tracker.AddMapping("BTCUSDT", []string{"a1"})
	tracker.AddMapping("ETHUSDT", []string{"a2"})

	symbols := tracker.TrackedSymbols()
	if len(symbols) != 2 {
		t.Fatalf("expected 2 tracked symbols, got %d", len(symbols))
	}
}

func TestCryptoSignalTrackerSetMapping(t *testing.T) {
	tracker := NewCryptoSignalTracker(CryptoSignalConfig{})
	tracker.SetMapping(map[string][]string{
		"BTCUSDT": {"a1", "a2"},
		"ETHUSDT": {"a3"},
	})

	symbols := tracker.TrackedSymbols()
	if len(symbols) != 2 {
		t.Fatalf("expected 2 tracked symbols, got %d", len(symbols))
	}
}
