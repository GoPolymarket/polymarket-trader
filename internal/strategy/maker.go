package strategy

import (
	"fmt"
	"math"
	"strconv"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"
)

type MakerConfig struct {
	MinSpreadBps       float64
	SpreadMultiplier   float64
	OrderSizeUSDC      float64
	MaxOrdersPerMarket int

	InventorySkewBps     float64 // default 30
	InventoryWidenFactor float64 // default 0.5
	MinOrderSizeUSDC     float64 // default 5
}

type InventoryState struct {
	NetPosition   float64
	MaxPosition   float64
	AvgEntryPrice float64
}

type Quote struct {
	AssetID   string
	BuyPrice  float64
	SellPrice float64
	Size      float64
}

type Maker struct {
	cfg MakerConfig
}

func NewMaker(cfg MakerConfig) *Maker {
	return &Maker{cfg: cfg}
}

// ComputeQuote calculates bid/ask prices with optional inventory adjustment.
func (m *Maker) ComputeQuote(book ws.OrderbookEvent, inv ...InventoryState) (Quote, error) {
	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		return Quote{}, fmt.Errorf("empty book for %s", book.AssetID)
	}

	bestBid, err := strconv.ParseFloat(book.Bids[0].Price, 64)
	if err != nil {
		return Quote{}, fmt.Errorf("bad bid price: %w", err)
	}
	bestAsk, err := strconv.ParseFloat(book.Asks[0].Price, 64)
	if err != nil {
		return Quote{}, fmt.Errorf("bad ask price: %w", err)
	}
	if bestAsk <= bestBid {
		return Quote{}, fmt.Errorf("crossed book: bid=%f ask=%f", bestBid, bestAsk)
	}

	mid := (bestBid + bestAsk) / 2
	marketSpreadBps := (bestAsk - bestBid) / mid * 10000

	halfSpreadBps := math.Max(m.cfg.MinSpreadBps/2, marketSpreadBps*m.cfg.SpreadMultiplier/2)

	size := m.cfg.OrderSizeUSDC

	// Apply inventory adjustments if provided.
	var invRatio float64
	if len(inv) > 0 && inv[0].MaxPosition > 0 {
		is := inv[0]
		invRatio = is.NetPosition / is.MaxPosition
		if invRatio > 1 {
			invRatio = 1
		} else if invRatio < -1 {
			invRatio = -1
		}

		// Skew midpoint: if long, shift mid down (sell cheaper to reduce inventory).
		skewBps := invRatio * m.cfg.InventorySkewBps
		mid -= mid * skewBps / 10000

		// Widen spread at high inventory.
		widening := 1 + math.Abs(invRatio)*m.cfg.InventoryWidenFactor
		halfSpreadBps *= widening

		// Reduce size at high inventory.
		size *= (1 - math.Abs(invRatio)*0.5)
		if m.cfg.MinOrderSizeUSDC > 0 && size < m.cfg.MinOrderSizeUSDC {
			size = m.cfg.MinOrderSizeUSDC
		}
	}

	halfSpread := mid * halfSpreadBps / 10000

	buyPrice := mid - halfSpread
	sellPrice := mid + halfSpread

	if buyPrice <= 0 {
		buyPrice = 0.01
	}
	if sellPrice >= 1 {
		sellPrice = 0.99
	}

	return Quote{
		AssetID:   book.AssetID,
		BuyPrice:  buyPrice,
		SellPrice: sellPrice,
		Size:      size,
	}, nil
}
