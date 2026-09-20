package v1beta3

import (
	"context"
	"errors"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func requireGraphLimitError(t *testing.T, err error, reason string, count, limit int) {
	t.Helper()
	var limitErr *ResultLimitError
	require.ErrorAs(t, err, &limitErr)
	require.Equal(t, reason, limitErr.Reason)
	require.Equal(t, count, limitErr.Count)
	require.Equal(t, limit, limitErr.Limit)
}

func TestTimeGraphNodeLimitBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		ids       []string
		wantError bool
	}{
		{name: "below_limit", ids: []string{"n1"}},
		{name: "at_limit", ids: []string{"n1", "n2"}},
		{name: "duplicate_node_does_not_count_twice", ids: []string{"n1", "n1", "n2"}},
		{name: "above_limit", ids: []string{"n1", "n2", "n3"}, wantError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tg := NewTimeGraphWithConfig(&TimeGraphConfig{
				Resource:     []TimeGraphResourceConfig{{Name: "node", Index: cmdb.Index{"id"}}},
				MaxNodes:     2,
				MaxNodeInfos: 100,
			})
			for i, id := range tc.ids {
				err := tg.AddTimeNode(context.Background(), "node", cmdb.Matcher{"id": id}, 100)
				if tc.wantError && i == len(tc.ids)-1 {
					requireGraphLimitError(t, err, "max_graph_nodes", 3, 2)
					return
				}
				require.NoError(t, err)
			}
		})
	}
}

func TestTimeGraphRelationEntryNodeLimitBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		relations  []cmdb.Matcher
		wantErr    bool
		wantCount  int
		wantReason string
		wantLimit  int
	}{
		{name: "one_edge_creates_two_nodes", relations: []cmdb.Matcher{{"from_id": "a", "to_id": "b"}}, wantCount: 2},
		{name: "two_edges_create_three_unique_nodes", relations: []cmdb.Matcher{{"from_id": "a", "to_id": "b"}, {"from_id": "a", "to_id": "c"}}, wantCount: 3},
		{name: "third_target_exceeds_node_limit", relations: []cmdb.Matcher{{"from_id": "a", "to_id": "b"}, {"from_id": "a", "to_id": "c"}, {"from_id": "a", "to_id": "d"}}, wantErr: true, wantCount: 4, wantReason: "max_graph_nodes", wantLimit: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tg := NewTimeGraphWithConfig(&TimeGraphConfig{
				Resource: []TimeGraphResourceConfig{{Name: "service", Index: cmdb.Index{"id"}}},
				MaxNodes: 3, MaxEdges: 100, MaxNodeInfos: 100,
			})
			relation := cmdb.Relation{
				V:            []cmdb.Resource{"service", "service"},
				RelationType: "service_to_service",
				MetricName:   "service_to_service_flow",
				Category:     string(RelationCategoryDynamic),
				Direction:    string(DirectionOutbound),
			}
			for i, info := range tc.relations {
				err := tg.AddTimeRelationWithRelation(context.Background(), relation, info, 100)
				if tc.wantErr && i == len(tc.relations)-1 {
					requireGraphLimitError(t, err, tc.wantReason, tc.wantCount, tc.wantLimit)
					return
				}
				require.NoError(t, err)
			}
			require.Equal(t, tc.wantCount, tg.nodeBuilder.Length())
		})
	}
}

func TestTimeGraphEdgeLimitBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		ids       []string
		wantError bool
	}{
		{name: "below_limit", ids: []string{"a"}},
		{name: "at_limit", ids: []string{"a", "b"}},
		{name: "duplicate_edge_does_not_count_twice", ids: []string{"a", "a", "b"}},
		{name: "above_limit", ids: []string{"a", "b", "c"}, wantError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tg := NewTimeGraphWithConfig(&TimeGraphConfig{
				Resource: []TimeGraphResourceConfig{
					{Name: "left", Index: cmdb.Index{"id"}},
					{Name: "right", Index: cmdb.Index{"id"}},
				},
				MaxNodes:     100,
				MaxEdges:     2,
				MaxNodeInfos: 100,
			})
			relation := cmdb.Relation{V: []cmdb.Resource{"left", "right"}, RelationType: "left_to_right"}
			for i, id := range tc.ids {
				err := tg.AddTimeRelationWithRelation(context.Background(), relation, cmdb.Matcher{"id": id}, 100)
				if tc.wantError && i == len(tc.ids)-1 {
					requireGraphLimitError(t, err, "max_graph_edges", 3, 2)
					return
				}
				require.NoError(t, err)
			}
		})
	}
}

func TestTimeGraphNodeInfoLimitBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		timestamps []int64
		wantError  bool
	}{
		{name: "below_limit", timestamps: []int64{100}},
		{name: "at_limit", timestamps: []int64{100, 200}},
		{name: "duplicate_timestamp_does_not_count_twice", timestamps: []int64{100, 100, 200}},
		{name: "above_limit", timestamps: []int64{100, 200, 300}, wantError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tg := NewTimeGraphWithConfig(&TimeGraphConfig{
				Resource:     []TimeGraphResourceConfig{{Name: "node", Index: cmdb.Index{"id"}}},
				MaxNodes:     100,
				MaxNodeInfos: 2,
			})
			for i, timestamp := range tc.timestamps {
				err := tg.AddTimeNode(context.Background(), "node", cmdb.Matcher{"id": "n1"}, timestamp)
				if tc.wantError && i == len(tc.timestamps)-1 {
					requireGraphLimitError(t, err, "max_graph_node_infos", 3, 2)
					return
				}
				require.NoError(t, err)
			}
		})
	}
}

func TestTimeGraphResultLimitBoundaries(t *testing.T) {
	tests := []struct {
		name            string
		ids             []string
		wantError       bool
		wantResultCount int
	}{
		{name: "below_limit", ids: []string{"a"}, wantResultCount: 1},
		{name: "at_limit", ids: []string{"a", "b"}, wantResultCount: 2},
		{name: "duplicate_path_does_not_count_twice", ids: []string{"a", "a", "b"}, wantResultCount: 2},
		{name: "above_limit", ids: []string{"a", "b", "c"}, wantError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tg := NewTimeGraphWithConfig(&TimeGraphConfig{
				Resource: []TimeGraphResourceConfig{
					{Name: "left", Index: cmdb.Index{"id"}},
					{Name: "right", Index: cmdb.Index{"id"}},
				},
				MaxNodes:     100,
				MaxEdges:     100,
				MaxResults:   2,
				MaxNodeInfos: 100,
			})
			relation := cmdb.Relation{V: []cmdb.Resource{"left", "right"}, RelationType: "left_to_right"}
			for _, id := range tc.ids {
				require.NoError(t, tg.AddTimeRelationWithRelation(context.Background(), relation, cmdb.Matcher{"id": id}, 100))
			}
			results, err := tg.FindPathResources(
				context.Background(), "left", []cmdb.Resource{"right"}, nil,
				[][]cmdb.Resource{{"left", "right"}},
			)
			if tc.wantError {
				requireGraphLimitError(t, err, "max_graph_results", 3, 2)
				return
			}
			require.NoError(t, err)
			require.Len(t, results, tc.wantResultCount)
		})
	}
}

func TestTimeGraphRangePointLimitBoundaries(t *testing.T) {
	oldMaxRangePoints := MaxRangePoints
	MaxRangePoints = 2
	t.Cleanup(func() { MaxRangePoints = oldMaxRangePoints })

	tests := []struct {
		name      string
		start     string
		end       string
		wantError bool
	}{
		{name: "one_point_below_limit", start: "1700000000", end: "1700000000"},
		{name: "two_points_at_limit", start: "1700000000", end: "1700000060"},
		{name: "three_points_above_limit", start: "1700000000", end: "1700000120", wantError: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model := &Model{
				schemaProvider:          timeGraphTestSchemaProvider{},
				timeGraphQueryReference: timeGraphTestQueryReference,
				timeGraphVMQuery: func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error) {
					return nil, nil
				},
			}
			ctx := initTimeGraphQueryTestEnvironment()
			_, err := model.QueryPathResourcesRange(
				ctx, "10m", "space", "1m", tc.start, tc.end,
				"node", []cmdb.Resource{"system"}, [][]cmdb.Resource{{"node", "system"}}, cmdb.Matcher{"node": "n1"},
			)
			if tc.wantError {
				require.ErrorContains(t, err, "range query has more than 2 points")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestTimeGraphCancelledRequestStopsMutation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "node", Index: cmdb.Index{"id"}}}})
	if err := tg.AddTimeNode(ctx, "node", cmdb.Matcher{"id": "n1"}, 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancelled add to stop immediately, got %v", err)
	}
	if _, err := tg.FindRelationPathResources(ctx, "node", []cmdb.Resource{"node"}, nil, []cmdb.RelationPath{{Steps: []cmdb.RelationPathStep{{ResourceType: "node"}}}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancelled find to stop immediately, got %v", err)
	}
}

func TestTimeGraphCancelledDuringExternalQueryStopsMutation(t *testing.T) {
	ctx, cancel := context.WithCancel(initTimeGraphQueryTestEnvironment())
	defer cancel()
	model := &Model{
		schemaProvider:          timeGraphTestSchemaProvider{},
		timeGraphQueryReference: timeGraphTestQueryReference,
		timeGraphVMQuery: func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error) {
			cancel()
			return contractMatrix(map[string]string{"node": "n1", "ip": "10.0.0.1"}, 1700000000000), nil
		},
	}

	_, err := model.QueryPathResources(
		ctx, "10m", "space", "1700000000", "node", []cmdb.Resource{"system"},
		[][]cmdb.Resource{{"node", "system"}}, cmdb.Matcher{"node": "n1"},
	)
	require.ErrorIs(t, err, context.Canceled)
}
