package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"bsky.watch/labeler/server"
	"bsky.watch/utils/didset"
	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/xrpc"
)

func startListUpdates(ctx context.Context, client *xrpc.Client, config map[string]string, server *server.Server, updateInterval time.Duration) {
	log := zerolog.Ctx(ctx)

	if err := updateOnce(ctx, client, config, server); err != nil {
		log.Error().Err(err).Msgf("Update failed: %s", err)
	}
	ticker := time.NewTicker(updateInterval)
	for {
		select {
		case <-ctx.Done():
			log.Info().Msgf("context cancelled, exiting")
			return
		case <-ticker.C:
			if err := updateOnce(ctx, client, config, server); err != nil {
				log.Error().Err(err).Msgf("Update failed: %s", err)
			}
		}
	}
}

func updateOnce(ctx context.Context, client *xrpc.Client, config map[string]string, server *server.Server) error {
	log := zerolog.Ctx(ctx)

	for label, list := range config {
		if err := updateFromList(ctx, client, server, label, list); err != nil {
			log.Error().Err(err).Str("label", label).Msgf("Failed to update label entries: %s", err)
		}
	}
	return nil
}

func updateFromList(ctx context.Context, client *xrpc.Client, server *server.Server, label string, listUri string) error {
	log := zerolog.Ctx(ctx).With().Str("label", label).Logger()
	ctx = log.WithContext(ctx)

	entries, err := server.LabelEntries(ctx, label)
	if err != nil {
		return fmt.Errorf("getting existing label entries: %w", err)
	}
	labeledDids := didset.StringSet{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Uri, "did:") {
			continue
		}
		labeledDids[entry.Uri] = true
	}
	log.Debug().Msgf("Currently labeled accounts: %d", len(labeledDids))

	var list didset.StringSet

	if cfg.ListServerURL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.ListServerURL, nil)
		if err != nil {
			return fmt.Errorf("creating request object: %w", err)
		}
		q := req.URL.Query()
		q.Set("uri", listUri)
		req.URL.RawQuery = q.Encode()

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("sending request to listserver: %w", err)
		}

		var dids []string
		err = json.NewDecoder(resp.Body).Decode(&dids)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("parsing listserver response: %w", err)
		}

		list = didset.StringSet{}
		for _, did := range dids {
			list[did] = true
		}
	} else {
		// Note: This uses `app.bsky.graph.getList` method, which filters out accounts that have blocked you.
		list, err = didset.MuteList(client, listUri).GetDIDs(ctx)
		if err != nil {
			return fmt.Errorf("getting list content: %w", err)
		}
	}
	log.Debug().Msgf("Number of list members: %d", len(list))

	toAdd, _ := didset.Difference(list, labeledDids).GetDIDs(ctx)
	toRemove, _ := didset.Difference(labeledDids, list).GetDIDs(ctx)
	if len(toAdd)+len(toRemove) == 0 {
		return nil
	}
	log.Debug().Msgf("Adding %d and removing %d labels", len(toAdd), len(toRemove))

	for did := range toAdd {
		_, err := server.AddLabel(ctx, atproto.LabelDefs_Label{
			Uri: did,
			Val: label,
		})
		if err != nil {
			return err
		}
		log.Debug().Msgf("Added %s", did)
	}
	for did := range toRemove {
		neg := true
		_, err := server.AddLabel(ctx, atproto.LabelDefs_Label{
			Uri: did,
			Val: label,
			Neg: &neg,
		})
		if err != nil {
			return err
		}
		log.Debug().Msgf("Removed %s", did)
	}
	return nil
}
