package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	polymarket "github.com/GoPolymarket/polymarket-go-sdk"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/auth"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/clobtypes"

	"github.com/GoPolymarket/polymarket-trader/internal/config"
	"github.com/GoPolymarket/polymarket-trader/internal/feed"
	"github.com/GoPolymarket/polymarket-trader/internal/risk"
	"github.com/GoPolymarket/polymarket-trader/internal/strategy"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.LoadFile(*cfgPath)
	if err != nil {
		log.Printf("warning: config file: %v, using defaults", err)
		cfg = config.Default()
	}
	cfg.ApplyEnv()

	if cfg.PrivateKey == "" || cfg.APIKey == "" {
		log.Fatal("POLYMARKET_PK and POLYMARKET_API_KEY are required")
	}

	log.Printf("polymarket-trader starting (dry_run=%t)", cfg.DryRun)

	signer, err := auth.NewPrivateKeySigner(strings.TrimSpace(cfg.PrivateKey), 137)
	if err != nil {
		log.Fatalf("signer: %v", err)
	}

	apiKey := &auth.APIKey{
		Key:        strings.TrimSpace(cfg.APIKey),
		Secret:     strings.TrimSpace(cfg.APISecret),
		Passphrase: strings.TrimSpace(cfg.APIPassphrase),
	}

	sdkClient := polymarket.NewClient()
	clobClient := sdkClient.CLOB.WithAuth(signer, apiKey)

	if cfg.BuilderKey != "" && cfg.BuilderSecret != "" {
		clobClient = clobClient.WithBuilderConfig(&auth.BuilderConfig{
			Local: &auth.BuilderCredentials{
				Key:        strings.TrimSpace(cfg.BuilderKey),
				Secret:     strings.TrimSpace(cfg.BuilderSecret),
				Passphrase: strings.TrimSpace(cfg.BuilderPassphrase),
			},
		})
		log.Println("builder attribution enabled")
	}

	wsClient := sdkClient.CLOBWS.Authenticate(signer, apiKey)

	books := feed.NewBookSnapshot()
	riskMgr := risk.New(risk.Config{
		MaxOpenOrders:        cfg.Risk.MaxOpenOrders,
		MaxDailyLossUSDC:     cfg.Risk.MaxDailyLossUSDC,
		MaxPositionPerMarket: cfg.Risk.MaxPositionPerMarket,
	})
	maker := strategy.NewMaker(strategy.MakerConfig{
		MinSpreadBps:       cfg.Maker.MinSpreadBps,
		SpreadMultiplier:   cfg.Maker.SpreadMultiplier,
		OrderSizeUSDC:      cfg.Maker.OrderSizeUSDC,
		MaxOrdersPerMarket: cfg.Maker.MaxOrdersPerMarket,
	})
	taker := strategy.NewTaker(strategy.TakerConfig{
		MinImbalance:   cfg.Taker.MinImbalance,
		DepthLevels:    cfg.Taker.DepthLevels,
		AmountUSDC:     cfg.Taker.AmountUSDC,
		MaxSlippageBps: cfg.Taker.MaxSlippageBps,
		Cooldown:       cfg.Taker.Cooldown,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	assetIDs := cfg.Maker.Markets
	if len(assetIDs) == 0 {
		log.Println("auto-selecting markets...")
		assetIDs, err = autoSelectMarkets(ctx, clobClient, cfg.Maker.AutoSelectTop)
		if err != nil {
			log.Fatalf("market selection: %v", err)
		}
	}
	if len(assetIDs) == 0 {
		log.Fatal("no markets selected")
	}
	log.Printf("monitoring %d assets: %v", len(assetIDs), assetIDs)

	bookCh, err := wsClient.SubscribeOrderbook(ctx, assetIDs)
	if err != nil {
		log.Fatalf("ws subscribe: %v", err)
	}

	activeOrders := make(map[string][]string) // assetID → []orderID

	log.Println("trading loop started")
	var totalOrders, totalFills int

	for {
		select {
		case <-sigCh:
			log.Println("shutdown signal received")
			goto shutdown
		case event, ok := <-bookCh:
			if !ok {
				log.Println("book channel closed, reconnecting...")
				time.Sleep(2 * time.Second)
				bookCh, err = wsClient.SubscribeOrderbook(ctx, assetIDs)
				if err != nil {
					log.Printf("reconnect failed: %v", err)
					goto shutdown
				}
				continue
			}
			books.Update(event)

			// Maker strategy
			if cfg.Maker.Enabled {
				quote, qErr := maker.ComputeQuote(event)
				if qErr != nil {
					continue
				}
				if old, has := activeOrders[event.AssetID]; has && len(old) > 0 {
					_, _ = clobClient.CancelOrders(ctx, &clobtypes.CancelOrdersRequest{OrderIDs: old})
					delete(activeOrders, event.AssetID)
				}

				if !cfg.DryRun {
					if rErr := riskMgr.Allow(event.AssetID, quote.Size); rErr != nil {
						continue
					}
					buyResp := placeLimit(ctx, clobClient, signer, event.AssetID, "BUY", quote.BuyPrice, quote.Size)
					if buyResp.ID != "" {
						activeOrders[event.AssetID] = append(activeOrders[event.AssetID], buyResp.ID)
						totalOrders++
					}
					sellResp := placeLimit(ctx, clobClient, signer, event.AssetID, "SELL", quote.SellPrice, quote.Size)
					if sellResp.ID != "" {
						activeOrders[event.AssetID] = append(activeOrders[event.AssetID], sellResp.ID)
						totalOrders++
					}
				} else {
					log.Printf("[DRY] maker %s: buy=%.4f sell=%.4f size=%.2f",
						event.AssetID, quote.BuyPrice, quote.SellPrice, quote.Size)
				}
			}

			// Taker strategy
			if cfg.Taker.Enabled {
				sig, sErr := taker.Evaluate(event)
				if sErr != nil || sig == nil {
					continue
				}
				if !cfg.DryRun {
					if rErr := riskMgr.Allow(event.AssetID, sig.AmountUSDC); rErr != nil {
						continue
					}
					resp := placeMarket(ctx, clobClient, signer, sig.AssetID, sig.Side, sig.AmountUSDC)
					if resp.ID != "" {
						taker.RecordTrade(sig.AssetID)
						totalFills++
					}
				} else {
					log.Printf("[DRY] taker %s: side=%s amount=%.2f imbalance=%.4f",
						sig.AssetID, sig.Side, sig.AmountUSDC, sig.Imbalance)
				}
			}
		}
	}

shutdown:
	log.Println("shutting down...")
	if !cfg.DryRun {
		log.Println("cancelling all open orders...")
		resp, cErr := clobClient.CancelAll(ctx)
		if cErr != nil {
			log.Printf("cancel all error: %v", cErr)
		} else {
			log.Printf("cancelled %d orders", resp.Count)
		}
	}
	_ = wsClient.Close()
	log.Printf("session complete: orders=%d fills=%d pnl=%.2f", totalOrders, totalFills, riskMgr.DailyPnL())
}

func autoSelectMarkets(ctx context.Context, client clob.Client, topN int) ([]string, error) {
	resp, err := client.Markets(ctx, &clobtypes.MarketsRequest{Active: boolPtr(true), Limit: 50})
	if err != nil {
		return nil, err
	}
	booksMap := make(map[string]clobtypes.OrderBook)
	for _, m := range resp.Data {
		for _, tok := range m.Tokens {
			book, bErr := client.OrderBook(ctx, &clobtypes.BookRequest{TokenID: tok.TokenID})
			if bErr != nil {
				continue
			}
			booksMap[tok.TokenID] = clobtypes.OrderBook(book)
		}
	}
	return strategy.SelectMarkets(resp.Data, booksMap, topN, 50), nil
}

func placeLimit(ctx context.Context, client clob.Client, signer auth.Signer, tokenID, side string, price, sizeUSDC float64) clobtypes.OrderResponse {
	builder := clob.NewOrderBuilder(client, signer).
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
	resp, err := client.CreateOrderFromSignable(ctx, signable)
	if err != nil {
		log.Printf("place limit %s %s: %v", side, tokenID, err)
		return clobtypes.OrderResponse{}
	}
	log.Printf("limit %s %s @ %.4f: id=%s", side, tokenID, price, resp.ID)
	return resp
}

func placeMarket(ctx context.Context, client clob.Client, signer auth.Signer, tokenID, side string, amountUSDC float64) clobtypes.OrderResponse {
	builder := clob.NewOrderBuilder(client, signer).
		TokenID(tokenID).
		Side(side).
		AmountUSDC(amountUSDC).
		OrderType(clobtypes.OrderTypeFAK)

	signable, err := builder.BuildMarketWithContext(ctx)
	if err != nil {
		log.Printf("build market %s %s: %v", side, tokenID, err)
		return clobtypes.OrderResponse{}
	}
	resp, err := client.CreateOrderFromSignable(ctx, signable)
	if err != nil {
		log.Printf("place market %s %s: %v", side, tokenID, err)
		return clobtypes.OrderResponse{}
	}
	log.Printf("market %s %s amount=%.2f: id=%s", side, tokenID, amountUSDC, resp.ID)
	return resp
}

func boolPtr(v bool) *bool { return &v }
