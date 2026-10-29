// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type qualityTestSchema struct{}

func (qualityTestSchema) GetResourcePrimaryKeys(string, v1beta3.ResourceType) []string {
	return []string{"id"}
}

func (qualityTestSchema) GetResourceFields(string, v1beta3.ResourceType) []string {
	return []string{"id"}
}

func (qualityTestSchema) ListResourceTypes(string) []v1beta3.ResourceType {
	return []v1beta3.ResourceType{"source", "target"}
}

func (qualityTestSchema) ListRelationSchemas(string) []v1beta3.RelationSchema {
	return []v1beta3.RelationSchema{{FromType: "source", ToType: "target", RelationType: "source_to_target", Category: v1beta3.RelationCategoryStatic, IsDirectional: true}}
}

type qualityTestBackend struct{ cmdb.CMDB }

func (*qualityTestBackend) QueryRelationTimeGraph(ctx context.Context, _, _ string, _, _ time.Time, _ time.Duration, _ cmdb.Resource, _ []cmdb.Resource, _ []cmdb.RelationPath, matcher, _ cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	switch matcher["id"] {
	case "partial":
		metadata.SetStatus(ctx, metadata.QueryTsPartial, "backend_partial")
	case "coverage-empty":
		metadata.SetStatus(ctx, metadata.QueryTsPartial, "vm_storage_coverage_mismatch")
	case "routes-empty":
		metadata.SetStatus(ctx, metadata.QueryTsPartial, "relation_no_physical_route:metric_a;relation_no_physical_route:metric_b;backend_partial")
	case "unknown-empty":
		metadata.SetStatus(ctx, metadata.QueryTsPartial, "unknown_backend_detail_a;unknown_backend_detail_b")
	case "limited":
		return nil, fmt.Errorf("wrapped: %w", &v1beta3.ResultLimitError{Reason: "max_targets", Count: 3, Limit: 2})
	case "failed":
		return nil, fmt.Errorf("backend unavailable")
	}
	if strings.HasSuffix(matcher["id"], "empty") {
		return nil, nil
	}
	return []cmdb.PathResourcesResult{{Timestamp: 1700000000000, TargetType: "target", Path: []cmdb.PathNode{{ResourceType: "source", Dimensions: matcher}, {ResourceType: "target", Dimensions: cmdb.Matcher{"id": "result"}}}}}, nil
}

func TestLegacyHTTPQualityUsesMetricsWithoutChangingResponse(t *testing.T) {
	log.InitTestLogger()
	for _, rangeQuery := range []bool{false, true} {
		t.Run(fmt.Sprintf("range=%t", rangeQuery), func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			previous := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() {
				otel.SetTracerProvider(previous)
				require.NoError(t, provider.Shutdown(context.Background()))
			})
			metadata.InitMetadata()
			ctx := metadata.InitHashID(context.Background())
			metadata.SetUser(ctx, &metadata.User{SpaceUID: "space"})
			model, err := v1beta3.GetModel(ctx)
			require.NoError(t, err)
			model.SetSchemaProvider(qualityTestSchema{})
			model.SetTimeGraphResolver(func(context.Context, string) (v1beta3.TimeGraphQuerier, error) { return &qualityTestBackend{}, nil })
			t.Cleanup(func() {
				model.SetSchemaProvider(v1beta3.GetSchemaProvider())
				model.SetTimeGraphResolver(func(context.Context, string) (v1beta3.TimeGraphQuerier, error) { return model, nil })
			})
			mode, timeFields := "instant", `"timestamp":1700000000`
			if rangeQuery {
				mode, timeFields = "range", `"start_time":1700000000,"end_time":1700000060,"step":"60s"`
			}
			qualityCounts := func() map[string]float64 {
				families, err := prometheus.DefaultGatherer.Gather()
				require.NoError(t, err)
				counts := make(map[string]float64)
				for _, family := range families {
					if family.GetName() != "unify_query_cmdb_relation_quality_total" {
						continue
					}
					for _, m := range family.GetMetric() {
						labels := make(map[string]string)
						for _, label := range m.GetLabel() {
							labels[label.GetName()] = label.GetValue()
						}
						require.Len(t, labels, 3)
						if labels["query_mode"] == mode {
							counts[labels["quality"]+"/"+labels["reason"]] = m.GetCounter().GetValue()
						}
					}
				}
				return counts
			}
			before := qualityCounts()
			ids := []string{"partial", "complete", "coverage-empty", "empty", "routes-empty", "unknown-empty", "limited", "failed"}
			queries := make([]string, 0, len(ids))
			for _, id := range ids {
				queries = append(queries, `{`+timeFields+`,"source_type":"source","target_type":"target","source_info":{"id":"`+id+`"}}`)
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"query_list":[`+strings.Join(queries, ",")+`]}`)).WithContext(ctx)
			if rangeQuery {
				HandlerAPIRelationV1Beta3MultiResourceRange(c)
			} else {
				HandlerAPIRelationV1Beta3MultiResource(c)
			}
			require.Equal(t, http.StatusOK, w.Code)
			var response struct {
				Data []map[string]any `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.Len(t, response.Data, len(ids))
			for i, item := range response.Data {
				// Assert all keys, including failed and empty items, against the old wire format.
				require.Len(t, item, 7)
				for _, key := range []string{"code", "message", "source_type", "source_info", "target_type", "target_list", "path"} {
					require.Contains(t, item, key)
				}
				targets, ok := item["target_list"].([]any)
				require.True(t, ok, "target_list must be an array, including failures")
				if i >= 6 {
					require.Equal(t, float64(400), item["code"])
					require.NotEmpty(t, item["message"])
					require.Empty(t, targets)
				} else {
					require.Equal(t, float64(200), item["code"])
					require.Empty(t, item["message"])
					if strings.HasSuffix(ids[i], "empty") {
						require.Empty(t, targets)
					} else {
						require.NotEmpty(t, targets)
					}
				}
			}
			after := qualityCounts()
			for key, count := range before {
				after[key] -= count
			}
			require.Equal(t, map[string]float64{
				"partial/backend_partial":              2,
				"partial/vm_storage_coverage_mismatch": 1,
				"partial/relation_no_physical_route":   1,
				"partial/other":                        1,
				"truncated/max_targets":                1,
			}, after)
			itemCount := 0
			for _, span := range recorder.Ended() {
				if span.Name() != "handler-api-relation-multi-resource-v1beta3-item" && span.Name() != "handler-api-relation-multi-resource-range-v1beta3-item" {
					continue
				}
				itemCount++
				attrs := make(map[string]any)
				for _, attr := range span.Attributes() {
					attrs[string(attr.Key)] = attr.Value.AsInterface()
				}
				index := int(attrs["query-index"].(int64))
				reason, partial := map[int]string{
					0: "backend_partial", 2: "vm_storage_coverage_mismatch",
					4: "backend_partial;relation_no_physical_route:metric_a;relation_no_physical_route:metric_b",
					5: "unknown_backend_detail_a;unknown_backend_detail_b",
				}[index]
				if partial {
					require.Equal(t, true, attrs["relation-partial"])
					require.Equal(t, reason, attrs["relation-partial-reason"])
				} else {
					require.NotContains(t, attrs, "relation-partial")
				}
				if ids[index] == "limited" {
					require.Equal(t, true, attrs["relation-truncated"])
					require.Equal(t, "max_targets", attrs["relation-truncated-reason"])
				} else {
					require.NotContains(t, attrs, "relation-truncated")
				}
			}
			require.Equal(t, len(ids), itemCount)
			require.Nil(t, metadata.GetStatus(ctx), "batch items must not pollute request quality")
		})
	}
}
