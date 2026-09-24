package v1beta3

import (
	"context"
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

func TestTimeGraphPublicDirectedPathsDoNotMixOrientations(t *testing.T) {
	const timestampMS int64 = 1700000000000
	var paths []cmdb.RelationPath
	for _, direction := range []TraversalDirection{DirectionOutbound, DirectionInbound} {
		step := cmdb.RelationPathStep{ResourceType: "service", RelationType: "service_to_service", MetricName: "service_to_service_flow", Category: string(RelationCategoryDynamic), Direction: string(direction)}
		paths = append(paths, cmdb.RelationPath{Steps: []cmdb.RelationPathStep{{ResourceType: "service"}, step, step}})
	}
	for _, mode := range []string{"instant", "range"} {
		t.Run(mode, func(t *testing.T) {
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
			var results []cmdb.PathResourcesResult
			var err error
			if mode == "instant" {
				results, err = model.QueryRelationPathResources(ctx, "10m", "space", "1700000000", "service", []cmdb.Resource{"service"}, paths, cmdb.Matcher{"id": "a"})
			} else {
				results, err = model.QueryRelationPathResourcesRange(ctx, "10m", "space", "1m", "1700000000", "1700000060", "service", []cmdb.Resource{"service"}, paths, cmdb.Matcher{"id": "a"})
			}
			require.NoError(t, err)
			var want []cmdb.PathResourcesResult
			for _, timestamp := range timestamps {
				for _, ids := range [][]string{{"a", "b", "d"}, {"a", "e", "f"}} {
					result := cmdb.PathResourcesResult{Timestamp: timestamp, TargetType: "service"}
					for _, id := range ids {
						result.Path = append(result.Path, cmdb.PathNode{ResourceType: "service", Dimensions: cmdb.Matcher{"id": id}})
					}
					want = append(want, result)
				}
			}
			require.ElementsMatch(t, want, results)
		})
	}
}

func TestTimeGraphPublicExplicitSelfRelations(t *testing.T) {
	const timestampMS int64 = 1700000000000
	for _, tc := range []struct {
		name  string
		path  []cmdb.Resource
		edges [][2]string
		want  [][]string
	}{
		{"both-directions", []cmdb.Resource{"service", "service"}, [][2]string{{"a", "b"}, {"c", "a"}}, [][]string{{"a", "b"}, {"a", "c"}}},
		{"repeated-relation", []cmdb.Resource{"service", "service", "service"}, [][2]string{{"a", "b"}, {"b", "c"}}, [][]string{{"a", "b", "c"}}},
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
				var results []cmdb.PathResourcesResult
				var err error
				if mode == "instant" {
					results, err = model.QueryPathResources(ctx, "10m", "space", "1700000000", "service", []cmdb.Resource{"service"}, [][]cmdb.Resource{tc.path}, cmdb.Matcher{"id": "a"})
				} else {
					results, err = model.QueryPathResourcesRange(ctx, "10m", "space", "1m", "1700000000", "1700000060", "service", []cmdb.Resource{"service"}, [][]cmdb.Resource{tc.path}, cmdb.Matcher{"id": "a"})
				}
				require.NoError(t, err)
				require.Len(t, results, len(tc.want)*len(timestamps))
				for _, timestamp := range timestamps {
					var paths [][]string
					for _, result := range results {
						if result.Timestamp != timestamp {
							continue
						}
						var ids []string
						for _, node := range result.Path {
							ids = append(ids, node.Dimensions["id"])
						}
						paths = append(paths, ids)
					}
					require.ElementsMatch(t, tc.want, paths)
				}
				require.Len(t, vm.calls, 2, "query each direction once, including for repeated hops")
				if len(tc.path) > 2 {
					for _, call := range vm.calls {
						for _, field := range call.conditions.FieldList {
							require.Equal(t, structured.ConditionNotEqual, field.Operator, "later hops must not inherit the root filter")
						}
					}
				}
			})
		}
	}
}
