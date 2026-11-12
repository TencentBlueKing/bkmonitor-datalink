package v1beta3

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/stretchr/testify/require"
)

// Normalize empty slices when comparing the legacy test helper with wire DTOs.
func convertSharedTopologySnapshots(snapshots []SharedTopologySnapshot) []cmdb.SharedTopologySnapshot {
	result := make([]cmdb.SharedTopologySnapshot, len(snapshots))
	for i, snapshot := range snapshots {
		result[i] = cmdb.SharedTopologySnapshot{
			Timestamp:     snapshot.Timestamp,
			Partial:       snapshot.Partial,
			PartialReason: snapshot.PartialReason,
			Nodes:         make([]cmdb.SharedTopologyNode, len(snapshot.Nodes)),
			Edges:         make([]cmdb.SharedTopologyEdge, len(snapshot.Edges)),
		}
		for j, node := range snapshot.Nodes {
			result[i].Nodes[j] = cmdb.SharedTopologyNode{
				ID:           node.ID,
				ResourceType: node.ResourceType,
				// Materialization already owns this map, independently of graph pools.
				Dimensions: node.Dimensions,
			}
		}
		for j, edge := range snapshot.Edges {
			result[i].Edges[j] = cmdb.SharedTopologyEdge{
				Source:       edge.Source,
				Target:       edge.Target,
				RelationType: edge.RelationType,
				MetricName:   edge.MetricName,
				Category:     edge.Category,
				Direction:    edge.Direction,
			}
		}
	}
	return result
}

func TestDirectTopologyDTOFrameContentsAndOwnership(t *testing.T) {
	for _, points := range []int{3, 60} {
		t.Run(fmt.Sprintf("points=%d", points), func(t *testing.T) {
			ctx := context.Background()
			times := sharedTopologyTestTimes(points)
			grid, err := NewTopologyGrid(times)
			require.NoError(t, err)
			g := newSharedTimeGraph(sharedTopologyTestConfig(), grid)
			ids := make(map[string]uint64)
			for _, id := range []string{"a", "b", "c"} {
				info := cmdb.Matcher{"id": id, "version": "old"}
				require.NoError(t, g.AddTimeNode(ctx, "entity", info, times...))
				ids[id], err = g.nodeBuilder.GetID("entity", info)
				require.NoError(t, err)
			}
			require.NoError(t, g.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "b", "version": "z"}, times[len(times)-1]))
			require.NoError(t, g.AddTimeRelationWithRelation(ctx, sharedTopologyTestRelation("calls"), cmdb.Matcher{"from_id": "a", "to_id": "b"}, times...))
			require.NoError(t, g.AddTimeRelationWithRelation(ctx, sharedTopologyTestRelation("other"), cmdb.Matcher{"from_id": "a", "to_id": "b"}, times...))
			require.NoError(t, g.AddTimeRelationWithRelation(ctx, sharedTopologyTestRelation("calls"), cmdb.Matcher{"from_id": "b", "to_id": "c"}, times[1:]...))
			query := SharedTopologyQuery{SourceType: "entity", SourceMatcher: cmdb.Matcher{"id": "a"}, MaxHops: 2, PartialTimestamps: map[int64]string{times[1]: "missing source"}}
			got, err := g.findSharedTopologyDTO(ctx, grid, query)
			require.NoError(t, err)
			require.Len(t, got, points)
			for index, frame := range got {
				require.Equal(t, times[index], frame.Timestamp)
				wantNodes := []cmdb.SharedTopologyNode{
					{ID: ids["a"], ResourceType: "entity", Dimensions: cmdb.Matcher{"id": "a", "version": "old"}},
					{ID: ids["b"], ResourceType: "entity", Dimensions: cmdb.Matcher{"id": "b", "version": "old"}},
				}
				wantEdges := []cmdb.SharedTopologyEdge{
					{Source: ids["a"], Target: ids["b"], RelationType: "calls", MetricName: "calls_flow", Category: "dynamic", Direction: "outbound"},
					{Source: ids["a"], Target: ids["b"], RelationType: "other", MetricName: "other_flow", Category: "dynamic", Direction: "outbound"},
				}
				if index > 0 {
					wantNodes = append(wantNodes, cmdb.SharedTopologyNode{ID: ids["c"], ResourceType: "entity", Dimensions: cmdb.Matcher{"id": "c", "version": "old"}})
					wantEdges = append(wantEdges, cmdb.SharedTopologyEdge{Source: ids["b"], Target: ids["c"], RelationType: "calls", MetricName: "calls_flow", Category: "dynamic", Direction: "outbound"})
				}
				if index == points-1 {
					wantNodes[1].Dimensions["version"] = "z"
				}
				require.Equal(t, wantNodes, frame.Nodes)
				require.Equal(t, wantEdges, frame.Edges)
				require.Equal(t, index == 1, frame.Partial)
				require.Equal(t, query.PartialTimestamps[times[index]], frame.PartialReason)
			}
			before, err := json.Marshal(got)
			require.NoError(t, err)
			got[0].Nodes[0].Dimensions["caller"] = "changed"
			require.NotContains(t, got[1].Nodes[0].Dimensions, "caller")
			fresh, err := g.findSharedTopologyDTO(ctx, grid, query)
			require.NoError(t, err)
			g.Clean(ctx)
			after, err := json.Marshal(fresh)
			require.NoError(t, err)
			require.Equal(t, before, after)
			empty, err := g.findSharedTopologyDTO(ctx, grid, SharedTopologyQuery{SourceType: "entity"})
			require.NoError(t, err)
			for _, frame := range empty {
				require.NotNil(t, frame.Nodes)
				require.NotNil(t, frame.Edges)
				require.Empty(t, frame.Nodes)
				require.Empty(t, frame.Edges)
			}
		})
	}
}

// target_types 在逐帧构造时过滤：保留源类型和目标类型的节点，删去其余节点及其关联边，
// 不把中间节点折叠为新边；每帧都是新分配的切片。取消在构造帧时生效。
func TestTopologyFramesFilterOutputTypes(t *testing.T) {
	ctx := context.Background()
	times := sharedTopologyTestTimes(2)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	g := newSharedTimeGraph(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "root", Index: cmdb.Index{"root_id"}},
		{Name: "dropped", Index: cmdb.Index{"dropped_id"}},
		{Name: "target", Index: cmdb.Index{"target_id"}},
	}}, grid)
	t.Cleanup(func() { g.Clean(ctx) })
	relation := func(source, target cmdb.Resource, relationType string) cmdb.Relation {
		return cmdb.Relation{V: []cmdb.Resource{source, target}, RelationType: relationType, Category: string(RelationCategoryStatic), Direction: string(DirectionOutbound)}
	}
	require.NoError(t, g.AddTimeRelationWithRelation(ctx, relation("root", "dropped", "removed"), cmdb.Matcher{"root_id": "r", "dropped_id": "d"}, times...))
	require.NoError(t, g.AddTimeRelationWithRelation(ctx, relation("root", "target", "kept"), cmdb.Matcher{"root_id": "r", "target_id": "t"}, times...))
	require.NoError(t, g.AddTimeRelationWithRelation(ctx, relation("dropped", "target", "folded"), cmdb.Matcher{"dropped_id": "d", "target_id": "t"}, times...))
	query := SharedTopologyQuery{SourceType: "root", SourceMatcher: cmdb.Matcher{"root_id": "r"}, MaxHops: 2}
	all, err := g.findSharedTopologyDTO(ctx, grid, query)
	require.NoError(t, err)
	require.Len(t, all[0].Nodes, 3)
	require.Len(t, all[0].Edges, 3)

	frames, err := g.prepareSharedTopologyFrames(ctx, grid, query, topologyOutputTypes("root", []cmdb.Resource{"target"}))
	require.NoError(t, err)
	var filtered []cmdb.SharedTopologySnapshot
	require.NoError(t, frames.each(ctx, func(snapshot cmdb.SharedTopologySnapshot) error {
		filtered = append(filtered, snapshot)
		return nil
	}))
	require.Len(t, filtered, len(times))
	for index, frame := range filtered {
		require.Equal(t, times[index], frame.Timestamp)
		var types []cmdb.Resource
		for _, node := range frame.Nodes {
			types = append(types, node.ResourceType)
		}
		require.ElementsMatch(t, []cmdb.Resource{"root", "target"}, types)
		require.Len(t, frame.Edges, 1)
		require.Equal(t, "kept", frame.Edges[0].RelationType)
	}
	require.Nil(t, topologyOutputTypes("root", nil), "an empty target_types keeps every reachable type")

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, frames.each(canceled, func(cmdb.SharedTopologySnapshot) error { return nil }), context.Canceled)
}
