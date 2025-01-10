package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"bsky.watch/redmine"
	"github.com/rs/zerolog"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"

	"bsky.watch/utils/bskyurl"

	"bsky.watch/modkit/pkg/attachments"
	"bsky.watch/modkit/pkg/format"
	"bsky.watch/modkit/pkg/tickets"
)

func requestedMetadataUpdate(ticket *Issue) bool {
	return strings.HasPrefix(ticket.Subject, "https://bsky.app/profile/")
}

func (h *handler) updateMetadata(ctx context.Context, ticket *Issue, newTicket bool) error {
	log := zerolog.Ctx(ctx).With().Int("ticket_id", ticket.Id).Logger()
	url, err := bskyurl.DetermineTarget(ticket.Subject)
	if err != nil {
		return err
	}
	did := ""
	switch url := url.(type) {
	case bskyurl.TargetWithProfile:
		s := url.GetProfile()
		if !strings.HasPrefix(s, "did:") {
			resp, err := comatproto.IdentityResolveHandle(ctx, h.client, s)
			if err != nil {
				return fmt.Errorf("failed to resolve handle %q: %w", s, err)
			}
			s = resp.Did
		}
		did = s
	}
	if did == "" {
		return fmt.Errorf("failed to extract DID from %q", ticket.Subject)
	}

	profile, err := bsky.ActorGetProfile(ctx, h.client, did)
	if err != nil {
		return err
	}

	opts := []tickets.TicketOption{
		tickets.Subject(profile.Handle),
		tickets.DID(did),
		tickets.Handle(profile.Handle),
	}
	if profile.DisplayName != nil {
		opts = append(opts, tickets.DisplayName(*profile.DisplayName))
	}

	redmineTicket, err := h.ticketsClient.Issue(ticket.Id)
	if err != nil {
		return fmt.Errorf("failed to fetch the current state of the ticket: %w", err)
	}

	redmineTicket, err = tickets.Update(ctx, h.ticketsClient, redmineTicket, opts...)
	if err != nil {
		return err
	}

	if redmineTicket.Description == "" {
		uploader := attachments.NewGlobalAttachmentCreator(h.ticketsClient)

		if b, err := json.Marshal(profile); err == nil {
			if _, err := uploader.Upload(ctx, "profile.json", b); err != nil {
				log.Warn().Err(err).Msgf("Failed to upload profile.json: %s", err)
			}
		}

		text := ""

		switch url := url.(type) {
		case *bskyurl.Post:
			record, err := comatproto.RepoGetRecord(ctx, h.client, "", "app.bsky.feed.post", did, url.Rkey)
			if err != nil {
				return fmt.Errorf("fetching post: %w", err)
			}
			post, ok := record.Value.Val.(*bsky.FeedPost)
			if !ok {
				return fmt.Errorf("post if on unexpected type %T", record.Value.Val)
			}
			postText, err := format.Post(ctx, h.client, post, profile, url.Rkey, uploader)
			if err != nil {
				return fmt.Errorf("formatting post: %w", err)
			}
			text += postText + "\n\n"

			if b, err := json.Marshal(post); err == nil {
				if _, err := uploader.Upload(ctx, "post.json", b); err != nil {
					log.Warn().Err(err).Msgf("Failed to upload post.json: %s", err)
				}
			}
		}

		profileText, err := format.Profile(ctx, profile, uploader)
		if err != nil {
			return fmt.Errorf("formatting profile: %w", err)
		}
		text += profileText

		redmineTicket, err = tickets.Update(ctx, h.ticketsClient, redmineTicket,
			tickets.Description(text), tickets.Attachments(uploader.Created()))
		if err != nil {
			return err
		}
	}

	if newTicket && redmineTicket.Tracker != nil && redmineTicket.Tracker.Id == tickets.Mappings().TicketTypes.Ticket {
		similarTickets, err := tickets.FindByDID(ctx, h.ticketsClient, did)
		if err != nil {
			return fmt.Errorf("checking for other tickets for the same DID: %w", err)
		}

		dedupeCandidates := []redmine.Issue{}

		for _, t := range similarTickets {
			if t.Id == redmineTicket.Id {
				continue
			}
			if t.Priority != nil && t.Priority.Id == tickets.Mappings().Priorities.Low {
				continue
			}
			if t.Tracker != nil && t.Tracker.Id != tickets.Mappings().TicketTypes.Ticket {
				continue
			}
			if t.Status != nil && t.Status.Id == tickets.Mappings().Statuses.Duplicate {
				continue
			}
			dedupeCandidates = append(dedupeCandidates, t)

		}

		if len(dedupeCandidates) > 0 {
			// TODO: set appropriate duplicate relationship
			redmineTicket, err = tickets.Update(ctx, h.ticketsClient, redmineTicket,
				tickets.Status(tickets.StatusDuplicate))
			if err != nil {
				return err
			}
		}
	}

	return nil
}
