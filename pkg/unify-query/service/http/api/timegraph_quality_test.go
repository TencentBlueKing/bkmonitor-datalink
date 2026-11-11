// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

func TestLegacyRelationQualityTelemetry(t *testing.T) {
	metadata.InitMetadata()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})
	for _, mode := range []string{"instant", "range"} {
		counts := func() map[string]float64 {
			families, err := prometheus.DefaultGatherer.Gather()
			require.NoError(t, err)
			result := make(map[string]float64)
			for _, family := range families {
				if family.GetName() != "unify_query_cmdb_relation_quality_total" {
					continue
				}
				for _, item := range family.GetMetric() {
					labels := make(map[string]string)
					for _, label := range item.GetLabel() {
						labels[label.GetName()] = label.GetValue()
					}
					require.Len(t, labels, 4)
					if labels["entry"] == "compat" && labels["query_mode"] == mode {
						result[labels["quality"]+"/"+labels["reason"]] = item.GetCounter().GetValue()
					}
				}
			}
			return result
		}
		before := counts()
		for _, tc := range []struct {
			name, partial, truncated string
			err                      error
		}{
			{name: "complete"},
			{name: "partial", partial: "backend_partial"},
			{name: "coverage", partial: "vm_storage_coverage_mismatch"},
			{name: "routes", partial: "relation_no_physical_route:metric_a;relation_no_physical_route:metric_b"},
			{name: "unknown", partial: "unknown_backend_detail_a;unknown_backend_detail_b"},
			{name: "limited", truncated: "max_response_bytes", err: fmt.Errorf("wrapped: %w", &v1beta3.ResultLimitError{Reason: "max_response_bytes", Count: 3, Limit: 2})},
			{name: "failed", err: fmt.Errorf("backend unavailable")},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				ctx, span := trace.NewSpan(metadata.InitHashID(context.Background()), "legacy-quality")
				if tc.partial != "" {
					metadata.SetStatus(ctx, metadata.QueryTsPartial, tc.partial)
				}
				observeLegacyRelationQuality(ctx, span, mode, tc.err)
				span.End(&tc.err)
				ended := recorder.Ended()
				attrs := make(map[string]any)
				for _, attr := range ended[len(ended)-1].Attributes() {
					attrs[string(attr.Key)] = attr.Value.AsInterface()
				}
				for key, reason := range map[string]string{"partial": tc.partial, "truncated": tc.truncated} {
					if reason == "" {
						require.NotContains(t, attrs, "relation-"+key)
					} else {
						require.Equal(t, true, attrs["relation-"+key])
						require.Equal(t, reason, attrs["relation-"+key+"-reason"])
					}
				}
			})
		}
		after := counts()
		for key, value := range before {
			after[key] -= value
		}
		require.Equal(t, map[string]float64{
			"partial/backend_partial": 1, "partial/vm_storage_coverage_mismatch": 1,
			"partial/relation_no_physical_route": 1, "partial/other": 1, "truncated/max_response_bytes": 1,
		}, after)
	}
}
