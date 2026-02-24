package feed

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"
)

// BookSnapshot maintains an in-memory orderbook snapshot per asset.
type BookSnapshot struct {
	mu    sync.RWMutex
	books map[string]ws.OrderbookEvent
}

func NewBookSnapshot() *BookSnapshot {
	return &BookSnapshot{books: make(map[string]ws.OrderbookEvent)}
}

func (s *BookSnapshot) Update(event ws.OrderbookEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.books[event.AssetID] = event
}

func (s *BookSnapshot) Get(assetID string) (ws.OrderbookEvent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.books[assetID]
	return b, ok
}

func (s *BookSnapshot) Mid(assetID string) (float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.books[assetID]
	if !ok || len(b.Bids) == 0 || len(b.Asks) == 0 {
		return 0, fmt.Errorf("no book for %s", assetID)
	}
	bid, err := strconv.ParseFloat(b.Bids[0].Price, 64)
	if err != nil {
		return 0, err
	}
	ask, err := strconv.ParseFloat(b.Asks[0].Price, 64)
	if err != nil {
		return 0, err
	}
	return (bid + ask) / 2, nil
}

// Depth returns total bid and ask depth for the top n levels.
func (s *BookSnapshot) Depth(assetID string, levels int) (bidDepth, askDepth float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.books[assetID]
	if !ok {
		return 0, 0
	}
	for i := 0; i < levels && i < len(b.Bids); i++ {
		size, _ := strconv.ParseFloat(b.Bids[i].Size, 64)
		bidDepth += size
	}
	for i := 0; i < levels && i < len(b.Asks); i++ {
		size, _ := strconv.ParseFloat(b.Asks[i].Size, 64)
		askDepth += size
	}
	return bidDepth, askDepth
}

// AssetIDs returns all tracked assets.
func (s *BookSnapshot) AssetIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.books))
	for id := range s.books {
		ids = append(ids, id)
	}
	return ids
}
