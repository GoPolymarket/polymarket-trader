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

func (m *Maker) ComputeQuote(book ws.OrderbookEvent) (Quote, error) {
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
		Size:      m.cfg.OrderSizeUSDC,
	}, nil
}
