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
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	queryPkg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	tsdbService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

// TestRawStorageTargetsIntegration 使用真实 ES 的搜索统计验证每个执行目标只查询一次。
func TestRawStorageTargetsIntegration(t *testing.T) {
	esURL := os.Getenv("BKMONITOR_TEST_ES_URL")
	if esURL == "" {
		t.Skip("设置 BKMONITOR_TEST_ES_URL 后执行真实 Elasticsearch 集成测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, err := elastic.NewClient(elastic.SetURL(esURL), elastic.SetSniff(false), elastic.SetHealthcheck(false))
	require.NoError(t, err)
	defer client.Stop()

	log.InitTestLogger()
	metadata.InitMetadata()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		require.NoError(t, provider.Shutdown(context.Background()))
	})
	oldTimeout, oldMaxSize := tsdbService.EsTimeout, tsdbService.EsMaxSize
	oldRouting, oldSettings := QueryMaxRouting, queryRawESBatchSettingsSnapshot.Load()
	tsdbService.EsTimeout, tsdbService.EsMaxSize = 30*time.Second, 1000
	QueryMaxRouting = 4
	queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{
		maxMembers: 16, maxBodyBytes: 1 << 20, maxConcurrentSearches: 4,
	})
	t.Cleanup(func() {
		tsdbService.EsTimeout, tsdbService.EsMaxSize = oldTimeout, oldMaxSize
		QueryMaxRouting = oldRouting
		queryRawESBatchSettingsSnapshot.Store(oldSettings)
	})

	prefix := fmt.Sprintf("uq_raw_targets_%d", time.Now().UnixNano())
	indexes := []string{prefix + "_a", prefix + "_b"}
	storageIDs := []string{prefix + "-a", prefix + "-b"}
	for _, storageID := range storageIDs {
		tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: esURL})
	}
	t.Cleanup(func() {
		_, deleteErr := client.DeleteIndex(indexes...).Do(context.Background())
		require.NoError(t, deleteErr)
	})
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	timestamps := [][]time.Duration{
		{time.Hour, day + time.Hour, 6*day + time.Hour, 7*day + time.Hour, 8*day + time.Hour},
		{3*day + time.Hour, 3*day + 2*time.Hour, 4*day + time.Hour, 4*day + 2*time.Hour, 5*day + time.Hour},
	}
	for target, index := range indexes {
		_, err = client.CreateIndex(index).BodyJson(map[string]any{
			"settings": map[string]any{"number_of_shards": 1, "number_of_replicas": 0},
			"mappings": map[string]any{"properties": map[string]any{
				"event_time": map[string]any{"type": "long"},
				"span_id":    map[string]any{"type": "keyword"},
				"bk_biz_id":  map[string]any{"type": "keyword"},
				"namespace":  map[string]any{"type": "keyword"},
				"service":    map[string]any{"type": "keyword"},
			}},
		}).Do(ctx)
		require.NoError(t, err)
		bulk := client.Bulk().Refresh("wait_for")
		for ordinal, offset := range timestamps[target] {
			spanID := fmt.Sprintf("%c-%d", 'a'+target, ordinal)
			bulk.Add(elastic.NewBulkIndexRequest().Index(index).Id(spanID).Doc(map[string]any{
				"event_time": start.Add(offset).UnixMilli(), "span_id": spanID,
				"bk_biz_id": "2", "namespace": "production", "service": "checkout",
			}))
		}
		result, bulkErr := bulk.Do(ctx)
		require.NoError(t, bulkErr)
		require.False(t, result.Errors)
	}

	newQuery := func(batch bool) *structured.QueryTs {
		return &structured.QueryTs{
			SpaceUid: "bkcc__2", Start: strconv.FormatInt(start.UnixMilli(), 10),
			End: strconv.FormatInt(start.Add(9*day).UnixMilli(), 10), Step: "1m", Timezone: "UTC",
			Limit: 100, OrderBy: structured.OrderBy{"event_time"}, IsESBatch: batch,
			QueryList: []*structured.Query{{
				ReferenceName: "A", TableID: structured.TableID("trace.raw_targets"),
				DataSource: structured.BkApm, KeepColumns: []string{"event_time", "span_id"}, ExactTotal: true,
			}},
			TsDBMap: map[string]structured.TsDBs{"A": {&queryPkg.TsDBV2{
				TableID: "trace.raw_targets", StorageType: metadata.ElasticsearchStorageType,
				StorageID: storageIDs[0], DB: indexes[0], Measurement: "__default__",
				TimeField: metadata.TimeField{Name: "event_time", Type: "long", Unit: "millisecond"},
				Filters:   []queryPkg.Filter{{"bk_biz_id": "2", "namespace": "production", "service": "checkout"}},
				StorageClusterRecords: queryPkg.StorageClusterRecords{
					{
						StorageID: storageIDs[0], StorageType: metadata.ElasticsearchStorageType,
						DB: indexes[0], Measurement: "__default__", EnableTime: start.Add(-day).Unix(),
					},
					{
						StorageID: storageIDs[1], StorageType: metadata.ElasticsearchStorageType,
						DB: indexes[1], Measurement: "__default__", EnableTime: start.Add(3 * day).Unix(),
					},
					{
						StorageID: storageIDs[0], StorageType: metadata.ElasticsearchStorageType,
						DB: indexes[0], Measurement: "__default__", EnableTime: start.Add(6 * day).Unix(),
					},
				},
			}}},
		}
	}
	searchCounts := func(t *testing.T) []int64 {
		t.Helper()
		response, statsErr := client.PerformRequest(ctx, elastic.PerformRequestOptions{
			Method: "GET", Path: "/" + indexes[0] + "," + indexes[1] + "/_stats/search",
		})
		require.NoError(t, statsErr)
		var stats struct {
			Indices map[string]struct {
				Primaries struct {
					Search struct {
						QueryTotal int64 `json:"query_total"`
					} `json:"search"`
				} `json:"primaries"`
			} `json:"indices"`
		}
		require.NoError(t, json.Unmarshal(response.Body, &stats))
		return []int64{stats.Indices[indexes[0]].Primaries.Search.QueryTotal, stats.Indices[indexes[1]].Primaries.Search.QueryTotal}
	}
	assertSpans := func(t *testing.T, executionCounts map[string]int64, wantError bool) {
		t.Helper()
		expected := map[string]map[string]int64{
			"query-raw-with-instance": executionCounts,
			"query-raw-plan": {
				"raw.plan.route_count":         3,
				"raw.plan.query_count":         2,
				"raw.plan.reused_target_count": 1,
				"raw.plan.merge_db_count":      0,
			},
		}
		for name, counts := range expected {
			var matching []tracetest.SpanStub
			for _, span := range exporter.GetSpans() {
				if span.Name == name {
					matching = append(matching, span)
				}
			}
			require.Len(t, matching, 1, "Span %s 应在每次请求中生成一次", name)
			attributes := make(map[string]attribute.Value)
			for _, field := range matching[0].Attributes {
				attributes[string(field.Key)] = field.Value
			}
			for field, count := range counts {
				require.Contains(t, attributes, field, "Span %s", name)
				require.Equal(t, attribute.INT64, attributes[field].Type(), "计数字段 %s 应使用整数类型", field)
				require.Equal(t, count, attributes[field].AsInt64(), "Span %s 的 %s", name, field)
			}
			if name == "query-raw-with-instance" && wantError {
				require.Equal(t, codes.Error, matching[0].Status.Code)
			}
		}
	}
	successfulExecutionCounts := map[string]int64{
		"raw.route_count":                    3,
		"raw.planned_query_count":            2,
		"raw.missing_index_query_count":      0,
		"raw.pagination_skipped_query_count": 0,
		"raw.ready_query_count":              2,
		"raw.succeeded_query_count":          2,
		"raw.failed_query_count":             0,
	}
	execute := func(t *testing.T, request *structured.QueryTs) ([]map[string]any, metadata.ResultTableOptions) {
		t.Helper()
		exporter.Reset()
		before := searchCounts(t)
		requestCtx := metadata.InitHashID(ctx)
		metadata.SetUser(requestCtx, &metadata.User{SpaceUID: "bkcc__2"})
		total, list, options, routes, queryErr := queryRawWithInstance(requestCtx, request)
		require.NoError(t, queryErr)
		require.EqualValues(t, 10, total)
		require.Len(t, routes, 3)
		after := searchCounts(t)
		for target := range indexes {
			require.EqualValues(t, 1, after[target]-before[target], "执行目标 %s 的查询次数", indexes[target])
		}
		assertSpans(t, successfulExecutionCounts, false)
		return list, options
	}
	spanIDs := func(list []map[string]any) []string {
		ids := make([]string, 0, len(list))
		for _, row := range list {
			ids = append(ids, row["span_id"].(string))
		}
		return ids
	}
	expectedIDs := []string{"a-0", "a-1", "b-0", "b-1", "b-2", "b-3", "b-4", "a-2", "a-3", "a-4"}
	for _, batch := range []bool{false, true} {
		t.Run(fmt.Sprintf("batch_%t", batch), func(t *testing.T) {
			t.Run("rows_and_total", func(t *testing.T) {
				list, _ := execute(t, newQuery(batch))
				require.Equal(t, expectedIDs, spanIDs(list))
			})
			t.Run("offset", func(t *testing.T) {
				request := newQuery(batch)
				request.From, request.Limit = 3, 4
				list, _ := execute(t, request)
				require.Equal(t, expectedIDs[3:7], spanIDs(list))
			})
			t.Run("search_after", func(t *testing.T) {
				request := newQuery(batch)
				request.IsSearchAfter, request.Limit = true, 3
				first, options := execute(t, request)
				require.Len(t, first, 6)
				request = newQuery(batch)
				request.IsSearchAfter, request.Limit, request.ResultTableOptions = true, 3, options
				second, _ := execute(t, request)
				require.Len(t, second, 4)
				require.ElementsMatch(t, expectedIDs, append(spanIDs(first), spanIDs(second)...))
			})
			t.Run("completed_target_exact_total", func(t *testing.T) {
				exporter.Reset()
				request := newQuery(batch)
				request.IsSearchAfter = true
				request.ResultTableOptions = metadata.ResultTableOptions{
					"trace.raw_targets|" + storageIDs[0]: {SearchAfter: []any{start.UnixMilli()}},
				}
				before := searchCounts(t)
				requestCtx := metadata.InitHashID(ctx)
				metadata.SetUser(requestCtx, &metadata.User{SpaceUID: "bkcc__2"})
				total, list, _, routes, queryErr := queryRawWithInstance(requestCtx, request)
				require.ErrorContains(t, queryErr, "exact total")
				require.EqualValues(t, 5, total)
				require.Len(t, list, 5)
				require.Len(t, routes, 3)
				after := searchCounts(t)
				require.EqualValues(t, 1, after[0]-before[0])
				require.EqualValues(t, 0, after[1]-before[1])
				assertSpans(t, map[string]int64{
					"raw.route_count":                    3,
					"raw.planned_query_count":            2,
					"raw.missing_index_query_count":      0,
					"raw.pagination_skipped_query_count": 1,
					"raw.ready_query_count":              1,
					"raw.succeeded_query_count":          1,
					"raw.failed_query_count":             0,
				}, true)
			})
			t.Run("scroll", func(t *testing.T) {
				request := newQuery(batch)
				request.Scroll, request.Limit = "1m", 3
				request.QueryList[0].ExactTotal = false
				list, options := execute(t, request)
				require.Len(t, list, 6)
				require.Len(t, options, 2)
				var scrollIDs []string
				for _, option := range options {
					require.NotEmpty(t, option.ScrollID)
					scrollIDs = append(scrollIDs, option.ScrollID)
				}
				t.Cleanup(func() {
					_, clearErr := client.ClearScroll().ScrollId(scrollIDs...).Do(context.Background())
					require.NoError(t, clearErr)
				})
			})
		})
	}
}
