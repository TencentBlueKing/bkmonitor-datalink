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

			source, matcher, path, target, matchers, err := model.QueryDynamicPaths(
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

func TestBuildTargetMatchersTimeSeries(t *testing.T) {
	testCases := []struct {
		Name       string
		Graphs     []*LivenessGraph
		TargetType ResourceType
		Start      int64
		End        int64
		StepMs     int64
		Expected   []cmdb.MatchersWithTimestamp
	}{
		{
			Name:       "EmptyGraphs",
			Graphs:     nil,
			TargetType: ResourceTypePod,
			Start:      0,
			End:        100000,
			StepMs:     50000,
			Expected:   nil,
		},
		{
			Name:       "EmptyGraphsList",
			Graphs:     []*LivenessGraph{},
			TargetType: ResourceTypePod,
			Start:      0,
			End:        100000,
			StepMs:     50000,
			Expected:   nil,
		},
		{
			Name: "NoTargetTypeNodes",
			Graphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"node:node-1": {
							ResourceID:   "node:node-1",
							ResourceType: ResourceTypeNode,
							Labels:       map[string]string{"node": "node-1"},
							RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200000}},
						},
					},
				},
			},
			TargetType: ResourceTypePod,
			Start:      0,
			End:        100000,
			StepMs:     50000,
			Expected:   nil,
		},
		{
			Name: "SingleNodeAlwaysActive",
			Graphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"pod:nginx-1": {
							ResourceID:   "pod:nginx-1",
							ResourceType: ResourceTypePod,
							Labels:       map[string]string{"pod": "nginx-1", "namespace": "default"},
							RawPeriods:   []*VisiblePeriod{{Start: 0, End: 300000}},
						},
					},
				},
			},
			TargetType: ResourceTypePod,
			Start:      0,
			End:        200000,
			StepMs:     100000,
			Expected: []cmdb.MatchersWithTimestamp{
				{Timestamp: 0, Matchers: cmdb.Matchers{{"pod": "nginx-1", "namespace": "default"}}},
				{Timestamp: 100000, Matchers: cmdb.Matchers{{"pod": "nginx-1", "namespace": "default"}}},
				{Timestamp: 200000, Matchers: cmdb.Matchers{{"pod": "nginx-1", "namespace": "default"}}},
			},
		},
		{
			Name: "SingleNodeNeverActive",
			Graphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"pod:nginx-1": {
							ResourceID:   "pod:nginx-1",
							ResourceType: ResourceTypePod,
							Labels:       map[string]string{"pod": "nginx-1"},
							RawPeriods:   []*VisiblePeriod{{Start: 500000, End: 600000}},
						},
					},
				},
			},
			TargetType: ResourceTypePod,
			Start:      0,
			End:        200000,
			StepMs:     100000,
			Expected:   nil,
		},
		{
			Name: "SingleNodePartialActive_StartOnly",
			Graphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"pod:nginx-1": {
							ResourceID:   "pod:nginx-1",
							ResourceType: ResourceTypePod,
							Labels:       map[string]string{"pod": "nginx-1"},
							RawPeriods:   []*VisiblePeriod{{Start: 0, End: 50000}},
						},
					},
				},
			},
			TargetType: ResourceTypePod,
			Start:      0,
			End:        200000,
			StepMs:     100000,
			Expected: []cmdb.MatchersWithTimestamp{
				{Timestamp: 0, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
			},
		},
		{
			Name: "SingleNodePartialActive_EndOnly",
			Graphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"pod:nginx-1": {
							ResourceID:   "pod:nginx-1",
							ResourceType: ResourceTypePod,
							Labels:       map[string]string{"pod": "nginx-1"},
							RawPeriods:   []*VisiblePeriod{{Start: 150000, End: 250000}},
						},
					},
				},
			},
			TargetType: ResourceTypePod,
			Start:      0,
			End:        200000,
			StepMs:     100000,
			Expected: []cmdb.MatchersWithTimestamp{
				{Timestamp: 200000, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
			},
		},
		{
			Name: "MultiplePeriods_SameNode",
			Graphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"pod:nginx-1": {
							ResourceID:   "pod:nginx-1",
							ResourceType: ResourceTypePod,
							Labels:       map[string]string{"pod": "nginx-1"},
							RawPeriods: []*VisiblePeriod{
								{Start: 0, End: 50000},
								{Start: 150000, End: 250000},
							},
						},
					},
				},
			},
			TargetType: ResourceTypePod,
			Start:      0,
			End:        200000,
			StepMs:     100000,
			Expected: []cmdb.MatchersWithTimestamp{
				{Timestamp: 0, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
				{Timestamp: 200000, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
			},
		},
		{
			Name: "MultipleNodes_DifferentActiveRanges",
			Graphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"pod:nginx-1": {
							ResourceID:   "pod:nginx-1",
							ResourceType: ResourceTypePod,
							Labels:       map[string]string{"pod": "nginx-1"},
							RawPeriods:   []*VisiblePeriod{{Start: 0, End: 100000}},
						},
						"pod:nginx-2": {
							ResourceID:   "pod:nginx-2",
							ResourceType: ResourceTypePod,
							Labels:       map[string]string{"pod": "nginx-2"},
							RawPeriods:   []*VisiblePeriod{{Start: 100000, End: 200000}},
						},
						"pod:nginx-3": {
							ResourceID:   "pod:nginx-3",
							ResourceType: ResourceTypePod,
							Labels:       map[string]string{"pod": "nginx-3"},
							RawPeriods:   []*VisiblePeriod{{Start: 200000, End: 300000}},
						},
					},
				},
			},
			TargetType: ResourceTypePod,
			Start:      0,
			End:        300000,
			StepMs:     100000,
			Expected: []cmdb.MatchersWithTimestamp{
				{Timestamp: 0, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
				{Timestamp: 100000, Matchers: cmdb.Matchers{{"pod": "nginx-1"}, {"pod": "nginx-2"}}},
				{Timestamp: 200000, Matchers: cmdb.Matchers{{"pod": "nginx-2"}, {"pod": "nginx-3"}}},
				{Timestamp: 300000, Matchers: cmdb.Matchers{{"pod": "nginx-3"}}},
			},
		},
		{
			Name: "MultipleGraphs_SameNode",
			Graphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"pod:nginx-1": {
							ResourceID:   "pod:nginx-1",
							ResourceType: ResourceTypePod,
							Labels:       map[string]string{"pod": "nginx-1"},
							RawPeriods:   []*VisiblePeriod{{Start: 0, End: 100000}},
						},
					},
				},
				{
					Nodes: map[string]*NodeLiveness{
						"pod:nginx-1": {
							ResourceID:   "pod:nginx-1",
							ResourceType: ResourceTypePod,
							Labels:       map[string]string{"pod": "nginx-1"},
							RawPeriods:   []*VisiblePeriod{{Start: 200000, End: 300000}},
						},
					},
				},
			},
			TargetType: ResourceTypePod,
			Start:      0,
			End:        300000,
			StepMs:     100000,
			Expected: []cmdb.MatchersWithTimestamp{
				{Timestamp: 0, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
				{Timestamp: 100000, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
				{Timestamp: 200000, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
				{Timestamp: 300000, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
			},
		},
		{
			Name: "BoundaryCondition_ExactMatch",
			Graphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"pod:nginx-1": {
							ResourceID:   "pod:nginx-1",
							ResourceType: ResourceTypePod,
							Labels:       map[string]string{"pod": "nginx-1"},
							RawPeriods:   []*VisiblePeriod{{Start: 100000, End: 100000}},
						},
					},
				},
			},
			TargetType: ResourceTypePod,
			Start:      0,
			End:        200000,
			StepMs:     100000,
			Expected: []cmdb.MatchersWithTimestamp{
				{Timestamp: 100000, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
			},
		},
		{
			Name: "SingleTimestamp",
			Graphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"pod:nginx-1": {
							ResourceID:   "pod:nginx-1",
							ResourceType: ResourceTypePod,
							Labels:       map[string]string{"pod": "nginx-1"},
							RawPeriods:   []*VisiblePeriod{{Start: 0, End: 100000}},
						},
					},
				},
			},
			TargetType: ResourceTypePod,
			Start:      50000,
			End:        50000,
			StepMs:     100000,
			Expected: []cmdb.MatchersWithTimestamp{
				{Timestamp: 50000, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
			},
		},
		{
			Name: "MixedResourceTypes",
			Graphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"pod:nginx-1": {
							ResourceID:   "pod:nginx-1",
							ResourceType: ResourceTypePod,
							Labels:       map[string]string{"pod": "nginx-1"},
							RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200000}},
						},
						"node:node-1": {
							ResourceID:   "node:node-1",
							ResourceType: ResourceTypeNode,
							Labels:       map[string]string{"node": "node-1"},
							RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200000}},
						},
						"service:svc-1": {
							ResourceID:   "service:svc-1",
							ResourceType: ResourceTypeService,
							Labels:       map[string]string{"service": "svc-1"},
							RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200000}},
						},
					},
				},
			},
			TargetType: ResourceTypePod,
			Start:      0,
			End:        100000,
			StepMs:     100000,
			Expected: []cmdb.MatchersWithTimestamp{
				{Timestamp: 0, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
				{Timestamp: 100000, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
			},
		},
		{
			Name: "EmptyLabels",
			Graphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"pod:nginx-1": {
							ResourceID:   "pod:nginx-1",
							ResourceType: ResourceTypePod,
							Labels:       map[string]string{},
							RawPeriods:   []*VisiblePeriod{{Start: 0, End: 100000}},
						},
					},
				},
			},
			TargetType: ResourceTypePod,
			Start:      0,
			End:        0,
			StepMs:     100000,
			Expected: []cmdb.MatchersWithTimestamp{
				{Timestamp: 0, Matchers: cmdb.Matchers{{}}},
			},
		},
		{
			Name: "EmptyRawPeriods",
			Graphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"pod:nginx-1": {
							ResourceID:   "pod:nginx-1",
							ResourceType: ResourceTypePod,
							Labels:       map[string]string{"pod": "nginx-1"},
							RawPeriods:   nil,
						},
					},
				},
			},
			TargetType: ResourceTypePod,
			Start:      0,
			End:        100000,
			StepMs:     100000,
			Expected:   nil,
		},
		{
			Name: "LargeStep_SkipTimestamps",
			Graphs: []*LivenessGraph{
				{
					Nodes: map[string]*NodeLiveness{
						"pod:nginx-1": {
							ResourceID:   "pod:nginx-1",
							ResourceType: ResourceTypePod,
							Labels:       map[string]string{"pod": "nginx-1"},
							RawPeriods:   []*VisiblePeriod{{Start: 0, End: 1000000}},
						},
					},
				},
			},
			TargetType: ResourceTypePod,
			Start:      0,
			End:        500000,
			StepMs:     500000,
			Expected: []cmdb.MatchersWithTimestamp{
				{Timestamp: 0, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
				{Timestamp: 500000, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			result := buildTargetMatchersTimeSeries(tc.Graphs, tc.TargetType, tc.Start, tc.End, tc.StepMs)

			if tc.Expected == nil {
				assert.Nil(t, result, "expected nil result")
				return
			}

			require.NotNil(t, result, "result should not be nil")
			require.Equal(t, len(tc.Expected), len(result), "time series length mismatch")

			for i, expected := range tc.Expected {
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
		})
	}
}

func TestIsActiveAt(t *testing.T) {
	testCases := []struct {
		Name     string
		Periods  []*VisiblePeriod
		Ts       int64
		Expected bool
	}{
		{
			Name:     "NilPeriods",
			Periods:  nil,
			Ts:       100,
			Expected: false,
		},
		{
			Name:     "EmptyPeriods",
			Periods:  []*VisiblePeriod{},
			Ts:       100,
			Expected: false,
		},
		{
			Name:     "WithinSinglePeriod",
			Periods:  []*VisiblePeriod{{Start: 0, End: 200}},
			Ts:       100,
			Expected: true,
		},
		{
			Name:     "AtPeriodStart",
			Periods:  []*VisiblePeriod{{Start: 100, End: 200}},
			Ts:       100,
			Expected: true,
		},
		{
			Name:     "AtPeriodEnd",
			Periods:  []*VisiblePeriod{{Start: 100, End: 200}},
			Ts:       200,
			Expected: true,
		},
		{
			Name:     "BeforePeriod",
			Periods:  []*VisiblePeriod{{Start: 100, End: 200}},
			Ts:       50,
			Expected: false,
		},
		{
			Name:     "AfterPeriod",
			Periods:  []*VisiblePeriod{{Start: 100, End: 200}},
			Ts:       250,
			Expected: false,
		},
		{
			Name: "WithinSecondPeriod",
			Periods: []*VisiblePeriod{
				{Start: 0, End: 100},
				{Start: 200, End: 300},
			},
			Ts:       250,
			Expected: true,
		},
		{
			Name: "BetweenPeriods",
			Periods: []*VisiblePeriod{
				{Start: 0, End: 100},
				{Start: 200, End: 300},
			},
			Ts:       150,
			Expected: false,
		},
		{
			Name:     "ExactPointPeriod",
			Periods:  []*VisiblePeriod{{Start: 100, End: 100}},
			Ts:       100,
			Expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			result := isActiveAt(tc.Periods, tc.Ts)
			assert.Equal(t, tc.Expected, result)
		})
	}
}
