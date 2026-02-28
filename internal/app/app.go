package app

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/auth"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/clobtypes"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/heartbeat"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/data"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/gamma"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/rtds"

	"github.com/GoPolymarket/polymarket-trader/internal/builder"
	"github.com/GoPolymarket/polymarket-trader/internal/config"
	"github.com/GoPolymarket/polymarket-trader/internal/execution"
	"github.com/GoPolymarket/polymarket-trader/internal/feed"
	"github.com/GoPolymarket/polymarket-trader/internal/notify"
	"github.com/GoPolymarket/polymarket-trader/internal/paper"
	"github.com/GoPolymarket/polymarket-trader/internal/portfolio"
	"github.com/GoPolymarket/polymarket-trader/internal/risk"
	"github.com/GoPolymarket/polymarket-trader/internal/strategy"
)

type App struct {
	cfg         config.Config
	clobClient  clob.Client
	wsClient    ws.Client
	signer      auth.Signer
	gammaClient gamma.Client
	dataClient  data.Client

	books   *feed.BookSnapshot
	riskMgr *risk.Manager
	maker   *strategy.Maker
	taker   *strategy.Taker
	tracker *execution.Tracker

	// Phase 1.1: FlowTracker for enhanced taker signals.
	flowTracker *strategy.FlowTracker
	// tokenPairs maps assetID → counterpart assetID (YES↔NO in binary markets).
	tokenPairs map[string]string

	// Phase 1.4: Heartbeat keepalive.
	heartbeatClient heartbeat.Client

	// Phase 2.1: Portfolio tracker.
	Portfolio *portfolio.PortfolioTracker

	// Phase 2.2: Builder volume tracker.
	BuilderTracker *builder.VolumeTracker

	// Phase 2.4: Telegram notifications.
	notifier Notifier

	activeOrders  map[string][]string
	assetToMarket map[string]string // assetID → market/condition ID

	gammaSelector *strategy.GammaSelector

	// Fee rate cache for fee-aware maker pricing.
	feeRates map[string]float64 // assetID → fee rate bps

	// Phase 3.2: RTDS crypto-correlated trading.
	rtdsClient    rtds.Client
	cryptoTracker *strategy.CryptoSignalTracker

	lastRealizedPnL       float64
	realizedInitialized   bool
	dailyRealizedBaseline float64
	dailyBaselineSet      bool
	tradingMode           string
	paperSim              *paper.Simulator

	mu      sync.RWMutex
	running bool
}

// Notifier defines alert methods used by the trading app.
type Notifier interface {
	NotifyFill(ctx context.Context, assetID, side string, price, size float64) error
	NotifyStopLoss(ctx context.Context, assetID string, pnl float64) error
	NotifyEmergencyStop(ctx context.Context) error
	NotifyDailySummary(ctx context.Context, pnl float64, fills int, volume float64) error
	NotifyRiskCooldown(ctx context.Context, consecutiveLosses, maxConsecutiveLosses int, cooldownRemaining time.Duration) error
}

func New(cfg config.Config, clobClient clob.Client, wsClient ws.Client, signer auth.Signer, gammaClient gamma.Client, dataClient data.Client, rtdsClient rtds.Client) *App {
	tracker := execution.NewTracker()
	riskMgr := risk.New(risk.Config{
		MaxOpenOrders:           cfg.Risk.MaxOpenOrders,
		MaxDailyLossUSDC:        cfg.Risk.MaxDailyLossUSDC,
		MaxDailyLossPct:         cfg.Risk.MaxDailyLossPct,
		AccountCapitalUSDC:      cfg.Risk.AccountCapitalUSDC,
		MaxPositionPerMarket:    cfg.Risk.MaxPositionPerMarket,
		StopLossPerMarket:       cfg.Risk.StopLossPerMarket,
		MaxDrawdownPct:          cfg.Risk.MaxDrawdownPct,
		RiskSyncInterval:        cfg.Risk.RiskSyncInterval,
		MaxConsecutiveLosses:    cfg.Risk.MaxConsecutiveLosses,
		ConsecutiveLossCooldown: cfg.Risk.ConsecutiveLossCooldown,
	})

	// Phase 2.4: Telegram notifier.
	var notifier Notifier
	if cfg.Telegram.Enabled {
		notifier = notify.NewNotifier(cfg.Telegram.BotToken, cfg.Telegram.ChatID)
	}

	// Phase 1.1: FlowTracker.
	flowWindow := cfg.Taker.FlowWindow
	if flowWindow <= 0 {
		flowWindow = 2 * time.Minute
	}
	flowTracker := strategy.NewFlowTracker(flowWindow)
	tradingMode := strings.ToLower(strings.TrimSpace(cfg.TradingMode))
	if tradingMode == "" {
		tradingMode = "paper"
	}
	if tradingMode != "live" && tradingMode != "paper" {
		tradingMode = "paper"
	}

	a := &App{
		cfg:         cfg,
		clobClient:  clobClient,
		wsClient:    wsClient,
		signer:      signer,
		gammaClient: gammaClient,
		dataClient:  dataClient,
		books:       feed.NewBookSnapshot(),
		riskMgr:     riskMgr,
		maker: strategy.NewMaker(strategy.MakerConfig{
			MinSpreadBps:         cfg.Maker.MinSpreadBps,
			SpreadMultiplier:     cfg.Maker.SpreadMultiplier,
			OrderSizeUSDC:        cfg.Maker.OrderSizeUSDC,
			MaxOrdersPerMarket:   cfg.Maker.MaxOrdersPerMarket,
			InventorySkewBps:     cfg.Maker.InventorySkewBps,
			InventoryWidenFactor: cfg.Maker.InventoryWidenFactor,
			MinOrderSizeUSDC:     cfg.Maker.MinOrderSizeUSDC,
		}),
		taker: strategy.NewTaker(strategy.TakerConfig{
			MinImbalance:      cfg.Taker.MinImbalance,
			DepthLevels:       cfg.Taker.DepthLevels,
			AmountUSDC:        cfg.Taker.AmountUSDC,
			MaxSlippageBps:    cfg.Taker.MaxSlippageBps,
			Cooldown:          cfg.Taker.Cooldown,
			MinConfidenceBps:  cfg.Taker.MinConfidenceBps,
			FlowWeight:        cfg.Taker.FlowWeight,
			ImbalanceWeight:   cfg.Taker.ImbalanceWeight,
			ConvergenceWeight: cfg.Taker.ConvergenceWeight,
			MinConvergenceBps: cfg.Taker.MinConvergenceBps,
			FlowWindow:        cfg.Taker.FlowWindow,
			MinCompositeScore: cfg.Taker.MinCompositeScore,
		}),
		tracker:       tracker,
		flowTracker:   flowTracker,
		tokenPairs:    make(map[string]string),
		notifier:      notifier,
		activeOrders:  make(map[string][]string),
		assetToMarket: make(map[string]string),
		feeRates:      make(map[string]float64),
		rtdsClient:    rtdsClient,
		cryptoTracker: strategy.NewCryptoSignalTracker(strategy.CryptoSignalConfig{
			MinPriceChangePct: 0.02,
			Cooldown:          5 * time.Minute,
			DefaultAmountUSDC: cfg.Taker.AmountUSDC,
		}),
		gammaSelector: strategy.NewGammaSelector(gammaClient, strategy.SelectorConfig{
			RescanInterval: cfg.Selector.RescanInterval,
			MinLiquidity:   cfg.Selector.MinLiquidity,
			MinVolume24hr:  cfg.Selector.MinVolume24hr,
			MaxSpread:      cfg.Selector.MaxSpread,
			MinDaysToEnd:   cfg.Selector.MinDaysToEnd,
		}),
		tradingMode: tradingMode,
	}
	if tradingMode == "paper" {
		allowShort := cfg.Paper.AllowShort
		a.paperSim = paper.NewSimulator(paper.Config{
			InitialBalanceUSDC: cfg.Paper.InitialBalanceUSDC,
			FeeBps:             cfg.Paper.FeeBps,
			SlippageBps:        cfg.Paper.SlippageBps,
			AllowShort:         &allowShort,
		})
	}

	// Phase 2.1: Portfolio tracker.
	if dataClient != nil && signer != nil {
		a.Portfolio = portfolio.NewTracker(dataClient, signer.Address(), 5*time.Minute)
	}

	// Phase 2.2: Builder volume tracker.
	if dataClient != nil && cfg.BuilderKey != "" {
		a.BuilderTracker = builder.NewVolumeTracker(dataClient, cfg.BuilderSyncInterval)
	}

	// Phase 1.4: Heartbeat client.
	if clobClient != nil {
		a.heartbeatClient = clobClient.Heartbeat()
	}

	// OnFill callback: record flow + notify.
	tracker.OnFill = func(f execution.Fill) {
		riskMgr.RecordPnL(0)
		log.Printf("fill: %s %s %s price=%.4f size=%.2f", f.Side, f.AssetID, f.TradeID, f.Price, f.Size)
		// Phase 1.1: Record flow for EvaluateEnhanced.
		a.flowTracker.Record(f.AssetID, f.Side, f.Size, f.Price)
		if a.notifier != nil {
			_ = a.notifier.NotifyFill(context.Background(), f.AssetID, f.Side, f.Price, f.Size)
		}
	}

	return a
}

func (a *App) Run(ctx context.Context) error {
	a.mu.Lock()
	a.running = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
	}()

	assetIDs := a.cfg.Maker.Markets
	if len(assetIDs) == 0 {
		log.Println("auto-selecting markets...")
		var err error
		assetIDs, err = a.autoSelectMarkets(ctx)
		if err != nil {
			return err
		}
	}
	if len(assetIDs) == 0 {
		log.Fatal("no markets selected")
	}
	log.Printf("monitoring %d assets: %v", len(assetIDs), assetIDs)

	// Phase 3.3: Fetch fee rates for fee-aware maker pricing.
	a.fetchFeeRates(ctx, assetIDs)

	bookCh, err := a.wsClient.SubscribeOrderbook(ctx, assetIDs)
	if err != nil {
		return err
	}

	// Subscribe to user order and trade streams for fill tracking.
	marketIDs := a.collectMarketIDs(assetIDs)
	var orderCh <-chan ws.OrderEvent
	var tradeCh <-chan ws.TradeEvent
	if a.tradingMode == "live" && len(marketIDs) > 0 {
		orderCh, err = a.wsClient.SubscribeUserOrders(ctx, marketIDs)
		if err != nil {
			log.Printf("warning: user orders subscription failed: %v", err)
		}
		tradeCh, err = a.wsClient.SubscribeUserTrades(ctx, marketIDs)
		if err != nil {
			log.Printf("warning: user trades subscription failed: %v", err)
		}
	}

	// Phase 1.5: Subscribe to market resolutions.
	var resolutionCh <-chan ws.MarketResolvedEvent
	resolutionCh, err = a.wsClient.SubscribeMarketResolutions(ctx, assetIDs)
	if err != nil {
		log.Printf("warning: market resolutions subscription failed: %v", err)
	}

	// Phase 2.1: Start portfolio sync in background.
	if a.Portfolio != nil {
		go func() {
			if err := a.Portfolio.Run(ctx); err != nil && err != context.Canceled {
				log.Printf("portfolio tracker stopped: %v", err)
			}
		}()
	}

	// Phase 2.2: Start builder volume sync in background.
	if a.BuilderTracker != nil {
		go func() {
			if err := a.BuilderTracker.Run(ctx); err != nil && err != context.Canceled {
				log.Printf("builder tracker stopped: %v", err)
			}
		}()
	}

	// Phase 3.2: Start RTDS crypto price subscription if configured.
	var cryptoCh <-chan rtds.CryptoPriceEvent
	if a.rtdsClient != nil && a.cryptoTracker != nil {
		symbols := a.cryptoTracker.TrackedSymbols()
		if len(symbols) > 0 {
			cryptoCh, err = a.rtdsClient.SubscribeCryptoPrices(ctx, symbols)
			if err != nil {
				log.Printf("warning: rtds crypto prices subscription failed: %v", err)
			} else {
				log.Printf("rtds: subscribed to %d crypto symbols", len(symbols))
			}
		}
	}

	log.Println("trading loop started")

	// Periodic risk sync ticker.
	riskInterval := a.cfg.Risk.RiskSyncInterval
	if riskInterval <= 0 {
		riskInterval = 5 * time.Second
	}
	riskTicker := time.NewTicker(riskInterval)
	defer riskTicker.Stop()

	// Phase 1.4: Heartbeat keepalive ticker.
	hbInterval := a.cfg.HeartbeatInterval
	if hbInterval <= 0 {
		hbInterval = 30 * time.Second
	}
	heartbeatTicker := time.NewTicker(hbInterval)
	defer heartbeatTicker.Stop()

	// Phase 1.3: Daily PnL reset timer.
	dailyResetTimer := time.NewTimer(timeUntilMidnightUTC())
	defer dailyResetTimer.Stop()

	// Phase 1.2: GammaSelector rescan ticker.
	var rescanCh <-chan time.Time
	var rescanTicker *time.Ticker
	if a.gammaSelector != nil && a.cfg.Selector.RescanInterval > 0 {
		rescanTicker = time.NewTicker(a.cfg.Selector.RescanInterval)
		rescanCh = rescanTicker.C
		defer rescanTicker.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case event, ok := <-bookCh:
			if !ok {
				log.Println("book channel closed, reconnecting...")
				time.Sleep(2 * time.Second)
				bookCh, err = a.wsClient.SubscribeOrderbook(ctx, assetIDs)
				if err != nil {
					return err
				}
				continue
			}
			a.HandleBookEvent(ctx, event)

		case orderEv, ok := <-orderCh:
			if !ok {
				orderCh = nil
				continue
			}
			a.tracker.ProcessOrderEvent(orderEv)
			a.riskMgr.SetOpenOrders(a.tracker.OpenOrderCount())

		case tradeEv, ok := <-tradeCh:
			if !ok {
				tradeCh = nil
				continue
			}
			a.tracker.ProcessTradeEvent(tradeEv)

		case <-riskTicker.C:
			a.riskSync(ctx)

		// Phase 1.4: Heartbeat.
		case <-heartbeatTicker.C:
			if a.heartbeatClient != nil {
				if _, hbErr := a.heartbeatClient.Heartbeat(ctx, nil); hbErr != nil {
					log.Printf("heartbeat: %v", hbErr)
				}
			}

		// Phase 1.3: Daily PnL reset at UTC midnight.
		case <-dailyResetTimer.C:
			a.resetDailyRisk()
			log.Println("daily PnL reset")
			if a.notifier != nil {
				_, fills, pnl := a.Stats()
				_ = a.notifier.NotifyDailySummary(ctx, pnl, fills, 0)
			}
			dailyResetTimer.Reset(timeUntilMidnightUTC())

		// Phase 1.5: Market resolution handling.
		case resEv, ok := <-resolutionCh:
			if !ok {
				resolutionCh = nil
				continue
			}
			a.handleMarketResolution(ctx, resEv)

		// Phase 1.2: Periodic market rescan via GammaSelector.
		case <-rescanCh:
			a.rescanMarkets(ctx, &assetIDs, &bookCh)

		// Phase 3.2: RTDS crypto price events → correlated trading signals.
		case cryptoEv, ok := <-cryptoCh:
			if !ok {
				cryptoCh = nil
				continue
			}
			a.handleCryptoPrice(ctx, cryptoEv)
		}
	}
}

func (a *App) HandleBookEvent(ctx context.Context, event ws.OrderbookEvent) {
	a.books.Update(event)

	if a.cfg.Maker.Enabled {
		// Build inventory state from tracker.
		var inv strategy.InventoryState
		if pos := a.tracker.Position(event.AssetID); pos != nil {
			inv = strategy.InventoryState{
				NetPosition:   pos.NetSize,
				MaxPosition:   a.cfg.Risk.MaxPositionPerMarket,
				AvgEntryPrice: pos.AvgEntryPrice,
			}
		}

		// Phase 3.3: Fee-aware maker pricing — ensure spread covers fees.
		quote, err := a.maker.ComputeQuote(event, inv)
		if err != nil {
			return
		}
		if feeRate, ok := a.feeRates[event.AssetID]; ok && feeRate > 0 {
			minSpread := feeRate * 2 / 10000 // 2x fee to ensure profitability
			actualSpread := quote.SellPrice - quote.BuyPrice
			if quote.BuyPrice+quote.SellPrice > 0 {
				mid := (quote.BuyPrice + quote.SellPrice) / 2
				if actualSpread/mid < minSpread {
					halfMin := mid * minSpread / 2
					quote.BuyPrice = mid - halfMin
					quote.SellPrice = mid + halfMin
				}
			}
		}

		if old, has := a.activeOrders[event.AssetID]; has && len(old) > 0 {
			if a.tradingMode == "live" && a.clobClient != nil {
				_, _ = a.clobClient.CancelOrders(ctx, &clobtypes.CancelOrdersRequest{OrderIDs: old})
			} else if a.tradingMode == "paper" {
				a.cancelPaperOrders(old)
			}
			delete(a.activeOrders, event.AssetID)
		}

		if !a.cfg.DryRun {
			if err := a.riskMgr.Allow(event.AssetID, quote.Size); err != nil {
				return
			}
			buyResp := a.placeLimit(ctx, event.AssetID, "BUY", quote.BuyPrice, quote.Size)
			if buyResp.ID != "" {
				if a.tradingMode == "live" {
					a.activeOrders[event.AssetID] = append(a.activeOrders[event.AssetID], buyResp.ID)
					a.tracker.RegisterOrder(buyResp.ID, event.AssetID, event.Market, "BUY", quote.BuyPrice, quote.Size)
				} else if strings.EqualFold(buyResp.Status, "LIVE") {
					a.activeOrders[event.AssetID] = append(a.activeOrders[event.AssetID], buyResp.ID)
				}
			}
			sellResp := a.placeLimit(ctx, event.AssetID, "SELL", quote.SellPrice, quote.Size)
			if sellResp.ID != "" {
				if a.tradingMode == "live" {
					a.activeOrders[event.AssetID] = append(a.activeOrders[event.AssetID], sellResp.ID)
					a.tracker.RegisterOrder(sellResp.ID, event.AssetID, event.Market, "SELL", quote.SellPrice, quote.Size)
				} else if strings.EqualFold(sellResp.Status, "LIVE") {
					a.activeOrders[event.AssetID] = append(a.activeOrders[event.AssetID], sellResp.ID)
				}
			}
		} else {
			log.Printf("[DRY] maker %s: buy=%.4f sell=%.4f size=%.2f",
				event.AssetID, quote.BuyPrice, quote.SellPrice, quote.Size)
		}
	}

	if a.cfg.Taker.Enabled {
		// Phase 1.1: Use EvaluateEnhanced with flow + convergence signals.
		counterpartPrice := a.getCounterpartMid(event.AssetID)
		sig, err := a.taker.EvaluateEnhanced(event, a.flowTracker, counterpartPrice)
		if err != nil || sig == nil {
			return
		}
		if !a.cfg.DryRun {
			if err := a.riskMgr.Allow(event.AssetID, sig.AmountUSDC); err != nil {
				return
			}
			resp := a.placeMarket(ctx, sig.AssetID, sig.Side, sig.AmountUSDC)
			if resp.ID != "" {
				a.taker.RecordTrade(sig.AssetID)
				if a.tradingMode == "live" {
					a.tracker.RegisterOrder(resp.ID, sig.AssetID, event.Market, sig.Side, sig.MaxPrice, sig.AmountUSDC)
				}
			}
		} else {
			log.Printf("[DRY] taker %s: side=%s amount=%.2f imbalance=%.4f",
				sig.AssetID, sig.Side, sig.AmountUSDC, sig.Imbalance)
		}
	}

	// Phase 3.1: Convergence arbitrage — buy both YES+NO when sum deviates from $1.
	a.checkConvergenceArbitrage(ctx, event)
}

func (a *App) Shutdown(ctx context.Context) {
	log.Println("shutting down...")
	if !a.cfg.DryRun && a.tradingMode == "live" {
		log.Println("cancelling all open orders...")
		resp, err := a.clobClient.CancelAll(ctx)
		if err != nil {
			log.Printf("cancel all error: %v", err)
		} else {
			log.Printf("cancelled %d orders", resp.Count)
		}
	}
	if a.wsClient != nil {
		_ = a.wsClient.Close()
	}
	orders := a.tracker.OpenOrderCount()
	fills := a.tracker.TotalFills()
	pnl := a.tracker.TotalRealizedPnL()
	log.Printf("session complete: orders=%d fills=%d pnl=%.2f", orders, fills, pnl)
}

// Stats returns current open orders, total fills, and realized PnL.
func (a *App) Stats() (orders int, fills int, pnl float64) {
	return a.tracker.OpenOrderCount(), a.tracker.TotalFills(), a.tracker.TotalRealizedPnL()
}

// IsRunning reports whether the trading loop is active.
func (a *App) IsRunning() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.running
}

// IsDryRun reports whether the app is in dry-run mode.
func (a *App) IsDryRun() bool { return a.cfg.DryRun }

// MonitoredAssets returns the list of currently monitored asset IDs.
func (a *App) MonitoredAssets() []string { return a.books.AssetIDs() }

// SetEmergencyStop activates or deactivates the emergency stop.
func (a *App) SetEmergencyStop(stop bool) {
	a.riskMgr.SetEmergencyStop(stop)
	if stop && a.notifier != nil {
		_ = a.notifier.NotifyEmergencyStop(context.Background())
	}
}

// RecentFills returns the last N trade fills.
func (a *App) RecentFills(limit int) []execution.Fill {
	return a.tracker.RecentFills(limit)
}

// ActiveOrders returns all currently LIVE orders.
func (a *App) ActiveOrders() []execution.OrderState {
	return a.tracker.ActiveOrders()
}

// Positions returns a snapshot of all tracked positions.
func (a *App) TrackedPositions() map[string]execution.Position {
	return a.tracker.Positions()
}

// RiskSnapshot returns the current risk state used by the dashboard API.
func (a *App) RiskSnapshot() risk.Snapshot {
	return a.riskMgr.Snapshot()
}

// TradingMode returns the effective execution mode: live or paper.
func (a *App) TradingMode() string {
	return a.tradingMode
}

// PaperSnapshot returns current paper account metrics (empty in live mode).
func (a *App) PaperSnapshot() paper.Snapshot {
	if a.paperSim == nil {
		return paper.Snapshot{}
	}
	return a.paperSim.Snapshot()
}

// UnrealizedPnL computes unrealized PnL across all positions.
func (a *App) UnrealizedPnL() float64 {
	positions := a.tracker.Positions()
	var total float64
	for assetID, pos := range positions {
		if pos.NetSize == 0 {
			continue
		}
		mid, err := a.books.Mid(assetID)
		if err != nil {
			continue
		}
		total += (mid - pos.AvgEntryPrice) * pos.NetSize
	}
	return total
}

// autoSelectMarkets uses GammaSelector first, falling back to CLOB depth-based selection.
func (a *App) autoSelectMarkets(ctx context.Context) ([]string, error) {
	// Phase 1.2: Try GammaSelector first.
	if a.gammaSelector != nil {
		candidates, err := a.gammaSelector.Select(ctx, a.cfg.Maker.AutoSelectTop)
		if err == nil && len(candidates) > 0 {
			var ids []string
			for _, c := range candidates {
				ids = append(ids, c.TokenID)
				a.assetToMarket[c.TokenID] = c.MarketID
			}
			// Build token pairs from candidates in the same market.
			a.buildTokenPairsFromCandidates(candidates)
			log.Printf("gamma selector: %d candidates", len(ids))
			return ids, nil
		}
		if err != nil {
			log.Printf("gamma selector failed, falling back to CLOB: %v", err)
		}
	}

	// Fallback: CLOB depth-based selection.
	resp, err := a.clobClient.Markets(ctx, &clobtypes.MarketsRequest{Active: boolPtr(true), Limit: 50})
	if err != nil {
		return nil, err
	}
	booksMap := make(map[string]clobtypes.OrderBook)
	for _, m := range resp.Data {
		tokens := m.Tokens
		for _, tok := range tokens {
			book, bErr := a.clobClient.OrderBook(ctx, &clobtypes.BookRequest{TokenID: tok.TokenID})
			if bErr != nil {
				continue
			}
			booksMap[tok.TokenID] = clobtypes.OrderBook(book)
			a.assetToMarket[tok.TokenID] = m.ConditionID
		}
		// Build token pairs for binary markets.
		if len(tokens) == 2 {
			a.tokenPairs[tokens[0].TokenID] = tokens[1].TokenID
			a.tokenPairs[tokens[1].TokenID] = tokens[0].TokenID
		}
	}
	return strategy.SelectMarkets(resp.Data, booksMap, a.cfg.Maker.AutoSelectTop, 50), nil
}

// buildTokenPairsFromCandidates maps YES↔NO token pairs from gamma candidates.
func (a *App) buildTokenPairsFromCandidates(candidates []strategy.MarketCandidate) {
	byMarket := make(map[string][]string) // marketID → tokenIDs
	for _, c := range candidates {
		byMarket[c.MarketID] = append(byMarket[c.MarketID], c.TokenID)
	}
	for _, tokens := range byMarket {
		if len(tokens) == 2 {
			a.tokenPairs[tokens[0]] = tokens[1]
			a.tokenPairs[tokens[1]] = tokens[0]
		}
	}
}

// collectMarketIDs returns unique market/condition IDs for the given asset IDs.
func (a *App) collectMarketIDs(assetIDs []string) []string {
	seen := make(map[string]bool)
	var marketIDs []string
	for _, aid := range assetIDs {
		if mid, ok := a.assetToMarket[aid]; ok && !seen[mid] {
			seen[mid] = true
			marketIDs = append(marketIDs, mid)
		}
	}
	return marketIDs
}

// getCounterpartMid returns the mid price of the counterpart token (YES↔NO pair).
func (a *App) getCounterpartMid(assetID string) float64 {
	counterpart, ok := a.tokenPairs[assetID]
	if !ok {
		return 0
	}
	mid, err := a.books.Mid(counterpart)
	if err != nil {
		return 0
	}
	return mid
}

// checkConvergenceArbitrage detects and executes convergence arbitrage opportunities.
// In binary markets, YES+NO should sum to $1. When they deviate, we can profit:
// - sum < $1: buy both tokens → at resolution one pays $1 → profit = $1 - sum
// - sum > $1: sell the overpriced token → prices will converge → profit = sum - $1
func (a *App) checkConvergenceArbitrage(ctx context.Context, event ws.OrderbookEvent) {
	counterpartID, ok := a.tokenPairs[event.AssetID]
	if !ok {
		return
	}

	yesMid, err := a.books.Mid(event.AssetID)
	if err != nil || yesMid == 0 {
		return
	}
	noMid, err := a.books.Mid(counterpartID)
	if err != nil || noMid == 0 {
		return
	}

	signal, edgeBps := a.taker.DetectConvergence(yesMid, noMid)
	if signal == "" {
		return
	}

	// Only trade if edge covers fees (at least 2x fee for both legs).
	minEdgeBps := a.cfg.Taker.MinConvergenceBps
	if minEdgeBps == 0 {
		minEdgeBps = 50
	}
	if edgeBps < minEdgeBps {
		return
	}

	amount := a.cfg.Taker.AmountUSDC
	sum := yesMid + noMid

	if a.cfg.DryRun {
		log.Printf("[DRY] convergence arb: YES=%.4f NO=%.4f sum=%.4f edge=%.1fbps signal=%s",
			yesMid, noMid, sum, edgeBps, signal)
		return
	}

	if sum < 1.0 {
		// Underpriced: buy both tokens. At resolution, one pays $1.
		// Cost = sum, Payout = $1, Profit = 1 - sum.
		halfAmount := amount / 2

		if err := a.riskMgr.Allow(event.AssetID, halfAmount); err != nil {
			return
		}
		if err := a.riskMgr.Allow(counterpartID, halfAmount); err != nil {
			return
		}

		resp1 := a.placeMarket(ctx, event.AssetID, "BUY", halfAmount)
		resp2 := a.placeMarket(ctx, counterpartID, "BUY", halfAmount)

		if resp1.ID != "" {
			if a.tradingMode == "live" {
				a.tracker.RegisterOrder(resp1.ID, event.AssetID, event.Market, "BUY", yesMid, halfAmount)
			}
			log.Printf("convergence arb: bought YES %s @ %.4f", event.AssetID, yesMid)
		}
		if resp2.ID != "" {
			if a.tradingMode == "live" {
				a.tracker.RegisterOrder(resp2.ID, counterpartID, event.Market, "BUY", noMid, halfAmount)
			}
			log.Printf("convergence arb: bought NO %s @ %.4f", counterpartID, noMid)
		}
	} else {
		// Overpriced: sell the more expensive token.
		targetID := event.AssetID
		targetPrice := yesMid
		if noMid > yesMid {
			targetID = counterpartID
			targetPrice = noMid
		}

		if err := a.riskMgr.Allow(targetID, amount); err != nil {
			return
		}

		resp := a.placeMarket(ctx, targetID, "SELL", amount)
		if resp.ID != "" {
			if a.tradingMode == "live" {
				a.tracker.RegisterOrder(resp.ID, targetID, event.Market, "SELL", targetPrice, amount)
			}
			log.Printf("convergence arb: sold %s @ %.4f (sum=%.4f)", targetID, targetPrice, sum)
		}
	}
}

// handleCryptoPrice processes an RTDS crypto price event and executes any generated signals.
func (a *App) handleCryptoPrice(ctx context.Context, ev rtds.CryptoPriceEvent) {
	if a.cryptoTracker == nil {
		return
	}

	price, _ := ev.Value.Float64()
	update := strategy.CryptoPriceUpdate{
		Symbol:    ev.Symbol,
		Price:     price,
		Timestamp: time.Unix(ev.Timestamp/1000, (ev.Timestamp%1000)*1e6),
	}

	signals := a.cryptoTracker.ProcessPrice(update)
	for _, sig := range signals {
		if a.cfg.DryRun {
			log.Printf("[DRY] crypto signal: %s %s amount=%.2f reason=%s",
				sig.Side, sig.MarketAssetID, sig.AmountUSDC, sig.Reason)
			continue
		}

		if err := a.riskMgr.Allow(sig.MarketAssetID, sig.AmountUSDC); err != nil {
			continue
		}

		resp := a.placeMarket(ctx, sig.MarketAssetID, sig.Side, sig.AmountUSDC)
		if resp.ID != "" {
			market := a.assetToMarket[sig.MarketAssetID]
			if a.tradingMode == "live" {
				a.tracker.RegisterOrder(resp.ID, sig.MarketAssetID, market, sig.Side, 0, sig.AmountUSDC)
			}
			log.Printf("crypto trade: %s %s (triggered by %s)", sig.Side, sig.MarketAssetID, sig.Reason)
		}
	}
}

// SetCryptoMapping sets the crypto symbol → Polymarket asset mapping for RTDS signals.
func (a *App) SetCryptoMapping(mapping map[string][]string) {
	if a.cryptoTracker != nil {
		a.cryptoTracker.SetMapping(mapping)
	}
}

// fetchFeeRates queries fee rates for all monitored assets.
func (a *App) fetchFeeRates(ctx context.Context, assetIDs []string) {
	for _, id := range assetIDs {
		resp, err := a.clobClient.FeeRate(ctx, &clobtypes.FeeRateRequest{TokenID: id})
		if err != nil {
			log.Printf("fee rate %s: %v", id, err)
			continue
		}
		if rate, pErr := strconv.ParseFloat(resp.FeeRate, 64); pErr == nil {
			a.feeRates[id] = rate
		}
	}
	if len(a.feeRates) > 0 {
		log.Printf("fetched fee rates for %d assets", len(a.feeRates))
	}
}

// handleMarketResolution processes a market resolution event.
func (a *App) handleMarketResolution(ctx context.Context, ev ws.MarketResolvedEvent) {
	log.Printf("market resolved: %s (winner: %s)", ev.Question, ev.WinningOutcome)

	// Cancel all orders for resolved market's assets.
	for _, assetID := range ev.AssetIDs {
		if ids, has := a.activeOrders[assetID]; has && len(ids) > 0 {
			if a.tradingMode == "live" && a.clobClient != nil {
				_, _ = a.clobClient.CancelOrders(ctx, &clobtypes.CancelOrdersRequest{OrderIDs: ids})
			} else if a.tradingMode == "paper" {
				a.cancelPaperOrders(ids)
			}
			delete(a.activeOrders, assetID)
		}
	}

	// Also cancel by market.
	if a.tradingMode == "live" && ev.Market != "" {
		_, _ = a.clobClient.CancelMarketOrders(ctx, &clobtypes.CancelMarketOrdersRequest{Market: ev.Market})
	}
}

// rescanMarkets periodically rescans markets using GammaSelector.
func (a *App) rescanMarkets(ctx context.Context, assetIDs *[]string, bookCh *<-chan ws.OrderbookEvent) {
	if a.gammaSelector == nil {
		return
	}
	candidates, err := a.gammaSelector.Select(ctx, a.cfg.Maker.AutoSelectTop)
	if err != nil {
		log.Printf("rescan: gamma selector: %v", err)
		return
	}

	newIDs := make(map[string]bool)
	for _, c := range candidates {
		newIDs[c.TokenID] = true
		a.assetToMarket[c.TokenID] = c.MarketID
	}
	a.buildTokenPairsFromCandidates(candidates)

	// Determine additions and removals.
	oldIDs := make(map[string]bool)
	for _, id := range *assetIDs {
		oldIDs[id] = true
	}

	var toAdd, toRemove []string
	for id := range newIDs {
		if !oldIDs[id] {
			toAdd = append(toAdd, id)
		}
	}
	for id := range oldIDs {
		if !newIDs[id] {
			toRemove = append(toRemove, id)
		}
	}

	if len(toAdd) == 0 && len(toRemove) == 0 {
		return
	}

	// Unsubscribe removed assets.
	if len(toRemove) > 0 {
		if unsubErr := a.wsClient.UnsubscribeMarketAssets(ctx, toRemove); unsubErr != nil {
			log.Printf("rescan: unsubscribe: %v", unsubErr)
		}
		log.Printf("rescan: removed %d assets", len(toRemove))
	}

	// Build new full asset list.
	var updated []string
	for _, c := range candidates {
		updated = append(updated, c.TokenID)
	}
	*assetIDs = updated

	// Subscribe to new assets by resubscribing to the full list.
	if len(toAdd) > 0 {
		newBookCh, subErr := a.wsClient.SubscribeOrderbook(ctx, toAdd)
		if subErr != nil {
			log.Printf("rescan: subscribe: %v", subErr)
		} else {
			// Merge: we just subscribe to new assets; the old channel keeps delivering.
			_ = newBookCh // events will be delivered through the existing connection
		}
		// Fetch fee rates for new assets.
		a.fetchFeeRates(ctx, toAdd)
		log.Printf("rescan: added %d assets", len(toAdd))
	}
}

// riskSync periodically syncs risk state from tracker and checks stop-loss.
func (a *App) riskSync(ctx context.Context) {
	currentRealized := a.tracker.TotalRealizedPnL()
	if !a.realizedInitialized {
		if currentRealized != 0 {
			if a.riskMgr.RecordTradeResult(currentRealized) {
				log.Printf("risk cooldown triggered: consecutive losses=%d", a.riskMgr.ConsecutiveLosses())
				a.notifyRiskCooldown(ctx)
			}
		}
		a.lastRealizedPnL = currentRealized
		a.realizedInitialized = true
	} else {
		realizedDelta := currentRealized - a.lastRealizedPnL
		if realizedDelta != 0 {
			if a.riskMgr.RecordTradeResult(realizedDelta) {
				log.Printf("risk cooldown triggered: consecutive losses=%d", a.riskMgr.ConsecutiveLosses())
				a.notifyRiskCooldown(ctx)
			}
		}
		a.lastRealizedPnL = currentRealized
	}

	if !a.dailyBaselineSet {
		a.dailyRealizedBaseline = currentRealized
		a.dailyBaselineSet = true
	}
	dailyRealized := currentRealized - a.dailyRealizedBaseline

	positions := a.tracker.Positions()
	a.riskMgr.SyncFromTracker(a.tracker.OpenOrderCount(), positions, dailyRealized)

	// Per-market stop-loss checks.
	for assetID, pos := range positions {
		if pos.NetSize == 0 {
			continue
		}
		mid, err := a.books.Mid(assetID)
		if err != nil {
			continue
		}
		if a.riskMgr.EvaluateStopLoss(assetID, pos, mid) {
			log.Printf("STOP-LOSS triggered for %s: unwinding position", assetID)
			if a.notifier != nil {
				_ = a.notifier.NotifyStopLoss(ctx, assetID, pos.RealizedPnL)
			}
			a.unwindPosition(ctx, assetID, pos)
		}
	}

	// Global drawdown check.
	var totalUnrealized float64
	for assetID, pos := range positions {
		if pos.NetSize == 0 {
			continue
		}
		mid, err := a.books.Mid(assetID)
		if err != nil {
			continue
		}
		totalUnrealized += (mid - pos.AvgEntryPrice) * pos.NetSize
	}
	capital := a.cfg.Risk.AccountCapitalUSDC
	if capital <= 0 {
		capital = a.cfg.Risk.MaxPositionPerMarket * 5
	}
	if a.riskMgr.EvaluateDrawdown(currentRealized, totalUnrealized, capital) {
		log.Println("EMERGENCY: max drawdown exceeded, triggering emergency stop")
		a.SetEmergencyStop(true)
	}
}

func (a *App) notifyRiskCooldown(ctx context.Context) {
	if a.notifier == nil {
		return
	}
	_ = a.notifier.NotifyRiskCooldown(
		ctx,
		a.riskMgr.ConsecutiveLosses(),
		a.cfg.Risk.MaxConsecutiveLosses,
		a.riskMgr.CooldownRemaining(),
	)
}

func (a *App) resetDailyRisk() {
	a.riskMgr.ResetDaily()
	currentRealized := a.tracker.TotalRealizedPnL()
	a.lastRealizedPnL = currentRealized
	a.realizedInitialized = true
	a.dailyRealizedBaseline = currentRealized
	a.dailyBaselineSet = true
}

// unwindPosition cancels all orders for an asset and places a market order to close.
func (a *App) unwindPosition(ctx context.Context, assetID string, pos execution.Position) {
	if ids, has := a.activeOrders[assetID]; has && len(ids) > 0 {
		if a.tradingMode == "live" && a.clobClient != nil {
			_, _ = a.clobClient.CancelOrders(ctx, &clobtypes.CancelOrdersRequest{OrderIDs: ids})
		} else if a.tradingMode == "paper" {
			a.cancelPaperOrders(ids)
		}
		delete(a.activeOrders, assetID)
	}

	if a.cfg.DryRun {
		log.Printf("[DRY] would unwind %s: size=%.4f", assetID, pos.NetSize)
		return
	}

	if pos.NetSize > 0 {
		a.placeMarket(ctx, assetID, "SELL", pos.NetSize*pos.AvgEntryPrice)
	} else if pos.NetSize < 0 {
		a.placeMarket(ctx, assetID, "BUY", -pos.NetSize*pos.AvgEntryPrice)
	}
}

func (a *App) placeLimit(ctx context.Context, tokenID, side string, price, sizeUSDC float64) clobtypes.OrderResponse {
	if a.tradingMode == "paper" {
		return a.placePaperLimit(tokenID, side, price, sizeUSDC)
	}

	builder := clob.NewOrderBuilder(a.clobClient, a.signer).
		TokenID(tokenID).
		Side(side).
		Price(price).
		AmountUSDC(sizeUSDC).
		OrderType(clobtypes.OrderTypeGTC)

	signable, err := builder.BuildSignableWithContext(ctx)
	if err != nil {
		log.Printf("build limit %s %s: %v", side, tokenID, err)
		return clobtypes.OrderResponse{}
	}
	resp, err := a.clobClient.CreateOrderFromSignable(ctx, signable)
	if err != nil {
		log.Printf("place limit %s %s: %v", side, tokenID, err)
		return clobtypes.OrderResponse{}
	}
	log.Printf("limit %s %s @ %.4f: id=%s", side, tokenID, price, resp.ID)
	return resp
}

func (a *App) placeMarket(ctx context.Context, tokenID, side string, amountUSDC float64) clobtypes.OrderResponse {
	if a.tradingMode == "paper" {
		return a.placePaperMarket(tokenID, side, amountUSDC)
	}

	builder := clob.NewOrderBuilder(a.clobClient, a.signer).
		TokenID(tokenID).
		Side(side).
		AmountUSDC(amountUSDC).
		OrderType(clobtypes.OrderTypeFAK)

	signable, err := builder.BuildMarketWithContext(ctx)
	if err != nil {
		log.Printf("build market %s %s: %v", side, tokenID, err)
		return clobtypes.OrderResponse{}
	}
	resp, err := a.clobClient.CreateOrderFromSignable(ctx, signable)
	if err != nil {
		log.Printf("place market %s %s: %v", side, tokenID, err)
		return clobtypes.OrderResponse{}
	}
	log.Printf("market %s %s amount=%.2f: id=%s", side, tokenID, amountUSDC, resp.ID)
	return resp
}

func (a *App) placePaperLimit(tokenID, side string, price, sizeUSDC float64) clobtypes.OrderResponse {
	if a.paperSim == nil {
		return clobtypes.OrderResponse{}
	}
	book, ok := a.books.Get(tokenID)
	if !ok {
		log.Printf("paper limit %s %s: no book", side, tokenID)
		return clobtypes.OrderResponse{}
	}
	fill, err := a.paperSim.ExecuteLimit(tokenID, side, price, sizeUSDC, book)
	if err != nil {
		log.Printf("paper limit %s %s: %v", side, tokenID, err)
		return clobtypes.OrderResponse{}
	}
	a.applyPaperFill(fill)
	return toPaperOrderResponse(fill)
}

func (a *App) placePaperMarket(tokenID, side string, amountUSDC float64) clobtypes.OrderResponse {
	if a.paperSim == nil {
		return clobtypes.OrderResponse{}
	}
	book, ok := a.books.Get(tokenID)
	if !ok {
		log.Printf("paper market %s %s: no book", side, tokenID)
		return clobtypes.OrderResponse{}
	}
	fill, err := a.paperSim.ExecuteMarket(tokenID, side, amountUSDC, book)
	if err != nil {
		log.Printf("paper market %s %s: %v", side, tokenID, err)
		return clobtypes.OrderResponse{}
	}
	a.applyPaperFill(fill)
	return toPaperOrderResponse(fill)
}

func (a *App) applyPaperFill(fill paper.FillResult) {
	market := a.assetToMarket[fill.AssetID]
	a.tracker.RegisterOrder(fill.OrderID, fill.AssetID, market, fill.Side, fill.Price, fill.AmountUSDC)
	matchedSize := "0"
	if fill.Filled {
		matchedSize = fmt.Sprintf("%.8f", fill.Size)
	}
	a.tracker.ProcessOrderEvent(ws.OrderEvent{
		ID:           fill.OrderID,
		AssetID:      fill.AssetID,
		Market:       market,
		Side:         fill.Side,
		Price:        fmt.Sprintf("%.8f", fill.Price),
		OriginalSize: fmt.Sprintf("%.8f", fill.AmountUSDC),
		SizeMatched:  matchedSize,
		Status:       fill.Status,
	})
	if fill.Filled {
		a.tracker.ProcessTradeEvent(ws.TradeEvent{
			ID:      fill.TradeID,
			AssetID: fill.AssetID,
			Price:   fmt.Sprintf("%.8f", fill.Price),
			Size:    fmt.Sprintf("%.8f", fill.Size),
			Side:    fill.Side,
			Market:  market,
		})
	}
}

func (a *App) cancelPaperOrders(orderIDs []string) {
	if a.tradingMode != "paper" {
		return
	}
	for _, orderID := range orderIDs {
		a.tracker.ProcessOrderEvent(ws.OrderEvent{
			ID:     orderID,
			Status: "CANCELED",
		})
	}
}

func toPaperOrderResponse(fill paper.FillResult) clobtypes.OrderResponse {
	matchedSize := "0"
	if fill.Filled {
		matchedSize = fmt.Sprintf("%.8f", fill.Size)
	}
	return clobtypes.OrderResponse{
		ID:           fill.OrderID,
		Status:       fill.Status,
		AssetID:      fill.AssetID,
		Side:         fill.Side,
		Price:        fmt.Sprintf("%.8f", fill.Price),
		OriginalSize: fmt.Sprintf("%.8f", fill.AmountUSDC),
		SizeMatched:  matchedSize,
	}
}

// timeUntilMidnightUTC returns the duration until the next UTC midnight.
func timeUntilMidnightUTC() time.Duration {
	now := time.Now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	return midnight.Sub(now)
}

func boolPtr(v bool) *bool { return &v }
