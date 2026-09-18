package v1beta1

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

func TestTimeGraphFindShortestPathAcrossTimestamps(t *testing.T) {
	updateResourceConfig(&Config{Resource: []ResourceConf{
		{Name: "pod", Index: cmdb.Index{"cluster", "namespace", "pod"}},
		{Name: "node", Index: cmdb.Index{"cluster", "node"}},
		{Name: "system", Index: cmdb.Index{"ip"}},
	}})
	t.Cleanup(func() { updateResourceConfig(configData) })

	tg := NewTimeGraph()
	info := cmdb.Matcher{
		"cluster":   "c1",
		"namespace": "default",
		"pod":       "p1",
		"node":      "n1",
		"ip":        "10.0.0.1",
	}
	ctx := context.Background()
	if err := tg.AddTimeRelation(ctx, "pod", "node", info, 100, 200); err != nil {
		t.Fatal(err)
	}
	if err := tg.AddTimeRelation(ctx, "node", "system", info, 100); err != nil {
		t.Fatal(err)
	}

	results, err := tg.FindShortestPath(ctx, "pod", "system", cmdb.Matcher{"namespace": "default", "pod": "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Timestamp != 100 {
		t.Fatalf("unexpected timegraph results: %+v", results)
	}
	if len(results[0].Path) != 3 || results[0].Path[1].ResourceType != "node" {
		t.Fatalf("unexpected path: %+v", results[0].Path)
	}
}

func TestBuildRelationsFromPathsDeduplicatesEdges(t *testing.T) {
	r := &model{}
	relations := r.buildRelationsFromPaths([][]cmdb.Resource{
		{"pod", "node", "system"},
		{"pod", "node", "system"},
		{"pod", "service"},
	})
	if len(relations) != 3 {
		t.Fatalf("expected 3 unique relations, got %d: %+v", len(relations), relations)
	}
}

func TestTimeGraphFindPathResourcesHonorsExpectedPath(t *testing.T) {
	updateResourceConfig(&Config{Resource: []ResourceConf{
		{Name: "pod", Index: cmdb.Index{"cluster", "namespace", "pod"}},
		{Name: "node", Index: cmdb.Index{"cluster", "node"}},
		{Name: "system", Index: cmdb.Index{"ip"}},
		{Name: "service", Index: cmdb.Index{"cluster", "service"}},
	}})
	t.Cleanup(func() { updateResourceConfig(configData) })

	info := cmdb.Matcher{
		"cluster":   "c1",
		"namespace": "default",
		"pod":       "p1",
		"node":      "n1",
		"ip":        "10.0.0.1",
		"service":   "svc1",
	}
	tg := NewTimeGraph()
	ctx := context.Background()
	for _, relation := range [][2]cmdb.Resource{
		{"pod", "node"},
		{"node", "system"},
		{"pod", "service"},
		{"service", "system"},
	} {
		if err := tg.AddTimeRelation(ctx, relation[0], relation[1], info, 100); err != nil {
			t.Fatal(err)
		}
	}

	results, err := tg.FindPathResources(
		ctx,
		"pod",
		[]cmdb.Resource{"system"},
		cmdb.Matcher{"namespace": "default", "pod": "p1"},
		[][]cmdb.Resource{{"pod", "node", "system"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("unexpected constrained path results: %+v", results)
	}
	if got := []cmdb.Resource{results[0].Path[0].ResourceType, results[0].Path[1].ResourceType, results[0].Path[2].ResourceType}; got[0] != "pod" || got[1] != "node" || got[2] != "system" {
		t.Fatalf("unexpected constrained path: %+v", got)
	}
}

func TestNodeIdentityIncludesResourceType(t *testing.T) {
	updateResourceConfig(&Config{Resource: []ResourceConf{
		{Name: "left", Index: cmdb.Index{"id"}},
		{Name: "right", Index: cmdb.Index{"id"}},
	}})
	t.Cleanup(func() { updateResourceConfig(configData) })

	tg := NewTimeGraph()
	if err := tg.AddTimeRelation(context.Background(), "left", "right", cmdb.Matcher{"id": "same"}, 100); err != nil {
		t.Fatal(err)
	}
	results, err := tg.FindPathResources(
		context.Background(),
		"left",
		[]cmdb.Resource{"right"},
		cmdb.Matcher{"id": "same"},
		[][]cmdb.Resource{{"left", "right"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("resource type collision collapsed the edge: %+v", results)
	}
}
