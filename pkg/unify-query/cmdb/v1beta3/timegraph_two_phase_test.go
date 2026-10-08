package v1beta3

import (
	"context"
	"errors"
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
	require.Equal(t, []string{"source_middle_flow", "middle_target_flow"}, fields)
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

func TestSharedTopologyRequiredRelationWithoutRouteFails(t *testing.T) {
	model := sharedTopologyQueryModel(nil)
	model.timeGraphQueryReference = func(context.Context, *structured.QueryTs) (metadata.QueryReference, error) {
		return metadata.QueryReference{}, nil
	}
	result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{
		SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"},
		Timestamp: 1700000000, MaxHops: 2,
	})
	require.ErrorContains(t, err, "required relation has no physical query route")
	require.Empty(t, result.Snapshots)
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
			seedFiltered := false
			for _, field := range q.QueryList[0].Conditions.FieldList {
				if field.DimensionName == "source_id" && field.Operator == structured.ConditionEqual && len(field.Value) == 1 && field.Value[0] == "a" {
					seedFiltered = true
				}
			}
			require.Equal(t, sourceQueries == 1, seedFiltered)
			if sourceQueries == 1 {
				return contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000), nil
			}
			return append(
				contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000),
				contractMatrix(map[string]string{"source_id": "c", "middle_id": "d"}, 1700000000000)...,
			), nil
		case "middle_source_flow":
			return contractMatrix(map[string]string{"middle_id": "b", "source_id": "c"}, 1700000000000), nil
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
	require.Equal(t, 2, sourceQueries, "the reached source type needs an unfiltered candidate read")
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
	results, err := model.QueryPathResources(ctx, "5m", "space", "1700000000", "source", []cmdb.Resource{"middle"},
		[][]cmdb.Resource{{"source", "middle"}}, cmdb.Matcher{"source_id": "a"})
	require.NoError(t, err)
	require.True(t, infoQueried)
	require.Len(t, results, 1)
	require.Equal(t, cmdb.Resource("middle"), results[0].TargetType)
	require.Len(t, results[0].Path, 2)
}
