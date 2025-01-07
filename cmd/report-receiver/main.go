package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Jille/convreq"
	"github.com/Jille/convreq/respond"
	"github.com/gorilla/websocket"
	_ "github.com/joho/godotenv/autoload"
	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/valkey-io/valkey-go"

	comatproto "github.com/bluesky-social/indigo/api/atproto"

	"bsky.watch/utils/bskyurl"

	"bsky.watch/modkit/pkg/cliutil"
	"bsky.watch/modkit/pkg/metrics"
	"bsky.watch/modkit/pkg/reportqueue"
)

func runMain(ctx context.Context) error {
	ctx = cliutil.SetupLogging(ctx, &cfg.LoggingConfig)
	log := zerolog.Ctx(ctx)
	log.Info().Msgf("Starting...")

	if cfg.ConfigPath != "" {
		if err := cfg.LoadDefaultsFromConfig(cfg.ConfigPath); err != nil {
			return err
		}
	}

	if len(cfg.OwnDIDs) == 0 {
		return fmt.Errorf("moderation account not configured, no reports would be accepted")
	}

	if cfg.TicketIDEncryptionKey == "" {
		return fmt.Errorf("missing ticket ID encryption key")
	}

	idCipher, err := reportqueue.NewIdCipher(cfg.TicketIDEncryptionKey)
	if err != nil {
		return err
	}

	var reportWriter *reportqueue.ValkeyWriter
	c, err := valkey.NewClient(valkey.ClientOption{
		InitAddress: []string{cfg.PersistentValkeyAddr},
	})
	if err != nil {
		return fmt.Errorf("creating valkey client: %w", err)
	}
	defer c.Close()

	reportWriter, err = reportqueue.NewValkeyWriter(ctx, c, uint(cfg.NodeId))
	if err != nil {
		return fmt.Errorf("creating report writer: %w", err)
	}
	log.Info().Msgf("Report writer instantiated: %s (our node id: %d)", cfg.PersistentValkeyAddr, cfg.NodeId)

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(cfg.MetricsAddr, nil); err != nil {
			log.Fatal().Err(err).Msgf("Failed to start HTTP server for exporting metrics")
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", convreq.Wrap(func() convreq.HttpResponse {
		return respond.Forbidden("forbidden")
	}))

	mux.HandleFunc("/xrpc/com.atproto.moderation.createReport", convreq.Wrap(createReport(ctx, idCipher, reportWriter)))
	mux.HandleFunc("/xrpc/com.atproto.label.queryLabels", convreq.Wrap(func() convreq.HttpResponse {
		return respond.JSON(map[string]any{"labels": []string{}})
	}))
	mux.HandleFunc("/xrpc/com.atproto.label.subscribeLabels", dummyWebsocket)

	log.Info().Msgf("Startup complete")

	return http.ListenAndServe(cfg.AtprotoListenAddr, mux)
}

func createReport(ctx context.Context, idCipher *reportqueue.IdCipher, reportWriter *reportqueue.ValkeyWriter) func(ctx context.Context, req *http.Request) convreq.HttpResponse {
	updateMetrics := func(success bool, statusCode int, start time.Time) {
		duration := time.Since(start).Seconds()
		metrics.RequestStatus.WithLabelValues("com.atproto.moderation.createReport", fmt.Sprint(success), fmt.Sprint(statusCode)).Inc()
		metrics.RequestDuration.WithLabelValues("com.atproto.moderation.createReport", fmt.Sprint(success), fmt.Sprint(statusCode)).Add(duration)
		metrics.RequestStats.WithLabelValues("com.atproto.moderation.createReport", fmt.Sprint(success)).Observe(duration)
	}

	return func(ctx context.Context, req *http.Request) convreq.HttpResponse {
		start := time.Now()
		log := zerolog.Ctx(ctx)

		did, err := validateCredentials(ctx, req, cfg.OwnDIDs)
		if err != nil {
			log.Info().Err(err).Msgf("Received invalid request: %s", err)
			updateMetrics(false, http.StatusForbidden, start)
			return respond.Forbidden("forbidden")
		}

		log = ptr(log.With().Str("sender", did).Logger())

		log.Info().Msgf("Received request from %q", did)

		reader := &io.LimitedReader{R: req.Body, N: 16 * 1024}
		body, err := io.ReadAll(reader)
		if err != nil {
			log.Warn().Err(err).Msgf("Failed to read request body: %s", err)
			updateMetrics(false, http.StatusBadRequest, start)
			return respond.BadRequest("failed to read request body")
		}
		if reader.N == 0 {
			log.Warn().Msgf("Request is too large")
			updateMetrics(false, http.StatusRequestEntityTooLarge, start)
			return respond.PayloadTooLarge("request is too large")
		}
		var report comatproto.ModerationCreateReport_Input
		if err := json.Unmarshal(body, &report); err != nil {
			log.Warn().Err(err).Msgf("Failed to decode request: %s", err)
			updateMetrics(false, http.StatusBadRequest, start)
			return respond.BadRequest("bad request")
		}

		if report.Subject == nil {
			updateMetrics(false, http.StatusBadRequest, start)
			return respond.BadRequest("missing subject")
		}

		subject := ""
		switch {
		case report.Subject.RepoStrongRef != nil:
			subject = report.Subject.RepoStrongRef.Uri
		case report.Subject.AdminDefs_RepoRef != nil:
			subject = report.Subject.AdminDefs_RepoRef.Did
		}
		if subject == "" {
			updateMetrics(false, http.StatusBadRequest, start)
			return respond.BadRequest("missing subject")
		}

		url, err := bskyurl.DetermineTarget(subject)
		if err != nil {
			log.Warn().Err(err).Msgf("Failed to parse subject: %s", err)
			updateMetrics(false, http.StatusBadRequest, start)
			return respond.BadRequest("bad subject")
		}
		_, ok := url.(bskyurl.TargetWithProfile)
		if !ok {
			log.Warn().Err(err).Msgf("Unsupported URI %q", subject)
			updateMetrics(false, http.StatusBadRequest, start)
			return respond.BadRequest("bad subject")
		}

		log = ptr(log.With().Str("subject", subject).Logger())

		reportId, err := reportWriter.AddReport(ctx, did, body)
		if err != nil {
			log.Error().Err(err).Msgf("Failed to write report to the queue: %s", err)
			updateMetrics(false, http.StatusInternalServerError, start)
			return respond.InternalServerError("oops")
		}
		encrypted := idCipher.Encrypt(reportId)
		log.Info().Uint64("report_id", reportId).
			Int64("encrypted_report_id", encrypted).
			Msgf("Report was written to the queue with ID %d", reportId)

		var response comatproto.ModerationCreateReport_Output
		json.Unmarshal(body, &response)
		response.Id = encrypted
		response.CreatedAt = time.Now().Format(time.RFC3339)
		response.ReportedBy = did

		updateMetrics(true, http.StatusOK, start)
		return respond.JSON(&response)
	}
}

func dummyWebsocket(w http.ResponseWriter, req *http.Request) {
	upgrader := websocket.Upgrader{}
	c, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
	defer c.Close()
	for {
		_, _, err := c.ReadMessage()
		if err != nil {
			break
		}
	}
}

func main() {
	ownDIDs := flag.String("own-dids", "", "List of DIDs to accept reports for")
	flag.StringVar(&cfg.ConfigPath, "config", "", "Path to the config file")
	flag.StringVar(&cfg.AtprotoListenAddr, "listen-addr", ":8080", "Address to accept reports on")
	flag.StringVar(&cfg.MetricsAddr, "metrics-addr", ":8081", "Address to expose metrics on")
	flag.StringVar(&cfg.TicketIDEncryptionKey, "ticket-id-encryption-key", "", "Secret key for encrypting IDs returned to the user")
	flag.StringVar(&cfg.PersistentValkeyAddr, "valkey-addr", "", "Address of the valkey instance to use")
	flag.IntVar(&cfg.NodeId, "node-id", 1, "Node ID to use for generating globally unique report IDs")

	cliutil.RegisterLoggingFlags(&cfg.LoggingConfig)

	if err := envconfig.Process("modkit", &cfg); err != nil {
		log.Fatalf("envconfig.Process: %s", err)
	}

	flag.Parse()

	if *ownDIDs != "" {
		cfg.OwnDIDs = strings.Split(*ownDIDs, ",")
	}

	if err := runMain(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

func ptr[T any](v T) *T {
	return &v
}
