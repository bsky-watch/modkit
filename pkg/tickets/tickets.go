package tickets

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/mattn/go-redmine"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func Create(ctx context.Context, client *redmine.Client, opts ...TicketOption) (*redmine.Issue, error) {
	log := zerolog.Ctx(ctx)

	data := &ticketData{}
	data.ProjectId = mappings.ProjectID
	data.StatusId = mappings.Statuses.New

	for _, opt := range opts {
		if err := opt(data); err != nil {
			return nil, err
		}
	}

	related := []int{}
	if data.newDID != "" {
		result, err := FindByDID(ctx, client, data.newDID)
		if err != nil {
			return nil, fmt.Errorf("checking for existing tickets with the same DID: %w", err)
		}
		for _, t := range result {
			related = append(related, t.Id)
		}
	}

	createClient := client
	if data.author != "" {
		createClient = client.Impersonate(data.author)
	}
	ticket, err := createClient.CreateIssue(data.Issue)
	if err != nil {
		return nil, err
	}

	for _, relatedId := range related {
		_, err := client.CreateIssueRelation(redmine.IssueRelation{
			IssueId:      fmt.Sprint(ticket.Id),
			IssueToId:    fmt.Sprint(relatedId),
			RelationType: "related to",
		})
		if err != nil {
			log.Warn().Err(err).Msgf("Failed to create relationship from %d to %d: %s", ticket.Id, relatedId, err)
		}
	}

	return ticket, nil
}

func Update(ctx context.Context, client *redmine.Client, ticket *redmine.Issue, opts ...TicketOption) (*redmine.Issue, error) {
	update := &ticketData{
		Issue:     *ticket,
		preUpdate: ticket,
	}

	for _, opt := range opts {
		if err := opt(update); err != nil {
			return nil, err
		}
	}

	related := []int{}
	if update.newDID != "" {
		result, err := FindByDID(ctx, client, update.newDID)
		if err != nil {
			return nil, fmt.Errorf("checking for existing tickets with the same DID: %w", err)
		}
		for _, t := range result {
			related = append(related, t.Id)
		}
	}

	err := client.UpdateIssue(update.Issue)
	if err != nil {
		return nil, fmt.Errorf("UpdateIssue: %w", err)
	}

	ticket, err = client.Issue(update.Issue.Id)
	if err != nil {
		return nil, fmt.Errorf("Issue(%d): %w", update.Issue.Id, err)
	}

	rels, err := client.IssueRelations(ticket.Id)
	if err != nil {
		return nil, fmt.Errorf("IssueRelations(%d): %w", ticket.Id, err)
	}
	skipRels := map[string]bool{fmt.Sprint(ticket.Id): true}
	for _, rel := range rels {
		skipRels[rel.IssueToId] = true
	}

	for _, relatedId := range related {
		if relatedId == ticket.Id || skipRels[fmt.Sprint(relatedId)] {
			continue
		}

		_, err := client.CreateIssueRelation(redmine.IssueRelation{
			IssueId:      fmt.Sprint(ticket.Id),
			IssueToId:    fmt.Sprint(relatedId),
			RelationType: "related to",
		})
		if err != nil {
			log.Warn().Err(err).Msgf("Failed to create relationship from %d to %d: %s", ticket.Id, relatedId, err)
		}
	}

	return ticket, nil
}

func AddNote(ctx context.Context, client *redmine.Client, ticket *redmine.Issue, note string) (*redmine.Journal, error) {
	copied := *ticket
	copied.Notes = note
	return nil, client.UpdateIssue(copied)
}

func FindByDID(ctx context.Context, client *redmine.Client, did string) ([]redmine.Issue, error) {
	didField := fmt.Sprintf("cf_%d", mappings.Fields.DID)

	return client.IssuesByFilter(&redmine.IssueFilter{
		ProjectId: fmt.Sprint(mappings.ProjectID),
		ExtraFilters: map[string]string{
			"f[]":                            didField,
			fmt.Sprintf("op[%s]", didField):  "=",
			fmt.Sprintf("v[%s][]", didField): did,
		},
	})
}

func SelectDedupeTicket(ctx context.Context, tickets []redmine.Issue) *redmine.Issue {
	dedupeCandidates := []redmine.Issue{}

	for _, t := range tickets {
		if t.Tracker != nil && t.Tracker.Id != mappings.TicketTypes.Ticket {
			continue
		}
		if t.Status != nil && t.Status.Id == mappings.Statuses.Duplicate {
			continue
		}
		dedupeCandidates = append(dedupeCandidates, t)
	}

	if len(dedupeCandidates) == 0 {
		return nil
	}

	slices.SortFunc(dedupeCandidates, func(a, b redmine.Issue) int {
		aPriority, _ := GetPriority(&a)
		bPriority, _ := GetPriority(&b)

		r := cmp.Compare(aPriority, bPriority)
		if r != 0 {
			return r
		}

		r = cmp.Compare(a.DoneRatio, b.DoneRatio)
		if r != 0 {
			return r
		}
		return cmp.Compare(a.CreatedOn, b.CreatedOn)
	})

	r := dedupeCandidates[0]
	return &r
}
