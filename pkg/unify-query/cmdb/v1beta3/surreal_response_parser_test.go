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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSurrealResponseParser_parseHopRelations(t *testing.T) {
	parser := NewSurrealResponseParser(1000, 2000)

	t.Run("empty hop data", func(t *testing.T) {
		graph := NewLivenessGraph(1000, 2000)
		rootNode := &NodeLiveness{
			ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ResourceType: ResourceTypePod,
			Labels:       map[string]string{"cluster": "c1", "namespace": "ns1", "pod": "p1"},
		}
		graph.AddNode(rootNode)

		hopData := map[string]any{}
		parser.parseHopRelations(graph, rootNode.ResourceID, hopData)

		assert.Equal(t, &LivenessGraph{
			QueryStart: 1000,
			QueryEnd:   2000,
			Nodes: map[string]*NodeLiveness{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {
					ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ResourceType: ResourceTypePod,
					Labels:       map[string]string{"cluster": "c1", "namespace": "ns1", "pod": "p1"},
				},
			},
			Edges: map[string]*EdgeLiveness{},
			Adjacency: map[string][]string{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {},
			},
		}, graph)
	})

	t.Run("single relation in hop", func(t *testing.T) {
		graph := NewLivenessGraph(1000, 2000)
		rootNode := &NodeLiveness{
			ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ResourceType: ResourceTypePod,
			Labels:       map[string]string{"cluster": "c1", "namespace": "ns1", "pod": "p1"},
		}
		graph.AddNode(rootNode)

		hopData := map[string]any{
			"pod_with_service": []any{
				map[string]any{
					"relation_id":       "rel_1",
					"relation_type":     "pod_with_service",
					"relation_category": "static",
					"direction":         "outbound",
					"relation_liveness": []any{
						map[string]any{"period_start": float64(1000), "period_end": float64(2000)},
					},
					"target": map[string]any{
						"entity_id":   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
						"entity_type": "service",
						"entity_data": map[string]any{
							"cluster":   "c1",
							"namespace": "ns1",
							"service":   "svc1",
						},
						"liveness": []any{
							map[string]any{"period_start": float64(1000), "period_end": float64(2000)},
						},
					},
				},
			},
		}

		parser.parseHopRelations(graph, rootNode.ResourceID, hopData)

		assert.Equal(t, &LivenessGraph{
			QueryStart: 1000,
			QueryEnd:   2000,
			Nodes: map[string]*NodeLiveness{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {
					ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ResourceType: ResourceTypePod,
					Labels:       map[string]string{"cluster": "c1", "namespace": "ns1", "pod": "p1"},
				},
				"service:⟨cluster=c1,namespace=ns1,service=svc1⟩": {
					ResourceID:   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
					ResourceType: ResourceTypeService,
					Labels:       map[string]string{"cluster": "c1", "namespace": "ns1", "service": "svc1"},
					RawPeriods:   []*VisiblePeriod{{Start: 1000, End: 2000}},
				},
			},
			Edges: map[string]*EdgeLiveness{
				"rel_1": {
					RelationID:   "rel_1",
					RelationType: RelationType("pod_with_service"),
					Category:     RelationCategoryStatic,
					Direction:    DirectionOutbound,
					FromID:       "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ToID:         "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
					RawPeriods:   []*VisiblePeriod{{Start: 1000, End: 2000}},
				},
			},
			Adjacency: map[string][]string{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩":           {"rel_1"},
				"service:⟨cluster=c1,namespace=ns1,service=svc1⟩": {},
			},
		}, graph)
	})

	t.Run("multiple relations in single hop", func(t *testing.T) {
		graph := NewLivenessGraph(1000, 2000)
		rootNode := &NodeLiveness{
			ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ResourceType: ResourceTypePod,
			Labels:       map[string]string{"cluster": "c1", "namespace": "ns1", "pod": "p1"},
		}
		graph.AddNode(rootNode)

		hopData := map[string]any{
			"pod_with_service": []any{
				map[string]any{
					"relation_id":       "rel_1",
					"relation_type":     "pod_with_service",
					"relation_category": "static",
					"direction":         "outbound",
					"relation_liveness": []any{},
					"target": map[string]any{
						"entity_id":   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
						"entity_type": "service",
						"entity_data": map[string]any{},
						"liveness":    []any{},
					},
				},
				map[string]any{
					"relation_id":       "rel_2",
					"relation_type":     "pod_with_service",
					"relation_category": "static",
					"direction":         "outbound",
					"relation_liveness": []any{},
					"target": map[string]any{
						"entity_id":   "service:⟨cluster=c1,namespace=ns1,service=svc2⟩",
						"entity_type": "service",
						"entity_data": map[string]any{},
						"liveness":    []any{},
					},
				},
			},
		}

		parser.parseHopRelations(graph, rootNode.ResourceID, hopData)

		assert.Equal(t, &LivenessGraph{
			QueryStart: 1000,
			QueryEnd:   2000,
			Nodes: map[string]*NodeLiveness{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {
					ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ResourceType: ResourceTypePod,
					Labels:       map[string]string{"cluster": "c1", "namespace": "ns1", "pod": "p1"},
				},
				"service:⟨cluster=c1,namespace=ns1,service=svc1⟩": {
					ResourceID:   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
					ResourceType: ResourceTypeService,
					Labels:       map[string]string{},
				},
				"service:⟨cluster=c1,namespace=ns1,service=svc2⟩": {
					ResourceID:   "service:⟨cluster=c1,namespace=ns1,service=svc2⟩",
					ResourceType: ResourceTypeService,
					Labels:       map[string]string{},
				},
			},
			Edges: map[string]*EdgeLiveness{
				"rel_1": {
					RelationID:   "rel_1",
					RelationType: RelationType("pod_with_service"),
					Category:     RelationCategoryStatic,
					Direction:    DirectionOutbound,
					FromID:       "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ToID:         "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
				},
				"rel_2": {
					RelationID:   "rel_2",
					RelationType: RelationType("pod_with_service"),
					Category:     RelationCategoryStatic,
					Direction:    DirectionOutbound,
					FromID:       "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ToID:         "service:⟨cluster=c1,namespace=ns1,service=svc2⟩",
				},
			},
			Adjacency: map[string][]string{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩":           {"rel_1", "rel_2"},
				"service:⟨cluster=c1,namespace=ns1,service=svc1⟩": {},
				"service:⟨cluster=c1,namespace=ns1,service=svc2⟩": {},
			},
		}, graph)
	})

	t.Run("multiple relation types in single hop", func(t *testing.T) {
		graph := NewLivenessGraph(1000, 2000)
		rootNode := &NodeLiveness{
			ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ResourceType: ResourceTypePod,
			Labels:       map[string]string{"cluster": "c1", "namespace": "ns1", "pod": "p1"},
		}
		graph.AddNode(rootNode)

		hopData := map[string]any{
			"pod_with_service": []any{
				map[string]any{
					"relation_id":       "rel_1",
					"relation_type":     "pod_with_service",
					"relation_category": "static",
					"direction":         "outbound",
					"relation_liveness": []any{},
					"target": map[string]any{
						"entity_id":   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
						"entity_type": "service",
						"entity_data": map[string]any{},
						"liveness":    []any{},
					},
				},
			},
			"pod_with_replicaset": []any{
				map[string]any{
					"relation_id":       "rel_2",
					"relation_type":     "pod_with_replicaset",
					"relation_category": "static",
					"direction":         "outbound",
					"relation_liveness": []any{},
					"target": map[string]any{
						"entity_id":   "replicaset:⟨cluster=c1,namespace=ns1,replicaset=rs1⟩",
						"entity_type": "replicaset",
						"entity_data": map[string]any{},
						"liveness":    []any{},
					},
				},
			},
		}

		parser.parseHopRelations(graph, rootNode.ResourceID, hopData)

		assert.Equal(t, 3, len(graph.Nodes))
		assert.Equal(t, 2, len(graph.Edges))
		assert.NotNil(t, graph.GetNode("pod:⟨cluster=c1,namespace=ns1,pod=p1⟩"))
		assert.NotNil(t, graph.GetNode("service:⟨cluster=c1,namespace=ns1,service=svc1⟩"))
		assert.NotNil(t, graph.GetNode("replicaset:⟨cluster=c1,namespace=ns1,replicaset=rs1⟩"))
		assert.NotNil(t, graph.GetEdge("rel_1"))
		assert.NotNil(t, graph.GetEdge("rel_2"))
		assert.False(t, graph.HasErrors())
	})

	t.Run("nested hop relations (hop2 inside target)", func(t *testing.T) {
		graph := NewLivenessGraph(1000, 2000)
		rootNode := &NodeLiveness{
			ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ResourceType: ResourceTypePod,
			Labels:       map[string]string{"cluster": "c1", "namespace": "ns1", "pod": "p1"},
		}
		graph.AddNode(rootNode)

		hopData := map[string]any{
			"pod_with_replicaset": []any{
				map[string]any{
					"relation_id":       "rel_1",
					"relation_type":     "pod_with_replicaset",
					"relation_category": "static",
					"direction":         "outbound",
					"relation_liveness": []any{},
					"target": map[string]any{
						"entity_id":   "replicaset:⟨cluster=c1,namespace=ns1,replicaset=rs1⟩",
						"entity_type": "replicaset",
						"entity_data": map[string]any{},
						"liveness":    []any{},
						// Nested hop2: replicaset -> deployment
						"hop2": map[string]any{
							"deployment_with_replicaset": []any{
								map[string]any{
									"relation_id":       "rel_2",
									"relation_type":     "deployment_with_replicaset",
									"relation_category": "static",
									"direction":         "inbound",
									"relation_liveness": []any{},
									"target": map[string]any{
										"entity_id":   "deployment:⟨cluster=c1,namespace=ns1,deployment=dep1⟩",
										"entity_type": "deployment",
										"entity_data": map[string]any{},
										"liveness":    []any{},
									},
								},
							},
						},
					},
				},
			},
		}

		parser.parseHopRelations(graph, rootNode.ResourceID, hopData)

		assert.Equal(t, &LivenessGraph{
			QueryStart: 1000,
			QueryEnd:   2000,
			Nodes: map[string]*NodeLiveness{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {
					ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ResourceType: ResourceTypePod,
					Labels:       map[string]string{"cluster": "c1", "namespace": "ns1", "pod": "p1"},
				},
				"replicaset:⟨cluster=c1,namespace=ns1,replicaset=rs1⟩": {
					ResourceID:   "replicaset:⟨cluster=c1,namespace=ns1,replicaset=rs1⟩",
					ResourceType: ResourceTypeReplicaSet,
					Labels:       map[string]string{},
				},
				"deployment:⟨cluster=c1,namespace=ns1,deployment=dep1⟩": {
					ResourceID:   "deployment:⟨cluster=c1,namespace=ns1,deployment=dep1⟩",
					ResourceType: ResourceTypeDeployment,
					Labels:       map[string]string{},
				},
			},
			Edges: map[string]*EdgeLiveness{
				"rel_1": {
					RelationID:   "rel_1",
					RelationType: RelationType("pod_with_replicaset"),
					Category:     RelationCategoryStatic,
					Direction:    DirectionOutbound,
					FromID:       "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ToID:         "replicaset:⟨cluster=c1,namespace=ns1,replicaset=rs1⟩",
				},
				"rel_2": {
					RelationID:   "rel_2",
					RelationType: RelationType("deployment_with_replicaset"),
					Category:     RelationCategoryStatic,
					Direction:    DirectionInbound,
					FromID:       "replicaset:⟨cluster=c1,namespace=ns1,replicaset=rs1⟩",
					ToID:         "deployment:⟨cluster=c1,namespace=ns1,deployment=dep1⟩",
				},
			},
			Adjacency: map[string][]string{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩":                 {"rel_1"},
				"replicaset:⟨cluster=c1,namespace=ns1,replicaset=rs1⟩":  {"rel_2"},
				"deployment:⟨cluster=c1,namespace=ns1,deployment=dep1⟩": {},
			},
		}, graph)
	})

	t.Run("deeply nested hops (hop3 inside hop2)", func(t *testing.T) {
		graph := NewLivenessGraph(1000, 2000)
		rootNode := &NodeLiveness{
			ResourceID:   "container:⟨cluster=c1,namespace=ns1,pod=p1,container=c1⟩",
			ResourceType: ResourceTypeContainer,
		}
		graph.AddNode(rootNode)

		hopData := map[string]any{
			"container_with_pod": []any{
				map[string]any{
					"relation_id":       "rel_1",
					"relation_type":     "container_with_pod",
					"relation_category": "static",
					"direction":         "outbound",
					"relation_liveness": []any{},
					"target": map[string]any{
						"entity_id":   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
						"entity_type": "pod",
						"entity_data": map[string]any{},
						"liveness":    []any{},
						// hop2: pod -> node
						"hop2": map[string]any{
							"node_with_pod": []any{
								map[string]any{
									"relation_id":       "rel_2",
									"relation_type":     "node_with_pod",
									"relation_category": "static",
									"direction":         "inbound",
									"relation_liveness": []any{},
									"target": map[string]any{
										"entity_id":   "node:⟨cluster=c1,node=node1⟩",
										"entity_type": "node",
										"entity_data": map[string]any{},
										"liveness":    []any{},
										// hop3: node -> system
										"hop3": map[string]any{
											"node_with_system": []any{
												map[string]any{
													"relation_id":       "rel_3",
													"relation_type":     "node_with_system",
													"relation_category": "static",
													"direction":         "outbound",
													"relation_liveness": []any{},
													"target": map[string]any{
														"entity_id":   "system:⟨bk_cloud_id=0,bk_target_ip=10.0.0.1⟩",
														"entity_type": "system",
														"entity_data": map[string]any{},
														"liveness":    []any{},
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
		}

		parser.parseHopRelations(graph, rootNode.ResourceID, hopData)

		assert.Equal(t, &LivenessGraph{
			QueryStart: 1000,
			QueryEnd:   2000,
			Nodes: map[string]*NodeLiveness{
				"container:⟨cluster=c1,namespace=ns1,pod=p1,container=c1⟩": {
					ResourceID:   "container:⟨cluster=c1,namespace=ns1,pod=p1,container=c1⟩",
					ResourceType: ResourceTypeContainer,
				},
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {
					ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ResourceType: ResourceTypePod,
					Labels:       map[string]string{},
				},
				"node:⟨cluster=c1,node=node1⟩": {
					ResourceID:   "node:⟨cluster=c1,node=node1⟩",
					ResourceType: ResourceTypeNode,
					Labels:       map[string]string{},
				},
				"system:⟨bk_cloud_id=0,bk_target_ip=10.0.0.1⟩": {
					ResourceID:   "system:⟨bk_cloud_id=0,bk_target_ip=10.0.0.1⟩",
					ResourceType: ResourceTypeSystem,
					Labels:       map[string]string{},
				},
			},
			Edges: map[string]*EdgeLiveness{
				"rel_1": {
					RelationID:   "rel_1",
					RelationType: RelationType("container_with_pod"),
					Category:     RelationCategoryStatic,
					Direction:    DirectionOutbound,
					FromID:       "container:⟨cluster=c1,namespace=ns1,pod=p1,container=c1⟩",
					ToID:         "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
				},
				"rel_2": {
					RelationID:   "rel_2",
					RelationType: RelationType("node_with_pod"),
					Category:     RelationCategoryStatic,
					Direction:    DirectionInbound,
					FromID:       "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ToID:         "node:⟨cluster=c1,node=node1⟩",
				},
				"rel_3": {
					RelationID:   "rel_3",
					RelationType: RelationType("node_with_system"),
					Category:     RelationCategoryStatic,
					Direction:    DirectionOutbound,
					FromID:       "node:⟨cluster=c1,node=node1⟩",
					ToID:         "system:⟨bk_cloud_id=0,bk_target_ip=10.0.0.1⟩",
				},
			},
			Adjacency: map[string][]string{
				"container:⟨cluster=c1,namespace=ns1,pod=p1,container=c1⟩": {"rel_1"},
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩":                    {"rel_2"},
				"node:⟨cluster=c1,node=node1⟩":                             {"rel_3"},
				"system:⟨bk_cloud_id=0,bk_target_ip=10.0.0.1⟩":             {},
			},
		}, graph)
	})

	t.Run("invalid relation data type (not []any)", func(t *testing.T) {
		graph := NewLivenessGraph(1000, 2000)
		rootNode := &NodeLiveness{
			ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ResourceType: ResourceTypePod,
		}
		graph.AddNode(rootNode)

		hopData := map[string]any{
			"pod_with_service": "invalid_string", // Should be []any
		}

		parser.parseHopRelations(graph, rootNode.ResourceID, hopData)

		assert.Equal(t, &LivenessGraph{
			QueryStart: 1000,
			QueryEnd:   2000,
			Nodes: map[string]*NodeLiveness{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {
					ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ResourceType: ResourceTypePod,
				},
			},
			Edges: map[string]*EdgeLiveness{},
			Adjacency: map[string][]string{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {},
			},
		}, graph)
	})

	t.Run("invalid relation item type (not map[string]any)", func(t *testing.T) {
		graph := NewLivenessGraph(1000, 2000)
		rootNode := &NodeLiveness{
			ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ResourceType: ResourceTypePod,
		}
		graph.AddNode(rootNode)

		hopData := map[string]any{
			"pod_with_service": []any{
				"invalid_string", // Should be map[string]any
				123,              // Invalid type
			},
		}

		parser.parseHopRelations(graph, rootNode.ResourceID, hopData)

		assert.Equal(t, &LivenessGraph{
			QueryStart: 1000,
			QueryEnd:   2000,
			Nodes: map[string]*NodeLiveness{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {
					ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ResourceType: ResourceTypePod,
				},
			},
			Edges: map[string]*EdgeLiveness{},
			Adjacency: map[string][]string{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {},
			},
		}, graph)
	})

	t.Run("missing relation_id adds error", func(t *testing.T) {
		graph := NewLivenessGraph(1000, 2000)
		rootNode := &NodeLiveness{
			ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ResourceType: ResourceTypePod,
		}
		graph.AddNode(rootNode)

		hopData := map[string]any{
			"pod_with_service": []any{
				map[string]any{
					// Missing relation_id
					"relation_type":     "pod_with_service",
					"relation_category": "static",
					"target": map[string]any{
						"entity_id":   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
						"entity_type": "service",
						"entity_data": map[string]any{},
						"liveness":    []any{},
					},
				},
			},
		}

		parser.parseHopRelations(graph, rootNode.ResourceID, hopData)

		assert.Equal(t, &LivenessGraph{
			QueryStart: 1000,
			QueryEnd:   2000,
			Nodes: map[string]*NodeLiveness{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {
					ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ResourceType: ResourceTypePod,
				},
			},
			Edges: map[string]*EdgeLiveness{},
			Adjacency: map[string][]string{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {},
			},
			TraversalErrors: []string{"parse relation pod_with_service: missing relation_id"},
		}, graph)
	})

	t.Run("missing target adds error", func(t *testing.T) {
		graph := NewLivenessGraph(1000, 2000)
		rootNode := &NodeLiveness{
			ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ResourceType: ResourceTypePod,
		}
		graph.AddNode(rootNode)

		hopData := map[string]any{
			"pod_with_service": []any{
				map[string]any{
					"relation_id":       "rel_1",
					"relation_type":     "pod_with_service",
					"relation_category": "static",
					// Missing target
				},
			},
		}

		parser.parseHopRelations(graph, rootNode.ResourceID, hopData)

		assert.Equal(t, &LivenessGraph{
			QueryStart: 1000,
			QueryEnd:   2000,
			Nodes: map[string]*NodeLiveness{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {
					ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ResourceType: ResourceTypePod,
				},
			},
			Edges: map[string]*EdgeLiveness{},
			Adjacency: map[string][]string{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {},
			},
			TraversalErrors: []string{"parse relation pod_with_service: missing target"},
		}, graph)
	})

	t.Run("missing entity_id in target adds error", func(t *testing.T) {
		graph := NewLivenessGraph(1000, 2000)
		rootNode := &NodeLiveness{
			ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ResourceType: ResourceTypePod,
		}
		graph.AddNode(rootNode)

		hopData := map[string]any{
			"pod_with_service": []any{
				map[string]any{
					"relation_id":       "rel_1",
					"relation_type":     "pod_with_service",
					"relation_category": "static",
					"target": map[string]any{
						// Missing entity_id
						"entity_type": "service",
						"entity_data": map[string]any{},
						"liveness":    []any{},
					},
				},
			},
		}

		parser.parseHopRelations(graph, rootNode.ResourceID, hopData)

		assert.Equal(t, &LivenessGraph{
			QueryStart: 1000,
			QueryEnd:   2000,
			Nodes: map[string]*NodeLiveness{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {
					ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ResourceType: ResourceTypePod,
				},
			},
			Edges: map[string]*EdgeLiveness{},
			Adjacency: map[string][]string{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {},
			},
			TraversalErrors: []string{"parse relation pod_with_service: parse target: missing entity_id"},
		}, graph)
	})

	t.Run("duplicate target node not added twice", func(t *testing.T) {
		graph := NewLivenessGraph(1000, 2000)
		rootNode := &NodeLiveness{
			ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ResourceType: ResourceTypePod,
		}
		graph.AddNode(rootNode)

		// Pre-add the target node
		existingTarget := &NodeLiveness{
			ResourceID:   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
			ResourceType: ResourceTypeService,
			Labels:       map[string]string{"existing": "true"},
		}
		graph.AddNode(existingTarget)

		hopData := map[string]any{
			"pod_with_service": []any{
				map[string]any{
					"relation_id":       "rel_1",
					"relation_type":     "pod_with_service",
					"relation_category": "static",
					"direction":         "outbound",
					"relation_liveness": []any{},
					"target": map[string]any{
						"entity_id":   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
						"entity_type": "service",
						"entity_data": map[string]any{"new": "label"},
						"liveness":    []any{},
					},
				},
			},
		}

		parser.parseHopRelations(graph, rootNode.ResourceID, hopData)

		assert.Equal(t, &LivenessGraph{
			QueryStart: 1000,
			QueryEnd:   2000,
			Nodes: map[string]*NodeLiveness{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {
					ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ResourceType: ResourceTypePod,
				},
				"service:⟨cluster=c1,namespace=ns1,service=svc1⟩": {
					ResourceID:   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
					ResourceType: ResourceTypeService,
					Labels:       map[string]string{"existing": "true"},
				},
			},
			Edges: map[string]*EdgeLiveness{
				"rel_1": {
					RelationID:   "rel_1",
					RelationType: RelationType("pod_with_service"),
					Category:     RelationCategoryStatic,
					Direction:    DirectionOutbound,
					FromID:       "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ToID:         "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
				},
			},
			Adjacency: map[string][]string{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩":           {"rel_1"},
				"service:⟨cluster=c1,namespace=ns1,service=svc1⟩": {},
			},
		}, graph)
	})

	t.Run("branching hops - multiple nested relations", func(t *testing.T) {
		graph := NewLivenessGraph(1000, 2000)
		rootNode := &NodeLiveness{
			ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ResourceType: ResourceTypePod,
		}
		graph.AddNode(rootNode)

		hopData := map[string]any{
			"pod_with_service": []any{
				map[string]any{
					"relation_id":       "rel_1",
					"relation_type":     "pod_with_service",
					"relation_category": "static",
					"direction":         "outbound",
					"relation_liveness": []any{},
					"target": map[string]any{
						"entity_id":   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
						"entity_type": "service",
						"entity_data": map[string]any{},
						"liveness":    []any{},
						// hop2 with multiple relation types
						"hop2": map[string]any{
							"ingress_with_service": []any{
								map[string]any{
									"relation_id":       "rel_2",
									"relation_type":     "ingress_with_service",
									"relation_category": "static",
									"direction":         "inbound",
									"relation_liveness": []any{},
									"target": map[string]any{
										"entity_id":   "ingress:⟨cluster=c1,namespace=ns1,ingress=ing1⟩",
										"entity_type": "ingress",
										"entity_data": map[string]any{},
										"liveness":    []any{},
									},
								},
								map[string]any{
									"relation_id":       "rel_3",
									"relation_type":     "ingress_with_service",
									"relation_category": "static",
									"direction":         "inbound",
									"relation_liveness": []any{},
									"target": map[string]any{
										"entity_id":   "ingress:⟨cluster=c1,namespace=ns1,ingress=ing2⟩",
										"entity_type": "ingress",
										"entity_data": map[string]any{},
										"liveness":    []any{},
									},
								},
							},
							"k8s_address_with_service": []any{
								map[string]any{
									"relation_id":       "rel_4",
									"relation_type":     "k8s_address_with_service",
									"relation_category": "static",
									"direction":         "inbound",
									"relation_liveness": []any{},
									"target": map[string]any{
										"entity_id":   "k8s_address:⟨address=10.0.0.1:8080⟩",
										"entity_type": "k8s_address",
										"entity_data": map[string]any{},
										"liveness":    []any{},
									},
								},
							},
						},
					},
				},
			},
		}

		parser.parseHopRelations(graph, rootNode.ResourceID, hopData)

		assert.Equal(t, 5, len(graph.Nodes))
		assert.Equal(t, 4, len(graph.Edges))
		assert.NotNil(t, graph.GetNode("pod:⟨cluster=c1,namespace=ns1,pod=p1⟩"))
		assert.NotNil(t, graph.GetNode("service:⟨cluster=c1,namespace=ns1,service=svc1⟩"))
		assert.NotNil(t, graph.GetNode("ingress:⟨cluster=c1,namespace=ns1,ingress=ing1⟩"))
		assert.NotNil(t, graph.GetNode("ingress:⟨cluster=c1,namespace=ns1,ingress=ing2⟩"))
		assert.NotNil(t, graph.GetNode("k8s_address:⟨address=10.0.0.1:8080⟩"))
		assert.NotNil(t, graph.GetEdge("rel_1"))
		assert.NotNil(t, graph.GetEdge("rel_2"))
		assert.NotNil(t, graph.GetEdge("rel_3"))
		assert.NotNil(t, graph.GetEdge("rel_4"))
		assert.False(t, graph.HasErrors())
	})

	t.Run("liveness periods parsed correctly", func(t *testing.T) {
		graph := NewLivenessGraph(1000, 2000)
		rootNode := &NodeLiveness{
			ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ResourceType: ResourceTypePod,
		}
		graph.AddNode(rootNode)

		hopData := map[string]any{
			"pod_with_service": []any{
				map[string]any{
					"relation_id":       "rel_1",
					"relation_type":     "pod_with_service",
					"relation_category": "static",
					"direction":         "outbound",
					"relation_liveness": []any{
						map[string]any{"period_start": float64(1100), "period_end": float64(1500)},
						map[string]any{"period_start": float64(1600), "period_end": float64(1900)},
					},
					"target": map[string]any{
						"entity_id":   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
						"entity_type": "service",
						"entity_data": map[string]any{},
						"liveness": []any{
							map[string]any{"period_start": float64(1000), "period_end": float64(2000)},
						},
					},
				},
			},
		}

		parser.parseHopRelations(graph, rootNode.ResourceID, hopData)

		assert.Equal(t, &LivenessGraph{
			QueryStart: 1000,
			QueryEnd:   2000,
			Nodes: map[string]*NodeLiveness{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {
					ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ResourceType: ResourceTypePod,
				},
				"service:⟨cluster=c1,namespace=ns1,service=svc1⟩": {
					ResourceID:   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
					ResourceType: ResourceTypeService,
					Labels:       map[string]string{},
					RawPeriods:   []*VisiblePeriod{{Start: 1000, End: 2000}},
				},
			},
			Edges: map[string]*EdgeLiveness{
				"rel_1": {
					RelationID:   "rel_1",
					RelationType: RelationType("pod_with_service"),
					Category:     RelationCategoryStatic,
					Direction:    DirectionOutbound,
					FromID:       "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ToID:         "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
					RawPeriods: []*VisiblePeriod{
						{Start: 1100, End: 1500},
						{Start: 1600, End: 1900},
					},
				},
			},
			Adjacency: map[string][]string{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩":           {"rel_1"},
				"service:⟨cluster=c1,namespace=ns1,service=svc1⟩": {},
			},
		}, graph)
	})
}

func TestSurrealResponseParser_Parse(t *testing.T) {
	t.Run("empty response", func(t *testing.T) {
		parser := NewSurrealResponseParser(1000, 2000)
		graphs, err := parser.Parse(nil)
		require.NoError(t, err)
		assert.Equal(t, ([]*LivenessGraph)(nil), graphs)
	})

	t.Run("empty array response", func(t *testing.T) {
		parser := NewSurrealResponseParser(1000, 2000)
		graphs, err := parser.Parse([]map[string]any{})
		require.NoError(t, err)
		assert.Equal(t, ([]*LivenessGraph)(nil), graphs)
	})

	t.Run("missing result field", func(t *testing.T) {
		parser := NewSurrealResponseParser(1000, 2000)
		graphs, err := parser.Parse([]map[string]any{
			{"other_field": "value"},
		})
		require.NoError(t, err)
		assert.Equal(t, ([]*LivenessGraph)(nil), graphs)
	})

	t.Run("result is not array", func(t *testing.T) {
		parser := NewSurrealResponseParser(1000, 2000)
		graphs, err := parser.Parse([]map[string]any{
			{"result": "not_an_array"},
		})
		require.NoError(t, err)
		assert.Equal(t, ([]*LivenessGraph)(nil), graphs)
	})

	t.Run("single record with root only", func(t *testing.T) {
		parser := NewSurrealResponseParser(1000, 2000)
		response := []map[string]any{
			{
				"result": []any{
					map[string]any{
						"result": map[string]any{
							"root": map[string]any{
								"entity_id":   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
								"entity_type": "pod",
								"entity_data": map[string]any{
									"cluster":   "c1",
									"namespace": "ns1",
									"pod":       "p1",
								},
								"liveness": []any{
									map[string]any{"period_start": float64(1000), "period_end": float64(2000)},
								},
							},
						},
					},
				},
			},
		}

		graphs, err := parser.Parse(response)
		require.NoError(t, err)
		require.Equal(t, 1, len(graphs))

		assert.Equal(t, &LivenessGraph{
			QueryStart: 1000,
			QueryEnd:   2000,
			Nodes: map[string]*NodeLiveness{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {
					ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ResourceType: ResourceTypePod,
					Labels:       map[string]string{"cluster": "c1", "namespace": "ns1", "pod": "p1"},
					RawPeriods:   []*VisiblePeriod{{Start: 1000, End: 2000}},
				},
			},
			Edges: map[string]*EdgeLiveness{},
			Adjacency: map[string][]string{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {},
			},
		}, graphs[0])
	})

	t.Run("single record with hop1", func(t *testing.T) {
		parser := NewSurrealResponseParser(1000, 2000)
		response := []map[string]any{
			{
				"result": []any{
					map[string]any{
						"result": map[string]any{
							"root": map[string]any{
								"entity_id":   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
								"entity_type": "pod",
								"entity_data": map[string]any{},
								"liveness":    []any{},
							},
							"hop1": map[string]any{
								"pod_with_service": []any{
									map[string]any{
										"relation_id":       "rel_1",
										"relation_type":     "pod_with_service",
										"relation_category": "static",
										"direction":         "outbound",
										"relation_liveness": []any{},
										"target": map[string]any{
											"entity_id":   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
											"entity_type": "service",
											"entity_data": map[string]any{},
											"liveness":    []any{},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		graphs, err := parser.Parse(response)
		require.NoError(t, err)
		require.Equal(t, 1, len(graphs))

		assert.Equal(t, &LivenessGraph{
			QueryStart: 1000,
			QueryEnd:   2000,
			Nodes: map[string]*NodeLiveness{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {
					ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ResourceType: ResourceTypePod,
					Labels:       map[string]string{},
				},
				"service:⟨cluster=c1,namespace=ns1,service=svc1⟩": {
					ResourceID:   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
					ResourceType: ResourceTypeService,
					Labels:       map[string]string{},
				},
			},
			Edges: map[string]*EdgeLiveness{
				"rel_1": {
					RelationID:   "rel_1",
					RelationType: RelationType("pod_with_service"),
					Category:     RelationCategoryStatic,
					Direction:    DirectionOutbound,
					FromID:       "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ToID:         "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
				},
			},
			Adjacency: map[string][]string{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩":           {"rel_1"},
				"service:⟨cluster=c1,namespace=ns1,service=svc1⟩": {},
			},
		}, graphs[0])
	})

	t.Run("multiple records create separate graphs", func(t *testing.T) {
		parser := NewSurrealResponseParser(1000, 2000)
		response := []map[string]any{
			{
				"result": []any{
					map[string]any{
						"result": map[string]any{
							"root": map[string]any{
								"entity_id":   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
								"entity_type": "pod",
								"entity_data": map[string]any{},
								"liveness":    []any{},
							},
						},
					},
					map[string]any{
						"result": map[string]any{
							"root": map[string]any{
								"entity_id":   "pod:⟨cluster=c1,namespace=ns1,pod=p2⟩",
								"entity_type": "pod",
								"entity_data": map[string]any{},
								"liveness":    []any{},
							},
						},
					},
				},
			},
		}

		graphs, err := parser.Parse(response)
		require.NoError(t, err)
		require.Equal(t, 2, len(graphs))

		assert.Equal(t, &LivenessGraph{
			QueryStart: 1000,
			QueryEnd:   2000,
			Nodes: map[string]*NodeLiveness{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {
					ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
					ResourceType: ResourceTypePod,
					Labels:       map[string]string{},
				},
			},
			Edges: map[string]*EdgeLiveness{},
			Adjacency: map[string][]string{
				"pod:⟨cluster=c1,namespace=ns1,pod=p1⟩": {},
			},
		}, graphs[0])

		assert.Equal(t, &LivenessGraph{
			QueryStart: 1000,
			QueryEnd:   2000,
			Nodes: map[string]*NodeLiveness{
				"pod:⟨cluster=c1,namespace=ns1,pod=p2⟩": {
					ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p2⟩",
					ResourceType: ResourceTypePod,
					Labels:       map[string]string{},
				},
			},
			Edges: map[string]*EdgeLiveness{},
			Adjacency: map[string][]string{
				"pod:⟨cluster=c1,namespace=ns1,pod=p2⟩": {},
			},
		}, graphs[1])
	})

	t.Run("invalid root adds error to graph", func(t *testing.T) {
		parser := NewSurrealResponseParser(1000, 2000)
		response := []map[string]any{
			{
				"result": []any{
					map[string]any{
						"result": map[string]any{
							"root": map[string]any{
								// Missing entity_id
								"entity_type": "pod",
								"entity_data": map[string]any{},
								"liveness":    []any{},
							},
						},
					},
				},
			},
		}

		graphs, err := parser.Parse(response)
		require.NoError(t, err)
		require.Equal(t, 1, len(graphs))

		assert.Equal(t, &LivenessGraph{
			QueryStart:      1000,
			QueryEnd:        2000,
			Nodes:           map[string]*NodeLiveness{},
			Edges:           map[string]*EdgeLiveness{},
			Adjacency:       map[string][]string{},
			TraversalErrors: []string{"parse root: missing entity_id"},
		}, graphs[0])
	})

	t.Run("record without result field skipped", func(t *testing.T) {
		parser := NewSurrealResponseParser(1000, 2000)
		response := []map[string]any{
			{
				"result": []any{
					map[string]any{
						"other": "data",
					},
				},
			},
		}

		graphs, err := parser.Parse(response)
		require.NoError(t, err)
		assert.Equal(t, ([]*LivenessGraph)(nil), graphs)
	})

	t.Run("record without root field skipped", func(t *testing.T) {
		parser := NewSurrealResponseParser(1000, 2000)
		response := []map[string]any{
			{
				"result": []any{
					map[string]any{
						"result": map[string]any{
							"hop1": map[string]any{}, // No root
						},
					},
				},
			},
		}

		graphs, err := parser.Parse(response)
		require.NoError(t, err)
		assert.Equal(t, ([]*LivenessGraph)(nil), graphs)
	})

	t.Run("multiple hops (hop1, hop2, hop3)", func(t *testing.T) {
		parser := NewSurrealResponseParser(1000, 2000)
		response := []map[string]any{
			{
				"result": []any{
					map[string]any{
						"result": map[string]any{
							"root": map[string]any{
								"entity_id":   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
								"entity_type": "pod",
								"entity_data": map[string]any{},
								"liveness":    []any{},
							},
							"hop1": map[string]any{
								"node_with_pod": []any{
									map[string]any{
										"relation_id":       "rel_1",
										"relation_type":     "node_with_pod",
										"relation_category": "static",
										"direction":         "inbound",
										"relation_liveness": []any{},
										"target": map[string]any{
											"entity_id":   "node:⟨cluster=c1,node=node1⟩",
											"entity_type": "node",
											"entity_data": map[string]any{},
											"liveness":    []any{},
										},
									},
								},
							},
							"hop2": map[string]any{
								"pod_with_service": []any{
									map[string]any{
										"relation_id":       "rel_2",
										"relation_type":     "pod_with_service",
										"relation_category": "static",
										"direction":         "outbound",
										"relation_liveness": []any{},
										"target": map[string]any{
											"entity_id":   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
											"entity_type": "service",
											"entity_data": map[string]any{},
											"liveness":    []any{},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		graphs, err := parser.Parse(response)
		require.NoError(t, err)
		require.Equal(t, 1, len(graphs))

		graph := graphs[0]
		assert.Equal(t, int64(1000), graph.QueryStart)
		assert.Equal(t, int64(2000), graph.QueryEnd)
		assert.Equal(t, 3, len(graph.Nodes))
		assert.Equal(t, 2, len(graph.Edges))

		// Check nodes
		assert.Equal(t, &NodeLiveness{
			ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ResourceType: ResourceTypePod,
			Labels:       map[string]string{},
		}, graph.Nodes["pod:⟨cluster=c1,namespace=ns1,pod=p1⟩"])

		assert.Equal(t, &NodeLiveness{
			ResourceID:   "node:⟨cluster=c1,node=node1⟩",
			ResourceType: ResourceTypeNode,
			Labels:       map[string]string{},
		}, graph.Nodes["node:⟨cluster=c1,node=node1⟩"])

		assert.Equal(t, &NodeLiveness{
			ResourceID:   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
			ResourceType: ResourceTypeService,
			Labels:       map[string]string{},
		}, graph.Nodes["service:⟨cluster=c1,namespace=ns1,service=svc1⟩"])

		// Check edges
		assert.Equal(t, &EdgeLiveness{
			RelationID:   "rel_1",
			RelationType: RelationType("node_with_pod"),
			Category:     RelationCategoryStatic,
			Direction:    DirectionInbound,
			FromID:       "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ToID:         "node:⟨cluster=c1,node=node1⟩",
		}, graph.Edges["rel_1"])

		assert.Equal(t, &EdgeLiveness{
			RelationID:   "rel_2",
			RelationType: RelationType("pod_with_service"),
			Category:     RelationCategoryStatic,
			Direction:    DirectionOutbound,
			FromID:       "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ToID:         "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
		}, graph.Edges["rel_2"])

		// Check adjacency (order-insensitive)
		assert.Equal(t, 3, len(graph.Adjacency))
		assert.ElementsMatch(t, []string{"rel_1", "rel_2"}, graph.Adjacency["pod:⟨cluster=c1,namespace=ns1,pod=p1⟩"])
		assert.Equal(t, []string{}, graph.Adjacency["node:⟨cluster=c1,node=node1⟩"])
		assert.Equal(t, []string{}, graph.Adjacency["service:⟨cluster=c1,namespace=ns1,service=svc1⟩"])
	})
}

func TestSurrealResponseParser_parseEntity(t *testing.T) {
	parser := NewSurrealResponseParser(1000, 2000)

	t.Run("valid entity", func(t *testing.T) {
		data := map[string]any{
			"entity_id":   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			"entity_type": "pod",
			"entity_data": map[string]any{
				"cluster":   "c1",
				"namespace": "ns1",
				"pod":       "p1",
			},
			"liveness": []any{
				map[string]any{"period_start": float64(1000), "period_end": float64(2000)},
			},
		}

		node, err := parser.parseEntity(data)
		require.NoError(t, err)
		assert.Equal(t, &NodeLiveness{
			ResourceID:   "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩",
			ResourceType: ResourceTypePod,
			Labels:       map[string]string{"cluster": "c1", "namespace": "ns1", "pod": "p1"},
			RawPeriods:   []*VisiblePeriod{{Start: 1000, End: 2000}},
		}, node)
	})

	t.Run("missing entity_id", func(t *testing.T) {
		data := map[string]any{
			"entity_type": "pod",
			"entity_data": map[string]any{},
		}

		_, err := parser.parseEntity(data)
		assert.Equal(t, "missing entity_id", err.Error())
	})

	t.Run("empty entity_id", func(t *testing.T) {
		data := map[string]any{
			"entity_id":   "",
			"entity_type": "pod",
			"entity_data": map[string]any{},
		}

		_, err := parser.parseEntity(data)
		assert.Equal(t, "missing entity_id", err.Error())
	})

	t.Run("non-string values in entity_data", func(t *testing.T) {
		data := map[string]any{
			"entity_id":   "test:⟨id=1⟩",
			"entity_type": "test",
			"entity_data": map[string]any{
				"string_val": "test",
				"int_val":    123,
				"float_val":  45.67,
				"bool_val":   true,
				"nil_val":    nil,
			},
		}

		node, err := parser.parseEntity(data)
		require.NoError(t, err)
		assert.Equal(t, &NodeLiveness{
			ResourceID:   "test:⟨id=1⟩",
			ResourceType: ResourceType("test"),
			Labels: map[string]string{
				"string_val": "test",
				"int_val":    "123",
				"float_val":  "45.67",
				"bool_val":   "true",
			},
		}, node)
	})

	t.Run("missing entity_data", func(t *testing.T) {
		data := map[string]any{
			"entity_id":   "test:⟨id=1⟩",
			"entity_type": "test",
		}

		node, err := parser.parseEntity(data)
		require.NoError(t, err)
		assert.Equal(t, &NodeLiveness{
			ResourceID:   "test:⟨id=1⟩",
			ResourceType: ResourceType("test"),
			Labels:       map[string]string{},
		}, node)
	})

	t.Run("missing liveness", func(t *testing.T) {
		data := map[string]any{
			"entity_id":   "test:⟨id=1⟩",
			"entity_type": "test",
		}

		node, err := parser.parseEntity(data)
		require.NoError(t, err)
		assert.Equal(t, &NodeLiveness{
			ResourceID:   "test:⟨id=1⟩",
			ResourceType: ResourceType("test"),
			Labels:       map[string]string{},
		}, node)
	})
}

func TestSurrealResponseParser_parseLivenessPeriods(t *testing.T) {
	parser := NewSurrealResponseParser(1000, 2000)

	t.Run("valid periods", func(t *testing.T) {
		data := []any{
			map[string]any{"period_start": float64(1000), "period_end": float64(1500)},
			map[string]any{"period_start": float64(1600), "period_end": float64(2000)},
		}

		periods := parser.parseLivenessPeriods(data)
		assert.Equal(t, []*VisiblePeriod{
			{Start: 1000, End: 1500},
			{Start: 1600, End: 2000},
		}, periods)
	})

	t.Run("nil input", func(t *testing.T) {
		periods := parser.parseLivenessPeriods(nil)
		assert.Equal(t, ([]*VisiblePeriod)(nil), periods)
	})

	t.Run("not array", func(t *testing.T) {
		periods := parser.parseLivenessPeriods("not_array")
		assert.Equal(t, ([]*VisiblePeriod)(nil), periods)
	})

	t.Run("empty array", func(t *testing.T) {
		periods := parser.parseLivenessPeriods([]any{})
		assert.Equal(t, ([]*VisiblePeriod)(nil), periods)
	})

	t.Run("invalid period item", func(t *testing.T) {
		data := []any{
			"not_a_map",
			map[string]any{"period_start": float64(1000), "period_end": float64(1500)},
		}

		periods := parser.parseLivenessPeriods(data)
		assert.Equal(t, []*VisiblePeriod{{Start: 1000, End: 1500}}, periods)
	})

	t.Run("period with start > end skipped", func(t *testing.T) {
		data := []any{
			map[string]any{"period_start": float64(2000), "period_end": float64(1000)}, // Invalid
			map[string]any{"period_start": float64(1000), "period_end": float64(1500)}, // Valid
		}

		periods := parser.parseLivenessPeriods(data)
		assert.Equal(t, []*VisiblePeriod{{Start: 1000, End: 1500}}, periods)
	})

	t.Run("int64 values", func(t *testing.T) {
		data := []any{
			map[string]any{"period_start": int64(1000), "period_end": int64(1500)},
		}

		periods := parser.parseLivenessPeriods(data)
		assert.Equal(t, []*VisiblePeriod{{Start: 1000, End: 1500}}, periods)
	})

	t.Run("int values", func(t *testing.T) {
		data := []any{
			map[string]any{"period_start": 1000, "period_end": 1500},
		}

		periods := parser.parseLivenessPeriods(data)
		assert.Equal(t, []*VisiblePeriod{{Start: 1000, End: 1500}}, periods)
	})

	t.Run("string values", func(t *testing.T) {
		data := []any{
			map[string]any{"period_start": "1000", "period_end": "1500"},
		}

		periods := parser.parseLivenessPeriods(data)
		assert.Equal(t, []*VisiblePeriod{{Start: 1000, End: 1500}}, periods)
	})
}

func TestSurrealResponseParser_toInt64(t *testing.T) {
	parser := NewSurrealResponseParser(1000, 2000)

	t.Run("float64", func(t *testing.T) {
		result := parser.toInt64(float64(1234.56))
		assert.Equal(t, int64(1234), result)
	})

	t.Run("int64", func(t *testing.T) {
		result := parser.toInt64(int64(1234))
		assert.Equal(t, int64(1234), result)
	})

	t.Run("int", func(t *testing.T) {
		result := parser.toInt64(1234)
		assert.Equal(t, int64(1234), result)
	})

	t.Run("string", func(t *testing.T) {
		result := parser.toInt64("1234")
		assert.Equal(t, int64(1234), result)
	})

	t.Run("invalid string", func(t *testing.T) {
		result := parser.toInt64("not_a_number")
		assert.Equal(t, int64(0), result)
	})

	t.Run("nil", func(t *testing.T) {
		result := parser.toInt64(nil)
		assert.Equal(t, int64(0), result)
	})

	t.Run("unsupported type", func(t *testing.T) {
		result := parser.toInt64([]int{1, 2, 3})
		assert.Equal(t, int64(0), result)
	})
}

func TestSurrealResponseParser_parseRelation(t *testing.T) {
	parser := NewSurrealResponseParser(1000, 2000)

	t.Run("valid relation", func(t *testing.T) {
		data := map[string]any{
			"relation_id":       "rel_1",
			"relation_type":     "pod_with_service",
			"relation_category": "static",
			"direction":         "outbound",
			"relation_liveness": []any{
				map[string]any{"period_start": float64(1000), "period_end": float64(2000)},
			},
			"target": map[string]any{
				"entity_id":   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
				"entity_type": "service",
				"entity_data": map[string]any{},
				"liveness":    []any{},
			},
		}

		edge, targetNode, nestedHops, err := parser.parseRelation("from_id", "pod_with_service", data)
		require.NoError(t, err)

		assert.Equal(t, &EdgeLiveness{
			RelationID:   "rel_1",
			RelationType: RelationType("pod_with_service"),
			Category:     RelationCategoryStatic,
			Direction:    DirectionOutbound,
			FromID:       "from_id",
			ToID:         "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
			RawPeriods:   []*VisiblePeriod{{Start: 1000, End: 2000}},
		}, edge)

		assert.Equal(t, &NodeLiveness{
			ResourceID:   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
			ResourceType: ResourceTypeService,
			Labels:       map[string]string{},
		}, targetNode)

		assert.Equal(t, ([]map[string]any)(nil), nestedHops)
	})

	t.Run("missing relation_id", func(t *testing.T) {
		data := map[string]any{
			"relation_type": "pod_with_service",
			"target": map[string]any{
				"entity_id": "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
			},
		}

		_, _, _, err := parser.parseRelation("from_id", "pod_with_service", data)
		assert.Equal(t, "missing relation_id", err.Error())
	})

	t.Run("missing target", func(t *testing.T) {
		data := map[string]any{
			"relation_id":   "rel_1",
			"relation_type": "pod_with_service",
		}

		_, _, _, err := parser.parseRelation("from_id", "pod_with_service", data)
		assert.Equal(t, "missing target", err.Error())
	})

	t.Run("with nested hops", func(t *testing.T) {
		data := map[string]any{
			"relation_id":       "rel_1",
			"relation_type":     "pod_with_service",
			"relation_category": "static",
			"relation_liveness": []any{},
			"target": map[string]any{
				"entity_id":   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
				"entity_type": "service",
				"entity_data": map[string]any{},
				"liveness":    []any{},
				"hop2": map[string]any{
					"ingress_with_service": []any{},
				},
				"hop3": map[string]any{
					"domain_with_service": []any{},
				},
			},
		}

		_, _, nestedHops, err := parser.parseRelation("from_id", "pod_with_service", data)
		require.NoError(t, err)
		assert.Equal(t, 2, len(nestedHops))
	})

	t.Run("invalid nested hop type", func(t *testing.T) {
		data := map[string]any{
			"relation_id":       "rel_1",
			"relation_type":     "pod_with_service",
			"relation_category": "static",
			"relation_liveness": []any{},
			"target": map[string]any{
				"entity_id":   "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
				"entity_type": "service",
				"entity_data": map[string]any{},
				"liveness":    []any{},
				"hop2":        "invalid_string", // Should be map[string]any
			},
		}

		_, _, nestedHops, err := parser.parseRelation("from_id", "pod_with_service", data)
		require.NoError(t, err)
		assert.Equal(t, ([]map[string]any)(nil), nestedHops)
	})
}
