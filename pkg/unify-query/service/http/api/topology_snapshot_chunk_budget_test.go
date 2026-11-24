// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

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
		b := topologySnapshotChunkBuilder{meter: &topologyEncodeMeter{ctx: context.Background()}, byHash: make(map[uint64][][]byte)}
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

// 编码字节只计量不拒绝：分块输出与整体编码相同，计数等于实际字节数。
func TestSnapshotChunkMeterCountsWire(t *testing.T) {
	for _, changing := range []bool{false, true} {
		frames := changingTopologySnapshots(changing)
		want, err := json.Marshal(frames)
		require.NoError(t, err)
		want = want[1 : len(want)-1]
		meter := &topologyEncodeMeter{ctx: context.Background()}
		parts, err := marshalTopologySnapshotFrames(meter, frames)
		require.NoError(t, err)
		require.Equal(t, want, bytes.Join(parts, nil))
		require.Equal(t, len(want), meter.wire)
	}
}
