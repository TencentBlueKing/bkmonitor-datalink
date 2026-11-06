// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func canonicalTopologyForTest(t *testing.T, snapshots []cmdb.SharedTopologySnapshot) []string {
	t.Helper()
	var result []string
	for _, snapshot := range snapshots {
		ids := make(map[uint64]string)
		var objects []string
		for _, node := range snapshot.Nodes {
			encoded, err := json.Marshal(struct {
				Resource cmdb.Resource
				Info     cmdb.Matcher
			}{node.ResourceType, node.Dimensions})
			require.NoError(t, err)
			ids[node.ID] = string(encoded)
			objects = append(objects, "node:"+string(encoded))
		}
		for _, edge := range snapshot.Edges {
			source, target := ids[edge.Source], ids[edge.Target]
			edge.Source, edge.Target = 0, 0
			encoded, err := json.Marshal(struct {
				Source, Target string
				Edge           cmdb.SharedTopologyEdge
			}{source, target, edge})
			require.NoError(t, err)
			objects = append(objects, "edge:"+string(encoded))
		}
		sort.Strings(objects)
		encoded, err := json.Marshal(struct {
			Timestamp int64
			Partial   bool
			Reason    string
			Objects   []string
		}{snapshot.Timestamp, snapshot.Partial, snapshot.PartialReason, objects})
		require.NoError(t, err)
		result = append(result, string(encoded))
	}
	return result
}

func TestIndependentAndCombinedTopologyOptimizations(t *testing.T) {
	oldReuse, oldPlan := SharedTopologyReuseMatrix, SharedTopologyPlanCandidates
	t.Cleanup(func() { SharedTopologyReuseMatrix = oldReuse; SharedTopologyPlanCandidates = oldPlan })
	responses := map[string]pl.Matrix{
		"source_info_relation": contractMatrix(map[string]string{"source_id": "a"}, 1700000000000, 1700000060000),
		"source_middle_flow":   contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000, 1700000060000),
		"middle_target_flow":   contractMatrix(map[string]string{"middle_id": "b", "target_id": "c"}, 1700000000000),
		"unused_flow":          contractMatrix(map[string]string{"x_id": "x", "y_id": "y"}, 1700000000000),
	}
	var baseline []string
	var calls []int
	for _, mode := range []struct{ reuse, plan bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		SharedTopologyReuseMatrix, SharedTopologyPlanCandidates = mode.reuse, mode.plan
		ctx := initTimeGraphQueryTestEnvironment()
		model := sharedTopologyQueryModel(responses)
		provider := sharedTopologyQueryProvider().(contractSchemaProvider)
		provider.schemas[0].IsDirectional = false
		provider.resources = append(provider.resources, "x", "y")
		provider.primary["x"], provider.primary["y"] = []string{"x_id"}, []string{"y_id"}
		provider.fields["x"], provider.fields["y"] = []string{"x_id"}, []string{"y_id"}
		provider.schemas = append(provider.schemas, RelationSchema{RelationType: "unused", Category: RelationCategoryStatic, FromType: "x", ToType: "y", IsDirectional: true, MetricName: "unused_flow"})
		model.schemaProvider = provider
		model.timeGraphQueryReference = func(ctx context.Context, q *structured.QueryTs) (metadata.QueryReference, error) {
			ref, err := timeGraphTestQueryReference(ctx, q)
			metadata.GetQueryParams(ctx).SetStorageType(metadata.VictoriaMetricsStorageType)
			return ref, err
		}
		count := 0
		fetch := model.timeGraphVMQuery
		model.timeGraphVMQuery = func(ctx context.Context, q *structured.QueryTs, expr string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
			count++
			return fetch(ctx, q, expr, instant, start, end, step)
		}
		request := cmdb.SharedTopologyQuery{SpaceUID: "space", StartTime: 1700000000, EndTime: 1700000060, Step: "60s", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, MaxHops: 2}
		result, err := model.QuerySharedTopology(ctx, request)
		require.NoError(t, err)
		calls = append(calls, count)
		canonical := canonicalTopologyForTest(t, result.Snapshots)
		if baseline == nil {
			baseline = canonical
		} else {
			require.Equal(t, baseline, canonical)
		}
		request.ResponseFormat = cmdb.CompactTopologyFormat
		compact, err := model.QuerySharedTopology(ctx, request)
		require.NoError(t, err)
		require.Equal(t, result.Snapshots, decodeCompactForTest(t, compact.Compact))
	}
	require.Equal(t, []int{5, 4, 4, 3}, calls)
}
