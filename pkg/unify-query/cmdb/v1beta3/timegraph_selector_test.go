package v1beta3

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"
)

func selectorProvider() contractSchemaProvider {
	return contractSchemaProvider{
		resources: []ResourceType{"a", "b", "c"},
		primary:   map[ResourceType][]string{"a": {"a_id"}, "b": {"b_id"}, "c": {"c_id"}},
		fields:    map[ResourceType][]string{"a": {"a_id"}, "b": {"b_id"}, "c": {"c_id"}},
		schemas: []RelationSchema{
			{RelationType: "ab", Category: RelationCategoryStatic, FromType: "a", ToType: "b", MetricName: "ab"},
			{RelationType: "bc", Category: RelationCategoryStatic, FromType: "b", ToType: "c", MetricName: "bc"},
		},
	}
}

func TestTopologySelectorFixedTwoStagesAndFullAnswer(t *testing.T) {
	old := SharedTopologyPlanCandidates
	t.Cleanup(func() { SharedTopologyPlanCandidates = old })
	for _, count := range []int{1, 64, 65, 128, 129} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			data := map[string]pl.Matrix{}
			for i := 0; i < count; i++ {
				data["ab"] = append(data["ab"], contractMatrix(map[string]string{"a_id": "root", "b_id": fmt.Sprint(i)}, 1700000000000)...)
				// A different parent is an induced edge, not a traversal-tree edge.
				data["ab"] = append(data["ab"], contractMatrix(map[string]string{"a_id": "other", "b_id": fmt.Sprint(i)}, 1700000000000)...)
				data["bc"] = append(data["bc"], contractMatrix(map[string]string{"b_id": fmt.Sprint(i), "c_id": fmt.Sprint(i)}, 1700000000000, 1700000060000)...)
			}
			data["bc"] = append(data["bc"], contractMatrix(map[string]string{"b_id": "unrelated", "c_id": "unrelated"}, 1700000000000)...)
			request := cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "a", SourceInfo: cmdb.Matcher{"a_id": "root"}, StartTime: 1700000000, EndTime: 1700000060, Step: "60s", MaxHops: 2}
			var baseline []string
			for _, planned := range []bool{false, true} {
				SharedTopologyPlanCandidates = planned
				calls := 0
				model := &Model{schemaProvider: selectorProvider(), timeGraphQueryReference: selectorQueryReference}
				model.timeGraphVMQuery = func(_ context.Context, ts *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
					calls++
					return filteredTimeGraphMatrix(t, data[ts.QueryList[0].FieldName], ts.QueryList[0].Conditions), nil
				}
				result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
				require.NoError(t, err)
				canonical := canonicalTopologyForTest(t, result.Snapshots)
				if !planned {
					baseline = canonical
					continue
				}
				require.Equal(t, baseline, canonical)
				expected := 3
				if count > 64 && count <= 128 {
					expected = 5
				}
				require.Equal(t, expected, calls, "each physical group is fetched once per fixed identity batch")
				request.ResponseFormat = cmdb.CompactTopologyFormat
				compact, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
				require.NoError(t, err)
				require.Equal(t, baseline, canonicalTopologyForTest(t, decodeCompactForTest(t, compact.Compact)))
			}
		})
	}
}

func TestTopologySelectorDeepUsesFixedGroups(t *testing.T) {
	previous := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = previous })
	data := map[string]pl.Matrix{}
	for i := 0; i < 130; i++ {
		data["ab"] = append(data["ab"], contractMatrix(map[string]string{"a_id": "root", "b_id": fmt.Sprint(i)}, 1700000000000)...)
		data["bc"] = append(data["bc"], contractMatrix(map[string]string{"b_id": fmt.Sprint(i), "c_id": fmt.Sprint(i)}, 1700000000000)...)
	}
	model := &Model{schemaProvider: selectorProvider(), timeGraphQueryReference: selectorQueryReference}
	calls := 0
	model.timeGraphVMQuery = func(_ context.Context, ts *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		calls++
		return filteredTimeGraphMatrix(t, data[ts.QueryList[0].FieldName], ts.QueryList[0].Conditions), nil
	}
	result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "a", SourceInfo: cmdb.Matcher{"a_id": "root"}, Timestamp: 1700000000, MaxHops: 10})
	require.NoError(t, err)
	require.Len(t, result.Snapshots[0].Nodes, 261)
	require.Len(t, result.Snapshots[0].Edges, 520)
	require.Equal(t, 3, calls, "deep queries must not expand instances through unbounded serial frontier batches")
}

func selectorQueryReference(ctx context.Context, query *structured.QueryTs) (metadata.QueryReference, error) {
	ref, err := timeGraphTestQueryReference(ctx, query)
	metadata.GetQueryParams(ctx).SetStorageType(metadata.VictoriaMetricsStorageType)
	return ref, err
}

func TestTopologySelectorPreservesAttributeApplicationOrder(t *testing.T) {
	old := SharedTopologyPlanCandidates
	t.Cleanup(func() { SharedTopologyPlanCandidates = old })
	provider := selectorProvider()
	provider.resources = []ResourceType{"a", "b"}
	provider.fields["a"] = []string{"a_id", "b_id"}
	provider.schemas = []RelationSchema{
		{RelationType: "r1", Category: RelationCategoryStatic, FromType: "a", ToType: "b", MetricName: "x"},
		{RelationType: "r2", Category: RelationCategoryStatic, FromType: "a", ToType: "b", MetricName: "y"},
		{RelationType: "r3", Category: RelationCategoryStatic, FromType: "a", ToType: "b", MetricName: "x"},
	}
	data := map[string]pl.Matrix{
		"x": contractMatrix(map[string]string{"a_id": "root", "b_id": "one"}, 1700000000000),
		"y": contractMatrix(map[string]string{"a_id": "root", "b_id": "two"}, 1700000000000),
	}
	var baseline []string
	for _, planned := range []bool{false, true} {
		SharedTopologyPlanCandidates = planned
		model := &Model{schemaProvider: provider, timeGraphQueryReference: selectorQueryReference}
		model.timeGraphVMQuery = func(_ context.Context, ts *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
			return filteredTimeGraphMatrix(t, data[ts.QueryList[0].FieldName], ts.QueryList[0].Conditions), nil
		}
		result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "a", SourceInfo: cmdb.Matcher{"a_id": "root"}, Timestamp: 1700000000, MaxHops: 2})
		require.NoError(t, err)
		if !planned {
			baseline = canonicalTopologyForTest(t, result.Snapshots)
		} else {
			require.Equal(t, baseline, canonicalTopologyForTest(t, result.Snapshots))
		}
	}
}

func selectorScopeProvider() contractSchemaProvider {
	return contractSchemaProvider{
		resources: []ResourceType{"container", "pod", "node", "system", "host"},
		primary: map[ResourceType][]string{
			"container": {"cluster", "namespace", "container_id"}, "pod": {"cluster", "namespace", "pod_id"},
			"node": {"cluster", "node_id"}, "system": {"system_id"}, "host": {"host_id"},
		},
		fields: map[ResourceType][]string{},
		schemas: []RelationSchema{
			{RelationType: "cp", Category: RelationCategoryStatic, FromType: "container", ToType: "pod", MetricName: "cp"},
			{RelationType: "np", Category: RelationCategoryStatic, FromType: "node", ToType: "pod", MetricName: "np"},
			{RelationType: "ns", Category: RelationCategoryStatic, FromType: "node", ToType: "system", MetricName: "ns"},
			{RelationType: "hs", Category: RelationCategoryStatic, FromType: "host", ToType: "system", MetricName: "hs"},
		},
	}
}

func TestTopologySelectorRangeProofPreservesCrossScopeBoundaries(t *testing.T) {
	oldPlan, oldYolo := SharedTopologyPlanCandidates, yoloMode
	yoloMode = true
	t.Cleanup(func() { SharedTopologyPlanCandidates = oldPlan; yoloMode = oldYolo })
	const stamp int64 = 1700000000000
	data := map[string]pl.Matrix{}
	add := func(metric string, labels map[string]string, times ...int64) {
		data[metric] = append(data[metric], contractMatrix(labels, times...)...)
	}
	add("cp", map[string]string{"cluster": "alpha", "namespace": "one", "container_id": "root", "pod_id": "p0"}, stamp, stamp+60000)
	add("np", map[string]string{"cluster": "alpha", "namespace": "one", "node_id": "n0", "pod_id": "p0"}, stamp, stamp+60000)
	add("ns", map[string]string{"cluster": "alpha", "node_id": "n0", "system_id": "bridge"}, stamp)
	add("ns", map[string]string{"cluster": "beta", "node_id": "n1", "system_id": "bridge"}, stamp, stamp+60000)
	add("hs", map[string]string{"host_id": "h0", "system_id": "bridge"}, stamp, stamp+60000)
	add("np", map[string]string{"cluster": "beta", "namespace": "two", "node_id": "n1", "pod_id": "p1"}, stamp, stamp+60000)
	add("cp", map[string]string{"cluster": "beta", "namespace": "two", "container_id": "c1", "pod_id": "p1"}, stamp, stamp+60000)
	add("np", map[string]string{"cluster": "alpha", "namespace": "two", "node_id": "n0", "pod_id": "p2"}, stamp, stamp+60000)
	add("cp", map[string]string{"cluster": "alpha", "namespace": "two", "container_id": "c2", "pod_id": "p2"}, stamp, stamp+60000)
	for _, hops := range []int{1, 2, 3, 4, 5, 6, 10} {
		t.Run(fmt.Sprint(hops), func(t *testing.T) {
			var baseline []string
			for _, planned := range []bool{false, true} {
				SharedTopologyPlanCandidates = planned
				model := &Model{schemaProvider: selectorScopeProvider(), timeGraphQueryReference: selectorQueryReference}
				queriedNP := false
				model.timeGraphVMQuery = func(_ context.Context, ts *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
					if planned && ts.QueryList[0].FieldName == "np" && hops >= 4 {
						queriedNP = true
						graph := NewTimeGraphWithConfig(model.timeGraphConfig("space"))
						allowed := model.sharedTopologyRelations("space", nil, nil, DirectionBoth)
						filters := graph.topologyRangeFilters("container", cmdb.Matcher{"cluster": "alpha", "namespace": "one", "container_id": "root"}, topologyRelationComponent("container", allowed), allowed, hops)
						for _, relation := range allowed {
							if relation.MetricName != "np" {
								continue
							}
							cluster := filters[timeGraphRelationKeyFor(relation)]["cluster"]
							if hops == 4 {
								require.Equal(t, "alpha", cluster)
							} else {
								require.Empty(t, cluster, "range narrowing must not exclude cross-cluster Pods at H5")
							}
						}
					}
					return filteredTimeGraphMatrix(t, data[ts.QueryList[0].FieldName], ts.QueryList[0].Conditions), nil
				}
				request := cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "container", SourceInfo: cmdb.Matcher{"cluster": "alpha", "namespace": "one", "container_id": "root"}, StartTime: stamp / 1000, EndTime: stamp/1000 + 60, Step: "60s", MaxHops: hops}
				result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
				require.NoError(t, err)
				if !planned {
					baseline = canonicalTopologyForTest(t, result.Snapshots)
				} else {
					require.Equal(t, baseline, canonicalTopologyForTest(t, result.Snapshots))
					if hops >= 4 {
						require.True(t, queriedNP)
					}
					request.ResponseFormat = cmdb.CompactTopologyFormat
					compact, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
					require.NoError(t, err)
					require.Equal(t, baseline, canonicalTopologyForTest(t, decodeCompactForTest(t, compact.Compact)))
				}
			}
		})
	}
}

func TestTopologySelectorRejectsUnprovedClosedNeighborhood(t *testing.T) {
	oldYolo := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = oldYolo })
	old := SharedTopologyPlanCandidates
	t.Cleanup(func() { SharedTopologyPlanCandidates = old })
	for _, kind := range []string{"neighbor-edge", "self-edge", "attribute-outside-star", "one-way"} {
		t.Run(kind, func(t *testing.T) {
			provider := selectorProvider()
			data := map[string]pl.Matrix{
				"ab": contractMatrix(map[string]string{"a_id": "root", "b_id": "b0"}, 1700000000000),
				"bc": contractMatrix(map[string]string{"b_id": "b0", "c_id": "c0"}, 1700000000000),
				"ac": contractMatrix(map[string]string{"a_id": "root", "c_id": "c0"}, 1700000000000),
			}
			switch kind {
			case "neighbor-edge":
				provider.schemas = append(provider.schemas, RelationSchema{RelationType: "ac", Category: RelationCategoryStatic, FromType: "a", ToType: "c", MetricName: "ac"})
			case "self-edge":
				provider.schemas = append(provider.schemas, RelationSchema{RelationType: "aa", Category: RelationCategoryStatic, FromType: "a", ToType: "a", MetricName: "aa"})
				data["aa"] = contractMatrix(map[string]string{"a_id": "root"}, 1700000000000)
			case "attribute-outside-star":
				provider.fields["b"] = append(provider.fields["b"], "c_id")
			case "one-way":
				provider.schemas[0].IsDirectional = true
			}
			for _, hops := range []int{1, 2, 10} {
				var baseline []string
				for _, planned := range []bool{false, true} {
					SharedTopologyPlanCandidates = planned
					model := &Model{schemaProvider: provider, timeGraphQueryReference: selectorQueryReference}
					model.timeGraphVMQuery = func(_ context.Context, ts *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
						return filteredTimeGraphMatrix(t, data[ts.QueryList[0].FieldName], ts.QueryList[0].Conditions), nil
					}
					result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "a", SourceInfo: cmdb.Matcher{"a_id": "root"}, Timestamp: 1700000000, MaxHops: hops})
					require.NoError(t, err)
					if !planned {
						baseline = canonicalTopologyForTest(t, result.Snapshots)
					} else {
						require.Equal(t, baseline, canonicalTopologyForTest(t, result.Snapshots))
					}
				}
			}
		})
	}
}

func TestTopologySelectorDoesNotRestoreDisconnectedAttributeRoutes(t *testing.T) {
	provider := selectorProvider()
	provider.resources = append(provider.resources, "d", "e")
	provider.primary["d"], provider.primary["e"] = []string{"d_id"}, []string{"e_id"}
	provider.fields["d"], provider.fields["e"] = []string{"d_id", "e_id"}, []string{"e_id"}
	provider.schemas = append(provider.schemas, RelationSchema{RelationType: "de", Category: RelationCategoryStatic, FromType: "d", ToType: "e", MetricName: "missing"})
	model := &Model{schemaProvider: provider}
	model.timeGraphQueryReference = func(ctx context.Context, ts *structured.QueryTs) (metadata.QueryReference, error) {
		require.NotEqual(t, "missing", ts.QueryList[0].FieldName, "a disconnected attribute collision must not become a required route")
		return selectorQueryReference(ctx, ts)
	}
	model.timeGraphVMQuery = func(_ context.Context, ts *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		return contractMatrix(map[string]string{"a_id": "root", "b_id": "b0"}, 1700000000000), nil
	}
	result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "a", SourceInfo: cmdb.Matcher{"a_id": "root"}, Timestamp: 1700000000, MaxHops: 1})
	require.NoError(t, err)
	require.Len(t, result.Snapshots[0].Nodes, 2)
	require.Len(t, result.Snapshots[0].Edges, 2)
}

func TestTopologySelectorPartialFirstStageRetainsQualityAndChecksWideRoutes(t *testing.T) {
	oldYolo := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = oldYolo })
	for _, hops := range []int{2, 10} {
		for _, missing := range []bool{false, true} {
			t.Run(fmt.Sprintf("hops=%d/missing=%v", hops, missing), func(t *testing.T) {
				model := &Model{schemaProvider: selectorProvider()}
				preparations, fetches := map[string]int{}, map[string]int{}
				model.timeGraphQueryReference = func(ctx context.Context, ts *structured.QueryTs) (metadata.QueryReference, error) {
					metric := ts.QueryList[0].FieldName
					preparations[metric]++
					if missing && metric == "bc" {
						return metadata.QueryReference{}, nil
					}
					return selectorQueryReference(ctx, ts)
				}
				model.timeGraphVMQueryWithPartial = func(_ context.Context, ts *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, bool, error) {
					metric := ts.QueryList[0].FieldName
					fetches[metric]++
					if metric == "ab" {
						return contractMatrix(map[string]string{"a_id": "root", "b_id": "b0"}, 1700000000000), true, nil
					}
					return contractMatrix(map[string]string{"b_id": "b0", "c_id": "c0"}, 1700000000000), false, nil
				}
				result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "a", SourceInfo: cmdb.Matcher{"a_id": "root"}, Timestamp: 1700000000, MaxHops: hops})
				if missing {
					require.ErrorContains(t, err, "required relation has no physical query route: bc")
					require.Empty(t, fetches, "validate the planned second stage even before the first stage can be empty")
					return
				}
				require.NoError(t, err)
				require.Positive(t, preparations["bc"])
				require.Len(t, result.Snapshots[0].Nodes, 3)
				require.Len(t, result.Snapshots[0].Edges, 4)
				require.True(t, result.Snapshots[0].Partial)
				require.Equal(t, "backend_partial", result.Snapshots[0].PartialReason)
				require.Equal(t, map[string]int{"ab": 2, "bc": 1}, fetches, "continue the same loader without repeating source preparation or root probing")
			})
		}
	}
}

func TestTopologySelectorIgnoresOnlyUnobservableAttributeCollisions(t *testing.T) {
	old := SharedTopologyPlanCandidates
	t.Cleanup(func() { SharedTopologyPlanCandidates = old })
	provider := selectorProvider()
	provider.resources = append(provider.resources, "version")
	provider.primary["version"] = []string{"version"}
	provider.fields["version"] = []string{"version"}
	provider.fields["c"] = append(provider.fields["c"], "version")
	provider.schemas = append(provider.schemas, RelationSchema{RelationType: "cv", Category: RelationCategoryStatic, FromType: "c", ToType: "version", MetricName: "cv"})
	data := map[string]pl.Matrix{
		"ab": contractMatrix(map[string]string{"a_id": "root", "b_id": "b0"}, 1700000000000),
		"bc": contractMatrix(map[string]string{"b_id": "b0", "c_id": "c0"}, 1700000000000),
		"cv": contractMatrix(map[string]string{"c_id": "c0", "version": "v1"}, 1700000000000),
	}
	for _, hops := range []int{1, 2} {
		var baseline []string
		for _, planned := range []bool{false, true} {
			SharedTopologyPlanCandidates = planned
			model := &Model{schemaProvider: provider, timeGraphQueryReference: selectorQueryReference}
			metrics := []string{}
			model.timeGraphVMQuery = func(_ context.Context, ts *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
				metrics = append(metrics, ts.QueryList[0].FieldName)
				return filteredTimeGraphMatrix(t, data[ts.QueryList[0].FieldName], ts.QueryList[0].Conditions), nil
			}
			result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "a", SourceInfo: cmdb.Matcher{"a_id": "root"}, Timestamp: 1700000000, MaxHops: hops})
			require.NoError(t, err)
			if !planned {
				baseline = canonicalTopologyForTest(t, result.Snapshots)
				continue
			}
			require.Equal(t, baseline, canonicalTopologyForTest(t, result.Snapshots))
			if hops == 1 {
				require.Equal(t, []string{"ab"}, metrics, "an attribute of an unreachable type cannot disable the star plan")
			} else {
				require.Contains(t, metrics, "cv", "a visible endpoint keeps its attribute provider beyond H")
				found := false
				for _, node := range result.Snapshots[0].Nodes {
					if node.ResourceType == "c" {
						require.Equal(t, "v1", node.Dimensions["version"])
						found = true
					}
				}
				require.True(t, found)
			}
		}
	}
}
