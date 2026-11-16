package v1beta3

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/stretchr/testify/require"
)

func TestSharedTraversalPreservesFrameReachabilityAndRootFilters(t *testing.T) {
	times := sharedTopologyTestTimes(60)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	g := newSharedTimeGraph(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "root", Index: cmdb.Index{"id"}, Info: cmdb.Index{"version"}},
		{Name: "entity", Index: cmdb.Index{"id"}, Info: cmdb.Index{"version"}},
	}}, grid)
	ids := make(map[string]uint64)
	for _, id := range []string{"r", "s", "a", "b", "c"} {
		kind := cmdb.Resource("entity")
		if id == "r" || id == "s" {
			kind = "root"
		}
		info := cmdb.Matcher{"id": id, "version": "old"}
		require.NoError(t, g.AddTimeNode(context.Background(), kind, info, times...))
		ids[id], err = g.nodeBuilder.GetID(kind, info)
		require.NoError(t, err)
	}
	require.NoError(t, g.AddTimeNode(context.Background(), "root", cmdb.Matcher{"id": "r", "version": "z"}, times[30], times[59]))
	bits := func(indices ...int) timeBitmap {
		var result timeBitmap
		for _, index := range indices {
			result.set(index)
		}
		return result
	}
	all := g.shared.fullGridBits
	nodes := map[uint64]timeBitmap{ids["r"]: all, ids["s"]: all, ids["a"]: bits(0, 30), ids["b"]: bits(30, 59), ids["c"]: bits(59)}
	edges := make([]sharedTopologyEdgeState, 0)
	for _, pair := range [][2]string{{"r", "a"}, {"r", "a"}, {"a", "b"}, {"b", "r"}, {"r", "c"}, {"s", "b"}} {
		active := all
		if pair[0] == "s" {
			active = bits(59)
		}
		edges = append(edges, sharedTopologyEdgeState{key: timeGraphTopologyEdgeKey{source: ids[pair[0]], target: ids[pair[1]]}, activeBits: active})
	}
	for _, tc := range []struct {
		name    string
		hops    int
		matcher cmdb.Matcher
		roots   map[uint64]timeBitmap
		seeds   int
		want    map[uint64]timeBitmap
	}{
		{name: "root only", matcher: cmdb.Matcher{"id": "r"}, seeds: 1, want: map[uint64]timeBitmap{ids["r"]: all}},
		{name: "one hop", hops: 1, matcher: cmdb.Matcher{"id": "r"}, seeds: 1, want: map[uint64]timeBitmap{ids["r"]: all, ids["a"]: bits(0, 30), ids["c"]: bits(59)}},
		{name: "two hops require a common frame", hops: 2, matcher: cmdb.Matcher{"id": "r"}, seeds: 1, want: map[uint64]timeBitmap{ids["r"]: all, ids["a"]: bits(0, 30), ids["b"]: bits(30), ids["c"]: bits(59)}},
		{name: "cycle and parallel edges", hops: 6, matcher: cmdb.Matcher{"id": "r"}, seeds: 1, want: map[uint64]timeBitmap{ids["r"]: all, ids["a"]: bits(0, 30), ids["b"]: bits(30), ids["c"]: bits(59)}},
		{name: "all matching roots", hops: 2, seeds: 2, want: map[uint64]timeBitmap{ids["r"]: all, ids["s"]: all, ids["a"]: bits(0, 30), ids["b"]: bits(30, 59), ids["c"]: bits(59)}},
		{name: "edge activity limits reachable frames", hops: 1, matcher: cmdb.Matcher{"id": "s"}, seeds: 1, want: map[uint64]timeBitmap{ids["s"]: all, ids["b"]: bits(59)}},
		{name: "time varying source attributes", hops: 2, matcher: cmdb.Matcher{"version": "z"}, seeds: 1, want: map[uint64]timeBitmap{ids["r"]: bits(30, 59), ids["a"]: bits(30), ids["b"]: bits(30), ids["c"]: bits(59)}},
		{name: "root frame constraint", hops: 2, matcher: cmdb.Matcher{"id": "r"}, roots: map[uint64]timeBitmap{ids["r"]: bits(59)}, seeds: 1, want: map[uint64]timeBitmap{ids["r"]: bits(59), ids["c"]: bits(59)}},
		{name: "empty root constraint", hops: 2, roots: map[uint64]timeBitmap{}, want: map[uint64]timeBitmap{}},
		{name: "missing root", hops: 2, matcher: cmdb.Matcher{"id": "missing"}, want: map[uint64]timeBitmap{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := SharedTopologyQuery{SourceType: "root", SourceMatcher: tc.matcher, MaxHops: tc.hops, RootBits: tc.roots}
			got, seeds, err := g.propagateSharedTopology(context.Background(), grid, query, nodes, edges)
			require.NoError(t, err)
			require.Equal(t, tc.seeds, seeds)
			require.Equal(t, tc.want, got)
			require.Equal(t, bits(0, 30), nodes[ids["a"]], "traversal must preserve source masks")
		})
	}
}

type traversalCancelContext struct {
	context.Context
	remaining int
}

func (c *traversalCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestSharedTraversalChecksCancellationWhileScanningEdges(t *testing.T) {
	g := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "root", Index: cmdb.Index{"id"}}}})
	root, err := g.nodeBuilder.GetID("root", cmdb.Matcher{"id": "r"})
	require.NoError(t, err)
	var bits timeBitmap
	bits.set(0)
	nodes := map[uint64]timeBitmap{root: bits}
	edges := make([]sharedTopologyEdgeState, 100)
	for i := range edges {
		edges[i] = sharedTopologyEdgeState{key: timeGraphTopologyEdgeKey{source: root, target: root}, activeBits: bits}
	}
	query := SharedTopologyQuery{SourceType: "root", MaxHops: 2}
	for _, after := range []int{1, 10, 90} {
		ctx := &traversalCancelContext{Context: context.Background(), remaining: after}
		_, _, err := g.propagateSharedTopology(ctx, TopologyGrid{timestamps: []int64{1}}, query, nodes, edges)
		require.ErrorIs(t, err, context.Canceled)
	}
}

func TestPrivateResourceIndexViewDoesNotChangeOwnedIndexes(t *testing.T) {
	config := sharedTopologyTestConfig()
	builder := newNodeBuilder(config)
	owned := builder.resourceIndexes("entity")
	require.NotEmpty(t, owned)
	original := append(cmdb.Index(nil), builder.resourceIndexView("entity")...)
	owned[0] = "caller-field"
	owned = append(owned, "extra")
	require.Equal(t, original, builder.resourceIndexView("entity"))
	other := newNodeBuilder(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "entity", Index: cmdb.Index{"other_id"}}}})
	require.Equal(t, cmdb.Index{"other_id"}, other.resourceIndexView("entity"))
	require.Equal(t, original, builder.resourceIndexView("entity"))
}

func TestCompactReservationsPreserveSparseTargetsAndVersions(t *testing.T) {
	times := sharedTopologyTestTimes(60)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	g := newSharedTimeGraph(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "root", Index: cmdb.Index{"root_id"}, Info: cmdb.Index{"version"}},
		{Name: "entity", Index: cmdb.Index{"entity_id"}},
	}}, grid)
	ctx := context.Background()
	relation := cmdb.Relation{V: []cmdb.Resource{"root", "entity"}, RelationType: "owns", Category: "static", Direction: "outbound"}
	require.NoError(t, g.AddTimeRelationWithRelation(ctx, relation, cmdb.Matcher{"root_id": "r", "entity_id": "e"}, times[30], times[59]))
	require.NoError(t, g.AddTimeNode(ctx, "root", cmdb.Matcher{"root_id": "r", "version": "a"}, times[30]))
	require.NoError(t, g.AddTimeNode(ctx, "root", cmdb.Matcher{"root_id": "r", "version": "z"}, times[59]))
	query := SharedTopologyQuery{SourceType: "root", MaxHops: 2}
	full, err := g.FindCompactTopology(ctx, grid, query, nil)
	require.NoError(t, err)
	require.Len(t, full.Nodes, 3)
	require.Len(t, full.Edges, 1)
	require.Equal(t, 2, full.EdgeOccurrences)
	filtered, err := g.FindCompactTopology(ctx, grid, query, []cmdb.Resource{"absent"})
	require.NoError(t, err)
	require.Len(t, filtered.Nodes, 2)
	require.NotNil(t, filtered.Edges)
	require.Empty(t, filtered.Edges)
	query.SourceMatcher = cmdb.Matcher{"root_id": "missing"}
	empty, err := g.FindCompactTopology(ctx, grid, query, nil)
	require.NoError(t, err)
	require.NotNil(t, empty.Nodes)
	require.NotNil(t, empty.Edges)
	require.Empty(t, empty.Nodes)
	require.Empty(t, empty.Edges)
}

func TestEmptyRootMatcherPreservesSharedBits(t *testing.T) {
	g := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "root", Index: cmdb.Index{"id"}}}})
	id, err := g.nodeBuilder.GetID("root", cmdb.Matcher{"id": "r"})
	require.NoError(t, err)
	bits := timeBitmap{first: 1}
	nodes := map[uint64]timeBitmap{id: bits}
	result, count, err := g.propagateSharedTopology(context.Background(), TopologyGrid{timestamps: sharedTopologyTestTimes(60)}, SharedTopologyQuery{SourceType: "root", MaxHops: 0}, nodes, nil)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, bits, result[id])
	changed := result[id]
	changed.set(59)
	require.Equal(t, timeBitmap{first: 1}, nodes[id])
}

func traversalTestGraph(t *testing.T) (*TimeGraph, TopologyGrid, SharedTopologyQuery) {
	t.Helper()
	grid, err := NewTopologyGrid(sharedTopologyTestTimes(60))
	require.NoError(t, err)
	graph := newSharedTimeGraph(sharedTopologyTestConfig(), grid)
	for _, edge := range []cmdb.Matcher{{"from_id": "a", "to_id": "b"}, {"from_id": "b", "to_id": "c"}, {"from_id": "a", "to_id": "c"}} {
		require.NoError(t, graph.AddTimeRelationWithRelation(context.Background(), sharedTopologyTestRelation("calls"), edge, grid.timestamps...))
	}
	return graph, grid, SharedTopologyQuery{SourceType: "entity", SourceMatcher: cmdb.Matcher{"id": "a"}, MaxHops: 2}
}

func TestSharedTraversalGridAndEmptyFrameContracts(t *testing.T) {
	t.Run("grid_mismatch_is_rejected", func(t *testing.T) {
		graph, grid, query := traversalTestGraph(t)
		defer graph.Clean(context.Background())
		badGrid, err := NewTopologyGrid(grid.timestamps[:59])
		require.NoError(t, err)
		_, err = graph.FindCompactTopology(context.Background(), badGrid, query, nil)
		require.ErrorContains(t, err, "grid does not match")
	})
	t.Run("public_empty_frames_keep_nil_contract", func(t *testing.T) {
		graph, grid, query := traversalTestGraph(t)
		defer graph.Clean(context.Background())
		query.SourceMatcher = cmdb.Matcher{"id": "missing"}
		frames, err := graph.FindSharedTopology(context.Background(), grid, query)
		require.NoError(t, err)
		require.Len(t, frames, 60)
		for _, frame := range frames {
			require.Nil(t, frame.Nodes)
			require.Nil(t, frame.Edges)
		}
	})
}
