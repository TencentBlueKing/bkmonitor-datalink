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

func requireGraphLimitError(t *testing.T, err error, want *ResultLimitError) {
	t.Helper()
	var limitErr *ResultLimitError
	require.ErrorAs(t, err, &limitErr)
	require.Equal(t, want, limitErr)
}

func TestTimeGraphNodeLimitBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		ids     []string
		wantErr *ResultLimitError
	}{
		{name: "below_limit", ids: []string{"n1"}},
		{name: "at_limit", ids: []string{"n1", "n2"}},
		{name: "duplicate_node_does_not_count_twice", ids: []string{"n1", "n1", "n2"}},
		{name: "third_unique_node_exceeds_limit", ids: []string{"n1", "n2", "n3"}, wantErr: &ResultLimitError{Reason: "max_graph_nodes", Count: 3, Limit: 2}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tg := NewTimeGraphWithConfig(&TimeGraphConfig{
				Resource:     []TimeGraphResourceConfig{{Name: "node", Index: cmdb.Index{"id"}}},
				MaxNodes:     2,
				MaxNodeInfos: 100,
			})
			var err error
			for _, id := range tc.ids {
				err = tg.AddTimeNode(context.Background(), "node", cmdb.Matcher{"id": id}, 100)
				if err != nil {
					break
				}
			}
			if tc.wantErr != nil {
				requireGraphLimitError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestTimeGraphRelationEntryNodeLimitBoundaries(t *testing.T) {
	tests := []struct {
		name          string
		relations     []cmdb.Matcher
		wantNodeCount int
		wantErr       *ResultLimitError
	}{
		{name: "one_edge_creates_two_nodes", relations: []cmdb.Matcher{{"from_id": "a", "to_id": "b"}}, wantNodeCount: 2},
		{name: "two_edges_create_three_unique_nodes", relations: []cmdb.Matcher{{"from_id": "a", "to_id": "b"}, {"from_id": "a", "to_id": "c"}}, wantNodeCount: 3},
		{name: "third_target_exceeds_node_limit", relations: []cmdb.Matcher{{"from_id": "a", "to_id": "b"}, {"from_id": "a", "to_id": "c"}, {"from_id": "a", "to_id": "d"}}, wantErr: &ResultLimitError{Reason: "max_graph_nodes", Count: 4, Limit: 3}},
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
			var err error
			for _, info := range tc.relations {
				err = tg.AddTimeRelationWithRelation(context.Background(), relation, info, 100)
				if err != nil {
					break
				}
			}
			if tc.wantErr != nil {
				requireGraphLimitError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantNodeCount, tg.nodeBuilder.Length())
		})
	}
}

func TestTimeGraphEdgeLimitBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		ids     []string
		wantErr *ResultLimitError
	}{
		{name: "below_limit", ids: []string{"a"}},
		{name: "at_limit", ids: []string{"a", "b"}},
		{name: "duplicate_edge_does_not_count_twice", ids: []string{"a", "a", "b"}},
		{name: "third_unique_edge_exceeds_limit", ids: []string{"a", "b", "c"}, wantErr: &ResultLimitError{Reason: "max_graph_edges", Count: 3, Limit: 2}},
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
			var err error
			for _, id := range tc.ids {
				err = tg.AddTimeRelationWithRelation(context.Background(), relation, cmdb.Matcher{"id": id}, 100)
				if err != nil {
					break
				}
			}
			if tc.wantErr != nil {
				requireGraphLimitError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestTimeGraphNodeInfoLimitBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		timestamps []int64
		wantErr    *ResultLimitError
	}{
		{name: "below_limit", timestamps: []int64{100}},
		{name: "at_limit", timestamps: []int64{100, 200}},
		{name: "duplicate_timestamp_does_not_count_twice", timestamps: []int64{100, 100, 200}},
		{name: "third_timestamp_exceeds_limit", timestamps: []int64{100, 200, 300}, wantErr: &ResultLimitError{Reason: "max_graph_node_infos", Count: 3, Limit: 2}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tg := NewTimeGraphWithConfig(&TimeGraphConfig{
				Resource:     []TimeGraphResourceConfig{{Name: "node", Index: cmdb.Index{"id"}}},
				MaxNodes:     100,
				MaxNodeInfos: 2,
			})
			var err error
			for _, timestamp := range tc.timestamps {
				err = tg.AddTimeNode(context.Background(), "node", cmdb.Matcher{"id": "n1"}, timestamp)
				if err != nil {
					break
				}
			}
			if tc.wantErr != nil {
				requireGraphLimitError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestTimeGraphResultLimitBoundaries(t *testing.T) {
	tests := []struct {
		name            string
		ids             []string
		wantErr         *ResultLimitError
		wantResultCount int
	}{
		{name: "below_limit", ids: []string{"a"}, wantResultCount: 1},
		{name: "at_limit", ids: []string{"a", "b"}, wantResultCount: 2},
		{name: "duplicate_path_does_not_count_twice", ids: []string{"a", "a", "b"}, wantResultCount: 2},
		{name: "third_path_exceeds_limit", ids: []string{"a", "b", "c"}, wantErr: &ResultLimitError{Reason: "max_graph_results", Count: 3, Limit: 2}},
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
			if tc.wantErr != nil {
				requireGraphLimitError(t, err, tc.wantErr)
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

	scenarios := []struct {
		name    string
		start   string
		end     string
		wantErr string
	}{
		{name: "one_point_below_limit", start: "1700000000", end: "1700000000"},
		{name: "two_points_at_limit", start: "1700000000", end: "1700000060"},
		{name: "three_points_above_limit", start: "1700000000", end: "1700000120", wantErr: "range query has more than 2 points"},
	}
	entries := []struct {
		name  string
		query func(*Model, context.Context, string, string) error
	}{
		{
			name: "resource_path",
			query: func(model *Model, ctx context.Context, start, end string) error {
				_, err := model.QueryPathResourcesRange(
					ctx, "10m", "space", "1m", start, end,
					"node", []cmdb.Resource{"system"}, [][]cmdb.Resource{{"node", "system"}}, cmdb.Matcher{"node": "n1"},
				)
				return err
			},
		},
		{
			name: "relation_path",
			query: func(model *Model, ctx context.Context, start, end string) error {
				_, err := model.QueryRelationPathResourcesRange(
					ctx, "10m", "space", "1m", start, end,
					"node", []cmdb.Resource{"system"}, []cmdb.RelationPath{{
						Steps: []cmdb.RelationPathStep{{ResourceType: "node"}, {ResourceType: "system"}},
					}}, cmdb.Matcher{"node": "n1"},
				)
				return err
			},
		},
	}
	for _, entry := range entries {
		for _, scenario := range scenarios {
			t.Run(entry.name+"/"+scenario.name, func(t *testing.T) {
				model := &Model{
					schemaProvider:          timeGraphTestSchemaProvider{},
					timeGraphQueryReference: timeGraphTestQueryReference,
					timeGraphVMQuery: func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error) {
						return nil, nil
					},
				}
				err := entry.query(model, initTimeGraphQueryTestEnvironment(), scenario.start, scenario.end)
				if scenario.wantErr != "" {
					require.ErrorContains(t, err, scenario.wantErr)
					return
				}
				require.NoError(t, err)
			})
		}
	}
}

func TestTimeGraphRangeQueryHonorsCancellableBudget(t *testing.T) {
	const budget = 10 * time.Millisecond
	oldTimeout := timeGraphQueryTimeout
	timeGraphQueryTimeout = budget
	t.Cleanup(func() { timeGraphQueryTimeout = oldTimeout })

	tests := []struct {
		name  string
		query func(*Model, context.Context) error
	}{
		{
			name: "path_entrypoint",
			query: func(model *Model, ctx context.Context) error {
				_, err := model.QueryPathResourcesRange(
					ctx, "10m", "space", "1m", "1700000000", "1700000060",
					"node", []cmdb.Resource{"system"}, [][]cmdb.Resource{{"node", "system"}}, cmdb.Matcher{"node": "n1"},
				)
				return err
			},
		},
		{
			name: "relation_entrypoint",
			query: func(model *Model, ctx context.Context) error {
				_, err := model.QueryRelationPathResourcesRange(
					ctx, "10m", "space", "1m", "1700000000", "1700000060",
					"node", []cmdb.Resource{"system"}, []cmdb.RelationPath{{
						Steps: []cmdb.RelationPathStep{{ResourceType: "node"}, {ResourceType: "system"}},
					}}, cmdb.Matcher{"node": "n1"},
				)
				return err
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deadlines := make(chan time.Time, 1)
			model := &Model{
				schemaProvider:          timeGraphTestSchemaProvider{},
				timeGraphQueryReference: timeGraphTestQueryReference,
				timeGraphVMQuery: func(ctx context.Context, _ *structured.QueryTs, _ string, _ bool, _ time.Time, _ time.Time, _ time.Duration) (pl.Matrix, error) {
					deadline, ok := ctx.Deadline()
					if !ok {
						return nil, errors.New("timegraph query has no deadline")
					}
					deadlines <- deadline
					<-ctx.Done()
					return nil, ctx.Err()
				},
			}
			ctx, cancel := context.WithCancel(initTimeGraphQueryTestEnvironment())
			t.Cleanup(cancel)
			result := make(chan error, 1)
			started := time.Now()
			go func() {
				result <- tc.query(model, ctx)
			}()

			select {
			case deadline := <-deadlines:
				require.True(t, deadline.After(started))
				require.LessOrEqual(t, deadline.Sub(started), budget+100*time.Millisecond)
			case <-time.After(time.Second):
				t.Fatal("range query did not reach VM mock")
			}
			select {
			case err := <-result:
				require.ErrorIs(t, err, context.DeadlineExceeded)
			case <-time.After(time.Second):
				t.Fatal("range query ignored its cancellable budget")
			}
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
			return contractMatrix(map[string]string{"node": "n1", "ip": "test-ip-1"}, 1700000000000), nil
		},
	}

	_, err := model.QueryPathResources(
		ctx, "10m", "space", "1700000000", "node", []cmdb.Resource{"system"},
		[][]cmdb.Resource{{"node", "system"}}, cmdb.Matcher{"node": "n1"},
	)
	require.ErrorIs(t, err, context.Canceled)
}
