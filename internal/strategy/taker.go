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

	FlowWeight        float64       // default 0.3
	ImbalanceWeight   float64       // default 0.5
	ConvergenceWeight float64       // default 0.2
	MinConvergenceBps float64       // default 50
	FlowWindow        time.Duration // default 2m
	MinCompositeScore float64       // default 0.3
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

// DetectConvergence checks if YES+NO prices deviate from $1 in a binary market.
// Returns the signal direction and edge in basis points.
func (tk *Taker) DetectConvergence(yesPrice, noPrice float64) (signal string, edgeBps float64) {
	sum := yesPrice + noPrice
	if sum == 0 {
		return "", 0
	}
	deviation := sum - 1.0
	edgeBps = math.Abs(deviation) * 10000

	if edgeBps < tk.cfg.MinConvergenceBps {
		return "", 0
	}

	// If sum > 1: both overpriced → sell the more expensive one.
	// If sum < 1: both underpriced → buy the cheaper one.
	if deviation > 0 {
		// Overpriced: sell whichever is higher.
		if yesPrice > noPrice {
			return "SELL", edgeBps
		}
		return "BUY", edgeBps // buy NO = sell YES effectively
	}
	// Underpriced: buy whichever is cheaper.
	if yesPrice < noPrice {
		return "BUY", edgeBps
	}
	return "SELL", edgeBps
}

// EvaluateEnhanced combines imbalance, flow, and convergence signals for a composite score.
func (tk *Taker) EvaluateEnhanced(book ws.OrderbookEvent, flow *FlowTracker, counterpartPrice float64) (*Signal, error) {
	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		return nil, fmt.Errorf("empty book for %s", book.AssetID)
	}

	tk.mu.Lock()
	if last, ok := tk.lastTrades[book.AssetID]; ok && time.Since(last) < tk.cfg.Cooldown {
		tk.mu.Unlock()
		return nil, nil
	}
	tk.mu.Unlock()

	// Compute imbalance.
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

	bestBid, _ := strconv.ParseFloat(book.Bids[0].Price, 64)
	bestAsk, _ := strconv.ParseFloat(book.Asks[0].Price, 64)
	mid := (bestBid + bestAsk) / 2

	// Get flow signal.
	var netFlow float64
	if flow != nil {
		netFlow = flow.NetFlow(book.AssetID)
	}

	// Get convergence edge.
	var convergenceEdge float64
	if counterpartPrice > 0 {
		_, convergenceEdge = tk.DetectConvergence(mid, counterpartPrice)
		convergenceEdge /= 10000 // normalize to 0–1 range
	}

	// Composite scoring.
	imbalanceW := tk.cfg.ImbalanceWeight
	flowW := tk.cfg.FlowWeight
	convergenceW := tk.cfg.ConvergenceWeight
	if imbalanceW == 0 && flowW == 0 && convergenceW == 0 {
		imbalanceW, flowW, convergenceW = 0.5, 0.3, 0.2
	}

	composite := imbalanceW*math.Abs(imbalance) + flowW*math.Abs(netFlow) + convergenceW*convergenceEdge

	minScore := tk.cfg.MinCompositeScore
	if minScore == 0 {
		minScore = 0.3
	}
	if composite < minScore {
		return nil, nil
	}

	// Determine direction from strongest signal.
	side := "BUY"
	buyScore := 0.0
	sellScore := 0.0
	if imbalance > 0 {
		buyScore += imbalanceW * imbalance
	} else {
		sellScore += imbalanceW * (-imbalance)
	}
	if netFlow > 0 {
		buyScore += flowW * netFlow
	} else {
		sellScore += flowW * (-netFlow)
	}
	if sellScore > buyScore {
		side = "SELL"
	}

	// Adaptive sizing: scale up to 1.5x at high confidence.
	amount := tk.cfg.AmountUSDC * math.Min(composite/0.5, 1.5)
	if amount < tk.cfg.AmountUSDC*0.5 {
		amount = tk.cfg.AmountUSDC * 0.5
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
		AmountUSDC: amount,
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
