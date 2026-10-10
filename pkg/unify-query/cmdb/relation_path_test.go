package cmdb

import "testing"

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
