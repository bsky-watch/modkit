package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync/atomic"
	"time"

	"bsky.watch/redmine"
	"github.com/Jille/convreq"
	"github.com/Jille/convreq/respond"
	"github.com/rs/zerolog"

	"bsky.watch/modkit/pkg/config"
	"bsky.watch/modkit/pkg/metrics"
	"github.com/bluesky-social/indigo/xrpc"
)

var requestId atomic.Uint64

type handler struct {
	client            *xrpc.Client
	listServerURL     string
	ticketsClient     *redmine.Client
	listUpdateClients map[string]*xrpc.Client
	config            *config.Config
	myId              int

	wrapped http.HandlerFunc
}

func NewHandler(ticketsClient *redmine.Client, config *config.Config, client *xrpc.Client, listUpdateClients map[string]*xrpc.Client, listServerUrl string) (*handler, error) {
	me, err := ticketsClient.MyAccount()
	if err != nil {
		return nil, err
	}

	h := &handler{
		client:            client,
		listServerURL:     listServerUrl,
		ticketsClient:     ticketsClient,
		listUpdateClients: listUpdateClients,
		config:            config,
		myId:              me.Id,
	}

	h.wrapped = convreq.Wrap(h.HandleWebhook)

	return h, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	h.wrapped.ServeHTTP(w, req)
}

func (h *handler) HandleWebhook(ctx context.Context, req *http.Request) convreq.HttpResponse {
	ctx = context.WithoutCancel(ctx)
	log := zerolog.Ctx(ctx).With().Uint64("request_id", requestId.Add(1)).Logger()
	ctx = log.WithContext(ctx)

	updateMetrics := func(success bool, statusCode int, start time.Time) {
		duration := time.Since(start).Seconds()
		metrics.RequestStatus.WithLabelValues("redmine-webhook", fmt.Sprint(success), fmt.Sprint(statusCode)).Inc()
		metrics.RequestDuration.WithLabelValues("redmine-webhook", fmt.Sprint(success), fmt.Sprint(statusCode)).Add(duration)
		metrics.RequestStats.WithLabelValues("redmine-webhook", fmt.Sprint(success)).Observe(duration)
	}

	defer req.Body.Close()

	start := time.Now()

	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to read request body: %s", err)
		updateMetrics(false, http.StatusBadRequest, start)
		return respond.BadRequest("failed to read request body")
	}

	if cfg.DumpPayloads {
		var fullPayload interface{}
		if err := json.Unmarshal(body, &fullPayload); err != nil {
			log.Error().Err(err).Msgf("Failed to parse the payload: %s", err)
			updateMetrics(false, http.StatusBadRequest, start)
			return respond.BadRequest("bad JSON")
		}

		b, err := json.MarshalIndent(fullPayload, "", "  ")
		if err != nil {
			updateMetrics(false, http.StatusInternalServerError, start)
			return respond.InternalServerError("failed to serialize")
		}

		log.Info().Msgf("Payload: %s", string(b))
	}

	payload := &webhookRequest{}

	if err := json.Unmarshal(body, payload); err != nil {
		log.Error().Err(err).Msgf("Failed to parse payload: %s", err)
		updateMetrics(false, http.StatusBadRequest, start)
		return respond.BadRequest("bad payload")
	}

	if payload.Payload.Journal != nil &&
		payload.Payload.Journal.Author != nil &&
		payload.Payload.Journal.Author.Id == h.myId {
		log.Debug().Msgf("Invocation seems to be triggered by our own update, so not doing anything to avoid falling into recursion.")
		updateMetrics(true, http.StatusNoContent, start)
		return respond.NoContent("OK")
	}

	err = h.processPayload(ctx, &payload.Payload)
	if err != nil {
		log.Error().Err(err).Msgf("Processing failed: %s", err)
		updateMetrics(false, http.StatusBadGateway, start)
		return respond.BadGateway("error")
	}

	updateMetrics(true, http.StatusOK, start)
	return respond.String("OK")
}

func (h *handler) processPayload(ctx context.Context, payload *WebhookPayload) error {
	log := zerolog.Ctx(ctx)

	switch {
	case requestedAddingToLists(payload.Issue):
		log.Info().Msgf("Requested adding to lists")
		return h.addToListsAndUpdateStatus(ctx, payload.Issue)
	case requestedMetadataUpdate(payload.Issue):
		log.Info().Msgf("Metadata update request")
		return h.updateMetadata(ctx, payload.Issue, payload.Action == "opened")
	}
	return nil
}
