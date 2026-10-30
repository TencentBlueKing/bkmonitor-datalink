// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.
package v1beta1

import (
	"bytes"
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func legacyMetricValue(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "unify_query_"+name {
			continue
		}
		for _, item := range family.Metric {
			matched := true
			for _, label := range item.Label {
				if labels[label.GetName()] != label.GetValue() {
					matched = false
				}
			}
			if matched {
				if item.Counter != nil {
					return item.Counter.GetValue()
				}
				return item.Histogram.GetSampleSum()
			}
		}
	}
	return 0
}

func observeLegacyQuery(t *testing.T, mode, execution, result string) func([]string, int) {
	t.Helper()
	labels := map[string]string{"route": "vm_legacy", "query_mode": mode, "result": result}
	before := legacyMetricValue(t, "cmdb_relation_route_total", labels)
	targetLabels := map[string]string{"route": "vm_legacy", "query_mode": mode, "execution_mode": execution}
	targetBefore := legacyMetricValue(t, "cmdb_relation_target_count", targetLabels)
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	var logs bytes.Buffer
	previousLogger := log.DefaultLogger
	log.SetOutput(&logs)
	log.InitTestLogger()
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
		log.DefaultLogger = previousLogger
		log.SetOutput(nil)
	})
	return func(path []string, targets int) {
		t.Helper()
		require.Equal(t, before+1, legacyMetricValue(t, "cmdb_relation_route_total", labels))
		require.Equal(t, targetBefore+float64(targets), legacyMetricValue(t, "cmdb_relation_target_count", targetLabels))
		var top map[string]attribute.Value
		points := int64(0)
		for _, span := range recorder.Ended() {
			attributes := make(map[string]attribute.Value)
			for _, value := range span.Attributes() {
				attributes[string(value.Key)] = value.Value
			}
			if span.Name() == "get-resource-indexMatcher" {
				top = attributes
			}
			if span.Name() == "query-do-request" {
				require.Equal(t, mode, attributes["query-mode"].AsString())
				points += attributes["query-result-points"].AsInt64()
			}
		}
		require.NotNil(t, top)
		require.Equal(t, path, top["query-hit-path"].AsStringSlice())
		require.Equal(t, execution, top["query-execution-mode"].AsString())
		require.Equal(t, int64(targets), top["query-target-count"].AsInt64())
		require.Equal(t, int64(targets), points)
		if targets > 0 {
			require.NotContains(t, logs.String(), "[query_relation] 查询不到数据")
		}
		if result == "empty" {
			require.Contains(t, logs.String(), "[query_relation] 查询不到数据")
		}
	}
}
