package app

import (
	"context"
	"log"
	"time"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/auth"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/clobtypes"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"

	"github.com/GoPolymarket/polymarket-trader/internal/config"
	"github.com/GoPolymarket/polymarket-trader/internal/feed"
	"github.com/GoPolymarket/polymarket-trader/internal/risk"
	"github.com/GoPolymarket/polymarket-trader/internal/strategy"
)

type App struct {
	cfg        config.Config
	clobClient clob.Client
	wsClient   ws.Client
	signer     auth.Signer

	books   *feed.BookSnapshot
	riskMgr *risk.Manager
	maker   *strategy.Maker
	taker   *strategy.Taker

	activeOrders map[string][]string
	totalOrders  int
	totalFills   int
}

func New(cfg config.Config, clobClient clob.Client, wsClient ws.Client, signer auth.Signer) *App {
	return &App{
		cfg:        cfg,
		clobClient: clobClient,
		wsClient:   wsClient,
		signer:     signer,
		books:      feed.NewBookSnapshot(),
		riskMgr: risk.New(risk.Config{
			MaxOpenOrders:        cfg.Risk.MaxOpenOrders,
			MaxDailyLossUSDC:     cfg.Risk.MaxDailyLossUSDC,
			MaxPositionPerMarket: cfg.Risk.MaxPositionPerMarket,
		}),
		maker: strategy.NewMaker(strategy.MakerConfig{
			MinSpreadBps:       cfg.Maker.MinSpreadBps,
			SpreadMultiplier:   cfg.Maker.SpreadMultiplier,
			OrderSizeUSDC:      cfg.Maker.OrderSizeUSDC,
			MaxOrdersPerMarket: cfg.Maker.MaxOrdersPerMarket,
		}),
		taker: strategy.NewTaker(strategy.TakerConfig{
			MinImbalance:   cfg.Taker.MinImbalance,
			DepthLevels:    cfg.Taker.DepthLevels,
			AmountUSDC:     cfg.Taker.AmountUSDC,
			MaxSlippageBps: cfg.Taker.MaxSlippageBps,
			Cooldown:       cfg.Taker.Cooldown,
		}),
		activeOrders: make(map[string][]string),
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

	log.Println("trading loop started")

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
		}
	}
}

func (a *App) HandleBookEvent(ctx context.Context, event ws.OrderbookEvent) {
	a.books.Update(event)

	if a.cfg.Maker.Enabled {
		quote, err := a.maker.ComputeQuote(event)
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
				a.totalOrders++
			}
			sellResp := a.placeLimit(ctx, event.AssetID, "SELL", quote.SellPrice, quote.Size)
			if sellResp.ID != "" {
				a.activeOrders[event.AssetID] = append(a.activeOrders[event.AssetID], sellResp.ID)
				a.totalOrders++
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
				a.totalFills++
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
	log.Printf("session complete: orders=%d fills=%d pnl=%.2f", a.totalOrders, a.totalFills, a.riskMgr.DailyPnL())
}

func (a *App) Stats() (orders int, fills int, pnl float64) {
	return a.totalOrders, a.totalFills, a.riskMgr.DailyPnL()
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
		}
	}
	return strategy.SelectMarkets(resp.Data, booksMap, a.cfg.Maker.AutoSelectTop, 50), nil
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
