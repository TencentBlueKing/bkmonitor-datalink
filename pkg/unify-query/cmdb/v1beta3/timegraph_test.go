package v1beta3

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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

func TestNodeIdentityLengthPrefixesPrimaryKeyTuple(t *testing.T) {
	tests := []struct {
		name  string
		infos []cmdb.Matcher
		want  []cmdb.Matcher
	}{
		{
			name:  "ordinary_values",
			infos: []cmdb.Matcher{{"a": "x", "b": "y"}},
			want:  []cmdb.Matcher{{"a": "x", "b": "y"}},
		},
		{
			name: "delimiter_values_remain_distinct",
			infos: []cmdb.Matcher{
				{"a": "x|b=y", "b": "z"},
				{"a": "x", "b": "y|b=z"},
			},
			want: []cmdb.Matcher{
				{"a": "x|b=y", "b": "z"},
				{"a": "x", "b": "y|b=z"},
			},
		},
		{
			name:  "repeated_primary_key_is_deduplicated",
			infos: []cmdb.Matcher{{"a": "x", "b": "y"}, {"a": "x", "b": "y"}},
			want:  []cmdb.Matcher{{"a": "x", "b": "y"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "node", Index: cmdb.Index{"a", "b"}}}})
			for _, info := range tc.infos {
				require.NoError(t, tg.AddTimeNode(context.Background(), "node", info, 100))
			}
			got := tg.GetNodesByResourceType("node")
			require.ElementsMatch(t, tc.want, got)
		})
	}
}

func TestTimeGraphSameTimestampAttributeConflictIsStable(t *testing.T) {
	tests := []struct {
		name     string
		versions []string
	}{
		{name: "v1_then_v2", versions: []string{"v1", "v2"}},
		{name: "v2_then_v1", versions: []string{"v2", "v1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "node", Index: cmdb.Index{"id"}, Info: cmdb.Index{"version"}}}})
			for _, version := range tc.versions {
				if err := tg.AddTimeNode(context.Background(), "node", cmdb.Matcher{"id": "m1", "version": version}, 100); err != nil {
					t.Fatal(err)
				}
			}
			results, err := tg.FindRelationPathResources(context.Background(), "node", []cmdb.Resource{"node"}, cmdb.Matcher{"id": "m1"}, []cmdb.RelationPath{{
				Steps: []cmdb.RelationPathStep{{ResourceType: "node"}},
			}})
			if err != nil || len(results) != 1 {
				t.Fatalf("unexpected result: %v %+v", err, results)
			}
			if got := results[0].Path[0].Dimensions["version"]; got != "v2" {
				t.Fatalf("attribute conflict depended on VM order: got %q", got)
			}
		})
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

func TestTimeGraphResultMatcherOwnership(t *testing.T) {
	tests := []struct {
		name         string
		mutateInput  bool
		mutateResult bool
		rebuildAfter bool
	}{
		{name: "input_map", mutateInput: true},
		{name: "returned_map", mutateResult: true},
		{name: "clean_then_rebuild", rebuildAfter: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
				{Name: "left", Index: cmdb.Index{"id"}},
				{Name: "right", Index: cmdb.Index{"id"}},
			}})
			input := cmdb.Matcher{"id": "old"}
			require.NoError(t, tg.AddTimeRelation(ctx, "left", "right", input, 100))
			if tc.mutateInput {
				input["id"] = "modified"
			}
			results, err := tg.FindPathResources(ctx, "left", []cmdb.Resource{"right"}, cmdb.Matcher{"id": "old"}, [][]cmdb.Resource{{"left", "right"}})
			require.NoError(t, err)
			require.Len(t, results, 1)
			if tc.mutateResult {
				results[0].Path[0].Dimensions["id"] = "modified"
			}
			if tc.rebuildAfter {
				tg.Clean(ctx)
				require.NoError(t, tg.AddTimeRelation(ctx, "left", "right", cmdb.Matcher{"id": "new"}, 100))
				require.Equal(t, "old", results[0].Path[0].Dimensions["id"])
				return
			}
			results, err = tg.FindPathResources(ctx, "left", []cmdb.Resource{"right"}, cmdb.Matcher{"id": "old"}, [][]cmdb.Resource{{"left", "right"}})
			require.NoError(t, err)
			require.Len(t, results, 1)
			require.Equal(t, "old", results[0].Path[0].Dimensions["id"])
		})
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

func TestTimeGraphSameTypeDynamicInboundAndOutboundCoexist(t *testing.T) {
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "service", Index: cmdb.Index{"id"}},
	}})
	ctx := context.Background()
	outbound := cmdb.Relation{
		V:            []cmdb.Resource{"service", "service"},
		RelationType: "service_to_service",
		MetricName:   "service_to_service_flow",
		Category:     string(RelationCategoryDynamic),
		Direction:    string(DirectionOutbound),
	}
	inbound := outbound
	inbound.Direction = string(DirectionInbound)
	if err := tg.AddTimeRelationWithRelation(ctx, outbound, cmdb.Matcher{"from_id": "caller", "to_id": "callee"}, 100); err != nil {
		t.Fatal(err)
	}
	if err := tg.AddTimeRelationWithRelation(ctx, inbound, cmdb.Matcher{"from_id": "other", "to_id": "caller"}, 100); err != nil {
		t.Fatal(err)
	}

	paths := []cmdb.RelationPath{
		{Steps: []cmdb.RelationPathStep{
			{ResourceType: "service"},
			{ResourceType: "service", RelationType: "service_to_service", Category: string(RelationCategoryDynamic), Direction: string(DirectionOutbound)},
		}},
		{Steps: []cmdb.RelationPathStep{
			{ResourceType: "service"},
			{ResourceType: "service", RelationType: "service_to_service", Category: string(RelationCategoryDynamic), Direction: string(DirectionInbound)},
		}},
	}
	results, err := tg.FindRelationPathResources(ctx, "service", []cmdb.Resource{"service"}, cmdb.Matcher{"id": "caller"}, paths)
	require.NoError(t, err)
	require.Equal(t, []PathResourcesResult{
		{Timestamp: 100, TargetType: "service", Path: []cmdb.PathNode{{ResourceType: "service", Dimensions: cmdb.Matcher{"id": "caller"}}, {ResourceType: "service", Dimensions: cmdb.Matcher{"id": "callee"}}}},
		{Timestamp: 100, TargetType: "service", Path: []cmdb.PathNode{{ResourceType: "service", Dimensions: cmdb.Matcher{"id": "caller"}}, {ResourceType: "service", Dimensions: cmdb.Matcher{"id": "other"}}}},
	}, results)
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

func TestTimeGraphResourceInfoQuerySeparatesStepAndLookback(t *testing.T) {
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "left", Index: cmdb.Index{"id"}},
		{Name: "right", Index: cmdb.Index{"id"}},
	}})
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
	type sample struct {
		at int64
		id string
	}
	tests := []struct {
		name    string
		samples []sample
		want    []PathResourcesResult
	}{
		{
			name:    "same_node_present_at_both_timestamps",
			samples: []sample{{at: 100, id: "n1"}, {at: 200, id: "n1"}},
			want: []PathResourcesResult{
				{Timestamp: 100, TargetType: "node", Path: []cmdb.PathNode{{ResourceType: "node", Dimensions: cmdb.Matcher{"id": "n1"}}}},
				{Timestamp: 200, TargetType: "node", Path: []cmdb.PathNode{{ResourceType: "node", Dimensions: cmdb.Matcher{"id": "n1"}}}},
			},
		},
		{
			name:    "nodes_are_visible_only_at_their_own_timestamps",
			samples: []sample{{at: 100, id: "n1"}, {at: 200, id: "n2"}},
			want: []PathResourcesResult{
				{Timestamp: 100, TargetType: "node", Path: []cmdb.PathNode{{ResourceType: "node", Dimensions: cmdb.Matcher{"id": "n1"}}}},
				{Timestamp: 200, TargetType: "node", Path: []cmdb.PathNode{{ResourceType: "node", Dimensions: cmdb.Matcher{"id": "n2"}}}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "node", Index: cmdb.Index{"id"}}}})
			t.Cleanup(func() { tg.Clean(ctx) })
			for _, item := range tc.samples {
				if err := tg.AddTimeNode(ctx, "node", cmdb.Matcher{"id": item.id}, item.at); err != nil {
					t.Fatal(err)
				}
			}
			got, err := tg.FindRelationPathResources(ctx, "node", []cmdb.Resource{"node"}, nil, []cmdb.RelationPath{{
				Steps: []cmdb.RelationPathStep{{ResourceType: "node"}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tc.want, got) {
				t.Fatalf("unexpected timestamp-specific nodes: want=%+v got=%+v", tc.want, got)
			}
		})
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
	wantFields := []structured.ConditionField{
		{DimensionName: "cluster", Value: []string{"c1"}, Operator: structured.ConditionEqual},
		{DimensionName: "pod", Value: []string{"p1"}, Operator: structured.ConditionEqual},
		{DimensionName: "cluster", Value: []string{"c2"}, Operator: structured.ConditionEqual},
		{DimensionName: "pod", Value: []string{"p2"}, Operator: structured.ConditionEqual},
	}
	wantConditions := structured.Conditions{
		FieldList:     wantFields,
		ConditionList: []string{structured.ConditionAnd, structured.ConditionOr, structured.ConditionAnd},
	}
	if !reflect.DeepEqual(conditions, wantConditions) {
		t.Fatalf("unexpected target filter: want=%+v got=%+v", wantConditions, conditions)
	}

	groups, err := conditions.AnalysisConditions()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		matcher cmdb.Matcher
		want    bool
	}{
		{name: "first_tuple_matches", matcher: cmdb.Matcher{"cluster": "c1", "pod": "p1"}, want: true},
		{name: "second_tuple_matches", matcher: cmdb.Matcher{"cluster": "c2", "pod": "p2"}, want: true},
		{name: "cross_tuple_one_does_not_match", matcher: cmdb.Matcher{"cluster": "c1", "pod": "p2"}, want: false},
		{name: "cross_tuple_two_does_not_match", matcher: cmdb.Matcher{"cluster": "c2", "pod": "p1"}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matched := false
			for _, group := range groups {
				groupMatched := true
				for _, field := range group {
					if len(field.Value) != 1 || tc.matcher[field.DimensionName] != field.Value[0] {
						groupMatched = false
						break
					}
				}
				if groupMatched {
					matched = true
					break
				}
			}
			if matched != tc.want {
				t.Fatalf("matcher %v matched=%v, want=%v; groups=%+v", tc.matcher, matched, tc.want, groups)
			}
		})
	}
}
