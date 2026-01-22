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
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// E2ETestCase 端到端测试用例
// 完整测试流程：QueryRequest → SurrealQL → MockResponse → []*LivenessGraph
type E2ETestCase struct {
	Name           string           // 测试用例名称
	QueryRequest   *QueryRequest    // 输入：查询请求
	ExpectedSQL    string           // 期望：生成的 SurrealQL（完整匹配）
	MockResponse   []map[string]any // Mock：SurrealDB 返回的响应
	ExpectedGraphs []*LivenessGraph // 期望：解析后的 LivenessGraph 列表（每个起始实体一个图）
}

// 测试用例 1: Node 查询（单跳静态关系）- 关系数量可控
func getNodeStaticTestCase() E2ETestCase {
	return E2ETestCase{
		Name: "Node_SingleHop_StaticOnly",
		QueryRequest: &QueryRequest{
			Timestamp:            600000,
			SourceType:           ResourceTypeNode,
			SourceInfo:           map[string]string{"bcs_cluster_id": "BCS-K8S-00001", "node": "node-1"},
			MaxHops:              1,
			AllowedRelationTypes: []RelationCategory{RelationCategoryStatic},
			LookBackDelta:        600000,
			Limit:                10,
		},
		ExpectedSQL: `LET $timestamp = 600000;
LET $look_back_delta = 600000;
LET $start = 0;
LET $end = 600000;

SELECT {
    root: {
        entity_type: meta::tb(id),
        entity_id: <string>id,
        entity_data: { bcs_cluster_id: bcs_cluster_id, node: node },
        created_at: created_at,
        updated_at: updated_at,
        liveness: (SELECT * FROM node_liveness_record WHERE node_id = $parent.id AND period_end >= $start AND period_start <= $end)
    },

    hop1: {
        node_with_system: (SELECT {
            hop: 1,
            relation_type: 'node_with_system',
            relation_category: 'static',
            relation_id: <string>id,
            relation_liveness: (SELECT * FROM node_with_system_liveness_record WHERE relation_id = $parent.id AND period_end >= $start AND period_start <= $end),
            target: {
                entity_type: 'system',
                entity_id: <string>target_id,
                entity_data: { bk_cloud_id: target_id.bk_cloud_id, bk_target_ip: target_id.bk_target_ip },
                liveness: (SELECT * FROM system_liveness_record WHERE system_id = $parent.target_id AND period_end >= $start AND period_start <= $end)
            }
        } FROM node_with_system WHERE source_id = $parent.id
          AND (SELECT count() FROM only node_with_system_liveness_record WHERE relation_id = $parent.id AND $end >= period_start AND $start <= period_end GROUP ALL) > 0),
        node_with_pod: (SELECT {
            hop: 1,
            relation_type: 'node_with_pod',
            relation_category: 'static',
            relation_id: <string>id,
            relation_liveness: (SELECT * FROM node_with_pod_liveness_record WHERE relation_id = $parent.id AND period_end >= $start AND period_start <= $end),
            target: {
                entity_type: 'pod',
                entity_id: <string>target_id,
                entity_data: { bcs_cluster_id: target_id.bcs_cluster_id, namespace: target_id.namespace, pod: target_id.pod },
                liveness: (SELECT * FROM pod_liveness_record WHERE pod_id = $parent.target_id AND period_end >= $start AND period_start <= $end)
            }
        } FROM node_with_pod WHERE source_id = $parent.id
          AND (SELECT count() FROM only node_with_pod_liveness_record WHERE relation_id = $parent.id AND $end >= period_start AND $start <= period_end GROUP ALL) > 0),
        datasource_with_node: (SELECT {
            hop: 1,
            relation_type: 'datasource_with_node',
            relation_category: 'static',
            relation_id: <string>id,
            relation_liveness: (SELECT * FROM datasource_with_node_liveness_record WHERE relation_id = $parent.id AND period_end >= $start AND period_start <= $end),
            target: {
                entity_type: 'datasource',
                entity_id: <string>source_id,
                entity_data: { bk_data_id: source_id.bk_data_id },
                liveness: (SELECT * FROM datasource_liveness_record WHERE datasource_id = $parent.source_id AND period_end >= $start AND period_start <= $end)
            }
        } FROM datasource_with_node WHERE target_id = $parent.id
          AND (SELECT count() FROM only datasource_with_node_liveness_record WHERE relation_id = $parent.id AND $end >= period_start AND $start <= period_end GROUP ALL) > 0)
    }
} AS result
FROM node
WHERE bcs_cluster_id = 'BCS-K8S-00001'
  AND node = 'node-1'
  AND (SELECT count() FROM only node_liveness_record WHERE node_id = $parent.id AND $end >= period_start AND $start <= period_end GROUP ALL) > 0
LIMIT 10;`,
		MockResponse: []map[string]any{
			{
				"result": []any{
					map[string]any{
						"result": map[string]any{
							"root": map[string]any{
								"entity_type": "node",
								"entity_id":   "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩",
								"entity_data": map[string]any{
									"bcs_cluster_id": "BCS-K8S-00001",
									"node":           "node-1",
								},
								"liveness": []any{
									map[string]any{"period_start": float64(100000), "period_end": float64(500000)},
								},
							},
							"hop1": map[string]any{
								"node_with_system": []any{
									map[string]any{
										"hop":               float64(1),
										"relation_type":     "node_with_system",
										"relation_category": "static",
										"relation_id":       "node_with_system:1",
										"relation_liveness": []any{
											map[string]any{"period_start": float64(100000), "period_end": float64(500000)},
										},
										"target": map[string]any{
											"entity_type": "system",
											"entity_id":   "system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩",
											"entity_data": map[string]any{
												"bk_cloud_id":  "0",
												"bk_target_ip": "192.168.1.1",
											},
											"liveness": []any{
												map[string]any{"period_start": float64(100000), "period_end": float64(500000)},
											},
										},
									},
								},
								"node_with_pod": []any{
									map[string]any{
										"hop":               float64(1),
										"relation_type":     "node_with_pod",
										"relation_category": "static",
										"relation_id":       "node_with_pod:1",
										"relation_liveness": []any{
											map[string]any{"period_start": float64(100000), "period_end": float64(500000)},
										},
										"target": map[string]any{
											"entity_type": "pod",
											"entity_id":   "pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩",
											"entity_data": map[string]any{
												"bcs_cluster_id": "BCS-K8S-00001",
												"namespace":      "default",
												"pod":            "nginx-1",
											},
											"liveness": []any{
												map[string]any{"period_start": float64(100000), "period_end": float64(500000)},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		ExpectedGraphs: []*LivenessGraph{
			{
				QueryStart: 0,
				QueryEnd:   600000,
				Nodes: map[string]*NodeLiveness{
					"node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩": {
						ResourceID:   "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩",
						ResourceType: ResourceTypeNode,
						Labels: map[string]string{
							"bcs_cluster_id": "BCS-K8S-00001",
							"node":           "node-1",
						},
						RawPeriods: []*VisiblePeriod{{Start: 100000, End: 500000}},
					},
					"system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩": {
						ResourceID:   "system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩",
						ResourceType: ResourceTypeSystem,
						Labels: map[string]string{
							"bk_cloud_id":  "0",
							"bk_target_ip": "192.168.1.1",
						},
						RawPeriods: []*VisiblePeriod{{Start: 100000, End: 500000}},
					},
					"pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩": {
						ResourceID:   "pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩",
						ResourceType: ResourceTypePod,
						Labels: map[string]string{
							"bcs_cluster_id": "BCS-K8S-00001",
							"namespace":      "default",
							"pod":            "nginx-1",
						},
						RawPeriods: []*VisiblePeriod{{Start: 100000, End: 500000}},
					},
				},
				Edges: map[string]*EdgeLiveness{
					"node_with_system:1": {
						RelationID:   "node_with_system:1",
						RelationType: RelationNodeWithSystem,
						Category:     RelationCategoryStatic,
						Direction:    "",
						FromID:       "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩",
						ToID:         "system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩",
						RawPeriods:   []*VisiblePeriod{{Start: 100000, End: 500000}},
					},
					"node_with_pod:1": {
						RelationID:   "node_with_pod:1",
						RelationType: RelationNodeWithPod,
						Category:     RelationCategoryStatic,
						Direction:    "",
						FromID:       "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩",
						ToID:         "pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩",
						RawPeriods:   []*VisiblePeriod{{Start: 100000, End: 500000}},
					},
				},
				Adjacency: map[string][]string{
					"node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩":                  {"node_with_system:1", "node_with_pod:1"},
					"system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩":                  {},
					"pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩": {},
				},
			},
		},
	}
}

// 测试用例 2: System 查询（动态关系 outbound）- 关系数量可控
func getSystemDynamicOutboundTestCase() E2ETestCase {
	return E2ETestCase{
		Name: "System_DynamicOutbound",
		QueryRequest: &QueryRequest{
			Timestamp:                600000,
			SourceType:               ResourceTypeSystem,
			SourceInfo:               map[string]string{"bk_cloud_id": "0", "bk_target_ip": "192.168.1.1"},
			MaxHops:                  1,
			AllowedRelationTypes:     []RelationCategory{RelationCategoryDynamic},
			DynamicRelationDirection: DirectionOutbound,
			LookBackDelta:            600000,
			Limit:                    10,
		},
		ExpectedSQL: `LET $timestamp = 600000;
LET $look_back_delta = 600000;
LET $start = 0;
LET $end = 600000;

SELECT {
    root: {
        entity_type: meta::tb(id),
        entity_id: <string>id,
        entity_data: { bk_cloud_id: bk_cloud_id, bk_target_ip: bk_target_ip },
        created_at: created_at,
        updated_at: updated_at,
        liveness: (SELECT * FROM system_liveness_record WHERE system_id = $parent.id AND period_end >= $start AND period_start <= $end)
    },

    hop1: {
        system_to_pod_outbound: (SELECT {
            hop: 1,
            relation_type: 'system_to_pod',
            relation_category: 'dynamic',
            direction: 'outbound',
            relation_id: <string>id,
            relation_liveness: (SELECT * FROM system_to_pod_liveness_record WHERE relation_id = $parent.id AND period_end >= $start AND period_start <= $end),
            target: {
                entity_type: 'pod',
                entity_id: <string>target_id,
                entity_data: { bcs_cluster_id: target_id.bcs_cluster_id, namespace: target_id.namespace, pod: target_id.pod },
                liveness: (SELECT * FROM pod_liveness_record WHERE pod_id = $parent.target_id AND period_end >= $start AND period_start <= $end)
            }
        } FROM system_to_pod WHERE source_id = $parent.id
          AND (SELECT count() FROM only system_to_pod_liveness_record WHERE relation_id = $parent.id AND $end >= period_start AND $start <= period_end GROUP ALL) > 0),
        system_to_system_outbound: (SELECT {
            hop: 1,
            relation_type: 'system_to_system',
            relation_category: 'dynamic',
            direction: 'outbound',
            relation_id: <string>id,
            relation_liveness: (SELECT * FROM system_to_system_liveness_record WHERE relation_id = $parent.id AND period_end >= $start AND period_start <= $end),
            target: {
                entity_type: 'system',
                entity_id: <string>target_id,
                entity_data: { bk_cloud_id: target_id.bk_cloud_id, bk_target_ip: target_id.bk_target_ip },
                liveness: (SELECT * FROM system_liveness_record WHERE system_id = $parent.target_id AND period_end >= $start AND period_start <= $end)
            }
        } FROM system_to_system WHERE source_id = $parent.id
          AND (SELECT count() FROM only system_to_system_liveness_record WHERE relation_id = $parent.id AND $end >= period_start AND $start <= period_end GROUP ALL) > 0)
    }
} AS result
FROM system
WHERE bk_cloud_id = '0'
  AND bk_target_ip = '192.168.1.1'
  AND (SELECT count() FROM only system_liveness_record WHERE system_id = $parent.id AND $end >= period_start AND $start <= period_end GROUP ALL) > 0
LIMIT 10;`,
		MockResponse: []map[string]any{
			{
				"result": []any{
					map[string]any{
						"result": map[string]any{
							"root": map[string]any{
								"entity_type": "system",
								"entity_id":   "system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩",
								"entity_data": map[string]any{
									"bk_cloud_id":  "0",
									"bk_target_ip": "192.168.1.1",
								},
								"liveness": []any{
									map[string]any{"period_start": float64(100000), "period_end": float64(500000)},
								},
							},
							"hop1": map[string]any{
								"system_to_pod_outbound": []any{
									map[string]any{
										"hop":               float64(1),
										"relation_type":     "system_to_pod",
										"relation_category": "dynamic",
										"direction":         "outbound",
										"relation_id":       "system_to_pod:1",
										"relation_liveness": []any{
											map[string]any{"period_start": float64(200000), "period_end": float64(400000)},
										},
										"target": map[string]any{
											"entity_type": "pod",
											"entity_id":   "pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩",
											"entity_data": map[string]any{
												"bcs_cluster_id": "BCS-K8S-00001",
												"namespace":      "default",
												"pod":            "nginx-1",
											},
											"liveness": []any{
												map[string]any{"period_start": float64(100000), "period_end": float64(500000)},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		ExpectedGraphs: []*LivenessGraph{
			{
				QueryStart: 0,
				QueryEnd:   600000,
				Nodes: map[string]*NodeLiveness{
					"system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩": {
						ResourceID:   "system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩",
						ResourceType: ResourceTypeSystem,
						Labels: map[string]string{
							"bk_cloud_id":  "0",
							"bk_target_ip": "192.168.1.1",
						},
						RawPeriods: []*VisiblePeriod{{Start: 100000, End: 500000}},
					},
					"pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩": {
						ResourceID:   "pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩",
						ResourceType: ResourceTypePod,
						Labels: map[string]string{
							"bcs_cluster_id": "BCS-K8S-00001",
							"namespace":      "default",
							"pod":            "nginx-1",
						},
						RawPeriods: []*VisiblePeriod{{Start: 100000, End: 500000}},
					},
				},
				Edges: map[string]*EdgeLiveness{
					"system_to_pod:1": {
						RelationID:   "system_to_pod:1",
						RelationType: RelationSystemToPod,
						Category:     RelationCategoryDynamic,
						Direction:    DirectionOutbound,
						FromID:       "system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩",
						ToID:         "pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩",
						RawPeriods:   []*VisiblePeriod{{Start: 200000, End: 400000}},
					},
				},
				Adjacency: map[string][]string{
					"system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩":                  {"system_to_pod:1"},
					"pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩": {},
				},
			},
		},
	}
}

// 测试用例 3: 空响应
func getEmptyResponseTestCase() E2ETestCase {
	return E2ETestCase{
		Name: "EmptyResponse_NoMatchingNode",
		QueryRequest: &QueryRequest{
			Timestamp:            600000,
			SourceType:           ResourceTypeNode,
			SourceInfo:           map[string]string{"bcs_cluster_id": "BCS-K8S-00001", "node": "non-existent"},
			MaxHops:              1,
			AllowedRelationTypes: []RelationCategory{RelationCategoryStatic},
			LookBackDelta:        600000,
			Limit:                10,
		},
		ExpectedSQL: `LET $timestamp = 600000;
LET $look_back_delta = 600000;
LET $start = 0;
LET $end = 600000;

SELECT {
    root: {
        entity_type: meta::tb(id),
        entity_id: <string>id,
        entity_data: { bcs_cluster_id: bcs_cluster_id, node: node },
        created_at: created_at,
        updated_at: updated_at,
        liveness: (SELECT * FROM node_liveness_record WHERE node_id = $parent.id AND period_end >= $start AND period_start <= $end)
    },

    hop1: {
        node_with_system: (SELECT {
            hop: 1,
            relation_type: 'node_with_system',
            relation_category: 'static',
            relation_id: <string>id,
            relation_liveness: (SELECT * FROM node_with_system_liveness_record WHERE relation_id = $parent.id AND period_end >= $start AND period_start <= $end),
            target: {
                entity_type: 'system',
                entity_id: <string>target_id,
                entity_data: { bk_cloud_id: target_id.bk_cloud_id, bk_target_ip: target_id.bk_target_ip },
                liveness: (SELECT * FROM system_liveness_record WHERE system_id = $parent.target_id AND period_end >= $start AND period_start <= $end)
            }
        } FROM node_with_system WHERE source_id = $parent.id
          AND (SELECT count() FROM only node_with_system_liveness_record WHERE relation_id = $parent.id AND $end >= period_start AND $start <= period_end GROUP ALL) > 0),
        node_with_pod: (SELECT {
            hop: 1,
            relation_type: 'node_with_pod',
            relation_category: 'static',
            relation_id: <string>id,
            relation_liveness: (SELECT * FROM node_with_pod_liveness_record WHERE relation_id = $parent.id AND period_end >= $start AND period_start <= $end),
            target: {
                entity_type: 'pod',
                entity_id: <string>target_id,
                entity_data: { bcs_cluster_id: target_id.bcs_cluster_id, namespace: target_id.namespace, pod: target_id.pod },
                liveness: (SELECT * FROM pod_liveness_record WHERE pod_id = $parent.target_id AND period_end >= $start AND period_start <= $end)
            }
        } FROM node_with_pod WHERE source_id = $parent.id
          AND (SELECT count() FROM only node_with_pod_liveness_record WHERE relation_id = $parent.id AND $end >= period_start AND $start <= period_end GROUP ALL) > 0),
        datasource_with_node: (SELECT {
            hop: 1,
            relation_type: 'datasource_with_node',
            relation_category: 'static',
            relation_id: <string>id,
            relation_liveness: (SELECT * FROM datasource_with_node_liveness_record WHERE relation_id = $parent.id AND period_end >= $start AND period_start <= $end),
            target: {
                entity_type: 'datasource',
                entity_id: <string>source_id,
                entity_data: { bk_data_id: source_id.bk_data_id },
                liveness: (SELECT * FROM datasource_liveness_record WHERE datasource_id = $parent.source_id AND period_end >= $start AND period_start <= $end)
            }
        } FROM datasource_with_node WHERE target_id = $parent.id
          AND (SELECT count() FROM only datasource_with_node_liveness_record WHERE relation_id = $parent.id AND $end >= period_start AND $start <= period_end GROUP ALL) > 0)
    }
} AS result
FROM node
WHERE bcs_cluster_id = 'BCS-K8S-00001'
  AND node = 'non-existent'
  AND (SELECT count() FROM only node_liveness_record WHERE node_id = $parent.id AND $end >= period_start AND $start <= period_end GROUP ALL) > 0
LIMIT 10;`,
		MockResponse: []map[string]any{
			{
				"result": []any{},
			},
		},
		ExpectedGraphs: []*LivenessGraph{},
	}
}

// 测试用例 4: 多跳查询 Node -> System（2跳静态）
func getMultiHopNodeToSystemTestCase() E2ETestCase {
	return E2ETestCase{
		Name: "MultiHop_NodeToSystem",
		QueryRequest: &QueryRequest{
			Timestamp:            600000,
			SourceType:           ResourceTypeNode,
			SourceInfo:           map[string]string{"bcs_cluster_id": "BCS-K8S-00001", "node": "node-1"},
			MaxHops:              2,
			AllowedRelationTypes: []RelationCategory{RelationCategoryStatic},
			LookBackDelta:        600000,
			Limit:                10,
		},
		// SQL 太长，此处省略完整 SQL，改用 MockResponse 和 ExpectedGraph 验证解析逻辑
		ExpectedSQL: "", // 设为空表示跳过 SQL 验证
		MockResponse: []map[string]any{
			{
				"result": []any{
					map[string]any{
						"result": map[string]any{
							"root": map[string]any{
								"entity_type": "node",
								"entity_id":   "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩",
								"entity_data": map[string]any{
									"bcs_cluster_id": "BCS-K8S-00001",
									"node":           "node-1",
								},
								"liveness": []any{
									map[string]any{"period_start": float64(100000), "period_end": float64(500000)},
								},
							},
							"hop1": map[string]any{
								"node_with_system": []any{
									map[string]any{
										"hop":               float64(1),
										"relation_type":     "node_with_system",
										"relation_category": "static",
										"relation_id":       "node_with_system:1",
										"relation_liveness": []any{
											map[string]any{"period_start": float64(100000), "period_end": float64(500000)},
										},
										"target": map[string]any{
											"entity_type": "system",
											"entity_id":   "system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩",
											"entity_data": map[string]any{
												"bk_cloud_id":  "0",
												"bk_target_ip": "192.168.1.1",
											},
											"liveness": []any{
												map[string]any{"period_start": float64(100000), "period_end": float64(500000)},
											},
											"hop2": map[string]any{
												"host_with_system": []any{
													map[string]any{
														"hop":               float64(2),
														"relation_type":     "host_with_system",
														"relation_category": "static",
														"relation_id":       "host_with_system:1",
														"relation_liveness": []any{
															map[string]any{"period_start": float64(100000), "period_end": float64(500000)},
														},
														"target": map[string]any{
															"entity_type": "host",
															"entity_id":   "host:⟨bk_host_id=12345⟩",
															"entity_data": map[string]any{
																"bk_host_id": "12345",
															},
															"liveness": []any{
																map[string]any{"period_start": float64(100000), "period_end": float64(500000)},
															},
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		ExpectedGraphs: []*LivenessGraph{
			{
				QueryStart: 0,
				QueryEnd:   600000,
				Nodes: map[string]*NodeLiveness{
					"node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩": {
						ResourceID:   "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩",
						ResourceType: ResourceTypeNode,
						Labels: map[string]string{
							"bcs_cluster_id": "BCS-K8S-00001",
							"node":           "node-1",
						},
						RawPeriods: []*VisiblePeriod{{Start: 100000, End: 500000}},
					},
					"system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩": {
						ResourceID:   "system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩",
						ResourceType: ResourceTypeSystem,
						Labels: map[string]string{
							"bk_cloud_id":  "0",
							"bk_target_ip": "192.168.1.1",
						},
						RawPeriods: []*VisiblePeriod{{Start: 100000, End: 500000}},
					},
					"host:⟨bk_host_id=12345⟩": {
						ResourceID:   "host:⟨bk_host_id=12345⟩",
						ResourceType: ResourceTypeHost,
						Labels: map[string]string{
							"bk_host_id": "12345",
						},
						RawPeriods: []*VisiblePeriod{{Start: 100000, End: 500000}},
					},
				},
				Edges: map[string]*EdgeLiveness{
					"node_with_system:1": {
						RelationID:   "node_with_system:1",
						RelationType: RelationNodeWithSystem,
						Category:     RelationCategoryStatic,
						Direction:    "",
						FromID:       "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩",
						ToID:         "system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩",
						RawPeriods:   []*VisiblePeriod{{Start: 100000, End: 500000}},
					},
					"host_with_system:1": {
						RelationID:   "host_with_system:1",
						RelationType: RelationHostWithSystem,
						Category:     RelationCategoryStatic,
						Direction:    "",
						FromID:       "system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩",
						ToID:         "host:⟨bk_host_id=12345⟩",
						RawPeriods:   []*VisiblePeriod{{Start: 100000, End: 500000}},
					},
				},
				Adjacency: map[string][]string{
					"node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩": {"node_with_system:1"},
					"system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩": {"host_with_system:1"},
					"host:⟨bk_host_id=12345⟩":                         {},
				},
			},
		},
	}
}

// 测试用例 5: 多个起始实体（返回多个图）
func getMultipleRootsTestCase() E2ETestCase {
	return E2ETestCase{
		Name: "MultipleRoots_TwoNodes",
		QueryRequest: &QueryRequest{
			Timestamp:            600000,
			SourceType:           ResourceTypeNode,
			SourceInfo:           map[string]string{"bcs_cluster_id": "BCS-K8S-00001"}, // 只指定 cluster，可能匹配多个 node
			MaxHops:              1,
			AllowedRelationTypes: []RelationCategory{RelationCategoryStatic},
			LookBackDelta:        600000,
			Limit:                10,
		},
		ExpectedSQL: "", // 跳过 SQL 验证
		MockResponse: []map[string]any{
			{
				"result": []any{
					// 第一个起始实体: node-1
					map[string]any{
						"result": map[string]any{
							"root": map[string]any{
								"entity_type": "node",
								"entity_id":   "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩",
								"entity_data": map[string]any{
									"bcs_cluster_id": "BCS-K8S-00001",
									"node":           "node-1",
								},
								"liveness": []any{
									map[string]any{"period_start": float64(100000), "period_end": float64(500000)},
								},
							},
							"hop1": map[string]any{
								"node_with_system": []any{
									map[string]any{
										"hop":               float64(1),
										"relation_type":     "node_with_system",
										"relation_category": "static",
										"relation_id":       "node_with_system:1",
										"relation_liveness": []any{
											map[string]any{"period_start": float64(100000), "period_end": float64(500000)},
										},
										"target": map[string]any{
											"entity_type": "system",
											"entity_id":   "system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩",
											"entity_data": map[string]any{
												"bk_cloud_id":  "0",
												"bk_target_ip": "192.168.1.1",
											},
											"liveness": []any{
												map[string]any{"period_start": float64(100000), "period_end": float64(500000)},
											},
										},
									},
								},
							},
						},
					},
					// 第二个起始实体: node-2
					map[string]any{
						"result": map[string]any{
							"root": map[string]any{
								"entity_type": "node",
								"entity_id":   "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-2⟩",
								"entity_data": map[string]any{
									"bcs_cluster_id": "BCS-K8S-00001",
									"node":           "node-2",
								},
								"liveness": []any{
									map[string]any{"period_start": float64(200000), "period_end": float64(600000)},
								},
							},
							"hop1": map[string]any{
								"node_with_system": []any{
									map[string]any{
										"hop":               float64(1),
										"relation_type":     "node_with_system",
										"relation_category": "static",
										"relation_id":       "node_with_system:2",
										"relation_liveness": []any{
											map[string]any{"period_start": float64(200000), "period_end": float64(600000)},
										},
										"target": map[string]any{
											"entity_type": "system",
											"entity_id":   "system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.2⟩",
											"entity_data": map[string]any{
												"bk_cloud_id":  "0",
												"bk_target_ip": "192.168.1.2",
											},
											"liveness": []any{
												map[string]any{"period_start": float64(200000), "period_end": float64(600000)},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		ExpectedGraphs: []*LivenessGraph{
			// 第一个图: node-1 -> system-1
			{
				QueryStart: 0,
				QueryEnd:   600000,
				Nodes: map[string]*NodeLiveness{
					"node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩": {
						ResourceID:   "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩",
						ResourceType: ResourceTypeNode,
						Labels: map[string]string{
							"bcs_cluster_id": "BCS-K8S-00001",
							"node":           "node-1",
						},
						RawPeriods: []*VisiblePeriod{{Start: 100000, End: 500000}},
					},
					"system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩": {
						ResourceID:   "system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩",
						ResourceType: ResourceTypeSystem,
						Labels: map[string]string{
							"bk_cloud_id":  "0",
							"bk_target_ip": "192.168.1.1",
						},
						RawPeriods: []*VisiblePeriod{{Start: 100000, End: 500000}},
					},
				},
				Edges: map[string]*EdgeLiveness{
					"node_with_system:1": {
						RelationID:   "node_with_system:1",
						RelationType: RelationNodeWithSystem,
						Category:     RelationCategoryStatic,
						Direction:    "",
						FromID:       "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩",
						ToID:         "system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩",
						RawPeriods:   []*VisiblePeriod{{Start: 100000, End: 500000}},
					},
				},
				Adjacency: map[string][]string{
					"node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩": {"node_with_system:1"},
					"system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.1⟩": {},
				},
			},
			// 第二个图: node-2 -> system-2
			{
				QueryStart: 0,
				QueryEnd:   600000,
				Nodes: map[string]*NodeLiveness{
					"node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-2⟩": {
						ResourceID:   "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-2⟩",
						ResourceType: ResourceTypeNode,
						Labels: map[string]string{
							"bcs_cluster_id": "BCS-K8S-00001",
							"node":           "node-2",
						},
						RawPeriods: []*VisiblePeriod{{Start: 200000, End: 600000}},
					},
					"system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.2⟩": {
						ResourceID:   "system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.2⟩",
						ResourceType: ResourceTypeSystem,
						Labels: map[string]string{
							"bk_cloud_id":  "0",
							"bk_target_ip": "192.168.1.2",
						},
						RawPeriods: []*VisiblePeriod{{Start: 200000, End: 600000}},
					},
				},
				Edges: map[string]*EdgeLiveness{
					"node_with_system:2": {
						RelationID:   "node_with_system:2",
						RelationType: RelationNodeWithSystem,
						Category:     RelationCategoryStatic,
						Direction:    "",
						FromID:       "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-2⟩",
						ToID:         "system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.2⟩",
						RawPeriods:   []*VisiblePeriod{{Start: 200000, End: 600000}},
					},
				},
				Adjacency: map[string][]string{
					"node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-2⟩": {"node_with_system:2"},
					"system:⟨bk_cloud_id=0,bk_target_ip=192.168.1.2⟩": {},
				},
			},
		},
	}
}

func TestGraphE2E(t *testing.T) {
	testCases := []E2ETestCase{
		getNodeStaticTestCase(),
		getSystemDynamicOutboundTestCase(),
		getEmptyResponseTestCase(),
		getMultiHopNodeToSystemTestCase(),
		getMultipleRootsTestCase(),
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			// Step 1: 构建查询
			builder := NewSurrealQueryBuilder(tc.QueryRequest)
			actualSQL := builder.Build()

			// Step 2: 验证 SQL（如果 ExpectedSQL 非空）
			if tc.ExpectedSQL != "" {
				assert.Equal(t, tc.ExpectedSQL, actualSQL, "Generated SQL mismatch")
			} else {
				t.Logf("Generated SQL (length=%d):\n%s", len(actualSQL), actualSQL)
			}

			// Step 3: 解析响应
			start, end := tc.QueryRequest.GetQueryRange()
			parser := NewSurrealResponseParser(start, end)
			actualGraphs, err := parser.Parse(tc.MockResponse)
			require.NoError(t, err, "Parse should not return error")

			// Step 4: 验证图的数量
			require.Equal(t, len(tc.ExpectedGraphs), len(actualGraphs), "Number of graphs mismatch")

			// Step 5: 对每个图进行比较（排序 Adjacency 以忽略顺序差异）
			for i, expectedGraph := range tc.ExpectedGraphs {
				assertLivenessGraphEqual(t, expectedGraph, actualGraphs[i], "LivenessGraph[%d] mismatch", i)
			}
		})
	}
}

// assertLivenessGraphEqual 比较两个 LivenessGraph，忽略 Adjacency 中的顺序差异
func assertLivenessGraphEqual(t *testing.T, expected, actual *LivenessGraph, msgAndArgs ...any) {
	t.Helper()

	// 对 Adjacency 排序后比较
	sortedExpected := sortAdjacency(expected)
	sortedActual := sortAdjacency(actual)

	expectedJSON, err := json.Marshal(sortedExpected)
	require.NoError(t, err, "Marshal expected graph failed")

	actualJSON, err := json.Marshal(sortedActual)
	require.NoError(t, err, "Marshal actual graph failed")

	assert.JSONEq(t, string(expectedJSON), string(actualJSON), msgAndArgs...)
}

// sortAdjacency 返回一个新的 LivenessGraph，其 Adjacency 中的 slice 已排序
func sortAdjacency(g *LivenessGraph) *LivenessGraph {
	sorted := &LivenessGraph{
		QueryStart:      g.QueryStart,
		QueryEnd:        g.QueryEnd,
		Nodes:           g.Nodes,
		Edges:           g.Edges,
		TraversalErrors: g.TraversalErrors,
		Adjacency:       make(map[string][]string, len(g.Adjacency)),
	}
	for k, v := range g.Adjacency {
		sortedSlice := make([]string, len(v))
		copy(sortedSlice, v)
		sort.Strings(sortedSlice)
		sorted.Adjacency[k] = sortedSlice
	}
	return sorted
}
