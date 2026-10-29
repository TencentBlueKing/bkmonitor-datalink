package v1beta3

import (
	"context"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/stretchr/testify/require"
)

func TestYOLOIndexedTraversalAndOrderedSnapshotsMatchScan(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	times := sharedGraphTestTimes(130)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	g := newSharedTimeGraph(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "root", Index: cmdb.Index{"id"}, Info: cmdb.Index{"version"}},
		{Name: "entity", Index: cmdb.Index{"id"}, Info: cmdb.Index{"version"}},
	}}, grid)
	nodes := make(map[uint64]timeBitmap)
	ids := make([]uint64, 12)
	for i := range ids {
		kind := cmdb.Resource("entity")
		if i < 3 {
			kind = "root"
		}
		info := cmdb.Matcher{"id": fmt.Sprint(i), "version": "old"}
		require.NoError(t, g.AddTimeNode(context.Background(), kind, info, times...))
		ids[i], err = g.nodeBuilder.GetID(kind, info)
		require.NoError(t, err)
		var bits timeBitmap
		for k := range times {
			if i < 3 || (k+i)%4 != 0 {
				bits.set(k)
			}
		}
		nodes[ids[i]] = bits
		require.NoError(t, g.AddTimeNode(context.Background(), kind, cmdb.Matcher{"id": fmt.Sprint(i), "version": "z"}, times[64], times[129]))
	}
	edges := make([]sharedTopologyEdgeState, 0)
	for i := range ids {
		for j := range ids {
			if (i+j)%3 != 0 && j != i+1 {
				continue
			}
			for parallel := 0; parallel < 2; parallel++ {
				var bits timeBitmap
				for k := range times {
					if (k+i+j)%5 != 0 {
						bits.set(k)
					}
				}
				edges = append(edges, sharedTopologyEdgeState{key: timeGraphTopologyEdgeKey{source: ids[i], target: ids[j], relation: timeGraphEdgeRelation{relationType: fmt.Sprintf("relation-%d", parallel), metricName: "calls", category: "dynamic", direction: "outbound"}}, activeBits: bits})
			}
		}
	}
	var rootFrame timeBitmap
	rootFrame.set(64)
	rootFrame.set(128)
	rootFrame.set(129)
	roots := []map[uint64]timeBitmap{nil, {}, {ids[0]: rootFrame, ids[1]: rootFrame}}
	for rootCase, rootBits := range roots {
		for _, h := range []int{0, 1, 2, 4, 10} {
			for _, matcher := range []cmdb.Matcher{nil, {"version": "z"}, {"id": "missing"}} {
				name := fmt.Sprintf("roots=%d/h=%d/matcher=%v", rootCase, h, matcher)
				t.Run(name, func(t *testing.T) {
					query := SharedTopologyQuery{SourceType: "root", SourceMatcher: matcher, MaxHops: h, RootBits: rootBits, PartialTimestamps: map[int64]string{times[64]: "partial"}}
					yoloMode = false
					scan, count, err := g.propagateSharedTopology(context.Background(), grid, query, nodes, edges)
					require.NoError(t, err)
					want, err := g.materializeSharedTopology(context.Background(), grid, query, scan, append([]sharedTopologyEdgeState(nil), edges...))
					require.NoError(t, err)
					yoloMode = true
					indexed, indexCount, err := g.propagateSharedTopology(context.Background(), grid, query, nodes, edges)
					require.NoError(t, err)
					require.Equal(t, count, indexCount)
					require.Equal(t, scan, indexed)
					got, err := g.materializeSharedTopology(context.Background(), grid, query, indexed, append([]sharedTopologyEdgeState(nil), edges...))
					require.NoError(t, err)
					require.Equal(t, want, got)
					if rootCase == 1 || len(matcher) > 0 && matcher["id"] == "missing" {
						require.Empty(t, indexed)
					}
					if len(got[0].Nodes) > 0 && len(got[1].Nodes) > 0 {
						got[0].Nodes[0].Dimensions["caller"] = "changed"
						require.NotContains(t, got[1].Nodes[0].Dimensions, "caller")
					}
				})
			}
		}
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

func TestYOLOIndexedTraversalChecksCancellationWithinIndexAndDegree(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
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
	for _, after := range []int{10, 110} {
		ctx := &traversalCancelContext{Context: context.Background(), remaining: after}
		_, _, err := g.propagateSharedTopology(ctx, TopologyGrid{timestamps: []int64{1}}, query, nodes, edges)
		require.ErrorIs(t, err, context.Canceled)
	}
}

func TestPrivateResourceIndexViewDoesNotChangeOwnedIndexes(t *testing.T) {
	config := sharedGraphTestConfig()
	builder := NewNodeBuilderWithConfig(NewStringDict(), config)
	owned := builder.resourceIndexes("entity")
	require.NotEmpty(t, owned)
	original := append(cmdb.Index(nil), builder.resourceIndexView("entity")...)
	owned[0] = "caller-field"
	owned = append(owned, "extra")
	require.Equal(t, original, builder.resourceIndexView("entity"))
	other := NewNodeBuilderWithConfig(NewStringDict(), &TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "entity", Index: cmdb.Index{"other_id"}}}})
	require.Equal(t, cmdb.Index{"other_id"}, other.resourceIndexView("entity"))
	require.Equal(t, original, builder.resourceIndexView("entity"))
}

func TestCompactReservationsPreserveSparseTargetsAndVersions(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	times := sharedGraphTestTimes(130)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	g := newSharedTimeGraph(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "root", Index: cmdb.Index{"root_id"}, Info: cmdb.Index{"version"}},
		{Name: "entity", Index: cmdb.Index{"entity_id"}},
	}}, grid)
	ctx := context.Background()
	relation := cmdb.Relation{V: []cmdb.Resource{"root", "entity"}, RelationType: "owns", Category: "static", Direction: "outbound"}
	require.NoError(t, g.AddTimeRelationWithRelation(ctx, relation, cmdb.Matcher{"root_id": "r", "entity_id": "e"}, times[64], times[128]))
	require.NoError(t, g.AddTimeNode(ctx, "root", cmdb.Matcher{"root_id": "r", "version": "a"}, times[64]))
	require.NoError(t, g.AddTimeNode(ctx, "root", cmdb.Matcher{"root_id": "r", "version": "z"}, times[128]))
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

func TestEmptyRootMatcherNormalizesZeroWordsWithoutChangingSharedBits(t *testing.T) {
	old := yoloMode
	yoloMode = false
	t.Cleanup(func() { yoloMode = old })
	g := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "root", Index: cmdb.Index{"id"}}}})
	id, err := g.nodeBuilder.GetID("root", cmdb.Matcher{"id": "r"})
	require.NoError(t, err)
	bits := timeBitmap{first: 1, rest: []uint64{0, 0}}
	nodes := map[uint64]timeBitmap{id: bits}
	result, count, err := g.propagateSharedTopology(context.Background(), TopologyGrid{timestamps: sharedGraphTestTimes(130)}, SharedTopologyQuery{SourceType: "root", MaxHops: 0}, nodes, nil)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Empty(t, result[id].rest)
	require.Equal(t, []uint64{0, 0}, bits.rest)
	require.Equal(t, 20*(1+len(timeBitmap{first: 1}.rest)), 20*(1+len(result[id].rest)))
}
