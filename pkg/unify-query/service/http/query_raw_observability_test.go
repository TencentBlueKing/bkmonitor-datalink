// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	queryPkg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestRawQueryObservabilityFilteredTargets(t *testing.T) {
	log.InitTestLogger()
	metadata.InitMetadata()
	exporter := rawQueryObservationRecorder(t)
	oldRouting := QueryMaxRouting
	QueryMaxRouting = 4
	t.Cleanup(func() { QueryMaxRouting = oldRouting })
	for _, reason := range []string{metric.RawQueryDecisionMissingIndex, metric.RawQueryDecisionPaginationFinished} {
		for _, batch := range []bool{false, true} {
			for _, exact := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/batch_%t/exact_%t", reason, batch, exact), func(t *testing.T) {
					exporter.Reset()
					ctx := metadata.InitHashID(context.Background())
					request := rawQueryObservationRequest()
					request.IsESBatch = batch
					request.QueryList[0].ExactTotal = exact
					counts := map[string]int64{
						"raw.route_count": 1, "raw.planned_query_count": 1,
						"raw.missing_index_query_count": 0, "raw.pagination_skipped_query_count": 0,
						"raw.ready_query_count": 0, "raw.succeeded_query_count": 0, "raw.failed_query_count": 0,
					}
					if reason == metric.RawQueryDecisionMissingIndex {
						request.TsDBMap["A"][0].DB = ""
						counts["raw.missing_index_query_count"] = 1
					} else {
						request.IsSearchAfter = true
						request.ResultTableOptions = metadata.ResultTableOptions{"finished": {}}
						counts["raw.pagination_skipped_query_count"] = 1
					}
					before := rawQueryObservationDecision(t, reason)
					_, list, _, _, err := queryRawWithInstance(ctx, request)
					require.Empty(t, list)
					if exact {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
					}
					require.Equal(t, before+1, rawQueryObservationDecision(t, reason))
					rawQueryObservationAssert(t, exporter, counts, exact)
				})
			}
		}
	}
}

func TestRawQueryObservabilityUnavailableInstance(t *testing.T) {
	log.InitTestLogger()
	metadata.InitMetadata()
	exporter := rawQueryObservationRecorder(t)
	oldRouting := QueryMaxRouting
	QueryMaxRouting = 4
	t.Cleanup(func() { QueryMaxRouting = oldRouting })
	for _, batch := range []bool{false, true} {
		t.Run(fmt.Sprintf("batch_%t", batch), func(t *testing.T) {
			exporter.Reset()
			request := rawQueryObservationRequest()
			request.IsESBatch = batch
			request.TsDBMap["A"][0].StorageType = "unsupported-storage"
			_, _, _, _, err := queryRawWithInstance(metadata.InitHashID(context.Background()), request)
			require.Error(t, err)
			rawQueryObservationAssert(t, exporter, map[string]int64{
				"raw.route_count": 1, "raw.planned_query_count": 1,
				"raw.missing_index_query_count": 0, "raw.pagination_skipped_query_count": 0,
				"raw.ready_query_count": 1, "raw.succeeded_query_count": 0, "raw.failed_query_count": 1,
			}, true)
		})
	}
}

func rawQueryObservationRequest() *structured.QueryTs {
	return &structured.QueryTs{
		SpaceUid: "bkcc__2", Start: "1700000000", End: "1700000600", Step: "1m", Timezone: "UTC", Limit: 10,
		QueryList: []*structured.Query{{ReferenceName: "A", TableID: "trace.observation", DataSource: structured.BkApm}},
		TsDBMap: map[string]structured.TsDBs{"A": {&queryPkg.TsDBV2{
			TableID: "trace.observation", StorageType: metadata.ElasticsearchStorageType,
			StorageID: "observation-storage", DB: "observation-index",
		}}},
	}
}

func rawQueryObservationRecorder(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})
	return exporter
}

func rawQueryObservationAssert(t *testing.T, exporter *tracetest.InMemoryExporter, counts map[string]int64, failed bool) {
	t.Helper()
	for _, span := range exporter.GetSpans() {
		if span.Name != "query-raw-with-instance" {
			continue
		}
		values := make(map[string]attribute.Value)
		for _, field := range span.Attributes {
			values[string(field.Key)] = field.Value
		}
		for name, count := range counts {
			require.Contains(t, values, name)
			require.Equal(t, attribute.INT64, values[name].Type(), name)
			require.Equal(t, count, values[name].AsInt64(), name)
		}
		if failed {
			require.Equal(t, codes.Error, span.Status.Code)
			require.NotEmpty(t, span.Events)
		} else {
			require.Equal(t, codes.Unset, span.Status.Code)
		}
		return
	}
	t.Fatal("缺少 query-raw-with-instance Span")
}

func rawQueryObservationDecision(t *testing.T, reason string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "unify_query_raw_query_decisions_total" {
			continue
		}
		for _, counter := range family.GetMetric() {
			labels := make(map[string]string)
			for _, label := range counter.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["reason"] == reason && labels["version"] == config.Version && labels["commit_id"] == config.CommitHash {
				return counter.GetCounter().GetValue()
			}
		}
	}
	return 0
}
