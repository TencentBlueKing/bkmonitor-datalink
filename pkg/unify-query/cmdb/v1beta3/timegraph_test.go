package v1beta3

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
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

func TestTimeGraphUsesTimeSpecificExpandedMatcher(t *testing.T) {
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "node", Index: cmdb.Index{"node"}, Info: cmdb.Index{"region"}},
		{Name: "system", Index: cmdb.Index{"ip"}},
	}})
	ctx := context.Background()
	if err := tg.AddTimeNode(ctx, "node", cmdb.Matcher{"node": "n1", "region": "east"}, 100); err != nil {
		t.Fatal(err)
	}
	if err := tg.AddTimeNode(ctx, "node", cmdb.Matcher{"node": "n1", "region": "west"}, 200); err != nil {
		t.Fatal(err)
	}
	if err := tg.AddTimeRelation(ctx, "node", "system", cmdb.Matcher{"node": "n1", "ip": "10.0.0.1"}, 100, 200); err != nil {
		t.Fatal(err)
	}
	results, err := tg.FindPathResources(ctx, "node", []cmdb.Resource{"system"}, cmdb.Matcher{"node": "n1", "region": "west"}, [][]cmdb.Resource{{"node", "system"}})
	if err != nil || len(results) != 1 || results[0].Timestamp != 200 {
		t.Fatalf("time-specific expanded matcher was not respected: %v %+v", err, results)
	}
}

func TestTimeGraphTargetInfoIsRetainedOnPath(t *testing.T) {
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "node", Index: cmdb.Index{"node"}},
		{Name: "system", Index: cmdb.Index{"ip"}, Info: cmdb.Index{"zone"}},
	}})
	ctx := context.Background()
	if err := tg.AddTimeNode(ctx, "system", cmdb.Matcher{"ip": "10.0.0.1", "zone": "zone-a"}, 100); err != nil {
		t.Fatal(err)
	}
	if err := tg.AddTimeRelation(ctx, "node", "system", cmdb.Matcher{"node": "n1", "ip": "10.0.0.1"}, 100); err != nil {
		t.Fatal(err)
	}
	results, err := tg.FindPathResources(ctx, "node", []cmdb.Resource{"system"}, cmdb.Matcher{"node": "n1"}, [][]cmdb.Resource{{"node", "system"}})
	if err != nil || len(results) != 1 || results[0].Path[1].Dimensions["zone"] != "zone-a" {
		t.Fatalf("target info was dropped from path: %v %+v", err, results)
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
	if len(resourceQuery.Conditions.FieldList) != 2 || resourceQuery.Conditions.FieldList[1].DimensionName != "region" {
		t.Fatalf("expanded field was not used as a filter: %+v", resourceQuery.Conditions.FieldList)
	}
}

func TestTimeGraphResultDoesNotReferencePooledMatcherAfterClean(t *testing.T) {
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "left", Index: cmdb.Index{"id"}},
		{Name: "right", Index: cmdb.Index{"id"}},
	}})
	ctx := context.Background()
	if err := tg.AddTimeRelation(ctx, "left", "right", cmdb.Matcher{"id": "old"}, 100); err != nil {
		t.Fatal(err)
	}
	results, err := tg.FindPathResources(ctx, "left", []cmdb.Resource{"right"}, cmdb.Matcher{"id": "old"}, [][]cmdb.Resource{{"left", "right"}})
	if err != nil || len(results) != 1 {
		t.Fatalf("unexpected initial result: %v %+v", err, results)
	}
	oldDimensions := results[0].Path[0].Dimensions
	tg.Clean(ctx)
	if err := tg.AddTimeRelation(ctx, "left", "right", cmdb.Matcher{"id": "new"}, 100); err != nil {
		t.Fatal(err)
	}
	if oldDimensions["id"] != "old" {
		t.Fatalf("result dimensions were mutated after Clean: %+v", oldDimensions)
	}
}

func TestTimeGraphDynamicRelationKeepsEndpointIdentity(t *testing.T) {
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "service", Index: cmdb.Index{"id"}},
		{Name: "pod", Index: cmdb.Index{"id"}},
	}})
	ctx := context.Background()
	relation := cmdb.Relation{
		V:            []cmdb.Resource{"service", "pod"},
		RelationType: "service_to_pod_flow",
		MetricName:   "service_to_pod_flow",
		Category:     string(RelationCategoryDynamic),
	}
	if err := tg.AddTimeRelationWithRelation(ctx, relation, cmdb.Matcher{"from_id": "svc-1", "to_id": "pod-1"}, 100); err != nil {
		t.Fatal(err)
	}
	results, err := tg.FindRelationPathResources(ctx, "service", []cmdb.Resource{"pod"}, cmdb.Matcher{"id": "svc-1"}, []cmdb.RelationPath{{
		Steps: []cmdb.RelationPathStep{
			{ResourceType: "service"},
			{ResourceType: "pod", RelationType: "service_to_pod_flow", Category: string(RelationCategoryDynamic)},
		},
	}})
	if err != nil || len(results) != 1 {
		t.Fatalf("unexpected dynamic relation result: %v %+v", err, results)
	}
	if got := results[0].Path[0].Dimensions["id"]; got != "svc-1" {
		t.Fatalf("unexpected source node: %+v", results[0].Path)
	}
	if got := results[0].Path[1].Dimensions["id"]; got != "pod-1" {
		t.Fatalf("unexpected target node: %+v", results[0].Path)
	}
}

func TestMakeQueryTsUsesDynamicEndpointLabels(t *testing.T) {
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "service", Index: cmdb.Index{"id"}},
		{Name: "pod", Index: cmdb.Index{"id"}},
	}})
	query, err := tg.MakeQueryTs(
		context.Background(), "space",
		cmdb.Matcher{"id": "svc-1"},
		time.Unix(100, 0), time.Unix(100, 0), time.Minute,
		cmdb.Relation{
			V:          []cmdb.Resource{"service", "pod"},
			MetricName: "service_to_pod_flow",
			Category:   string(RelationCategoryDynamic),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fields := query.QueryList[0].Conditions.FieldList
	if len(fields) != 2 || fields[0].DimensionName != "from_id" || fields[0].Value[0] != "svc-1" || fields[1].DimensionName != "to_id" || fields[1].Operator != structured.ConditionNotEqual {
		t.Fatalf("unexpected dynamic relation conditions: %+v", fields)
	}
}

func TestTimeGraphDynamicSelfRelationUsesDirectionForInboundQuery(t *testing.T) {
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{
		Resource: []TimeGraphResourceConfig{{Name: "service", Index: cmdb.Index{"id"}}},
		Relation: []TimeGraphRelationConfig{{
			Resources:    []cmdb.Resource{"service", "service"},
			RelationType: "service_to_service",
			MetricName:   "service_to_service_flow",
			Category:     string(RelationCategoryDynamic),
		}},
	})
	ctx := context.Background()
	relation := cmdb.Relation{
		V:            []cmdb.Resource{"service", "service"},
		RelationType: "service_to_service",
		MetricName:   "service_to_service_flow",
		Category:     string(RelationCategoryDynamic),
		Direction:    string(DirectionInbound),
	}
	if err := tg.AddTimeRelationWithRelation(ctx, relation, cmdb.Matcher{
		"from_id": "caller",
		"to_id":   "callee",
	}, 100); err != nil {
		t.Fatal(err)
	}

	results, err := tg.FindRelationPathResources(ctx, "service", []cmdb.Resource{"service"}, cmdb.Matcher{"id": "callee"}, []cmdb.RelationPath{{
		Steps: []cmdb.RelationPathStep{
			{ResourceType: "service"},
			{ResourceType: "service", RelationType: "service_to_service", Category: string(RelationCategoryDynamic), Direction: string(DirectionInbound)},
		},
	}})
	if err != nil || len(results) != 1 {
		t.Fatalf("unexpected inbound self relation result: %v %+v", err, results)
	}
	if got := results[0].Path[0].Dimensions["id"]; got != "callee" {
		t.Fatalf("unexpected inbound source: %+v", results[0].Path)
	}
	if got := results[0].Path[1].Dimensions["id"]; got != "caller" {
		t.Fatalf("unexpected inbound target: %+v", results[0].Path)
	}
}

func TestTimeGraphRangeQuerySeparatesStepAndLookback(t *testing.T) {
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "left", Index: cmdb.Index{"id"}},
		{Name: "right", Index: cmdb.Index{"id"}},
	}})
	query, err := tg.MakeQueryTsWithWindow(
		context.Background(), "space", nil,
		time.Unix(100, 0), time.Unix(200, 0), time.Minute, 10*time.Minute,
		cmdb.Relation{V: []cmdb.Resource{"left", "right"}, MetricName: "left_to_right"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(query.QueryList[0].TimeAggregation.Window); got != "10m0s" {
		t.Fatalf("unexpected relation lookback window: %s", got)
	}
	if query.Step != "1m0s" {
		t.Fatalf("unexpected range step: %s", query.Step)
	}

	infoQuery, err := tg.MakeResourceInfoQueryTsWithWindow(
		"space", "left", nil, nil, nil,
		time.Unix(100, 0), time.Unix(200, 0), time.Minute, 10*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(infoQuery.QueryList[0].TimeAggregation.Window); got != "10m0s" || infoQuery.Step != "1m0s" {
		t.Fatalf("unexpected resource info query timing: %+v", infoQuery)
	}
}

func TestTimeGraphSingleNodePathRequiresTimestampPresence(t *testing.T) {
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "node", Index: cmdb.Index{"id"}}}})
	ctx := context.Background()
	if err := tg.AddTimeNode(ctx, "node", cmdb.Matcher{"id": "n1"}, 100); err != nil {
		t.Fatal(err)
	}
	if err := tg.AddTimeNode(ctx, "node", cmdb.Matcher{"id": "n2"}, 200); err != nil {
		t.Fatal(err)
	}
	results, err := tg.FindRelationPathResources(ctx, "node", []cmdb.Resource{"node"}, nil, []cmdb.RelationPath{{
		Steps: []cmdb.RelationPathStep{{ResourceType: "node"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected one node per timestamp, got %+v", results)
	}
	if results[0].Timestamp != 100 || results[0].Path[0].Dimensions["id"] != "n1" || results[1].Timestamp != 200 || results[1].Path[0].Dimensions["id"] != "n2" {
		t.Fatalf("unexpected timestamp-specific nodes: %+v", results)
	}
}

func TestTimeGraphLimitsTimeSpecificNodeInfos(t *testing.T) {
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{
		Resource:     []TimeGraphResourceConfig{{Name: "node", Index: cmdb.Index{"id"}}},
		MaxNodeInfos: 1,
	})
	ctx := context.Background()
	if err := tg.AddTimeNode(ctx, "node", cmdb.Matcher{"id": "n1"}, 100); err != nil {
		t.Fatal(err)
	}
	if err := tg.AddTimeNode(ctx, "node", cmdb.Matcher{"id": "n1"}, 200); err == nil {
		t.Fatal("expected time-specific node info limit")
	}
}

func TestTimeGraphResourceInfoQueryFiltersExactPrimaryTuples(t *testing.T) {
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{
		Name: "pod", Index: cmdb.Index{"cluster", "pod"}, Info: cmdb.Index{"version"},
	}}})
	query, err := tg.MakeResourceInfoQueryTsWithWindow(
		"space", "pod", nil, nil,
		[]cmdb.Matcher{{"cluster": "c1", "pod": "p1"}, {"cluster": "c2", "pod": "p2"}},
		time.Unix(100, 0), time.Unix(200, 0), time.Minute, 10*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	conditions := query.QueryList[0].Conditions
	if len(conditions.FieldList) != 4 || len(conditions.ConditionList) != 3 {
		t.Fatalf("unexpected target filter shape: %+v", conditions)
	}
	if conditions.ConditionList[0] != structured.ConditionAnd || conditions.ConditionList[1] != structured.ConditionOr || conditions.ConditionList[2] != structured.ConditionAnd {
		t.Fatalf("target primary tuples were not grouped exactly: %+v", conditions.ConditionList)
	}
}
