package api

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

// Exercise the production encoder.
func marshalLargeTopologySnapshots(item cmdb.SharedTopologyResponseData) ([][]byte, int, error) {
	return marshalLargeTopologySnapshotsContext(context.Background(), item)
}

func marshalRepeatedTopologySnapshots(snapshots []cmdb.SharedTopologySnapshot) ([][]byte, error) {
	return marshalRepeatedTopologySnapshotsContext(context.Background(), snapshots)
}

type snapshotCancelAfterChecks struct {
	context.Context
	remaining int
}

func (c *snapshotCancelAfterChecks) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestSnapshotEncodingChecksCancellationInsideRows(t *testing.T) {
	for _, changing := range []bool{false, true} {
		item := cmdb.SharedTopologyResponseData{Code: 200, Snapshots: changingTopologySnapshots(changing)}
		for _, after := range []int{1, 5, 20, 50, 200} {
			ctx := &snapshotCancelAfterChecks{Context: context.Background(), remaining: after}
			parts, size, err := marshalLargeTopologySnapshotsContext(ctx, item)
			if !errors.Is(err, context.Canceled) || parts != nil || size != 0 {
				t.Fatalf("changing=%v after=%d: size=%d parts=%d err=%v", changing, after, size, len(parts), err)
			}
		}
	}
}

func TestBoundedSnapshotEncodingPreservesPublicInputAndOwnsBytes(t *testing.T) {
	for _, changing := range []bool{false, true} {
		for _, count := range []int{1, 130} {
			original := changingTopologySnapshots(changing)[:count]
			item := cmdb.SharedTopologyResponseData{Code: 200, PointCount: count, Snapshots: original, Message: "<safe>"}
			want, err := stdjson.Marshal(item)
			require.NoError(t, err)
			parts, size, err := marshalLargeTopologySnapshots(item)
			require.NoError(t, err)
			require.Equal(t, len(want), size)
			require.Equal(t, want, bytes.Join(parts, nil))
			after, err := stdjson.Marshal(item)
			require.NoError(t, err)
			require.Equal(t, want, after)
			// Cached/owned bytes must remain immutable after public caller mutation.
			if len(original[0].Nodes) > 2 {
				original[0].Nodes[2].Dimensions["large"] = "caller changed"
			}
			_, _, err = marshalLargeTopologySnapshots(item)
			require.NoError(t, err)
			require.Equal(t, want, bytes.Join(parts, nil))
		}
	}
}

func marshalLargeTopologySnapshotsContext(ctx context.Context, item cmdb.SharedTopologyResponseData) ([][]byte, int, error) {
	return marshalTopologyItem(ctx, item)
}

func marshalRepeatedTopologySnapshotsContext(ctx context.Context, snapshots []cmdb.SharedTopologySnapshot) ([][]byte, error) {
	return marshalTopologySnapshotFrames(&topologyEncodeMeter{ctx: ctx}, snapshots)
}

type snapshotCancelContext struct {
	context.Context
	cancel     context.CancelFunc
	at, checks int
}

func (c *snapshotCancelContext) Err() error {
	c.checks++
	if c.at > 0 && c.checks == c.at {
		c.cancel()
	}
	return c.Context.Err()
}

func TestBoundedTopologyCancellationDiscardsEncodedParts(t *testing.T) {
	item := cmdb.SharedTopologyResponseData{Code: 200, Snapshots: changingTopologySnapshots(true)}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := &snapshotCancelContext{Context: parent, cancel: cancel}
	_, _, err := marshalLargeTopologySnapshotsContext(count, item)
	require.NoError(t, err)
	for _, at := range []int{1, count.checks / 2, count.checks} {
		t.Run(fmt.Sprint(at), func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &snapshotCancelContext{Context: parent, cancel: cancel, at: at}
			parts, size, err := marshalLargeTopologySnapshotsContext(ctx, item)
			require.ErrorIs(t, err, context.Canceled)
			require.Nil(t, parts)
			require.Zero(t, size)
		})
	}
}
