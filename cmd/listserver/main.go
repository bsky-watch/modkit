package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"maps"
	"net/http"
	"os"
	"slices"

	"github.com/Jille/convreq"
	"github.com/Jille/convreq/respond"
	_ "github.com/joho/godotenv/autoload"
	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"

	"bsky.watch/utils/aturl"
	"bsky.watch/utils/firehose"
	"bsky.watch/utils/listserver"
	"bsky.watch/utils/xrpcauth"

	"bsky.watch/modkit/pkg/cliutil"
	"bsky.watch/modkit/pkg/config"
)

func runMain(ctx context.Context) error {
	ctx = cliutil.SetupLogging(ctx, &cfg.LoggingConfig)
	log := zerolog.Ctx(ctx)

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(cfg.MetricsAddr, nil); err != nil {
			log.Fatal().Err(err).Msgf("Failed to start HTTP server for exporting metrics")
		}
	}()

	if cfg.ConfigPath == "" {
		return fmt.Errorf("please provide config file")
	}

	var modkitConfig config.Config
	b, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		return fmt.Errorf("reading %q: %w", cfg.ConfigPath, err)
	}
	if err := yaml.Unmarshal(b, &modkitConfig); err != nil {
		return fmt.Errorf("parsing %q: %w", cfg.ConfigPath, err)
	}

	dids := map[string]bool{}
	for _, l := range modkitConfig.Lists {
		u, err := aturl.Parse(l.URI)
		if err != nil {
			return fmt.Errorf("parsing %q: %w", l.URI, err)
		}
		dids[u.Host] = true
	}
	for _, l := range modkitConfig.LabelsFromLists {
		u, err := aturl.Parse(l)
		if err != nil {
			return fmt.Errorf("parsing %q: %w", l, err)
		}
		dids[u.Host] = true
	}

	server := listserver.New(xrpcauth.NewAnonymousClient(ctx), slices.Sorted(maps.Keys(dids))...)

	mux := http.NewServeMux()
	mux.Handle("/xrpc/watch.bsky.list.getMemberships", server)
	mux.HandleFunc("/xrpc/watch.bsky.list.getMembers", convreq.Wrap(dumpList(server)))

	go func() {
		if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
			log.Fatal().Err(err).Msgf("Failed to start HTTP server: %s", err)
		}
	}()

	f := firehose.New()
	f.Hooks = []firehose.Hook{server.UpdateFromFirehose()}

	errCh := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		errCh <- f.Run(ctx)
	}()
	// Yield to scheduler to actually start the goroutine
	<-started

	log.Info().Msgf("Starting listserver sync...")
	if err := server.Sync(ctx); err != nil {
		return err
	}

	log.Info().Msgf("Startup complete")

	return <-errCh
}

func main() {
	flag.StringVar(&cfg.ConfigPath, "config", "", "Path to the config file")
	flag.StringVar(&cfg.ListenAddr, "listen-addr", ":8080", "Address to expose metrics on")
	flag.StringVar(&cfg.MetricsAddr, "metrics-addr", ":8081", "Address to expose metrics on")

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

func dumpList(server *listserver.Server) func(ctx context.Context, req *http.Request) convreq.HttpResponse {
	return func(ctx context.Context, req *http.Request) convreq.HttpResponse {
		uri := req.FormValue("uri")
		if uri == "" {
			return respond.BadRequest("missing uri")
		}
		list, err := server.List(uri)
		if err != nil {
			return respond.BadRequest(fmt.Sprintf("%s", err))
		}
		dids, err := list.GetDIDs(ctx)
		if err != nil {
			return respond.InternalServerError(fmt.Sprintf("%s", err))
		}
		return respond.JSON(slices.Collect(maps.Keys(dids)))
	}
}
