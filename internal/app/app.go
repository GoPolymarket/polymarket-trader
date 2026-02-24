package app

import (
	"context"
	"log"
	"time"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/auth"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/clobtypes"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/data"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/gamma"

	"github.com/GoPolymarket/polymarket-trader/internal/config"
	"github.com/GoPolymarket/polymarket-trader/internal/execution"
	"github.com/GoPolymarket/polymarket-trader/internal/feed"
	"github.com/GoPolymarket/polymarket-trader/internal/risk"
	"github.com/GoPolymarket/polymarket-trader/internal/strategy"
)

type App struct {
	cfg        config.Config
	clobClient clob.Client
	wsClient   ws.Client
	signer     auth.Signer
	gammaClient gamma.Client
	dataClient  data.Client

	books   *feed.BookSnapshot
	riskMgr *risk.Manager
	maker   *strategy.Maker
	taker   *strategy.Taker
	tracker *execution.Tracker

	activeOrders  map[string][]string
	assetToMarket map[string]string // assetID -> market/condition ID

	gammaSelector *strategy.GammaSelector
}

func New(cfg config.Config, clobClient clob.Client, wsClient ws.Client, signer auth.Signer, gammaClient gamma.Client, dataClient data.Client) *App {
	tracker := execution.NewTracker()
	riskMgr := risk.New(risk.Config{
		MaxOpenOrders:        cfg.Risk.MaxOpenOrders,
		MaxDailyLossUSDC:     cfg.Risk.MaxDailyLossUSDC,
		MaxPositionPerMarket: cfg.Risk.MaxPositionPerMarket,
		StopLossPerMarket:    cfg.Risk.StopLossPerMarket,
		MaxDrawdownPct:       cfg.Risk.MaxDrawdownPct,
		RiskSyncInterval:     cfg.Risk.RiskSyncInterval,
	})

	tracker.OnFill = func(f execution.Fill) {
		riskMgr.RecordPnL(0) // Position change recorded; PnL realized via tracker
		log.Printf("fill: %s %s %s price=%.4f size=%.2f", f.Side, f.AssetID, f.TradeID, f.Price, f.Size)
	}

	return &App{
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
			MinImbalance:   cfg.Taker.MinImbalance,
			DepthLevels:    cfg.Taker.DepthLevels,
			AmountUSDC:     cfg.Taker.AmountUSDC,
			MaxSlippageBps: cfg.Taker.MaxSlippageBps,
			Cooldown:       cfg.Taker.Cooldown,
		}),
		tracker:       tracker,
		activeOrders:  make(map[string][]string),
		assetToMarket: make(map[string]string),
		gammaSelector: strategy.NewGammaSelector(gammaClient, strategy.SelectorConfig{
			RescanInterval: cfg.Selector.RescanInterval,
			MinLiquidity:   cfg.Selector.MinLiquidity,
			MinVolume24hr:  cfg.Selector.MinVolume24hr,
			MaxSpread:      cfg.Selector.MaxSpread,
			MinDaysToEnd:   cfg.Selector.MinDaysToEnd,
		}),
	}
}

func (a *App) Run(ctx context.Context) error {
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

	bookCh, err := a.wsClient.SubscribeOrderbook(ctx, assetIDs)
	if err != nil {
		return err
	}

	// Subscribe to user order and trade streams for fill tracking.
	marketIDs := a.collectMarketIDs(assetIDs)
	var orderCh <-chan ws.OrderEvent
	var tradeCh <-chan ws.TradeEvent
	if len(marketIDs) > 0 {
		orderCh, err = a.wsClient.SubscribeUserOrders(ctx, marketIDs)
		if err != nil {
			log.Printf("warning: user orders subscription failed: %v", err)
		}
		tradeCh, err = a.wsClient.SubscribeUserTrades(ctx, marketIDs)
		if err != nil {
			log.Printf("warning: user trades subscription failed: %v", err)
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
		quote, err := a.maker.ComputeQuote(event, inv)
		if err != nil {
			return
		}
		if old, has := a.activeOrders[event.AssetID]; has && len(old) > 0 {
			_, _ = a.clobClient.CancelOrders(ctx, &clobtypes.CancelOrdersRequest{OrderIDs: old})
			delete(a.activeOrders, event.AssetID)
		}

		if !a.cfg.DryRun {
			if err := a.riskMgr.Allow(event.AssetID, quote.Size); err != nil {
				return
			}
			buyResp := a.placeLimit(ctx, event.AssetID, "BUY", quote.BuyPrice, quote.Size)
			if buyResp.ID != "" {
				a.activeOrders[event.AssetID] = append(a.activeOrders[event.AssetID], buyResp.ID)
				a.tracker.RegisterOrder(buyResp.ID, event.AssetID, event.Market, "BUY", quote.BuyPrice, quote.Size)
			}
			sellResp := a.placeLimit(ctx, event.AssetID, "SELL", quote.SellPrice, quote.Size)
			if sellResp.ID != "" {
				a.activeOrders[event.AssetID] = append(a.activeOrders[event.AssetID], sellResp.ID)
				a.tracker.RegisterOrder(sellResp.ID, event.AssetID, event.Market, "SELL", quote.SellPrice, quote.Size)
			}
		} else {
			log.Printf("[DRY] maker %s: buy=%.4f sell=%.4f size=%.2f",
				event.AssetID, quote.BuyPrice, quote.SellPrice, quote.Size)
		}
	}

	if a.cfg.Taker.Enabled {
		sig, err := a.taker.Evaluate(event)
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
				a.tracker.RegisterOrder(resp.ID, sig.AssetID, event.Market, sig.Side, sig.MaxPrice, sig.AmountUSDC)
			}
		} else {
			log.Printf("[DRY] taker %s: side=%s amount=%.2f imbalance=%.4f",
				sig.AssetID, sig.Side, sig.AmountUSDC, sig.Imbalance)
		}
	}
}

func (a *App) Shutdown(ctx context.Context) {
	log.Println("shutting down...")
	if !a.cfg.DryRun {
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

func (a *App) Stats() (orders int, fills int, pnl float64) {
	return a.tracker.OpenOrderCount(), a.tracker.TotalFills(), a.tracker.TotalRealizedPnL()
}

func (a *App) autoSelectMarkets(ctx context.Context) ([]string, error) {
	resp, err := a.clobClient.Markets(ctx, &clobtypes.MarketsRequest{Active: boolPtr(true), Limit: 50})
	if err != nil {
		return nil, err
	}
	booksMap := make(map[string]clobtypes.OrderBook)
	for _, m := range resp.Data {
		for _, tok := range m.Tokens {
			book, bErr := a.clobClient.OrderBook(ctx, &clobtypes.BookRequest{TokenID: tok.TokenID})
			if bErr != nil {
				continue
			}
			booksMap[tok.TokenID] = clobtypes.OrderBook(book)
			a.assetToMarket[tok.TokenID] = m.ConditionID
		}
	}
	return strategy.SelectMarkets(resp.Data, booksMap, a.cfg.Maker.AutoSelectTop, 50), nil
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

// riskSync periodically syncs risk state from tracker and checks stop-loss.
func (a *App) riskSync(ctx context.Context) {
	positions := a.tracker.Positions()
	a.riskMgr.SyncFromTracker(a.tracker.OpenOrderCount(), positions, a.tracker.TotalRealizedPnL())

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
	if a.riskMgr.EvaluateDrawdown(a.tracker.TotalRealizedPnL(), totalUnrealized, a.cfg.Risk.MaxPositionPerMarket*5) {
		log.Println("EMERGENCY: max drawdown exceeded, triggering emergency stop")
		a.riskMgr.SetEmergencyStop(true)
	}
}

// unwindPosition cancels all orders for an asset and places a market order to close.
func (a *App) unwindPosition(ctx context.Context, assetID string, pos execution.Position) {
	// Cancel all open orders for this asset.
	if ids, has := a.activeOrders[assetID]; has && len(ids) > 0 {
		_, _ = a.clobClient.CancelOrders(ctx, &clobtypes.CancelOrdersRequest{OrderIDs: ids})
		delete(a.activeOrders, assetID)
	}

	if a.cfg.DryRun {
		log.Printf("[DRY] would unwind %s: size=%.4f", assetID, pos.NetSize)
		return
	}

	// Close the position.
	if pos.NetSize > 0 {
		a.placeMarket(ctx, assetID, "SELL", pos.NetSize*pos.AvgEntryPrice)
	} else if pos.NetSize < 0 {
		a.placeMarket(ctx, assetID, "BUY", -pos.NetSize*pos.AvgEntryPrice)
	}
}

func (a *App) placeLimit(ctx context.Context, tokenID, side string, price, sizeUSDC float64) clobtypes.OrderResponse {
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

func boolPtr(v bool) *bool { return &v }
