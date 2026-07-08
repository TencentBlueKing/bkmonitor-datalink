// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
)

func TestSurrealDBQuerySync(t *testing.T) {
	tests := []struct {
		name        string
		request     QueryRequest
		provider    SchemaProvider
		binding     BindingInfo
		expectedSQL string
	}{
		{
			name: "host to module query uses runtime binding schema and bkbase query_sync payload",
			request: QueryRequest{
				SpaceUID:             tableMockSpaceUID,
				Timestamp:            1776910000000,
				SourceType:           ResourceTypeHost,
				SourceInfo:           map[string]string{"bk_host_id": "38268"},
				TargetType:           ResourceTypeModule,
				MaxHops:              1,
				AllowedRelationTypes: []RelationCategory{RelationCategoryStatic},
				LookBackDelta:        7000000000,
				Limit:                10,
			},
			provider: newTableSchemaProvider(
				map[ResourceType]tableResourceDefinition{
					ResourceTypeHost:   {primaryKeys: []string{"bk_host_id"}},
					ResourceTypeModule: {primaryKeys: []string{"bk_module_id"}},
				},
				[]RelationSchema{
					{
						RelationType: "host_module_link",
						Category:     RelationCategoryStatic,
						FromType:     ResourceTypeHost,
						ToType:       ResourceTypeModule,
					},
				},
			),
			binding: *tableMockBindingInfo(),
			expectedSQL: `LET $timestamp = 1776910000000;
LET $look_back_delta = 7000000000;
LET $start = 1769910000;
LET $end = 1776910000;
LET $start_ms = 1769910000000;
LET $end_ms = 1776910000000;

SELECT {
    root: {
        entity_type: meta::tb(id),
        entity_id: <string>id,
        entity_data: { bk_host_id: bk_host_id },
        created_at: created_at,
        updated_at: updated_at,
        liveness: (SELECT * FROM host_liveness_record WHERE reference_id = $parent.id AND period_end >= $start AND period_start <= $end)
    },

    hop1: {
        host_module_link: (SELECT VALUE {
            hop: 1,
            relation_type: 'host_module_link',
            relation_category: 'static',
            relation_id: <string>id,
            relation_liveness: (SELECT * FROM host_module_link_liveness_record WHERE relation_id = $parent.id AND period_end >= $start_ms AND period_start <= $end_ms),
            target: {
                entity_type: 'module',
                entity_id: <string>out,
                entity_data: { bk_module_id: out.bk_module_id },
                liveness: (SELECT * FROM module_liveness_record WHERE reference_id = $parent.out AND period_end >= $start AND period_start <= $end)
            }
        } FROM host_module_link WHERE in = $parent.id
          AND (SELECT * FROM host_module_link_liveness_record WHERE relation_id = $parent.id AND $end_ms >= period_start AND $start_ms <= period_end LIMIT 1)[0] != NONE
          AND (SELECT * FROM module_liveness_record WHERE reference_id = $parent.out AND $end >= period_start AND $start <= period_end LIMIT 1)[0] != NONE)
    }
} AS result
FROM host
WHERE bk_host_id = '38268'
  AND (SELECT * FROM host_liveness_record WHERE reference_id = $parent.id AND $end >= period_start AND $start <= period_end LIMIT 1)[0] != NONE
LIMIT 10;`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := tt.request
			sql := NewSurrealQueryBuilderWithSchemaProvider(&req, tt.provider).Build()
			assert.Equal(t, tt.expectedSQL, sql)

			start, end := req.GetQueryRange()
			mockCurl := &mockBKBaseCurl{
				response: BKBaseResponse{
					Result: true,
					Code:   "00",
					Data:   &BKBaseData{List: []map[string]any{}},
				},
			}
			client := &BKBaseSurrealDBClient{curl: mockCurl}
			_, err := client.ExecuteWithBinding(context.Background(), req.SpaceUID, tt.binding, sql, start, end)
			require.NoError(t, err)

			assert.Equal(t, curl.Post, mockCurl.method)

			var body map[string]any
			require.NoError(t, json.Unmarshal(mockCurl.options.Body, &body))
			assert.Equal(t, PreferStorageSurrealDB, body["prefer_storage"])
			assert.Equal(t, map[string]any{"cluster_name": tt.binding.ClusterName}, body["properties"])

			sqlPayloadText, ok := body["sql"].(string)
			require.True(t, ok)

			var payload BKBaseSQLPayload
			require.NoError(t, json.Unmarshal([]byte(sqlPayloadText), &payload))
			assert.Equal(t, tableMockUseNSDBStatement+tt.expectedSQL, payload.DSL)
			assert.Equal(t, tt.binding.Database, payload.ResultTableID)
		})
	}
}

func TestSurrealDBResponseParsing(t *testing.T) {
	tests := []struct {
		name           string
		queryStart     int64
		queryEnd       int64
		bkbaseResponse string
		expected       []tableGraphSummary
	}{
		{
			name:       "bkbase query_sync list parses host module relation graph",
			queryStart: 1769910000000,
			queryEnd:   1776910000000,
			bkbaseResponse: `{
  "result": true,
  "code": "00",
  "data": {
    "total_records": 1,
    "device": "surrealdb",
    "result_table_ids": [
      "mock_graph_result_table"
    ],
    "list": [
      {
        "result": {
          "root": {
            "entity_type": "host",
            "entity_id": "host:⟨38268⟩",
            "entity_data": {
              "bk_host_id": 38268
            },
            "liveness": [
              {
                "period_start": 1770305362,
                "period_end": 1770306030
              }
            ]
          },
          "hop1": {
            "host_module_link": [
              {
                "relation_type": "host_module_link",
                "relation_category": "static",
                "relation_id": "host_module_link:⟨38268||10259⟩",
                "relation_liveness": [
                  {
                    "period_start": 1770305545030,
                    "period_end": 1770306030891
                  }
                ],
                "target": {
                  "entity_type": "module",
                  "entity_id": "module:⟨10259⟩",
                  "entity_data": {
                    "bk_module_id": "10259"
                  },
                  "liveness": [
                    {
                      "period_start": 1770305423,
                      "period_end": 1770306030
                    }
                  ]
                }
              }
            ]
          }
        }
      }
    ]
  }
}`,
			expected: []tableGraphSummary{
				{
					QueryStart: 1769910000000,
					QueryEnd:   1776910000000,
					RootID:     "host:⟨38268⟩",
					Nodes: []tableNodeSummary{
						{
							ResourceID:   "host:⟨38268⟩",
							ResourceType: ResourceTypeHost,
							Labels:       map[string]string{"bk_host_id": "38268"},
							RawPeriods:   []*VisiblePeriod{{Start: 1770305362000, End: 1770306030000}},
						},
						{
							ResourceID:   "module:⟨10259⟩",
							ResourceType: ResourceTypeModule,
							Labels:       map[string]string{"bk_module_id": "10259"},
							RawPeriods:   []*VisiblePeriod{{Start: 1770305423000, End: 1770306030000}},
						},
					},
					Edges: []tableEdgeSummary{
						{
							RelationID:   "host_module_link:⟨38268||10259⟩",
							RelationType: "host_module_link",
							Category:     RelationCategoryStatic,
							FromID:       "host:⟨38268⟩",
							ToID:         "module:⟨10259⟩",
							RawPeriods:   []*VisiblePeriod{{Start: 1770305545030, End: 1770306030891}},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var response BKBaseResponse
			decoder := json.NewDecoder(strings.NewReader(tt.bkbaseResponse))
			decoder.UseNumber()
			require.NoError(t, decoder.Decode(&response))

			client := &BKBaseSurrealDBClient{curl: &mockBKBaseCurl{response: response}}
			graphs, err := client.Execute(context.Background(), "SELECT * FROM host", tt.queryStart, tt.queryEnd)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, summarizeTableGraphs(graphs))
		})
	}
}

func TestSurrealDBPathSplitQuerySyncRequestsTableDriven(t *testing.T) {
	provider := newTableSchemaProvider(
		map[ResourceType]tableResourceDefinition{
			ResourceTypeNode:   {primaryKeys: []string{"node"}},
			ResourceTypeSystem: {primaryKeys: []string{"system"}},
			ResourceTypePod:    {primaryKeys: []string{"pod"}},
		},
		[]RelationSchema{
			{RelationType: RelationNodeWithPod, Category: RelationCategoryStatic, FromType: ResourceTypeNode, ToType: ResourceTypePod},
			{RelationType: RelationNodeWithSystem, Category: RelationCategoryStatic, FromType: ResourceTypeNode, ToType: ResourceTypeSystem},
			{RelationType: RelationSystemToPod, Category: RelationCategoryStatic, FromType: ResourceTypeSystem, ToType: ResourceTypePod},
		},
	)

	tests := []struct {
		name                    string
		mode                    graphQueryMode
		requestJSON             string
		rangeStart              int64
		rangeEnd                int64
		stepMs                  int64
		bkbaseResponseOverrides map[string]string
		expectedResponseJSON    string
	}{
		{
			name:                 "instant query returns target from the direct path response",
			mode:                 graphQueryModeInstant,
			requestJSON:          `{"space_uid":"` + tableMockSpaceUID + `","timestamp":600000,"source_type":"node","source_info":{"node":"node-1"},"target_type":"pod","look_back_delta":600000}`,
			expectedResponseJSON: `{"query_mode":"path-by-path","path":["node","pod"],"matchers":[{"pod":"pod-1"}],"query_sync_requests":[{"path":["node","pod"],"prefer_storage":"surrealdb","properties":{"cluster_name":"mock_surrealdb_cluster"},"result_table_id":"mock_graph_result_table","contains_relations":["node_with_pod"],"not_contains_relations":["node_with_system","system_to_pod"]},{"path":["node","system","pod"],"prefer_storage":"surrealdb","properties":{"cluster_name":"mock_surrealdb_cluster"},"result_table_id":"mock_graph_result_table","contains_relations":["node_with_system","system_to_pod"],"not_contains_relations":["node_with_pod"]}]}`,
		},
		{
			name:                 "range query returns target series from the direct path response",
			mode:                 graphQueryModeRange,
			rangeStart:           0,
			rangeEnd:             600000,
			stepMs:               60000,
			requestJSON:          `{"space_uid":"` + tableMockSpaceUID + `","timestamp":600000,"source_type":"node","source_info":{"node":"node-1"},"target_type":"pod","look_back_delta":1200000}`,
			expectedResponseJSON: `{"query_mode":"path-by-path","path":["node","pod"],"matchers":[{"pod":"pod-1"}],"range_result":[{"timestamp":0,"matchers":[{"pod":"pod-1"}]},{"timestamp":60000,"matchers":[{"pod":"pod-1"}]}],"query_sync_requests":[{"path":["node","pod"],"prefer_storage":"surrealdb","properties":{"cluster_name":"mock_surrealdb_cluster"},"result_table_id":"mock_graph_result_table","contains_relations":["node_with_pod"],"not_contains_relations":["node_with_system","system_to_pod"]},{"path":["node","system","pod"],"prefer_storage":"surrealdb","properties":{"cluster_name":"mock_surrealdb_cluster"},"result_table_id":"mock_graph_result_table","contains_relations":["node_with_system","system_to_pod"],"not_contains_relations":["node_with_pod"]}]}`,
		},
		{
			name:        "instant query falls back to indirect path when direct path is empty",
			mode:        graphQueryModeInstant,
			requestJSON: `{"space_uid":"` + tableMockSpaceUID + `","timestamp":600000,"source_type":"node","source_info":{"node":"node-1"},"target_type":"pod","look_back_delta":600000}`,
			bkbaseResponseOverrides: map[string]string{
				"node/pod": tableEmptyBKBaseResponseJSON,
			},
			expectedResponseJSON: `{"query_mode":"path-by-path","path":["node","system","pod"],"matchers":[{"pod":"pod-via-system"}],"query_sync_requests":[{"path":["node","pod"],"prefer_storage":"surrealdb","properties":{"cluster_name":"mock_surrealdb_cluster"},"result_table_id":"mock_graph_result_table","contains_relations":["node_with_pod"],"not_contains_relations":["node_with_system","system_to_pod"]},{"path":["node","system","pod"],"prefer_storage":"surrealdb","properties":{"cluster_name":"mock_surrealdb_cluster"},"result_table_id":"mock_graph_result_table","contains_relations":["node_with_system","system_to_pod"],"not_contains_relations":["node_with_pod"]}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := decodeTableQueryRequestJSON(t, tt.requestJSON)
			server := newSurrealDBMockServer(t, tablePathSplitBKBaseResponsesBySurrealQL(t, req, provider, tt.bkbaseResponseOverrides))
			defer server.Close()

			restoreQueryURL := setTableBKBaseQueryURLForTest(server.URL)
			defer restoreQueryURL()

			resolver := &BindingResolver{cache: make(map[string]*bindingCacheEntry)}
			resolver.storeCache(tableMockBindingCacheKey, tableMockBindingInfo())

			model, err := NewModel(context.Background(), &BKBaseSurrealDBClient{curl: &curl.HttpCurl{}})
			require.NoError(t, err)
			model.SetResolver(resolver)
			model.SetSchemaProvider(provider)

			graphs, paths, matchers, err := model.queryLivenessGraph(
				context.Background(),
				&req,
				true,
				tt.mode,
				tt.rangeStart,
				tt.rangeEnd,
				tt.stepMs,
			)
			require.NoError(t, err)

			actualResponse := tablePathSplitQueryResponse{
				QueryMode:         "path-by-path",
				Path:              convertResourcePathToResources(paths),
				Matchers:          matchers,
				QuerySyncRequests: tablePathSplitQuerySyncRequestSummaries(t, server.Requests()),
			}
			if tt.mode == graphQueryModeRange {
				actualResponse.RangeResult = tableRangeResultFromMatchersWithTimestamp(
					buildTargetMatchersTimeSeriesWithOptions(
						graphs,
						req.TargetType,
						targetExtractionPathResource(&req),
						tt.rangeStart,
						tt.rangeEnd,
						tt.stepMs,
						provider,
						req.SchemaNamespace(),
						req.TargetInfoShow,
						shouldIncludeRootTarget(&req),
					),
				)
			}
			assert.JSONEq(t, tt.expectedResponseJSON, encodeTablePathSplitQueryResponseJSON(t, actualResponse))

			require.Len(t, actualResponse.QuerySyncRequests, 2)
		})
	}
}

type tableResourceDefinition struct {
	primaryKeys []string
	fields      []string
}

type tableSchemaProvider struct {
	resources map[ResourceType]tableResourceDefinition
	relations []RelationSchema
}

func newTableSchemaProvider(resources map[ResourceType]tableResourceDefinition, relations []RelationSchema) SchemaProvider {
	return &tableSchemaProvider{
		resources: resources,
		relations: relations,
	}
}

func (p *tableSchemaProvider) GetResourcePrimaryKeys(_ string, resourceType ResourceType) []string {
	resource := p.resources[resourceType]
	return append([]string(nil), resource.primaryKeys...)
}

func (p *tableSchemaProvider) GetResourceFields(_ string, resourceType ResourceType) []string {
	resource := p.resources[resourceType]
	if len(resource.fields) > 0 {
		return append([]string(nil), resource.fields...)
	}
	return append([]string(nil), resource.primaryKeys...)
}

func (p *tableSchemaProvider) ListResourceTypes(_ string) []ResourceType {
	resourceTypes := make([]ResourceType, 0, len(p.resources))
	for resourceType := range p.resources {
		resourceTypes = append(resourceTypes, resourceType)
	}
	sort.Slice(resourceTypes, func(i, j int) bool {
		return resourceTypes[i] < resourceTypes[j]
	})
	return resourceTypes
}

func (p *tableSchemaProvider) ListRelationSchemas(_ string) []RelationSchema {
	return append([]RelationSchema(nil), p.relations...)
}

type tableGraphSummary struct {
	QueryStart      int64              `json:"query_start"`
	QueryEnd        int64              `json:"query_end"`
	RootID          string             `json:"root_id,omitempty"`
	Nodes           []tableNodeSummary `json:"nodes"`
	Edges           []tableEdgeSummary `json:"edges"`
	TraversalErrors []string           `json:"traversal_errors,omitempty"`
}

type tableNodeSummary struct {
	ResourceID   string            `json:"resource_id"`
	ResourceType ResourceType      `json:"resource_type"`
	Labels       map[string]string `json:"labels,omitempty"`
	RawPeriods   []*VisiblePeriod  `json:"raw_periods,omitempty"`
}

type tableEdgeSummary struct {
	RelationID   string             `json:"relation_id"`
	RelationType RelationType       `json:"relation_type"`
	Category     RelationCategory   `json:"category"`
	Direction    TraversalDirection `json:"direction,omitempty"`
	FromID       string             `json:"from_id"`
	ToID         string             `json:"to_id"`
	RawPeriods   []*VisiblePeriod   `json:"raw_periods,omitempty"`
}

func summarizeTableGraphs(graphs []*LivenessGraph) []tableGraphSummary {
	result := make([]tableGraphSummary, 0, len(graphs))
	for _, graph := range graphs {
		if graph == nil {
			continue
		}

		nodes := make([]tableNodeSummary, 0, len(graph.Nodes))
		for _, node := range graph.Nodes {
			nodes = append(nodes, tableNodeSummary{
				ResourceID:   node.ResourceID,
				ResourceType: node.ResourceType,
				Labels:       node.Labels,
				RawPeriods:   node.RawPeriods,
			})
		}
		sort.Slice(nodes, func(i, j int) bool {
			return nodes[i].ResourceID < nodes[j].ResourceID
		})

		edges := make([]tableEdgeSummary, 0, len(graph.Edges))
		for _, edge := range graph.Edges {
			edges = append(edges, tableEdgeSummary{
				RelationID:   edge.RelationID,
				RelationType: edge.RelationType,
				Category:     edge.Category,
				Direction:    edge.Direction,
				FromID:       edge.FromID,
				ToID:         edge.ToID,
				RawPeriods:   edge.RawPeriods,
			})
		}
		sort.Slice(edges, func(i, j int) bool {
			if edges[i].RelationID == edges[j].RelationID {
				return edges[i].Direction < edges[j].Direction
			}
			return edges[i].RelationID < edges[j].RelationID
		})

		errors := append([]string(nil), graph.TraversalErrors...)
		sort.Strings(errors)
		result = append(result, tableGraphSummary{
			QueryStart:      graph.QueryStart,
			QueryEnd:        graph.QueryEnd,
			RootID:          graph.RootID,
			Nodes:           nodes,
			Edges:           edges,
			TraversalErrors: errors,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].RootID < result[j].RootID
	})
	return result
}
