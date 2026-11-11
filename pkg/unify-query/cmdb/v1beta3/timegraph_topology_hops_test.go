// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package v1beta3

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestSharedTopologySixHopsAtSixtyPoints(t *testing.T) {
	require.Equal(t, 2, DefaultMaxHops)
	require.Equal(t, 6, MaxAllowedHops)
	timestamps := sharedTopologyTestTimes(60)
	model := &Model{schemaProvider: longPathTestProvider(), timeGraphQueryReference: timeGraphTestQueryReference}
	var calls atomic.Int32
	model.timeGraphVMQuery = func(ctx context.Context, _ *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		calls.Add(1)
		_, deadline := ctx.Deadline()
		require.True(t, deadline)
		require.Positive(t, metadata.BackendResponseLimit(ctx))
		return contractMatrix(map[string]string{"id": "root"}, timestamps...), nil
	}
	request := cmdb.SharedTopologyQuery{
		SpaceUID: "space", SourceType: "r0", SourceInfo: cmdb.Matcher{"id": "root"},
		StartTime: timestamps[0], EndTime: timestamps[59], Step: "1s", MaxHops: 6,
	}
	result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.NoError(t, err)
	require.Positive(t, calls.Load())
	require.Len(t, result.Snapshots, 60)
	for _, frame := range result.Snapshots {
		require.False(t, frame.Partial)
		require.Len(t, frame.Nodes, 7)
		require.Len(t, frame.Edges, 12)
		require.True(t, hasSharedTopologyNode(frame, "r6", "id", "root"))
		require.False(t, hasSharedTopologyNode(frame, "r7", "id", "root"))
	}

	calls.Store(0)
	request.MaxHops = 7
	result, err = model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.ErrorContains(t, err, "max hops 7 is outside [0, 6]")
	require.Empty(t, result)
	require.Zero(t, calls.Load(), "invalid hop limits must fail before querying the backend")
}
