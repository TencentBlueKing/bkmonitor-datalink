package v1beta3

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// 图规模不设上限：节点、边和节点时间点超过旧的默认计数上限（节点 100000、边 200000、
// 节点时间点 1000000）时照常写入，重复观测不重复计数。数量取自旧默认值加一，旧实现在这些
// 规模上会拒绝。
const (
	formerMaxGraphNodes     = 100000
	formerMaxGraphEdges     = 200000
	formerMaxGraphNodeInfos = 1000000
)

func TestTimeGraphNodesHaveNoCountCap(t *testing.T) {
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "node", Index: cmdb.Index{"id"}}}})
	for i := 0; i <= formerMaxGraphNodes; i++ {
		require.NoError(t, tg.AddTimeNode(context.Background(), "node", cmdb.Matcher{"id": strconv.Itoa(i)}, 100))
	}
	require.NoError(t, tg.AddTimeNode(context.Background(), "node", cmdb.Matcher{"id": "0"}, 100))
	require.Equal(t, formerMaxGraphNodes+1, tg.nodeBuilder.Length())
}

func TestTimeGraphRelationEntryNodesHaveNoCountCap(t *testing.T) {
	grid, err := NewTopologyGrid([]int64{100})
	require.NoError(t, err)
	tg := newSharedTimeGraph(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "service", Index: cmdb.Index{"id"}}}}, grid)
	relation := cmdb.Relation{
		V:            []cmdb.Resource{"service", "service"},
		RelationType: "service_to_service",
		MetricName:   "service_to_service_flow",
		Category:     string(RelationCategoryDynamic),
		Direction:    string(DirectionOutbound),
	}
	for i := 0; i < formerMaxGraphNodes; i++ {
		require.NoError(t, tg.AddTimeRelationWithRelation(context.Background(), relation, cmdb.Matcher{"from_id": "a", "to_id": strconv.Itoa(i)}, 100))
	}
	require.NoError(t, tg.AddTimeRelationWithRelation(context.Background(), relation, cmdb.Matcher{"from_id": "a", "to_id": "0"}, 100))
	require.Equal(t, formerMaxGraphNodes+1, tg.nodeBuilder.Length())
}

func TestTimeGraphEdgesHaveNoCountCap(t *testing.T) {
	grid, err := NewTopologyGrid([]int64{100})
	require.NoError(t, err)
	tg := newSharedTimeGraph(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "left", Index: cmdb.Index{"id"}},
		{Name: "right", Index: cmdb.Index{"id"}},
	}}, grid)
	relation := cmdb.Relation{V: []cmdb.Resource{"left", "right"}, RelationType: "left_to_right"}
	for i := 0; i <= formerMaxGraphEdges; i++ {
		require.NoError(t, tg.AddTimeRelationWithRelation(context.Background(), relation, cmdb.Matcher{"id": strconv.Itoa(i)}, 100))
	}
	require.NoError(t, tg.AddTimeRelationWithRelation(context.Background(), relation, cmdb.Matcher{"id": "0"}, 100))
	require.Equal(t, formerMaxGraphEdges+1, tg.edgeCount)
}

func TestTimeGraphNodeInfosHaveNoCountCap(t *testing.T) {
	timestamps := sharedTopologyTestTimes(60)
	grid, err := NewTopologyGrid(timestamps)
	require.NoError(t, err)
	tg := newSharedTimeGraph(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "node", Index: cmdb.Index{"id"}}}}, grid)
	nodes := formerMaxGraphNodeInfos/len(timestamps) + 1
	for i := 0; i < nodes; i++ {
		require.NoError(t, tg.AddTimeNode(context.Background(), "node", cmdb.Matcher{"id": strconv.Itoa(i)}, timestamps...))
	}
	require.NoError(t, tg.AddTimeNode(context.Background(), "node", cmdb.Matcher{"id": "0"}, timestamps...))
	require.Greater(t, tg.nodeInfoCount, formerMaxGraphNodeInfos)
	require.Equal(t, nodes*len(timestamps), tg.nodeInfoCount)
}

// FindRelationTargets 没有结果上限。
func TestTimeGraphRelationTargetCountBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		ids         []string
		wantTargets []string
	}{
		{name: "one_target", ids: []string{"a"}, wantTargets: []string{"a"}},
		{name: "two_targets", ids: []string{"a", "b"}, wantTargets: []string{"a", "b"}},
		{name: "duplicate_relation_yields_one_target", ids: []string{"a", "a", "b"}, wantTargets: []string{"a", "b"}},
		{name: "third_target_has_no_result_limit", ids: []string{"a", "b", "c"}, wantTargets: []string{"a", "b", "c"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tg := NewTimeGraphWithConfig(&TimeGraphConfig{
				Resource: []TimeGraphResourceConfig{
					{Name: "left", Index: cmdb.Index{"id"}},
					{Name: "right", Index: cmdb.Index{"id"}},
				},
			})
			relation := cmdb.Relation{V: []cmdb.Resource{"left", "right"}, RelationType: "left_to_right"}
			for _, id := range tc.ids {
				require.NoError(t, tg.AddTimeRelationWithRelation(context.Background(), relation, cmdb.Matcher{"id": id}, 100))
			}
			results, err := tg.FindTargets(
				context.Background(), "left", []cmdb.Resource{"right"}, nil,
				[][]cmdb.Resource{{"left", "right"}},
			)
			require.NoError(t, err)
			want := make([]RelationTarget, 0, len(tc.wantTargets))
			for _, id := range tc.wantTargets {
				want = append(want, RelationTarget{Timestamp: 100, TargetType: "right", Target: cmdb.PathNode{ResourceType: "right", Dimensions: cmdb.Matcher{"id": id}}})
			}
			require.Equal(t, want, results)
		})
	}
}

// 兼容入口不按目标数截断：每帧超过旧的 5000 个目标时照常全部返回。
func TestLegacyCompatReturnsAllTargetsPerFrame(t *testing.T) {
	const targets = 5001
	start := time.Unix(1700000040, 0)
	middles := make([]string, targets)
	for i := range middles {
		middles[i] = fmt.Sprintf("m%04d", i)
	}
	want := make(cmdb.Matchers, 0, targets)
	for _, middle := range middles {
		want = append(want, cmdb.Matcher{"middle_id": middle})
	}
	sort.SliceStable(want, func(i, j int) bool { return fmt.Sprint(want[i]) < fmt.Sprint(want[j]) })
	for _, mode := range []string{"instant", "range"} {
		t.Run(mode, func(t *testing.T) {
			model := legacySegmentModel()
			model.timeGraphVMQuery = func(_ context.Context, query *structured.QueryTs, _ string, _ bool, queryStart, queryEnd time.Time, step time.Duration) (pl.Matrix, error) {
				var timestamps []int64
				for at := queryStart; !at.After(queryEnd); at = at.Add(step) {
					timestamps = append(timestamps, at.UnixMilli())
				}
				matrix := make(pl.Matrix, 0, len(middles))
				for _, middle := range middles {
					matrix = append(matrix, contractMatrix(map[string]string{"source_id": "a", "middle_id": middle}, timestamps...)...)
				}
				return filterTopologyRegressionMatrix(query, matrix), nil
			}
			ctx := initTimeGraphQueryTestEnvironment()
			if mode == "instant" {
				_, _, _, _, matchers, err := model.QueryResourceMatcher(ctx, "10m", "space", strconv.FormatInt(start.Unix(), 10), "middle", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
				require.NoError(t, err)
				require.Equal(t, want, matchers)
				return
			}
			end := start.Add(time.Minute)
			_, _, _, _, buckets, err := model.QueryResourceMatcherRange(ctx, "", "space", "1m", strconv.FormatInt(start.Unix(), 10), strconv.FormatInt(end.Unix(), 10), "middle", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
			require.NoError(t, err)
			require.Equal(t, []cmdb.MatchersWithTimestamp{{Timestamp: start.UnixMilli(), Matchers: want}, {Timestamp: end.UnixMilli(), Matchers: want}}, buckets)
		})
	}
}

func TestTimeGraphRangePointLimitBoundaries(t *testing.T) {
	oldMaxRangePoints := MaxRangePoints
	MaxRangePoints = 2
	t.Cleanup(func() { MaxRangePoints = oldMaxRangePoints })
	for _, scenario := range []struct {
		name, start, end, wantErr string
	}{
		{name: "one_point_below_limit", start: "1700000000", end: "1700000000"},
		{name: "two_points_at_limit", start: "1700000000", end: "1700000060"},
		{name: "three_points_above_limit", start: "1700000000", end: "1700000120", wantErr: "range query has more than 2 points"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			model := legacyRegressionModel(timeGraphTestSchemaProvider{})
			model.timeGraphVMQuery = func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error) {
				return nil, nil
			}
			_, _, _, _, _, err := model.QueryResourceMatcherRange(
				initTimeGraphQueryTestEnvironment(), "10m", "space", "1m", scenario.start, scenario.end,
				"system", "node", cmdb.Matcher{"node": "n1"}, nil, false, nil,
			)
			if scenario.wantErr != "" {
				require.ErrorContains(t, err, scenario.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestTimeGraphRangeQueryHonorsCancellableBudget(t *testing.T) {
	const budget = 10 * time.Millisecond
	oldTimeout := timeGraphQueryTimeout
	timeGraphQueryTimeout = budget
	t.Cleanup(func() { timeGraphQueryTimeout = oldTimeout })
	deadlines := make(chan time.Time, 1)
	model := legacyRegressionModel(timeGraphTestSchemaProvider{})
	model.timeGraphVMQuery = func(ctx context.Context, _ *structured.QueryTs, _ string, _ bool, _ time.Time, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			return nil, errors.New("timegraph query has no deadline")
		}
		deadlines <- deadline
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(initTimeGraphQueryTestEnvironment())
	t.Cleanup(cancel)
	result := make(chan error, 1)
	started := time.Now()
	go func() {
		_, _, _, _, _, err := model.QueryResourceMatcherRange(
			ctx, "10m", "space", "1m", "1700000000", "1700000060",
			"system", "node", cmdb.Matcher{"node": "n1"}, nil, false, nil,
		)
		result <- err
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
}

func TestTimeGraphCancelledRequestStopsMutation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "node", Index: cmdb.Index{"id"}}}})
	if err := tg.AddTimeNode(ctx, "node", cmdb.Matcher{"id": "n1"}, 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancelled add to stop immediately, got %v", err)
	}
	if _, err := tg.FindRelationTargets(ctx, "node", []cmdb.Resource{"node"}, nil, []cmdb.RelationPath{{Steps: []cmdb.RelationPathStep{{ResourceType: "node"}}}}); !errors.Is(err, context.Canceled) {
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

	_, _, _, _, _, err := model.QueryResourceMatcher(
		ctx, "10m", "space", "1700000000", "system", "node",
		cmdb.Matcher{"node": "n1"}, nil, false, []cmdb.Resource{"node", "system"},
	)
	require.ErrorIs(t, err, context.Canceled)
}
