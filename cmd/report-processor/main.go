package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	_ "github.com/joho/godotenv/autoload"
	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"golang.org/x/oauth2"

	"bsky.watch/utils/xrpcauth"

	"bsky.watch/modkit/pkg/cliutil"
	"bsky.watch/modkit/pkg/tickets"
)

func runMain(ctx context.Context) error {
	ctx = cliutil.SetupLogging(ctx, &cfg.LoggingConfig)
	log := zerolog.Ctx(ctx)

	if cfg.ConfigPath == "" {
		return fmt.Errorf("please provide config file")
	}

	if err := cfg.LoadDefaultsFromConfig(cfg.ConfigPath); err != nil {
		return err
	}

	var tokenSource oauth2.TokenSource
	switch {
	case cfg.AuthFile != "" && cfg.AuthLogin != "":
		tokenSource = xrpcauth.PasswordAuthWithFileCache(cfg.AuthLogin, cfg.AuthPassword, cfg.AuthFile)
	case cfg.AuthFile != "":
		tokenSource = xrpcauth.SessionFile(cfg.AuthFile)
	case cfg.AuthLogin != "":
		tokenSource = xrpcauth.PasswordAuth(cfg.AuthLogin, cfg.AuthPassword)
	}

	client := xrpcauth.NewAnonymousClient(ctx)
	if tokenSource != nil {
		client = xrpcauth.NewClientWithTokenSource(ctx, tokenSource)
	}

	ticketsClient := tickets.NewClient(cfg.RedmineAddr, cfg.RedmineAPIKey)

	if cfg.Mappings != "" {
		if err := tickets.LoadMappingsFromFile(cfg.Mappings); err != nil {
			return err
		}
	}

	if cfg.TicketIDEncryptionKey == "" {
		return fmt.Errorf("missing ticket ID encryption key")
	}

	handler, err := NewHandler(ctx, client, ticketsClient, &cfg)
	if err != nil {
		return fmt.Errorf("constructing report handler: %w", err)
	}

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(cfg.MetricsAddr, nil); err != nil {
			log.Fatal().Err(err).Msgf("Failed to start HTTP server for exporting metrics")
		}
	}()

	log.Info().Msgf("Startup complete")

	return handler.Run(ctx)
}

func main() {
	flag.StringVar(&cfg.ConfigPath, "config", "", "Path to the config file")
	flag.StringVar(&cfg.AuthFile, "auth-file", "", "Path to the file with credentials")
	flag.StringVar(&cfg.AuthLogin, "login", "", "Login of the Bluesky account to use for queries (provide password using MODKIT_AUTH_PASSWORD env var)")
	flag.StringVar(&cfg.MetricsAddr, "metrics-addr", ":8081", "Address to expose metrics on")
	flag.StringVar(&cfg.TicketIDEncryptionKey, "ticket-id-encryption-key", "", "Secret key for encrypting IDs returned to the user")
	flag.StringVar(&cfg.PersistentValkeyAddr, "valkey-addr", "", "Address of the valkey instance to use")
	flag.StringVar(&cfg.RedmineAddr, "redmine-addr", "", "Address of the Redmine instance")
	flag.StringVar(&cfg.Mappings, "mappings", "", "Path to the file with ID mappings")

	cliutil.RegisterLoggingFlags(&cfg.LoggingConfig)

	if err := envconfig.Process("modkit", &cfg); err != nil {
		log.Fatalf("envconfig.Process: %s", err)
	}

	flag.Parse()

	if err := runMain(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

func ptr[T any](v T) *T {
	return &v
}
