package api

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

// Background wrappers preserve the existing encoder reference tests.
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

func TestSnapshotEncodingChecksCancellationInsideRowsAndFallback(t *testing.T) {
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

func TestOwnedSnapshotEncodingPreservesPublicInput(t *testing.T) {
	for _, changing := range []bool{false, true} {
		for _, count := range []int{1, 130} {
			original := changingTopologySnapshots(changing)[:count]
			item := cmdb.SharedTopologyResponseData{Code: 200, PointCount: count, Snapshots: original, Message: "<safe>"}
			want, err := stdjson.Marshal(item)
			require.NoError(t, err)
			owned := item
			owned.Snapshots = append([]cmdb.SharedTopologySnapshot(nil), original...)
			parts, size, err := marshalOwnedTopologySnapshots(context.Background(), owned)
			require.NoError(t, err)
			require.Equal(t, len(want), size)
			require.Equal(t, want, bytes.Join(parts, nil))
			for _, frame := range owned.Snapshots {
				require.Nil(t, frame.Nodes)
				require.Nil(t, frame.Edges)
			}
			after, err := stdjson.Marshal(item)
			require.NoError(t, err)
			require.Equal(t, want, after)
			// Cached/owned bytes must remain immutable after public caller mutation.
			if len(original[0].Nodes) > 2 {
				original[0].Nodes[2].Dimensions["large"] = "caller changed"
			}
			require.Equal(t, want, bytes.Join(parts, nil))
		}
	}
}

func marshalLargeTopologySnapshotsContext(ctx context.Context, item cmdb.SharedTopologyResponseData) ([][]byte, int, error) {
	return marshalTopologySnapshotItem(ctx, item, false)
}

func marshalRepeatedTopologySnapshotsContext(ctx context.Context, snapshots []cmdb.SharedTopologySnapshot) ([][]byte, error) {
	return marshalTopologySnapshotFrames(ctx, snapshots, false)
}
