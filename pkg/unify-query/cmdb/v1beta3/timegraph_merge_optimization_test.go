package v1beta3

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/stretchr/testify/require"
)

func TestNodeAttributeMergePreservesEmptyKeysAndOwnership(t *testing.T) {
	b := NewNodeBuilderWithConfig(NewStringDict(), sharedGraphTestConfig())
	id, err := b.GetID("entity", cmdb.Matcher{"id": "a", "version": "m"})
	require.NoError(t, err)
	_, before := b.Info(id)
	packed := b.GetCompressedInfo(id)
	for _, version := range []string{"m", "a"} {
		same, err := b.GetID("entity", cmdb.Matcher{"id": "a", "version": version})
		require.NoError(t, err)
		require.Equal(t, id, same)
		_, info := b.Info(id)
		require.Equal(t, before, info)
		require.Equal(t, packed, b.GetCompressedInfo(id))
	}
	_, err = b.GetID("entity", cmdb.Matcher{"id": "a", "version": "z", "name": ""})
	require.NoError(t, err)
	_, after := b.Info(id)
	require.Equal(t, cmdb.Matcher{"id": "a", "version": "z", "name": ""}, after)
	require.Equal(t, cmdb.Resource("entity"), b.resourceName(id))
	require.Equal(t, cmdb.Resource(""), b.resourceName(0))
	compressed := b.GetCompressedInfo(id)
	require.NotEqual(t, packed, compressed)
	before["version"] = "caller"
	_, again := b.Info(id)
	require.Equal(t, "z", again["version"])
	b.Clean()
	_, err = b.GetID("entity", cmdb.Matcher{"id": "b", "version": "new"})
	require.NoError(t, err)
	require.Equal(t, "z", after["version"])
	require.Equal(t, "a", after["id"])
}

func TestSharedNodeMergePreservesTimeVersionsAndEmptyFields(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	times := sharedGraphTestTimes(130)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	g, err := newSharedTimeGraph(sharedGraphTestConfig(), grid)
	require.NoError(t, err)
	ctx := context.Background()
	initial := []int64{times[0], times[64], times[129]}
	require.NoError(t, g.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "a", "version": "m"}, initial...))
	id, err := g.nodeBuilder.GetID("entity", cmdb.Matcher{"id": "a"})
	require.NoError(t, err)
	for _, version := range []string{"m", "a"} {
		require.NoError(t, g.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "a", "version": version}, times[64], times[129]))
		require.Len(t, g.shared.nodeInfos[id], 1)
		require.Equal(t, 3, g.nodeInfoCount)
		_, info := g.nodeInfoAt(times[129], id, nil)
		require.Equal(t, "m", info["version"])
	}
	require.NoError(t, g.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "a"}, times[128]))
	_, info := g.nodeInfoAt(times[128], id, nil)
	require.Equal(t, cmdb.Matcher{"id": "a"}, info)
	require.Equal(t, 4, g.nodeInfoCount)
	require.NoError(t, g.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "a", "name": ""}, times[64]))
	_, info = g.nodeInfoAt(times[64], id, nil)
	require.Equal(t, "", info["name"])
	require.Contains(t, info, "name")
	_, other := g.nodeInfoAt(times[129], id, nil)
	require.NotContains(t, other, "name")
	require.NoError(t, g.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "a", "version": "z"}, times[129]))
	_, other = g.nodeInfoAt(times[129], id, nil)
	require.Equal(t, "z", other["version"])
	require.Equal(t, "m", info["version"])

	// A repeated update must still honor an already exceeded version limit.
	single, err := newSharedTimeGraph(sharedGraphTestConfig(), grid)
	require.NoError(t, err)
	require.NoError(t, single.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "b"}, initial...))
	single.maxNodeInfos = -1
	single.shared.nodeInfoVersionCount = 2
	single.maxNodeInfos = 1
	err = single.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "b"}, initial...)
	var limit *ResultLimitError
	require.ErrorAs(t, err, &limit)
	require.Equal(t, "max_graph_node_infos", limit.Reason)
}

func TestTimeBitmapSubsetAcrossWords(t *testing.T) {
	var all, timeSubset, missing timeBitmap
	for _, i := range []int{0, 63, 64, 128, 129} {
		all.set(i)
	}
	for _, i := range []int{63, 64, 129} {
		timeSubset.set(i)
	}
	missing.set(130)
	require.True(t, timeSubset.isSubsetOf(all))
	require.True(t, (timeBitmap{}).isSubsetOf(all))
	require.False(t, missing.isSubsetOf(all))
	require.False(t, all.isSubsetOf(timeSubset))
	require.True(t, (timeBitmap{rest: []uint64{0, 0, 0}}).isSubsetOf(timeBitmap{}))
}

func TestTimeBitmapCommonIntersectionAcrossWords(t *testing.T) {
	for _, index := range []int{0, 64, 128} {
		var a, b, c timeBitmap
		a.set(index)
		b.set(index)
		c.set(index)
		require.True(t, a.intersectsBoth(b, c))
		c = timeBitmap{}
		c.set(index + 1)
		require.False(t, a.intersectsBoth(b, c))
	}
	var a, b, c timeBitmap
	for _, i := range []int{0, 64} {
		a.set(i)
	}
	for _, i := range []int{64, 128} {
		b.set(i)
	}
	for _, i := range []int{0, 128} {
		c.set(i)
	}
	require.False(t, a.intersectsBoth(b, c), "pairwise intersections do not imply a common frame")
}

func TestFullGridTimeBitsFastPathPreservesValidationAndOwnership(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	for _, size := range []int{1, 63, 64, 65, 127, 128, 130} {
		grid, err := NewTopologyGrid(sharedGraphTestTimes(size))
		require.NoError(t, err)
		g, err := newSharedTimeGraph(sharedGraphTestConfig(), grid)
		require.NoError(t, err)
		first, err := g.shared.timeBits(grid.Timestamps)
		require.NoError(t, err)
		require.Equal(t, size, first.count())
		var expected timeBitmap
		for i := range grid.Timestamps {
			expected.set(i)
		}
		require.Equal(t, expected, first)
		_ = first.subtract(expected)
		_ = first.intersect(timeBitmap{})
		_ = first.union(timeBitmap{})
		again, err := g.shared.timeBits(grid.Timestamps)
		require.NoError(t, err)
		require.Equal(t, expected, again)
		subset := []int64{grid.Timestamps[size-1], grid.Timestamps[0], grid.Timestamps[size-1]}
		got, err := g.shared.timeBits(subset)
		require.NoError(t, err)
		require.Equal(t, min(size, 2), got.count())
		_, err = g.shared.timeBits([]int64{grid.Timestamps[0] - 1})
		require.ErrorContains(t, err, "does not match topology time grid")
		g.Clean(context.Background())
		require.NoError(t, g.AddTimeNode(context.Background(), "entity", cmdb.Matcher{"id": "after"}, grid.Timestamps...))
		after, err := g.shared.timeBits(grid.Timestamps)
		require.NoError(t, err)
		require.Equal(t, expected, after)
	}
}

func TestCompactMaskFixedWidthAndZeroWords(t *testing.T) {
	require.Equal(t, []string{"0000000000000000"}, compactTimeMask(timeBitmap{}))
	require.Equal(t, []string{"0000000000000001"}, compactTimeMask(timeBitmap{first: 1}))
	require.Equal(t, []string{"ffffffffffffffff"}, compactTimeMask(timeBitmap{first: ^uint64(0)}))
	require.Equal(t, []string{"8000000000000000", "0000000000000000", "0000000000000001"}, compactTimeMask(timeBitmap{first: uint64(1) << 63, rest: []uint64{0, 1, 0, 0}}))
}

func TestSharedInitialAttributesStayImmutableThroughUpdatesAndCleanup(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	times := sharedGraphTestTimes(130)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	g, err := newSharedTimeGraph(sharedGraphTestConfig(), grid)
	require.NoError(t, err)
	ctx := context.Background()
	input := cmdb.Matcher{"id": "a", "version": "m", "name": ""}
	require.NoError(t, g.AddTimeNode(ctx, "entity", input, times...))
	id, err := g.nodeBuilder.GetID("entity", input)
	require.NoError(t, err)
	_, borrowed := g.nodeBuilder.matchingInfoView(id, input)
	require.True(t, borrowed)
	_, borrowed = g.nodeBuilder.matchingInfoView(id, cmdb.Matcher{"id": "a"})
	require.False(t, borrowed)
	_, borrowed = g.nodeBuilder.matchingInfoView(id, cmdb.Matcher{"id": "a", "version": "m", "name": "", "extra": "unprojected"})
	require.False(t, borrowed)
	clear(input)
	input["id"] = "other"
	require.NoError(t, g.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "a", "version": "a"}, times[0]))
	require.NoError(t, g.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "a", "version": "z"}, times[64]))
	_, before := g.nodeInfoAt(times[0], id, nil)
	require.Equal(t, "m", before["version"])
	_, changed := g.nodeInfoAt(times[64], id, nil)
	require.Equal(t, "z", changed["version"])
	query := SharedTopologyQuery{SourceType: "entity", MaxHops: 0}
	snapshots, err := g.FindSharedTopology(ctx, grid, query)
	require.NoError(t, err)
	compact, err := g.FindCompactTopology(ctx, grid, query, nil)
	require.NoError(t, err)
	want := decodeCompactForTest(t, compact)
	g.Clean(ctx)
	other := NewTimeGraphWithConfig(sharedGraphTestConfig())
	require.NoError(t, other.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "different", "version": "reuse"}, times...))
	require.Equal(t, "m", snapshots[0].Nodes[0].Dimensions["version"])
	require.Equal(t, "z", snapshots[64].Nodes[0].Dimensions["version"])
	require.Equal(t, want, decodeCompactForTest(t, compact))
	snapshots[0].Nodes[0].Dimensions["version"] = "caller"
	require.Equal(t, "m", snapshots[1].Nodes[0].Dimensions["version"])
	require.Equal(t, "m", want[0].Nodes[0].Dimensions["version"])
}

func TestUnchangedBorrowedAttributesExportSurvivesPoolReuse(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	ctx := context.Background()
	grid, err := NewTopologyGrid(sharedGraphTestTimes(130))
	require.NoError(t, err)
	g, err := newSharedTimeGraph(sharedGraphTestConfig(), grid)
	require.NoError(t, err)
	info := cmdb.Matcher{"id": "unchanged", "version": "m", "name": "original"}
	require.NoError(t, g.AddTimeNode(ctx, "entity", info, grid.Timestamps...))
	id, err := g.nodeBuilder.GetID("entity", info)
	require.NoError(t, err)
	_, borrowed := g.nodeBuilder.matchingInfoView(id, info)
	require.True(t, borrowed)
	query := SharedTopologyQuery{SourceType: "entity", MaxHops: 0}
	snapshots, err := g.FindSharedTopology(ctx, grid, query)
	require.NoError(t, err)
	compact, err := g.FindCompactTopology(ctx, grid, query, nil)
	require.NoError(t, err)
	want := decodeCompactForTest(t, compact)
	// No update has replaced n.info: Clean returns the actual borrowed map to its pool.
	g.Clean(ctx)
	other := NewTimeGraphWithConfig(sharedGraphTestConfig())
	for _, value := range []string{"a", "b", "c"} {
		require.NoError(t, other.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": value, "version": "different", "name": "recycled"}, grid.Timestamps...))
	}
	for _, snapshot := range snapshots {
		require.Equal(t, info, snapshot.Nodes[0].Dimensions)
	}
	require.Equal(t, want, decodeCompactForTest(t, compact))
	other.Clean(ctx)
}
