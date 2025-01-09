package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/bluesky-social/indigo/xrpc"
	_ "github.com/joho/godotenv/autoload"
	"github.com/kelseyhightower/envconfig"
	"github.com/mattn/go-redmine"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"

	"bsky.watch/modkit/pkg/cliutil"
	"bsky.watch/modkit/pkg/config"
	"bsky.watch/modkit/pkg/tickets"
	"bsky.watch/utils/xrpcauth"
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
	var addToListField *redmine.CustomFieldDefinition
	for _, cf := range fields {
		if cf.Id == tickets.Mappings().Fields.AddToLists {
			cf := cf
			addToListField = &cf
			break
		}
	}
	if addToListField != nil {
		// XXX: Redmine doesn't return this field, so we're just hardcoding it for now.
		addToListField.EditTagStyle = ptr("check_box")

		label := map[string]string{}
		for id, l := range modkitConfig.Lists {
			label[id] = fmt.Sprintf("%s [%s]", l.Name, id)
		}

		changed := false
		have := map[string]bool{}
		var newValues []redmine.CustomFieldPossibleValue
		for _, pv := range addToListField.PossibleValues {
			if pv.Value == "dummy" {
				changed = true
				continue
			}
			// We explicitly don't delete any values here (except for "dummy"),
			// leaving it to the admin to decide how to handle
			// removing the values from existing tickets.
			id := extractListId(pv.Value)
			if id == "" {
				// No ID provided in the entry, let's just keep it as it and move on.
				newValues = append(newValues, pv)
				continue
			}
			if label[id] != "" && pv.Value != label[id] {
				pv.Value = label[id]
				changed = true
			}
			newValues = append(newValues, pv)
			have[id] = true
		}
		for id := range modkitConfig.Lists {
			if have[id] {
				continue
			}
			newValues = append(newValues, redmine.CustomFieldPossibleValue{
				Value: label[id],
			})
			changed = true
		}

		if changed {
			addToListField.PossibleValues = newValues
			if err := ticketsClient.UpdateCustomField(*addToListField); err != nil {
				return fmt.Errorf("updating 'Add to lists' field: %w", err)
			}
		}
	}

	clients := map[string]*xrpc.Client{
		modkitConfig.ModerationAccount.DID: client,
	}

	handler := NewHandler(ticketsClient, &modkitConfig, client, clients, cfg.ListServerURL)

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
