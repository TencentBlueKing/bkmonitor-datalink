// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

// contractSchemaProvider makes the schema that matters to a case visible next
// to the request and VM response. It deliberately does not hide configuration
// in a setup callback.
type contractSchemaProvider struct {
	resources []ResourceType
	primary   map[ResourceType][]string
	fields    map[ResourceType][]string
	schemas   []RelationSchema
}

func (p contractSchemaProvider) GetResourcePrimaryKeys(_ string, resourceType ResourceType) []string {
	return append([]string(nil), p.primary[resourceType]...)
}

func (p contractSchemaProvider) GetResourceFields(_ string, resourceType ResourceType) []string {
	return append([]string(nil), p.fields[resourceType]...)
}

func (p contractSchemaProvider) ListResourceTypes(string) []ResourceType {
	return append([]ResourceType(nil), p.resources...)
}

func (p contractSchemaProvider) ListRelationSchemas(string) []RelationSchema {
	return append([]RelationSchema(nil), p.schemas...)
}

type contractQueryWant struct {
	field      string
	window     string
	step       string
	start      string
	end        string
	conditions structured.Conditions
}

type recordedContractQuery struct {
	field      string
	window     string
	step       string
	start      string
	end        string
	conditions structured.Conditions
}

type contractVM struct {
	responses map[string]pl.Matrix
	calls     []recordedContractQuery
}

func (vm *contractVM) query(_ context.Context, queryTs *structured.QueryTs) (pl.Matrix, error) {
	if queryTs == nil || len(queryTs.QueryList) != 1 || queryTs.QueryList[0] == nil {
		return nil, fmt.Errorf("unexpected empty timegraph query: %#v", queryTs)
	}
	query := queryTs.QueryList[0]
	vm.calls = append(vm.calls, recordedContractQuery{
		field:      query.FieldName,
		window:     string(query.TimeAggregation.Window),
		step:       queryTs.Step,
		start:      queryTs.Start,
		end:        queryTs.End,
		conditions: cloneContractConditions(query.Conditions),
	})
	response, ok := vm.responses[query.FieldName]
	if !ok {
		return nil, fmt.Errorf("unexpected VM query metric %q", query.FieldName)
	}
	return response, nil
}

func cloneContractConditions(conditions structured.Conditions) structured.Conditions {
	result := structured.Conditions{
		FieldList:     make([]structured.ConditionField, len(conditions.FieldList)),
		ConditionList: append([]string(nil), conditions.ConditionList...),
	}
	for i, field := range conditions.FieldList {
		result.FieldList[i] = field
		result.FieldList[i].Value = append([]string(nil), field.Value...)
	}
	return result
}

func contractMatrix(metric map[string]string, timestamps ...int64) pl.Matrix {
	metricLabels := make(labels.Labels, 0, len(metric))
	for name, value := range metric {
		metricLabels = append(metricLabels, labels.Label{Name: name, Value: value})
	}
	sort.Slice(metricLabels, func(i, j int) bool { return metricLabels[i].Name < metricLabels[j].Name })
	points := make([]pl.Point, 0, len(timestamps))
	for _, timestamp := range timestamps {
		points = append(points, pl.Point{T: timestamp, V: 1})
	}
	return pl.Matrix{{Metric: metricLabels, Points: points}}
}

func contractDynamicProvider(resourceType ResourceType, metricName string) SchemaProvider {
	return contractSchemaProvider{
		resources: []ResourceType{resourceType},
		primary:   map[ResourceType][]string{resourceType: {"id"}},
		fields:    map[ResourceType][]string{resourceType: {"id"}},
		schemas: []RelationSchema{{
			RelationType:  RelationType("service_to_service"),
			Category:      RelationCategoryDynamic,
			FromType:      resourceType,
			ToType:        resourceType,
			IsDirectional: true,
			MetricName:    metricName,
		}},
	}
}

func TestTimeGraphContractCases(t *testing.T) {
	startSec := int64(100)
	instantStart := time.Unix(startSec, 0)
	timestampMS := startSec * 1000
	defaultProvider := NewSchemaProviderFromRelation(relation.NewDefaultStaticSchemaProvider())
	serviceProvider := contractDynamicProvider("service", "service_to_service_flow")
	staticProvider := contractSchemaProvider{
		resources: []ResourceType{"left", "right"},
		primary: map[ResourceType][]string{
			"left":  {"left_id"},
			"right": {"right_id"},
		},
		fields: map[ResourceType][]string{
			"left":  {"left_id"},
			"right": {"right_id"},
		},
		schemas: []RelationSchema{{
			RelationType: RelationType("left_to_right"),
			Category:     RelationCategoryStatic,
			FromType:     "left",
			ToType:       "right",
			MetricName:   "left_to_right_flow",
		}},
	}
	enrichmentProvider := contractSchemaProvider{
		resources: []ResourceType{"node", "container"},
		primary: map[ResourceType][]string{
			"node":      {"node_id"},
			"container": {"container_id"},
		},
		fields: map[ResourceType][]string{
			"node":      {"node_id"},
			"container": {"container_id", "version"},
		},
		schemas: []RelationSchema{{
			RelationType: RelationType("node_to_container"),
			Category:     RelationCategoryStatic,
			FromType:     "node",
			ToType:       "container",
			MetricName:   "node_to_container_flow",
		}},
	}
	dynamicChainProvider := contractSchemaProvider{
		resources: []ResourceType{"front", "middle", "back"},
		primary: map[ResourceType][]string{
			"front":  {"id"},
			"middle": {"id"},
			"back":   {"id"},
		},
		fields: map[ResourceType][]string{
			"front":  {"id"},
			"middle": {"id"},
			"back":   {"id"},
		},
		schemas: []RelationSchema{
			{RelationType: RelationType("front_to_middle"), Category: RelationCategoryDynamic, FromType: "front", ToType: "middle", IsDirectional: true, MetricName: "front_to_middle_flow"},
			{RelationType: RelationType("middle_to_back"), Category: RelationCategoryDynamic, FromType: "middle", ToType: "back", IsDirectional: true, MetricName: "middle_to_back_flow"},
		},
	}

	tests := []struct {
		name             string
		provider         SchemaProvider
		path             cmdb.RelationPath
		sourceType       cmdb.Resource
		targetTypes      []cmdb.Resource
		sourceInfo       cmdb.Matcher
		sourceExpandInfo cmdb.Matcher
		start            time.Time
		end              time.Time
		step             time.Duration
		lookBackDelta    string
		targetInfoShow   bool
		maxNodes         int
		responses        map[string]pl.Matrix
		wantErr          string
		wantQueries      []contractQueryWant
		wantResults      []PathResourcesResult
	}{
		{
			name:        "default_dynamic_metric_name_is_used",
			provider:    defaultProvider,
			sourceType:  "pod",
			targetTypes: []cmdb.Resource{"system"},
			path: cmdb.RelationPath{Steps: []cmdb.RelationPathStep{
				{ResourceType: "pod"},
				{ResourceType: "system", RelationType: "pod_to_system", Category: string(RelationCategoryDynamic), Direction: string(DirectionOutbound)},
			}},
			sourceInfo:    cmdb.Matcher{"bcs_cluster_id": "c1", "namespace": "ns", "pod": "p1"},
			start:         instantStart,
			end:           instantStart,
			step:          5 * time.Minute,
			lookBackDelta: "10m",
			responses: map[string]pl.Matrix{
				"pod_to_system_flow": contractMatrix(map[string]string{
					"from_bcs_cluster_id": "c1", "from_namespace": "ns", "from_pod": "p1", "to_bk_target_ip": "10.0.0.1",
				}, timestampMS),
			},
			wantQueries: []contractQueryWant{{
				field: "pod_to_system_flow", window: "10m0s", step: "5m0s", start: "100", end: "100",
				conditions: structured.Conditions{
					FieldList: []structured.ConditionField{
						{DimensionName: "from_bcs_cluster_id", Value: []string{"c1"}, Operator: structured.ConditionEqual},
						{DimensionName: "from_namespace", Value: []string{"ns"}, Operator: structured.ConditionEqual},
						{DimensionName: "from_pod", Value: []string{"p1"}, Operator: structured.ConditionEqual},
						{DimensionName: "to_bk_target_ip", Value: []string{""}, Operator: structured.ConditionNotEqual},
					},
					ConditionList: []string{structured.ConditionAnd, structured.ConditionAnd, structured.ConditionAnd},
				},
			}},
			wantResults: []PathResourcesResult{{
				Timestamp: timestampMS, TargetType: "system", Path: []cmdb.PathNode{
					{ResourceType: "pod", Dimensions: cmdb.Matcher{"bcs_cluster_id": "c1", "namespace": "ns", "pod": "p1"}},
					{ResourceType: "system", Dimensions: cmdb.Matcher{"bk_target_ip": "10.0.0.1"}},
				},
			}},
		},
		{
			name:        "same_type_dynamic_outbound_uses_from_endpoint",
			provider:    serviceProvider,
			sourceType:  "service",
			targetTypes: []cmdb.Resource{"service"},
			path: cmdb.RelationPath{Steps: []cmdb.RelationPathStep{
				{ResourceType: "service"},
				{ResourceType: "service", RelationType: "service_to_service", Category: string(RelationCategoryDynamic), Direction: string(DirectionOutbound)},
			}},
			sourceInfo: cmdb.Matcher{"id": "caller"},
			start:      instantStart, end: instantStart, step: time.Minute, lookBackDelta: "10m",
			responses: map[string]pl.Matrix{"service_to_service_flow": contractMatrix(map[string]string{"from_id": "caller", "to_id": "callee"}, timestampMS)},
			wantQueries: []contractQueryWant{{
				field: "service_to_service_flow", window: "10m0s", step: "1m0s", start: "100", end: "100",
				conditions: structured.Conditions{FieldList: []structured.ConditionField{
					{DimensionName: "from_id", Value: []string{"caller"}, Operator: structured.ConditionEqual},
					{DimensionName: "to_id", Value: []string{""}, Operator: structured.ConditionNotEqual},
				}, ConditionList: []string{structured.ConditionAnd}},
			}},
			wantResults: []PathResourcesResult{{Timestamp: timestampMS, TargetType: "service", Path: []cmdb.PathNode{
				{ResourceType: "service", Dimensions: cmdb.Matcher{"id": "caller"}}, {ResourceType: "service", Dimensions: cmdb.Matcher{"id": "callee"}},
			}}},
		},
		{
			name:        "same_type_dynamic_inbound_uses_to_endpoint",
			provider:    serviceProvider,
			sourceType:  "service",
			targetTypes: []cmdb.Resource{"service"},
			path: cmdb.RelationPath{Steps: []cmdb.RelationPathStep{
				{ResourceType: "service"},
				{ResourceType: "service", RelationType: "service_to_service", Category: string(RelationCategoryDynamic), Direction: string(DirectionInbound)},
			}},
			sourceInfo: cmdb.Matcher{"id": "callee"},
			start:      instantStart, end: instantStart, step: time.Minute, lookBackDelta: "10m",
			responses: map[string]pl.Matrix{"service_to_service_flow": contractMatrix(map[string]string{"from_id": "caller", "to_id": "callee"}, timestampMS)},
			wantQueries: []contractQueryWant{{
				field: "service_to_service_flow", window: "10m0s", step: "1m0s", start: "100", end: "100",
				conditions: structured.Conditions{FieldList: []structured.ConditionField{
					{DimensionName: "from_id", Value: []string{""}, Operator: structured.ConditionNotEqual},
					{DimensionName: "to_id", Value: []string{"callee"}, Operator: structured.ConditionEqual},
				}, ConditionList: []string{structured.ConditionAnd}},
			}},
			wantResults: []PathResourcesResult{{Timestamp: timestampMS, TargetType: "service", Path: []cmdb.PathNode{
				{ResourceType: "service", Dimensions: cmdb.Matcher{"id": "callee"}}, {ResourceType: "service", Dimensions: cmdb.Matcher{"id": "caller"}},
			}}},
		},
		{
			name:        "range_step_and_lookback_are_sent_separately",
			provider:    staticProvider,
			sourceType:  "left",
			targetTypes: []cmdb.Resource{"right"},
			path: cmdb.RelationPath{Steps: []cmdb.RelationPathStep{
				{ResourceType: "left"}, {ResourceType: "right", RelationType: "left_to_right", Category: string(RelationCategoryStatic), Direction: string(DirectionOutbound)},
			}},
			sourceInfo: cmdb.Matcher{"left_id": "l1"},
			start:      time.Unix(100, 0), end: time.Unix(220, 0), step: time.Minute, lookBackDelta: "10m",
			responses: map[string]pl.Matrix{"left_to_right_flow": contractMatrix(map[string]string{"left_id": "l1", "right_id": "r1"}, 100000, 160000, 220000)},
			wantQueries: []contractQueryWant{{
				field: "left_to_right_flow", window: "10m0s", step: "1m0s", start: "100", end: "220",
				conditions: structured.Conditions{FieldList: []structured.ConditionField{
					{DimensionName: "left_id", Value: []string{"l1"}, Operator: structured.ConditionEqual},
					{DimensionName: "right_id", Value: []string{""}, Operator: structured.ConditionNotEqual},
				}, ConditionList: []string{structured.ConditionAnd}},
			}},
			wantResults: []PathResourcesResult{
				{Timestamp: 100000, TargetType: "right", Path: []cmdb.PathNode{{ResourceType: "left", Dimensions: cmdb.Matcher{"left_id": "l1"}}, {ResourceType: "right", Dimensions: cmdb.Matcher{"right_id": "r1"}}}},
				{Timestamp: 160000, TargetType: "right", Path: []cmdb.PathNode{{ResourceType: "left", Dimensions: cmdb.Matcher{"left_id": "l1"}}, {ResourceType: "right", Dimensions: cmdb.Matcher{"right_id": "r1"}}}},
				{Timestamp: 220000, TargetType: "right", Path: []cmdb.PathNode{{ResourceType: "left", Dimensions: cmdb.Matcher{"left_id": "l1"}}, {ResourceType: "right", Dimensions: cmdb.Matcher{"right_id": "r1"}}}},
			},
		},
		{
			name:        "target_info_is_filtered_to_related_ids",
			provider:    enrichmentProvider,
			sourceType:  "node",
			targetTypes: []cmdb.Resource{"container"},
			path: cmdb.RelationPath{Steps: []cmdb.RelationPathStep{
				{ResourceType: "node"}, {ResourceType: "container", RelationType: "node_to_container", Category: string(RelationCategoryStatic), Direction: string(DirectionOutbound)},
			}},
			sourceInfo: cmdb.Matcher{"node_id": "n1"},
			start:      instantStart, end: instantStart, step: time.Minute, lookBackDelta: "10m", targetInfoShow: true, maxNodes: 2,
			responses: map[string]pl.Matrix{
				"node_to_container_flow": contractMatrix(map[string]string{"node_id": "n1", "container_id": "c1"}, 100000),
				"container_info_relation": append(
					contractMatrix(map[string]string{"container_id": "c1", "version": "v1"}, 100000),
					contractMatrix(map[string]string{"container_id": "unrelated", "version": "v2"}, 100000)...,
				),
			},
			wantQueries: []contractQueryWant{
				{field: "node_to_container_flow", window: "10m0s", step: "1m0s", start: "100", end: "100", conditions: structured.Conditions{FieldList: []structured.ConditionField{{DimensionName: "container_id", Value: []string{""}, Operator: structured.ConditionNotEqual}, {DimensionName: "node_id", Value: []string{"n1"}, Operator: structured.ConditionEqual}}, ConditionList: []string{structured.ConditionAnd}}},
				{field: "container_info_relation", window: "10m0s", step: "1m0s", start: "100", end: "100", conditions: structured.Conditions{FieldList: []structured.ConditionField{{DimensionName: "container_id", Value: []string{"c1"}, Operator: structured.ConditionEqual}}}},
			},
			wantResults: []PathResourcesResult{{Timestamp: 100000, TargetType: "container", Path: []cmdb.PathNode{
				{ResourceType: "node", Dimensions: cmdb.Matcher{"node_id": "n1"}}, {ResourceType: "container", Dimensions: cmdb.Matcher{"container_id": "c1", "version": "v1"}},
			}}},
		},
		{
			name:        "multi_hop_dynamic_query_only_constrains_root_edge",
			provider:    dynamicChainProvider,
			sourceType:  "front",
			targetTypes: []cmdb.Resource{"back"},
			path: cmdb.RelationPath{Steps: []cmdb.RelationPathStep{
				{ResourceType: "front"},
				{ResourceType: "middle", RelationType: "front_to_middle", Category: string(RelationCategoryDynamic), Direction: string(DirectionOutbound)},
				{ResourceType: "back", RelationType: "middle_to_back", Category: string(RelationCategoryDynamic), Direction: string(DirectionOutbound)},
			}},
			sourceInfo:    cmdb.Matcher{"id": "f1"},
			start:         instantStart,
			end:           instantStart,
			step:          time.Minute,
			lookBackDelta: "10m",
			responses: map[string]pl.Matrix{
				"front_to_middle_flow": contractMatrix(map[string]string{"from_id": "f1", "to_id": "m1"}, timestampMS),
				"middle_to_back_flow":  contractMatrix(map[string]string{"from_id": "m1", "to_id": "b1"}, timestampMS),
			},
			wantQueries: []contractQueryWant{
				{
					field: "front_to_middle_flow", window: "10m0s", step: "1m0s", start: "100", end: "100",
					conditions: structured.Conditions{FieldList: []structured.ConditionField{
						{DimensionName: "from_id", Value: []string{"f1"}, Operator: structured.ConditionEqual},
						{DimensionName: "to_id", Value: []string{""}, Operator: structured.ConditionNotEqual},
					}, ConditionList: []string{structured.ConditionAnd}},
				},
				{
					field: "middle_to_back_flow", window: "10m0s", step: "1m0s", start: "100", end: "100",
					conditions: structured.Conditions{FieldList: []structured.ConditionField{
						{DimensionName: "from_id", Value: []string{""}, Operator: structured.ConditionNotEqual},
						{DimensionName: "to_id", Value: []string{""}, Operator: structured.ConditionNotEqual},
					}, ConditionList: []string{structured.ConditionAnd}},
				},
			},
			wantResults: []PathResourcesResult{{Timestamp: timestampMS, TargetType: "back", Path: []cmdb.PathNode{
				{ResourceType: "front", Dimensions: cmdb.Matcher{"id": "f1"}},
				{ResourceType: "middle", Dimensions: cmdb.Matcher{"id": "m1"}},
				{ResourceType: "back", Dimensions: cmdb.Matcher{"id": "b1"}},
			}}},
		},
		{
			name:        "external_vm_error_is_returned",
			provider:    staticProvider,
			sourceType:  "left",
			targetTypes: []cmdb.Resource{"right"},
			path: cmdb.RelationPath{Steps: []cmdb.RelationPathStep{
				{ResourceType: "left"}, {ResourceType: "right", RelationType: "left_to_right", Category: string(RelationCategoryStatic), Direction: string(DirectionOutbound)},
			}},
			sourceInfo:    cmdb.Matcher{"left_id": "l1"},
			start:         instantStart,
			end:           instantStart,
			step:          time.Minute,
			lookBackDelta: "10m",
			responses:     map[string]pl.Matrix{},
			wantErr:       `unexpected VM query metric "left_to_right_flow"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oldMaxNodes, oldMaxNodeInfos := MaxGraphNodes, MaxGraphNodeInfos
			t.Cleanup(func() {
				MaxGraphNodes = oldMaxNodes
				MaxGraphNodeInfos = oldMaxNodeInfos
			})
			if tc.maxNodes > 0 {
				MaxGraphNodes = tc.maxNodes
			}

			model := &Model{schemaProvider: tc.provider}
			relations := model.buildRelationsFromRelationPathsForNamespace("", []cmdb.RelationPath{tc.path})
			require.Len(t, relations, len(tc.path.Steps)-1)
			vm := &contractVM{responses: tc.responses}
			ctx := withTimeGraphTargetInfoShow(context.Background(), tc.targetInfoShow)
			tg, err := model.buildTimeGraphFromRelationsWithQuery(
				ctx, "space", tc.start, tc.end, tc.step, tc.sourceType, tc.sourceInfo, tc.sourceExpandInfo,
				relations, tc.lookBackDelta, vm.query,
			)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.Len(t, vm.calls, 1)
				return
			}
			require.NoError(t, err)
			t.Cleanup(func() { tg.Clean(ctx) })

			got, err := tg.FindRelationPathResources(ctx, tc.sourceType, tc.targetTypes, tc.sourceInfo, []cmdb.RelationPath{tc.path})
			require.NoError(t, err)
			require.Equal(t, tc.wantResults, got)
			require.Len(t, vm.calls, len(tc.wantQueries))
			for i, want := range tc.wantQueries {
				require.Equal(t, want.field, vm.calls[i].field)
				require.Equal(t, want.window, vm.calls[i].window)
				require.Equal(t, want.step, vm.calls[i].step)
				require.Equal(t, want.start, vm.calls[i].start)
				require.Equal(t, want.end, vm.calls[i].end)
				require.Equal(t, want.conditions, vm.calls[i].conditions)
			}
		})
	}
}

func TestTimeGraphSubqueryContextsPreserveUser(t *testing.T) {
	relation := cmdb.Relation{
		V:            []cmdb.Resource{"node", "system"},
		RelationType: "node_with_system",
		Category:     string(RelationCategoryStatic),
	}
	tests := []struct {
		name             string
		sourceExpandInfo cmdb.Matcher
		targetInfoShow   bool
		relations        []cmdb.Relation
		wantQueryCount   int
	}{
		{name: "source_info_query", sourceExpandInfo: cmdb.Matcher{"region": "east"}, wantQueryCount: 1},
		{name: "relation_query", relations: []cmdb.Relation{relation}, wantQueryCount: 1},
		{name: "target_info_query", targetInfoShow: true, relations: []cmdb.Relation{relation}, wantQueryCount: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			parentUser := &metadata.User{Key: "bkmonitor:tester", TenantID: "tenant-a", SpaceUID: "bkcc__2"}
			metadata.SetUser(ctx, parentUser)
			parentHashID := parentUser.HashID
			seenUsers := make([]metadata.User, 0, tc.wantQueryCount)
			seenBizIDs := make([]string, 0, tc.wantQueryCount)
			query := func(queryCtx context.Context, queryTs *structured.QueryTs) (pl.Matrix, error) {
				seenUsers = append(seenUsers, *metadata.GetUser(queryCtx))
				seenBizIDs = append(seenBizIDs, metadata.GetBkBizID(queryCtx))
				if len(queryTs.QueryList) == 1 && queryTs.QueryList[0].FieldName == "node_with_system_relation" {
					return contractMatrix(map[string]string{"node": "n1", "ip": "10.0.0.1"}, 100000), nil
				}
				return nil, nil
			}
			model := &Model{schemaProvider: timeGraphTestSchemaProvider{}}
			queryCtx := withTimeGraphTargetInfoShow(ctx, tc.targetInfoShow)
			tg, err := model.buildTimeGraphFromRelationsWithQuery(
				queryCtx, "bkcc__2", time.Unix(100, 0), time.Unix(100, 0), time.Minute,
				"node", cmdb.Matcher{"node": "n1"}, tc.sourceExpandInfo, tc.relations, "10m", query,
			)
			require.NoError(t, err)
			t.Cleanup(func() { tg.Clean(queryCtx) })
			require.Len(t, seenUsers, tc.wantQueryCount)
			require.Equal(t, parentHashID, parentUser.HashID)
			for _, user := range seenUsers {
				require.Equal(t, "tenant-a", user.TenantID)
				require.Equal(t, "bkcc__2", user.SpaceUID)
				require.Equal(t, "bkmonitor:tester", user.Key)
				require.NotEmpty(t, user.HashID)
				require.NotEqual(t, parentHashID, user.HashID)
			}
			for _, bizID := range seenBizIDs {
				require.Equal(t, "2", bizID)
			}
		})
	}
}
