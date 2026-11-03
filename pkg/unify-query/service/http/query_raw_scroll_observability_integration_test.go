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
	"os"
	"strconv"
	"testing"
	"time"

	goRedis "github.com/go-redis/redis/v8"
	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	tsdbService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

// TestRawScrollObservabilityIntegration 使用真实 Redis、ES 和 OTel 验证下载切片统计。
// Redis 使用进程单例，本用例应独立运行；两个环境变量未同时设置时跳过。
// BKMONITOR_TEST_ES_URL=http://127.0.0.1:19367 BKMONITOR_TEST_REDIS_ADDR=127.0.0.1:19368 go test ./service/http -run '^TestRawScrollObservabilityIntegration$' -count=1
func TestRawScrollObservabilityIntegration(t *testing.T) {
	esURL, redisAddr := os.Getenv("BKMONITOR_TEST_ES_URL"), os.Getenv("BKMONITOR_TEST_REDIS_ADDR")
	if esURL == "" || redisAddr == "" {
		t.Skip("设置 BKMONITOR_TEST_ES_URL 和 BKMONITOR_TEST_REDIS_ADDR 后独立运行真实下载集成测试")
	}
	require.Nil(t, redisUtil.Client(), "Redis 为进程单例，请使用注释中的独立测试命令")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	log.InitTestLogger()
	metadata.InitMetadata()
	prefix := fmt.Sprintf("uq_scroll_observability_%d", time.Now().UnixNano())
	require.NoError(t, redisUtil.SetInstance(ctx, prefix, prefix, &goRedis.UniversalOptions{
		Addrs: []string{redisAddr}, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
	}))
	t.Cleanup(redisUtil.Close)
	require.NoError(t, redisUtil.Client().Ping(ctx).Err())

	client, err := elastic.NewClient(elastic.SetURL(esURL), elastic.SetSniff(false), elastic.SetHealthcheck(false))
	require.NoError(t, err)
	t.Cleanup(client.Stop)
	_, err = client.CreateIndex(prefix).BodyJson(map[string]any{
		"settings": map[string]any{"number_of_shards": 1, "number_of_replicas": 0},
		"mappings": map[string]any{"properties": map[string]any{
			"event_time": map[string]any{"type": "long"},
			"span_id":    map[string]any{"type": "keyword"},
		}},
	}).Do(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, deleteErr := client.DeleteIndex(prefix).Do(context.Background())
		require.NoError(t, deleteErr)
	})
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	bulk := client.Bulk().Refresh("wait_for")
	for ordinal := range 24 {
		spanID := strconv.Itoa(ordinal)
		bulk.Add(elastic.NewBulkIndexRequest().Index(prefix).Id(spanID).Doc(map[string]any{
			"event_time": start.Add(time.Duration(ordinal) * time.Minute).UnixMilli(), "span_id": spanID,
		}))
	}
	indexed, err := bulk.Do(ctx)
	require.NoError(t, err)
	require.False(t, indexed.Errors)
	tsdb.SetStorage(prefix, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: esURL})
	oldTimeout, oldMaxSize, oldRouting := tsdbService.EsTimeout, tsdbService.EsMaxSize, QueryMaxRouting
	tsdbService.EsTimeout, tsdbService.EsMaxSize, QueryMaxRouting = 10*time.Second, 100, 2
	t.Cleanup(func() {
		tsdbService.EsTimeout, tsdbService.EsMaxSize, QueryMaxRouting = oldTimeout, oldMaxSize, oldRouting
	})
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		require.NoError(t, provider.Shutdown(context.Background()))
	})
	newQuery := func() *structured.QueryTs {
		return &structured.QueryTs{
			SpaceUid: "bkcc__2", Start: strconv.FormatInt(start.UnixMilli(), 10),
			End: strconv.FormatInt(start.Add(24*time.Hour).UnixMilli(), 10), Step: "1m", Timezone: "UTC",
			Scroll: "1m", OrderBy: structured.OrderBy{"event_time"},
			QueryList: []*structured.Query{{ReferenceName: "A", DataSource: structured.BkApm, TableID: "trace.scroll"}},
			TsDBMap: map[string]structured.TsDBs{"A": {{
				TableID: "trace.scroll", StorageID: prefix, StorageType: metadata.ElasticsearchStorageType,
				DB: prefix, Measurement: "__default__",
				TimeField: metadata.TimeField{Name: "event_time", Type: "long", Unit: "millisecond"},
			}}},
		}
	}
	newSession := func(name string) *redisUtil.ScrollSession {
		session := redisUtil.NewScrollSession(prefix+name, time.Minute, time.Second, 2, 3, 3)
		sliceKey := "trace.scroll|" + prefix + "|1"
		completed := session.Slice(sliceKey)
		completed.Status = redisUtil.StatusCompleted
		session.UpdateSliceStatus(sliceKey, completed)
		t.Cleanup(func() {
			require.NoError(t, redisUtil.Client().Del(context.Background(), session.SessionKey, session.LockKey).Err())
		})
		return session
	}
	execute := func(session *redisUtil.ScrollSession) ([]map[string]any, bool) {
		exporter.Reset()
		queryCtx := metadata.InitHashID(ctx)
		metadata.SetUser(queryCtx, &metadata.User{SpaceUID: "bkcc__2"})
		total, list, _, routes, done, queryErr := queryRawWithScroll(queryCtx, newQuery(), session)
		require.NoError(t, queryErr)
		require.EqualValues(t, len(list), total)
		require.Len(t, routes, 1)
		return list, done
	}
	assertCounts := func(expected map[string]int64) {
		var spans []tracetest.SpanStub
		for _, span := range exporter.GetSpans() {
			if span.Name == "query-raw-with-scroll" {
				spans = append(spans, span)
			}
		}
		require.Len(t, spans, 1)
		require.NotEqual(t, codes.Error, spans[0].Status.Code)
		attributes := make(map[string]attribute.Value)
		for _, field := range spans[0].Attributes {
			attributes[string(field.Key)] = field.Value
		}
		for field, count := range expected {
			require.Contains(t, attributes, field)
			require.Equal(t, attribute.INT64, attributes[field].Type(), field)
			require.Equal(t, count, attributes[field].AsInt64(), field)
		}
	}
	counts := map[string]int64{
		"raw_route_count": 1, "raw_planned_query_count": 1, "raw_missing_index_query_count": 0, "raw_ready_query_count": 1,
		"raw_slice_candidate_count": 2, "raw_slice_completed_skip_count": 1,
		"raw_slice_attempted_count": 1, "raw_slice_succeeded_count": 1, "raw_slice_failed_count": 0,
		"raw_slice_preparation_failed_count": 0, "raw_slice_submission_failed_count": 0,
	}
	session := newSession("-success")
	list, done := execute(session)
	require.Len(t, list, 3)
	require.False(t, done)
	assertCounts(counts)
	// 实际读完剩余切片后，下一次请求应跳过两个已完成切片。
	for page := 0; !done && page < 24; page++ {
		_, done = execute(session)
	}
	require.True(t, done)
	list, done = execute(session)
	require.Empty(t, list)
	require.True(t, done)
	counts["raw_slice_completed_skip_count"] = 2
	counts["raw_slice_attempted_count"], counts["raw_slice_succeeded_count"] = 0, 0
	assertCounts(counts)

	// 真实关闭索引触发查询错误，未达到重试阈值时仍保留原先的成功返回语义。
	_, err = client.CloseIndex(prefix).Do(ctx)
	require.NoError(t, err)
	failedSession := newSession("-retry")
	list, done = execute(failedSession)
	require.Empty(t, list)
	require.False(t, done)
	counts["raw_slice_completed_skip_count"] = 1
	counts["raw_slice_attempted_count"], counts["raw_slice_failed_count"] = 1, 1
	assertCounts(counts)
	retrying := failedSession.Slice("trace.scroll|" + prefix + "|0")
	require.Equal(t, 1, retrying.FailedNum)
	require.Equal(t, redisUtil.StatusPending, retrying.Status)
	// 子查询保留真实 ES 错误，根 Span 仅通过失败切片计数暴露本次可重试失败。
	foundError := false
	for _, span := range exporter.GetSpans() {
		if span.Name == "elasticsearch-query-raw" && span.Status.Code == codes.Error {
			foundError = true
		}
	}
	require.True(t, foundError)
}
