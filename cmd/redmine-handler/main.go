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
	"gopkg.in/yaml.v3"

	"bsky.watch/redmine"
	"bsky.watch/utils/xrpcauth"
	"github.com/bluesky-social/indigo/xrpc"

	"bsky.watch/modkit/pkg/cliutil"
	"bsky.watch/modkit/pkg/config"
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

	if cfg.Mappings == "" {
		return fmt.Errorf("please provide the file with ID mappings")
	}
	if err := tickets.LoadMappingsFromFile(cfg.Mappings); err != nil {
		return err
	}

	if cfg.TicketIDEncryptionKey == "" {
		return fmt.Errorf("missing ticket ID encryption key")
	}

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(cfg.MetricsAddr, nil); err != nil {
			log.Fatal().Err(err).Msgf("Failed to start HTTP server for exporting metrics")
		}
	}()

	log.Info().Msgf("Startup complete")

	var modkitConfig config.Config
	b, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		return fmt.Errorf("reading %q: %w", cfg.ConfigPath, err)
	}
	if err := yaml.Unmarshal(b, &modkitConfig); err != nil {
		return fmt.Errorf("parsing %q: %w", cfg.ConfigPath, err)
	}

	fields, err := ticketsClient.CustomFields()
	if err != nil {
		return fmt.Errorf("querying custom fields: %w", err)
	}
	var addToListField, labelsField *redmine.CustomFieldDefinition
	for _, cf := range fields {
		switch cf.Id {
		case tickets.Mappings().Fields.AddToLists:
			cf := cf
			addToListField = &cf
		case tickets.Mappings().Fields.Labels:
			cf := cf
			labelsField = &cf
		}
	}
	if addToListField != nil {
		values := map[string]string{}
		for id, l := range modkitConfig.Lists {
			values[id] = fmt.Sprintf("%s [%s]", l.Name, id)
		}

		if err := updateCustomFieldValues(ticketsClient, addToListField, values); err != nil {
			return err
		}
	}

	if labelsField != nil && len(modkitConfig.LabelerPolicies.LabelValueDefinitions) > 0 {
		values := map[string]string{}
		for _, label := range modkitConfig.LabelerPolicies.LabelValueDefinitions {
			displayName := label.Identifier
			if len(label.Locales) > 0 {
				displayName = label.Locales[0].Name
			}
			values[label.Identifier] = fmt.Sprintf("%s [%s]", displayName, label.Identifier)
		}
		if err := updateCustomFieldValues(ticketsClient, labelsField, values); err != nil {
			return err
		}
	}

	clients := map[string]*xrpc.Client{
		modkitConfig.ModerationAccount.DID: client,
	}

	handler, err := NewHandler(ticketsClient, &modkitConfig, client, clients, cfg.ListServerURL)
	if err != nil {
		return fmt.Errorf("constructing handler: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/webhook", handler)

	return http.ListenAndServe(cfg.ListenAddr, mux)
}

func main() {
	flag.StringVar(&cfg.ConfigPath, "config", "", "Path to the config file")
	flag.StringVar(&cfg.AuthFile, "auth-file", "", "Path to the file with credentials")
	flag.StringVar(&cfg.AuthLogin, "login", "", "Login of the Bluesky account to use for queries (provide password using MODKIT_AUTH_PASSWORD env var)")
	flag.StringVar(&cfg.ListenAddr, "listen-addr", ":8080", "Address to listen on")
	flag.StringVar(&cfg.MetricsAddr, "metrics-addr", ":8081", "Address to expose metrics on")
	flag.StringVar(&cfg.TicketIDEncryptionKey, "ticket-id-encryption-key", "", "Secret key for encrypting IDs returned to the user")
	flag.StringVar(&cfg.RedmineAddr, "redmine-addr", "", "Address of the Redmine instance")
	flag.StringVar(&cfg.Mappings, "mappings", "", "Path to the file with ID mappings")
	flag.StringVar(&cfg.ListServerURL, "listserver-addr", "", "Address of the listserver")
	flag.BoolVar(&cfg.DumpPayloads, "dump-payloads", false, "If set, will log the full payloads received from Redmine")

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
