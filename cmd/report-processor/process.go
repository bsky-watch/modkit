package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"bsky.watch/redmine"
	"github.com/rs/zerolog"
	"github.com/valkey-io/valkey-go"

	"bsky.watch/utils/bskyurl"
	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/bluesky-social/indigo/xrpc"

	"bsky.watch/modkit/pkg/attachments"
	"bsky.watch/modkit/pkg/format"
	"bsky.watch/modkit/pkg/reportqueue"
	"bsky.watch/modkit/pkg/resolver"
	"bsky.watch/modkit/pkg/tickets"
)

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

func run(ctx context.Context, client *xrpc.Client, ticketsClient *redmine.Client, idCipher *reportqueue.IdCipher) error {
	log := zerolog.Ctx(ctx)

	remotes := append([]string{cfg.PersistentValkeyAddr}, cfg.RemoteReportQueueValkey...)

	ch := make(chan workItem)

	var wg sync.WaitGroup
	subCtx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		wg.Wait()
	}()
	for _, addr := range remotes {
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
			err := processReport(ctx, client, ticketsClient, idCipher, item.Payload)
			item.errCh <- err
			processingStats.WithLabelValues(item.Label, fmt.Sprint(err == nil)).Observe(time.Since(start).Seconds())
			reportsProcessed.WithLabelValues(item.Label, fmt.Sprint(err == nil)).Inc()
		case <-ctx.Done():
			log.Info().Msgf("Shutting down...")
			return ctx.Err()
		}
	}
}

func processReport(ctx context.Context, client *xrpc.Client, ticketsClient *redmine.Client, idCipher *reportqueue.IdCipher, report *reportqueue.QueueEntry) error {
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

	existing, err := tickets.FindByDID(ctx, ticketsClient, target.GetProfile())
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
		reportResp.Id = idCipher.Encrypt(n)
	}

	ticket := tickets.SelectDedupeTicket(ctx, existing)
	ticket, err = createOrUpdateTicket(ctx, client, ticketsClient, url, ticket, reportResp, target.GetProfile())
	if err != nil {
		return fmt.Errorf("failed to create or update ticket: %w", err)
	}
	// TODO: write report metadata into sqlite.

	log.Info().Msgf("Ticket ID: %d", ticket.Id)
	return nil
}

func createOrUpdateTicket(ctx context.Context, client *xrpc.Client, ticketsClient *redmine.Client, url bskyurl.Target, ticket *redmine.Issue, report *atproto.ModerationCreateReport_Output, subject string) (*redmine.Issue, error) {
	log := zerolog.Ctx(ctx)

	uploader := attachments.NewGlobalAttachmentCreator(ticketsClient)

	text := ""
	reasonTypeText := ""
	reasonText := ""

	if report.ReasonType != nil {
		switch *report.ReasonType {
		case "com.atproto.moderation.defs#reasonOther":
			// no-op
		case "com.atproto.moderation.defs#reasonSpam":
			reasonTypeText = "Spam"
		case "com.atproto.moderation.defs#reasonViolation":
			reasonTypeText = "ToS violation"
		case "com.atproto.moderation.defs#reasonMisleading":
			reasonTypeText = "Impersonation"
		case "com.atproto.moderation.defs#reasonSexual":
			reasonTypeText = "Sexual content"
		case "com.atproto.moderation.defs#reasonRude":
			reasonTypeText = "Antisocial behavior"
		case "com.atproto.moderation.defs#reasonAppeal":
			// TODO: auto-convert into appeal?
			reasonTypeText = "Appeal"
		default:
			reasonTypeText += fmt.Sprintf("`%s`", *report.ReasonType)
		}
	}

	userID := tickets.UserForDID(report.ReportedBy)
	if userID != "" {
		// TODO: check if the user actually exist and fallback to
		// default behaviour if it doesn't.
		ticketsClient = ticketsClient.Impersonate(userID)
		uploader = attachments.NewGlobalAttachmentCreator(ticketsClient)

		parts := []string{}
		if reasonTypeText != "" {
			parts = append(parts, reasonTypeText)
		}
		if report.Reason != nil && *report.Reason != "" {
			parts = append(parts, *report.Reason)
		}
		reasonText += strings.Join(parts, ": ") + "\n\n"
	} else {
		reporterDisplayName := report.ReportedBy
		reporter, err := bsky.ActorGetProfile(ctx, client, report.ReportedBy)
		if err != nil {
			log.Warn().Err(err).Msgf("Failed to fetch reporter profile %q: %s", report.ReportedBy, err)
		} else {
			reporterDisplayName = reporter.Handle
			if reporter.DisplayName != nil {
				reporterDisplayName = fmt.Sprintf("%s (%s)", *reporter.DisplayName, reporter.Handle)
			}
		}

		parts := []string{fmt.Sprintf("Reported by [%s](https://bsky.app/profile/%s)", reporterDisplayName, report.ReportedBy)}

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

		reasonText += strings.Join(parts, ":\n\n")
	}

	profile, err := bsky.ActorGetProfile(ctx, client, subject)
	if err != nil {
		return nil, err
	}
	if b, err := json.MarshalIndent(profile, "", "  "); err == nil {
		if _, err := uploader.Upload(ctx, fmt.Sprintf("profile_%s.json", time.Now().Format("20060102_030405")), b); err != nil {
			log.Warn().Err(err).Msgf("Failed to upload profile.json: %s", err)
		}
	}

	pdsClient := *client
	pds, _, err := resolver.GetPDSEndpointAndPublicKey(ctx, subject)
	if err != nil {
		return nil, fmt.Errorf("failed to get the PDS address: %w", err)
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
			return nil, fmt.Errorf("fetching post: %w", err)
		}
		post, ok := record.Value.Val.(*bsky.FeedPost)
		if !ok {
			return nil, fmt.Errorf("post if on unexpected type %T", record.Value.Val)
		}
		postText, err := format.Post(ctx, client, post, profile, t.Rkey, uploader)
		if err != nil {
			return nil, fmt.Errorf("formatting post: %w", err)
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
				return nil, fmt.Errorf("formatting profile: %w", err)
			}
			text += profileText
		}
	case *bskyurl.Profile:
		profileText, err := format.Profile(ctx, profile, uploader)
		if err != nil {
			return nil, fmt.Errorf("formatting profile: %w", err)
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

	b, _ := json.MarshalIndent(report, "", "  ")
	_, err = uploader.Upload(ctx, fmt.Sprintf("report_%s_%s.json", report.ReportedBy, time.Now().Format(time.DateOnly)), b)
	if err != nil {
		return nil, fmt.Errorf("uploading report.json: %w", err)
	}

	if ticket == nil {
		opts := []tickets.TicketOption{
			tickets.Subject(profile.Handle),
			tickets.DID(subject),
			tickets.Handle(profile.Handle),
			tickets.Description(text),
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

		ticket, err = tickets.Create(ctx, ticketsClient, opts...)
		if err != nil {
			return nil, err
		}

		if reasonText != "" {
			_, err = tickets.AddNote(ctx, ticketsClient, ticket, reasonText)
			if err != nil {
				return nil, err
			}
		}
	} else {
		if reasonText != "" {
			text = reasonText + "\n\n" + text
		}
		_, err = tickets.AddNote(ctx, ticketsClient, ticket, text)
		if err != nil {
			return nil, err
		}
		updates := []tickets.TicketOption{}

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
		if len(uploads) > 0 {
			updates = append(updates, tickets.Attachments(uploads))
		}

		if len(updates) > 0 {
			ticket, err = tickets.Update(ctx, ticketsClient, ticket, updates...)
		}
	}
	if err != nil {
		return nil, err
	}

	return ticket, nil
}
