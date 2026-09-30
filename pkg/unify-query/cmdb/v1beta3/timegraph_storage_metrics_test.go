// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestSharedTopologyStorageObservability(t *testing.T) {
	for _, test := range []struct {
		name, outcome, reason                     string
		graphLimit, outputLimit, canceled, failed bool
	}{
		{name: "success", outcome: "success"},
		{name: "build rejection", outcome: "rejected", reason: "max_graph_edges", graphLimit: true},
		{name: "output rejection", outcome: "success", reason: "max_topology_output_elements", outputLimit: true},
		{name: "backend cancellation", outcome: "canceled", canceled: true},
		{name: "backend error", outcome: "failed", failed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			oldEdges, oldOutput := MaxGraphEdges, MaxSharedTopologyOutputElements
			t.Cleanup(func() { MaxGraphEdges, MaxSharedTopologyOutputElements = oldEdges, oldOutput })
			if test.graphLimit {
				MaxGraphEdges = 1
			}
			if test.outputLimit {
				MaxSharedTopologyOutputElements = 1
			}
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			previous := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() { otel.SetTracerProvider(previous); require.NoError(t, provider.Shutdown(context.Background())) })
			stamps := []int64{1700000000000, 1700000100000}
			model := sharedTopologyQueryModel(map[string]pl.Matrix{
				"source_info_relation": contractMatrix(map[string]string{"source_id": "a"}, stamps...),
				"source_middle_flow":   contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, stamps...),
				"middle_target_flow":   {},
			})
			backend := model.timeGraphVMQuery
			model.timeGraphVMQuery = func(c context.Context, q *structured.QueryTs, e string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
				if q.QueryList[0].FieldName == "source_middle_flow" {
					if test.canceled {
						return nil, context.Canceled
					}
					if test.failed {
						return nil, fmt.Errorf("backend failed")
					}
				}
				return backend(c, q, e, instant, start, end, step)
			}
			storageLabels := func(kind string) map[string]string {
				return map[string]string{"storage": "shared", "kind": kind, "result": test.outcome}
			}
			before := map[string]float64{}
			for _, kind := range []string{"nodes", "relations", "attribute_versions", "legacy_graphs", "edge_time_entries", "node_time_entries"} {
				before[kind] = readTimeGraphMetric(t, "cmdb_timegraph_storage_size", storageLabels(kind), "sum")
			}
			countBefore := readTimeGraphMetric(t, "cmdb_timegraph_storage_size", storageLabels("nodes"), "count")
			_, err := model.QuerySharedTopology(ctx, cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, StartTime: 1700000000, EndTime: 1700000100, Step: "100s", MaxHops: 1})
			if test.name == "success" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, countBefore+1, readTimeGraphMetric(t, "cmdb_timegraph_storage_size", storageLabels("nodes"), "count"))
			require.Equal(t, before["legacy_graphs"], readTimeGraphMetric(t, "cmdb_timegraph_storage_size", storageLabels("legacy_graphs"), "sum"))
			if test.outcome == "success" {
				for kind, want := range map[string]float64{"nodes": 2, "relations": 1, "attribute_versions": 2, "edge_time_entries": 2, "node_time_entries": 4} {
					require.Equal(t, before[kind]+want, readTimeGraphMetric(t, "cmdb_timegraph_storage_size", storageLabels(kind), "sum"), kind)
				}
			}
			require.Zero(t, readTimeGraphMetric(t, "cmdb_topology_admission_active", nil, "gauge"))
			require.Len(t, recorder.Ended(), len(recorder.Started()))
			spans := map[string]sdktrace.ReadOnlySpan{}
			for _, span := range recorder.Ended() {
				spans[span.Name()] = span
			}
			build := spans["build-time-graph-from-relations"]
			require.NotNil(t, build)
			attrs := map[string]any{}
			for _, a := range build.Attributes() {
				attrs[string(a.Key)] = a.Value.AsInterface()
			}
			require.Equal(t, int64(0), attrs["graph-legacy-timepoint-count"])
			require.Equal(t, "shared", attrs["graph-storage-mode"])
			require.GreaterOrEqual(t, attrs["matrix-query-duration-seconds"].(float64), 0.0)
			require.GreaterOrEqual(t, attrs["local-build-duration-seconds"].(float64), 0.0)
			if test.outcome != "success" {
				require.Equal(t, codes.Error, build.Status().Code)
				require.GreaterOrEqual(t, attrs["graph-attribute-version-count"].(int64), int64(1))
			}
			if test.reason != "" {
				var limit *ResultLimitError
				require.ErrorAs(t, err, &limit)
				var matching int
				for _, span := range recorder.Ended() {
					values := map[string]any{}
					for _, a := range span.Attributes() {
						values[string(a.Key)] = a.Value.AsInterface()
					}
					if values["limit-reason"] == test.reason {
						matching++
						require.Equal(t, int64(limit.Count), values["limit-count"])
						require.Equal(t, int64(limit.Limit), values["limit-maximum"])
					}
				}
				require.GreaterOrEqual(t, matching, 2)
			}
			if test.outcome == "success" {
				for _, name := range []string{"timegraph-admission", "timegraph-release-admission", "timegraph-clean", "timegraph-create-storage", "timegraph-plan-topology-fetch", "timegraph-validate-matrix", "timegraph-apply-source-info-matrix", "timegraph-apply-relation-matrix", "timegraph-propagate-topology"} {
					require.NotNil(t, spans[name], name)
				}
				require.NotNil(t, spans["timegraph-materialize-topology"])
				if test.outputLimit {
					require.Equal(t, codes.Error, spans["timegraph-materialize-topology"].Status().Code)
				} else {
					require.NotNil(t, spans["timegraph-convert-topology"])
				}
			}
		})
	}
}

// Exercise real localhost HTTP, JSON decoding and VM Matrix conversion; increasing
// series count must not create a span for each node or edge.
func TestSharedTopologyFullPipelineTrace(t *testing.T) {
	var previousCount int
	for _, edges := range []int{1, 64} {
		t.Run(strconv.Itoa(edges), func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			old := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() { otel.SetTracerProvider(old); require.NoError(t, provider.Shutdown(context.Background())) })
			t.Setenv("TG_PIPELINE_MODE", "shared")
			t.Setenv("TG_EDGES", strconv.Itoa(edges))
			t.Setenv("TG_POINTS", "3")
			t.Setenv("TG_CONCURRENCY", "1")
			TestSharedTopologyPipelineProbe(t)
			require.Len(t, recorder.Ended(), len(recorder.Started()))
			names := map[string]int{}
			for _, span := range recorder.Ended() {
				names[span.Name()]++
			}
			for _, name := range []string{"http-curl-response-headers", "http-curl-read-body", "http-curl-json-decode", "victoria-metrics-matrixFormat", "timegraph-validate-matrix", "timegraph-apply-relation-matrix", "timegraph-propagate-topology", "timegraph-materialize-topology", "timegraph-clean"} {
				require.Positive(t, names[name], name)
			}
			if previousCount > 0 {
				require.Len(t, recorder.Ended(), previousCount, "span count must not grow with series count")
			}
			previousCount = len(recorder.Ended())
		})
	}
}
