package reportqueue

import (
	"context"
	"fmt"
	"time"

	"github.com/valkey-io/valkey-go"
)

type ValkeyWriter struct {
	client valkey.Client
	nodeId uint
}

// NewValkeyWriter creates a new report writer that persists data in valkey.
// nodeId must be different between different valkey instances (so that when
// reports are collected in a central location there would be no collisions).
func NewValkeyWriter(ctx context.Context, client valkey.Client, nodeId uint) (*ValkeyWriter, error) {
	if nodeId > valkeyMaxNodeId {
		return nil, fmt.Errorf("provided node ID is out of range: %d > %d", nodeId, valkeyMaxNodeId)
	}

	return &ValkeyWriter{
		client: client,
		nodeId: nodeId,
	}, nil
}

func (w *ValkeyWriter) AddReport(ctx context.Context, sender string, timestamp string, payload []byte) (uint64, error) {
	id, err := w.allocateReportId(ctx)
	if err != nil {
		return 0, err
	}

	cmd := w.client.B().Xadd().Key(valkeyStreamName).Id("*").
		FieldValue().
		FieldValue("id", fmt.Sprint(id)).
		FieldValue("sender", sender).
		FieldValue("report", string(payload)).
		FieldValue("timestamp", timestamp).
		Build()

	result := w.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		return 0, fmt.Errorf("writing the report: %w", err)
	}

	return id, nil
}

func (w *ValkeyWriter) allocateReportId(ctx context.Context) (uint64, error) {
	ts := uint64(time.Now().Unix())
	for i := 0; i < 50; i++ {
		n := ts & (1<<valkeyReportIdLocalBits - 1)
		n = n<<valkeyReportIdNodeIdBits + uint64(w.nodeId)
		cmd := w.client.B().Set().Key(fmt.Sprintf("report:%d", n)).Value(fmt.Sprint(time.Now().Unix())).Nx().Build()

		result := w.client.Do(ctx, cmd)
		if result.Error() == nil {
			return n, nil
		}

		if ctx.Err() != nil {
			return 0, ctx.Err()
		}

		// try again
		ts++
	}
	return 0, fmt.Errorf("failed to generate report ID")
}
