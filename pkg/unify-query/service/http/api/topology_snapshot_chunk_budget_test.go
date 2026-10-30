// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/stretchr/testify/require"
)

func TestSnapshotChunkFlushBoundaries(t *testing.T) {
	for _, tc := range []struct {
		size     int
		boundary uint64
		flushed  bool
	}{
		{0, 0, false},
		{topologyChunkMinBytes - 1, 0, false},
		{topologyChunkMinBytes, 0, true},
		{topologyChunkMinBytes, 1, false},
		{topologyChunkMinBytes, 127, false},
		{topologyChunkMinBytes, 128, true},
		{topologyChunkMinBytes, 129, false},
		{topologyChunkTargetBytes - 1, 0, true},
		{topologyChunkTargetBytes - 1, 1, false},
		{topologyChunkTargetBytes, 1, true},
		{topologyChunkTargetBytes + 1, 1, true},
	} {
		b := topologySnapshotChunkBuilder{byHash: make(map[uint64][][]byte)}
		payload := bytes.Repeat([]byte("x"), tc.size)
		require.NoError(t, b.append(payload, tc.boundary))
		if tc.flushed {
			require.Len(t, b.parts, 1, "size=%d boundary=%d", tc.size, tc.boundary)
			require.Equal(t, payload, b.parts[0])
			require.Empty(t, b.work)
		} else {
			require.Empty(t, b.parts, "size=%d boundary=%d", tc.size, tc.boundary)
			require.Equal(t, tc.size, len(b.work))
		}
		require.NoError(t, b.flush())
		require.Equal(t, tc.size, len(bytes.Join(b.parts, nil)))
		require.Equal(t, tc.size, b.size)
	}
}

func TestSnapshotChunkMeterKeepsWireAndReservations(t *testing.T) {
	for _, changing := range []bool{false, true} {
		frames := changingTopologySnapshots(changing)
		want, err := json.Marshal(frames)
		require.NoError(t, err)
		want = want[1 : len(want)-1]
		for _, wireLimit := range []int{len(want), len(want) - 1} {
			ctx, release, err := v1beta3.AcquireSharedTopology(context.Background())
			require.NoError(t, err)
			func() {
				defer release()
				meter := &topologyEncodeMeter{ctx: ctx, limit: wireLimit}
				parts, err := marshalTopologySnapshotFrames(context.WithValue(ctx, topologyEncodeMeterKey{}, meter), frames, false)
				if wireLimit == len(want) {
					require.NoError(t, err)
					require.Equal(t, want, bytes.Join(parts, nil))
					require.Equal(t, len(want), meter.wire)
					require.Positive(t, meter.owned)
					require.Positive(t, meter.working)
					require.GreaterOrEqual(t, topologyTestAdmissionState(t)["reserved"], meter.owned+meter.working+meter.cached+meter.scratch+meter.container)
				} else {
					require.ErrorContains(t, err, "max_topology_output_bytes")
					require.Nil(t, parts)
				}
			}()
			require.Zero(t, topologyTestAdmissionState(t)["reserved"])
		}
	}
}

func TestSnapshotChunkReservationFailurePrecedesAllocation(t *testing.T) {
	previous := v1beta3.MaxSharedTopologyReservedBytes
	v1beta3.MaxSharedTopologyReservedBytes = 1024 * 1024
	t.Cleanup(func() { v1beta3.MaxSharedTopologyReservedBytes = previous })
	for _, flushing := range []bool{false, true} {
		ctx, release, err := v1beta3.AcquireSharedTopology(context.Background())
		require.NoError(t, err)
		func() {
			defer release()
			b := topologySnapshotChunkBuilder{meter: &topologyEncodeMeter{ctx: ctx, limit: 1024}, byHash: make(map[uint64][][]byte)}
			if flushing {
				b.work = []byte("payload")
				err = b.flush()
				require.Equal(t, "payload", string(b.work))
			} else {
				err = b.append([]byte("payload"), 1)
				require.Zero(t, cap(b.work))
			}
			require.ErrorContains(t, err, "max_topology_reserved_bytes")
			require.Empty(t, b.parts)
			require.Zero(t, b.size)
			require.Zero(t, topologyTestAdmissionState(t)["reserved"])
		}()
	}
}
