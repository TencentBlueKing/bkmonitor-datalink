package cmdb

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRelationPathHelpersPreservePathOrder(t *testing.T) {
	paths := [][]Resource{{"pod", "node", "system"}, {"pod", "service"}}
	relationPaths := RelationPathsFromResourcePaths(paths)
	require.Equal(t, []RelationPath{
		{Steps: []RelationPathStep{{ResourceType: "pod"}, {ResourceType: "node"}, {ResourceType: "system"}}},
		{Steps: []RelationPathStep{{ResourceType: "pod"}, {ResourceType: "service"}}},
	}, relationPaths)

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
	}
}
