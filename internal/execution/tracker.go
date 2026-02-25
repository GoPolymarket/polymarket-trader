package execution

import (
	"strconv"
	"sync"
	"time"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"
)

// OrderState tracks the lifecycle of a placed order.
type OrderState struct {
	ID         string
	AssetID    string
	Market     string
	Side       string
	Status     string
	Price      float64
	OrigSize   float64
	FilledSize float64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Fill represents a single trade execution.
type Fill struct {
	TradeID   string
	OrderID   string
	AssetID   string
	Side      string
	Price     float64
	Size      float64
	Timestamp time.Time
}

// Position tracks aggregated holdings for an asset.
type Position struct {
	AssetID       string
	NetSize       float64
	AvgEntryPrice float64
	RealizedPnL   float64
	TotalFills    int
}

// Tracker monitors orders, fills, and positions.
type Tracker struct {
	mu        sync.RWMutex
	orders    map[string]*OrderState // orderID -> state
	fills     []Fill
	positions map[string]*Position // assetID -> position
	OnFill    func(Fill)           // callback for risk integration
}

// NewTracker creates a Tracker ready to use.
func NewTracker() *Tracker {
	return &Tracker{
		orders:    make(map[string]*OrderState),
		positions: make(map[string]*Position),
	}
}

// RegisterOrder records a newly placed order.
func (t *Tracker) RegisterOrder(id, assetID, market, side string, price, size float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.orders[id] = &OrderState{
		ID:        id,
		AssetID:   assetID,
		Market:    market,
		Side:      side,
		Status:    "LIVE",
		Price:     price,
		OrigSize:  size,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// ProcessOrderEvent updates order state from a WebSocket order event.
func (t *Tracker) ProcessOrderEvent(ev ws.OrderEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()

	o, ok := t.orders[ev.ID]
	if !ok {
		// Order placed externally or before tracker started; create stub.
		price, _ := strconv.ParseFloat(ev.Price, 64)
		origSize, _ := strconv.ParseFloat(ev.OriginalSize, 64)
		matched, _ := strconv.ParseFloat(ev.SizeMatched, 64)
		o = &OrderState{
			ID:         ev.ID,
			AssetID:    ev.AssetID,
			Market:     ev.Market,
			Side:       ev.Side,
			Status:     ev.Status,
			Price:      price,
			OrigSize:   origSize,
			FilledSize: matched,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		t.orders[ev.ID] = o
		return
	}

	o.Status = ev.Status
	o.UpdatedAt = time.Now()
	if matched, err := strconv.ParseFloat(ev.SizeMatched, 64); err == nil {
		o.FilledSize = matched
	}
}

// ProcessTradeEvent records a fill and updates the position.
func (t *Tracker) ProcessTradeEvent(ev ws.TradeEvent) {
	price, _ := strconv.ParseFloat(ev.Price, 64)
	size, _ := strconv.ParseFloat(ev.Size, 64)
	if size == 0 {
		return
	}

	fill := Fill{
		TradeID:   ev.ID,
		AssetID:   ev.AssetID,
		Side:      ev.Side,
		Price:     price,
		Size:      size,
		Timestamp: time.Now(),
	}

	t.mu.Lock()
	t.fills = append(t.fills, fill)
	t.updatePosition(fill)
	cb := t.OnFill
	t.mu.Unlock()

	if cb != nil {
		cb(fill)
	}
}

// updatePosition adjusts the position for a fill. Caller must hold t.mu.
func (t *Tracker) updatePosition(f Fill) {
	pos, ok := t.positions[f.AssetID]
	if !ok {
		pos = &Position{AssetID: f.AssetID}
		t.positions[f.AssetID] = pos
	}
	pos.TotalFills++

	if f.Side == "BUY" {
		// Increasing long position — adjust cost basis.
		totalCost := pos.AvgEntryPrice*pos.NetSize + f.Price*f.Size
		pos.NetSize += f.Size
		if pos.NetSize > 0 {
			pos.AvgEntryPrice = totalCost / pos.NetSize
		}
	} else {
		// SELL — closing or going short.
		if pos.NetSize > 0 {
			// Closing portion of long — realize PnL.
			closedQty := f.Size
			if closedQty > pos.NetSize {
				closedQty = pos.NetSize
			}
			pos.RealizedPnL += (f.Price - pos.AvgEntryPrice) * closedQty
			pos.NetSize -= closedQty

			// If oversold, start short at this fill price.
			remaining := f.Size - closedQty
			if remaining > 0 {
				pos.NetSize = -remaining
				pos.AvgEntryPrice = f.Price
			}
			if pos.NetSize == 0 {
				pos.AvgEntryPrice = 0
			}
		} else {
			// Increasing short position.
			absCurrent := -pos.NetSize
			totalCost := pos.AvgEntryPrice*absCurrent + f.Price*f.Size
			pos.NetSize -= f.Size
			absNew := -pos.NetSize
			if absNew > 0 {
				pos.AvgEntryPrice = totalCost / absNew
			}
		}
	}
}

// Position returns the current position for an asset (nil if none).
func (t *Tracker) Position(assetID string) *Position {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.positions[assetID]
	if !ok {
		return nil
	}
	cp := *p
	return &cp
}

// Positions returns a snapshot of all positions.
func (t *Tracker) Positions() map[string]Position {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]Position, len(t.positions))
	for k, v := range t.positions {
		out[k] = *v
	}
	return out
}

// OpenOrderCount returns the number of orders with LIVE status.
func (t *Tracker) OpenOrderCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n := 0
	for _, o := range t.orders {
		if o.Status == "LIVE" {
			n++
		}
	}
	return n
}

// TotalFills returns the total number of recorded fills.
func (t *Tracker) TotalFills() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.fills)
}

// TotalRealizedPnL sums realized PnL across all positions.
func (t *Tracker) TotalRealizedPnL() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var total float64
	for _, p := range t.positions {
		total += p.RealizedPnL
	}
	return total
}

// OrderIDs returns IDs of all orders with the given status for an asset.
func (t *Tracker) OrderIDs(assetID, status string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var ids []string
	for _, o := range t.orders {
		if o.AssetID == assetID && o.Status == status {
			ids = append(ids, o.ID)
		}
	}
	return ids
}

// RecentFills returns the last N fills (most recent first).
func (t *Tracker) RecentFills(limit int) []Fill {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n := len(t.fills)
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]Fill, limit)
	for i := 0; i < limit; i++ {
		out[i] = t.fills[n-1-i]
	}
	return out
}

// ActiveOrders returns a snapshot of all LIVE orders.
func (t *Tracker) ActiveOrders() []OrderState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []OrderState
	for _, o := range t.orders {
		if o.Status == "LIVE" {
			out = append(out, *o)
		}
	}
	return out
}
