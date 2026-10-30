package cmdb

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRelationPathHelpersPreservePathOrder(t *testing.T) {
	paths := [][]Resource{{"pod", "node", "system"}, {"pod", "service"}}
	relationPaths := RelationPathsFromResourcePaths(paths)
	if got := ResourcePathsFromRelationPaths(relationPaths); len(got) != len(paths) || got[0][1] != "node" || got[1][1] != "service" {
		t.Fatalf("unexpected round trip paths: %+v", got)
	}

	rank := PathRank([]PathNode{{ResourceType: "pod"}, {ResourceType: "service"}}, paths)
	if rank != 1 {
		t.Fatalf("expected second candidate rank, got %d", rank)
	}
	if got := PathResourceTypes([]PathNode{{ResourceType: "pod"}, {ResourceType: "service"}}); len(got) != 2 || got[1] != "service" {
		t.Fatalf("unexpected path resource types: %+v", got)
	}
}

func TestRelationPathHelpersEmptyAndMismatchedCandidates(t *testing.T) {
	for _, paths := range [][][]Resource{nil, {}, {{}}, {{"pod"}}, {{"pod", "node"}, {}, {"pod", "service"}}} {
		relationPaths := RelationPathsFromResourcePaths(paths)
		require.Len(t, relationPaths, len(paths))
		for i, path := range paths {
			require.Len(t, relationPaths[i].Steps, len(path))
			for j, resource := range path {
				require.Equal(t, RelationPathStep{ResourceType: resource}, relationPaths[i].Steps[j])
			}
		}
		roundTrip := ResourcePathsFromRelationPaths(relationPaths)
		require.Len(t, roundTrip, len(paths))
		for i := range paths {
			require.Equal(t, paths[i], roundTrip[i])
		}
	}
	path := []PathNode{{ResourceType: "pod"}, {ResourceType: "service"}}
	for _, tc := range []struct {
		candidates [][]Resource
		want       int
	}{
		{nil, 0}, {[][]Resource{{"pod", "service"}}, 0},
		{[][]Resource{{"pod", "node"}, {"pod", "service"}}, 1},
		{[][]Resource{{"node", "service"}, {"pod"}, {"pod", "service"}}, 2},
		{[][]Resource{{"pod", "node"}}, 1},
	} {
		require.Equal(t, tc.want, PathRank(path, tc.candidates))
	}
	require.Equal(t, 2, PathRank(nil, [][]Resource{{}, {"pod"}}))
	require.Empty(t, PathResourceTypes(nil))
	require.Equal(t, []string{"pod"}, PathResourceTypes(path[:1]))
}
