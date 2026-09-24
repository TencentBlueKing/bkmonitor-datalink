package v1beta3

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// Filtering at the VM boundary is essential: returning all fixtures would hide
// incorrectly pushed-down source matchers.
func filteredTimeGraphMatrix(t *testing.T, matrix pl.Matrix, conditions structured.Conditions) pl.Matrix {
	t.Helper()
	var result pl.Matrix
	for _, series := range matrix {
		matches := true
		for _, condition := range conditions.ConditionList {
			require.Equal(t, structured.ConditionAnd, condition)
		}
		for _, field := range conditions.FieldList {
			require.Len(t, field.Value, 1)
			equal := series.Metric.Get(field.DimensionName) == field.Value[0]
			switch field.Operator {
			case structured.ConditionEqual:
				matches = matches && equal
			case structured.ConditionNotEqual:
				matches = matches && !equal
			default:
				t.Fatalf("unexpected operator: %s", field.Operator)
			}
		}
		if matches {
			result = append(result, series)
		}
	}
	return result
}

func TestTimeGraphRandomizedPathsMatchIndependentEnumeration(t *testing.T) {
	ctx := initTimeGraphQueryTestEnvironment()
	const stamp int64 = 1700000000000
	type edge struct {
		from, to, relation string
		at                 int64
	}
	provider := contractSchemaProvider{
		resources: []ResourceType{"service"},
		primary:   map[ResourceType][]string{"service": {"id"}},
		fields:    map[ResourceType][]string{"service": {"id"}},
		schemas: []RelationSchema{
			{RelationType: "calls", Category: RelationCategoryDynamic, FromType: "service", ToType: "service", IsDirectional: true, MetricName: "calls_flow"},
			{RelationType: "depends", Category: RelationCategoryDynamic, FromType: "service", ToType: "service", IsDirectional: true, MetricName: "depends_flow"},
		},
	}
	for seed := int64(0); seed < 32; seed++ {
		rng := rand.New(rand.NewSource(seed))
		var edges []edge
		for _, at := range []int64{stamp, stamp + 60000, stamp + 120000} {
			for _, relation := range []string{"calls", "depends"} {
				for from := 0; from < 6; from++ {
					for to := 0; to < 6; to++ {
						if rng.Intn(5) == 0 {
							edges = append(edges, edge{fmt.Sprint(from), fmt.Sprint(to), relation, at})
						}
					}
				}
			}
		}
		for _, mode := range []string{"instant", "range"} {
			for plan := 0; plan < 8; plan++ {
				t.Run(fmt.Sprintf("seed-%d/%s/plan-%d", seed, mode, plan), func(t *testing.T) {
					hops := 1 + plan%3
					path := cmdb.RelationPath{Steps: []cmdb.RelationPathStep{{ResourceType: "service"}}}
					for hop := 0; hop < hops; hop++ {
						step := cmdb.RelationPathStep{ResourceType: "service"}
						if plan >= 3 {
							step.RelationType = []string{"calls", "depends"}[(plan+hop)%2]
							step.MetricName = step.RelationType + "_flow"
							step.Category = string(RelationCategoryDynamic)
							step.Direction = []string{string(DirectionOutbound), string(DirectionInbound), string(DirectionBoth)}[(plan+hop)%3]
						}
						path.Steps = append(path.Steps, step)
					}
					matrix := map[string]pl.Matrix{"calls_flow": {}, "depends_flow": {}}
					for _, e := range edges {
						if mode == "instant" && e.at != stamp {
							continue
						}
						name := e.relation + "_flow"
						matrix[name] = append(matrix[name], contractMatrix(map[string]string{"from_id": e.from, "to_id": e.to}, e.at)...)
					}
					model := &Model{schemaProvider: provider, timeGraphQueryReference: timeGraphTestQueryReference}
					model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
						query := q.QueryList[0]
						return filteredTimeGraphMatrix(t, matrix[query.FieldName], query.Conditions), nil
					}
					var got []cmdb.PathResourcesResult
					var err error
					if mode == "instant" {
						got, err = model.QueryRelationPathResources(ctx, "5m", "space", "1700000000", "service", []cmdb.Resource{"service"}, []cmdb.RelationPath{path}, cmdb.Matcher{"id": "0"})
					} else {
						got, err = model.QueryRelationPathResourcesRange(ctx, "5m", "space", "1m", "1700000000", "1700000120", "service", []cmdb.Resource{"service"}, []cmdb.RelationPath{path}, cmdb.Matcher{"id": "0"})
					}
					require.NoError(t, err)
					actual := make(map[string]bool)
					for _, result := range got {
						var ids []string
						for _, node := range result.Path {
							ids = append(ids, node.Dimensions["id"])
						}
						key := fmt.Sprintf("%d:%s", result.Timestamp, strings.Join(ids, "/"))
						require.False(t, actual[key], "duplicate result: %s", key)
						actual[key] = true
					}
					// A separate DFS over the input edges, without using production graph helpers.
					expected := make(map[string]bool)
					var visit func(int64, []string)
					visit = func(at int64, ids []string) {
						if len(ids) == len(path.Steps) {
							expected[fmt.Sprintf("%d:%s", at, strings.Join(ids, "/"))] = true
							return
						}
						step := path.Steps[len(ids)]
						current := ids[len(ids)-1]
						for _, e := range edges {
							if e.at != at || (step.RelationType != "" && step.RelationType != e.relation) {
								continue
							}
							var next string
							if step.Direction != string(DirectionInbound) && e.from == current {
								next = e.to
							} else if step.Direction != string(DirectionOutbound) && e.to == current {
								next = e.from
							}
							if next == "" {
								continue
							}
							seen := false
							for _, id := range ids {
								seen = seen || id == next
							}
							if !seen {
								visit(at, append(append([]string(nil), ids...), next))
							}
						}
					}
					visit(stamp, []string{"0"})
					if mode == "range" {
						visit(stamp+60000, []string{"0"})
						visit(stamp+120000, []string{"0"})
					}
					require.Equal(t, expected, actual)
				})
			}
		}
	}
}

func BenchmarkTimeGraphBranchingConvergence(b *testing.B) {
	for _, width := range []int{8, 32, 64, 96} {
		b.Run(fmt.Sprintf("width-%d", width), func(b *testing.B) {
			ctx := context.Background()
			cfg := &TimeGraphConfig{Resource: []TimeGraphResourceConfig{
				{Name: "root", Index: cmdb.Index{"root_id"}},
				{Name: "left", Index: cmdb.Index{"left_id"}},
				{Name: "right", Index: cmdb.Index{"right_id"}},
				{Name: "sink", Index: cmdb.Index{"sink_id"}},
			}}
			b.ReportAllocs()
			b.ReportMetric(float64(width*width), "paths/op")
			for n := 0; n < b.N; n++ {
				tg := NewTimeGraphWithConfig(cfg)
				for i := 0; i < width; i++ {
					if err := tg.AddTimeRelation(ctx, "root", "left", cmdb.Matcher{"root_id": "r", "left_id": fmt.Sprint(i)}, 100); err != nil {
						b.Fatal(err)
					}
					if err := tg.AddTimeRelation(ctx, "right", "sink", cmdb.Matcher{"right_id": fmt.Sprint(i), "sink_id": "s"}, 100); err != nil {
						b.Fatal(err)
					}
					for j := 0; j < width; j++ {
						if err := tg.AddTimeRelation(ctx, "left", "right", cmdb.Matcher{"left_id": fmt.Sprint(i), "right_id": fmt.Sprint(j)}, 100); err != nil {
							b.Fatal(err)
						}
					}
				}
				results, err := tg.FindPathResources(ctx, "root", []cmdb.Resource{"sink"}, cmdb.Matcher{"root_id": "r"}, [][]cmdb.Resource{{"root", "left", "right", "sink"}})
				if err != nil {
					b.Fatal(err)
				}
				if len(results) != width*width {
					b.Fatalf("want %d paths, got %d", width*width, len(results))
				}
				ids := make([]string, 0, len(results))
				for _, result := range results {
					ids = append(ids, result.Path[1].Dimensions["left_id"]+"/"+result.Path[2].Dimensions["right_id"])
				}
				sort.Strings(ids)
				for i := 1; i < len(ids); i++ {
					if ids[i] == ids[i-1] {
						b.Fatal("duplicate path")
					}
				}
				tg.Clean(ctx)
			}
		})
	}
}
