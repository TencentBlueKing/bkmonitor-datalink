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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

type mockGraphQueryExecutor struct {
	graphs []*LivenessGraph
	err    error
}

func (m *mockGraphQueryExecutor) Execute(ctx context.Context, sql string, start, end int64) ([]*LivenessGraph, error) {
	if m.err != nil {
		return nil, m.err
	}
	for _, g := range m.graphs {
		g.QueryStart = start
		g.QueryEnd = end
	}
	return m.graphs, nil
}

type CMDBHandlerTestCase struct {
	Name          string
	LookBackDelta string
	SpaceUid      string
	Ts            string
	StartTs       string
	EndTs         string
	Step          string
	Target        cmdb.Resource
	Source        cmdb.Resource
	IndexMatcher  cmdb.Matcher
	ExpandMatcher cmdb.Matcher
	ExpandShow    bool
	PathResource  []cmdb.Resource

	MockGraphs []*LivenessGraph
	MockError  error

	ExpectedSource     cmdb.Resource
	ExpectedMatcher    cmdb.Matcher
	ExpectedPath       []cmdb.PathV2
	ExpectedTarget     cmdb.Resource
	ExpectedMatchers   cmdb.Matchers                // instant 查询用
	ExpectedTimeSeries []cmdb.MatchersWithTimestamp // range 查询用：完整时间序列
	ExpectedError      bool
}

func TestQueryResourceMatcher(t *testing.T) {
	testCases := []CMDBHandlerTestCase{
		{
			Name:          "Node_To_Pod_SingleHop",
			LookBackDelta: "10m",
			SpaceUid:      "test-space",
			Ts:            "600",
			Target:        "pod",
			Source:        "node",
			IndexMatcher: cmdb.Matcher{
				"bcs_cluster_id": "BCS-K8S-00001",
				"node":           "node-1",
			},
			ExpandMatcher: nil,
			ExpandShow:    false,
			PathResource:  nil,
			MockGraphs: []*LivenessGraph{
				{
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
						"pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=kube-system,pod=coredns-1⟩": {
							ResourceID:   "pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=kube-system,pod=coredns-1⟩",
							ResourceType: ResourceTypePod,
							Labels: map[string]string{
								"bcs_cluster_id": "BCS-K8S-00001",
								"namespace":      "kube-system",
								"pod":            "coredns-1",
							},
							RawPeriods: []*VisiblePeriod{{Start: 200000, End: 600000}},
						},
					},
					Edges: map[string]*EdgeLiveness{
						"node_with_pod:1": {
							RelationID:   "node_with_pod:1",
							RelationType: RelationNodeWithPod,
							Category:     RelationCategoryStatic,
							FromID:       "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩",
							ToID:         "pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩",
						},
						"node_with_pod:2": {
							RelationID:   "node_with_pod:2",
							RelationType: RelationNodeWithPod,
							Category:     RelationCategoryStatic,
							FromID:       "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩",
							ToID:         "pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=kube-system,pod=coredns-1⟩",
						},
					},
				},
			},
			ExpectedSource: "node",
			ExpectedMatcher: cmdb.Matcher{
				"bcs_cluster_id": "BCS-K8S-00001",
				"node":           "node-1",
			},
			ExpectedPath: []cmdb.PathV2{
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "system", RelationType: "node_with_system", Category: "static", Direction: "outbound"},
					{ResourceType: "pod", RelationType: "pod_to_system", Category: "dynamic", Direction: "inbound"},
				}},
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "system", RelationType: "node_with_system", Category: "static", Direction: "outbound"},
					{ResourceType: "pod", RelationType: "system_to_pod", Category: "dynamic", Direction: "outbound"},
				}},
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "pod", RelationType: "node_with_pod", Category: "static", Direction: "outbound"},
				}},
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "datasource", RelationType: "datasource_with_node", Category: "static", Direction: "inbound"},
					{ResourceType: "pod", RelationType: "datasource_with_pod", Category: "static", Direction: "outbound"},
				}},
			},
			ExpectedTarget: "pod",
			ExpectedMatchers: cmdb.Matchers{
				{
					"bcs_cluster_id": "BCS-K8S-00001",
					"namespace":      "default",
					"pod":            "nginx-1",
				},
				{
					"bcs_cluster_id": "BCS-K8S-00001",
					"namespace":      "kube-system",
					"pod":            "coredns-1",
				},
			},
			ExpectedError: false,
		},
		{
			Name:          "Pod_To_Node_WithPath",
			LookBackDelta: "5m",
			SpaceUid:      "test-space",
			Ts:            "1000",
			Target:        "node",
			Source:        "pod",
			IndexMatcher: cmdb.Matcher{
				"bcs_cluster_id": "BCS-K8S-00001",
				"namespace":      "default",
				"pod":            "nginx-1",
			},
			ExpandMatcher: nil,
			ExpandShow:    false,
			PathResource:  []cmdb.Resource{"node"},
			MockGraphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩": {
							ResourceID:   "pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩",
							ResourceType: ResourceTypePod,
							Labels: map[string]string{
								"bcs_cluster_id": "BCS-K8S-00001",
								"namespace":      "default",
								"pod":            "nginx-1",
							},
						},
						"node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩": {
							ResourceID:   "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩",
							ResourceType: ResourceTypeNode,
							Labels: map[string]string{
								"bcs_cluster_id": "BCS-K8S-00001",
								"node":           "node-1",
							},
						},
					},
				},
			},
			ExpectedSource: "pod",
			ExpectedMatcher: cmdb.Matcher{
				"bcs_cluster_id": "BCS-K8S-00001",
				"namespace":      "default",
				"pod":            "nginx-1",
			},
			ExpectedPath: []cmdb.PathV2{
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "pod", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "node", RelationType: "node_with_pod", Category: "static", Direction: "inbound"},
				}},
			},
			ExpectedTarget: "node",
			ExpectedMatchers: cmdb.Matchers{
				{
					"bcs_cluster_id": "BCS-K8S-00001",
					"node":           "node-1",
				},
			},
			ExpectedError: false,
		},
		{
			Name:          "Empty_Result",
			LookBackDelta: "10m",
			SpaceUid:      "test-space",
			Ts:            "600",
			Target:        "pod",
			Source:        "node",
			IndexMatcher: cmdb.Matcher{
				"bcs_cluster_id": "BCS-K8S-00001",
				"node":           "non-existent",
			},
			MockGraphs:      nil,
			ExpectedSource:  "node",
			ExpectedMatcher: cmdb.Matcher{"bcs_cluster_id": "BCS-K8S-00001", "node": "non-existent"},
			ExpectedPath: []cmdb.PathV2{
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "system", RelationType: "node_with_system", Category: "static", Direction: "outbound"},
					{ResourceType: "pod", RelationType: "pod_to_system", Category: "dynamic", Direction: "inbound"},
				}},
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "system", RelationType: "node_with_system", Category: "static", Direction: "outbound"},
					{ResourceType: "pod", RelationType: "system_to_pod", Category: "dynamic", Direction: "outbound"},
				}},
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "pod", RelationType: "node_with_pod", Category: "static", Direction: "outbound"},
				}},
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "datasource", RelationType: "datasource_with_node", Category: "static", Direction: "inbound"},
					{ResourceType: "pod", RelationType: "datasource_with_pod", Category: "static", Direction: "outbound"},
				}},
			},
			ExpectedTarget:   "pod",
			ExpectedMatchers: nil,
			ExpectedError:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			ctx := context.Background()
			model, err := NewModel(ctx, &mockGraphQueryExecutor{
				graphs: tc.MockGraphs,
				err:    tc.MockError,
			})
			require.NoError(t, err)

			source, matcher, path, target, matchers, err := model.QueryResourceMatcher(
				ctx,
				tc.LookBackDelta,
				tc.SpaceUid,
				tc.Ts,
				tc.Target,
				tc.Source,
				tc.IndexMatcher,
				tc.ExpandMatcher,
				tc.ExpandShow,
				tc.PathResource,
			)

			if tc.ExpectedError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.ExpectedSource, source, "source mismatch")
			assert.Equal(t, tc.ExpectedMatcher, matcher, "matcher mismatch")
			assert.Equal(t, tc.ExpectedPath, path, "path mismatch")
			assert.Equal(t, tc.ExpectedTarget, target, "target mismatch")

			if tc.ExpectedMatchers == nil {
				assert.Nil(t, matchers)
			} else {
				assert.Equal(t, len(tc.ExpectedMatchers), len(matchers), "matchers count mismatch")
				for _, expected := range tc.ExpectedMatchers {
					found := false
					for _, actual := range matchers {
						if matchersEqual(expected, actual) {
							found = true
							break
						}
					}
					assert.True(t, found, "expected matcher not found: %v", expected)
				}
			}
		})
	}
}

func TestQueryResourceMatcherRange(t *testing.T) {
	nginx1Matcher := cmdb.Matcher{
		"bcs_cluster_id": "BCS-K8S-00001",
		"namespace":      "default",
		"pod":            "nginx-1",
	}
	nginx2Matcher := cmdb.Matcher{
		"bcs_cluster_id": "BCS-K8S-00001",
		"namespace":      "default",
		"pod":            "nginx-2",
	}

	testCases := []CMDBHandlerTestCase{
		{
			Name:          "Node_To_Pod_Range_AllActive",
			LookBackDelta: "10m",
			SpaceUid:      "test-space",
			StartTs:       "0",
			EndTs:         "300",
			Step:          "100s",
			Target:        "pod",
			Source:        "node",
			IndexMatcher: cmdb.Matcher{
				"bcs_cluster_id": "BCS-K8S-00001",
				"node":           "node-1",
			},
			MockGraphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩": {
							ResourceID:   "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩",
							ResourceType: ResourceTypeNode,
							Labels: map[string]string{
								"bcs_cluster_id": "BCS-K8S-00001",
								"node":           "node-1",
							},
							RawPeriods: []*VisiblePeriod{{Start: 0, End: 400000}},
						},
						"pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩": {
							ResourceID:   "pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩",
							ResourceType: ResourceTypePod,
							Labels: map[string]string{
								"bcs_cluster_id": "BCS-K8S-00001",
								"namespace":      "default",
								"pod":            "nginx-1",
							},
							RawPeriods: []*VisiblePeriod{{Start: 0, End: 400000}},
						},
					},
				},
			},
			ExpectedSource: "node",
			ExpectedMatcher: cmdb.Matcher{
				"bcs_cluster_id": "BCS-K8S-00001",
				"node":           "node-1",
			},
			ExpectedTarget: "pod",
			// start=0, end=300s=300000ms, step=100s=100000ms
			// 时间点: 0, 100000, 200000, 300000
			ExpectedTimeSeries: []cmdb.MatchersWithTimestamp{
				{Timestamp: 0, Matchers: cmdb.Matchers{nginx1Matcher}},
				{Timestamp: 100000, Matchers: cmdb.Matchers{nginx1Matcher}},
				{Timestamp: 200000, Matchers: cmdb.Matchers{nginx1Matcher}},
				{Timestamp: 300000, Matchers: cmdb.Matchers{nginx1Matcher}},
			},
			ExpectedError: false,
		},
		{
			Name:          "Node_To_Pod_Range_PartialActive",
			LookBackDelta: "10m",
			SpaceUid:      "test-space",
			StartTs:       "0",
			EndTs:         "500",
			Step:          "100s",
			Target:        "pod",
			Source:        "node",
			IndexMatcher: cmdb.Matcher{
				"bcs_cluster_id": "BCS-K8S-00001",
				"node":           "node-1",
			},
			MockGraphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩": {
							ResourceID:   "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩",
							ResourceType: ResourceTypeNode,
							Labels: map[string]string{
								"bcs_cluster_id": "BCS-K8S-00001",
								"node":           "node-1",
							},
							RawPeriods: []*VisiblePeriod{{Start: 0, End: 600000}},
						},
						// nginx-1: 活跃时间段 [100000, 300000]
						"pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩": {
							ResourceID:   "pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-1⟩",
							ResourceType: ResourceTypePod,
							Labels: map[string]string{
								"bcs_cluster_id": "BCS-K8S-00001",
								"namespace":      "default",
								"pod":            "nginx-1",
							},
							RawPeriods: []*VisiblePeriod{{Start: 100000, End: 300000}},
						},
						// nginx-2: 活跃时间段 [250000, 500000]
						"pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-2⟩": {
							ResourceID:   "pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-2⟩",
							ResourceType: ResourceTypePod,
							Labels: map[string]string{
								"bcs_cluster_id": "BCS-K8S-00001",
								"namespace":      "default",
								"pod":            "nginx-2",
							},
							RawPeriods: []*VisiblePeriod{{Start: 250000, End: 500000}},
						},
					},
				},
			},
			ExpectedSource: "node",
			ExpectedMatcher: cmdb.Matcher{
				"bcs_cluster_id": "BCS-K8S-00001",
				"node":           "node-1",
			},
			ExpectedTarget: "pod",
			// start=0, end=500s=500000ms, step=100s=100000ms
			// 时间点: 0, 100000, 200000, 300000, 400000, 500000
			// nginx-1 活跃: [100000, 300000]
			// nginx-2 活跃: [250000, 500000]
			ExpectedTimeSeries: []cmdb.MatchersWithTimestamp{
				// ts=0: 无活跃 pod (不在结果中)
				{Timestamp: 100000, Matchers: cmdb.Matchers{nginx1Matcher}},
				{Timestamp: 200000, Matchers: cmdb.Matchers{nginx1Matcher}},
				{Timestamp: 300000, Matchers: cmdb.Matchers{nginx1Matcher, nginx2Matcher}}, // 两个都活跃
				{Timestamp: 400000, Matchers: cmdb.Matchers{nginx2Matcher}},
				{Timestamp: 500000, Matchers: cmdb.Matchers{nginx2Matcher}},
			},
			ExpectedError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			ctx := context.Background()
			model, err := NewModel(ctx, &mockGraphQueryExecutor{
				graphs: tc.MockGraphs,
				err:    tc.MockError,
			})
			require.NoError(t, err)

			source, matcher, _, target, result, err := model.QueryResourceMatcherRange(
				ctx,
				tc.LookBackDelta,
				tc.SpaceUid,
				tc.Step,
				tc.StartTs,
				tc.EndTs,
				tc.Target,
				tc.Source,
				tc.IndexMatcher,
				tc.ExpandMatcher,
				tc.ExpandShow,
				tc.PathResource,
			)

			if tc.ExpectedError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.ExpectedSource, source, "source mismatch")
			assert.Equal(t, tc.ExpectedMatcher, matcher, "matcher mismatch")
			assert.Equal(t, tc.ExpectedTarget, target, "target mismatch")

			if tc.ExpectedTimeSeries == nil {
				assert.Nil(t, result, "expected nil result")
			} else {
				require.NotNil(t, result, "result should not be nil")
				require.Equal(t, len(tc.ExpectedTimeSeries), len(result), "time series length mismatch")

				for i, expected := range tc.ExpectedTimeSeries {
					actual := result[i]
					assert.Equal(t, expected.Timestamp, actual.Timestamp, "timestamp mismatch at index %d", i)
					require.Equal(t, len(expected.Matchers), len(actual.Matchers),
						"matchers count mismatch at timestamp %d", expected.Timestamp)

					for _, expectedMatcher := range expected.Matchers {
						found := false
						for _, actualMatcher := range actual.Matchers {
							if matchersEqual(expectedMatcher, actualMatcher) {
								found = true
								break
							}
						}
						assert.True(t, found, "expected matcher not found at timestamp %d: %v",
							expected.Timestamp, expectedMatcher)
					}
				}
			}
		})
	}
}

func matchersEqual(a, b cmdb.Matcher) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
