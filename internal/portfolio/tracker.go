package portfolio

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/data"
	"github.com/ethereum/go-ethereum/common"
)

// PortfolioTracker periodically syncs positions and value from the Data API.
type PortfolioTracker struct {
	dataClient   data.Client
	userAddr     common.Address
	mu           sync.RWMutex
	positions    []data.Position
	totalValue   float64
	lastSync     time.Time
	syncInterval time.Duration
}

// NewTracker creates a PortfolioTracker that syncs at the given interval.
func NewTracker(dataClient data.Client, userAddr common.Address, syncInterval time.Duration) *PortfolioTracker {
	return &PortfolioTracker{
		dataClient:   dataClient,
		userAddr:     userAddr,
		syncInterval: syncInterval,
	}
}

// Sync fetches current positions and portfolio value from the Data API.
func (t *PortfolioTracker) Sync(ctx context.Context) error {
	positions, err := t.dataClient.Positions(ctx, &data.PositionsRequest{User: t.userAddr})
	if err != nil {
		return err
	}

	values, err := t.dataClient.Value(ctx, &data.ValueRequest{User: t.userAddr})
	if err != nil {
		return err
	}

	var totalValue float64
	for _, v := range values {
		f, _ := v.Value.Float64()
		totalValue += f
	}

	t.mu.Lock()
	t.positions = positions
	t.totalValue = totalValue
	t.lastSync = time.Now()
	t.mu.Unlock()
	return nil
}

// Positions returns cached positions.
func (t *PortfolioTracker) Positions() []data.Position {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.positions
}

// TotalValue returns the cached total portfolio value.
func (t *PortfolioTracker) TotalValue() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.totalValue
}

// LastSync returns the time of the last successful sync.
func (t *PortfolioTracker) LastSync() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastSync
}

// RecentTrades fetches recent trades from the Data API.
func (t *PortfolioTracker) RecentTrades(ctx context.Context, limit int) ([]data.Trade, error) {
	return t.dataClient.Trades(ctx, &data.TradesRequest{User: &t.userAddr, Limit: &limit})
}

// Run starts the periodic sync loop. Blocks until ctx is cancelled.
func (t *PortfolioTracker) Run(ctx context.Context) error {
	if err := t.Sync(ctx); err != nil {
		log.Printf("portfolio initial sync: %v", err)
	}

	ticker := time.NewTicker(t.syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := t.Sync(ctx); err != nil {
				log.Printf("portfolio sync: %v", err)
			}
		}
	}
}
