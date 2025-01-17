package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/rs/zerolog"

	"github.com/bluesky-social/indigo/api/atproto"

	"bsky.watch/modkit/pkg/tickets"
)

func requestedApplyLabels(ticket *Issue) bool {
	return ticket.Tracker != nil &&
		ticket.Tracker.Id == tickets.Mappings().TicketTypes.RecordTicket &&
		ticket.Status != nil &&
		ticket.Status.Id == tickets.Mappings().Statuses.Completed
}

func (h *handler) applyLabelsAndUpdateStatus(ctx context.Context, ticket *Issue) error {
	log := zerolog.Ctx(ctx)

	updateOpts := []tickets.TicketOption{tickets.Status(tickets.StatusApplied)}

	text, addErr := h.applyLabels(ctx, ticket)
	redmineTicket, err := h.ticketsClient.Issue(ticket.Id)
	if err != nil {
		return fmt.Errorf("failed to fetch the current state of the ticket: %w", err)
	}
	updateOpts = append(updateOpts, tickets.WithNote(text))
	if addErr != nil {
		log.Error().Err(addErr).Msgf("Failed to update labels: %s", err)
		updateOpts = []tickets.TicketOption{
			tickets.Status(tickets.StatusInProgress),
			tickets.WithNote(fmt.Sprintf("Failed to apply updates: %s", addErr)),
		}
	}

	_, err = tickets.Update(ctx, h.ticketsClient, redmineTicket, updateOpts...)
	if err != nil {
		return err
	}
	return nil
}

func (h *handler) applyLabels(ctx context.Context, ticket *Issue) (string, error) {
	mappings := tickets.Mappings()

	subjectField, found := ticket.CustomField(mappings.Fields.Subject)
	if !found {
		return "", fmt.Errorf("subject not set")
	}
	var subject string
	if err := json.Unmarshal(subjectField.Value, &subject); err != nil {
		return "", fmt.Errorf("failed to parse subject: %w", err)
	}
	if subject == "" {
		return "", fmt.Errorf("subject not set")
	}

	labels, found := ticket.CustomField(mappings.Fields.Labels)
	if !found {
		return "", fmt.Errorf("Labels field is not set")
	}

	var values []string
	if err := json.Unmarshal(labels.Value, &values); err != nil {
		return "", fmt.Errorf("unmarshaling labels from JSON: %w", err)
	}

	shouldHaveLabel := map[string]bool{}
	for _, value := range values {
		if value == "" {
			continue
		}
		id := extractListId(value)
		if id == "" {
			return "", fmt.Errorf("no label ID in the value %q", value)
		}
		// TODO: extract some of this into helper methods on the config struct.
		for _, l := range h.config.SkipLabels {
			if id == l {
				return "", fmt.Errorf("label %q is disabled and should not be applied", id)
			}
		}
		found := false
		for _, l := range h.config.LabelerPolicies.LabelValueDefinitions {
			if l.Identifier == id {
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("missing mapping for %q", id)
		}
		shouldHaveLabel[id] = true
	}

	labelerClient := *h.client
	labelerClient.Host = cfg.LabelerPublicURL

	// XXX: our labeler implementation doesn't do pagination and returns
	// everything in one go, so we don't need to do multiple requests.
	labelsResp, err := atproto.LabelQueryLabels(ctx, &labelerClient, "", 100, nil, []string{subject})
	if err != nil {
		return "", fmt.Errorf("failed to query existing labels: %w", err)
	}

	result := listUpdateResult{}

	r, err := h.tryApplyingLabels(ctx, subject, labelsResp, shouldHaveLabel)
	if err != nil {
		return "", err
	}

	result.added = append(result.added, r.added...)
	result.removed = append(result.removed, r.removed...)

	lines := []string{}
	if len(result.added) == 0 && len(result.removed) == 0 {
		lines = append(lines, "No changes made, labels already match the desired state.")
	}
	if len(result.added) > 0 {
		lines = append(lines, "", "Added labels:", "")
		for _, s := range result.added {
			lines = append(lines, "* "+s)
		}
	}
	if len(result.removed) > 0 {
		lines = append(lines, "", "Removed labels:", "")
		for _, s := range result.removed {
			lines = append(lines, "* "+s)
		}
	}
	return strings.Join(lines, "\n"), nil
}

func (h *handler) tryApplyingLabels(ctx context.Context, subject string, currentLabels *atproto.LabelQueryLabels_Output, shouldHaveLabel map[string]bool) (listUpdateResult, error) {
	add := []string{}
	remove := []string{}

	result := listUpdateResult{}

	for _, label := range h.config.LabelerPolicies.LabelValueDefinitions {
		id := label.Identifier

		hasLabel := false
		for _, l := range currentLabels.Labels {
			if l.Val != id {
				continue
			}
			hasLabel = true
			if !shouldHaveLabel[id] {
				remove = append(remove, id)
			}
		}
		if !hasLabel && shouldHaveLabel[id] {
			add = append(add, id)
		}
	}

	if len(add) > 0 || len(remove) > 0 {
		adminUrl, err := url.Parse(cfg.LabelerAdminURL)
		if err != nil {
			return result, fmt.Errorf("failed to parse %q as URL: %w", cfg.LabelerAdminURL, err)
		}
		adminUrl.Path = "/label"

		payloads := []*atproto.LabelDefs_Label{}
		for _, label := range add {
			payloads = append(payloads, &atproto.LabelDefs_Label{
				Val: label,
				Uri: subject,
			})
		}
		for _, label := range remove {
			payloads = append(payloads, &atproto.LabelDefs_Label{
				Val: label,
				Uri: subject,
				Neg: ptr(true),
			})
		}

		for _, payload := range payloads {
			b, err := json.Marshal(payload)
			if err != nil {
				return result, fmt.Errorf("failed to serialize label as JSON: %w", err)
			}

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, adminUrl.String(), bytes.NewReader(b))
			if err != nil {
				return result, fmt.Errorf("constructing request object: %w", err)
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return result, fmt.Errorf("sending request: %w", err)
			}
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 400 {
				if payload.Neg != nil && *payload.Neg == true {
					return result, fmt.Errorf("failed to remove label: %s %s", resp.Status, string(respBody))
				} else {
					return result, fmt.Errorf("failed to add label: %s %s", resp.Status, string(respBody))
				}
			}
			if resp.StatusCode == http.StatusCreated {
				if payload.Neg != nil && *payload.Neg == true {
					result.removed = append(result.removed, payload.Val)
				} else {
					result.added = append(result.added, payload.Val)
				}
			}
		}
	}

	return result, nil
}
