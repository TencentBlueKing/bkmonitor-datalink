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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
)

func TestSurrealDBQuerySync(t *testing.T) {
	tests := []struct {
		name                       string
		request                    QueryRequest
		provider                   SchemaProvider
		binding                    BindingInfo
		activeEdgeServingRelations []string
		queryMode                  graphQueryMode
		expectedSQL                string
	}{
		{
			name: "host to module query uses runtime binding schema and bkbase query_sync payload",
			request: QueryRequest{
				SpaceUID:             tableMockSpaceUID,
				Timestamp:            1776910000000,
				SourceType:           ResourceTypeHost,
				SourceInfo:           map[string]string{"bk_host_id": "38268"},
				TargetType:           ResourceTypeModule,
				TargetTypeExplicit:   true,
				MaxHops:              1,
				AllowedRelationTypes: []RelationCategory{RelationCategoryStatic},
				LookBackDelta:        7000000000,
				Limit:                10,
			},
			provider: newTableSchemaProvider(
				map[ResourceType]tableResourceDefinition{
					ResourceTypeHost:   {primaryKeys: []string{"bk_host_id"}, fieldTypes: map[string]string{"bk_host_id": "integer"}},
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
			binding:                    *tableMockBindingInfo(),
			activeEdgeServingRelations: []string{string(RelationNodeWithPod)},
			queryMode:                  graphQueryModeInstant,
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
        updated_at: updated_at
    },

    hop1: {
        host_module_link: (SELECT VALUE {
            hop: 1,
            relation_type: 'host_module_link',
            relation_category: 'static',
            relation_id: <string>id,
            target: {
                entity_type: 'module',
                entity_id: <string>out,
                entity_data: { bk_module_id: out.bk_module_id }
            }
        } FROM host_module_link WHERE in = $parent.id
          AND (SELECT * FROM host_module_link_liveness_record WHERE relation_id = $parent.id AND $end_ms >= period_start AND $start_ms <= period_end AND period_start <= period_end LIMIT 1)[0] != NONE
          AND (SELECT * FROM module_liveness_record WHERE reference_id = $parent.out AND $end >= period_start AND $start <= period_end AND period_start <= period_end LIMIT 1)[0] != NONE
          LIMIT 1001)
    }
} AS result
FROM host
WHERE bk_host_id = '38268'
  AND (SELECT * FROM host_liveness_record WHERE reference_id = $parent.id AND $end >= period_start AND $start <= period_end AND period_start <= period_end LIMIT 1)[0] != NONE
LIMIT 10;`,
		},
		{
			name: "node to pod query uses active edge serving table",
			request: QueryRequest{
				SpaceUID:           tableMockSpaceUID,
				Timestamp:          300000,
				SourceType:         ResourceTypeNode,
				TargetType:         ResourceTypePod,
				TargetTypeExplicit: true,
				MaxHops:            1,
			},
			provider: newTableSchemaProvider(
				map[ResourceType]tableResourceDefinition{
					ResourceTypeNode: {primaryKeys: []string{"bcs_cluster_id", "node"}},
					ResourceTypePod:  {primaryKeys: []string{"bcs_cluster_id", "namespace", "pod"}},
				},
				[]RelationSchema{
					{
						RelationType: RelationNodeWithPod,
						Category:     RelationCategoryStatic,
						FromType:     ResourceTypeNode,
						ToType:       ResourceTypePod,
					},
				},
			),
			binding:                    *tableMockBindingInfo(),
			activeEdgeServingRelations: []string{string(RelationNodeWithPod)},
			queryMode:                  graphQueryModeInstant,
			expectedSQL: `LET $timestamp = 300000;
LET $look_back_delta = 86400000;
LET $start = 0;
LET $end = 300;
LET $start_ms = 0;
LET $end_ms = 300000;

SELECT {
    root: {
        entity_type: meta::tb(id),
        entity_id: <string>id,
        entity_data: { bcs_cluster_id: bcs_cluster_id, node: node },
        created_at: created_at,
        updated_at: updated_at
    },

    hop1: {
        node_with_pod: (SELECT VALUE {
                hop: 1,
                relation_type: 'node_with_pod',
                relation_category: 'static',
                relation_id: <string>relation_id,
                target: {
                    entity_type: target_type,
                    entity_id: <string>target_id,
                    entity_data: target_data
                }
            } FROM node_with_pod_active_edge_view WHERE source_id = $parent.id
              AND active_period_start_ms <= active_period_end_ms
              AND active_period_start_ms <= $end_ms
              AND active_period_end_ms >= $start_ms
              LIMIT 1001)
    }
} AS result
FROM node
WHERE (SELECT * FROM node_liveness_record WHERE reference_id = $parent.id AND $end >= period_start AND $start <= period_end AND period_start <= period_end LIMIT 1)[0] != NONE
LIMIT 100;`,
		},
		{
			name: "pod to node query uses active edge serving table in reverse",
			request: QueryRequest{
				SpaceUID:           tableMockSpaceUID,
				Timestamp:          300000,
				SourceType:         ResourceTypePod,
				TargetType:         ResourceTypeNode,
				TargetTypeExplicit: true,
				MaxHops:            1,
			},
			provider: newTableSchemaProvider(
				map[ResourceType]tableResourceDefinition{
					ResourceTypeNode: {primaryKeys: []string{"bcs_cluster_id", "node"}},
					ResourceTypePod:  {primaryKeys: []string{"bcs_cluster_id", "namespace", "pod"}},
				},
				[]RelationSchema{{
					RelationType: RelationNodeWithPod,
					Category:     RelationCategoryStatic,
					FromType:     ResourceTypeNode,
					ToType:       ResourceTypePod,
				}},
			),
			binding:                    *tableMockBindingInfo(),
			activeEdgeServingRelations: []string{string(RelationNodeWithPod)},
			queryMode:                  graphQueryModeInstant,
			expectedSQL: `LET $timestamp = 300000;
LET $look_back_delta = 86400000;
LET $start = 0;
LET $end = 300;
LET $start_ms = 0;
LET $end_ms = 300000;

SELECT {
    root: {
        entity_type: meta::tb(id),
        entity_id: <string>id,
        entity_data: { bcs_cluster_id: bcs_cluster_id, namespace: namespace, pod: pod },
        created_at: created_at,
        updated_at: updated_at
    },

    hop1: {
        node_with_pod: (SELECT VALUE {
                hop: 1,
                relation_type: 'node_with_pod',
                relation_category: 'static',
                relation_id: <string>relation_id,
                target: {
                    entity_type: source_type,
                    entity_id: <string>source_id,
                    entity_data: source_data
                }
            } FROM node_with_pod_active_edge_view WHERE target_id = $parent.id
              AND active_period_start_ms <= active_period_end_ms
              AND active_period_start_ms <= $end_ms
              AND active_period_end_ms >= $start_ms
              LIMIT 1001)
    }
} AS result
FROM pod
WHERE (SELECT * FROM pod_liveness_record WHERE reference_id = $parent.id AND $end >= period_start AND $start <= period_end AND period_start <= period_end LIMIT 1)[0] != NONE
LIMIT 100;`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldRelations := ActiveEdgeServingRelations
			ActiveEdgeServingRelations = append([]string(nil), tt.activeEdgeServingRelations...)
			t.Cleanup(func() {
				ActiveEdgeServingRelations = oldRelations
			})

			req := tt.request
			builder := NewSurrealQueryBuilderWithSchemaProvider(&req, tt.provider)
			configureBuilderForGraphQueryMode(builder, tt.queryMode)
			sql := builder.Build()
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

func TestActiveEdgeServingSurrealQLFallbackContract(t *testing.T) {
	provider := newTableSchemaProvider(
		map[ResourceType]tableResourceDefinition{
			ResourceTypeNode: {primaryKeys: []string{"bcs_cluster_id", "node"}},
			ResourceTypePod:  {primaryKeys: []string{"bcs_cluster_id", "namespace", "pod"}},
		},
		[]RelationSchema{{
			RelationType: RelationNodeWithPod,
			Category:     RelationCategoryStatic,
			FromType:     ResourceTypeNode,
			ToType:       ResourceTypePod,
		}},
	)
	req := QueryRequest{
		Timestamp:          300000,
		SourceType:         ResourceTypeNode,
		TargetType:         ResourceTypePod,
		TargetTypeExplicit: true,
		MaxHops:            1,
	}
	tests := []struct {
		name             string
		mode             graphQueryMode
		servingRelations []string
		expectedSQL      string
	}{
		{
			name: "instant falls back when relation is not enabled",
			mode: graphQueryModeInstant,
			expectedSQL: `LET $timestamp = 300000;
LET $look_back_delta = 86400000;
LET $start = 0;
LET $end = 300;
LET $start_ms = 0;
LET $end_ms = 300000;

SELECT {
    root: {
        entity_type: meta::tb(id),
        entity_id: <string>id,
        entity_data: { bcs_cluster_id: bcs_cluster_id, node: node },
        created_at: created_at,
        updated_at: updated_at
    },

    hop1: {
        node_with_pod: (SELECT VALUE {
            hop: 1,
            relation_type: 'node_with_pod',
            relation_category: 'static',
            relation_id: <string>id,
            target: {
                entity_type: 'pod',
                entity_id: <string>out,
                entity_data: { bcs_cluster_id: out.bcs_cluster_id, namespace: out.namespace, pod: out.pod }
            }
        } FROM node_with_pod WHERE in = $parent.id
          AND (SELECT * FROM node_with_pod_liveness_record WHERE relation_id = $parent.id AND $end_ms >= period_start AND $start_ms <= period_end AND period_start <= period_end LIMIT 1)[0] != NONE
          AND (SELECT * FROM pod_liveness_record WHERE reference_id = $parent.out AND $end >= period_start AND $start <= period_end AND period_start <= period_end LIMIT 1)[0] != NONE
          LIMIT 1001)
    }
} AS result
FROM node
WHERE (SELECT * FROM node_liveness_record WHERE reference_id = $parent.id AND $end >= period_start AND $start <= period_end AND period_start <= period_end LIMIT 1)[0] != NONE
LIMIT 100;`,
		},
		{
			name:             "range ignores enabled serving relation",
			mode:             graphQueryModeRange,
			servingRelations: []string{string(RelationNodeWithPod)},
			expectedSQL: `LET $timestamp = 300000;
LET $look_back_delta = 86400000;
LET $start = 0;
LET $end = 300;
LET $start_ms = 0;
LET $end_ms = 300000;

SELECT {
    root: {
        entity_type: meta::tb(id),
        entity_id: <string>id,
        entity_data: { bcs_cluster_id: bcs_cluster_id, node: node },
        created_at: created_at,
        updated_at: updated_at,
        liveness: (SELECT * FROM node_liveness_record WHERE reference_id = $parent.id AND period_end >= $start AND period_start <= $end AND period_start <= period_end)
    },

    hop1: {
        node_with_pod: (SELECT VALUE {
            hop: 1,
            relation_type: 'node_with_pod',
            relation_category: 'static',
            relation_id: <string>id,
            relation_liveness: (SELECT * FROM node_with_pod_liveness_record WHERE relation_id = $parent.id AND period_end >= $start_ms AND period_start <= $end_ms AND period_start <= period_end),
            target: {
                entity_type: 'pod',
                entity_id: <string>out,
                entity_data: { bcs_cluster_id: out.bcs_cluster_id, namespace: out.namespace, pod: out.pod },
                liveness: (SELECT * FROM pod_liveness_record WHERE reference_id = $parent.out AND period_end >= $start AND period_start <= $end AND period_start <= period_end)
            }
        } FROM node_with_pod WHERE in = $parent.id
          AND (SELECT * FROM node_with_pod_liveness_record WHERE relation_id = $parent.id AND $end_ms >= period_start AND $start_ms <= period_end AND period_start <= period_end LIMIT 1)[0] != NONE
          AND (SELECT * FROM pod_liveness_record WHERE reference_id = $parent.out AND $end >= period_start AND $start <= period_end AND period_start <= period_end LIMIT 1)[0] != NONE
          LIMIT 1001)
    }
} AS result
FROM node
WHERE (SELECT * FROM node_liveness_record WHERE reference_id = $parent.id AND $end >= period_start AND $start <= period_end AND period_start <= period_end LIMIT 1)[0] != NONE
LIMIT 100;`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldRelations := ActiveEdgeServingRelations
			ActiveEdgeServingRelations = append([]string(nil), tt.servingRelations...)
			t.Cleanup(func() { ActiveEdgeServingRelations = oldRelations })

			query := req
			builder := NewSurrealQueryBuilderWithSchemaProvider(&query, provider)
			configureBuilderForGraphQueryMode(builder, tt.mode)

			actualSQL := builder.Build()
			if tt.mode == graphQueryModeRange && len(tt.servingRelations) > 0 {
				assert.Contains(t, actualSQL, "FROM node_with_pod_active_edge_view")
				assert.NotContains(t, actualSQL, "FROM node_with_pod WHERE")
				return
			}
			assert.Equal(t, tt.expectedSQL, actualSQL)
		})
	}

	t.Run("multi hop follows serving relation configuration", func(t *testing.T) {
		multiHopProvider := newTableSchemaProvider(
			map[ResourceType]tableResourceDefinition{
				ResourceTypeNode:       {primaryKeys: []string{"bcs_cluster_id", "node"}},
				ResourceTypePod:        {primaryKeys: []string{"bcs_cluster_id", "namespace", "pod"}},
				ResourceTypeReplicaSet: {primaryKeys: []string{"bcs_cluster_id", "namespace", "replicaset"}},
			},
			[]RelationSchema{
				{RelationType: RelationNodeWithPod, Category: RelationCategoryStatic, FromType: ResourceTypeNode, ToType: ResourceTypePod},
				{RelationType: RelationPodWithReplicaSet, Category: RelationCategoryStatic, FromType: ResourceTypePod, ToType: ResourceTypeReplicaSet},
			},
		)
		multiHopReq := QueryRequest{
			Timestamp:          300000,
			SourceType:         ResourceTypeNode,
			TargetType:         ResourceTypeReplicaSet,
			TargetTypeExplicit: true,
			MaxHops:            2,
		}
		path := resourcePath{Steps: []resourcePathStep{
			{ResourceType: string(ResourceTypeNode)},
			{ResourceType: string(ResourceTypePod), RelationType: string(RelationNodeWithPod), Category: string(RelationCategoryStatic), Direction: string(DirectionOutbound)},
			{ResourceType: string(ResourceTypeReplicaSet), RelationType: string(RelationPodWithReplicaSet), Category: string(RelationCategoryStatic), Direction: string(DirectionOutbound)},
		}}

		cases := []struct {
			name                  string
			servingRelations      []string
			expectedRoute         string
			expectedSQLContains   []string
			expectedSQLNotContain []string
		}{
			{
				name:             "all hops use serving",
				servingRelations: []string{string(RelationNodeWithPod), string(RelationPodWithReplicaSet)},
				expectedRoute:    "active_edge_serving",
				expectedSQLContains: []string{
					"FROM node_with_pod_active_edge_view",
					"entity_id: target_id",
					"FROM pod_with_replicaset_active_edge_view WHERE source_id = $parent.entity_id",
				},
			},
			{
				name:             "configured hop uses serving and remaining hop uses raw",
				servingRelations: []string{string(RelationNodeWithPod)},
				expectedRoute:    "mixed",
				expectedSQLContains: []string{
					"FROM node_with_pod_active_edge_view",
					"FROM pod_with_replicaset WHERE in = $parent.entity_id",
				},
				expectedSQLNotContain: []string{"FROM pod_with_replicaset_active_edge_view"},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				oldRelations := ActiveEdgeServingRelations
				ActiveEdgeServingRelations = append([]string(nil), tc.servingRelations...)
				t.Cleanup(func() { ActiveEdgeServingRelations = oldRelations })

				builder := NewSurrealQueryBuilderForPath(&multiHopReq, multiHopProvider, path)
				sql := builder.Build()
				assert.Equal(t, tc.expectedRoute, builder.routeName())
				for _, expected := range tc.expectedSQLContains {
					assert.Contains(t, sql, expected)
				}
				for _, unexpected := range tc.expectedSQLNotContain {
					assert.NotContains(t, sql, unexpected)
				}
			})
		}
	})
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
			name:       "bkbase query_sync direct list row parses host module relation graph",
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
		{
			name:       "bkbase root record miss returns no graph",
			queryStart: 1769910000000,
			queryEnd:   1776910000000,
			bkbaseResponse: `{
  "result": true,
  "code": "00",
  "data": {
    "total_records": 1,
    "device": "surrealdb",
    "list": [
      {"__ids": []}
    ]
  }
}`,
			expected: []tableGraphSummary{},
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

func TestNormalizeBKBaseGraphRows(t *testing.T) {
	directGraphRow := map[string]any{
		"root": map[string]any{"entity_id": "node:node-1"},
		"hop1": map[string]any{},
	}
	wrappedGraphRow := map[string]any{
		"result": map[string]any{
			"root": map[string]any{"entity_id": "node:node-1"},
		},
	}

	tests := []struct {
		name     string
		rows     []map[string]any
		expected []any
		errText  string
	}{
		{
			name:     "direct RETURN graph row",
			rows:     []map[string]any{directGraphRow},
			expected: []any{map[string]any{"result": directGraphRow}},
		},
		{
			name:     "wrapped SELECT graph row",
			rows:     []map[string]any{wrappedGraphRow},
			expected: []any{wrappedGraphRow},
		},
		{
			name:     "empty graph result",
			rows:     []map[string]any{},
			expected: []any{},
		},
		{
			name: "missing root record row is skipped",
			rows: []map[string]any{
				{"__ids": []any{}},
				directGraphRow,
			},
			expected: []any{map[string]any{"result": directGraphRow}},
		},
		{
			name:    "non empty root record marker is rejected",
			rows:    []map[string]any{{"__ids": []any{"node:node-1"}}},
			errText: "data.list[0]: missing field root or result for graph row",
		},
		{
			name:    "non graph row is rejected",
			rows:    []map[string]any{{"count": 1}},
			errText: "data.list[0]: missing field root or result for graph row",
		},
		{
			name:    "non object result wrapper is rejected",
			rows:    []map[string]any{{"result": []any{}}},
			errText: "data.list[0].result: expected object, got []interface {}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, err := normalizeBKBaseGraphRows(tt.rows)
			if tt.errText != "" {
				require.EqualError(t, err, "parse bkbase response: "+tt.errText)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, actual)
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
		expectedRequestCount    int
	}{
		{
			name:                 "instant query returns target from the direct path response",
			mode:                 graphQueryModeInstant,
			requestJSON:          `{"space_uid":"` + tableMockSpaceUID + `","timestamp":600000,"source_type":"node","source_info":{"node":"node-1"},"target_type":"pod","look_back_delta":600000}`,
			expectedResponseJSON: `{"path":["node","pod"],"matchers":[{"pod":"pod-1"}],"query_sync_requests":[{"path":["node","pod"],"prefer_storage":"surrealdb","properties":{"cluster_name":"mock_surrealdb_cluster"},"result_table_id":"mock_graph_result_table","contains_relations":["node_with_pod"],"not_contains_relations":["node_with_system","system_to_pod"]}]}`,
			expectedRequestCount: 1,
		},
		{
			name:                 "range query returns target series from the direct path response",
			mode:                 graphQueryModeRange,
			rangeStart:           0,
			rangeEnd:             600000,
			stepMs:               60000,
			requestJSON:          `{"space_uid":"` + tableMockSpaceUID + `","timestamp":600000,"source_type":"node","source_info":{"node":"node-1"},"target_type":"pod","look_back_delta":1200000}`,
			expectedResponseJSON: `{"path":["node","pod"],"matchers":[{"pod":"pod-1"}],"range_result":[{"timestamp":0,"matchers":[{"pod":"pod-1"}]},{"timestamp":60000,"matchers":[{"pod":"pod-1"}]},{"timestamp":120000,"matchers":[{"pod":"pod-1"}]},{"timestamp":180000,"matchers":[{"pod":"pod-1"}]},{"timestamp":240000,"matchers":[{"pod":"pod-1"}]},{"timestamp":300000,"matchers":[{"pod":"pod-1"}]},{"timestamp":360000,"matchers":[{"pod":"pod-1"}]},{"timestamp":420000,"matchers":[{"pod":"pod-1"}]},{"timestamp":480000,"matchers":[{"pod":"pod-1"}]},{"timestamp":540000,"matchers":[{"pod":"pod-1"}]},{"timestamp":600000,"matchers":[{"pod":"pod-1"}]}],"query_sync_requests":[{"path":["node","pod"],"prefer_storage":"surrealdb","properties":{"cluster_name":"mock_surrealdb_cluster"},"result_table_id":"mock_graph_result_table","contains_relations":["node_with_pod"],"not_contains_relations":["node_with_system","system_to_pod"]}]}`,
			expectedRequestCount: 1,
		},
		{
			name:        "instant query falls back to indirect path when direct path is empty",
			mode:        graphQueryModeInstant,
			requestJSON: `{"space_uid":"` + tableMockSpaceUID + `","timestamp":600000,"source_type":"node","source_info":{"node":"node-1"},"target_type":"pod","look_back_delta":600000}`,
			bkbaseResponseOverrides: map[string]string{
				"node/pod": tableEmptyBKBaseResponseJSON,
			},
			expectedResponseJSON: `{"path":["node","system","pod"],"matchers":[{"pod":"pod-via-system"}],"query_sync_requests":[{"path":["node","pod"],"prefer_storage":"surrealdb","properties":{"cluster_name":"mock_surrealdb_cluster"},"result_table_id":"mock_graph_result_table","contains_relations":["node_with_pod"],"not_contains_relations":["node_with_system","system_to_pod"]},{"path":["node","system","pod"],"prefer_storage":"surrealdb","properties":{"cluster_name":"mock_surrealdb_cluster"},"result_table_id":"mock_graph_result_table","contains_relations":["node_with_system","system_to_pod"],"not_contains_relations":["node_with_pod"]}]}`,
			expectedRequestCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := decodeTableQueryRequestJSON(t, tt.requestJSON)
			server := newSurrealDBMockServer(t, tablePathSplitBKBaseResponsesBySurrealQL(t, req, provider, tt.mode, tt.bkbaseResponseOverrides))
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
				tt.mode,
				tt.rangeStart,
				tt.rangeEnd,
				tt.stepMs,
			)
			require.NoError(t, err)

			actualResponse := tablePathSplitQueryResponse{
				Path:              convertResourcePathToResources(paths),
				Matchers:          matchers,
				QuerySyncRequests: tablePathSplitQuerySyncRequestSummaries(t, server.Requests()),
			}
			if tt.mode == graphQueryModeRange {
				extractionPathResource := targetExtractionPathResource(&req)
				if len(paths) > 0 {
					extractionPathResource = resourcePathTypes(paths[0])
				}
				actualResponse.RangeResult = tableRangeResultFromMatchersWithTimestamp(
					buildTargetMatchersTimeSeriesWithOptions(
						graphs,
						req.TargetType,
						extractionPathResource,
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

			require.Len(t, actualResponse.QuerySyncRequests, tt.expectedRequestCount)
		})
	}
}

func TestActiveEdgeServingQuerySyncTableDriven(t *testing.T) {
	provider := newTableSchemaProvider(
		map[ResourceType]tableResourceDefinition{
			ResourceTypeHost: {
				primaryKeys: []string{"bk_host_id"},
				fieldTypes:  map[string]string{"bk_host_id": "integer"},
			},
			ResourceTypeModule: {
				primaryKeys: []string{"bk_module_id"},
				fieldTypes:  map[string]string{"bk_module_id": "integer"},
			},
		},
		[]RelationSchema{{
			RelationType: RelationHostWithModule,
			Category:     RelationCategoryStatic,
			FromType:     ResourceTypeHost,
			ToType:       ResourceTypeModule,
		}},
	)

	forwardResponse := tableActiveEdgeServingResponseJSON(
		t,
		ResourceTypeHost,
		"host:⟨bk_host_id=38268⟩",
		map[string]string{"bk_host_id": "38268"},
		"host_with_module:⟨38268||10259⟩",
		ResourceTypeModule,
		"module:⟨bk_module_id=10259⟩",
		map[string]string{"bk_module_id": "10259"},
	)
	reverseResponse := tableActiveEdgeServingResponseJSON(
		t,
		ResourceTypeModule,
		"module:⟨bk_module_id=10259⟩",
		map[string]string{"bk_module_id": "10259"},
		"host_with_module:⟨38268||10259⟩",
		ResourceTypeHost,
		"host:⟨bk_host_id=38268⟩",
		map[string]string{"bk_host_id": "38268"},
	)

	tests := []struct {
		name                   string
		requestJSON            string
		responseJSON           string
		mode                   graphQueryMode
		rangeStart             int64
		rangeEnd               int64
		stepMs                 int64
		expectedPath           []string
		expectedMatchers       cmdb.Matchers
		expectedRangeResult    []tableMatchersWithTimestampJSON
		expectedMatchClause    string
		expectedDataProjection string
	}{
		{
			name:                   "forward serving response returns module primary key matcher",
			requestJSON:            `{"space_uid":"` + tableMockSpaceUID + `","timestamp":600000,"source_type":"host","source_info":{"bk_host_id":"38268"},"target_type":"module","look_back_delta":600000}`,
			responseJSON:           forwardResponse,
			mode:                   graphQueryModeInstant,
			expectedPath:           []string{"host", "module"},
			expectedMatchers:       cmdb.Matchers{{"bk_module_id": "10259"}},
			expectedMatchClause:    "source_id = $parent.id",
			expectedDataProjection: "entity_data: target_data",
		},
		{
			name:                   "reverse serving response returns host primary key matcher",
			requestJSON:            `{"space_uid":"` + tableMockSpaceUID + `","timestamp":600000,"source_type":"module","source_info":{"bk_module_id":"10259"},"target_type":"host","look_back_delta":600000}`,
			responseJSON:           reverseResponse,
			mode:                   graphQueryModeInstant,
			expectedPath:           []string{"module", "host"},
			expectedMatchers:       cmdb.Matchers{{"bk_host_id": "38268"}},
			expectedMatchClause:    "target_id = $parent.id",
			expectedDataProjection: "entity_data: source_data",
		},
		{
			name:         "range serving response produces target buckets from active period",
			requestJSON:  `{"space_uid":"` + tableMockSpaceUID + `","timestamp":600000,"source_type":"host","source_info":{"bk_host_id":"38268"},"target_type":"module","look_back_delta":600000}`,
			responseJSON: forwardResponse,
			mode:         graphQueryModeRange,
			rangeStart:   0,
			rangeEnd:     600000,
			stepMs:       300000,
			expectedPath: []string{"host", "module"},
			expectedRangeResult: []tableMatchersWithTimestampJSON{
				{Timestamp: 0, Matchers: cmdb.Matchers{{"bk_module_id": "10259"}}},
				{Timestamp: 300000, Matchers: cmdb.Matchers{{"bk_module_id": "10259"}}},
				{Timestamp: 600000, Matchers: cmdb.Matchers{{"bk_module_id": "10259"}}},
			},
			expectedMatchClause:    "source_data.bk_host_id = '38268'",
			expectedDataProjection: "entity_data: target_data",
		},
	}

	oldRelations := ActiveEdgeServingRelations
	oldFlatRelations := FlatOneHopActiveEdgeServingRelations
	ActiveEdgeServingRelations = []string{string(RelationHostWithModule)}
	t.Cleanup(func() {
		ActiveEdgeServingRelations = oldRelations
		FlatOneHopActiveEdgeServingRelations = oldFlatRelations
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mode == graphQueryModeRange {
				FlatOneHopActiveEdgeServingRelations = []string{string(RelationHostWithModule)}
			} else {
				FlatOneHopActiveEdgeServingRelations = nil
			}

			req := decodeTableQueryRequestJSON(t, tt.requestJSON)
			responses := tableActiveEdgeServingResponsesBySurrealQL(t, req, provider, tt.mode, tt.responseJSON)
			server := newSurrealDBMockServer(t, responses)
			t.Cleanup(server.Close)

			restoreQueryURL := setTableBKBaseQueryURLForTest(server.URL)
			t.Cleanup(restoreQueryURL)

			resolver := &BindingResolver{cache: make(map[string]*bindingCacheEntry)}
			resolver.storeCache(tableMockBindingCacheKey, tableMockBindingInfo())

			model, err := NewModel(context.Background(), &BKBaseSurrealDBClient{curl: &curl.HttpCurl{}})
			require.NoError(t, err)
			model.SetResolver(resolver)
			model.SetSchemaProvider(provider)

			graphs, paths, matchers, err := model.queryLivenessGraph(
				context.Background(),
				&req,
				tt.mode,
				tt.rangeStart,
				tt.rangeEnd,
				tt.stepMs,
			)
			require.NoError(t, err)
			require.NotEmpty(t, graphs)
			assert.Equal(t, tt.expectedPath, convertResourcePathToResources(paths))
			if tt.mode == graphQueryModeRange {
				assert.Equal(t, cmdb.Matchers{{"bk_module_id": "10259"}}, matchers)
				rangeResult := tableRangeResultFromMatchersWithTimestamp(buildTargetMatchersTimeSeriesWithOptions(
					graphs,
					req.TargetType,
					resourcePathTypes(paths[0]),
					tt.rangeStart,
					tt.rangeEnd,
					tt.stepMs,
					provider,
					req.SchemaNamespace(),
					req.TargetInfoShow,
					shouldIncludeRootTarget(&req),
				))
				assert.Equal(t, tt.expectedRangeResult, rangeResult)
			} else {
				assert.Equal(t, tt.expectedMatchers, matchers)
			}

			requests := server.Requests()
			require.Len(t, requests, 1)
			assert.Equal(t, PreferStorageSurrealDB, requests[0].Body["prefer_storage"])
			assert.Equal(t, tableMockDatabase, requests[0].SQLPayload.ResultTableID)

			dsl := requests[0].SQLPayload.DSL
			assert.Contains(t, dsl, "FROM host_with_module_active_edge_view")
			assert.Contains(t, dsl, tt.expectedMatchClause)
			assert.Contains(t, dsl, tt.expectedDataProjection)
			assert.Contains(t, dsl, "active_period_start_ms <= $end_ms")
			assert.Contains(t, dsl, "active_period_end_ms >= $start_ms")
			assert.NotContains(t, dsl, "host_with_module_liveness_record")
		})
	}
}

func TestSurrealQueryBuilderForPathUsesRelationOnlyLiveness(t *testing.T) {
	provider := newTableSchemaProvider(
		map[ResourceType]tableResourceDefinition{
			ResourceTypeNode: {primaryKeys: []string{"node"}},
			ResourceTypePod:  {primaryKeys: []string{"pod"}},
		},
		[]RelationSchema{{
			RelationType: RelationNodeWithPod,
			Category:     RelationCategoryStatic,
			FromType:     ResourceTypeNode,
			ToType:       ResourceTypePod,
		}},
	)
	req := &QueryRequest{
		Timestamp:          300000,
		SourceType:         ResourceTypeNode,
		SourceInfo:         cmdb.Matcher{"node": "node-1"},
		TargetType:         ResourceTypePod,
		TargetTypeExplicit: true,
	}
	path := resourcePath{Steps: []resourcePathStep{
		{ResourceType: string(ResourceTypeNode)},
		{
			ResourceType: string(ResourceTypePod),
			RelationType: string(RelationNodeWithPod),
			Category:     string(RelationCategoryStatic),
			Direction:    string(DirectionOutbound),
		},
	}}

	oldRelations := ActiveEdgeServingRelations
	t.Cleanup(func() { ActiveEdgeServingRelations = oldRelations })

	ActiveEdgeServingRelations = nil
	rawSQL := NewSurrealQueryBuilderForPath(req, provider, path).Build()
	assert.Contains(t, rawSQL, "node_with_pod_liveness_record")
	assert.NotContains(t, rawSQL, "FROM node_liveness_record")
	assert.NotContains(t, rawSQL, "FROM pod_liveness_record")
	assert.Contains(t, rawSQL, ResponseFieldRelationLiveness+":")

	ActiveEdgeServingRelations = []string{string(RelationNodeWithPod)}
	servingBuilder := NewSurrealQueryBuilderForPath(req, provider, path)
	servingSQL := servingBuilder.Build()
	assert.Equal(t, "active_edge_serving", servingBuilder.routeName())
	assert.Contains(t, servingSQL, "FROM node_with_pod_active_edge_view")
	assert.NotContains(t, servingSQL, "FROM node_liveness_record")
	assert.NotContains(t, servingSQL, "FROM pod_liveness_record")
	assert.Contains(t, servingSQL, ResponseFieldRelationLiveness+":")

	rootOnlyPath := resourcePath{Steps: []resourcePathStep{{ResourceType: string(ResourceTypeNode)}}}
	rootOnlySQL := NewSurrealQueryBuilderForPath(req, provider, rootOnlyPath).Build()
	assert.Contains(t, rootOnlySQL, "node_liveness_record")
}

func tableActiveEdgeServingResponsesBySurrealQL(
	t *testing.T,
	req QueryRequest,
	provider SchemaProvider,
	mode graphQueryMode,
	responseJSON string,
) map[surrealQL]string {
	t.Helper()

	req.Normalize()
	adjustMaxHopsForUnconstrainedPath(&req, provider)
	pFinder := NewPathFinder(
		WithAllowedCategories(req.AllowedRelationTypes...),
		WithDynamicDirection(req.DynamicRelationDirection),
		WithMaxHops(req.MaxHops),
		WithSchemaProvider(provider),
		WithNamespace(req.SchemaNamespace()),
	)
	paths, err := pFinder.FindAllPaths(req.SourceType, req.TargetType, req.PathResource)
	require.NoError(t, err)
	require.Len(t, paths, 1)

	builder := NewSurrealQueryBuilderForPath(&req, provider, paths[0])
	configureBuilderForGraphQueryMode(builder, mode)
	return map[surrealQL]string{
		surrealQL(tableMockUseNSDBStatement + builder.Build()): responseJSON,
	}
}

func tableActiveEdgeServingResponseJSON(
	t *testing.T,
	rootType ResourceType,
	rootID string,
	rootData map[string]string,
	relationID string,
	targetType ResourceType,
	targetID string,
	targetData map[string]string,
) string {
	t.Helper()

	response := map[string]any{
		"result": true,
		"code":   "00",
		"data": map[string]any{
			"list": []any{
				map[string]any{
					"result": map[string]any{
						"root": map[string]any{
							"entity_type": string(rootType),
							"entity_id":   rootID,
							"entity_data": rootData,
							"liveness":    []any{map[string]any{"period_start": 0, "period_end": 600000}},
						},
						"hop1": map[string]any{
							string(RelationHostWithModule): []any{
								map[string]any{
									"hop":               1,
									"relation_type":     string(RelationHostWithModule),
									"relation_category": string(RelationCategoryStatic),
									"relation_id":       relationID,
									"relation_liveness": []any{map[string]any{"period_start": 0, "period_end": 600000}},
									"target": map[string]any{
										"entity_type": string(targetType),
										"entity_id":   targetID,
										"entity_data": targetData,
										"liveness":    []any{map[string]any{"period_start": 0, "period_end": 600000}},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	data, err := json.Marshal(response)
	require.NoError(t, err)
	return string(data)
}

type tableResourceDefinition struct {
	primaryKeys []string
	fields      []string
	fieldTypes  map[string]string
}

func (p *tableSchemaProvider) GetResourceFieldType(_ string, resourceType ResourceType, field string) string {
	return p.resources[resourceType].fieldTypes[field]
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
