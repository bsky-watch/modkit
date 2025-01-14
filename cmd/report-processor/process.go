package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"bsky.watch/redmine"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/valkey-io/valkey-go"

	"bsky.watch/utils/bskyurl"
	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/bluesky-social/indigo/lex/util"
	"github.com/bluesky-social/indigo/xrpc"

	"bsky.watch/modkit/pkg/attachments"
	"bsky.watch/modkit/pkg/format"
	"bsky.watch/modkit/pkg/reportqueue"
	"bsky.watch/modkit/pkg/resolver"
	"bsky.watch/modkit/pkg/tickets"
)

type handler struct {
	client        *xrpc.Client
	ticketsClient *redmine.Client
	idCipher      *reportqueue.IdCipher
	valkeyRemotes []string
}

func NewHandler(ctx context.Context, client *xrpc.Client, ticketsClient *redmine.Client, cfg *Config) (*handler, error) {
	idCipher, err := reportqueue.NewIdCipher(cfg.TicketIDEncryptionKey)
	if err != nil {
		return nil, err
	}

	return &handler{
		client:        client,
		ticketsClient: ticketsClient,
		idCipher:      idCipher,
		valkeyRemotes: append([]string{cfg.PersistentValkeyAddr}, cfg.RemoteReportQueueValkey...),
	}, nil
}

type workItem struct {
	Payload *reportqueue.QueueEntry
	Remote  *reportqueue.ValkeyConsumer
	Label   string

	errCh chan error
}

func pullReports(ctx context.Context, client *reportqueue.ValkeyConsumer, label string, out chan<- workItem) {
	log := zerolog.Ctx(ctx)

	for {
		r, err := client.GetNextReport(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error().Err(err).Msgf("Failed to fetch the next report in the queue: %s", err)
			continue
		}

		item := workItem{
			Payload: &r,
			Remote:  client,
			Label:   label,
			errCh:   make(chan error),
		}

		select {
		case <-ctx.Done():
			return
		case out <- item:
		}

		select {
		case <-ctx.Done():
			return
		case err, ok := <-item.errCh:
			if !ok || err == nil {
				// Channel was closed or received error is nil.
				if err := client.Ack(ctx, item.Payload.AckToken); err != nil {
					log.Error().Err(err).Msgf("Failed to ack report %q (token=%s): %s", item.Payload.ID, item.Payload.AckToken, err)
				}
				break
			}
			log.Error().Err(err).Msgf("Failed to process report %q (token=%s): %s", item.Payload.ID, item.Payload.AckToken, err)

			n, err := client.AttemptCount(ctx, item.Payload.AckToken)
			if err != nil {
				log.Error().Err(err).Msgf("Failed to get attempt count for report %q (token=%s): %s", item.Payload.ID, item.Payload.AckToken, err)
			} else {
				if n > 15 {
					err := client.Quarantine(ctx, *item.Payload)
					if err != nil {
						log.Error().Err(err).Msgf("Failed to move report %q (token=%s) to quarantine: %s", item.Payload.ID, item.Payload.AckToken, err)
					} else {
						break
					}
				}
			}

			time.Sleep(5 * time.Second)
		}
	}
}

func (h *handler) Run(ctx context.Context) error {
	log := zerolog.Ctx(ctx)

	ch := make(chan workItem)

	var wg sync.WaitGroup
	subCtx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		wg.Wait()
	}()
	for _, addr := range h.valkeyRemotes {
		c, err := valkey.NewClient(valkey.ClientOption{
			InitAddress: []string{addr},
		})
		if err != nil {
			return fmt.Errorf("creating valkey client for %q: %w", addr, err)
		}

		client, err := reportqueue.NewValkeyConsumer(ctx, c, "report-processor")
		if err != nil {
			return fmt.Errorf("creating queue consumer for %q: %w", addr, err)
		}

		wg.Add(1)
		go func(ctx context.Context, client *reportqueue.ValkeyConsumer) {
			pullReports(ctx, client, addr, ch)
			wg.Done()
		}(log.With().Str("remote", addr).Logger().WithContext(subCtx), client)
	}

	for {
		select {
		case item := <-ch:
			start := time.Now()
			err := h.processReport(ctx, item.Payload)
			item.errCh <- err
			processingStats.WithLabelValues(item.Label, fmt.Sprint(err == nil)).Observe(time.Since(start).Seconds())
			reportsProcessed.WithLabelValues(item.Label, fmt.Sprint(err == nil)).Inc()
		case <-ctx.Done():
			log.Info().Msgf("Shutting down...")
			return ctx.Err()
		}
	}
}

func (h *handler) processReport(ctx context.Context, report *reportqueue.QueueEntry) error {
	log := zerolog.Ctx(ctx).With().Str("sender", report.ReportedBy).
		Str("report_id", report.ID).Logger()

	subject := ""
	switch {
	case report.Report.Subject.RepoStrongRef != nil:
		subject = report.Report.Subject.RepoStrongRef.Uri
	case report.Report.Subject.AdminDefs_RepoRef != nil:
		subject = report.Report.Subject.AdminDefs_RepoRef.Did
	}
	if subject == "" {
		return fmt.Errorf("missing subject")
	}

	log.Info().Str("subject", subject).Msgf("Received report of %q from %q", subject, report.ReportedBy)

	url, err := bskyurl.DetermineTarget(subject)
	if err != nil {
		return fmt.Errorf("failed to parse subject: %w", err)
	}
	target, ok := url.(bskyurl.TargetWithProfile)
	if !ok {
		return fmt.Errorf("unsupported URI %q", subject)
	}

	profile, err := bsky.ActorGetProfile(ctx, h.client, target.GetProfile())
	if err != nil {
		return err
	}

	existing, err := tickets.FindByDID(ctx, h.ticketsClient, target.GetProfile())
	if err != nil {
		return fmt.Errorf("failed to query for existing tickets for %q: %w", target.GetProfile(), err)
	}

	reportResp := &atproto.ModerationCreateReport_Output{
		Reason:     report.Report.Reason,
		ReasonType: report.Report.ReasonType,
		Subject: &atproto.ModerationCreateReport_Output_Subject{
			AdminDefs_RepoRef: report.Report.Subject.AdminDefs_RepoRef,
			RepoStrongRef:     report.Report.Subject.RepoStrongRef,
		},
		ReportedBy: report.ReportedBy,
		CreatedAt:  report.Timestamp,
	}

	if n, err := strconv.ParseUint(report.ID, 10, 64); err != nil {
		log.Warn().Err(err).Msgf("Failed to parse %q as uint64: %s", report.ID, err)
	} else {
		reportResp.Id = h.idCipher.Encrypt(n)
	}

	ticket := tickets.SelectDedupeTicket(ctx, existing)
	if ticket == nil {
		ticket, err = h.createAccountTicket(ctx, target.GetProfile(), report.ReportedBy, profile)
		if err != nil {
			return fmt.Errorf("failed to create ticket: %w", err)
		}
	}

	if target, ok := target.(bskyurl.TargetRecord); ok && cfg.EnablePerRecordTickets {
		// One ticket for each unique subject.
		uri, err := makeNormalizedURI(ctx, h.client, target)
		if err != nil {
			return fmt.Errorf("failed to generate normalized URI: %w", err)
		}

		pdsClient := *h.client
		pds, _, err := resolver.GetPDSEndpointAndPublicKey(ctx, target.GetProfile())
		if err != nil {
			return fmt.Errorf("failed to get the PDS address: %w", err)
		}
		pdsClient.Host = pds.String()

		// Doesn't work with atproto.brid.gy: generated code always sends empty cid, which confuses it.
		// record, err := atproto.RepoGetRecord(ctx, &pdsClient, "", "app.bsky.feed.post", t.Profile, t.Rkey)

		var record atproto.RepoGetRecord_Output
		params := map[string]interface{}{
			"collection": target.GetCollection(),
			"repo":       target.GetProfile(),
			"rkey":       target.GetRKey(),
		}
		err = pdsClient.Do(ctx, xrpc.Query, "", "com.atproto.repo.getRecord", params, nil, &record)
		if err != nil {
			return fmt.Errorf("fetching post: %w", err)
		}

		existing, err := tickets.FindBySubject(ctx, h.ticketsClient, uri)
		if err != nil {
			return fmt.Errorf("failed to query for existing tickets for %q: %w", uri, err)
		}

		recordTicket := tickets.SelectDedupeTicket(ctx, existing)
		if recordTicket == nil {
			recordTicket, err = h.createRecordTicket(ctx, uri, target.GetRKey(), record.Value, target.GetProfile(), report.ReportedBy, profile, ticket)
			if err != nil {
				return fmt.Errorf("failed to create ticket: %w", err)
			}
		}

		err = h.postReport(ctx, recordTicket, reportResp, record.Value)
		if err != nil {
			return err
		}

		log.Info().Msgf("Ticket ID: %d, Account ticket ID: %d", recordTicket.Id, ticket.Id)
	} else {
		// One ticket per account (reports for all records go into the same ticket).
		err = h.postReportOnAccountTicket(ctx, ticket, reportResp, target, profile)
		if err != nil {
			return fmt.Errorf("failed to update ticket: %w", err)
		}
		log.Info().Msgf("Ticket ID: %d", ticket.Id)
	}
	// TODO: write report metadata into sqlite.
	return nil
}

func (h *handler) getTicketsClient(userDID string) *redmine.Client {
	userID := tickets.UserForDID(userDID)
	if userID != "" {
		// TODO: check if the user actually exist and fallback to
		// default behaviour if it doesn't.
		r := *h.ticketsClient
		return r.Impersonate(userID)
	}
	return h.ticketsClient
}

func (h *handler) createAccountTicket(ctx context.Context, did string, reportedBy string, profile *bsky.ActorDefs_ProfileViewDetailed) (*redmine.Issue, error) {
	userID := tickets.UserForDID(reportedBy)
	ticketsClient := h.getTicketsClient(reportedBy)
	uploader := attachments.NewGlobalAttachmentCreator(ticketsClient)

	profileText, err := format.Profile(ctx, profile, uploader)
	if err != nil {
		return nil, fmt.Errorf("formatting profile: %w", err)
	}

	opts := []tickets.TicketOption{
		tickets.Subject(profile.Handle),
		tickets.DID(did),
		tickets.Handle(profile.Handle),
		tickets.Description(profileText),
		tickets.Attachments(uploader.Created()),
		tickets.Type(tickets.TypeTicket),
	}
	if profile.DisplayName != nil {
		opts = append(opts, tickets.DisplayName(*profile.DisplayName))
	}
	if userID != "" {
		opts = append(opts,
			tickets.Priority(tickets.PriorityNormal),
			// tickets.CreationTrigger(tickets.TriggerManual),
		)
	} else {
		opts = append(opts,
			tickets.Priority(tickets.PriorityUrgent),
			// tickets.CreationTrigger(tickets.TriggerEscalation),
		)
	}

	return tickets.Create(ctx, ticketsClient, opts...)
}

func (h *handler) createRecordTicket(ctx context.Context, uri string, rkey string, record *util.LexiconTypeDecoder, did string, reportedBy string, profile *bsky.ActorDefs_ProfileViewDetailed, accountTicket *redmine.Issue) (*redmine.Issue, error) {
	userID := tickets.UserForDID(reportedBy)
	ticketsClient := h.getTicketsClient(reportedBy)
	uploader := attachments.NewGlobalAttachmentCreator(ticketsClient)

	text := ""
	switch record := record.Val.(type) {
	case *bsky.FeedPost:
		postText, err := format.Post(ctx, h.client, record, profile, rkey, uploader)
		if err != nil {
			return nil, fmt.Errorf("formatting post: %w", err)
		}
		text += postText

		if b, err := json.MarshalIndent(record, "", "  "); err == nil {
			if _, err := uploader.Upload(ctx, fmt.Sprintf("post_%s.json", rkey), b); err != nil {
				log.Warn().Err(err).Msgf("Failed to upload post.json: %s", err)
			}
		}
	default:
		b, err := json.MarshalIndent(record, "", "  ")
		if err == nil {
			if _, err := uploader.Upload(ctx, fmt.Sprintf("record_%s.json", rkey), b); err != nil {
				log.Warn().Err(err).Msgf("Failed to upload record.json: %s", err)
			}

			text += fmt.Sprintf("```json\n%s\n```", string(b))
		}
	}

	opts := []tickets.TicketOption{
		tickets.Subject(fmt.Sprintf("%s %s", profile.Handle, rkey)),
		tickets.DID(did),
		tickets.Handle(profile.Handle),
		tickets.Description(text),
		tickets.Attachments(uploader.Created()),
		tickets.Type(tickets.TypeRecordTicket),
		tickets.ReportSubject(uri),
	}
	if profile.DisplayName != nil {
		opts = append(opts, tickets.DisplayName(*profile.DisplayName))
	}
	if userID != "" {
		opts = append(opts,
			tickets.Priority(tickets.PriorityNormal),
		)
	} else {
		opts = append(opts,
			tickets.Priority(tickets.PriorityUrgent),
		)
	}

	ticket, err := tickets.Create(ctx, ticketsClient, opts...)
	if err != nil {
		return nil, err
	}

	_, err = ticketsClient.CreateIssueRelation(redmine.IssueRelation{
		IssueId:      ticket.Id,
		IssueToId:    accountTicket.Id,
		RelationType: "relates",
	})
	if err != nil {
		return nil, err
	}
	return ticket, nil
}

var reasonTypes = map[string]string{
	"com.atproto.moderation.defs#reasonOther":      "",
	"com.atproto.moderation.defs#reasonSpam":       "Spam",
	"com.atproto.moderation.defs#reasonViolation":  "ToS violation",
	"com.atproto.moderation.defs#reasonMisleading": "Impersonation",
	"com.atproto.moderation.defs#reasonSexual":     "Sexual content",
	"com.atproto.moderation.defs#reasonRude":       "Antisocial behavior",
	"com.atproto.moderation.defs#reasonAppeal":     "Appeal",
}

func (h *handler) reasonTypeText(report *atproto.ModerationCreateReport_Output) string {
	if report.ReasonType == nil {
		return ""
	}
	if s, found := reasonTypes[*report.ReasonType]; found {
		return s
	}
	return fmt.Sprintf("`%s`", *report.ReasonType)
}

func (h *handler) formatReasonTextAsModerator(report *atproto.ModerationCreateReport_Output) string {
	parts := []string{}
	reasonTypeText := h.reasonTypeText(report)
	if reasonTypeText != "" {
		parts = append(parts, reasonTypeText)
	}
	if report.Reason != nil && *report.Reason != "" {
		parts = append(parts, *report.Reason)
	}
	return strings.Join(parts, ": ")
}

func (h *handler) formatReasonTextAsSubscriber(ctx context.Context, report *atproto.ModerationCreateReport_Output) string {
	reporterDisplayName := report.ReportedBy
	reporter, err := bsky.ActorGetProfile(ctx, h.client, report.ReportedBy)
	if err != nil {
		log.Warn().Err(err).Msgf("Failed to fetch reporter profile %q: %s", report.ReportedBy, err)
	} else {
		reporterDisplayName = reporter.Handle
		if reporter.DisplayName != nil {
			reporterDisplayName = fmt.Sprintf("%s (%s)", *reporter.DisplayName, reporter.Handle)
		}
	}

	parts := []string{fmt.Sprintf("Reported by [%s](https://bsky.app/profile/%s)", reporterDisplayName, report.ReportedBy)}

	reasonTypeText := h.reasonTypeText(report)
	if reasonTypeText != "" {
		parts = append(parts, reasonTypeText)
	}

	if report.Reason != nil && *report.Reason != "" {
		s := ""
		for _, line := range strings.Split(*report.Reason, "\n") {
			s += "> " + line + "\n"
		}
		parts = append(parts, s)

	}

	return strings.Join(parts, ":\n\n")
}

func (h *handler) postReportOnAccountTicket(ctx context.Context, ticket *redmine.Issue, report *atproto.ModerationCreateReport_Output, url bskyurl.TargetWithProfile, profile *bsky.ActorDefs_ProfileViewDetailed) error {
	log := zerolog.Ctx(ctx)

	userID := tickets.UserForDID(report.ReportedBy)
	ticketsClient := h.getTicketsClient(report.ReportedBy)
	uploader := attachments.NewGlobalAttachmentCreator(ticketsClient)

	text := ""
	reasonText := ""

	if userID != "" {
		reasonText = h.formatReasonTextAsModerator(report)
	} else {
		reasonText = h.formatReasonTextAsSubscriber(ctx, report)
	}

	pdsClient := *h.client
	pds, _, err := resolver.GetPDSEndpointAndPublicKey(ctx, url.GetProfile())
	if err != nil {
		return fmt.Errorf("failed to get the PDS address: %w", err)
	}
	pdsClient.Host = pds.String()

	switch t := url.(type) {
	case *bskyurl.Post:
		// Doesn't work with atproto.brid.gy: generated code always sends empty cid, which confuses it.
		// record, err := atproto.RepoGetRecord(ctx, &pdsClient, "", "app.bsky.feed.post", t.Profile, t.Rkey)

		var record atproto.RepoGetRecord_Output
		params := map[string]interface{}{
			"collection": "app.bsky.feed.post",
			"repo":       t.Profile,
			"rkey":       t.Rkey,
		}
		err := pdsClient.Do(ctx, xrpc.Query, "", "com.atproto.repo.getRecord", params, nil, &record)
		if err != nil {
			return fmt.Errorf("fetching post: %w", err)
		}
		post, ok := record.Value.Val.(*bsky.FeedPost)
		if !ok {
			return fmt.Errorf("post if on unexpected type %T", record.Value.Val)
		}
		postText, err := format.Post(ctx, h.client, post, profile, t.Rkey, uploader)
		if err != nil {
			return fmt.Errorf("formatting post: %w", err)
		}
		text += postText + "\n\n"

		if b, err := json.MarshalIndent(post, "", "  "); err == nil {
			if _, err := uploader.Upload(ctx, fmt.Sprintf("post_%s.json", t.Rkey), b); err != nil {
				log.Warn().Err(err).Msgf("Failed to upload post.json: %s", err)
			}
		}

		if ticket == nil {
			profileText, err := format.Profile(ctx, profile, uploader)
			if err != nil {
				return fmt.Errorf("formatting profile: %w", err)
			}
			text += profileText
		}
	case *bskyurl.Profile:
		profileText, err := format.Profile(ctx, profile, uploader)
		if err != nil {
			return fmt.Errorf("formatting profile: %w", err)
		}
		text += profileText
	default:
		reportSubject := "failed to determine report subject"
		switch {
		case report.Subject.RepoStrongRef != nil:
			reportSubject = report.Subject.RepoStrongRef.Uri
		case report.Subject.AdminDefs_RepoRef != nil:
			reportSubject = report.Subject.AdminDefs_RepoRef.Did
		}
		text += fmt.Sprintf("\n`%s`\n", reportSubject)
	}

	if b, err := json.MarshalIndent(profile, "", "  "); err == nil {
		if _, err := uploader.Upload(ctx, fmt.Sprintf("profile_%s.json", time.Now().Format("20060102_030405")), b); err != nil {
			log.Warn().Err(err).Msgf("Failed to upload profile.json: %s", err)
		}
	}

	if reasonText != "" {
		text = reasonText + "\n\n" + text
	}
	updates := []tickets.TicketOption{tickets.WithNote(text)}

	// progress := 0
	// if ticket.PercentageDone != nil {
	// 	progress = *ticket.PercentageDone
	// }
	// if progress >= 90 {
	// 	updates = append(updates, tickets.Status(tickets.StatusInProgress))
	// }

	if userID == "" {
		prio, ok := tickets.GetPriority(ticket)
		if !ok || prio < tickets.PriorityHigh {
			updates = append(updates, tickets.Priority(tickets.PriorityHigh))
		}
	}

	uploads := uploader.Created()
	updates = append(updates, tickets.Attachments(uploads))

	if len(updates) > 0 {
		ticket, err = tickets.Update(ctx, ticketsClient, ticket, updates...)
		if err != nil {
			return err
		}
	}
	return nil
}

func (h *handler) postReport(ctx context.Context, ticket *redmine.Issue, report *atproto.ModerationCreateReport_Output, record *util.LexiconTypeDecoder) error {
	userID := tickets.UserForDID(report.ReportedBy)
	ticketsClient := h.getTicketsClient(report.ReportedBy)
	uploader := attachments.NewGlobalAttachmentCreator(ticketsClient)

	text := ""

	if userID != "" {
		text = h.formatReasonTextAsModerator(report)
	} else {
		text = h.formatReasonTextAsSubscriber(ctx, report)
	}

	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling report: %w", err)
	}
	if _, err := uploader.Upload(ctx, fmt.Sprintf("report_%s_%s.json", report.ReportedBy, time.Now().Format(time.DateOnly)), b); err != nil {
		return fmt.Errorf("uploading report: %w", err)
	}

	ticket, err = tickets.Update(ctx, ticketsClient, ticket,
		tickets.WithNote(text),
		tickets.Attachments(uploader.Created()),
	)
	if err != nil {
		return err
	}
	return nil
}

func makeNormalizedURI(ctx context.Context, client *xrpc.Client, target bskyurl.TargetRecord) (string, error) {
	profile := target.GetProfile()
	if !strings.HasPrefix(profile, "did:") {
		resp, err := atproto.IdentityResolveHandle(ctx, client, profile)
		if err != nil {
			return "", err
		}
		profile = resp.Did
	}

	var u url.URL
	u.Scheme = "at"
	u.Host = profile
	u.Path = path.Join("/", target.GetCollection(), target.GetRKey())

	return u.String(), nil
}
