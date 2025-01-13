package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/rs/zerolog/log"

	"bsky.watch/utils/bskyurl"
	"bsky.watch/utils/xrpcauth"

	"bsky.watch/modkit/pkg/attachments"
	"bsky.watch/modkit/pkg/format"
	"bsky.watch/modkit/pkg/tickets"
)

func (h *handler) updateJournalIfNeeded(ctx context.Context, ticket *Issue) error {
	client := xrpcauth.NewAnonymousClient(ctx)
	client.Host = "https://public.api.bsky.app"

	redmineTicket, err := h.ticketsClient.IssueWithArgs(ticket.Id, map[string]string{"include": "journals"})
	if err != nil {
		return fmt.Errorf("fetching notes: %w", err)
	}

	journals := redmineTicket.Journals
	// Unset, so it doesn't interefere with update calls.
	redmineTicket.Journals = nil

	for _, journal := range journals {
		comment := journal.Notes

		if strings.Index(comment, "\n") >= 0 {
			continue
		}
		if !strings.HasPrefix(comment, "https://bsky.app/profile/") {
			continue
		}

		url, err := bskyurl.DetermineTarget(comment)
		if err != nil {
			return err
		}
		switch target := url.(type) {
		case *bskyurl.Post:
			did := ""
			if strings.HasPrefix(target.Profile, "did:") {
				did = target.Profile
			} else {
				resolved, err := comatproto.IdentityResolveHandle(ctx, h.client, target.Profile)
				if err != nil {
					return fmt.Errorf("resolving handle %q: %w", target.Profile, err)
				}
				did = resolved.Did
			}

			record, err := comatproto.RepoGetRecord(ctx, client, "", "app.bsky.feed.post", did, target.Rkey)
			if err != nil {
				return fmt.Errorf("RepoGetRecord: %w", err)
			}
			post, ok := record.Value.Val.(*bsky.FeedPost)
			if !ok {
				return fmt.Errorf("unexpected record type %T", post)
			}

			profile, err := bsky.ActorGetProfile(ctx, h.client, did)
			if err != nil {
				return fmt.Errorf("ActorGetProfile: %w", err)
			}

			ticketsClient := h.ticketsClient
			if journal.User != nil {
				user, err := h.ticketsClient.User(journal.User.Id)
				if err != nil {
					return fmt.Errorf("fetching user info id=%d: %w", journal.User.Id, err)
				}

				ticketsClient = h.ticketsClient.Impersonate(user.Login)
			}
			uploader := attachments.NewGlobalAttachmentCreator(ticketsClient)
			formatted, err := format.Post(ctx, h.client, post, profile, target.Rkey, uploader)
			if err != nil {
				return fmt.Errorf("formatting the post: %w", err)
			}
			if b, err := json.MarshalIndent(post, "", "  "); err == nil {
				if _, err := uploader.Upload(ctx, fmt.Sprintf("post_%s.json", target.Rkey), b); err != nil {
					log.Warn().Err(err).Msgf("Failed to upload post.json: %s", err)
				}
			}

			if len(uploader.Created()) > 0 {
				redmineTicket, err = tickets.Update(ctx, ticketsClient, redmineTicket, tickets.Attachments(uploader.Created()))
				if err != nil {
					return fmt.Errorf("adding attachments: %w", err)
				}
			}

			// Do update w/o impersonation, since the user might not have edit permissions.
			journal.Notes = formatted
			err = h.ticketsClient.UpdateJournal(journal)
			if err != nil {
				return err
			}

		case *bskyurl.Profile:
		}
	}
	return nil
}
