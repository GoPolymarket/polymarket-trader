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
