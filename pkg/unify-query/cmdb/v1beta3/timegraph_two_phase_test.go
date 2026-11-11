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

func TestSharedTopologyCompleteFirstHopEmptyStopsFetching(t *testing.T) {
	model := sharedTopologyQueryModel(map[string]pl.Matrix{"source_middle_flow": nil})
	var fields []string
	query := model.timeGraphVMQuery
	model.timeGraphVMQuery = func(ctx context.Context, q *structured.QueryTs, expr string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
		fields = append(fields, q.QueryList[0].FieldName)
		return query(ctx, q, expr, instant, start, end, step)
	}
	result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{
		SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"},
		StartTime: 1700000000, EndTime: 1700000200, Step: "100s", MaxHops: 2,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"source_middle_flow"}, fields)
	require.Len(t, result.Snapshots, 3)
	for _, snapshot := range result.Snapshots {
		require.Empty(t, snapshot.Nodes)
		require.Empty(t, snapshot.Edges)
		require.False(t, snapshot.Partial)
	}
}

func TestSharedTopologyNoLegalRelationsDoesNotQueryInfo(t *testing.T) {
	provider := sharedTopologyQueryProvider().(contractSchemaProvider)
	provider.schemas = nil
	model := &Model{schemaProvider: provider, timeGraphQueryReference: timeGraphTestQueryReference}
	model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		t.Fatalf("unexpected query: %s", q.QueryList[0].FieldName)
		return nil, nil
	}
	result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{
		SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"},
		Timestamp: 1700000000, MaxHops: 2,
	})
	require.NoError(t, err)
	require.Len(t, result.Snapshots, 1)
	require.Empty(t, result.Snapshots[0].Nodes)
}

func TestSharedTopologyPartialFirstHopIsNotCompleteEmpty(t *testing.T) {
	model := sharedTopologyQueryModel(nil)
	var fields []string
	model.timeGraphVMQueryWithPartial = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, bool, error) {
		field := q.QueryList[0].FieldName
		fields = append(fields, field)
		return nil, field == "source_middle_flow", nil
	}
	result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{
		SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"},
		StartTime: 1700000000, EndTime: 1700000100, Step: "100s", MaxHops: 2,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"source_middle_flow", "middle_target_flow"}, fields, "an incomplete first stage continues the broad plan without repeating the root probe")
	for _, snapshot := range result.Snapshots {
		require.Empty(t, snapshot.Nodes)
		require.True(t, snapshot.Partial)
	}
}

func TestSharedTopologyPartialIdentityMatchesMultipleRootsPerFrame(t *testing.T) {
	provider := sharedTopologyQueryProvider().(contractSchemaProvider)
	provider.primary["source"] = []string{"tenant", "source_id"}
	provider.fields["source"] = []string{"tenant", "source_id"}
	model := &Model{schemaProvider: provider, timeGraphQueryReference: timeGraphTestQueryReference}
	model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		switch q.QueryList[0].FieldName {
		case "source_middle_flow":
			foundTenantFilter := false
			for _, field := range q.QueryList[0].Conditions.FieldList {
				if field.DimensionName == "tenant" && field.Operator == structured.ConditionEqual && len(field.Value) == 1 && field.Value[0] == "x" {
					foundTenantFilter = true
				}
			}
			require.True(t, foundTenantFilter)
			return append(
				append(
					contractMatrix(map[string]string{"tenant": "x", "source_id": "a", "middle_id": "m1"}, 1700000000000),
					contractMatrix(map[string]string{"tenant": "x", "source_id": "b", "middle_id": "m2"}, 1700000000000)...,
				),
				contractMatrix(map[string]string{"tenant": "y", "source_id": "c", "middle_id": "m3"}, 1700000000000)...,
			), nil
		case "middle_target_flow":
			return append(
				contractMatrix(map[string]string{"middle_id": "m1", "target_id": "t1"}, 1700000000000),
				contractMatrix(map[string]string{"middle_id": "m2", "target_id": "t2"}, 1700000000000)...,
			), nil
		default:
			t.Fatalf("unexpected query: %s", q.QueryList[0].FieldName)
			return nil, nil
		}
	}
	result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{
		SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"tenant": "x"},
		StartTime: 1700000000, EndTime: 1700000100, Step: "100s", MaxHops: 2,
	})
	require.NoError(t, err)
	require.Len(t, result.Snapshots[0].Nodes, 6)
	require.Len(t, result.Snapshots[0].Edges, 4)
	require.Empty(t, result.Snapshots[1].Nodes)
}

// 范围内所有关系都缺少物理路由时返回空图而不是错误：每帧带全部关系的原因，
// 快照和紧凑格式一致，且没有任何关系访问后端。
func TestSharedTopologyAllRelationsWithoutRouteReturnPartialEmpty(t *testing.T) {
	old := SharedTopologyPlanCandidates
	t.Cleanup(func() { SharedTopologyPlanCandidates = old })
	const reason = "relation_no_physical_route:middle_target_flow;relation_no_physical_route:source_middle_flow"
	for _, planned := range []bool{false, true} {
		for _, mode := range []string{"instant", "range"} {
			t.Run(fmt.Sprintf("planned=%v/%s", planned, mode), func(t *testing.T) {
				SharedTopologyPlanCandidates = planned
				model := sharedTopologyQueryModel(nil)
				model.timeGraphQueryReference = func(context.Context, *structured.QueryTs) (metadata.QueryReference, error) {
					return metadata.QueryReference{}, nil
				}
				var fields []string
				model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
					fields = append(fields, q.QueryList[0].FieldName)
					return nil, nil
				}
				request := cmdb.SharedTopologyQuery{
					SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"},
					Timestamp: 1700000000, MaxHops: 2,
				}
				points := 1
				if mode == "range" {
					request.Timestamp, request.StartTime, request.EndTime, request.Step = 0, 1700000000, 1700000200, "100s"
					points = 3
				}
				result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
				require.NoError(t, err)
				require.Len(t, result.Snapshots, points)
				for _, snapshot := range result.Snapshots {
					require.Empty(t, snapshot.Nodes)
					require.Empty(t, snapshot.Edges)
					require.True(t, snapshot.Partial)
					require.Equal(t, reason, snapshot.PartialReason)
				}

				request.ResponseFormat = cmdb.CompactTopologyFormat
				compact, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
				require.NoError(t, err)
				require.NotNil(t, compact.Compact)
				require.Empty(t, compact.Compact.Nodes)
				require.Empty(t, compact.Compact.Edges)
				require.Len(t, compact.Compact.Partial, points)
				for i, partial := range compact.Compact.Partial {
					require.Equal(t, cmdb.CompactTopologyPartial{Index: i, Reason: reason}, partial)
				}
				require.Equal(t, canonicalTopologyForTest(t, result.Snapshots), canonicalTopologyForTest(t, decodeCompactForTest(t, compact.Compact)))
				require.Empty(t, fields, "unrouted relations never reach the backend")
			})
		}
	}
}

func TestSharedTopologyBoundaryRelationWithoutRouteIsNotRequired(t *testing.T) {
	model := sharedTopologyQueryModel(map[string]pl.Matrix{
		"source_middle_flow": contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000),
	})
	model.timeGraphQueryReference = func(ctx context.Context, queryTs *structured.QueryTs) (metadata.QueryReference, error) {
		if queryTs.QueryList[0].FieldName == "middle_target_flow" {
			return metadata.QueryReference{}, nil
		}
		return timeGraphTestQueryReference(ctx, queryTs)
	}
	request := cmdb.SharedTopologyQuery{
		SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"},
		Timestamp: 1700000000, MaxHops: 1,
	}
	result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.NoError(t, err)
	require.Len(t, result.Snapshots, 1)
	require.Len(t, result.Snapshots[0].Nodes, 2)
	require.Len(t, result.Snapshots[0].Edges, 1)
	require.False(t, result.Snapshots[0].Partial, "a relation beyond the hop boundary is not in scope")
	request.ResponseFormat = cmdb.CompactTopologyFormat
	compact, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.NoError(t, err)
	require.Equal(t, result.Snapshots, decodeCompactForTest(t, compact.Compact))

	// 进入范围后缺路由关系按已读且无数据处理：其余结果不变，只多出 partial 原因。
	// 该关系若访问后端，sharedTopologyQueryModel 会因未知指标返回错误。
	request.ResponseFormat = ""
	request.MaxHops = 2
	inScope, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.NoError(t, err)
	require.Len(t, inScope.Snapshots, 1)
	require.Equal(t, topologyObjectsForTest(t, result.Snapshots), topologyObjectsForTest(t, inScope.Snapshots))
	require.True(t, inScope.Snapshots[0].Partial)
	require.Equal(t, "relation_no_physical_route:middle_target_flow", inScope.Snapshots[0].PartialReason)
}

func TestSharedTopologyTypeCycleReadsReachedSourceInstances(t *testing.T) {
	provider := sharedTopologyQueryProvider().(contractSchemaProvider)
	provider.schemas = append(provider.schemas, RelationSchema{
		RelationType: "middle_to_source", Category: RelationCategoryStatic,
		FromType: "middle", ToType: "source", IsDirectional: true, MetricName: "middle_source_flow",
	})
	model := &Model{schemaProvider: provider, timeGraphQueryReference: timeGraphTestQueryReference}
	var sourceQueries int
	model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		switch q.QueryList[0].FieldName {
		case "source_middle_flow":
			sourceQueries++
			return filterTopologyRegressionMatrix(q, append(
				contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000),
				contractMatrix(map[string]string{"source_id": "c", "middle_id": "d"}, 1700000000000)...,
			)), nil
		case "middle_source_flow":
			return filterTopologyRegressionMatrix(q, contractMatrix(map[string]string{"middle_id": "b", "source_id": "c"}, 1700000000000)), nil
		case "middle_target_flow":
			return nil, nil
		default:
			t.Fatalf("unexpected query: %s", q.QueryList[0].FieldName)
			return nil, nil
		}
	}
	result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{
		SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"},
		Timestamp: 1700000000, MaxHops: 3,
	})
	require.NoError(t, err)
	require.Equal(t, 1, sourceQueries, "the directed type cycle uses one broad read covering every reached source instance")
	require.Len(t, result.Snapshots[0].Nodes, 4)
	require.Len(t, result.Snapshots[0].Edges, 3)
}

func TestLegacyOptionalTargetInfoFailureKeepsRelationResult(t *testing.T) {
	provider := sharedTopologyQueryProvider().(contractSchemaProvider)
	provider.schemas = provider.schemas[:1]
	provider.fields["middle"] = append(provider.fields["middle"], "zone")
	model := &Model{schemaProvider: provider, timeGraphQueryReference: timeGraphTestQueryReference}
	infoQueried := false
	model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		switch q.QueryList[0].FieldName {
		case "source_middle_flow":
			return contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000), nil
		case "middle_info_relation":
			infoQueried = true
			return nil, errors.New("optional info unavailable")
		default:
			t.Fatalf("unexpected query: %s", q.QueryList[0].FieldName)
			return nil, nil
		}
	}
	ctx := withTimeGraphTargetInfoShow(initTimeGraphQueryTestEnvironment(), true)
	metricLabels := map[string]string{"stage": "target-info", "result": "failed"}
	failedBefore := readTimeGraphMetric(t, "cmdb_timegraph_load_operations_total", metricLabels, "counter")
	results, err := model.QueryRelationTimeGraph(
		ctx, "5m", "space",
		time.Unix(1700000000, 0), time.Unix(1700000000, 0), 5*time.Minute,
		"source", []cmdb.Resource{"middle"},
		cmdb.RelationPathsFromResourcePaths([][]cmdb.Resource{{"source", "middle"}}),
		cmdb.Matcher{"source_id": "a"}, nil,
	)
	require.NoError(t, err)
	require.True(t, infoQueried)
	require.Equal(t, failedBefore+1, readTimeGraphMetric(t, "cmdb_timegraph_load_operations_total", metricLabels, "counter"))
	require.Len(t, results, 1)
	require.Equal(t, cmdb.Resource("middle"), results[0].TargetType)
	require.Equal(t, cmdb.Resource("middle"), results[0].Target.ResourceType)
	require.Equal(t, "b", results[0].Target.Dimensions["middle_id"])
}
