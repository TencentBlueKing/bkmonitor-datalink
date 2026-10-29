package v1beta3

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/stretchr/testify/require"
)

// Original conversion remains an independent reference for graph/DTO equivalence.
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

func TestDirectTopologyDTOEquivalenceAndOwnership(t *testing.T) {
	original := yoloMode
	t.Cleanup(func() { yoloMode = original })
	for _, points := range []int{3, 130} {
		for _, storage := range []bool{false, true} {
			t.Run(fmt.Sprintf("points=%d/shared=%v", points, storage), func(t *testing.T) {
				yoloMode = points > 60
				ctx := context.Background()
				times := sharedGraphTestTimes(points)
				grid, err := NewTopologyGrid(times)
				require.NoError(t, err)
				g := NewTimeGraphWithConfig(sharedGraphTestConfig())
				if storage {
					g = newSharedTimeGraph(sharedGraphTestConfig(), grid)
				}
				for _, id := range []string{"a", "b", "c"} {
					require.NoError(t, g.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": id, "version": "old"}, times...))
				}
				require.NoError(t, g.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "b", "version": "z"}, times[len(times)-1]))
				require.NoError(t, g.AddTimeRelationWithRelation(ctx, sharedGraphTestRelation("calls"), cmdb.Matcher{"from_id": "a", "to_id": "b"}, times...))
				require.NoError(t, g.AddTimeRelationWithRelation(ctx, sharedGraphTestRelation("other"), cmdb.Matcher{"from_id": "a", "to_id": "b"}, times...))
				require.NoError(t, g.AddTimeRelationWithRelation(ctx, sharedGraphTestRelation("calls"), cmdb.Matcher{"from_id": "b", "to_id": "c"}, times[1:]...))
				query := SharedTopologyQuery{SourceType: "entity", SourceMatcher: cmdb.Matcher{"id": "a"}, MaxHops: 2, PartialTimestamps: map[int64]string{times[1]: "missing source"}}
				want, err := g.FindSharedTopology(ctx, grid, query)
				require.NoError(t, err)
				got, err := g.findSharedTopologyDTO(ctx, grid, query)
				require.NoError(t, err)
				require.Equal(t, convertSharedTopologySnapshots(want), got)
				before, err := json.Marshal(got)
				require.NoError(t, err)
				got[0].Nodes[0].Dimensions["caller"] = "changed"
				require.NotContains(t, got[1].Nodes[0].Dimensions, "caller")
				require.NotContains(t, want[0].Nodes[0].Dimensions, "caller")
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
}

func TestTopologyDTOFilterDropsRetainedReferences(t *testing.T) {
	frames := []cmdb.SharedTopologySnapshot{{Nodes: []cmdb.SharedTopologyNode{
		{ID: 1, ResourceType: "root", Dimensions: cmdb.Matcher{"id": "root"}},
		{ID: 2, ResourceType: "dropped", Dimensions: cmdb.Matcher{"id": "dropped"}},
		{ID: 3, ResourceType: "target", Dimensions: cmdb.Matcher{"id": "target"}},
	}, Edges: []cmdb.SharedTopologyEdge{{Source: 1, Target: 2, RelationType: "removed"}, {Source: 1, Target: 3, RelationType: "kept"}}}}
	backingNodes := frames[0].Nodes
	backingEdges := frames[0].Edges
	require.NoError(t, filterSharedTopologySnapshots(context.Background(), frames, "root", []cmdb.Resource{"target"}))
	require.Len(t, frames[0].Nodes, 2)
	require.Len(t, frames[0].Edges, 1)
	require.Zero(t, backingNodes[2])
	require.Zero(t, backingEdges[1])
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, filterSharedTopologySnapshots(canceled, frames, "root", nil), context.Canceled)
}
