package strategy

import (
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"
)

type TakerConfig struct {
	MinImbalance     float64
	DepthLevels      int
	AmountUSDC       float64
	MaxSlippageBps   float64
	Cooldown         time.Duration
	MinConfidenceBps float64
}

type Signal struct {
	AssetID    string
	Side       string
	AmountUSDC float64
	MaxPrice   float64
	Mid        float64
	Imbalance  float64
}

type Taker struct {
	cfg        TakerConfig
	mu         sync.Mutex
	lastTrades map[string]time.Time
}

func NewTaker(cfg TakerConfig) *Taker {
	return &Taker{
		cfg:        cfg,
		lastTrades: make(map[string]time.Time),
	}
}

func (tk *Taker) Evaluate(book ws.OrderbookEvent) (*Signal, error) {
	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		return nil, fmt.Errorf("empty book for %s", book.AssetID)
	}

	tk.mu.Lock()
	if last, ok := tk.lastTrades[book.AssetID]; ok && time.Since(last) < tk.cfg.Cooldown {
		tk.mu.Unlock()
		return nil, nil
	}
	tk.mu.Unlock()

	var bidDepth, askDepth float64
	for i := 0; i < tk.cfg.DepthLevels && i < len(book.Bids); i++ {
		size, _ := strconv.ParseFloat(book.Bids[i].Size, 64)
		bidDepth += size
	}
	for i := 0; i < tk.cfg.DepthLevels && i < len(book.Asks); i++ {
		size, _ := strconv.ParseFloat(book.Asks[i].Size, 64)
		askDepth += size
	}

	totalDepth := bidDepth + askDepth
	if totalDepth == 0 {
		return nil, nil
	}

	imbalance := (bidDepth - askDepth) / totalDepth
	if math.Abs(imbalance) < tk.cfg.MinImbalance {
		return nil, nil
	}

	bestBid, _ := strconv.ParseFloat(book.Bids[0].Price, 64)
	bestAsk, _ := strconv.ParseFloat(book.Asks[0].Price, 64)
	mid := (bestBid + bestAsk) / 2

	side := "BUY"
	if imbalance < 0 {
		side = "SELL"
	}

	delta := mid * tk.cfg.MaxSlippageBps / 10000
	maxPrice := mid + delta
	if side == "SELL" {
		maxPrice = mid - delta
		if maxPrice <= 0 {
			maxPrice = 0.01
		}
	}

	return &Signal{
		AssetID:    book.AssetID,
		Side:       side,
		AmountUSDC: tk.cfg.AmountUSDC,
		MaxPrice:   maxPrice,
		Mid:        mid,
		Imbalance:  imbalance,
	}, nil
}

func (tk *Taker) RecordTrade(assetID string) {
	tk.mu.Lock()
	defer tk.mu.Unlock()
	tk.lastTrades[assetID] = time.Now()
}
