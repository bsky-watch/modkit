package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/gorilla/websocket"
	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"

	"bsky.watch/labeler/account"
	labelerconfig "bsky.watch/labeler/config"
	"bsky.watch/labeler/server"
	"bsky.watch/labeler/simpleapi"
	"bsky.watch/utils/xrpcauth"

	"bsky.watch/modkit/pkg/cliutil"
	"bsky.watch/modkit/pkg/config"
)

func runMain(ctx context.Context) error {
	ctx = cliutil.SetupLogging(ctx, &cfg.LoggingConfig)
	log := zerolog.Ctx(ctx)

	if cfg.ConfigPath == "" {
		return fmt.Errorf("please provide config file")
	}

	b, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		return fmt.Errorf("reading config file: %w", err)
	}

	var modkitConfig config.Config
	if err := yaml.Unmarshal(b, &modkitConfig); err != nil {
		return fmt.Errorf("parsing config file: %w", err)
	}

	var labelerCfg labelerconfig.Config
	labelerCfg.DID = modkitConfig.ModerationAccount.DID
	labelerCfg.Password = modkitConfig.ModerationAccount.Password
	labelerCfg.Endpoint = fmt.Sprintf("https://%s", modkitConfig.PublicHostname)
	labelerCfg.PostgresURL = cfg.PostgresURL
	labelerCfg.PrivateKey = modkitConfig.LabelSigningKey
	labelerCfg.Labels = modkitConfig.LabelerPolicies
	labelerCfg.UpdateLabelValues()

	server, err := server.NewWithConfig(ctx, &labelerCfg)
	if err != nil {
		return fmt.Errorf("instantiating a server: %w", err)
	}

	server.SetAllowedLabels(labelerCfg.LabelValues())

	if cfg.CloneFrom != "" {
		return cloneLabeler(ctx, server, cfg.CloneFrom)
	}

	if labelerCfg.Password != "" && len(labelerCfg.Labels.LabelValueDefinitions) > 0 {
		client := xrpcauth.NewClientWithTokenSource(ctx, xrpcauth.PasswordAuth(labelerCfg.DID, labelerCfg.Password))
		err := account.UpdateLabelDefs(ctx, client, &labelerCfg.Labels)
		if err != nil {
			return fmt.Errorf("updating label definitions: %w", err)
		}
	}

	if cfg.AdminAddr != "" {
		frontend := simpleapi.New(server)
		mux := http.NewServeMux()
		mux.Handle("/label", frontend)

		go func() {
			if err := http.ListenAndServe(cfg.AdminAddr, mux); err != nil {
				log.Fatal().Err(err).Msgf("Failed to start listening on admin API address: %s", err)
			}
		}()
	}

	if cfg.MetricsAddr != "" {
		http.Handle("/metrics", promhttp.Handler())

		go func() {
			if err := http.ListenAndServe(cfg.MetricsAddr, nil); err != nil {
				log.Fatal().Err(err).Msgf("Failed to start HTTP server for exporting metrics")
			}
		}()
	}

	mux := http.NewServeMux()
	mux.Handle("/xrpc/com.atproto.label.subscribeLabels", server.Subscribe())
	mux.Handle("/xrpc/com.atproto.label.queryLabels", server.Query())

	log.Info().Msgf("Starting HTTP listener...")
	return http.ListenAndServe(cfg.ListenAddr, mux)
}

func main() {
	flag.StringVar(&cfg.ConfigPath, "config", "", "Path to the config file")
	flag.StringVar(&cfg.ListenAddr, "listen-addr", ":8080", "Address to expose metrics on")
	flag.StringVar(&cfg.MetricsAddr, "metrics-addr", ":8081", "Address to expose metrics on")
	flag.StringVar(&cfg.AdminAddr, "admin-addr", ":8082", "Address to expose admin API on")
	flag.StringVar(&cfg.PostgresURL, "db-url", "", "URL of the Postgres database to use")
	flag.StringVar(&cfg.CloneFrom, "clone-from", "", "URL of a labeler to clone")

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

func cloneLabeler(ctx context.Context, server *server.Server, endpoint string) error {
	empty, err := server.IsEmpty()
	if err != nil {
		return err
	}
	if !empty {
		return fmt.Errorf("refusing to do import into a non-empty database")
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	if u.Scheme == "http" {
		u.Scheme = "ws"
	} else {
		u.Scheme = "wss"
	}
	u.Path = "/xrpc/com.atproto.label.subscribeLabels"
	u.RawQuery = "cursor=0"

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", u.String(), err)
	}

	entries := map[int64]atproto.LabelDefs_Label{}
	for {
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return fmt.Errorf("setting read deadline: %w", err)
		}
		_, b, err := conn.ReadMessage()
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) || strings.HasSuffix(err.Error(), os.ErrDeadlineExceeded.Error()) {
				break
			}
			return fmt.Errorf("reading from websocket: %w", err)
		}

		if !bytes.HasPrefix(b, []byte("\xa2atg#labelsbop\x01")) {
			fmt.Printf("Unexpected prefix: %q", string(b))
			continue
		}
		labels := &atproto.LabelSubscribeLabels_Labels{}
		err = labels.UnmarshalCBOR(bytes.NewBuffer(bytes.TrimPrefix(b, []byte("\xa2atg#labelsbop\x01"))))
		if err != nil {
			return fmt.Errorf("unmarshaling labels: %w", err)
		}
		if len(labels.Labels) > 1 {
			return fmt.Errorf("unsupported: seq %d has more than one label", labels.Seq)
		}
		for _, label := range labels.Labels {
			op := "+"
			if label.Neg != nil && *label.Neg {
				op = "-"
			}
			fmt.Printf("%s %d\t%s\t%s\n", op, labels.Seq, label.Uri, label.Val)
			entries[labels.Seq] = *label
		}
	}
	conn.Close()

	return server.ImportEntries(entries)
}
