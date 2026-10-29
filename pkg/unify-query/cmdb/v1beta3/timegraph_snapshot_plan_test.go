package v1beta3

import (
	"context"
	"fmt"
	"testing"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

// Expand the exported wire mask using literal bit tests, independently of the
// graph's bitmap and export helpers. Preserve nil versus empty dimensions.
func expandSnapshotPlanForTest(t *testing.T, plan *cmdb.SharedTopologySnapshotPlan) []cmdb.SharedTopologySnapshot {
	t.Helper()
	require.NotNil(t, plan)
	frames := make([]cmdb.SharedTopologySnapshot, len(plan.Timestamps))
	nodeCount, edgeCount := 0, 0
	for i, ts := range plan.Timestamps {
		frames[i] = cmdb.SharedTopologySnapshot{Timestamp: ts, Nodes: []cmdb.SharedTopologyNode{}, Edges: []cmdb.SharedTopologyEdge{}}
		active := func(mask cmdb.SharedTopologyTimeMask) bool {
			word := mask.First
			if i >= 64 {
				if i/64-1 >= len(mask.Rest) {
					return false
				}
				word = mask.Rest[i/64-1]
			}
			return word&(uint64(1)<<uint(i%64)) != 0
		}
		seen := make(map[uint64]bool)
		for _, node := range plan.Nodes {
			if active(node.Mask) {
				require.False(t, seen[node.Node.ID], "overlapping versions at frame %d", i)
				seen[node.Node.ID] = true
				frames[i].Nodes = append(frames[i].Nodes, node.Node)
				nodeCount++
			}
		}
		for _, edge := range plan.Edges {
			if active(edge.Mask) {
				frames[i].Edges = append(frames[i].Edges, edge.Edge)
				edgeCount++
			}
		}
	}
	for _, partial := range plan.Partial {
		require.GreaterOrEqual(t, partial.Index, 0)
		require.Less(t, partial.Index, len(frames))
		frames[partial.Index].Partial = true
		frames[partial.Index].PartialReason = partial.Reason
	}
	require.Equal(t, nodeCount, plan.NodeOccurrences)
	require.Equal(t, edgeCount, plan.EdgeOccurrences)
	return frames
}

func enableSnapshotPlanForTest(t *testing.T) {
	t.Helper()
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
}

func TestSnapshotPlanMatchesFullDTOAndOwnsGraphData(t *testing.T) {
	enableSnapshotPlanForTest(t)
	ctx := context.Background()
	times := sharedGraphTestTimes(130)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	g := newSharedTimeGraph(sharedGraphTestConfig(), grid)
	t.Cleanup(func() { g.Clean(ctx) })
	relation := sharedGraphTestRelation("calls")
	input := cmdb.Matcher{"from_id": "a", "from_version": "m", "to_id": "b", "to_version": "before"}
	require.NoError(t, g.AddTimeRelationWithRelation(ctx, relation, input, times...))
	relation.MetricName = "other_flow"
	relation.Direction = "inbound"
	require.NoError(t, g.AddTimeRelationWithRelation(ctx, relation, input, times[0], times[63], times[64], times[127], times[128], times[129]))
	require.NoError(t, g.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "a", "version": "z"}, times[64], times[128]))
	a, err := g.nodeBuilder.GetID("entity", cmdb.Matcher{"id": "a"})
	require.NoError(t, err)
	b, err := g.nodeBuilder.GetID("entity", cmdb.Matcher{"id": "b"})
	require.NoError(t, err)
	require.Less(t, a, b)
	query := SharedTopologyQuery{SourceType: "entity", SourceMatcher: cmdb.Matcher{"id": "a"}, MaxHops: 1, PartialTimestamps: map[int64]string{times[64]: "", times[129]: "partial <&>"}}
	plan, err := g.findSharedTopologySnapshotPlan(ctx, grid, query, nil)
	require.NoError(t, err)
	want := make([]cmdb.SharedTopologySnapshot, len(times))
	for i, ts := range times {
		version := "m"
		if i == 64 || i == 128 {
			version = "z"
		}
		want[i] = cmdb.SharedTopologySnapshot{Timestamp: ts, Nodes: []cmdb.SharedTopologyNode{{ID: a, ResourceType: "entity", Dimensions: cmdb.Matcher{"id": "a", "version": version}}, {ID: b, ResourceType: "entity", Dimensions: cmdb.Matcher{"id": "b", "version": "before"}}}, Edges: []cmdb.SharedTopologyEdge{{Source: a, Target: b, RelationType: "calls", MetricName: "calls_flow", Category: "dynamic", Direction: "outbound"}}}
		if i == 0 || i == 63 || i == 64 || i == 127 || i == 128 || i == 129 {
			want[i].Edges = append(want[i].Edges, cmdb.SharedTopologyEdge{Source: b, Target: a, RelationType: "calls", MetricName: "other_flow", Category: "dynamic", Direction: "inbound"})
		}
		if reason, ok := query.PartialTimestamps[ts]; ok {
			want[i].Partial = true
			want[i].PartialReason = reason
		}
	}
	require.Equal(t, want, expandSnapshotPlanForTest(t, plan))
	public, err := g.findSharedTopologyDTO(ctx, grid, query)
	require.NoError(t, err)
	require.Equal(t, want, public)
	require.GreaterOrEqual(t, len(plan.Edges[0].Mask.Rest), 2)
	// Keep references to storage that may later be recycled. First verify the
	// exporter owns every mutable map and every bitmap word before Clean.
	borrowed := make(map[string]bool)
	for _, info := range g.nodeBuilder.info {
		borrowed[fmt.Sprintf("%p", info)] = true
	}
	for _, versions := range g.shared.nodeInfos {
		for _, v := range versions {
			v.dimensions["version"] = "storage-mutated"
			clear(v.activeBits.rest)
		}
	}
	for _, bits := range g.shared.edgeBits {
		clear(bits.rest)
	}
	require.Equal(t, want, expandSnapshotPlanForTest(t, plan))
	g.Clean(ctx)
	other := newSharedTimeGraph(sharedGraphTestConfig(), grid)
	t.Cleanup(func() { other.Clean(ctx) })
	for i := 0; i < 64; i++ {
		require.NoError(t, other.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": fmt.Sprintf("new-%d", i), "version": "recycled"}, times...))
	}
	reused := 0
	for _, info := range other.nodeBuilder.info {
		if borrowed[fmt.Sprintf("%p", info)] {
			reused++
		}
	}
	// sync.Pool is allowed to drop any entry, including under the race detector.
	// The direct storage mutation above makes ownership assertions unconditional.
	t.Logf("observed original matcher maps reused by new graph: %d", reused)
	require.Equal(t, want, expandSnapshotPlanForTest(t, plan))
	// Exported timestamps must also survive caller reuse of the input grid.
	times[0] = 0
	grid.timestamps[64] = 0
	require.Equal(t, want, expandSnapshotPlanForTest(t, plan))
}

func TestSnapshotPlanFallbackShapes(t *testing.T) {
	enableSnapshotPlanForTest(t)
	for _, mode := range []string{"nil-version", "empty-version", "missing-version", "missing-fallback", "nil-fallback"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			grid, err := NewTopologyGrid(sharedGraphTestTimes(130))
			require.NoError(t, err)
			g := newSharedTimeGraph(sharedGraphTestConfig(), grid)
			t.Cleanup(func() { g.Clean(ctx) })
			initial := cmdb.Matcher{"id": "a", "version": "before"}
			require.NoError(t, g.AddTimeNode(ctx, "entity", initial, grid.timestamps...))
			id, err := g.nodeBuilder.GetID("entity", initial)
			require.NoError(t, err)
			// Restore the valid pooled map after the deliberately malformed fallback fixture.
			stored := g.nodeBuilder.info[id]
			t.Cleanup(func() { g.nodeBuilder.info[id] = stored })
			var dimensions cmdb.Matcher
			switch mode {
			case "nil-version":
				g.shared.nodeInfos[id][0].dimensions = nil
			case "empty-version":
				g.shared.nodeInfos[id][0].dimensions = cmdb.Matcher{}
				dimensions = cmdb.Matcher{}
			case "missing-version":
				delete(g.shared.nodeInfos, id)
				dimensions = initial
			case "missing-fallback":
				delete(g.shared.nodeInfos, id)
				delete(g.nodeBuilder.info, id)
			case "nil-fallback":
				delete(g.shared.nodeInfos, id)
				g.nodeBuilder.info[id] = nil
			}
			query := SharedTopologyQuery{SourceType: "entity"}
			plan, err := g.findSharedTopologySnapshotPlan(ctx, grid, query, nil)
			require.NoError(t, err)
			want := make([]cmdb.SharedTopologySnapshot, len(grid.timestamps))
			for i, ts := range grid.timestamps {
				want[i] = cmdb.SharedTopologySnapshot{Timestamp: ts, Nodes: []cmdb.SharedTopologyNode{{ID: id, ResourceType: "entity", Dimensions: dimensions}}, Edges: []cmdb.SharedTopologyEdge{}}
			}
			require.Equal(t, want, expandSnapshotPlanForTest(t, plan))
			public, err := g.findSharedTopologyDTO(ctx, grid, query)
			require.NoError(t, err)
			require.Equal(t, want, public)
		})
	}
}

func TestSnapshotPlanPublicResultAndRejectionMetrics(t *testing.T) {
	enableSnapshotPlanForTest(t)
	responses := map[string]pl.Matrix{"source_info_relation": contractMatrix(map[string]string{"source_id": "a"}, 1700000000000, 1700000100000), "source_middle_flow": contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000, 1700000100000), "middle_target_flow": {}}
	model := sharedTopologyQueryModel(responses)
	query := cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, StartTime: 1700000000, EndTime: 1700000100, Step: "100s", MaxHops: 1}
	public, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), query)
	require.NoError(t, err)
	planned, err := model.QuerySharedTopologySnapshotPlan(initTimeGraphQueryTestEnvironment(), query)
	require.NoError(t, err)
	require.Equal(t, public.StartTime, planned.StartTime)
	require.Equal(t, public.EndTime, planned.EndTime)
	require.Equal(t, public.Step, planned.Step)
	require.Equal(t, public.PointCount, planned.PointCount)
	require.Equal(t, public.Snapshots, expandSnapshotPlanForTest(t, planned.Plan))
	oldPoints := MaxSharedTopologyPoints
	t.Cleanup(func() { MaxSharedTopologyPoints = oldPoints })
	MaxSharedTopologyPoints = 1
	for _, mode := range []string{"ordinary-planner", "compact-planner", "invalid-format", "ordinary-original"} {
		t.Run(mode, func(t *testing.T) {
			yoloMode = mode != "ordinary-planner" && mode != "ordinary-original"
			q := query
			if mode == "compact-planner" {
				q.ResponseFormat = cmdb.CompactTopologyFormat
			}
			if mode == "invalid-format" {
				q.ResponseFormat = "invalid"
			}
			labels := map[string]string{"scope": "query", "query_mode": "range", "result": "rejected"}
			before := readTimeGraphMetric(t, "cmdb_topology_operations_total", labels, "counter")
			gauge := map[string]string{"query_mode": "range"}
			inflight := readTimeGraphMetric(t, "cmdb_topology_inflight", gauge, "gauge")
			if mode == "ordinary-original" {
				_, err = model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), q)
				require.ErrorContains(t, err, "topology time grid contains 2 points, maximum is 1")
			} else {
				result, e := model.QuerySharedTopologySnapshotPlan(initTimeGraphQueryTestEnvironment(), q)
				wantError := map[string]string{
					"ordinary-planner": "snapshot plan requires YOLO mode",
					"compact-planner":  "snapshot plan requires snapshots format",
					"invalid-format":   "unsupported topology response format",
				}[mode]
				require.ErrorContains(t, e, wantError)
				require.Nil(t, result.Plan)
			}
			require.Equal(t, before+1, readTimeGraphMetric(t, "cmdb_topology_operations_total", labels, "counter"))
			require.Equal(t, inflight, readTimeGraphMetric(t, "cmdb_topology_inflight", gauge, "gauge"))
		})
	}
}

func TestSnapshotPlanFilteredInducedEdgesAcrossWords(t *testing.T) {
	enableSnapshotPlanForTest(t)
	ctx := context.Background()
	times := sharedGraphTestTimes(130)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	cfg := &TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "root", Index: cmdb.Index{"id"}, Info: cmdb.Index{"version"}},
		{Name: "middle", Index: cmdb.Index{"id"}, Info: cmdb.Index{"version"}},
		{Name: "target", Index: cmdb.Index{"id"}, Info: cmdb.Index{"version"}},
	}}
	graph := newSharedTimeGraph(cfg, grid)
	t.Cleanup(func() { graph.Clean(ctx) })
	add := func(source, target cmdb.Resource, from, to, name string, active ...int64) {
		relation := cmdb.Relation{V: []cmdb.Resource{source, target}, RelationType: name, MetricName: name + "_flow", Category: "dynamic", Direction: "outbound"}
		require.NoError(t, graph.AddTimeRelationWithRelation(ctx, relation, cmdb.Matcher{"from_id": from, "from_version": "v1", "to_id": to, "to_version": "v1"}, active...))
	}
	add("root", "middle", "a", "b", "first", times...)
	add("middle", "root", "b", "c", "return", times[0], times[64], times[129])
	add("middle", "target", "b", "d", "second", times...)
	add("root", "target", "a", "d", "direct", times...)
	add("root", "target", "a", "d", "parallel", times...)
	add("root", "target", "c", "d", "boundary", times[129])
	id := func(kind cmdb.Resource, value string) uint64 {
		valueID, idErr := graph.nodeBuilder.GetID(kind, cmdb.Matcher{"id": value})
		require.NoError(t, idErr)
		return valueID
	}
	a, c, d := id("root", "a"), id("root", "c"), id("target", "d")
	query := SharedTopologyQuery{SourceType: "root", SourceMatcher: cmdb.Matcher{"id": "a"}, MaxHops: 2, RootBits: graph.sharedTopologyRootBits()}
	before, err := graph.findSharedTopologyDTO(ctx, grid, query)
	require.NoError(t, err)
	for _, targets := range [][]cmdb.Resource{{"target"}, {"absent"}, {"target", "target"}} {
		plan, planErr := graph.findSharedTopologySnapshotPlan(ctx, grid, query, targets)
		require.NoError(t, planErr)
		frames := expandSnapshotPlanForTest(t, plan)
		for index, frame := range frames {
			nodes := []cmdb.SharedTopologyNode{{ID: a, ResourceType: "root", Dimensions: cmdb.Matcher{"id": "a", "version": "v1"}}}
			if index == 0 || index == 64 || index == 129 {
				nodes = append(nodes, cmdb.SharedTopologyNode{ID: c, ResourceType: "root", Dimensions: cmdb.Matcher{"id": "c", "version": "v1"}})
			}
			edges := []cmdb.SharedTopologyEdge{}
			if targets[0] == "target" {
				nodes = append(nodes, cmdb.SharedTopologyNode{ID: d, ResourceType: "target", Dimensions: cmdb.Matcher{"id": "d", "version": "v1"}})
				for _, name := range []string{"direct", "parallel"} {
					edges = append(edges, cmdb.SharedTopologyEdge{Source: a, Target: d, RelationType: name, MetricName: name + "_flow", Category: "dynamic", Direction: "outbound"})
				}
				if index == 129 {
					edges = append(edges, cmdb.SharedTopologyEdge{Source: c, Target: d, RelationType: "boundary", MetricName: "boundary_flow", Category: "dynamic", Direction: "outbound"})
				}
			}
			require.Equal(t, times[index], frame.Timestamp)
			require.ElementsMatch(t, nodes, frame.Nodes, "frame %d", index)
			require.ElementsMatch(t, edges, frame.Edges, "frame %d", index)
			require.False(t, frame.Partial)
		}
	}
	// In-place filtering may only mutate the traversal's private edge slice.
	// The next query must retain all middle nodes and original mask words.
	after, err := graph.findSharedTopologyDTO(ctx, grid, query)
	require.NoError(t, err)
	require.Equal(t, before, after)
}
