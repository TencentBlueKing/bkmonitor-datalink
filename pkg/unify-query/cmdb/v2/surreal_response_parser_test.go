// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v2

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

		assert.Len(t, graph.Nodes, 1)
		assert.Len(t, graph.Edges, 0)
		assert.False(t, graph.HasErrors())
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

		assert.Len(t, graph.Nodes, 2)
		assert.Len(t, graph.Edges, 1)
		assert.False(t, graph.HasErrors())

		// Verify target node
		targetNode := graph.GetNode("service:⟨cluster=c1,namespace=ns1,service=svc1⟩")
		require.NotNil(t, targetNode)
		assert.Equal(t, ResourceTypeService, targetNode.ResourceType)
		assert.Equal(t, "c1", targetNode.Labels["cluster"])

		// Verify edge
		edge := graph.GetEdge("rel_1")
		require.NotNil(t, edge)
		assert.Equal(t, RelationType("pod_with_service"), edge.RelationType)
		assert.Equal(t, rootNode.ResourceID, edge.FromID)
		assert.Equal(t, targetNode.ResourceID, edge.ToID)
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

		assert.Len(t, graph.Nodes, 3)
		assert.Len(t, graph.Edges, 2)
		assert.False(t, graph.HasErrors())
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

		assert.Len(t, graph.Nodes, 3)
		assert.Len(t, graph.Edges, 2)
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

		assert.Len(t, graph.Nodes, 3) // pod, replicaset, deployment
		assert.Len(t, graph.Edges, 2) // pod->replicaset, replicaset->deployment
		assert.False(t, graph.HasErrors())

		// Verify nested edge
		edge2 := graph.GetEdge("rel_2")
		require.NotNil(t, edge2)
		assert.Equal(t, "replicaset:⟨cluster=c1,namespace=ns1,replicaset=rs1⟩", edge2.FromID)
		assert.Equal(t, "deployment:⟨cluster=c1,namespace=ns1,deployment=dep1⟩", edge2.ToID)
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

		assert.Len(t, graph.Nodes, 4) // container, pod, node, system
		assert.Len(t, graph.Edges, 3)
		assert.False(t, graph.HasErrors())

		// Verify the chain
		assert.NotNil(t, graph.GetEdge("rel_1"))
		assert.NotNil(t, graph.GetEdge("rel_2"))
		assert.NotNil(t, graph.GetEdge("rel_3"))
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

		assert.Len(t, graph.Nodes, 1)
		assert.Len(t, graph.Edges, 0)
		assert.False(t, graph.HasErrors())
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

		assert.Len(t, graph.Nodes, 1)
		assert.Len(t, graph.Edges, 0)
		assert.False(t, graph.HasErrors())
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

		assert.Len(t, graph.Nodes, 1)
		assert.Len(t, graph.Edges, 0)
		assert.True(t, graph.HasErrors())
		assert.Contains(t, graph.TraversalErrors[0], "relation_id")
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

		assert.Len(t, graph.Nodes, 1)
		assert.Len(t, graph.Edges, 0)
		assert.True(t, graph.HasErrors())
		assert.Contains(t, graph.TraversalErrors[0], "target")
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

		assert.Len(t, graph.Nodes, 1)
		assert.Len(t, graph.Edges, 0)
		assert.True(t, graph.HasErrors())
		assert.Contains(t, graph.TraversalErrors[0], "entity_id")
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

		assert.Len(t, graph.Nodes, 2)
		assert.Len(t, graph.Edges, 1)

		// Verify original node preserved (not overwritten)
		targetNode := graph.GetNode("service:⟨cluster=c1,namespace=ns1,service=svc1⟩")
		assert.Equal(t, "true", targetNode.Labels["existing"])
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

		assert.Len(t, graph.Nodes, 5) // pod, service, ingress1, ingress2, k8s_address
		assert.Len(t, graph.Edges, 4)
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

		edge := graph.GetEdge("rel_1")
		require.NotNil(t, edge)
		assert.Len(t, edge.RawPeriods, 2)
		assert.Equal(t, int64(1100), edge.RawPeriods[0].Start)
		assert.Equal(t, int64(1500), edge.RawPeriods[0].End)

		targetNode := graph.GetNode("service:⟨cluster=c1,namespace=ns1,service=svc1⟩")
		require.NotNil(t, targetNode)
		assert.Len(t, targetNode.RawPeriods, 1)
	})
}

func TestSurrealResponseParser_Parse(t *testing.T) {
	t.Run("empty response", func(t *testing.T) {
		parser := NewSurrealResponseParser(1000, 2000)
		graphs, err := parser.Parse(nil)
		require.NoError(t, err)
		assert.Len(t, graphs, 0)
	})

	t.Run("empty array response", func(t *testing.T) {
		parser := NewSurrealResponseParser(1000, 2000)
		graphs, err := parser.Parse([]map[string]any{})
		require.NoError(t, err)
		assert.Len(t, graphs, 0)
	})

	t.Run("missing result field", func(t *testing.T) {
		parser := NewSurrealResponseParser(1000, 2000)
		graphs, err := parser.Parse([]map[string]any{
			{"other_field": "value"},
		})
		require.NoError(t, err)
		assert.Len(t, graphs, 0)
	})

	t.Run("result is not array", func(t *testing.T) {
		parser := NewSurrealResponseParser(1000, 2000)
		graphs, err := parser.Parse([]map[string]any{
			{"result": "not_an_array"},
		})
		require.NoError(t, err)
		assert.Len(t, graphs, 0)
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
		assert.Len(t, graphs, 1)

		graph := graphs[0]
		assert.Len(t, graph.Nodes, 1)
		assert.Len(t, graph.Edges, 0)

		rootNode := graph.GetNode("pod:⟨cluster=c1,namespace=ns1,pod=p1⟩")
		require.NotNil(t, rootNode)
		assert.Equal(t, ResourceTypePod, rootNode.ResourceType)
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
		assert.Len(t, graphs, 1)

		graph := graphs[0]
		assert.Len(t, graph.Nodes, 2)
		assert.Len(t, graph.Edges, 1)
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
		assert.Len(t, graphs, 2)

		// Each graph has its own root
		assert.NotNil(t, graphs[0].GetNode("pod:⟨cluster=c1,namespace=ns1,pod=p1⟩"))
		assert.NotNil(t, graphs[1].GetNode("pod:⟨cluster=c1,namespace=ns1,pod=p2⟩"))
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
		assert.Len(t, graphs, 1)

		graph := graphs[0]
		assert.True(t, graph.HasErrors())
		assert.Contains(t, graph.TraversalErrors[0], "root")
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
		assert.Len(t, graphs, 0)
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
		assert.Len(t, graphs, 0)
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
		assert.Len(t, graphs, 1)

		graph := graphs[0]
		// Note: hop1 and hop2 at result level are both from root
		assert.Len(t, graph.Nodes, 3) // pod, node, service
		assert.Len(t, graph.Edges, 2)
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
		assert.Equal(t, "pod:⟨cluster=c1,namespace=ns1,pod=p1⟩", node.ResourceID)
		assert.Equal(t, ResourceTypePod, node.ResourceType)
		assert.Equal(t, "c1", node.Labels["cluster"])
		assert.Len(t, node.RawPeriods, 1)
	})

	t.Run("missing entity_id", func(t *testing.T) {
		data := map[string]any{
			"entity_type": "pod",
			"entity_data": map[string]any{},
		}

		_, err := parser.parseEntity(data)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "entity_id")
	})

	t.Run("empty entity_id", func(t *testing.T) {
		data := map[string]any{
			"entity_id":   "",
			"entity_type": "pod",
			"entity_data": map[string]any{},
		}

		_, err := parser.parseEntity(data)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "entity_id")
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
		assert.Equal(t, "test", node.Labels["string_val"])
		assert.Equal(t, "123", node.Labels["int_val"])
		assert.Equal(t, "45.67", node.Labels["float_val"])
		assert.Equal(t, "true", node.Labels["bool_val"])
		_, hasNil := node.Labels["nil_val"]
		assert.False(t, hasNil)
	})

	t.Run("missing entity_data", func(t *testing.T) {
		data := map[string]any{
			"entity_id":   "test:⟨id=1⟩",
			"entity_type": "test",
		}

		node, err := parser.parseEntity(data)
		require.NoError(t, err)
		assert.Empty(t, node.Labels)
	})

	t.Run("missing liveness", func(t *testing.T) {
		data := map[string]any{
			"entity_id":   "test:⟨id=1⟩",
			"entity_type": "test",
		}

		node, err := parser.parseEntity(data)
		require.NoError(t, err)
		assert.Nil(t, node.RawPeriods)
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
		assert.Len(t, periods, 2)
		assert.Equal(t, int64(1000), periods[0].Start)
		assert.Equal(t, int64(1500), periods[0].End)
	})

	t.Run("nil input", func(t *testing.T) {
		periods := parser.parseLivenessPeriods(nil)
		assert.Nil(t, periods)
	})

	t.Run("not array", func(t *testing.T) {
		periods := parser.parseLivenessPeriods("not_array")
		assert.Nil(t, periods)
	})

	t.Run("empty array", func(t *testing.T) {
		periods := parser.parseLivenessPeriods([]any{})
		assert.Nil(t, periods)
	})

	t.Run("invalid period item", func(t *testing.T) {
		data := []any{
			"not_a_map",
			map[string]any{"period_start": float64(1000), "period_end": float64(1500)},
		}

		periods := parser.parseLivenessPeriods(data)
		assert.Len(t, periods, 1)
	})

	t.Run("period with start > end skipped", func(t *testing.T) {
		data := []any{
			map[string]any{"period_start": float64(2000), "period_end": float64(1000)}, // Invalid
			map[string]any{"period_start": float64(1000), "period_end": float64(1500)}, // Valid
		}

		periods := parser.parseLivenessPeriods(data)
		assert.Len(t, periods, 1)
		assert.Equal(t, int64(1000), periods[0].Start)
	})

	t.Run("int64 values", func(t *testing.T) {
		data := []any{
			map[string]any{"period_start": int64(1000), "period_end": int64(1500)},
		}

		periods := parser.parseLivenessPeriods(data)
		assert.Len(t, periods, 1)
		assert.Equal(t, int64(1000), periods[0].Start)
	})

	t.Run("int values", func(t *testing.T) {
		data := []any{
			map[string]any{"period_start": 1000, "period_end": 1500},
		}

		periods := parser.parseLivenessPeriods(data)
		assert.Len(t, periods, 1)
		assert.Equal(t, int64(1000), periods[0].Start)
	})

	t.Run("string values", func(t *testing.T) {
		data := []any{
			map[string]any{"period_start": "1000", "period_end": "1500"},
		}

		periods := parser.parseLivenessPeriods(data)
		assert.Len(t, periods, 1)
		assert.Equal(t, int64(1000), periods[0].Start)
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

		assert.Equal(t, "rel_1", edge.RelationID)
		assert.Equal(t, RelationType("pod_with_service"), edge.RelationType)
		assert.Equal(t, RelationCategoryStatic, edge.Category)
		assert.Equal(t, DirectionOutbound, edge.Direction)
		assert.Equal(t, "from_id", edge.FromID)
		assert.Equal(t, "service:⟨cluster=c1,namespace=ns1,service=svc1⟩", edge.ToID)
		assert.Len(t, edge.RawPeriods, 1)

		assert.Equal(t, "service:⟨cluster=c1,namespace=ns1,service=svc1⟩", targetNode.ResourceID)
		assert.Empty(t, nestedHops)
	})

	t.Run("missing relation_id", func(t *testing.T) {
		data := map[string]any{
			"relation_type": "pod_with_service",
			"target": map[string]any{
				"entity_id": "service:⟨cluster=c1,namespace=ns1,service=svc1⟩",
			},
		}

		_, _, _, err := parser.parseRelation("from_id", "pod_with_service", data)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "relation_id")
	})

	t.Run("missing target", func(t *testing.T) {
		data := map[string]any{
			"relation_id":   "rel_1",
			"relation_type": "pod_with_service",
		}

		_, _, _, err := parser.parseRelation("from_id", "pod_with_service", data)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "target")
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
		assert.Len(t, nestedHops, 2)
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
		assert.Len(t, nestedHops, 0) // Invalid hop ignored
	})
}
