package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	polymarket "github.com/GoPolymarket/polymarket-go-sdk"
	"github.com/GoPolymarket/polymarket-go-sdk/pkg/auth"

	"github.com/GoPolymarket/polymarket-trader/internal/api"
	"github.com/GoPolymarket/polymarket-trader/internal/app"
	"github.com/GoPolymarket/polymarket-trader/internal/config"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	phase := flag.String("phase", "", "rollout phase preset: paper|shadow|live-small|live")
	modeOverride := flag.String("mode", "", "override trading mode: paper|live")
	flag.Parse()

	cfg, err := config.LoadFile(*cfgPath)
	if err != nil {
		log.Printf("warning: config file: %v, using defaults", err)
		cfg = config.Default()
	}
	cfg.ApplyEnv()
	if v := strings.ToLower(strings.TrimSpace(*modeOverride)); v != "" {
		cfg.TradingMode = v
	}
	if err := config.ApplyRolloutPhase(&cfg, *phase); err != nil {
		log.Fatalf("invalid -phase: %v", err)
	}

	mode := strings.ToLower(strings.TrimSpace(cfg.TradingMode))
	if mode == "" {
		mode = "paper"
	}

	log.Printf(
		"polymarket-trader starting (mode=%s dry_run=%t phase=%s maker_size=%.2f taker_amount=%.2f max_pos=%.2f daily_loss_pct=%.2f%%)",
		mode,
		cfg.DryRun,
		strings.TrimSpace(*phase),
		cfg.Maker.OrderSizeUSDC,
		cfg.Taker.AmountUSDC,
		cfg.Risk.MaxPositionPerMarket,
		cfg.Risk.MaxDailyLossPct*100,
	)

	sdkClient := polymarket.NewClient()
	clobClient := sdkClient.CLOB
	wsClient := sdkClient.CLOBWS
	var signer auth.Signer
	var apiKey *auth.APIKey

	requireAuth := mode == "live"
	if requireAuth || (cfg.PrivateKey != "" && cfg.APIKey != "") {
		if cfg.PrivateKey == "" || cfg.APIKey == "" {
			log.Fatal("live mode requires POLYMARKET_PK and POLYMARKET_API_KEY")
		}
		signer, err = auth.NewPrivateKeySigner(strings.TrimSpace(cfg.PrivateKey), 137)
		if err != nil {
			log.Fatalf("signer: %v", err)
		}
		apiKey = &auth.APIKey{
			Key:        strings.TrimSpace(cfg.APIKey),
			Secret:     strings.TrimSpace(cfg.APISecret),
			Passphrase: strings.TrimSpace(cfg.APIPassphrase),
		}
		clobClient = clobClient.WithAuth(signer, apiKey)
		wsClient = wsClient.Authenticate(signer, apiKey)
	} else {
		log.Println("paper mode without API credentials: using public market/orderbook data")
	}

	if mode == "live" && cfg.BuilderKey != "" && cfg.BuilderSecret != "" {
		clobClient = clobClient.WithBuilderConfig(&auth.BuilderConfig{
			Local: &auth.BuilderCredentials{
				Key:        strings.TrimSpace(cfg.BuilderKey),
				Secret:     strings.TrimSpace(cfg.BuilderSecret),
				Passphrase: strings.TrimSpace(cfg.BuilderPassphrase),
			},
		})
		log.Println("builder attribution enabled")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	a := app.New(cfg, clobClient, wsClient, signer, sdkClient.Gamma, sdkClient.Data, sdkClient.RTDS)

	// Phase 2.3: Start HTTP API server if enabled.
	var apiServer *api.Server
	if cfg.API.Enabled {
		apiServer = api.NewServer(cfg.API.Addr, a, a.Portfolio, a.BuilderTracker)
		if err := apiServer.Start(ctx); err != nil {
			log.Printf("warning: api server failed to start: %v", err)
		}
	}

	go func() {
		<-sigCh
		log.Println("shutdown signal received")
		cancel()
	}()

	if err := a.Run(ctx); err != nil && err != context.Canceled {
		log.Printf("run error: %v", err)
	}

	if apiServer != nil {
		_ = apiServer.Shutdown(context.Background())
	}
	a.Shutdown(context.Background())
}
