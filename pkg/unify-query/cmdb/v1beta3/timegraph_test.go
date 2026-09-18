package v1beta3

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

func TestTimeGraphFindShortestPathAcrossTimestamps(t *testing.T) {
	config := &TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "pod", Index: cmdb.Index{"cluster", "namespace", "pod"}},
		{Name: "node", Index: cmdb.Index{"cluster", "node"}},
		{Name: "system", Index: cmdb.Index{"ip"}},
	}}

	tg := NewTimeGraphWithConfig(config)
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
	r := &Model{}
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
	config := &TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "pod", Index: cmdb.Index{"cluster", "namespace", "pod"}},
		{Name: "node", Index: cmdb.Index{"cluster", "node"}},
		{Name: "system", Index: cmdb.Index{"ip"}},
		{Name: "service", Index: cmdb.Index{"cluster", "service"}},
	}}

	info := cmdb.Matcher{
		"cluster":   "c1",
		"namespace": "default",
		"pod":       "p1",
		"node":      "n1",
		"ip":        "10.0.0.1",
		"service":   "svc1",
	}
	tg := NewTimeGraphWithConfig(config)
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
	config := &TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "left", Index: cmdb.Index{"id"}},
		{Name: "right", Index: cmdb.Index{"id"}},
	}}

	tg := NewTimeGraphWithConfig(config)
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

func TestTimeGraphRelationTypeConstrainsSameEndpoint(t *testing.T) {
	config := &TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "left", Index: cmdb.Index{"id"}},
		{Name: "right", Index: cmdb.Index{"id"}},
	}}
	tg := NewTimeGraphWithConfig(config)
	ctx := context.Background()
	info := cmdb.Matcher{"id": "same"}
	if err := tg.AddTimeRelationWithRelation(ctx, cmdb.Relation{
		V:            []cmdb.Resource{"left", "right"},
		RelationType: "relation_a",
		MetricName:   "relation_a_metric",
	}, info, 100); err != nil {
		t.Fatal(err)
	}
	if err := tg.AddTimeRelationWithRelation(ctx, cmdb.Relation{
		V:            []cmdb.Resource{"left", "right"},
		RelationType: "relation_b",
		MetricName:   "relation_b_metric",
	}, info, 100); err != nil {
		t.Fatal(err)
	}

	results, err := tg.FindRelationPathResources(ctx, "left", []cmdb.Resource{"right"}, info, []cmdb.RelationPath{{
		Steps: []cmdb.RelationPathStep{
			{ResourceType: "left"},
			{ResourceType: "right", RelationType: "relation_b"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected relation_b path, got %+v", results)
	}

	results, err = tg.FindRelationPathResources(ctx, "left", []cmdb.Resource{"right"}, info, []cmdb.RelationPath{{
		Steps: []cmdb.RelationPathStep{
			{ResourceType: "left"},
			{ResourceType: "right", RelationType: "relation_c"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("unexpected unmatched relation path: %+v", results)
	}
}

func TestTimeGraphUsesModelResourceConfigAndMetricName(t *testing.T) {
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "left", Index: cmdb.Index{"left_id"}},
		{Name: "right", Index: cmdb.Index{"right_id"}},
	}})
	query, err := tg.MakeQueryTs(
		context.Background(),
		"space",
		cmdb.Matcher{"left_id": "l1", "right_id": "r1"},
		time.Unix(100, 0),
		time.Unix(100, 0),
		time.Minute,
		cmdb.Relation{V: []cmdb.Resource{"left", "right"}, MetricName: "custom_relation_metric"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if query == nil || len(query.QueryList) != 1 || query.QueryList[0].FieldName != "custom_relation_metric" {
		t.Fatalf("unexpected custom relation query: %+v", query)
	}
}

func TestTimeGraphSourceInfoNodeSupportsExpandedMatcher(t *testing.T) {
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "node", Index: cmdb.Index{"node"}, Info: cmdb.Index{"region"}},
		{Name: "system", Index: cmdb.Index{"ip"}},
	}})
	ctx := context.Background()
	if err := tg.AddTimeNode(ctx, "node", cmdb.Matcher{"node": "n1", "region": "east"}, 100); err != nil {
		t.Fatal(err)
	}
	if err := tg.AddTimeRelation(ctx, "node", "system", cmdb.Matcher{"node": "n1", "ip": "10.0.0.1"}, 100); err != nil {
		t.Fatal(err)
	}

	results, err := tg.FindPathResources(
		ctx,
		"node",
		[]cmdb.Resource{"system"},
		cmdb.Matcher{"node": "n1", "region": "east"},
		[][]cmdb.Resource{{"node", "system"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Path[0].Dimensions["region"] != "east" {
		t.Fatalf("expanded source matcher did not select enriched node: %+v", results)
	}
}

func TestMakeResourceInfoQueryTsKeepsExpandedFields(t *testing.T) {
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "node", Index: cmdb.Index{"node"}, Info: cmdb.Index{"region"}},
	}})
	query, err := tg.MakeResourceInfoQueryTs(
		"space",
		"node",
		map[string]string{"node": "n1"},
		map[string]string{"region": "east"},
		time.Unix(100, 0),
		time.Unix(100, 0),
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if query == nil || len(query.QueryList) != 1 {
		t.Fatalf("unexpected resource info query: %+v", query)
	}
	resourceQuery := query.QueryList[0]
	if resourceQuery.FieldName != "node_info_relation" {
		t.Fatalf("unexpected resource info metric: %+v", resourceQuery)
	}
	if len(resourceQuery.AggregateMethodList) != 1 || len(resourceQuery.AggregateMethodList[0].Dimensions) != 2 {
		t.Fatalf("resource info query dropped expanded dimensions: %+v", resourceQuery.AggregateMethodList)
	}
	if resourceQuery.AggregateMethodList[0].Dimensions[0] != "node" || resourceQuery.AggregateMethodList[0].Dimensions[1] != "region" {
		t.Fatalf("unexpected resource info dimensions: %+v", resourceQuery.AggregateMethodList[0].Dimensions)
	}
}
