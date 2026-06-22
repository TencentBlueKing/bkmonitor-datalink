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
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

type mockGraphQueryExecutor struct {
	graphs []*LivenessGraph
	err    error
	sql    string
	start  int64
	end    int64
}

func (m *mockGraphQueryExecutor) Execute(ctx context.Context, sql string, start, end int64) ([]*LivenessGraph, error) {
	m.sql = sql
	m.start = start
	m.end = end
	if m.err != nil {
		return nil, m.err
	}
	for _, g := range m.graphs {
		g.QueryStart = start
		g.QueryEnd = end
	}
	return m.graphs, nil
}

type mockBindingGraphQueryExecutor struct {
	mockGraphQueryExecutor
}

func (m *mockBindingGraphQueryExecutor) ExecuteWithBinding(ctx context.Context, spaceUID string, binding BindingInfo, dsl string, start, end int64) ([]*LivenessGraph, error) {
	return m.Execute(ctx, dsl, start, end)
}

type mockBKBaseCurl struct {
	response BKBaseResponse
}

func (m *mockBKBaseCurl) WithDecoder(decoder func(ctx context.Context, reader io.Reader, resp any) (int, error)) {
}

func (m *mockBKBaseCurl) Request(ctx context.Context, method string, opt curl.Options, res any) (int, error) {
	if response, ok := res.(*BKBaseResponse); ok {
		*response = m.response
	}
	return 0, nil
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
							RawPeriods: []*VisiblePeriod{{Start: 0, End: 1000000}},
						},
						"node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩": {
							ResourceID:   "node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩",
							ResourceType: ResourceTypeNode,
							Labels: map[string]string{
								"bcs_cluster_id": "BCS-K8S-00001",
								"node":           "node-1",
							},
							RawPeriods: []*VisiblePeriod{{Start: 0, End: 1000000}},
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

func TestQueryResourceMatcherRangeRejectsNonPositiveStep(t *testing.T) {
	ctx := context.Background()
	model, err := NewModel(ctx, &mockGraphQueryExecutor{})
	require.NoError(t, err)

	_, _, _, _, _, err = model.QueryResourceMatcherRange(
		ctx,
		"10m",
		"test-space",
		"0s",
		"0",
		"300",
		"pod",
		"node",
		cmdb.Matcher{"bcs_cluster_id": "BCS-K8S-00001", "node": "node-1"},
		nil,
		false,
		nil,
	)
	require.Error(t, err)
	assert.ErrorContains(t, err, "step must be greater than 0")
}

func TestQueryLivenessGraphRejectsUnknownResourceType(t *testing.T) {
	ctx := context.Background()
	model, err := NewModel(ctx, &mockGraphQueryExecutor{})
	require.NoError(t, err)

	_, _, _, err = model.QueryLivenessGraph(ctx, &QueryRequest{
		Timestamp:  300000,
		SourceType: "node; DELETE node",
		SourceInfo: map[string]string{
			"node": "node-1",
		},
		TargetType: ResourceTypePod,
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "unknown resource type")
}

func TestQueryLivenessGraphUsesInjectedSchemaProvider(t *testing.T) {
	ctx := context.Background()
	provider := relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourcePrimaryKeys: map[string][]string{
			"custom_source": {"custom_id"},
			"custom_target": {"target_id"},
		},
		RelationSchemas: []relation.RelationSchema{
			{
				RelationName: "custom_source_to_custom_target",
				Category:     relation.RelationCategoryDynamic,
				FromType:     "custom_source",
				ToType:       "custom_target",
			},
		},
	})
	InitSchemaProvider(provider)
	t.Cleanup(func() { InitSchemaProvider(nil) })

	executor := &mockGraphQueryExecutor{}
	model, err := NewModel(ctx, executor)
	require.NoError(t, err)

	_, paths, _, err := model.QueryLivenessGraph(ctx, &QueryRequest{
		Timestamp:  300000,
		SourceType: "custom_source",
		SourceInfo: map[string]string{
			"custom_id": "source-1",
		},
		TargetType: "custom_target",
		MaxHops:    1,
	})

	require.NoError(t, err)
	assert.Equal(t, []cmdb.PathV2{{Steps: []cmdb.PathStepV2{
		{ResourceType: "custom_source"},
		{
			ResourceType: "custom_target",
			RelationType: "custom_source_to_custom_target",
			Category:     "dynamic",
			Direction:    "outbound",
		},
	}}}, paths)
	assert.Contains(t, executor.sql, "entity_data: { custom_id: custom_id }")
	assert.Contains(t, executor.sql, "entity_data: { target_id: target_id.target_id }")
}

func TestQueryLivenessGraphProjectsTargetInfoFields(t *testing.T) {
	ctx := context.Background()
	provider := NewSchemaProviderFromRelation(&namespaceRelationProvider{
		resources: map[string]map[string]*relation.ResourceDefinition{
			relation.NamespaceAll: {
				"custom_source": resourceDefinition("custom_source", "custom_id"),
				"custom_target": resourceDefinitionWithFields("custom_target", []string{"target_id"}, "version"),
			},
		},
		relations: map[string][]*relation.RelationDefinition{
			relation.NamespaceAll: {
				{Name: "custom_source_to_custom_target", FromResource: "custom_source", ToResource: "custom_target", Category: "dynamic", IsDirectional: true},
			},
		},
	})
	executor := &mockGraphQueryExecutor{}
	model, err := NewModel(ctx, executor)
	require.NoError(t, err)
	model.SetSchemaProvider(provider)

	_, _, _, err = model.QueryLivenessGraph(ctx, &QueryRequest{
		Timestamp:  300000,
		SourceType: "custom_source",
		SourceInfo: map[string]string{
			"custom_id": "source-1",
		},
		TargetType:     "custom_target",
		TargetInfoShow: true,
		MaxHops:        1,
	})

	require.NoError(t, err)
	assert.Contains(t, executor.sql, "entity_data: { target_id: target_id.target_id, version: target_id.version }")
}

func TestInitSchemaProviderRefreshesDefaultModel(t *testing.T) {
	ctx := context.Background()
	InitSchemaProvider(nil)

	executor := &mockGraphQueryExecutor{}
	model, err := NewModel(ctx, executor)
	require.NoError(t, err)

	modelMutex.Lock()
	previousModel := defaultModel
	defaultModel = model
	modelMutex.Unlock()
	t.Cleanup(func() {
		modelMutex.Lock()
		defaultModel = previousModel
		modelMutex.Unlock()
		InitSchemaProvider(nil)
	})

	provider := relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourcePrimaryKeys: map[string][]string{
			"custom_source": {"custom_id"},
			"custom_target": {"target_id"},
		},
		RelationSchemas: []relation.RelationSchema{
			{
				RelationName: "custom_source_to_custom_target",
				Category:     relation.RelationCategoryDynamic,
				FromType:     "custom_source",
				ToType:       "custom_target",
			},
		},
	})
	InitSchemaProvider(provider)

	_, paths, _, err := model.QueryLivenessGraph(ctx, &QueryRequest{
		Timestamp:  300000,
		SourceType: "custom_source",
		SourceInfo: map[string]string{
			"custom_id": "source-1",
		},
		TargetType: "custom_target",
		MaxHops:    1,
	})

	require.NoError(t, err)
	assert.Equal(t, []cmdb.PathV2{{Steps: []cmdb.PathStepV2{
		{ResourceType: "custom_source"},
		{
			ResourceType: "custom_target",
			RelationType: "custom_source_to_custom_target",
			Category:     "dynamic",
			Direction:    "outbound",
		},
	}}}, paths)
	assert.Contains(t, executor.sql, "FROM custom_source_to_custom_target")
}

func TestQueryLivenessGraphDefaultsEmptyTargetTypeToSourceType(t *testing.T) {
	ctx := context.Background()
	provider := relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourcePrimaryKeys: map[string][]string{
			"custom_source": {"custom_id"},
		},
		RelationSchemas: []relation.RelationSchema{
			{
				RelationName: "custom_source_to_custom_source",
				Category:     relation.RelationCategoryDynamic,
				FromType:     "custom_source",
				ToType:       "custom_source",
			},
		},
	})
	InitSchemaProvider(provider)
	t.Cleanup(func() { InitSchemaProvider(nil) })

	executor := &mockGraphQueryExecutor{}
	model, err := NewModel(ctx, executor)
	require.NoError(t, err)

	req := &QueryRequest{
		Timestamp:  300000,
		SourceType: "custom_source",
		SourceInfo: map[string]string{
			"custom_id": "source-1",
		},
		MaxHops: 1,
	}
	_, paths, _, err := model.QueryLivenessGraph(ctx, req)

	require.NoError(t, err)
	assert.Equal(t, ResourceType("custom_source"), req.TargetType)
	assert.Equal(t, []cmdb.PathV2{{Steps: []cmdb.PathStepV2{{ResourceType: "custom_source"}}}}, paths)
	assert.NotContains(t, executor.sql, "custom_source_to_custom_source")
}

func TestDefaultStaticSchemaProviderMatchesServiceStaticProvider(t *testing.T) {
	buildSQL := func() string {
		return NewSurrealQueryBuilder(&QueryRequest{
			Timestamp:  300000,
			SourceType: ResourceTypeNode,
			SourceInfo: map[string]string{
				"bcs_cluster_id": "BCS-K8S-00001",
				"node":           "node-1",
			},
			TargetType: ResourceTypePod,
			MaxHops:    1,
		}).Build()
	}

	InitSchemaProvider(nil)
	defaultSQL := buildSQL()

	InitSchemaProvider(relation.NewDefaultStaticSchemaProvider())
	t.Cleanup(func() { InitSchemaProvider(nil) })
	serviceSQL := buildSQL()

	assert.Equal(t, defaultSQL, serviceSQL)
	assert.Contains(t, serviceSQL, "FROM node_with_pod")
	assert.NotContains(t, serviceSQL, "FROM node_pod")
}

func TestQueryLivenessGraphUsesSpaceUIDSchemaNamespace(t *testing.T) {
	ctx := context.Background()
	provider := NewSchemaProviderFromRelation(&namespaceRelationProvider{
		resources: map[string]map[string]*relation.ResourceDefinition{
			relation.NamespaceAll: {
				"pod":  resourceDefinition("pod", "global_pod"),
				"node": resourceDefinition("node", "global_node"),
			},
			"bkcc__2": {
				"pod": resourceDefinition("pod", "biz_pod"),
			},
		},
		relations: map[string][]*relation.RelationDefinition{
			relation.NamespaceAll: {
				{Name: "global_pod_node", FromResource: "pod", ToResource: "node", Category: "static"},
			},
			"bkcc__2": {
				{Name: "biz_pod_node", FromResource: "pod", ToResource: "node", Category: "static"},
			},
		},
	})

	executor := &mockGraphQueryExecutor{}
	model, err := NewModel(ctx, executor)
	require.NoError(t, err)
	model.SetSchemaProvider(provider)

	_, paths, _, err := model.QueryLivenessGraph(ctx, &QueryRequest{
		SpaceUID:   "bkcc__2",
		Timestamp:  300000,
		SourceType: ResourceTypePod,
		SourceInfo: map[string]string{
			"biz_pod": "pod-1",
		},
		TargetType: ResourceTypeNode,
		MaxHops:    1,
	})

	require.NoError(t, err)
	assert.Equal(t, []cmdb.PathV2{{Steps: []cmdb.PathStepV2{
		{ResourceType: "pod"},
		{
			ResourceType: "node",
			RelationType: "biz_pod_node",
			Category:     "static",
			Direction:    "outbound",
		},
	}}}, paths)
	assert.Contains(t, executor.sql, "FROM biz_pod_node")
	assert.Contains(t, executor.sql, "entity_data: { biz_pod: biz_pod }")
	assert.Contains(t, executor.sql, "entity_data: { global_node: target_id.global_node }")
	assert.NotContains(t, executor.sql, "global_pod_node")
}

func TestInferSourceTypePrefersMostSpecificPrimaryKeys(t *testing.T) {
	provider := NewSchemaProviderFromRelation(&namespaceRelationProvider{
		resources: map[string]map[string]*relation.ResourceDefinition{
			relation.NamespaceAll: {
				"pod":       resourceDefinition("pod", "bcs_cluster_id", "namespace", "pod"),
				"container": resourceDefinition("container", "bcs_cluster_id", "namespace", "pod", "container"),
			},
		},
		relations: map[string][]*relation.RelationDefinition{
			relation.NamespaceAll: {
				{Name: "container_pod", FromResource: "container", ToResource: "pod", Category: "static"},
			},
		},
	})

	sourceType, err := inferSourceTypeFromInfo(&QueryRequest{
		SourceInfo: map[string]string{
			"bcs_cluster_id": "BCS-K8S-00001",
			"namespace":      "default",
			"pod":            "nginx-1",
			"container":      "main",
		},
	}, provider)

	require.NoError(t, err)
	assert.Equal(t, ResourceType("container"), sourceType)
}

func TestComputeMaxHopsKeepsRoomForPartialPathResource(t *testing.T) {
	assert.Equal(t, DefaultMaxHops, computeMaxHops(nil))
	assert.Equal(t, DefaultMaxHops+1, computeMaxHops([]cmdb.Resource{"pod"}))
	assert.Equal(t, MaxAllowedHops, computeMaxHops([]cmdb.Resource{"a", "b", "c", "d", "e"}))
}

func TestExecuteGraphQueryRequiresSpaceUIDForBindingExecutor(t *testing.T) {
	model := &Model{
		executor: &mockBindingGraphQueryExecutor{},
		resolver: &BindingResolver{},
	}

	_, err := model.executeGraphQuery(context.Background(), &QueryRequest{}, "RETURN []", 0, 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "space_uid is required")
}

func TestBindingCacheKeyIncludesTenant(t *testing.T) {
	resolver := &BindingResolver{cache: make(map[string]*bindingCacheEntry)}
	first := &BindingInfo{Name: "tenant-a-binding"}
	second := &BindingInfo{Name: "tenant-b-binding"}

	resolver.storeCache(bindingCacheKey("tenant-a", "2"), first)
	resolver.storeCache(bindingCacheKey("tenant-b", "2"), second)

	assert.Equal(t, first, resolver.lookupCache(bindingCacheKey("tenant-a", "2")))
	assert.Equal(t, second, resolver.lookupCache(bindingCacheKey("tenant-b", "2")))
	assert.Nil(t, resolver.lookupCache("2"))
}

func resourceDefinition(name string, primaryKeys ...string) *relation.ResourceDefinition {
	fields := make([]relation.FieldDefinition, 0, len(primaryKeys))
	for _, primaryKey := range primaryKeys {
		fields = append(fields, relation.FieldDefinition{Name: primaryKey, Required: true})
	}
	return &relation.ResourceDefinition{Name: name, Fields: fields}
}

func resourceDefinitionWithFields(name string, primaryKeys []string, optionalFields ...string) *relation.ResourceDefinition {
	fields := make([]relation.FieldDefinition, 0, len(primaryKeys)+len(optionalFields))
	for _, primaryKey := range primaryKeys {
		fields = append(fields, relation.FieldDefinition{Name: primaryKey, Required: true})
	}
	for _, field := range optionalFields {
		fields = append(fields, relation.FieldDefinition{Name: field})
	}
	return &relation.ResourceDefinition{Name: name, Fields: fields}
}

type namespaceRelationProvider struct {
	resources map[string]map[string]*relation.ResourceDefinition
	relations map[string][]*relation.RelationDefinition
}

func (p *namespaceRelationProvider) Name() string { return "namespace-relation-provider" }

func (p *namespaceRelationProvider) ListNamespaces() ([]string, error) {
	namespaces := make([]string, 0, len(p.resources)+len(p.relations))
	seen := map[string]struct{}{}
	for namespace := range p.resources {
		if _, ok := seen[namespace]; !ok {
			namespaces = append(namespaces, namespace)
			seen[namespace] = struct{}{}
		}
	}
	for namespace := range p.relations {
		if _, ok := seen[namespace]; !ok {
			namespaces = append(namespaces, namespace)
			seen[namespace] = struct{}{}
		}
	}
	return namespaces, nil
}

func (p *namespaceRelationProvider) GetResourceDefinition(namespace, name string) (*relation.ResourceDefinition, error) {
	if nsResources, ok := p.resources[namespace]; ok {
		if resourceDef, ok := nsResources[name]; ok {
			return resourceDef, nil
		}
	}
	return nil, relation.ErrResourceDefinitionNotFound
}

func (p *namespaceRelationProvider) ListResourceDefinitions(namespace string) ([]*relation.ResourceDefinition, error) {
	nsResources := p.resources[namespace]
	result := make([]*relation.ResourceDefinition, 0, len(nsResources))
	for _, resourceDef := range nsResources {
		result = append(result, resourceDef)
	}
	return result, nil
}

func (p *namespaceRelationProvider) ListAllResourceDefinitions() (map[string][]*relation.ResourceDefinition, error) {
	result := make(map[string][]*relation.ResourceDefinition, len(p.resources))
	for namespace := range p.resources {
		result[namespace], _ = p.ListResourceDefinitions(namespace)
	}
	return result, nil
}

func (p *namespaceRelationProvider) GetRelationDefinition(namespace, name string) (*relation.RelationDefinition, error) {
	for _, relationDef := range p.relations[namespace] {
		if relationDef.Name == name {
			return relationDef, nil
		}
	}
	return nil, relation.ErrRelationDefinitionNotFound
}

func (p *namespaceRelationProvider) ListRelationDefinitions(namespace string) ([]*relation.RelationDefinition, error) {
	return append([]*relation.RelationDefinition(nil), p.relations[namespace]...), nil
}

func (p *namespaceRelationProvider) ListAllRelationDefinitions() (map[string][]*relation.RelationDefinition, error) {
	result := make(map[string][]*relation.RelationDefinition, len(p.relations))
	for namespace, relationDefs := range p.relations {
		result[namespace] = append([]*relation.RelationDefinition(nil), relationDefs...)
	}
	return result, nil
}

func (p *namespaceRelationProvider) GetResourcePrimaryKeys(resourceType relation.ResourceType) []string {
	resourceDef, err := p.GetResourceDefinition(relation.NamespaceAll, string(resourceType))
	if err != nil {
		return nil
	}
	return resourceDef.GetPrimaryKeys()
}

func (p *namespaceRelationProvider) GetRelationSchema(relationType relation.RelationName) (*relation.RelationSchema, error) {
	relationDef, err := p.GetRelationDefinition(relation.NamespaceAll, string(relationType))
	if err != nil {
		return nil, err
	}
	schema := relation.ToRelationSchema(relationDef)
	return &schema, nil
}

func (p *namespaceRelationProvider) ListRelationSchemas() []relation.RelationSchema {
	relationDefs := p.relations[relation.NamespaceAll]
	result := make([]relation.RelationSchema, 0, len(relationDefs))
	for _, relationDef := range relationDefs {
		result = append(result, relation.ToRelationSchema(relationDef))
	}
	return result
}

func (p *namespaceRelationProvider) FindRelationByResourceTypes(namespace, fromResource, toResource string, directionType relation.DirectionType) (*relation.RelationDefinition, bool) {
	for _, relationDef := range p.relations[namespace] {
		if relationDef.FromResource == fromResource && relationDef.ToResource == toResource {
			return relationDef, true
		}
	}
	return nil, false
}

func (p *namespaceRelationProvider) Subscribe(callback relation.SchemaChangeCallback) error {
	return nil
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

func TestQueryResourceMatcherAppliesSourceExpandInfo(t *testing.T) {
	executor := &mockGraphQueryExecutor{}
	model, err := NewModel(context.Background(), executor)
	require.NoError(t, err)

	_, _, _, _, _, err = model.QueryResourceMatcher(
		context.Background(),
		"10m",
		"test-space",
		"600",
		"pod",
		"node",
		cmdb.Matcher{"bcs_cluster_id": "BCS-K8S-00001", "node": "node-1"},
		cmdb.Matcher{"namespace": "default", "unsafe.field": "ignored"},
		false,
		nil,
	)
	require.NoError(t, err)
	assert.Contains(t, executor.sql, "namespace = 'default'")
	assert.Contains(t, executor.sql, "pod_liveness_record WHERE pod_id = $parent.target_id")
	assert.NotContains(t, executor.sql, "unsafe.field")
}

func TestQueryResourceMatcherNormalizesSourceMatcherAliases(t *testing.T) {
	executor := &mockGraphQueryExecutor{}
	model, err := NewModel(context.Background(), executor)
	require.NoError(t, err)

	source, matcher, _, _, _, err := model.QueryResourceMatcher(
		context.Background(),
		"10m",
		"test-space",
		"600",
		"system",
		"pod",
		cmdb.Matcher{"bcs_cluster_id": "BCS-K8S-00001", "namespace": "default", "pod_name": "nginx-1"},
		nil,
		false,
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, cmdb.Resource("pod"), source)
	assert.Equal(t, cmdb.Matcher{"bcs_cluster_id": "BCS-K8S-00001", "namespace": "default", "pod": "nginx-1"}, matcher)
	assert.Contains(t, executor.sql, "pod = 'nginx-1'")
	assert.NotContains(t, executor.sql, "pod_name")
}

func TestQueryResourceMatcherInfersOmittedSourceType(t *testing.T) {
	executor := &mockGraphQueryExecutor{}
	model, err := NewModel(context.Background(), executor)
	require.NoError(t, err)

	source, matcher, _, _, _, err := model.QueryResourceMatcher(
		context.Background(),
		"10m",
		"test-space",
		"600",
		"pod",
		"",
		cmdb.Matcher{"bk_data_id": "1001"},
		nil,
		false,
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, cmdb.Resource("datasource"), source)
	assert.Equal(t, cmdb.Matcher{"bk_data_id": "1001"}, matcher)
	assert.Contains(t, executor.sql, "FROM datasource")
	assert.Contains(t, executor.sql, "bk_data_id = '1001'")
}

func TestQueryResourceMatcherRangeUsesFullRangeLookback(t *testing.T) {
	executor := &mockGraphQueryExecutor{}
	model, err := NewModel(context.Background(), executor)
	require.NoError(t, err)

	_, _, _, _, _, err = model.QueryResourceMatcherRange(
		context.Background(),
		"10m",
		"test-space",
		"1m",
		"0",
		"3600",
		"pod",
		"datasource",
		cmdb.Matcher{"bk_data_id": "1001"},
		nil,
		false,
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, int64(0), executor.start)
	assert.Equal(t, int64(3600000), executor.end)
}

func TestQueryResourceMatcherFiltersTargetsByPathResource(t *testing.T) {
	graph := NewLivenessGraph(0, 200)
	root := &NodeLiveness{ResourceID: "node:1", ResourceType: ResourceTypeNode, Labels: map[string]string{"node": "node-1"}}
	system := &NodeLiveness{ResourceID: "system:1", ResourceType: ResourceTypeSystem, Labels: map[string]string{"system": "system-1"}}
	podViaSystem := &NodeLiveness{
		ResourceID:   "pod:via-system",
		ResourceType: ResourceTypePod,
		Labels:       map[string]string{"pod": "via-system"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	podDirect := &NodeLiveness{
		ResourceID:   "pod:direct",
		ResourceType: ResourceTypePod,
		Labels:       map[string]string{"pod": "direct"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	graph.AddNode(root)
	graph.AddNode(system)
	graph.AddNode(podViaSystem)
	graph.AddNode(podDirect)
	graph.AddEdge(&EdgeLiveness{RelationID: "node-system", FromID: root.ResourceID, ToID: system.ResourceID})
	graph.AddEdge(&EdgeLiveness{RelationID: "system-pod", FromID: system.ResourceID, ToID: podViaSystem.ResourceID})
	graph.AddEdge(&EdgeLiveness{RelationID: "node-pod", FromID: root.ResourceID, ToID: podDirect.ResourceID})

	matchers := extractMatchersFromGraphs([]*LivenessGraph{graph}, ResourceTypePod, []ResourceType{ResourceTypeSystem})

	assert.Equal(t, cmdb.Matchers{{"pod": "via-system"}}, matchers)
}

func TestExtractMatchersFiltersInactiveInstantTargets(t *testing.T) {
	graph := NewLivenessGraph(0, 200)
	root := &NodeLiveness{
		ResourceID:   "node:1",
		ResourceType: ResourceTypeNode,
		Labels:       map[string]string{"node": "node-1"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	inactivePod := &NodeLiveness{
		ResourceID:   "pod:inactive",
		ResourceType: ResourceTypePod,
		Labels:       map[string]string{"pod": "inactive"},
	}
	graph.AddNode(root)
	graph.AddNode(inactivePod)
	graph.AddEdge(&EdgeLiveness{RelationID: "node-pod", FromID: root.ResourceID, ToID: inactivePod.ResourceID})

	matchers := extractMatchersFromGraphs([]*LivenessGraph{graph}, ResourceTypePod, nil)

	assert.Nil(t, matchers)
}

func TestExtractMatchersSkipsRootForExplicitSelfTarget(t *testing.T) {
	graph := NewLivenessGraph(0, 200)
	graph.AddNode(&NodeLiveness{
		ResourceID:   "system:1",
		ResourceType: ResourceTypeSystem,
		Labels:       map[string]string{"bk_target_ip": "127.0.0.1"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	})

	implicitTarget := extractMatchersFromGraphsWithOptions(
		[]*LivenessGraph{graph},
		ResourceTypeSystem,
		nil,
		GetSchemaProvider(),
		"",
		false,
		true,
	)
	explicitSelfTarget := extractMatchersFromGraphsWithOptions(
		[]*LivenessGraph{graph},
		ResourceTypeSystem,
		nil,
		GetSchemaProvider(),
		"",
		false,
		false,
	)

	assert.Equal(t, cmdb.Matchers{{"bk_target_ip": "127.0.0.1"}}, implicitTarget)
	assert.Nil(t, explicitSelfTarget)
}

func TestExtractMatchersRespectsTargetInfoShow(t *testing.T) {
	provider := NewSchemaProviderFromRelation(&namespaceRelationProvider{
		resources: map[string]map[string]*relation.ResourceDefinition{
			relation.NamespaceAll: {
				"pod": resourceDefinitionWithFields("pod", []string{"bcs_cluster_id", "namespace", "pod"}, "version"),
			},
		},
	})
	graph := NewLivenessGraph(0, 200)
	root := &NodeLiveness{
		ResourceID:   "node:1",
		ResourceType: ResourceTypeNode,
		Labels:       map[string]string{"node": "node-1"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	pod := &NodeLiveness{
		ResourceID:   "pod:1",
		ResourceType: ResourceTypePod,
		Labels:       map[string]string{"bcs_cluster_id": "BCS-K8S-00001", "namespace": "default", "pod": "nginx-1", "version": "v1"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	graph.AddNode(root)
	graph.AddNode(pod)
	graph.AddEdge(&EdgeLiveness{RelationID: "node-pod", FromID: root.ResourceID, ToID: pod.ResourceID})

	withoutInfo := extractMatchersFromGraphsWithOptions(
		[]*LivenessGraph{graph},
		ResourceTypePod,
		nil,
		provider,
		"",
		false,
		true,
	)
	withInfo := extractMatchersFromGraphsWithOptions(
		[]*LivenessGraph{graph},
		ResourceTypePod,
		nil,
		provider,
		"",
		true,
		true,
	)

	assert.Equal(t, cmdb.Matchers{{"bcs_cluster_id": "BCS-K8S-00001", "namespace": "default", "pod": "nginx-1"}}, withoutInfo)
	assert.Equal(t, cmdb.Matchers{{"bcs_cluster_id": "BCS-K8S-00001", "namespace": "default", "pod": "nginx-1", "version": "v1"}}, withInfo)
}

func TestBuildTargetMatchersTimeSeriesRequiresEdgeLiveness(t *testing.T) {
	graph := NewLivenessGraph(0, 200)
	root := &NodeLiveness{
		ResourceID:   "node:1",
		ResourceType: ResourceTypeNode,
		Labels:       map[string]string{"node": "node-1"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	pod := &NodeLiveness{
		ResourceID:   "pod:1",
		ResourceType: ResourceTypePod,
		Labels:       map[string]string{"pod": "nginx-1"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	graph.AddNode(root)
	graph.AddNode(pod)
	graph.AddEdge(&EdgeLiveness{
		RelationID: "node-pod",
		FromID:     root.ResourceID,
		ToID:       pod.ResourceID,
		RawPeriods: []*VisiblePeriod{{Start: 100, End: 100}},
	})

	result := buildTargetMatchersTimeSeries([]*LivenessGraph{graph}, ResourceTypePod, nil, 0, 200, 100)

	assert.Equal(t, []cmdb.MatchersWithTimestamp{
		{Timestamp: 100, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
	}, result)
}

func TestBuildTargetMatchersTimeSeriesRequiresEveryNodeLiveness(t *testing.T) {
	graph := NewLivenessGraph(0, 200)
	root := &NodeLiveness{
		ResourceID:   "node:1",
		ResourceType: ResourceTypeNode,
		Labels:       map[string]string{"node": "node-1"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 50}},
	}
	pod := &NodeLiveness{
		ResourceID:   "pod:1",
		ResourceType: ResourceTypePod,
		Labels:       map[string]string{"pod": "nginx-1"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	graph.AddNode(root)
	graph.AddNode(pod)
	graph.AddEdge(&EdgeLiveness{
		RelationID: "node-pod",
		FromID:     root.ResourceID,
		ToID:       pod.ResourceID,
		RawPeriods: []*VisiblePeriod{{Start: 0, End: 200}},
	})

	result := buildTargetMatchersTimeSeries([]*LivenessGraph{graph}, ResourceTypePod, nil, 0, 200, 100)

	assert.Equal(t, []cmdb.MatchersWithTimestamp{
		{Timestamp: 0, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
	}, result)
}

func TestBKBaseSurrealDBClientConvertsListForParser(t *testing.T) {
	client := &BKBaseSurrealDBClient{
		curl: &mockBKBaseCurl{response: BKBaseResponse{
			Result: true,
			Data: &BKBaseData{List: []map[string]any{
				{
					ResponseFieldResult: map[string]any{
						ResponseFieldRoot: map[string]any{
							ResponseFieldEntityType: string(ResourceTypeNode),
							ResponseFieldEntityID:   "node:1",
							ResponseFieldEntityData: map[string]any{"node": "node-1"},
							ResponseFieldLiveness:   []any{},
						},
					},
				},
			}},
		}},
	}

	graphs, err := client.Execute(context.Background(), "SELECT * FROM node", 0, 100)

	require.NoError(t, err)
	require.Len(t, graphs, 1)
	assert.Equal(t, "node:1", graphs[0].GetNode("node:1").ResourceID)
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
			result := buildTargetMatchersTimeSeries(tc.Graphs, tc.TargetType, nil, tc.Start, tc.End, tc.StepMs)

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
