package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/imax9000/errors"
	"github.com/rs/zerolog"
	"golang.org/x/exp/maps"

	"bsky.watch/utils/aturl"
	"bsky.watch/utils/listserver"

	"bsky.watch/modkit/pkg/tickets"
)

func requestedAddingToLists(ticket *Issue) bool {
	return ticket.Tracker != nil &&
		ticket.Tracker.Id == tickets.Mappings().TicketTypes.Ticket &&
		ticket.Status != nil &&
		ticket.Status.Id == tickets.Mappings().Statuses.Completed
}

func (h *handler) getListMemberships(ctx context.Context, did string) (*listserver.Response, error) {
	if h.listServerURL == "" {
		return nil, fmt.Errorf("listserver address is not specified")
	}
	u, err := url.Parse(h.listServerURL)
	if err != nil {
		return nil, fmt.Errorf("parsing %q as a URL: %w", h.listServerURL, err)
	}
	query := u.Query()
	query.Set("subject", did)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating a request object: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	r := &listserver.Response{}
	if err := json.NewDecoder(resp.Body).Decode(r); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}
	return r, nil
}

func (h *handler) addToLists(ctx context.Context, ticket *Issue) error {
	log := zerolog.Ctx(ctx)
	mappings := tickets.Mappings()

	didField, found := ticket.CustomField(mappings.Fields.DID)
	if !found {
		return fmt.Errorf("DID not set")
	}
	var did string
	if err := json.Unmarshal(didField.Value, &did); err != nil {
		return fmt.Errorf("failed to parse DID: %w", err)
	}
	if did == "" {
		return fmt.Errorf("DID not set")
	}

	addToListsField, found := ticket.CustomField(mappings.Fields.AddToLists)
	if !found {
		return fmt.Errorf("AddToLists field is not set")
	}

	var values []string
	if err := json.Unmarshal(addToListsField.Value, &values); err != nil {
		return fmt.Errorf("unmarshaling AddToLists from JSON: %w", err)
	}

	shouldBeInList := map[string]bool{}
	for _, value := range values {
		if value == "" {
			continue
		}
		id := extractListId(value)
		if id == "" {
			return fmt.Errorf("no list ID in the value %q", value)
		}
		_, found := h.config.Lists[id]
		if !found {
			return fmt.Errorf("missing mapping for %q", id)
		}
		shouldBeInList[id] = true
	}

	memberships, err := h.getListMemberships(ctx, did)
	if err != nil {
		return fmt.Errorf("failed to get list memberships: %w", err)
	}

	result := listUpdateResult{}

	for owner, client := range h.listUpdateClients {
		memberships, found := memberships.Results[owner]
		if !found {
			continue
		}

		var addErr error
		var r listUpdateResult
		for attempt := 1; attempt <= 5; attempt++ {
			r, addErr = h.tryAddToLists(ctx, client, owner, did, &memberships, shouldBeInList)
			if addErr == nil {
				// All done.
				break
			}
			if xrpcErr, ok := errors.As[*xrpc.XRPCError](addErr); ok && xrpcErr.ErrStr == "InvalidSwap" {
				// Retry in a few seconds, hopefully with the updated rev & CID in membership response.
				log.Warn().Err(err).Msgf("Update failed due to concurrent writes (will retry): %s", err)
				time.Sleep(3 * time.Second)
				m, err := h.getListMemberships(ctx, did)
				if err != nil {
					return fmt.Errorf("failed to get list memberships: %w", err)
				}
				memberships = m.Results[owner]
				continue
			}
			return addErr
		}

		if addErr != nil {
			// Do one last try without swapCommit
			memberships.Cid = ""
			r, err = h.tryAddToLists(ctx, client, owner, did, &memberships, shouldBeInList)
			if err != nil {
				return err
			}
		}

		result.added = append(result.added, r.added...)
		result.removed = append(result.removed, r.removed...)
	}

	lines := []string{}
	if len(result.added) == 0 && len(result.removed) == 0 {
		lines = append(lines, "No changes made, list memberships already match the desired state.")
	}
	if len(result.added) > 0 {
		lines = append(lines, "", "Added to:", "")
		for _, s := range result.added {
			lines = append(lines, "* "+s)
		}
	}
	if len(result.removed) > 0 {
		lines = append(lines, "", "Removed from:", "")
		for _, s := range result.removed {
			lines = append(lines, "* "+s)
		}
	}

	redmineTicket, err := h.ticketsClient.Issue(ticket.Id)
	if err != nil {
		return fmt.Errorf("failed to fetch the current state of the ticket: %w", err)
	}

	_, err = tickets.Update(ctx, h.ticketsClient, redmineTicket, tickets.Status(tickets.StatusApplied))
	if err != nil {
		return err
	}

	_, err = tickets.AddNote(ctx, h.ticketsClient, redmineTicket, strings.Join(lines, "\n"))
	if err != nil {
		return err
	}
	return nil
}

type listUpdateResult struct {
	added   []string
	removed []string
}

func (h *handler) tryAddToLists(ctx context.Context, client *xrpc.Client, clientDid string, did string, memberships *listserver.ResponseFromSingleAccount, shouldBeInList map[string]bool) (listUpdateResult, error) {
	add := []string{}
	remove := map[string]string{}

	result := listUpdateResult{}

	for id, list := range h.config.Lists {
		u, err := aturl.Parse(list.URI)
		if err != nil {
			return result, fmt.Errorf("failed to parse %q: %w", list.URI, err)
		}
		if u.Host != clientDid {
			continue
		}

		isMemberOf := false
		for rkey, uri := range memberships.Listitems {
			if uri != list.URI {
				continue
			}
			isMemberOf = true
			if !shouldBeInList[id] {
				remove[rkey] = uri
			}
		}
		if !isMemberOf && shouldBeInList[id] {
			add = append(add, list.URI)
		}
	}

	// Fetch lists' display names
	title := map[string]string{}
	for _, uri := range append(add, maps.Values(remove)...) {
		info, err := bsky.GraphGetList(ctx, client, "", 1, uri)
		if err != nil {
			return result, fmt.Errorf("getting list info for %q: %w", uri, err)
		}
		if info.List == nil {
			continue
		}
		title[uri] = info.List.Name
	}
	listRkey := func(s string) string {
		parts := strings.Split(s, "/app.bsky.graph.list/")
		return parts[len(parts)-1]
	}

	var added []string

	if len(add) == 0 && len(remove) == 0 {
	} else {
		writes := &comatproto.RepoApplyWrites_Input{
			Repo: clientDid,
		}
		if memberships.Cid != "" {
			writes.SwapCommit = &memberships.Cid
		}

		if len(add) > 0 {
			for _, uri := range add {
				writes.Writes = append(writes.Writes, &comatproto.RepoApplyWrites_Input_Writes_Elem{
					RepoApplyWrites_Create: &comatproto.RepoApplyWrites_Create{
						// TODO: consider providing rkey here, containing list rkey and subject DID,
						// for easy lookups.
						Collection: "app.bsky.graph.listitem",
						Value: &lexutil.LexiconTypeDecoder{Val: &bsky.GraphListitem{
							List:      uri,
							Subject:   did,
							CreatedAt: time.Now().UTC().Format(time.RFC3339),
						}},
					},
				})
				added = append(added, uri)
				result.added = append(result.added, fmt.Sprintf("\"%s\" (`%s`)", title[uri], listRkey(uri)))
			}
		}

		if len(remove) > 0 {
			for rkey, uri := range remove {
				writes.Writes = append(writes.Writes, &comatproto.RepoApplyWrites_Input_Writes_Elem{
					RepoApplyWrites_Delete: &comatproto.RepoApplyWrites_Delete{
						Collection: "app.bsky.graph.listitem",
						Rkey:       rkey,
					},
				})
				result.removed = append(result.removed, fmt.Sprintf("\"%s\" (`%s`)", title[uri], listRkey(uri)))
			}
		}

		_, err := comatproto.RepoApplyWrites(ctx, client, writes)
		if err != nil {
			return result, fmt.Errorf("Failed to update lists: %w", err)
		}
	}

	return result, nil
}
