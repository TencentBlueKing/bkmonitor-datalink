package v1beta3

import (
	"context"
	"fmt"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestTimeGraphRootRelationsExcludeLaterHopsAcrossPaths(t *testing.T) {
	model := &Model{schemaProvider: publicDynamicSelfProvider()}
	outbound := cmdb.RelationPathStep{ResourceType: "service", Direction: string(DirectionOutbound)}
	inbound := cmdb.RelationPathStep{ResourceType: "service", Direction: string(DirectionInbound)}
	first := cmdb.RelationPathStep{ResourceType: "service"}
	paths := []cmdb.RelationPath{
		{Steps: []cmdb.RelationPathStep{first, outbound}},
		{Steps: []cmdb.RelationPathStep{first, inbound, outbound}},
	}
	for _, ordered := range [][]cmdb.RelationPath{paths, {paths[1], paths[0]}} {
		roots := model.rootTimeGraphRelationKeys("space", "service", ordered)
		require.Len(t, roots, 1)
		for key := range roots {
			require.Equal(t, string(DirectionInbound), key.direction)
		}
	}
}

// 有向自关联的出向、入向计划各自只走本方向的边。多跳自关联会回访资源类型，
// 逐层集合无法表达“不回到更早实例”，必须在取数前具名拒绝，而不是混用方向或改变答案。
func TestTimeGraphPublicDirectedPathsDoNotMixOrientations(t *testing.T) {
	const timestampMS int64 = 1700000000000
	for _, hops := range []int{1, 2} {
		var paths []cmdb.RelationPath
		for _, direction := range []TraversalDirection{DirectionOutbound, DirectionInbound} {
			step := cmdb.RelationPathStep{ResourceType: "service", RelationType: "service_to_service", MetricName: "service_to_service_flow", Category: string(RelationCategoryDynamic), Direction: string(direction)}
			path := cmdb.RelationPath{Steps: []cmdb.RelationPathStep{{ResourceType: "service"}}}
			for hop := 0; hop < hops; hop++ {
				path.Steps = append(path.Steps, step)
			}
			paths = append(paths, path)
		}
		for _, mode := range []string{"instant", "range"} {
			t.Run(fmt.Sprintf("%d-hop/%s", hops, mode), func(t *testing.T) {
				timestamps := []int64{timestampMS}
				if mode == "range" {
					timestamps = append(timestamps, timestampMS+60000)
				}
				var matrix pl.Matrix
				for _, edge := range [][2]string{{"a", "b"}, {"c", "b"}, {"b", "d"}, {"e", "a"}, {"f", "e"}} {
					matrix = append(matrix, contractMatrix(map[string]string{"from_id": edge[0], "to_id": edge[1]}, timestamps...)...)
				}
				vm := &publicTimeGraphVM{responses: map[string]pl.Matrix{"service_to_service_flow": matrix}}
				model := &Model{schemaProvider: publicDynamicSelfProvider(), timeGraphVMQuery: vm.query, timeGraphQueryReference: timeGraphTestQueryReference}
				ctx := initTimeGraphQueryTestEnvironment()
				var results []RelationTarget
				var err error
				if mode == "instant" {
					results, err = model.QueryRelationTimeGraph(
						ctx, "10m", "space",
						time.Unix(1700000000, 0), time.Unix(1700000000, 0), 5*time.Minute,
						"service", []cmdb.Resource{"service"},
						paths,
						cmdb.Matcher{"id": "a"}, nil,
					)
				} else {
					results, err = model.QueryRelationTimeGraph(
						ctx, "10m", "space",
						time.Unix(1700000000, 0), time.Unix(1700000060, 0), time.Minute,
						"service", []cmdb.Resource{"service"},
						paths,
						cmdb.Matcher{"id": "a"}, nil,
					)
				}
				if hops > 1 {
					require.ErrorContains(t, err, "relation path revisits resource type service")
					require.Empty(t, results)
					require.Empty(t, vm.calls, "rejected paths must not query the backend")
					return
				}
				require.NoError(t, err)
				// 出向只到 b，入向只到 e；c、d、f 只能经错误方向或第二跳到达。
				requireRelationTargets(t, serviceRelationTargets(timestamps, "b", "e"), results)
			})
		}
	}
}

func TestTimeGraphPublicExplicitSelfRelations(t *testing.T) {
	const timestampMS int64 = 1700000000000
	for _, tc := range []struct {
		name    string
		path    []cmdb.Resource
		edges   [][2]string
		want    []string
		wantErr bool
	}{
		{name: "both-directions", path: []cmdb.Resource{"service", "service"}, edges: [][2]string{{"a", "b"}, {"c", "a"}}, want: []string{"b", "c"}},
		{name: "real-self-loop", path: []cmdb.Resource{"service", "service"}, edges: [][2]string{{"a", "a"}}, want: []string{"a"}},
		{name: "self-loop-and-neighbor", path: []cmdb.Resource{"service", "service"}, edges: [][2]string{{"a", "a"}, {"a", "b"}}, want: []string{"a", "b"}},
		{name: "no-zero-hop-match", path: []cmdb.Resource{"service", "service"}},
		// 多跳自关联回访资源类型，逐层集合不能表达旧路径的“不回溯”约束，取数前拒绝。
		{name: "repeated-relation", path: []cmdb.Resource{"service", "service", "service"}, edges: [][2]string{{"a", "b"}, {"b", "c"}}, wantErr: true},
		{name: "repeated-self-loop", path: []cmdb.Resource{"service", "service", "service"}, edges: [][2]string{{"a", "a"}}, wantErr: true},
		{name: "no-backtracking-cycle", path: []cmdb.Resource{"service", "service", "service"}, edges: [][2]string{{"a", "b"}}, wantErr: true},
	} {
		for _, mode := range []string{"instant", "range"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				matrix := make(pl.Matrix, 0, len(tc.edges))
				timestamps := []int64{timestampMS}
				if mode == "range" {
					timestamps = append(timestamps, timestampMS+60000)
				}
				for _, edge := range tc.edges {
					matrix = append(matrix, contractMatrix(map[string]string{"from_id": edge[0], "to_id": edge[1]}, timestamps...)...)
				}
				vm := &publicTimeGraphVM{responses: map[string]pl.Matrix{"service_to_service_flow": matrix}}
				model := &Model{schemaProvider: publicDynamicSelfProvider(), timeGraphQueryReference: timeGraphTestQueryReference}
				model.timeGraphVMQuery = func(ctx context.Context, queryTs *structured.QueryTs, expr string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
					response, err := vm.query(ctx, queryTs, expr, instant, start, end, step)
					if err != nil {
						return nil, err
					}
					// Apply the generated filters like VM does; an unconditional mock hides missing edges.
					var filtered pl.Matrix
					for _, series := range response {
						matches := true
						for _, field := range queryTs.QueryList[0].Conditions.FieldList {
							require.Len(t, field.Value, 1)
							equal := series.Metric.Get(field.DimensionName) == field.Value[0]
							switch field.Operator {
							case structured.ConditionEqual:
								matches = matches && equal
							case structured.ConditionNotEqual:
								matches = matches && !equal
							default:
								t.Fatalf("unexpected operator %q", field.Operator)
							}
						}
						if matches {
							filtered = append(filtered, series)
						}
					}
					return filtered, nil
				}
				ctx := initTimeGraphQueryTestEnvironment()
				var results []RelationTarget
				var err error
				if mode == "instant" {
					results, err = model.QueryRelationTimeGraph(
						ctx, "10m", "space",
						time.Unix(1700000000, 0), time.Unix(1700000000, 0), 5*time.Minute,
						"service", []cmdb.Resource{"service"},
						cmdb.RelationPathsFromResourcePaths([][]cmdb.Resource{tc.path}),
						cmdb.Matcher{"id": "a"}, nil,
					)
				} else {
					results, err = model.QueryRelationTimeGraph(
						ctx, "10m", "space",
						time.Unix(1700000000, 0), time.Unix(1700000060, 0), time.Minute,
						"service", []cmdb.Resource{"service"},
						cmdb.RelationPathsFromResourcePaths([][]cmdb.Resource{tc.path}),
						cmdb.Matcher{"id": "a"}, nil,
					)
				}
				if tc.wantErr {
					require.ErrorContains(t, err, "relation path revisits resource type service")
					require.Empty(t, results)
					require.Empty(t, vm.calls, "rejected paths must not query the backend")
					return
				}
				require.NoError(t, err)
				requireRelationTargets(t, serviceRelationTargets(timestamps, tc.want...), results)
				require.Len(t, vm.calls, 2, "query each direction once")
			})
		}
	}
}

// serviceRelationTargets 按 FindRelationTargets 的顺序生成各时刻的 service 目标。
func serviceRelationTargets(timestamps []int64, ids ...string) []RelationTarget {
	var targets []RelationTarget
	for _, timestamp := range timestamps {
		for _, id := range ids {
			targets = append(targets, RelationTarget{Timestamp: timestamp, TargetType: "service", Target: cmdb.PathNode{ResourceType: "service", Dimensions: cmdb.Matcher{"id": id}}})
		}
	}
	return sortedRelationTargets(targets)
}
