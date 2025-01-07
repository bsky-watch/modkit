package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"

	"github.com/Jille/convreq"
	"github.com/Jille/convreq/respond"
	"github.com/mattn/go-redmine"
	"github.com/rs/zerolog"

	"bsky.watch/modkit/pkg/config"
	"github.com/bluesky-social/indigo/xrpc"
)

var requestId atomic.Uint64

type handler struct {
	listServerURL     string
	ticketsClient     *redmine.Client
	listUpdateClients map[string]*xrpc.Client
	config            *config.Config

	wrapped http.HandlerFunc
}

func NewHandler(ticketsClient *redmine.Client, config *config.Config, listUpdateClients map[string]*xrpc.Client, listServerUrl string) *handler {
	h := &handler{
		listServerURL:     listServerUrl,
		ticketsClient:     ticketsClient,
		listUpdateClients: listUpdateClients,
		config:            config,
	}

	h.wrapped = convreq.Wrap(h.HandleWebhook)

	return h
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	h.wrapped.ServeHTTP(w, req)
}

func (h *handler) HandleWebhook(ctx context.Context, req *http.Request) convreq.HttpResponse {
	ctx = context.WithoutCancel(ctx)
	log := zerolog.Ctx(ctx).With().Uint64("request_id", requestId.Add(1)).Logger()
	ctx = log.WithContext(ctx)

	defer req.Body.Close()

	var fullPayload interface{}
	if err := json.NewDecoder(req.Body).Decode(&fullPayload); err != nil {
		log.Error().Err(err).Msgf("Failed to parse the payload: %s", err)
		return respond.BadRequest("bad JSON")
	}

	b, err := json.MarshalIndent(fullPayload, "", "  ")
	if err != nil {
		return respond.InternalServerError("failed to serialize")
	}

	log.Info().Msgf("Payload: %s", string(b))

	payload := &webhookRequest{}

	if err := json.Unmarshal(b, payload); err != nil {
		log.Error().Err(err).Msgf("Failed to parse payload: %s", err)
		return respond.BadRequest("bad payload")
	}

	err = h.processPayload(ctx, &payload.Payload)
	if err != nil {
		log.Error().Err(err).Msgf("Processing failed: %s", err)
		return respond.BadGateway("error")
	}

	return respond.String("OK")
}

func (h *handler) processPayload(ctx context.Context, payload *WebhookPayload) error {
	log := zerolog.Ctx(ctx)
	log.Debug().Interface("payload", payload).Msgf("Payload: %+v", payload)

	switch {
	case requestedAddingToLists(payload.Issue):
		log.Info().Msgf("Requested adding to lists")
		return h.addToLists(ctx, payload.Issue)
	}
	return nil
}
