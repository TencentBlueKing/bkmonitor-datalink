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
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/bkapi"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

type mockGraphQueryExecutor struct {
	graphs []*LivenessGraph
	err    error
	sql    string
	sqls   []string
	start  int64
	end    int64
	mu     sync.Mutex
}

func (m *mockGraphQueryExecutor) Execute(ctx context.Context, sql string, start, end int64) ([]*LivenessGraph, error) {
	m.mu.Lock()
	m.sql = sql
	m.sqls = append(m.sqls, sql)
	m.start = start
	m.end = end
	m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	for _, g := range m.graphs {
		g.QueryStart = start
		g.QueryEnd = end
	}
	return m.graphs, nil
}

type graphQueryResponse struct {
	graphs []*LivenessGraph
	err    error
}

type recordingGraphQueryExecutor struct {
	responses      []graphQueryResponse
	responseForSQL func(sql string) graphQueryResponse
	sqls           []string
	mu             sync.Mutex
}

func (m *recordingGraphQueryExecutor) Execute(ctx context.Context, sql string, start, end int64) ([]*LivenessGraph, error) {
	m.mu.Lock()
	m.sqls = append(m.sqls, sql)
	idx := len(m.sqls) - 1
	m.mu.Unlock()

	var resp graphQueryResponse
	if m.responseForSQL != nil {
		resp = m.responseForSQL(sql)
	} else if idx < len(m.responses) {
		resp = m.responses[idx]
	} else {
		return nil, nil
	}
	if resp.err != nil {
		return nil, resp.err
	}
	for _, g := range resp.graphs {
		g.QueryStart = start
		g.QueryEnd = end
	}
	return resp.graphs, nil
}

type mockBindingGraphQueryExecutor struct {
	mockGraphQueryExecutor
}

func (m *mockBindingGraphQueryExecutor) ExecuteWithBinding(ctx context.Context, spaceUID string, binding BindingInfo, dsl string, start, end int64) ([]*LivenessGraph, error) {
	return m.Execute(ctx, dsl, start, end)
}

type mockBKBaseCurl struct {
	response BKBaseResponse
	method   string
	options  curl.Options
}

func (m *mockBKBaseCurl) WithDecoder(decoder func(ctx context.Context, reader io.Reader, resp any) (int, error)) {
}

func (m *mockBKBaseCurl) Request(ctx context.Context, method string, opt curl.Options, res any) (int, error) {
	m.method = method
	m.options = opt
	if response, ok := res.(*BKBaseResponse); ok {
		*response = m.response
	}
	return 0, nil
}

func extractMatchersFromGraphs(
	graphs []*LivenessGraph,
	targetType ResourceType,
	pathResource []ResourceType,
) cmdb.Matchers {
	return extractMatchersFromGraphsWithOptions(
		graphs,
		targetType,
		pathResource,
		GetSchemaProvider(),
		"",
		true,
		true,
	)
}

func buildTargetMatchersTimeSeries(
	graphs []*LivenessGraph,
	targetType ResourceType,
	pathResource []ResourceType,
	start, end, stepMs int64,
) []cmdb.MatchersWithTimestamp {
	return buildTargetMatchersTimeSeriesWithOptions(
		graphs,
		targetType,
		pathResource,
		start,
		end,
		stepMs,
		GetSchemaProvider(),
		"",
		true,
		true,
	)
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
	ExpectedTarget     cmdb.Resource
	ExpectedMatchers   cmdb.Matchers                // instant 查询用
	ExpectedTimeSeries []cmdb.MatchersWithTimestamp // range 查询用：完整时间序列
	ExpectedError      bool
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

func TestQueryResourceMatcherRangeRejectsReversedWindow(t *testing.T) {
	ctx := context.Background()
	model, err := NewModel(ctx, &mockGraphQueryExecutor{})
	require.NoError(t, err)

	_, _, _, _, _, err = model.QueryResourceMatcherRange(
		ctx,
		"10m",
		"test-space",
		"1m",
		"300",
		"0",
		"pod",
		"node",
		cmdb.Matcher{"bcs_cluster_id": "BCS-K8S-00001", "node": "node-1"},
		nil,
		false,
		nil,
	)
	require.Error(t, err)
	assert.ErrorContains(t, err, "start_time must be less than or equal to end_time")
}

func TestQueryResourceMatcherHonorsExplicitZeroLookback(t *testing.T) {
	ctx := context.Background()
	executor := &mockGraphQueryExecutor{}
	model, err := NewModel(ctx, executor)
	require.NoError(t, err)

	_, _, _, _, _, err = model.QueryResourceMatcher(
		ctx,
		"0s",
		"test-space",
		"300",
		"pod",
		"node",
		cmdb.Matcher{"bcs_cluster_id": "BCS-K8S-00001", "node": "node-1"},
		nil,
		false,
		nil,
	)

	require.NoError(t, err)
	assert.Equal(t, int64(300000), executor.start)
	assert.Equal(t, int64(300000), executor.end)
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

func TestQueryLivenessGraphRejectsUnknownSourceInfoField(t *testing.T) {
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
	model, err := NewModel(ctx, &mockGraphQueryExecutor{})
	require.NoError(t, err)
	model.SetSchemaProvider(NewSchemaProviderFromRelation(provider))

	_, _, _, err = model.QueryLivenessGraph(ctx, &QueryRequest{
		Timestamp:  300000,
		SourceType: "custom_source",
		SourceInfo: map[string]string{
			"custom_id":   "source-1",
			"custom_typo": "typo",
		},
		TargetType: "custom_target",
		MaxHops:    1,
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, `unknown source_info field "custom_typo"`)
}

func TestQueryLivenessGraphAllowsDefinedResourceWithoutRelations(t *testing.T) {
	ctx := context.Background()
	provider := relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourcePrimaryKeys: map[string][]string{
			"cluster": {"bcs_cluster_id"},
		},
	})
	model, err := NewModel(ctx, &mockGraphQueryExecutor{})
	require.NoError(t, err)
	model.SetSchemaProvider(NewSchemaProviderFromRelation(provider))

	_, paths, _, err := model.QueryLivenessGraph(ctx, &QueryRequest{
		Timestamp:  300000,
		SourceType: ResourceTypeCluster,
		SourceInfo: map[string]string{
			"bcs_cluster_id": "BCS-K8S-00001",
		},
	})

	require.NoError(t, err)
	assert.Equal(t, []cmdb.PathV2{{Steps: []cmdb.PathStepV2{{ResourceType: "cluster"}}}}, paths)
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
	assert.Contains(t, executor.sql, "entity_data: { target_id: out.target_id }")
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
	assert.Contains(t, executor.sql, "entity_data: { target_id: out.target_id, version: out.version }")
}

func TestSurrealQueryBuilderProjectsRootInfoFieldsForImplicitTarget(t *testing.T) {
	provider := NewSchemaProviderFromRelation(&namespaceRelationProvider{
		resources: map[string]map[string]*relation.ResourceDefinition{
			relation.NamespaceAll: {
				"custom_source": resourceDefinitionWithFields("custom_source", []string{"custom_id"}, "version"),
			},
		},
	})

	builder := NewSurrealQueryBuilderWithSchemaProvider(&QueryRequest{
		Timestamp:      300000,
		SourceType:     "custom_source",
		TargetInfoShow: true,
	}, provider)

	sql := builder.buildRootSelect()
	assert.Contains(t, sql, "entity_data: { custom_id: custom_id, version: version }")
}

func TestSurrealQueryBuilderKeepsRootPrimaryKeysForExplicitSameTypeTarget(t *testing.T) {
	provider := NewSchemaProviderFromRelation(&namespaceRelationProvider{
		resources: map[string]map[string]*relation.ResourceDefinition{
			relation.NamespaceAll: {
				"custom_source": resourceDefinitionWithFields("custom_source", []string{"custom_id"}, "version"),
			},
		},
	})

	builder := NewSurrealQueryBuilderWithSchemaProvider(&QueryRequest{
		Timestamp:          300000,
		SourceType:         "custom_source",
		TargetType:         "custom_source",
		TargetTypeExplicit: true,
		TargetInfoShow:     true,
	}, provider)

	sql := builder.buildRootSelect()
	assert.Contains(t, sql, "entity_data: { custom_id: custom_id }")
	assert.NotContains(t, sql, "version: version")
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

func TestDefaultStaticSchemaProviderKeepsInfoFields(t *testing.T) {
	provider := NewStaticSchemaProvider()

	assert.Equal(t, []string{"bcs_cluster_id", "namespace", "pod", "container"}, provider.GetResourcePrimaryKeys("", ResourceTypeContainer))
	assert.Contains(t, provider.GetResourceFields("", ResourceTypeContainer), "version")
	assert.Contains(t, provider.GetResourceFields("", ResourceTypeHost), "env_name")
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
	assert.Contains(t, executor.sql, "entity_data: { global_node: out.global_node }")
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

func TestInferSourceTypePrefersBizOverBusinessAlias(t *testing.T) {
	sourceType, err := inferSourceTypeFromInfo(&QueryRequest{
		SourceInfo: map[string]string{"bk_biz_id": "2"},
	}, NewStaticSchemaProvider())

	require.NoError(t, err)
	assert.Equal(t, ResourceTypeBiz, sourceType)
}

func TestInferSourceTypeUsesDefinedResourcesWithoutRelations(t *testing.T) {
	provider := relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourcePrimaryKeys: map[string][]string{
			"cluster": {"bcs_cluster_id"},
		},
	})

	sourceType, err := inferSourceTypeFromInfo(&QueryRequest{
		SourceInfo: map[string]string{"bcs_cluster_id": "BCS-K8S-00001"},
	}, NewSchemaProviderFromRelation(provider))

	require.NoError(t, err)
	assert.Equal(t, ResourceTypeCluster, sourceType)
}

func TestComputeMaxHopsKeepsRoomForPartialPathResource(t *testing.T) {
	assert.Equal(t, DefaultMaxHops, computeMaxHops("", "", nil))
	assert.Equal(t, DefaultMaxHops, computeMaxHops("node", "deployment", nil))
	assert.Equal(t, DefaultMaxHops, computeMaxHops("node", "", nil))
	assert.Equal(t, 1, computeMaxHops("node", "pod", []cmdb.Resource{""}))
	assert.Equal(t, DefaultMaxHops, computeMaxHops("node", "pod", []cmdb.Resource{"pod"}))
	assert.Equal(t, DefaultMaxHops+2, computeMaxHops("node", "deployment", []cmdb.Resource{"pod"}))
	assert.Equal(t, MaxAllowedHops, computeMaxHops("node", "host", []cmdb.Resource{"a", "b", "c", "d", "e"}))
}

func TestQueryLivenessGraphRaisesMaxHopsWhenDefaultCannotReachTarget(t *testing.T) {
	ctx := context.Background()
	provider := relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourcePrimaryKeys: map[string][]string{
			"node":       {"node"},
			"pod":        {"pod"},
			"replicaset": {"replicaset"},
			"deployment": {"deployment"},
		},
		RelationSchemas: []relation.RelationSchema{
			{RelationName: "node_with_pod", Category: relation.RelationCategoryStatic, FromType: "node", ToType: "pod"},
			{RelationName: "pod_with_replicaset", Category: relation.RelationCategoryStatic, FromType: "pod", ToType: "replicaset"},
			{RelationName: "deployment_with_replicaset", Category: relation.RelationCategoryStatic, FromType: "deployment", ToType: "replicaset"},
		},
	})
	executor := &mockGraphQueryExecutor{}
	model, err := NewModel(ctx, executor)
	require.NoError(t, err)
	model.SetSchemaProvider(NewSchemaProviderFromRelation(provider))

	_, paths, _, err := model.QueryLivenessGraph(ctx, &QueryRequest{
		Timestamp:  300000,
		SourceType: ResourceTypeNode,
		SourceInfo: map[string]string{"node": "node-1"},
		TargetType: ResourceTypeDeployment,
		MaxHops:    DefaultMaxHops,
	})

	require.NoError(t, err)
	assert.Contains(t, executor.sql, "hop3")
	assert.Equal(t, []cmdb.PathV2{{Steps: []cmdb.PathStepV2{
		{ResourceType: "node"},
		{ResourceType: "pod", RelationType: "node_with_pod", Category: "static", Direction: "outbound"},
		{ResourceType: "replicaset", RelationType: "pod_with_replicaset", Category: "static", Direction: "outbound"},
		{ResourceType: "deployment", RelationType: "deployment_with_replicaset", Category: "static", Direction: "inbound"},
	}}}, paths)
}

func TestSurrealQueryBuilderUsesScalarLivenessFilters(t *testing.T) {
	sql := NewSurrealQueryBuilder(&QueryRequest{
		Timestamp:     600000,
		LookBackDelta: 600000,
		SourceType:    ResourceTypeNode,
		SourceInfo:    map[string]string{"bcs_cluster_id": "BCS-K8S-00001", "node": "node-1"},
		TargetType:    ResourceTypePod,
		MaxHops:       1,
		Limit:         10,
	}).Build()

	assert.Contains(t, sql, "SELECT * FROM node_liveness_record WHERE reference_id = $parent.id")
	assert.Contains(t, sql, "SELECT * FROM node_with_pod_liveness_record WHERE relation_id = $parent.id")
	assert.Contains(t, sql, "LET $end = 600;")
	assert.Contains(t, sql, "LET $end_ms = 600000;")
	assert.Contains(t, sql, "[0] != NONE")
	assert.NotContains(t, sql, "(SELECT count() FROM only")
}

func TestSurrealQueryBuilderUsesSecondEntityAndMillisecondRelationWindows(t *testing.T) {
	sql := NewSurrealQueryBuilder(&QueryRequest{
		Timestamp:     1782984106000,
		LookBackDelta: 604800000,
		SourceType:    ResourceTypeModule,
		SourceInfo:    map[string]string{"bk_module_id": "10086"},
		TargetType:    ResourceTypeSet,
		PathResource:  []ResourceType{ResourceTypeModule, ResourceTypeSet},
		MaxHops:       1,
		Limit:         100,
	}).Build()

	assert.Contains(t, sql, "LET $start = 1782379306;")
	assert.Contains(t, sql, "LET $end = 1782984106;")
	assert.Contains(t, sql, "LET $start_ms = 1782379306000;")
	assert.Contains(t, sql, "LET $end_ms = 1782984106000;")
	assert.Contains(t, sql, "SELECT * FROM module_liveness_record WHERE reference_id = $parent.id AND $end >= period_start AND $start <= period_end")
	assert.Contains(t, sql, "SELECT * FROM module_with_set_liveness_record WHERE relation_id = $parent.id AND $end_ms >= period_start AND $start_ms <= period_end")
}

func TestSurrealParserNormalizesSecondEntityPeriodsForRangeTargetList(t *testing.T) {
	const (
		queryStartMs = int64(1782558647000)
		queryEndMs   = int64(1782559015000)
		stepMs       = int64(60000)
	)

	rawResponse := []map[string]any{
		{
			ResponseFieldResult: []any{
				map[string]any{
					ResponseFieldResult: map[string]any{
						ResponseFieldRoot: map[string]any{
							ResponseFieldEntityType: string(ResourceTypeModule),
							ResponseFieldEntityID:   "module:⟨10086⟩",
							ResponseFieldEntityData: map[string]any{"bk_module_id": "10086"},
							ResponseFieldLiveness: []any{
								map[string]any{FieldPeriodStart: float64(1782558647), FieldPeriodEnd: float64(1782559015)},
							},
						},
						ResponseFieldHopPrefix + "1": map[string]any{
							string(RelationModuleWithSet): []any{
								map[string]any{
									ResponseFieldRelationType:     string(RelationModuleWithSet),
									ResponseFieldRelationCategory: string(RelationCategoryStatic),
									ResponseFieldRelationID:       "module_with_set:⟨10086_2731⟩",
									ResponseFieldRelationLiveness: []any{
										map[string]any{FieldPeriodStart: float64(1782558647643), FieldPeriodEnd: float64(1782559015590)},
									},
									ResponseFieldTarget: map[string]any{
										ResponseFieldEntityType: string(ResourceTypeSet),
										ResponseFieldEntityID:   "set:⟨2731⟩",
										ResponseFieldEntityData: map[string]any{"bk_set_id": "2731"},
										ResponseFieldLiveness: []any{
											map[string]any{FieldPeriodStart: float64(1782558647), FieldPeriodEnd: float64(1782559015)},
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

	graphs, err := NewSurrealResponseParser(queryStartMs, queryEndMs).Parse(rawResponse)
	require.NoError(t, err)
	require.Len(t, graphs, 1)

	root := graphs[0].GetNode("module:⟨10086⟩")
	require.NotNil(t, root)
	assert.Equal(t, []*VisiblePeriod{{Start: 1782558647000, End: 1782559015000}}, root.RawPeriods)

	edge := graphs[0].GetEdge("module_with_set:⟨10086_2731⟩")
	require.NotNil(t, edge)
	assert.Equal(t, []*VisiblePeriod{{Start: 1782558647643, End: 1782559015590}}, edge.RawPeriods)

	targetList := buildTargetMatchersTimeSeriesWithOptions(
		graphs,
		ResourceTypeSet,
		[]ResourceType{ResourceTypeModule, ResourceTypeSet},
		queryStartMs,
		queryEndMs,
		stepMs,
		NewStaticSchemaProvider(),
		relation.NamespaceAll,
		false,
		true,
	)

	require.NotEmpty(t, targetList)
	assert.Equal(t, int64(1782558707000), targetList[0].Timestamp)
	assert.Equal(t, []cmdb.Matcher{{"bk_set_id": "2731"}}, targetList[0].Matchers)
}

func TestQueryResourceMatcherReturnsLegacyResourcePath(t *testing.T) {
	ctx := context.Background()
	provider := relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourcePrimaryKeys: map[string][]string{
			"node": {"node"},
			"pod":  {"pod"},
		},
		RelationSchemas: []relation.RelationSchema{
			{
				RelationName: "node_with_pod",
				Category:     relation.RelationCategoryStatic,
				FromType:     "node",
				ToType:       "pod",
			},
		},
	})
	model, err := NewModel(ctx, &mockGraphQueryExecutor{})
	require.NoError(t, err)
	model.SetSchemaProvider(NewSchemaProviderFromRelation(provider))

	source, matcher, path, target, matchers, err := model.QueryResourceMatcher(
		ctx,
		"10m",
		"test-space",
		"600",
		"pod",
		"node",
		cmdb.Matcher{"node": "node-1"},
		nil,
		false,
		nil,
	)

	require.NoError(t, err)
	assert.Equal(t, cmdb.Resource("node"), source)
	assert.Equal(t, cmdb.Matcher{"node": "node-1"}, matcher)
	assert.Equal(t, []string{"node", "pod"}, path)
	assert.Equal(t, cmdb.Resource("pod"), target)
	assert.Nil(t, matchers)

	_, _, rangePath, _, rangeResult, err := model.QueryResourceMatcherRange(
		ctx,
		"10m",
		"test-space",
		"60s",
		"0",
		"60",
		"pod",
		"node",
		cmdb.Matcher{"node": "node-1"},
		nil,
		false,
		nil,
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"node", "pod"}, rangePath)
	assert.Nil(t, rangeResult)
}

func TestQueryResourceMatcherReturnsPathFromMatchedGraph(t *testing.T) {
	ctx := context.Background()
	provider := relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourcePrimaryKeys: map[string][]string{
			"node":   {"node"},
			"system": {"system"},
			"pod":    {"pod"},
		},
		RelationSchemas: []relation.RelationSchema{
			{RelationName: "node_with_system", Category: relation.RelationCategoryStatic, FromType: "node", ToType: "system"},
			{RelationName: "system_to_pod", Category: relation.RelationCategoryStatic, FromType: "system", ToType: "pod"},
			{RelationName: "node_with_pod", Category: relation.RelationCategoryStatic, FromType: "node", ToType: "pod"},
		},
	})
	periods := []*VisiblePeriod{{Start: 0, End: 600000}}
	graph := NewLivenessGraph(0, 600000)
	node := &NodeLiveness{
		ResourceID:   "node:node-1",
		ResourceType: ResourceTypeNode,
		Labels:       map[string]string{"node": "node-1"},
		RawPeriods:   periods,
	}
	pod := &NodeLiveness{
		ResourceID:   "pod:pod-1",
		ResourceType: ResourceTypePod,
		Labels:       map[string]string{"pod": "pod-1"},
		RawPeriods:   periods,
	}
	graph.AddNode(node)
	graph.AddNode(pod)
	graph.AddEdge(&EdgeLiveness{
		RelationID:   "node_with_pod:1",
		RelationType: RelationNodeWithPod,
		FromID:       node.ResourceID,
		ToID:         pod.ResourceID,
		RawPeriods:   periods,
	})

	model, err := NewModel(ctx, &mockGraphQueryExecutor{graphs: []*LivenessGraph{graph}})
	require.NoError(t, err)
	model.SetSchemaProvider(NewSchemaProviderFromRelation(provider))

	_, _, path, _, matchers, err := model.QueryResourceMatcher(
		ctx,
		"10m",
		"test-space",
		"600",
		"pod",
		"node",
		cmdb.Matcher{"node": "node-1"},
		nil,
		false,
		nil,
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"node", "pod"}, path)
	assert.Equal(t, cmdb.Matchers{{"pod": "pod-1"}}, matchers)

	_, _, rangePath, _, rangeResult, err := model.QueryResourceMatcherRange(
		ctx,
		"10m",
		"test-space",
		"60s",
		"0",
		"600",
		"pod",
		"node",
		cmdb.Matcher{"node": "node-1"},
		nil,
		false,
		nil,
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"node", "pod"}, rangePath)
	require.NotEmpty(t, rangeResult)
	assert.Equal(t, []cmdb.Matcher{{"pod": "pod-1"}}, rangeResult[0].Matchers)
}

func TestQueryResourceMatcherRangeFiltersTargetsBySelectedLegacyPath(t *testing.T) {
	ctx := context.Background()
	provider := relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourcePrimaryKeys: map[string][]string{
			"node":   {"node"},
			"system": {"system"},
			"pod":    {"pod"},
		},
		RelationSchemas: []relation.RelationSchema{
			{RelationName: "node_with_pod", Category: relation.RelationCategoryStatic, FromType: "node", ToType: "pod"},
			{RelationName: "node_with_system", Category: relation.RelationCategoryStatic, FromType: "node", ToType: "system"},
			{RelationName: "system_to_pod", Category: relation.RelationCategoryStatic, FromType: "system", ToType: "pod"},
		},
	})
	periods := []*VisiblePeriod{{Start: 0, End: 600000}}
	node := &NodeLiveness{
		ResourceID:   "node:node-1",
		ResourceType: ResourceTypeNode,
		Labels:       map[string]string{"node": "node-1"},
		RawPeriods:   periods,
	}
	system := &NodeLiveness{
		ResourceID:   "system:system-1",
		ResourceType: ResourceTypeSystem,
		Labels:       map[string]string{"system": "system-1"},
		RawPeriods:   periods,
	}
	directPod := &NodeLiveness{
		ResourceID:   "pod:direct",
		ResourceType: ResourceTypePod,
		Labels:       map[string]string{"pod": "direct"},
		RawPeriods:   periods,
	}
	indirectPod := &NodeLiveness{
		ResourceID:   "pod:indirect",
		ResourceType: ResourceTypePod,
		Labels:       map[string]string{"pod": "indirect"},
		RawPeriods:   periods,
	}

	executor := &recordingGraphQueryExecutor{
		responseForSQL: func(sql string) graphQueryResponse {
			switch {
			case strings.Contains(sql, "node_with_pod"):
				directGraph := NewLivenessGraph(0, 600000)
				directGraph.AddNode(node)
				directGraph.AddNode(directPod)
				directGraph.AddEdge(&EdgeLiveness{
					RelationID:   "node_with_pod:direct",
					RelationType: RelationNodeWithPod,
					FromID:       node.ResourceID,
					ToID:         directPod.ResourceID,
					RawPeriods:   periods,
				})
				return graphQueryResponse{graphs: []*LivenessGraph{directGraph}}
			case strings.Contains(sql, "node_with_system"):
				indirectGraph := NewLivenessGraph(0, 600000)
				indirectGraph.AddNode(node)
				indirectGraph.AddNode(system)
				indirectGraph.AddNode(indirectPod)
				indirectGraph.AddEdge(&EdgeLiveness{
					RelationID:   "node_with_system:1",
					RelationType: RelationNodeWithSystem,
					FromID:       node.ResourceID,
					ToID:         system.ResourceID,
					RawPeriods:   periods,
				})
				indirectGraph.AddEdge(&EdgeLiveness{
					RelationID:   "system_to_pod:indirect",
					RelationType: RelationSystemToPod,
					FromID:       system.ResourceID,
					ToID:         indirectPod.ResourceID,
					RawPeriods:   periods,
				})
				return graphQueryResponse{graphs: []*LivenessGraph{indirectGraph}}
			default:
				return graphQueryResponse{}
			}
		},
	}

	model, err := NewModel(ctx, executor)
	require.NoError(t, err)
	model.SetSchemaProvider(NewSchemaProviderFromRelation(provider))

	_, _, rangePath, _, rangeResult, err := model.QueryResourceMatcherRange(
		ctx,
		"10m",
		"test-space",
		"60s",
		"0",
		"600",
		"pod",
		"node",
		cmdb.Matcher{"node": "node-1"},
		nil,
		false,
		nil,
	)

	require.NoError(t, err)
	assert.Len(t, executor.sqls, 2)
	for _, sql := range executor.sqls {
		if strings.Contains(sql, "node_with_pod") {
			assert.NotContains(t, sql, "node_with_system")
			assert.NotContains(t, sql, "system_to_pod")
			continue
		}
		assert.Contains(t, sql, "node_with_system")
		assert.Contains(t, sql, "system_to_pod")
		assert.NotContains(t, sql, "node_with_pod")
	}
	assert.Equal(t, []string{"node", "pod"}, rangePath)
	require.NotEmpty(t, rangeResult)
	assert.Equal(t, []cmdb.Matcher{{"pod": "direct"}}, rangeResult[0].Matchers)
}

func TestQueryResourceMatcherRejectsNegativeLookBackDelta(t *testing.T) {
	model, err := NewModel(context.Background(), &mockGraphQueryExecutor{})
	require.NoError(t, err)

	_, _, _, _, _, err = model.QueryResourceMatcher(
		context.Background(),
		"-1m",
		"test-space",
		"600",
		"pod",
		"node",
		cmdb.Matcher{"node": "node-1"},
		nil,
		false,
		nil,
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "look_back_delta must be greater than or equal to 0")
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
	assert.Contains(t, executor.sql, "pod_liveness_record WHERE reference_id = $parent.")
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
	root := &NodeLiveness{
		ResourceID:   "node:1",
		ResourceType: ResourceTypeNode,
		Labels:       map[string]string{"node": "node-1"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	system := &NodeLiveness{
		ResourceID:   "system:1",
		ResourceType: ResourceTypeSystem,
		Labels:       map[string]string{"system": "system-1"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
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
	graph.AddEdge(&EdgeLiveness{RelationID: "node-system", FromID: root.ResourceID, ToID: system.ResourceID, RawPeriods: []*VisiblePeriod{{Start: 0, End: 200}}})
	graph.AddEdge(&EdgeLiveness{RelationID: "system-pod", FromID: system.ResourceID, ToID: podViaSystem.ResourceID, RawPeriods: []*VisiblePeriod{{Start: 0, End: 200}}})
	graph.AddEdge(&EdgeLiveness{RelationID: "node-pod", FromID: root.ResourceID, ToID: podDirect.ResourceID, RawPeriods: []*VisiblePeriod{{Start: 0, End: 200}}})

	matchers := extractMatchersFromGraphs([]*LivenessGraph{graph}, ResourceTypePod, []ResourceType{ResourceTypeSystem})

	assert.Equal(t, cmdb.Matchers{{"pod": "via-system"}}, matchers)
}

func TestQueryResourceMatcherPathResourceAllowsUnconstrainedIntermediateHops(t *testing.T) {
	graph := NewLivenessGraph(0, 200)
	pod := &NodeLiveness{
		ResourceID:   "pod:1",
		ResourceType: ResourceTypePod,
		Labels:       map[string]string{"pod": "root"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	node := &NodeLiveness{
		ResourceID:   "node:1",
		ResourceType: ResourceTypeNode,
		Labels:       map[string]string{"node": "node-1"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	system := &NodeLiveness{
		ResourceID:   "system:1",
		ResourceType: ResourceTypeSystem,
		Labels:       map[string]string{"system": "system-1"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	hostViaSystem := &NodeLiveness{
		ResourceID:   "host:via-system",
		ResourceType: ResourceTypeHost,
		Labels:       map[string]string{"bk_host_id": "via-system"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	hostDirect := &NodeLiveness{
		ResourceID:   "host:direct",
		ResourceType: ResourceTypeHost,
		Labels:       map[string]string{"bk_host_id": "direct"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	graph.AddNode(pod)
	graph.AddNode(node)
	graph.AddNode(system)
	graph.AddNode(hostViaSystem)
	graph.AddNode(hostDirect)
	graph.AddEdge(&EdgeLiveness{RelationID: "pod-node", FromID: pod.ResourceID, ToID: node.ResourceID, RawPeriods: []*VisiblePeriod{{Start: 0, End: 200}}})
	graph.AddEdge(&EdgeLiveness{RelationID: "node-system", FromID: node.ResourceID, ToID: system.ResourceID, RawPeriods: []*VisiblePeriod{{Start: 0, End: 200}}})
	graph.AddEdge(&EdgeLiveness{RelationID: "system-host", FromID: system.ResourceID, ToID: hostViaSystem.ResourceID, RawPeriods: []*VisiblePeriod{{Start: 0, End: 200}}})
	graph.AddEdge(&EdgeLiveness{RelationID: "pod-host", FromID: pod.ResourceID, ToID: hostDirect.ResourceID, RawPeriods: []*VisiblePeriod{{Start: 0, End: 200}}})

	matchers := extractMatchersFromGraphs([]*LivenessGraph{graph}, ResourceTypeHost, []ResourceType{ResourceTypeSystem})

	assert.Equal(t, cmdb.Matchers{{"bk_host_id": "via-system"}}, matchers)
}

func TestExtractMatchersUsesRootIDForCyclicDirectOnlyGraph(t *testing.T) {
	graph := NewLivenessGraph(0, 200)
	root := &NodeLiveness{
		ResourceID:   "node:root",
		ResourceType: ResourceTypeNode,
		Labels:       map[string]string{"node": "root"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	system := &NodeLiveness{
		ResourceID:   "system:1",
		ResourceType: ResourceTypeSystem,
		Labels:       map[string]string{"bk_target_ip": "127.0.0.1"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	pod := &NodeLiveness{
		ResourceID:   "pod:1",
		ResourceType: ResourceTypePod,
		Labels:       map[string]string{"pod": "pod-1"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	graph.AddNode(root)
	graph.AddNode(system)
	graph.AddNode(pod)
	graph.RootID = root.ResourceID
	graph.AddEdge(&EdgeLiveness{RelationID: "node-system", FromID: root.ResourceID, ToID: system.ResourceID, RawPeriods: []*VisiblePeriod{{Start: 0, End: 200}}})
	graph.AddEdge(&EdgeLiveness{RelationID: "system-node", FromID: system.ResourceID, ToID: root.ResourceID, RawPeriods: []*VisiblePeriod{{Start: 0, End: 200}}})
	graph.AddEdge(&EdgeLiveness{RelationID: "system-pod", FromID: system.ResourceID, ToID: pod.ResourceID, RawPeriods: []*VisiblePeriod{{Start: 0, End: 200}}})

	matchers := extractMatchersFromGraphs([]*LivenessGraph{graph}, ResourceTypePod, []ResourceType{""})

	assert.Nil(t, matchers)
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

func TestExtractMatchersRequiresPathWideInstantOverlap(t *testing.T) {
	testCases := []struct {
		name     string
		root     []*VisiblePeriod
		edge     []*VisiblePeriod
		target   []*VisiblePeriod
		expected cmdb.Matchers
	}{
		{
			name:   "path_has_no_common_active_window",
			root:   []*VisiblePeriod{{Start: 0, End: 50}},
			edge:   []*VisiblePeriod{{Start: 0, End: 50}},
			target: []*VisiblePeriod{{Start: 100, End: 200}},
		},
		{
			name:     "path_has_common_active_window",
			root:     []*VisiblePeriod{{Start: 0, End: 150}},
			edge:     []*VisiblePeriod{{Start: 50, End: 150}},
			target:   []*VisiblePeriod{{Start: 100, End: 200}},
			expected: cmdb.Matchers{{"pod": "nginx-1"}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			graph := NewLivenessGraph(0, 200)
			root := &NodeLiveness{
				ResourceID:   "node:1",
				ResourceType: ResourceTypeNode,
				Labels:       map[string]string{"node": "node-1"},
				RawPeriods:   tc.root,
			}
			pod := &NodeLiveness{
				ResourceID:   "pod:1",
				ResourceType: ResourceTypePod,
				Labels:       map[string]string{"pod": "nginx-1"},
				RawPeriods:   tc.target,
			}
			graph.AddNode(root)
			graph.AddNode(pod)
			graph.AddEdge(&EdgeLiveness{
				RelationID: "node-pod",
				FromID:     root.ResourceID,
				ToID:       pod.ResourceID,
				RawPeriods: tc.edge,
			})

			matchers := extractMatchersFromGraphs([]*LivenessGraph{graph}, ResourceTypePod, nil)

			assert.Equal(t, tc.expected, matchers)
		})
	}
}

func TestLivenessGraphPreservesRepeatedRelationRowsByDirection(t *testing.T) {
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
		Labels:       map[string]string{"pod": "pod-1"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	}
	graph.AddNode(root)
	graph.AddNode(pod)
	graph.RootID = root.ResourceID
	graph.AddEdge(&EdgeLiveness{
		RelationID: "node-pod:1",
		Direction:  DirectionOutbound,
		FromID:     root.ResourceID,
		ToID:       pod.ResourceID,
		RawPeriods: []*VisiblePeriod{{Start: 0, End: 200}},
	})
	graph.AddEdge(&EdgeLiveness{
		RelationID: "node-pod:1",
		Direction:  DirectionInbound,
		FromID:     pod.ResourceID,
		ToID:       root.ResourceID,
		RawPeriods: []*VisiblePeriod{{Start: 0, End: 200}},
	})

	matchers := extractMatchersFromGraphs([]*LivenessGraph{graph}, ResourceTypePod, nil)

	assert.Len(t, graph.Edges, 2)
	assert.Equal(t, cmdb.Matchers{{"pod": "pod-1"}}, matchers)
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
		true,
		false,
	)

	assert.Equal(t, cmdb.Matchers{{"bk_target_ip": "127.0.0.1"}}, implicitTarget)
	assert.Nil(t, explicitSelfTarget)
}

func TestExtractMatchersAllowsSelfLoopForExplicitSelfTarget(t *testing.T) {
	graph := NewLivenessGraph(0, 200)
	graph.AddNode(&NodeLiveness{
		ResourceID:   "pod:self",
		ResourceType: ResourceTypePod,
		Labels:       map[string]string{"pod": "pod-a"},
		RawPeriods:   []*VisiblePeriod{{Start: 0, End: 200}},
	})
	graph.RootID = "pod:self"
	graph.AddEdge(&EdgeLiveness{
		RelationID: "pod-to-pod:self",
		FromID:     "pod:self",
		ToID:       "pod:self",
		RawPeriods: []*VisiblePeriod{{Start: 0, End: 200}},
	})

	matchers := extractMatchersFromGraphsWithOptions(
		[]*LivenessGraph{graph},
		ResourceTypePod,
		nil,
		GetSchemaProvider(),
		"",
		false,
		false,
	)

	assert.Equal(t, cmdb.Matchers{{"pod": "pod-a"}}, matchers)
}

func TestQueryLivenessGraphConstrainsExplicitSameTypeTargetsToSelfRelation(t *testing.T) {
	ctx := context.Background()
	provider := relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourcePrimaryKeys: map[string][]string{
			"system": {"bk_target_ip"},
			"pod":    {"pod"},
		},
		RelationSchemas: []relation.RelationSchema{
			{
				RelationName:  "system_to_system",
				Category:      relation.RelationCategoryDynamic,
				FromType:      "system",
				ToType:        "system",
				IsDirectional: true,
			},
			{
				RelationName:  "system_to_pod",
				Category:      relation.RelationCategoryDynamic,
				FromType:      "system",
				ToType:        "pod",
				IsDirectional: true,
			},
			{
				RelationName:  "pod_to_system",
				Category:      relation.RelationCategoryDynamic,
				FromType:      "pod",
				ToType:        "system",
				IsDirectional: true,
			},
		},
	})
	periods := []*VisiblePeriod{{Start: 0, End: 200}}
	graph := NewLivenessGraph(0, 200)
	source := &NodeLiveness{
		ResourceID:   "system:source",
		ResourceType: ResourceTypeSystem,
		Labels:       map[string]string{"bk_target_ip": "source"},
		RawPeriods:   periods,
	}
	directTarget := &NodeLiveness{
		ResourceID:   "system:direct",
		ResourceType: ResourceTypeSystem,
		Labels:       map[string]string{"bk_target_ip": "direct"},
		RawPeriods:   periods,
	}
	pod := &NodeLiveness{
		ResourceID:   "pod:1",
		ResourceType: ResourceTypePod,
		Labels:       map[string]string{"pod": "pod-1"},
		RawPeriods:   periods,
	}
	indirectTarget := &NodeLiveness{
		ResourceID:   "system:indirect",
		ResourceType: ResourceTypeSystem,
		Labels:       map[string]string{"bk_target_ip": "indirect"},
		RawPeriods:   periods,
	}
	graph.AddNode(source)
	graph.AddNode(directTarget)
	graph.AddNode(pod)
	graph.AddNode(indirectTarget)
	graph.AddEdge(&EdgeLiveness{RelationID: "system-system", FromID: source.ResourceID, ToID: directTarget.ResourceID, RawPeriods: periods})
	graph.AddEdge(&EdgeLiveness{RelationID: "system-pod", FromID: source.ResourceID, ToID: pod.ResourceID, RawPeriods: periods})
	graph.AddEdge(&EdgeLiveness{RelationID: "pod-system", FromID: pod.ResourceID, ToID: indirectTarget.ResourceID, RawPeriods: periods})

	executor := &mockGraphQueryExecutor{graphs: []*LivenessGraph{graph}}
	model, err := NewModel(ctx, executor)
	require.NoError(t, err)
	model.SetSchemaProvider(NewSchemaProviderFromRelation(provider))

	_, paths, matchers, err := model.QueryLivenessGraph(ctx, &QueryRequest{
		Timestamp:          200,
		LookBackDelta:      200,
		SourceType:         ResourceTypeSystem,
		SourceInfo:         map[string]string{"bk_target_ip": "source"},
		TargetType:         ResourceTypeSystem,
		TargetTypeExplicit: true,
	})

	require.NoError(t, err)
	assert.Equal(t, []cmdb.PathV2{
		{Steps: []cmdb.PathStepV2{
			{ResourceType: "system"},
			{
				ResourceType: "system",
				RelationType: "system_to_system",
				Category:     "dynamic",
				Direction:    "outbound",
			},
		}},
		{Steps: []cmdb.PathStepV2{
			{ResourceType: "system"},
			{
				ResourceType: "system",
				RelationType: "system_to_system",
				Category:     "dynamic",
				Direction:    "inbound",
			},
		}},
	}, paths)
	assert.Equal(t, cmdb.Matchers{{"bk_target_ip": "direct"}}, matchers)
	assert.NotContains(t, executor.sql, "hop2")
}

func TestQueryLivenessGraphRejectsTraversalErrors(t *testing.T) {
	ctx := context.Background()
	graph := NewLivenessGraph(0, 200)
	graph.AddTraversalError("parse relation pod_with_service: missing relation_id")
	model, err := NewModel(ctx, &mockGraphQueryExecutor{graphs: []*LivenessGraph{graph}})
	require.NoError(t, err)

	_, _, _, err = model.QueryLivenessGraph(ctx, &QueryRequest{
		Timestamp:          300000,
		SourceType:         ResourceTypeNode,
		SourceInfo:         map[string]string{"bcs_cluster_id": "BCS-K8S-00001", "node": "node-1"},
		TargetType:         ResourceTypePod,
		TargetTypeExplicit: true,
		LookBackDelta:      600000,
		LookBackDeltaSet:   true,
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "parse SurrealDB graph response")
	assert.ErrorContains(t, err, "missing relation_id")
}

func TestQueryLivenessGraphRejectsExplicitSameTypeWithoutSelfRelation(t *testing.T) {
	ctx := context.Background()
	provider := relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourcePrimaryKeys: map[string][]string{
			"system": {"bk_target_ip"},
			"pod":    {"pod"},
		},
		RelationSchemas: []relation.RelationSchema{
			{
				RelationName:  "system_to_pod",
				Category:      relation.RelationCategoryDynamic,
				FromType:      "system",
				ToType:        "pod",
				IsDirectional: true,
			},
		},
	})
	model, err := NewModel(ctx, &mockGraphQueryExecutor{})
	require.NoError(t, err)
	model.SetSchemaProvider(NewSchemaProviderFromRelation(provider))

	_, _, _, err = model.QueryLivenessGraph(ctx, &QueryRequest{
		Timestamp:          300000,
		SourceType:         ResourceTypeSystem,
		SourceInfo:         map[string]string{"bk_target_ip": "127.0.0.1"},
		TargetType:         ResourceTypeSystem,
		TargetTypeExplicit: true,
		LookBackDelta:      600000,
		LookBackDeltaSet:   true,
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "empty paths")
}

func TestQueryLivenessGraphExecutesInstantQueryPathByPath(t *testing.T) {
	ctx := context.Background()
	provider := relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourcePrimaryKeys: map[string][]string{
			"system":      {"bk_target_ip"},
			"container":   {"container_id"},
			"statefulset": {"statefulset"},
			"pod":         {"pod"},
		},
		RelationSchemas: []relation.RelationSchema{
			{
				RelationName:  "system_to_container",
				Category:      relation.RelationCategoryDynamic,
				FromType:      "system",
				ToType:        "container",
				IsDirectional: true,
			},
			{
				RelationName:  "container_to_pod",
				Category:      relation.RelationCategoryDynamic,
				FromType:      "container",
				ToType:        "pod",
				IsDirectional: true,
			},
			{
				RelationName:  "system_to_statefulset",
				Category:      relation.RelationCategoryDynamic,
				FromType:      "system",
				ToType:        "statefulset",
				IsDirectional: true,
			},
			{
				RelationName:  "statefulset_to_pod",
				Category:      relation.RelationCategoryDynamic,
				FromType:      "statefulset",
				ToType:        "pod",
				IsDirectional: true,
			},
			{
				RelationName:  "system_to_pod",
				Category:      relation.RelationCategoryDynamic,
				FromType:      "system",
				ToType:        "pod",
				IsDirectional: true,
			},
		},
	})

	periods := []*VisiblePeriod{{Start: 0, End: 200}}
	targetGraph := func(podName string) *LivenessGraph {
		graph := NewLivenessGraph(0, 200)
		source := &NodeLiveness{
			ResourceID:   "system:source",
			ResourceType: ResourceTypeSystem,
			Labels:       map[string]string{"bk_target_ip": "source"},
			RawPeriods:   periods,
		}
		target := &NodeLiveness{
			ResourceID:   "pod:" + podName,
			ResourceType: ResourceTypePod,
			Labels:       map[string]string{"pod": podName},
			RawPeriods:   periods,
		}
		graph.RootID = source.ResourceID
		graph.AddNode(source)
		graph.AddNode(target)
		graph.AddEdge(&EdgeLiveness{
			RelationID: "edge-" + podName,
			FromID:     source.ResourceID,
			ToID:       target.ResourceID,
			RawPeriods: periods,
		})
		return graph
	}

	testCases := []struct {
		name             string
		responseForSQL   func(sql string) graphQueryResponse
		expectedMatchers cmdb.Matchers
		expectedPaths    []cmdb.PathV2
	}{
		{
			name: "direct path hit wins",
			responseForSQL: func(sql string) graphQueryResponse {
				if strings.Contains(sql, "system_to_pod") {
					return graphQueryResponse{graphs: []*LivenessGraph{targetGraph("direct")}}
				}
				return graphQueryResponse{}
			},
			expectedMatchers: cmdb.Matchers{{"pod": "direct"}},
			expectedPaths: []cmdb.PathV2{{Steps: []cmdb.PathStepV2{
				{ResourceType: "system"},
				{ResourceType: "pod", RelationType: "system_to_pod", Category: "dynamic", Direction: "outbound"},
			}}},
		},
		{
			name: "empty direct path continues to next matching path",
			responseForSQL: func(sql string) graphQueryResponse {
				if strings.Contains(sql, "system_to_container") {
					return graphQueryResponse{graphs: []*LivenessGraph{targetGraph("via-container")}}
				}
				return graphQueryResponse{}
			},
			expectedMatchers: cmdb.Matchers{{"pod": "via-container"}},
			expectedPaths: []cmdb.PathV2{{Steps: []cmdb.PathStepV2{
				{ResourceType: "system"},
				{ResourceType: "container", RelationType: "system_to_container", Category: "dynamic", Direction: "outbound"},
				{ResourceType: "pod", RelationType: "container_to_pod", Category: "dynamic", Direction: "outbound"},
			}}},
		},
		{
			name: "all paths empty keeps all candidate paths",
			responseForSQL: func(sql string) graphQueryResponse {
				return graphQueryResponse{}
			},
			expectedMatchers: nil,
			expectedPaths: []cmdb.PathV2{
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "system"},
					{ResourceType: "pod", RelationType: "system_to_pod", Category: "dynamic", Direction: "outbound"},
				}},
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "system"},
					{ResourceType: "container", RelationType: "system_to_container", Category: "dynamic", Direction: "outbound"},
					{ResourceType: "pod", RelationType: "container_to_pod", Category: "dynamic", Direction: "outbound"},
				}},
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "system"},
					{ResourceType: "statefulset", RelationType: "system_to_statefulset", Category: "dynamic", Direction: "outbound"},
					{ResourceType: "pod", RelationType: "statefulset_to_pod", Category: "dynamic", Direction: "outbound"},
				}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor := &recordingGraphQueryExecutor{responseForSQL: tc.responseForSQL}
			model, err := NewModel(ctx, executor)
			require.NoError(t, err)
			model.SetSchemaProvider(NewSchemaProviderFromRelation(provider))

			_, paths, matchers, err := model.QueryLivenessGraph(ctx, &QueryRequest{
				Timestamp:          200,
				LookBackDelta:      200,
				SourceType:         ResourceTypeSystem,
				SourceInfo:         map[string]string{"bk_target_ip": "source"},
				TargetType:         ResourceTypePod,
				TargetTypeExplicit: true,
			})

			require.NoError(t, err)
			assert.NotEmpty(t, executor.sqls)
			assert.Equal(t, tc.expectedPaths, paths)
			assert.Equal(t, tc.expectedMatchers, matchers)
			assertTrimmedPathSQLs(t, executor.sqls)
		})
	}
}

func assertTrimmedPathSQLs(t *testing.T, sqls []string) {
	t.Helper()
	for _, sql := range sqls {
		switch {
		case strings.Contains(sql, "system_to_pod"):
			assert.NotContains(t, sql, "system_to_container")
			assert.NotContains(t, sql, "system_to_statefulset")
		case strings.Contains(sql, "system_to_container"):
			assert.Contains(t, sql, "container_to_pod")
			assert.NotContains(t, sql, "system_to_pod")
			assert.NotContains(t, sql, "system_to_statefulset")
		case strings.Contains(sql, "system_to_statefulset"):
			assert.Contains(t, sql, "statefulset_to_pod")
			assert.NotContains(t, sql, "system_to_pod")
			assert.NotContains(t, sql, "system_to_container")
		default:
			assert.Failf(t, "unexpected path sql", "sql does not contain a known relation path: %s", sql)
		}
	}
}

func TestSortPathsForQueryPrefersShorterPaths(t *testing.T) {
	paths := []cmdb.PathV2{
		{Steps: []cmdb.PathStepV2{{ResourceType: "system"}, {ResourceType: "container"}, {ResourceType: "pod"}}},
		{Steps: []cmdb.PathStepV2{{ResourceType: "system"}, {ResourceType: "pod"}}},
		{Steps: []cmdb.PathStepV2{{ResourceType: "system"}, {ResourceType: "statefulset"}, {ResourceType: "pod"}}},
	}

	sorted := sortPathsForQuery(paths)

	assert.Equal(t, []cmdb.PathV2{
		{Steps: []cmdb.PathStepV2{{ResourceType: "system"}, {ResourceType: "pod"}}},
		{Steps: []cmdb.PathStepV2{{ResourceType: "system"}, {ResourceType: "container"}, {ResourceType: "pod"}}},
		{Steps: []cmdb.PathStepV2{{ResourceType: "system"}, {ResourceType: "statefulset"}, {ResourceType: "pod"}}},
	}, sorted)
	assert.Equal(t, "container", paths[0].Steps[1].ResourceType, "sort should not mutate caller paths")
}

func TestQueryLivenessGraphPathByPathDoesNotWaitForLowerPriorityPath(t *testing.T) {
	ctx := context.Background()
	provider := relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourcePrimaryKeys: map[string][]string{
			"system":      {"bk_target_ip"},
			"container":   {"container_id"},
			"statefulset": {"statefulset"},
			"pod":         {"pod"},
		},
		RelationSchemas: []relation.RelationSchema{
			{RelationName: "system_to_pod", Category: relation.RelationCategoryDynamic, FromType: "system", ToType: "pod", IsDirectional: true},
			{RelationName: "system_to_container", Category: relation.RelationCategoryDynamic, FromType: "system", ToType: "container", IsDirectional: true},
			{RelationName: "container_to_pod", Category: relation.RelationCategoryDynamic, FromType: "container", ToType: "pod", IsDirectional: true},
			{RelationName: "system_to_statefulset", Category: relation.RelationCategoryDynamic, FromType: "system", ToType: "statefulset", IsDirectional: true},
			{RelationName: "statefulset_to_pod", Category: relation.RelationCategoryDynamic, FromType: "statefulset", ToType: "pod", IsDirectional: true},
		},
	})
	periods := []*VisiblePeriod{{Start: 0, End: 200}}
	hitGraph := NewLivenessGraph(0, 200)
	source := &NodeLiveness{
		ResourceID:   "system:source",
		ResourceType: ResourceTypeSystem,
		Labels:       map[string]string{"bk_target_ip": "source"},
		RawPeriods:   periods,
	}
	target := &NodeLiveness{
		ResourceID:   "pod:target",
		ResourceType: ResourceTypePod,
		Labels:       map[string]string{"pod": "target"},
		RawPeriods:   periods,
	}
	hitGraph.RootID = source.ResourceID
	hitGraph.AddNode(source)
	hitGraph.AddNode(target)
	hitGraph.AddEdge(&EdgeLiveness{RelationID: "edge-target", FromID: source.ResourceID, ToID: target.ResourceID, RawPeriods: periods})

	executor := &recordingGraphQueryExecutor{
		responseForSQL: func(sql string) graphQueryResponse {
			switch {
			case strings.Contains(sql, "system_to_pod"):
				return graphQueryResponse{}
			case strings.Contains(sql, "system_to_container"):
				time.Sleep(20 * time.Millisecond)
				return graphQueryResponse{graphs: []*LivenessGraph{hitGraph}}
			case strings.Contains(sql, "system_to_statefulset"):
				time.Sleep(200 * time.Millisecond)
				return graphQueryResponse{}
			default:
				return graphQueryResponse{}
			}
		},
	}
	model, err := NewModel(ctx, executor)
	require.NoError(t, err)
	model.SetSchemaProvider(NewSchemaProviderFromRelation(provider))

	start := time.Now()
	_, paths, matchers, err := model.QueryLivenessGraph(ctx, &QueryRequest{
		Timestamp:          200,
		LookBackDelta:      200,
		SourceType:         ResourceTypeSystem,
		SourceInfo:         map[string]string{"bk_target_ip": "source"},
		TargetType:         ResourceTypePod,
		TargetTypeExplicit: true,
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 150*time.Millisecond)
	assert.Equal(t, cmdb.Matchers{{"pod": "target"}}, matchers)
	assert.Equal(t, []cmdb.PathV2{{Steps: []cmdb.PathStepV2{
		{ResourceType: "system"},
		{ResourceType: "container", RelationType: "system_to_container", Category: "dynamic", Direction: "outbound"},
		{ResourceType: "pod", RelationType: "container_to_pod", Category: "dynamic", Direction: "outbound"},
	}}}, paths)
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
	graph.AddEdge(&EdgeLiveness{RelationID: "node-pod", FromID: root.ResourceID, ToID: pod.ResourceID, RawPeriods: []*VisiblePeriod{{Start: 0, End: 200}}})

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
		{Timestamp: 100, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
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

func TestBKBaseSurrealDBClientUsesOverrideQueryURLAndPayload(t *testing.T) {
	oldQueryURL := BKBaseSurrealDBQueryURL
	BKBaseSurrealDBQueryURL = "http://bkapi.example.com/api/bk-base/prod/v4/queryengine/query_sync/"
	defer func() {
		BKBaseSurrealDBQueryURL = oldQueryURL
	}()

	mockCurl := &mockBKBaseCurl{response: BKBaseResponse{
		Result: true,
		Data:   &BKBaseData{List: []map[string]any{}},
	}}
	client := &BKBaseSurrealDBClient{curl: mockCurl}

	_, err := client.ExecuteWithBinding(context.Background(), "bkcc__10", BindingInfo{
		Database:    "2_graph",
		Namespace:   "mapleleaf_2",
		ClusterName: "surrealdb-alt",
	}, "INFO FOR DB;", 0, 100)

	require.NoError(t, err)
	assert.Equal(t, curl.Post, mockCurl.method)
	assert.Equal(t, BKBaseSurrealDBQueryURL, mockCurl.options.UrlPath)

	var body map[string]any
	require.NoError(t, json.Unmarshal(mockCurl.options.Body, &body))
	assert.Equal(t, PreferStorageSurrealDB, body["prefer_storage"])
	assert.Equal(t, map[string]any{"cluster_name": "surrealdb-alt"}, body["properties"])

	sqlPayloadText, ok := body["sql"].(string)
	require.True(t, ok)

	var payload BKBaseSQLPayload
	require.NoError(t, json.Unmarshal([]byte(sqlPayloadText), &payload))
	assert.Equal(t, "USE NS mapleleaf_2 DB `2_graph`;INFO FOR DB;", payload.DSL)
	assert.Equal(t, "2_graph", payload.ResultTableID)
}

func TestSurrealDBQuerySyncURLUsesBkDataSpaceRoute(t *testing.T) {
	oldQueryURL := BKBaseSurrealDBQueryURL
	BKBaseSurrealDBQueryURL = ""
	defer func() {
		BKBaseSurrealDBQueryURL = oldQueryURL
	}()

	assert.Equal(t, bkapi.GetBkDataAPI().QueryUrl("bkcc__10"), surrealDBQuerySyncURL("bkcc__10"))
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
				{Timestamp: 100000, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
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
				{Timestamp: 100000, Matchers: cmdb.Matchers{{"pod": "nginx-1"}}},
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
