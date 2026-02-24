package strategy

import (
	"sync"
	"time"
)

// FlowSample records a single trade for order flow tracking.
type FlowSample struct {
	Side      string // BUY or SELL
	Size      float64
	Price     float64
	Timestamp time.Time
}

// FlowTracker tracks order flow in a rolling window per asset.
type FlowTracker struct {
	mu      sync.RWMutex
	window  time.Duration
	samples map[string][]FlowSample // assetID → rolling window
}

// NewFlowTracker creates a FlowTracker with the given window duration.
func NewFlowTracker(window time.Duration) *FlowTracker {
	return &FlowTracker{
		window:  window,
		samples: make(map[string][]FlowSample),
	}
}

// Record adds a trade sample to the tracker.
func (ft *FlowTracker) Record(assetID, side string, size, price float64) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.samples[assetID] = append(ft.samples[assetID], FlowSample{
		Side:      side,
		Size:      size,
		Price:     price,
		Timestamp: time.Now(),
	})
	ft.evict(assetID)
}

// NetFlow returns a normalized flow score from -1 (all sells) to +1 (all buys).
func (ft *FlowTracker) NetFlow(assetID string) float64 {
	ft.mu.RLock()
	defer ft.mu.RUnlock()

	cutoff := time.Now().Add(-ft.window)
	var buyVol, sellVol float64
	for _, s := range ft.samples[assetID] {
		if s.Timestamp.Before(cutoff) {
			continue
		}
		if s.Side == "BUY" {
			buyVol += s.Size
		} else {
			sellVol += s.Size
		}
	}
	total := buyVol + sellVol
	if total == 0 {
		return 0
	}
	return (buyVol - sellVol) / total
}

// VWAP returns the volume-weighted average price for recent trades.
func (ft *FlowTracker) VWAP(assetID string) float64 {
	ft.mu.RLock()
	defer ft.mu.RUnlock()

	cutoff := time.Now().Add(-ft.window)
	var totalSize, totalNotional float64
	for _, s := range ft.samples[assetID] {
		if s.Timestamp.Before(cutoff) {
			continue
		}
		totalSize += s.Size
		totalNotional += s.Price * s.Size
	}
	if totalSize == 0 {
		return 0
	}
	return totalNotional / totalSize
}

// evict removes expired samples. Caller must hold ft.mu.
func (ft *FlowTracker) evict(assetID string) {
	cutoff := time.Now().Add(-ft.window)
	samples := ft.samples[assetID]
	i := 0
	for i < len(samples) && samples[i].Timestamp.Before(cutoff) {
		i++
	}
	if i > 0 {
		ft.samples[assetID] = samples[i:]
	}
}
