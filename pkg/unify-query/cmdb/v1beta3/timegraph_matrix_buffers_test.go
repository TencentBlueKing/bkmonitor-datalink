package v1beta3

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"
)

func TestMatrixWorkingBuffersMatchOwnedInputApplication(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	times := sharedGraphTestTimes(130)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	relation := sharedGraphTestRelation("calls")
	matrix := append(contractMatrix(map[string]string{"from_id": "a", "to_id": "b", "from_version": "old", "to_name": "first"}, times[0], times[64], times[129]), contractMatrix(map[string]string{"from_id": "c", "to_id": "d"}, times[128])...)
	matrix = append(matrix, contractMatrix(map[string]string{"from_id": "a", "to_id": "b", "from_version": "z"}, times[64])...)
	matrix = append(matrix, contractMatrix(map[string]string{"from_id": "zero", "to_id": "zero-target"})...)
	matrix = append(matrix, pl.Series{})
	before, err := json.Marshal(matrix)
	require.NoError(t, err)
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "shared"}[shared], func(t *testing.T) {
			newGraph := func() *TimeGraph {
				if shared {
					g, e := newSharedTimeGraph(sharedGraphTestConfig(), grid)
					require.NoError(t, e)
					return g
				}
				return NewTimeGraphWithConfig(sharedGraphTestConfig())
			}
			got, want := newGraph(), newGraph()
			ctx := withTimeGraphTargetInfoShow(context.Background(), true)
			gotMatchers := make(map[cmdb.Resource]map[string]cmdb.Matcher)
			gotIDs := make(map[int64]map[cmdb.Resource]map[string]struct{})
			wantMatchers := make(map[cmdb.Resource]map[string]cmdb.Matcher)
			wantIDs := make(map[int64]map[cmdb.Resource]map[string]struct{})
			require.NoError(t, got.applyRelationMatrix(ctx, matrix, relation, gotMatchers, gotIDs))
			for _, series := range matrix {
				info := make(cmdb.Matcher)
				for _, label := range series.Metric {
					info[label.Name] = label.Value
				}
				timestamps := make([]int64, len(series.Points))
				for k, point := range series.Points {
					timestamps[k] = point.T
				}
				require.NoError(t, want.AddTimeRelationWithRelation(ctx, relation, info, timestamps...))
				_, prefix := want.relationEndpointPrefixes(relation, relation.V[0], relation.V[1])
				target := want.relationEndpointInfo(info, relation.V[1], prefix, true)
				if len(target) == 0 {
					target = info
				}
				key := want.primaryMatcherKey(relation.V[1], target)
				if key == "" {
					continue
				}
				if wantMatchers[relation.V[1]] == nil {
					wantMatchers[relation.V[1]] = make(map[string]cmdb.Matcher)
				}
				wantMatchers[relation.V[1]][key] = want.primaryMatcher(relation.V[1], target)
				for _, point := range series.Points {
					if wantIDs[point.T] == nil {
						wantIDs[point.T] = make(map[cmdb.Resource]map[string]struct{})
					}
					if wantIDs[point.T][relation.V[1]] == nil {
						wantIDs[point.T][relation.V[1]] = make(map[string]struct{})
					}
					wantIDs[point.T][relation.V[1]][key] = struct{}{}
				}
			}
			require.Equal(t, wantMatchers, gotMatchers)
			require.Equal(t, wantIDs, gotIDs)
			query := SharedTopologyQuery{SourceType: "entity", MaxHops: 3}
			wantSnapshots, err := want.FindSharedTopology(context.Background(), grid, query)
			require.NoError(t, err)
			gotSnapshots, err := got.FindSharedTopology(context.Background(), grid, query)
			require.NoError(t, err)
			require.Equal(t, wantSnapshots, gotSnapshots)
			after, err := json.Marshal(matrix)
			require.NoError(t, err)
			require.Equal(t, before, after)
			// A missing endpoint must not inherit the previous series' primary label.
			invalid := append(contractMatrix(map[string]string{"from_id": "x", "to_id": "y"}, times[0]), contractMatrix(map[string]string{"from_id": "x"}, times[64])...)
			require.Error(t, newGraph().applyRelationMatrix(context.Background(), invalid, relation, nil, nil))
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			require.ErrorIs(t, newGraph().applyRelationMatrix(canceled, matrix, relation, nil, nil), context.Canceled)
		})
	}
}

func TestSourceAndTargetMatrixWorkingBuffersPreserveTimeAttributes(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	times := sharedGraphTestTimes(130)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	matrix := append(contractMatrix(map[string]string{"id": "a", "version": "old", "name": "present"}, times[0], times[64], times[129]), contractMatrix(map[string]string{"id": "b"}, times[128])...)
	matrix = append(matrix, contractMatrix(map[string]string{"id": "a", "version": "z"}, times[64])...)
	matrix = append(matrix, contractMatrix(map[string]string{"id": "empty"})...)
	for _, target := range []bool{false, true} {
		for _, shared := range []bool{false, true} {
			t.Run(map[bool]string{false: "source", true: "target"}[target]+map[bool]string{false: "-legacy", true: "-shared"}[shared], func(t *testing.T) {
				newGraph := func() *TimeGraph {
					if shared {
						g, e := newSharedTimeGraph(sharedGraphTestConfig(), grid)
						require.NoError(t, e)
						return g
					}
					return NewTimeGraphWithConfig(sharedGraphTestConfig())
				}
				got, want := newGraph(), newGraph()
				ctx := context.Background()
				ids := make(map[int64]map[cmdb.Resource]map[string]struct{})
				for _, series := range matrix {
					info := make(cmdb.Matcher)
					for _, label := range series.Metric {
						info[label.Name] = label.Value
					}
					key := want.primaryMatcherKey("entity", info)
					for _, point := range series.Points {
						if point.T == times[129] {
							continue
						}
						if ids[point.T] == nil {
							ids[point.T] = map[cmdb.Resource]map[string]struct{}{"entity": {}}
						}
						ids[point.T]["entity"][key] = struct{}{}
					}
				}
				if target {
					require.NoError(t, got.applyTargetMatrix(ctx, matrix, "entity", ids))
				} else {
					require.NoError(t, got.applySourceMatrix(ctx, matrix, "entity"))
				}
				for _, series := range matrix {
					info := make(cmdb.Matcher)
					for _, label := range series.Metric {
						info[label.Name] = label.Value
					}
					timestamps := make([]int64, 0, len(series.Points))
					key := want.primaryMatcherKey("entity", info)
					for _, point := range series.Points {
						if _, ok := ids[point.T]["entity"][key]; !target || ok {
							timestamps = append(timestamps, point.T)
						}
					}
					require.NoError(t, want.AddTimeNode(ctx, "entity", info, timestamps...))
				}
				query := SharedTopologyQuery{SourceType: "entity", MaxHops: 0}
				wantSnapshots, err := want.FindSharedTopology(ctx, grid, query)
				require.NoError(t, err)
				gotSnapshots, err := got.FindSharedTopology(ctx, grid, query)
				require.NoError(t, err)
				require.Equal(t, wantSnapshots, gotSnapshots)
			})
		}
	}
}
