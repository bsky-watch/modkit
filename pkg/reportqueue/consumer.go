package reportqueue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/valkey-io/valkey-go"

	"github.com/bluesky-social/indigo/api/atproto"
)

type QueueEntry struct {
	AckToken   string
	ID         string
	ReportedBy string
	Timestamp  string
	Report     atproto.ModerationCreateReport_Input
}

type ValkeyConsumer struct {
	client valkey.Client
	name   string
}

func NewValkeyConsumer(ctx context.Context, client valkey.Client, name string) (*ValkeyConsumer, error) {
	c := &ValkeyConsumer{
		client: client,
		name:   name,
	}
	return c, c.setup(ctx)
}

func (c *ValkeyConsumer) setup(ctx context.Context) error {
	result := c.client.Do(ctx, c.client.B().
		XgroupCreate().
		Key(valkeyStreamName).
		Group(c.name).
		Id("0").
		Mkstream().Build())

	if err := result.Error(); err != nil && !valkey.IsValkeyBusyGroup(err) {
		return fmt.Errorf("creating consumer group: %w", err)
	}
	return nil
}

func (c *ValkeyConsumer) GetNextReport(ctx context.Context) (QueueEntry, error) {
	log := zerolog.Ctx(ctx)

	var entry QueueEntry
	result, err := c.client.Do(ctx, c.client.B().
		Xreadgroup().
		Group(c.name, c.name).
		Count(1).
		Streams().Key(valkeyStreamName).Id("0").
		Build()).AsXRead()

	if err != nil && !valkey.IsValkeyNil(err) {
		return entry, err
	}

	for len(result) == 0 || len(result[valkeyStreamName]) == 0 {
		// No pending entries, fetch a new one.
		result, err = c.client.Do(ctx, c.client.B().
			Xreadgroup().
			Group(c.name, c.name).
			Count(1).
			Block(time.Hour.Milliseconds()).
			Streams().Key(valkeyStreamName).Id(">").
			Build()).AsXRead()
		if err != nil && !valkey.IsValkeyNil(err) {
			return entry, err
		}
	}

	for _, entries := range result {
		for _, e := range entries {
			entry.AckToken = e.ID
			for k, v := range e.FieldValues {
				switch k {
				case "id":
					entry.ID = v
				case "sender":
					entry.ReportedBy = v
				case "timestamp":
					entry.Timestamp = v
				case "report":
					if err := json.Unmarshal([]byte(v), &entry.Report); err != nil {
						return entry, fmt.Errorf("unmarshaling report: %w", err)
					}
				}
			}
			return entry, nil
		}
	}
	log.Error().Interface("result", result).Msgf("unreachable: no results in the response despite len > 0")
	return entry, fmt.Errorf("unreachable: no results in the response despite len > 0")
}

func (c *ValkeyConsumer) Ack(ctx context.Context, ackToken string) error {
	return c.client.Do(ctx, c.client.B().
		Xack().
		Key(valkeyStreamName).
		Group(c.name).
		Id(ackToken).
		Build()).Error()
}

func (c *ValkeyConsumer) AttemptCount(ctx context.Context, ackToken string) (int64, error) {
	resp, err := c.client.Do(ctx, c.client.B().Xpending().Key(valkeyStreamName).Group(c.name).Start(ackToken).End(ackToken).Count(1).Build()).ToArray()
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("XPENDING: %w", err)
	}

	if len(resp) == 0 {
		return 0, nil
	}

	info, err := resp[0].ToArray()
	if err != nil {
		return 0, fmt.Errorf("converting first response entry to array: %w", err)
	}
	if len(info) < 4 {
		return 0, fmt.Errorf("expected at least 4 items, got %d", len(info))
	}
	return info[3].ToInt64()
}

func (c *ValkeyConsumer) Quarantine(ctx context.Context, item QueueEntry) error {
	cmd := c.client.B().Xadd().Key(valkeyQuarantineStreamName).Id("*").
		FieldValue().
		FieldValue("queue_key", item.AckToken).
		FieldValue("id", item.ID).
		FieldValue("sender", item.ReportedBy).
		FieldValue("report", valkey.JSON(item.Report)).
		Build()

	if err := c.client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("writing the report to quarantine: %w", err)
	}

	return c.Ack(ctx, item.AckToken)
}

func (c *ValkeyConsumer) QuarantinedReports(ctx context.Context) ([]QueueEntry, error) {
	return nil, fmt.Errorf("not implemented")
}
