// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestLegacyMissingRouteQuality(t *testing.T) {
	for _, mode := range []string{"instant", "range"} {
		for _, tc := range []struct {
			name    string
			missing map[string]bool
			empty   bool
		}{
			{name: "unavailable direct then hit", missing: map[string]bool{"source_target_flow": true}},
			{name: "unavailable direct then empty", missing: map[string]bool{"source_target_flow": true}, empty: true},
			{name: "all candidates unavailable", missing: map[string]bool{"source_target_flow": true, "source_middle_flow": true}},
			{name: "complete empty", empty: true},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				ctx := initTimeGraphQueryTestEnvironment()
				model := legacyRegressionModel(legacyAlternativeProvider(true))
				model.timeGraphQueryReference = func(ctx context.Context, query *structured.QueryTs) (metadata.QueryReference, error) {
					if tc.missing[query.QueryList[0].FieldName] {
						return nil, nil
					}
					return timeGraphTestQueryReference(ctx, query)
				}
				model.timeGraphVMQuery = func(_ context.Context, query *structured.QueryTs, _ string, _ bool, start, _ time.Time, _ time.Duration) (pl.Matrix, error) {
					name := query.QueryList[0].FieldName
					require.False(t, tc.missing[name], "unresolved aliases must never reach VM")
					if tc.empty {
						return nil, nil
					}
					return legacyAlternativeMatrix(name, start.UnixMilli()), nil
				}
				var path []string
				var count int
				if mode == "instant" {
					_, _, p, _, targets, err := model.QueryResourceMatcher(ctx, "24h", "space", "1700000040", "target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
					require.NoError(t, err)
					path, count = p, len(targets)
				} else {
					_, _, p, _, targets, err := model.QueryResourceMatcherRange(ctx, "", "space", "1m", "1700000040", "1700000100", "target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
					require.NoError(t, err)
					path, count = p, len(targets)
				}
				require.Equal(t, []string{"source", "middle", "target"}, path)
				if tc.empty || tc.missing["source_middle_flow"] {
					require.Zero(t, count)
				} else {
					require.Equal(t, 1, count)
				}
				status := metadata.GetStatus(ctx)
				if len(tc.missing) == 0 {
					require.True(t, status == nil || status.Code != metadata.QueryTsPartial)
				} else {
					require.NotNil(t, status)
					require.Equal(t, metadata.QueryTsPartial, status.Code)
					for name := range tc.missing {
						require.Contains(t, status.Message, "relation_no_physical_route:"+name)
					}
				}
			})
		}
	}
}

func TestLegacyCandidateQualityAndLastEmptyPath(t *testing.T) {
	backendFailure := errors.New("backend failed")
	for _, tc := range []struct {
		name string
		errs []error
		last int
		want error
	}{
		{name: "last empty", errs: []error{nil, nil}, last: 1},
		{name: "unavailable counts as compatible empty", errs: []error{nil, &relationNoPhysicalRouteError{metric: "missing"}}, last: 1},
		{name: "backend failure is not empty", errs: []error{nil, backendFailure}, last: 0, want: backendFailure},
		{name: "deadline is fatal", errs: []error{context.DeadlineExceeded, nil}, last: -1, want: context.DeadlineExceeded},
		{name: "cancel is fatal", errs: []error{context.Canceled, nil}, last: -1, want: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.WithValue(initTimeGraphQueryTestEnvironment(), timeGraphLegacyCompatibilityKey{}, true)
			metadata.SetStatus(ctx, metadata.QueryTsPartial, "path_search_depth_limit")
			index := 0
			_, last, err := queryFirstTimeGraphPath(ctx, make([]resourcePath, len(tc.errs)), func(resourcePath) ([]cmdb.PathResourcesResult, error) {
				err := tc.errs[index]
				index++
				metadata.SetStatus(ctx, "another_status", "")
				return nil, err
			})
			require.Equal(t, tc.last, last)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
			} else {
				require.NoError(t, err)
				require.Equal(t, metadata.QueryTsPartial, metadata.GetStatus(ctx).Code)
				require.Contains(t, metadata.GetStatus(ctx).Message, "path_search_depth_limit")
			}
		})
	}
}

func TestLegacyPathConstraintCompatibility(t *testing.T) {
	oldYolo := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = oldYolo })
	model := &Model{schemaProvider: NewStaticSchemaProvider()}
	for _, tc := range []struct {
		source, target cmdb.Resource
		via            []cmdb.Resource
		count          int
	}{
		{"deployment", "datasource", []cmdb.Resource{"pod"}, 4},
		{"apm_service", "datasource", []cmdb.Resource{"pod"}, 6},
		{"datasource", "apm_service_instance", []cmdb.Resource{"datasource", "pod", "apm_service_instance"}, 1},
		{"datasource", "pod", []cmdb.Resource{""}, 1},
	} {
		t.Run(string(tc.source)+"/"+string(tc.target), func(t *testing.T) {
			_, paths, err := model.buildTimeGraphRequest(initTimeGraphQueryTestEnvironment(), "space", tc.target, tc.source, nil, nil, false, tc.via)
			require.NoError(t, err)
			require.Len(t, paths, tc.count)
		})
	}
	_, _, err := model.buildTimeGraphRequest(initTimeGraphQueryTestEnvironment(), "space", "datasource", "deployment", nil, nil, false, []cmdb.Resource{"pod", ""})
	require.ErrorContains(t, err, "empty resource")

	yoloMode = false
	ctx := initTimeGraphQueryTestEnvironment()
	_, _, err = model.buildTimeGraphRequest(ctx, "space", "datasource", "deployment", nil, nil, false, []cmdb.Resource{"pod"})
	require.NoError(t, err)
	require.Equal(t, &metadata.Status{Code: metadata.QueryTsPartial, Message: "path_search_depth_limit"}, metadata.GetStatus(ctx))
	for _, via := range [][]cmdb.Resource{{"datasource", "pod", "apm_service_instance"}, {""}} {
		ctx := initTimeGraphQueryTestEnvironment()
		target := cmdb.Resource("apm_service_instance")
		if len(via) == 1 {
			target = "pod"
		}
		_, paths, err := model.buildTimeGraphRequest(ctx, "space", target, "datasource", nil, nil, false, via)
		require.NoError(t, err)
		require.Len(t, paths, 1)
		require.Nil(t, metadata.GetStatus(ctx), "unrelated long branches cannot make a complete explicit path partial")
	}
}

func TestLegacyConstrainedLongPathReturnsData(t *testing.T) {
	oldYolo := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = oldYolo })
	model := legacyRegressionModel(yoloLongPathProvider())
	calls := 0
	model.timeGraphVMQuery = func(_ context.Context, query *structured.QueryTs, _ string, _ bool, start, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		calls++
		return filterTopologyRegressionMatrix(query, contractMatrix(map[string]string{"id": "a"}, start.UnixMilli())), nil
	}
	_, _, path, _, result, err := model.QueryResourceMatcher(initTimeGraphQueryTestEnvironment(), "24h", "space", "1700000040", "r12", "r0", cmdb.Matcher{"id": "a"}, nil, false, []cmdb.Resource{"r6"})
	require.NoError(t, err)
	require.Len(t, path, 13)
	require.Equal(t, 12, calls)
	require.Equal(t, cmdb.Matchers{{"id": "a"}}, result)
}

func TestLegacyFrontierCompositeIdentitiesAndTime(t *testing.T) {
	const first int64 = 1700000040000
	const second = first + 60000
	provider := sharedTopologyQueryProvider().(contractSchemaProvider)
	provider.primary["middle"] = []string{"cluster", "namespace", "middle_id"}
	provider.fields["middle"] = provider.primary["middle"]
	model := legacyRegressionModel(provider)
	var calls []string
	model.timeGraphVMQuery = func(_ context.Context, query *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		name := query.QueryList[0].FieldName
		calls = append(calls, name)
		switch name {
		case "source_middle_flow":
			return append(contractMatrix(map[string]string{"source_id": "a", "cluster": "c1", "namespace": "n1", "middle_id": "same"}, first), contractMatrix(map[string]string{"source_id": "a", "cluster": "c2", "namespace": "n2", "middle_id": "same"}, second)...), nil
		case "middle_target_flow":
			cross := contractMatrix(map[string]string{"cluster": "c1", "namespace": "n2", "middle_id": "same", "target_id": "cross"}, first)
			require.Empty(t, filterTopologyRegressionMatrix(query, cross), "complete tuple OR must reject cross products")
			matrix := append(contractMatrix(map[string]string{"cluster": "c1", "namespace": "n1", "middle_id": "same", "target_id": "one"}, first, second), contractMatrix(map[string]string{"cluster": "c2", "namespace": "n2", "middle_id": "same", "target_id": "two"}, first, second)...)
			return filterTopologyRegressionMatrix(query, matrix), nil
		default:
			return nil, fmt.Errorf("unexpected query %s", name)
		}
	}
	_, _, path, _, result, err := model.QueryResourceMatcherRange(initTimeGraphQueryTestEnvironment(), "", "space", "1m", "1700000040", "1700000100", "target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"source", "middle", "target"}, path)
	require.Equal(t, []string{"source_middle_flow", "middle_target_flow"}, calls)
	require.Equal(t, []cmdb.MatchersWithTimestamp{
		{Timestamp: first, Matchers: cmdb.Matchers{{"target_id": "one"}}},
		{Timestamp: second, Matchers: cmdb.Matchers{{"target_id": "two"}}},
	}, result)
}

func TestLegacyEmptyRootStopsWithoutLosingQuality(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			model := legacyRegressionModel(sharedTopologyQueryProvider())
			calls := 0
			model.timeGraphVMQueryWithPartial = func(_ context.Context, query *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, bool, error) {
				calls++
				require.Equal(t, "source_middle_flow", query.QueryList[0].FieldName)
				return nil, partial, nil
			}
			_, _, _, _, result, err := model.QueryResourceMatcher(ctx, "24h", "space", "1700000040", "target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
			require.NoError(t, err)
			require.Empty(t, result)
			require.Equal(t, 1, calls)
			status := metadata.GetStatus(ctx)
			if partial {
				require.NotNil(t, status)
				require.Equal(t, metadata.QueryTsPartial, status.Code)
			} else {
				require.True(t, status == nil || status.Code != metadata.QueryTsPartial)
			}
		})
	}
}

func TestLegacyFrontierDoesNotMutateMatrixOrDropMissingKeys(t *testing.T) {
	ctx := initTimeGraphQueryTestEnvironment()
	model := legacyRegressionModel(sharedTopologyQueryProvider())
	graph := NewTimeGraphWithConfig(model.timeGraphConfig("space"))
	defer graph.Clean(ctx)
	relation := cmdb.Relation{V: []cmdb.Resource{"middle", "target"}, RelationType: "middle_to_target"}
	matrix := contractMatrix(map[string]string{"middle_id": "b", "target_id": "c"}, 1700000040000, 1700000100000)
	key := graph.primaryMatcherKey("middle", cmdb.Matcher{"middle_id": "b"})
	frontier := legacyPathFrontier{1700000040000: {key: {"middle_id": "b"}}}
	filtered, err := graph.filterLegacyPathMatrix(ctx, matrix, relation, frontier, make(legacyPathFrontier))
	require.NoError(t, err)
	require.Len(t, filtered[0].Points, 1)
	require.Len(t, matrix[0].Points, 2)
	_, err = graph.filterLegacyPathMatrix(ctx, contractMatrix(map[string]string{"middle_id": "b"}, 1700000040000), relation, frontier, make(legacyPathFrontier))
	require.ErrorContains(t, err, "incomplete endpoint")
	_, err = graph.filterLegacyPathMatrix(ctx, contractMatrix(map[string]string{"target_id": "c"}, 1700000040000), relation, frontier, make(legacyPathFrontier))
	require.ErrorContains(t, err, "incomplete endpoint")
}

func TestLegacyFrontierDynamicDirectionAndRepeatedResource(t *testing.T) {
	for _, direction := range []TraversalDirection{DirectionOutbound, DirectionInbound} {
		t.Run(string(direction), func(t *testing.T) {
			ctx := context.WithValue(initTimeGraphQueryTestEnvironment(), timeGraphLegacyCompatibilityKey{}, true)
			model := legacyRegressionModel(frontierCompositeProvider())
			calls := 0
			model.timeGraphVMQuery = func(_ context.Context, query *structured.QueryTs, _ string, _ bool, start, _ time.Time, _ time.Duration) (pl.Matrix, error) {
				calls++
				matrix := pl.Matrix{}
				for _, pair := range [][2]string{{"a", "b"}, {"b", "c"}, {"outside", "unreachable"}} {
					from, to := pair[0], pair[1]
					if direction == DirectionInbound {
						from, to = to, from
					}
					matrix = append(matrix, contractMatrix(map[string]string{"from_tenant": "x", "from_id": from, "to_tenant": "x", "to_id": to}, start.UnixMilli())...)
				}
				filtered := filterTopologyRegressionMatrix(query, matrix)
				require.Len(t, filtered, 1, "each hop must read only its current endpoint, including reverse traversal")
				return filtered, nil
			}
			path := cmdb.RelationPath{Steps: []cmdb.RelationPathStep{{ResourceType: "service"}}}
			for hop := 0; hop < 2; hop++ {
				path.Steps = append(path.Steps, cmdb.RelationPathStep{ResourceType: "service", RelationType: "calls", Category: string(RelationCategoryDynamic), Direction: string(direction), MetricName: "calls_flow"})
			}
			stamp := time.Unix(1700000040, 0)
			result, err := model.QueryRelationTimeGraph(ctx, "24h", "space", stamp, stamp, time.Minute, "service", []cmdb.Resource{"service"}, []cmdb.RelationPath{path}, cmdb.Matcher{"tenant": "x", "id": "a"}, nil)
			require.NoError(t, err)
			require.Equal(t, 2, calls)
			require.Len(t, result, 1)
			require.Len(t, result[0].Path, 3)
			require.Equal(t, "c", result[0].Path[2].Dimensions["id"])
		})
	}
}

func TestLegacyFrontierBoundsIdentityBatches(t *testing.T) {
	ctx := initTimeGraphQueryTestEnvironment()
	model := legacyRegressionModel(sharedTopologyQueryProvider())
	calls := 0
	model.timeGraphVMQuery = func(_ context.Context, query *structured.QueryTs, _ string, _ bool, start, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		calls++
		name := query.QueryList[0].FieldName
		matrix := pl.Matrix{}
		for i := 0; i < timeGraphFrontierBatchSize+1; i++ {
			info := map[string]string{"middle_id": fmt.Sprint(i)}
			if name == "source_middle_flow" {
				info["source_id"] = "a"
			} else {
				info["target_id"] = fmt.Sprint(i)
			}
			matrix = append(matrix, contractMatrix(info, start.UnixMilli())...)
		}
		if name == "middle_target_flow" {
			groups, err := query.QueryList[0].Conditions.AnalysisConditions()
			require.NoError(t, err)
			require.LessOrEqual(t, len(groups), timeGraphFrontierBatchSize)
		}
		return filterTopologyRegressionMatrix(query, matrix), nil
	}
	_, _, _, _, result, err := model.QueryResourceMatcher(ctx, "24h", "space", "1700000040", "target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
	require.NoError(t, err)
	require.Len(t, result, timeGraphFrontierBatchSize+1)
	require.Equal(t, 3, calls)
}
