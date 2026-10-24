// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func frontierCompositeProvider() contractSchemaProvider {
	return contractSchemaProvider{
		resources: []ResourceType{"service"},
		primary:   map[ResourceType][]string{"service": {"tenant", "id"}},
		fields:    map[ResourceType][]string{"service": {"tenant", "id", "version"}},
		schemas:   []RelationSchema{{RelationType: "calls", Category: RelationCategoryDynamic, FromType: "service", ToType: "service", IsDirectional: true, MetricName: "calls_flow"}},
	}
}

func TestTopologySelectorDynamicFallbackMatchesFullScanWithCompositeKeysAndChanges(t *testing.T) {
	old := SharedTopologyPlanCandidates
	t.Cleanup(func() { SharedTopologyPlanCandidates = old })
	const stamp int64 = 1700000000000
	provider := frontierCompositeProvider()
	matrix := pl.Matrix{}
	add := func(ft, fi, tt, ti string, times ...int64) {
		matrix = append(matrix, contractMatrix(map[string]string{"from_tenant": ft, "from_id": fi, "to_tenant": tt, "to_id": ti}, times...)...)
	}
	// Boundary cross-edge, cycle, self-loop, and a cross-frame path that must
	// never become reachable merely because the candidate union includes it.
	add("x", "a", "x", "b", stamp, stamp+60000)
	add("x", "a", "y", "c", stamp, stamp+60000)
	add("x", "b", "y", "c", stamp)
	add("y", "c", "x", "a", stamp, stamp+120000)
	add("x", "b", "x", "b", stamp+60000)
	add("x", "b", "y", "d", stamp+120000)
	add("y", "c", "y", "e", stamp+60000)
	// Composite cross-combinations must not escape tuple selectors.
	add("y", "b", "z", "bad", stamp, stamp+60000, stamp+120000)
	add("x", "c", "z", "bad", stamp, stamp+60000, stamp+120000)
	for i := 0; i < 2000; i++ {
		add("isolated", fmt.Sprint(i), "isolated", fmt.Sprint(i+1), stamp, stamp+60000, stamp+120000)
	}
	for _, direction := range []string{"outbound", "inbound", "both"} {
		for _, hops := range []int{1, 2, 3} {
			t.Run(fmt.Sprintf("%s/H%d", direction, hops), func(t *testing.T) {
				request := cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "service", SourceInfo: cmdb.Matcher{"tenant": "x", "id": "a"}, StartTime: 1700000000, EndTime: 1700000120, Step: "60s", MaxHops: hops, DynamicRelationDirection: direction}
				var baseline []string
				counts := []int{}
				for _, planned := range []bool{false, true} {
					SharedTopologyPlanCandidates = planned
					fetched := 0
					model := &Model{schemaProvider: provider, timeGraphQueryReference: timeGraphTestQueryReference}
					model.timeGraphVMQuery = func(_ context.Context, ts *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
						value := filteredTimeGraphMatrix(t, matrix, ts.QueryList[0].Conditions)
						fetched += len(value)
						return value, nil
					}
					got, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
					require.NoError(t, err)
					canonical := canonicalTopologyForTest(t, got.Snapshots)
					if !planned {
						baseline = canonical
					} else {
						require.Equal(t, baseline, canonical)
					}
					counts = append(counts, fetched)
					request.ResponseFormat = cmdb.CompactTopologyFormat
					compact, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
					require.NoError(t, err)
					require.Equal(t, canonical, canonicalTopologyForTest(t, decodeCompactForTest(t, compact.Compact)))
					request.ResponseFormat = ""
				}
				require.LessOrEqual(t, counts[1], counts[0], "dynamic relations use fixed broad groups without per-instance expansion")
			})
		}
	}
}

func TestTopologyFrontierSelectorCopiesGuardsIntoEveryTuple(t *testing.T) {
	provider := frontierCompositeProvider()
	model := &Model{schemaProvider: provider}
	relations := model.sharedTopologyRelations("space", nil, nil, DirectionOutbound)
	graph := NewTimeGraphWithConfig(model.timeGraphConfig("space"))
	loader := &timeGraphMatrixLoader{graph: graph, start: time.Unix(1700000000, 0), end: time.Unix(1700000120, 0), step: time.Minute, lookBack: 5 * time.Minute}
	for _, targetAnchor := range []bool{false, true} {
		ts, err := loader.frontierQuery(context.Background(), "space", relations[0], targetAnchor, []cmdb.Matcher{{"tenant": "x", "id": "b"}, {"tenant": "y", "id": "c"}})
		require.NoError(t, err)
		groups, err := ts.QueryList[0].Conditions.AnalysisConditions()
		require.NoError(t, err)
		require.Len(t, groups, 2)
		prefix, other := "from_", "to_"
		if targetAnchor {
			prefix, other = other, prefix
		}
		for _, tc := range []struct {
			tenant, id, otherTenant, otherID string
			want                             bool
		}{
			{"x", "b", "z", "n", true},
			{"y", "c", "z", "n", true},
			{"x", "c", "z", "n", false},
			{"y", "b", "z", "n", false},
			{"x", "b", "z", "", false},
			{"y", "c", "", "n", false},
		} {
			matched := topologyFixtureMatchesGroups(groups, map[string]string{prefix + "tenant": tc.tenant, prefix + "id": tc.id, other + "tenant": tc.otherTenant, other + "id": tc.otherID})
			require.Equal(t, tc.want, matched)
		}
	}
}

func TestTopologyFrontierBatchSplitsIdentityBeforeTimeAndKeepsPartial(t *testing.T) {
	for _, yolo := range []bool{false, true} {
		t.Run(fmt.Sprint(yolo), func(t *testing.T) {
			previous := yoloMode
			yoloMode = yolo
			t.Cleanup(func() { yoloMode = previous })
			ctx := metadata.WithBackendResponseLimit(metadata.WithExactTimeGrid(initTimeGraphQueryTestEnvironment()), 1024)
			provider := frontierCompositeProvider()
			model := &Model{schemaProvider: provider, timeGraphQueryReference: timeGraphTestQueryReference}
			const stamp int64 = 1700000000000
			matrix := pl.Matrix{}
			identities := []cmdb.Matcher{}
			for i := 0; i < 17; i++ {
				id := fmt.Sprint(i)
				identities = append(identities, cmdb.Matcher{"tenant": "x", "id": id})
				matrix = append(matrix, contractMatrix(map[string]string{"from_tenant": "x", "from_id": id, "to_tenant": "y", "to_id": "target"}, stamp, stamp+60000, stamp+120000)...)
			}
			calls, applied := 0, 0
			model.timeGraphVMQueryWithPartial = func(queryCtx context.Context, ts *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, bool, error) {
				calls++
				require.Equal(t, "1700000000", ts.Start, "identity sharding must keep the entire time grid")
				require.Equal(t, "1700000120", ts.End)
				groups, err := ts.QueryList[0].Conditions.AnalysisConditions()
				require.NoError(t, err)
				if len(groups) > 4 {
					if !yolo {
						metadata.MarkBackendResponseLimitExceeded(queryCtx)
						return nil, true, nil
					}
					return nil, false, metadata.ErrBackendResponseTooLarge
				}
				return filteredTimeGraphMatrix(t, matrix, ts.QueryList[0].Conditions), true, nil
			}
			grid, err := NewTopologyGrid([]int64{stamp, stamp + 60000, stamp + 120000})
			require.NoError(t, err)
			graph, err := newSharedTimeGraph(model.timeGraphConfig("space"), grid)
			require.NoError(t, err)
			loader := &timeGraphMatrixLoader{model: model, graph: graph, start: time.Unix(1700000000, 0), end: time.Unix(1700000120, 0), step: time.Minute, lookBack: 5 * time.Minute, topology: true}
			relations := model.sharedTopologyRelations("space", nil, nil, DirectionOutbound)
			queries := 0
			err = loader.loadFrontierBatch(withTimeGraphQueryStage(ctx, "relation-edge"), "space", relations[0], false, identities, func(_ context.Context, matrix pl.Matrix) error { applied += len(matrix); return nil }, &queries)
			require.NoError(t, err)
			require.Equal(t, 17, applied)
			require.Greater(t, calls, 4)
			require.Equal(t, calls, queries)
			require.Equal(t, 51, loader.pointCount, "failed oversized batches do not reset successful shard accounting")
			require.Len(t, graph.partialTimes, 3)
			require.False(t, metadata.BackendResponseLimitExceeded(ctx), "a physical response cap must not poison later identity shards")
		})
	}
}

func TestTopologyFrontierSingleIdentityCapAndCanceledShardStayFatal(t *testing.T) {
	for _, cancelShard := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelShard), func(t *testing.T) {
			previous := yoloMode
			yoloMode = false
			t.Cleanup(func() { yoloMode = previous })
			ctx, cancel := context.WithCancel(metadata.WithBackendResponseLimit(metadata.WithExactTimeGrid(initTimeGraphQueryTestEnvironment()), 1024))
			defer cancel()
			model := &Model{schemaProvider: frontierCompositeProvider(), timeGraphQueryReference: timeGraphTestQueryReference}
			identities := []cmdb.Matcher{{"tenant": "x", "id": "a"}}
			if cancelShard {
				identities = append(identities, cmdb.Matcher{"tenant": "x", "id": "b"})
			}
			applied := 0
			model.timeGraphVMQueryWithPartial = func(queryCtx context.Context, ts *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, bool, error) {
				groups, err := ts.QueryList[0].Conditions.AnalysisConditions()
				require.NoError(t, err)
				if !cancelShard || len(groups) > 1 {
					metadata.MarkBackendResponseLimitExceeded(queryCtx)
					return nil, true, nil
				}
				return contractMatrix(map[string]string{"from_tenant": "x", "from_id": "a", "to_tenant": "y", "to_id": "n"}, 1700000000000, 1700000060000, 1700000120000), false, nil
			}
			grid, err := NewTopologyGrid([]int64{1700000000000, 1700000060000, 1700000120000})
			require.NoError(t, err)
			graph, err := newSharedTimeGraph(model.timeGraphConfig("space"), grid)
			require.NoError(t, err)
			loader := &timeGraphMatrixLoader{model: model, graph: graph, start: time.Unix(1700000000, 0), end: time.Unix(1700000120, 0), step: time.Minute, lookBack: 5 * time.Minute, topology: true}
			relations := model.sharedTopologyRelations("space", nil, nil, DirectionOutbound)
			queries := 0
			err = loader.loadFrontierBatch(withTimeGraphQueryStage(ctx, "relation-edge"), "space", relations[0], false, identities, func(_ context.Context, matrix pl.Matrix) error { applied += len(matrix); cancel(); return nil }, &queries)
			if cancelShard {
				require.ErrorIs(t, err, context.Canceled)
				require.Equal(t, 1, applied)
				require.Equal(t, 3, loader.pointCount)
			} else {
				require.ErrorContains(t, err, "max_response_bytes")
				require.Zero(t, applied)
				require.Zero(t, loader.pointCount)
			}
			require.False(t, metadata.BackendResponseLimitExceeded(ctx))
		})
	}
}
