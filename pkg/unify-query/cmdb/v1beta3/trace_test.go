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
	"time"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestBindingResolverTraceRecordsNotFoundAsError(t *testing.T) {
	recorder := setupV1Beta3TraceRecorder(t)
	resolver := &BindingResolver{
		redisLookup: func(context.Context, string, string) (string, error) {
			return "", goRedis.Nil
		},
		cache: make(map[string]*bindingCacheEntry),
	}

	_, err := resolver.Resolve(context.Background(), "bkcc__2")

	require.Error(t, err)
	span := endedSpanByName(t, recorder, "cmdb-v2-binding-resolver")
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.Equal(t, "not-found", traceStringAttribute(t, span, "lookup-result"))
	assert.Equal(t, "no_binding", traceStringAttribute(t, span, "error-category"))
	assert.Equal(t, int64(0), traceIntAttribute(t, span, "cache-expired-removed"))
}

func TestBindingResolverTraceRecordsCapacityEviction(t *testing.T) {
	recorder := setupV1Beta3TraceRecorder(t)
	previousMaxSize := BindingCacheMaxSize
	BindingCacheMaxSize = 1
	t.Cleanup(func() { BindingCacheMaxSize = previousMaxSize })
	resolver := &BindingResolver{
		redisLookup: func(context.Context, string, string) (string, error) {
			return `{"name":"binding-a","bk_biz_id":"2","database":"2_graph_rt","namespace":"mapleleaf_2","phase":"Ok"}`, nil
		},
		cache: map[string]*bindingCacheEntry{
			"other": {info: &BindingInfo{Name: "other"}, expiry: time.Now().Add(time.Hour)},
		},
	}

	_, err := resolver.Resolve(context.Background(), "bkcc__2")

	require.NoError(t, err)
	span := endedSpanByName(t, recorder, "cmdb-v2-binding-resolver")
	assert.Equal(t, codes.Unset, span.Status().Code)
	assert.True(t, traceBoolAttribute(t, span, "cache-evicted"))
	assert.Equal(t, int64(1), traceIntAttribute(t, span, "cache-size"))
}

func TestBKBaseTraceRecordsMalformedSuccessfulResponseAsError(t *testing.T) {
	recorder := setupV1Beta3TraceRecorder(t)
	client := &BKBaseSurrealDBClient{
		curl: &mockBKBaseCurl{response: BKBaseResponse{Result: true, Code: "0"}},
	}

	_, err := client.Execute(context.Background(), "RETURN []", 0, 1)

	require.ErrorContains(t, err, "result=true requires non-null data")
	span := endedSpanByName(t, recorder, "bkbase-surrealdb-execute")
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.True(t, traceBoolAttribute(t, span, "bkbase-result"))
	assert.Equal(t, "0", traceStringAttribute(t, span, "bkbase-code"))
	assert.Equal(t, "parse", traceStringAttribute(t, span, "error-category"))
}

func TestBKBaseTraceRecordsParserFailureAsError(t *testing.T) {
	recorder := setupV1Beta3TraceRecorder(t)
	client := &BKBaseSurrealDBClient{
		curl: &mockBKBaseCurl{response: BKBaseResponse{
			Result: true,
			Code:   "0",
			Data: &BKBaseData{List: []map[string]any{
				{"unexpected": "value"},
			}},
		}},
	}

	_, err := client.Execute(context.Background(), "RETURN []", 0, 1)

	require.ErrorContains(t, err, "missing field")
	span := endedSpanByName(t, recorder, "bkbase-surrealdb-execute")
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.Equal(t, int64(1), traceIntAttribute(t, span, "response-list-count"))
	assert.Equal(t, "parse", traceStringAttribute(t, span, "error-category"))
	assert.Len(t, traceStringAttribute(t, span, "dsl-hash"), 64)
	assert.False(t, traceHasAttribute(span, "dsl"))
}

func TestQueryValidationTraceIncludesRequestContext(t *testing.T) {
	recorder := setupV1Beta3TraceRecorder(t)
	model, err := NewModel(context.Background(), &mockGraphQueryExecutor{})
	require.NoError(t, err)

	_, _, _, err = model.QueryLivenessGraph(context.Background(), &QueryRequest{
		Timestamp:  600000,
		SourceType: ResourceTypeHost,
		SourceInfo: map[string]string{"bk_host_id": "not-an-integer"},
	})

	require.Error(t, err)
	span := endedSpanByName(t, recorder, "cmdb-v2-query-liveness-graph")
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.Equal(t, "resource-validation", traceStringAttribute(t, span, "failure-stage"))
	assert.Equal(t, string(ResourceTypeHost), traceStringAttribute(t, span, "requested-source-type"))
	assert.False(t, traceBoolAttribute(t, span, "legacy-compatibility"))
}

func TestRangeValidationTraceIncludesPointLimit(t *testing.T) {
	recorder := setupV1Beta3TraceRecorder(t)
	previousMaxPoints := MaxRangePoints
	MaxRangePoints = 3
	t.Cleanup(func() { MaxRangePoints = previousMaxPoints })
	model := &Model{}

	_, _, _, _, _, err := model.QueryResourceMatcherRange(
		context.Background(), "", "", "1ms", "0", "3", "", "", nil, nil, false, nil,
	)

	require.ErrorContains(t, err, "more than 3 points")
	span := endedSpanByName(t, recorder, "cmdb-query-resource-matcher-range")
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.Equal(t, "range-points-validation", traceStringAttribute(t, span, "failure-stage"))
	assert.Equal(t, int64(3), traceIntAttribute(t, span, "max-range-points"))
}

func TestPathQueryTraceIncludesFullPathIdentityAndSelection(t *testing.T) {
	recorder := setupV1Beta3TraceRecorder(t)
	model := &Model{}
	path := resourcePath{Steps: []resourcePathStep{
		{ResourceType: string(ResourceTypeNode)},
		{
			ResourceType: string(ResourceTypePod),
			RelationType: string(RelationNodeWithPod),
			Category:     string(RelationCategoryStatic),
			Direction:    string(DirectionOutbound),
		},
	}}
	graph := NewLivenessGraph(0, 1)
	root := &NodeLiveness{ResourceID: "node:1", ResourceType: ResourceTypeNode}
	target := &NodeLiveness{ResourceID: "pod:1", ResourceType: ResourceTypePod}
	graph.RootID = root.ResourceID
	graph.AddNode(root)
	graph.AddNode(target)
	graph.AddEdge(&EdgeLiveness{
		RelationID:   "node_with_pod:1",
		RelationType: RelationNodeWithPod,
		Category:     RelationCategoryStatic,
		Direction:    DirectionOutbound,
		FromID:       root.ResourceID,
		ToID:         target.ResourceID,
	})
	req := &QueryRequest{
		SourceType:         ResourceTypeNode,
		TargetType:         ResourceTypePod,
		TargetTypeExplicit: true,
	}
	runner := func(context.Context, string, int64, int64) ([]*LivenessGraph, error) {
		return []*LivenessGraph{graph}, nil
	}

	graphs, selectedPaths, err := model.executeGraphQueryPathByPath(
		context.Background(), req, GetSchemaProvider(), []resourcePath{path}, 0, 1,
		graphQueryModeInstant, 0, 0, 0, runner,
	)

	require.NoError(t, err)
	require.Len(t, graphs, 1)
	require.Equal(t, []resourcePath{path}, selectedPaths)
	identity := resourcePathSortKey(path)
	aggregateSpan := endedSpanByName(t, recorder, "cmdb-v2-query-graph-paths")
	assert.Equal(t, codes.Unset, aggregateSpan.Status().Code)
	assert.Equal(t, "target-hit", traceStringAttribute(t, aggregateSpan, "selection-result"))
	assert.Equal(t, identity, traceStringAttribute(t, aggregateSpan, "selected-path-identity"))
	assert.Equal(t, int64(1), traceIntAttribute(t, aggregateSpan, "target-hit-path-count"))
	pathSpan := endedSpanByName(t, recorder, "cmdb-v2-query-graph-path")
	assert.Equal(t, codes.Unset, pathSpan.Status().Code)
	assert.Equal(t, aggregateSpan.SpanContext().SpanID(), pathSpan.Parent().SpanID())
	assert.Equal(t, identity, traceStringAttribute(t, pathSpan, "path-identity"))
	assert.Equal(t, "success", traceStringAttribute(t, pathSpan, "path-result"))
	assert.Equal(t, int64(1), traceIntAttribute(t, pathSpan, "edge-count"))
}

func setupV1Beta3TraceRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
	})
	return recorder
}

func endedSpanByName(t *testing.T, recorder *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range recorder.Ended() {
		if span.Name() == name {
			return span
		}
	}
	require.FailNow(t, "span not found", name)
	return nil
}

func traceAttribute(t *testing.T, span sdktrace.ReadOnlySpan, key string) attribute.Value {
	t.Helper()
	for _, item := range span.Attributes() {
		if string(item.Key) == key {
			return item.Value
		}
	}
	require.FailNow(t, "trace attribute not found", key)
	return attribute.Value{}
}

func traceHasAttribute(span sdktrace.ReadOnlySpan, key string) bool {
	for _, item := range span.Attributes() {
		if string(item.Key) == key {
			return true
		}
	}
	return false
}

func traceStringAttribute(t *testing.T, span sdktrace.ReadOnlySpan, key string) string {
	t.Helper()
	return traceAttribute(t, span, key).AsString()
}

func traceIntAttribute(t *testing.T, span sdktrace.ReadOnlySpan, key string) int64 {
	t.Helper()
	return traceAttribute(t, span, key).AsInt64()
}

func traceBoolAttribute(t *testing.T, span sdktrace.ReadOnlySpan, key string) bool {
	t.Helper()
	return traceAttribute(t, span, key).AsBool()
}
