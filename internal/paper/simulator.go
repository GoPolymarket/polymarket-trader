package paper

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"
)

type Config struct {
	InitialBalanceUSDC float64 `yaml:"initial_balance_usdc"`
	FeeBps             float64 `yaml:"fee_bps"`
	SlippageBps        float64 `yaml:"slippage_bps"`
	AllowShort         *bool   `yaml:"allow_short"`
}

type FillResult struct {
	OrderID    string
	TradeID    string
	AssetID    string
	Side       string
	Status     string
	Filled     bool
	Price      float64
	Size       float64
	AmountUSDC float64
	FeeUSDC    float64
	Timestamp  time.Time
}

type Snapshot struct {
	InitialBalanceUSDC float64 `json:"initial_balance_usdc"`
	BalanceUSDC        float64 `json:"balance_usdc"`
	FeesPaidUSDC       float64 `json:"fees_paid_usdc"`
	TotalVolumeUSDC    float64 `json:"total_volume_usdc"`
	TotalTrades        int     `json:"total_trades"`
	AllowShort         bool    `json:"allow_short"`
}

type Simulator struct {
	mu sync.Mutex

	cfg Config

	sequence        int64
	balanceUSDC     float64
	feesPaidUSDC    float64
	totalVolumeUSDC float64
	totalTrades     int
	allowShort      bool
	inventory       map[string]float64 // assetID -> token units (can go negative if shorting)
}

func NewSimulator(cfg Config) *Simulator {
	initial := cfg.InitialBalanceUSDC
	if initial <= 0 {
		initial = 1000
	}
	allowShort := true
	if cfg.AllowShort != nil {
		allowShort = *cfg.AllowShort
	}
	return &Simulator{
		cfg: Config{
			InitialBalanceUSDC: initial,
			FeeBps:             cfg.FeeBps,
			SlippageBps:        cfg.SlippageBps,
			AllowShort:         cfg.AllowShort,
		},
		balanceUSDC: initial,
		allowShort:  allowShort,
		inventory:   make(map[string]float64),
	}
}

func (s *Simulator) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		InitialBalanceUSDC: s.cfg.InitialBalanceUSDC,
		BalanceUSDC:        s.balanceUSDC,
		FeesPaidUSDC:       s.feesPaidUSDC,
		TotalVolumeUSDC:    s.totalVolumeUSDC,
		TotalTrades:        s.totalTrades,
		AllowShort:         s.allowShort,
	}
}

func (s *Simulator) ExecuteMarket(assetID, side string, amountUSDC float64, book ws.OrderbookEvent) (FillResult, error) {
	bestBid, bestAsk, err := topOfBook(book)
	if err != nil {
		return FillResult{}, err
	}
	side = strings.ToUpper(strings.TrimSpace(side))
	var price float64
	switch side {
	case "BUY":
		price = bestAsk
	case "SELL":
		price = bestBid
	default:
		return FillResult{}, fmt.Errorf("unsupported side: %s", side)
	}
	price = applySlippage(price, side, s.cfg.SlippageBps)
	return s.fill(assetID, side, amountUSDC, price, true)
}

func (s *Simulator) ExecuteLimit(assetID, side string, limitPrice, amountUSDC float64, book ws.OrderbookEvent) (FillResult, error) {
	bestBid, bestAsk, err := topOfBook(book)
	if err != nil {
		return FillResult{}, err
	}
	side = strings.ToUpper(strings.TrimSpace(side))

	fillable := false
	execPrice := limitPrice
	switch side {
	case "BUY":
		if bestAsk <= limitPrice {
			fillable = true
			execPrice = bestAsk
		}
	case "SELL":
		if bestBid >= limitPrice {
			fillable = true
			execPrice = bestBid
		}
	default:
		return FillResult{}, fmt.Errorf("unsupported side: %s", side)
	}

	if !fillable {
		return s.openOrder(assetID, side, limitPrice, amountUSDC), nil
	}
	execPrice = applySlippage(execPrice, side, s.cfg.SlippageBps)
	return s.fill(assetID, side, amountUSDC, execPrice, false)
}

func (s *Simulator) openOrder(assetID, side string, price, amountUSDC float64) FillResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	orderID := fmt.Sprintf("paper-order-%06d", s.sequence)
	size := 0.0
	if price > 0 {
		size = amountUSDC / price
	}
	return FillResult{
		OrderID:    orderID,
		AssetID:    assetID,
		Side:       side,
		Status:     "LIVE",
		Filled:     false,
		Price:      price,
		Size:       size,
		AmountUSDC: amountUSDC,
		Timestamp:  time.Now().UTC(),
	}
}

func (s *Simulator) fill(assetID, side string, amountUSDC, price float64, marketOrder bool) (FillResult, error) {
	if amountUSDC <= 0 {
		return FillResult{}, fmt.Errorf("amount_usdc must be positive")
	}
	if price <= 0 {
		return FillResult{}, fmt.Errorf("invalid execution price")
	}

	fee := amountUSDC * s.cfg.FeeBps / 10000
	size := amountUSDC / price

	s.mu.Lock()
	defer s.mu.Unlock()

	switch side {
	case "BUY":
		if amountUSDC+fee > s.balanceUSDC {
			return FillResult{}, fmt.Errorf("insufficient paper balance: need %.4f have %.4f", amountUSDC+fee, s.balanceUSDC)
		}
	case "SELL":
		if !s.allowShort {
			current := s.inventory[assetID]
			if current+1e-9 < size {
				return FillResult{}, fmt.Errorf("insufficient paper inventory: need %.8f have %.8f", size, current)
			}
		}
	default:
		return FillResult{}, fmt.Errorf("unsupported side: %s", side)
	}

	s.sequence++
	orderID := fmt.Sprintf("paper-order-%06d", s.sequence)
	s.sequence++
	tradeID := fmt.Sprintf("paper-trade-%06d", s.sequence)

	if side == "BUY" {
		s.balanceUSDC -= amountUSDC + fee
		s.inventory[assetID] += size
	} else { // SELL
		s.balanceUSDC += amountUSDC - fee
		s.inventory[assetID] -= size
		if s.inventory[assetID] > -1e-9 && s.inventory[assetID] < 1e-9 {
			delete(s.inventory, assetID)
		}
	}
	s.feesPaidUSDC += fee
	s.totalVolumeUSDC += amountUSDC
	s.totalTrades++

	status := "MATCHED"
	if marketOrder {
		status = "FILLED"
	}

	return FillResult{
		OrderID:    orderID,
		TradeID:    tradeID,
		AssetID:    assetID,
		Side:       side,
		Status:     status,
		Filled:     true,
		Price:      price,
		Size:       size,
		AmountUSDC: amountUSDC,
		FeeUSDC:    fee,
		Timestamp:  time.Now().UTC(),
	}, nil
}

func topOfBook(book ws.OrderbookEvent) (bestBid, bestAsk float64, err error) {
	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		return 0, 0, fmt.Errorf("missing top-of-book levels")
	}
	bestBid, err = strconv.ParseFloat(book.Bids[0].Price, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse bid: %w", err)
	}
	bestAsk, err = strconv.ParseFloat(book.Asks[0].Price, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse ask: %w", err)
	}
	return bestBid, bestAsk, nil
}

func applySlippage(price float64, side string, slippageBps float64) float64 {
	if slippageBps <= 0 {
		return price
	}
	multiplier := slippageBps / 10000
	if side == "BUY" {
		return price * (1 + multiplier)
	}
	return price * (1 - multiplier)
}
