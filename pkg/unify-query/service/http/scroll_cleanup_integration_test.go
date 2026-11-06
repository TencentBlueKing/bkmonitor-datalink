package http

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goRedis "github.com/go-redis/redis/v8"
	elastic "github.com/olivere/elastic/v7"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/bkapi"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	tsdbService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

type scrollCleanupCluster struct {
	address   string
	username  string
	password  string
	storageID string
	index     string
	client    *elastic.Client
}

func TestScrollCleanupIntegration(testContext *testing.T) {
	firstURL, secondURL, redisAddr := os.Getenv("BKMONITOR_TEST_ES_URL"), os.Getenv("BKMONITOR_TEST_ES_SECOND_URL"), os.Getenv("BKMONITOR_TEST_REDIS_ADDR")
	if firstURL == "" || secondURL == "" || redisAddr == "" {
		testContext.Skip("设置两个独立 ES URL 与 Redis 地址后独立运行真实清理集成测试")
	}
	require.Nil(testContext, redisUtil.Client())
	metadata.InitMetadata()
	log.InitTestLogger()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	prefix := fmt.Sprintf("uq_scroll_cleanup_%d", time.Now().UnixNano())
	require.NoError(testContext, redisUtil.SetInstance(ctx, prefix, prefix, &goRedis.UniversalOptions{
		Addrs: []string{redisAddr}, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
	}))
	testContext.Cleanup(redisUtil.Close)
	require.NoError(testContext, redisUtil.Client().Ping(ctx).Err())
	oldTimeout, oldMaxSize, oldRouting := tsdbService.EsTimeout, tsdbService.EsMaxSize, QueryMaxRouting
	tsdbService.EsTimeout, tsdbService.EsMaxSize, QueryMaxRouting = 10*time.Second, 100, 4
	testContext.Cleanup(func() {
		tsdbService.EsTimeout, tsdbService.EsMaxSize, QueryMaxRouting = oldTimeout, oldMaxSize, oldRouting
	})
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	testContext.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		require.NoError(testContext, provider.Shutdown(context.Background()))
	})

	start := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	var expectedEventIDs []string
	expectedEventTimes := make(map[string]int64)
	clusters := []scrollCleanupCluster{
		{address: firstURL, username: os.Getenv("BKMONITOR_TEST_ES_USERNAME"), password: os.Getenv("BKMONITOR_TEST_ES_PASSWORD"), storageID: prefix + "_a", index: prefix + "_a"},
		{address: secondURL, username: os.Getenv("BKMONITOR_TEST_ES_SECOND_USERNAME"), password: os.Getenv("BKMONITOR_TEST_ES_SECOND_PASSWORD"), storageID: prefix + "_b", index: prefix + "_b"},
	}
	for clusterIndex := range clusters {
		cluster := &clusters[clusterIndex]
		client, err := elastic.NewClient(elastic.SetURL(cluster.address), elastic.SetBasicAuth(cluster.username, cluster.password), elastic.SetSniff(false), elastic.SetHealthcheck(false))
		require.NoError(testContext, err)
		cluster.client = client
		testContext.Cleanup(client.Stop)
		_, err = client.CreateIndex(cluster.index).BodyJson(map[string]any{
			"settings": map[string]any{"number_of_shards": 1, "number_of_replicas": 0},
			"mappings": map[string]any{"properties": map[string]any{
				"event_time": map[string]any{"type": "long"}, "event_id": map[string]any{"type": "keyword"},
			}},
		}).Do(ctx)
		require.NoError(testContext, err)
		testContext.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			_, deleteErr := client.DeleteIndex(cluster.index).Do(cleanupCtx)
			require.NoError(testContext, deleteErr)
		})
		bulk := client.Bulk().Refresh("wait_for")
		for ordinal := range 3 + clusterIndex*12 {
			eventID := fmt.Sprintf("%d-%d", clusterIndex, ordinal)
			expectedEventIDs = append(expectedEventIDs, eventID)
			expectedEventTimes[eventID] = start.Add(time.Duration(ordinal) * time.Minute).UnixMilli()
			bulk.Add(elastic.NewBulkIndexRequest().Index(cluster.index).Id(eventID).Doc(map[string]any{
				"event_time": start.Add(time.Duration(ordinal) * time.Minute).UnixMilli(), "event_id": eventID,
			}))
		}
		indexed, err := bulk.Do(ctx)
		require.NoError(testContext, err)
		require.False(testContext, indexed.Errors)
		tsdb.SetStorage(cluster.storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: cluster.address, Username: cluster.username, Password: cluster.password})
		info, _, err := client.Ping(cluster.address).Do(ctx)
		require.NoError(testContext, err)
		testContext.Logf("cluster=%d ES=%s storage=%s", clusterIndex, info.Version.Number, cluster.storageID)
	}
	firstInfo, _, err := clusters[0].client.Ping(firstURL).Do(ctx)
	require.NoError(testContext, err)
	secondInfo, _, err := clusters[1].client.Ping(secondURL).Do(ctx)
	require.NoError(testContext, err)
	require.NotEqual(testContext, firstInfo.ClusterName, secondInfo.ClusterName)

	newQuery := func() *structured.QueryTs {
		query := &structured.QueryTs{
			SpaceUid: "bkcc__2", Start: strconv.FormatInt(start.UnixMilli(), 10),
			End: strconv.FormatInt(start.Add(24*time.Hour).UnixMilli(), 10), Step: "1m", Timezone: "UTC",
			Scroll: "5m", OrderBy: structured.OrderBy{"event_time"}, TsDBMap: make(map[string]structured.TsDBs),
		}
		for clusterIndex, cluster := range clusters {
			reference := strconv.Itoa(clusterIndex)
			tableID := "trace.cleanup_" + reference
			query.QueryList = append(query.QueryList, &structured.Query{ReferenceName: reference, DataSource: structured.BkApm, TableID: structured.TableID(tableID)})
			query.TsDBMap[reference] = structured.TsDBs{{
				TableID: tableID, StorageID: cluster.storageID, StorageType: metadata.ElasticsearchStorageType,
				DB: cluster.index, Measurement: "__default__",
				TimeField: metadata.TimeField{Name: "event_time", Type: "long", Unit: "millisecond"},
			}}
		}
		return query
	}
	queryContext := func() context.Context {
		queryCtx := metadata.InitHashID(ctx)
		metadata.SetUser(queryCtx, &metadata.User{Key: "backend:scroll-cleanup-integration", SpaceUID: "bkcc__2"})
		return queryCtx
	}
	newSession := func(testContext *testing.T, name string) *redisUtil.ScrollSession {
		session := redisUtil.NewScrollSession(prefix+name, 5*time.Minute, 10*time.Second, 2, redisUtil.DefaultSliceMaxFailedNum, 2)
		testContext.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			for _, slice := range session.Slices() {
				if slice.ScrollID == "" {
					continue
				}
				for _, cluster := range clusters {
					if slice.ScrollTarget != nil && slice.ScrollTarget.StorageID == cluster.storageID {
						_, clearErr := cluster.client.ClearScroll().ScrollId(slice.ScrollID).Do(cleanupCtx)
						require.True(testContext, clearErr == nil || elastic.IsNotFound(clearErr), "%v", clearErr)
					}
				}
			}
			require.NoError(testContext, redisUtil.Client().Del(cleanupCtx, session.SessionKey, session.LockKey).Err())
		})
		return session
	}
	scrollCount := func(testContext *testing.T, clusterIndex int) int64 {
		stats, err := clusters[clusterIndex].client.NodesStats().Metric("indices").IndexMetric("search").Do(ctx)
		require.NoError(testContext, err)
		var count int64
		for _, node := range stats.Nodes {
			count += node.Indices.Search.ScrollCurrent
		}
		return count
	}
	assertNoScrolls := func(testContext *testing.T) {
		for clusterIndex := range clusters {
			require.Eventually(testContext, func() bool { return scrollCount(testContext, clusterIndex) == 0 }, time.Second, 20*time.Millisecond)
		}
	}
	createScroll := func(testContext *testing.T, clusterIndex int, keepAlive string) string {
		result, err := clusters[clusterIndex].client.Scroll(clusters[clusterIndex].index).Size(1).Scroll(keepAlive).Do(ctx)
		require.NoError(testContext, err)
		require.NotEmpty(testContext, result.ScrollId)
		return result.ScrollId
	}
	completedSlice := func(session *redisUtil.ScrollSession, key string, clusterIndex int, scrollID string) {
		slice := session.Slice(key)
		slice.ScrollID, slice.Status = scrollID, redisUtil.StatusCompleted
		slice.ScrollTarget = &redisUtil.ElasticsearchScrollTarget{StorageID: clusters[clusterIndex].storageID, SpaceUID: "bkcc__2"}
		session.UpdateSliceStatus(key, slice)
	}
	execute := func(testContext *testing.T, session *redisUtil.ScrollSession, query *structured.QueryTs) ([]map[string]any, bool, error) {
		total, list, _, _, done, err := queryRawWithScroll(queryContext(), query, session)
		require.EqualValues(testContext, len(list), total)
		for _, event := range list {
			eventID, ok := event["event_id"].(string)
			require.True(testContext, ok)
			require.Contains(testContext, expectedEventTimes, eventID)
			eventTime, parseErr := strconv.ParseInt(fmt.Sprint(event["event_time"]), 10, 64)
			require.NoError(testContext, parseErr)
			require.Equal(testContext, expectedEventTimes[eventID], eventTime)
		}
		return list, done, err
	}
	readAll := func(testContext *testing.T, session *redisUtil.ScrollSession) []string {
		var eventIDs []string
		for page := range 40 {
			list, done, err := execute(testContext, session, newQuery())
			require.NoError(testContext, err)
			for _, event := range list {
				eventIDs = append(eventIDs, event["event_id"].(string))
			}
			if done {
				require.True(testContext, session.Done())
				return eventIDs
			}
			require.Less(testContext, page, 39, "session did not reach EOF")
		}
		return nil
	}

	testContext.Run("stop_clears_and_preserves_state_ttl_and_unlock", func(testContext *testing.T) {
		session := newSession(testContext, "stop-lifecycle")
		for clusterIndex := range clusters {
			completedSlice(session, clusters[clusterIndex].storageID, clusterIndex, createScroll(testContext, clusterIndex, "5m"))
		}
		require.NoError(testContext, session.Start(queryContext()))
		require.EqualValues(testContext, 1, redisUtil.Client().Exists(ctx, session.LockKey).Val())
		require.NoError(testContext, session.Stop(queryContext(), prometheus.ClearScroll))
		assertNoScrolls(testContext)
		require.Zero(testContext, redisUtil.Client().Exists(ctx, session.LockKey).Val())
		stored, err := redisUtil.Client().Get(ctx, session.SessionKey).Bytes()
		require.NoError(testContext, err)
		var restored redisUtil.ScrollSession
		require.NoError(testContext, restored.UnmarshalBinary(stored))
		require.True(testContext, restored.Done())
		require.ElementsMatch(testContext, session.Slices(), restored.Slices())
		ttl, err := redisUtil.Client().TTL(ctx, session.SessionKey).Result()
		require.NoError(testContext, err)
		require.InDelta(testContext, 300, ttl.Seconds(), 3)
		require.NoError(testContext, restored.Stop(queryContext(), prometheus.ClearScroll))
		assertNoScrolls(testContext)
	})

	testContext.Run("stop_preserves_incomplete_contexts", func(testContext *testing.T) {
		testCases := []struct {
			name        string
			status      string
			failedNum   int
			pendingPeer bool
		}{
			{name: "pending", status: redisUtil.StatusPending},
			{name: "retryable", status: redisUtil.StatusPending, failedNum: redisUtil.DefaultSliceMaxFailedNum - 1},
			{name: "completed_with_pending_peer", status: redisUtil.StatusCompleted, pendingPeer: true},
			{name: "exhausted_with_pending_peer", status: redisUtil.StatusPending, failedNum: redisUtil.DefaultSliceMaxFailedNum, pendingPeer: true},
		}
		for _, testCase := range testCases {
			testContext.Run(testCase.name, func(testContext *testing.T) {
				session := newSession(testContext, "stop-"+testCase.name)
				completedSlice(session, "unfinished", 0, createScroll(testContext, 0, "5m"))
				slice := session.Slice("unfinished")
				slice.Status, slice.FailedNum = testCase.status, testCase.failedNum
				if testCase.pendingPeer {
					session.Slice("pending-peer")
				}
				require.False(testContext, session.Done())
				require.NoError(testContext, session.Start(queryContext()))
				require.NoError(testContext, session.Stop(queryContext(), prometheus.ClearScroll))
				require.EqualValues(testContext, 1, scrollCount(testContext, 0))
				require.Zero(testContext, redisUtil.Client().Exists(ctx, session.LockKey).Val())
				for _, saved := range session.Slices() {
					session.Slice(saved.SliceKey).Status = redisUtil.StatusCompleted
				}
				require.NoError(testContext, session.Stop(queryContext(), prometheus.ClearScroll))
				assertNoScrolls(testContext)
			})
		}
	})

	testContext.Run("stop_preserves_redis_error_on_cleanup_failure", func(testContext *testing.T) {
		session := newSession(testContext, "stop-redis-error")
		completedSlice(session, "forbidden", 0, createScroll(testContext, 0, "5m"))
		storageID := prefix + "_stop_forbidden"
		tsdb.SetStorage(storageID, &tsdb.Storage{
			Type: metadata.ElasticsearchStorageType, Address: firstURL,
			Username: prefix + "_unprovisioned", Password: clusters[0].password,
		})
		session.Slice("forbidden").ScrollTarget.StorageID = storageID
		queryCtx := queryContext()
		require.NoError(testContext, session.Start(queryCtx))
		cancelledCtx, cleanupCancel := context.WithCancel(queryCtx)
		cleanupCancel()
		var output scrollCleanupLogBuffer
		log.SetOutput(&output)
		log.InitTestLogger()
		defer func() { log.SetOutput(nil); log.InitTestLogger() }()
		require.ErrorIs(testContext, session.Stop(cancelledCtx, prometheus.ClearScroll), context.Canceled)
		require.Contains(testContext, output.String(), "scroll cleanup failed: session=")
		require.Contains(testContext, output.String(), "storage_id="+storageID)
		require.Contains(testContext, output.String(), "401")
		require.EqualValues(testContext, 1, scrollCount(testContext, 0))
		require.Zero(testContext, redisUtil.Client().Exists(ctx, session.SessionKey).Val())
		require.EqualValues(testContext, 1, redisUtil.Client().Exists(ctx, session.LockKey).Val())
		session.Slice("forbidden").ScrollTarget.StorageID = clusters[0].storageID
		require.NoError(testContext, session.Stop(queryContext(), prometheus.ClearScroll))
		assertNoScrolls(testContext)
		require.Zero(testContext, redisUtil.Client().Exists(ctx, session.LockKey).Val())
	})

	testContext.Run("EOF_all_saved_branches_and_clusters", func(testContext *testing.T) {
		exporter.Reset()
		session := newSession(testContext, "multi-cluster")
		var eventIDs []string
		sawEarlierCompletion, sawFinalData := false, false
		for page := range 40 {
			list, done, err := execute(testContext, session, newQuery())
			require.NoError(testContext, err)
			if page == 0 {
				testContext.Logf("first_page_scroll_current=[%d,%d]", scrollCount(testContext, 0), scrollCount(testContext, 1))
			}
			for _, event := range list {
				eventIDs = append(eventIDs, event["event_id"].(string))
			}
			if done {
				require.True(testContext, session.Done())
				break
			}
			for _, slice := range session.Slices() {
				require.NotNil(testContext, slice.ScrollTarget)
				if slice.Status == redisUtil.StatusCompleted && slice.ScrollID != "" {
					sawEarlierCompletion = true
				}
			}
			if len(list) > 0 {
				sawFinalData = true
			}
			stored, err := redisUtil.Client().Get(ctx, session.SessionKey).Bytes()
			require.NoError(testContext, err)
			require.NoError(testContext, session.UnmarshalBinary(stored))
			require.Less(testContext, page, 39)
		}
		require.True(testContext, sawEarlierCompletion)
		require.True(testContext, sawFinalData)
		require.ElementsMatch(testContext, expectedEventIDs, eventIDs)
		require.Len(testContext, session.Slices(), 4)
		unique := make(map[string]bool)
		for _, eventID := range eventIDs {
			require.False(testContext, unique[eventID], eventID)
			unique[eventID] = true
		}
		assertNoScrolls(testContext)
		var freed int64
		for _, span := range exporter.GetSpans() {
			if span.Name == "elasticsearch-clear-scroll" {
				for _, attribute := range span.Attributes {
					if attribute.Key == "num-freed" {
						freed += attribute.Value.AsInt64()
					}
				}
			}
		}
		require.Positive(testContext, freed)
		testContext.Logf("exported=%d contexts_freed=%d completed_scroll_current=[%d,%d]", len(eventIDs), freed, scrollCount(testContext, 0), scrollCount(testContext, 1))
		ttl, err := redisUtil.Client().TTL(ctx, session.SessionKey).Result()
		require.NoError(testContext, err)
		require.InDelta(testContext, 300, ttl.Seconds(), 3)
		stored, err := redisUtil.Client().Get(ctx, session.SessionKey).Bytes()
		require.NoError(testContext, err)
		var restored redisUtil.ScrollSession
		require.NoError(testContext, restored.UnmarshalBinary(stored))
		require.True(testContext, restored.Done())
		require.NoError(testContext, restored.Stop(queryContext(), prometheus.ClearScroll))
		assertNoScrolls(testContext)
	})

	testContext.Run("saved_route_missing_from_current_plan", func(testContext *testing.T) {
		session := newSession(testContext, "removed-route")
		completedSlice(session, "removed-table|old-storage|0", 1, createScroll(testContext, 1, "5m"))
		require.EqualValues(testContext, 1, scrollCount(testContext, 1))
		eventIDs := readAll(testContext, session)
		require.Len(testContext, eventIDs, 18)
		assertNoScrolls(testContext)
	})

	testContext.Run("retriable_and_error_done_do_not_clear", func(testContext *testing.T) {
		session := newSession(testContext, "retry")
		list, done, err := execute(testContext, session, newQuery())
		require.NoError(testContext, err)
		require.NotEmpty(testContext, list)
		require.False(testContext, done)
		before := []int64{scrollCount(testContext, 0), scrollCount(testContext, 1)}
		for _, slice := range session.Slices() {
			saved := session.Slice(slice.SliceKey)
			saved.FailedNum = redisUtil.DefaultSliceMaxFailedNum - 1
			session.UpdateSliceStatus(saved.SliceKey, saved)
		}
		invalidQuery := newQuery()
		invalidQuery.Start = "invalid-time"
		_, done, err = execute(testContext, session, invalidQuery)
		require.Error(testContext, err)
		require.True(testContext, done)
		require.False(testContext, session.Done())
		require.Equal(testContext, before, []int64{scrollCount(testContext, 0), scrollCount(testContext, 1)})
		require.NotEmpty(testContext, readAll(testContext, session))
		assertNoScrolls(testContext)
	})

	testContext.Run("parallel_download_isolation", func(testContext *testing.T) {
		other := newSession(testContext, "other")
		list, done, err := execute(testContext, other, newQuery())
		require.NoError(testContext, err)
		require.False(testContext, done)
		firstCount := len(list)
		session := newSession(testContext, "isolated")
		require.Len(testContext, readAll(testContext, session), 18)
		require.Positive(testContext, scrollCount(testContext, 0)+scrollCount(testContext, 1))
		require.Len(testContext, readAll(testContext, other), 18-firstCount)
		assertNoScrolls(testContext)
	})

	testContext.Run("expired_and_repeated_clear", func(testContext *testing.T) {
		session := newSession(testContext, "expired")
		completedSlice(session, "expired", 0, createScroll(testContext, 0, "1ms"))
		require.Eventually(testContext, func() bool { return scrollCount(testContext, 0) == 0 }, 65*time.Second, 20*time.Millisecond)
		_, err := clusters[0].client.Scroll().ScrollId(session.Slice("expired").ScrollID).Scroll("1ms").Do(ctx)
		require.True(testContext, elastic.IsNotFound(err), "%v", err)
		require.NoError(testContext, session.Stop(queryContext(), prometheus.ClearScroll))
		require.NoError(testContext, session.Stop(queryContext(), prometheus.ClearScroll))
		require.True(testContext, session.Done())
		assertNoScrolls(testContext)
	})

	testContext.Run("unknown_target_and_missing_storage", func(testContext *testing.T) {
		for _, targetMissing := range []bool{true, false} {
			session := newSession(testContext, fmt.Sprintf("unknown-target-%v", targetMissing))
			scrollID := createScroll(testContext, 0, "5m")
			completedSlice(session, "unknown-target", 0, scrollID)
			message := "scroll cleanup failed: session="
			if targetMissing {
				session.Slice("unknown-target").ScrollTarget = nil
				message = "scroll cleanup target missing: session="
			} else {
				session.Slice("unknown-target").ScrollTarget.StorageID = prefix + "_missing"
			}
			var output scrollCleanupLogBuffer
			log.SetOutput(&output)
			log.InitTestLogger()
			require.NoError(testContext, session.Stop(queryContext(), prometheus.ClearScroll))
			log.SetOutput(nil)
			log.InitTestLogger()
			require.Contains(testContext, output.String(), message)
			require.EqualValues(testContext, 1, scrollCount(testContext, 0))
			_, err := clusters[0].client.ClearScroll().ScrollId(scrollID).Do(ctx)
			require.NoError(testContext, err)
		}
		assertNoScrolls(testContext)
	})

	testContext.Run("query_error_and_terminal_failure", func(testContext *testing.T) {
		session := newSession(testContext, "terminal-failure")
		_, done, err := execute(testContext, session, newQuery())
		require.NoError(testContext, err)
		require.False(testContext, done)
		for _, slice := range session.Slices() {
			saved := session.Slice(slice.SliceKey)
			saved.FailedNum = redisUtil.DefaultSliceMaxFailedNum - 1
			session.UpdateSliceStatus(saved.SliceKey, saved)
		}
		require.Positive(testContext, scrollCount(testContext, 0)+scrollCount(testContext, 1))
		failedQuery := newQuery()
		for reference := range failedQuery.TsDBMap {
			for tableIndex := range failedQuery.TsDBMap[reference] {
				failedQuery.TsDBMap[reference][tableIndex].DB += "_missing"
			}
		}
		_, done, err = execute(testContext, session, failedQuery)
		require.ErrorContains(testContext, err, "index_not_found_exception")
		require.True(testContext, done)
		require.True(testContext, session.Done())
		for _, slice := range session.Slices() {
			if slice.Status == redisUtil.StatusCompleted {
				require.Equal(testContext, redisUtil.DefaultSliceMaxFailedNum-1, slice.FailedNum)
			} else {
				require.Equal(testContext, redisUtil.DefaultSliceMaxFailedNum, slice.FailedNum)
				require.Equal(testContext, redisUtil.StatusPending, slice.Status)
			}
		}
		assertNoScrolls(testContext)
	})

	testContext.Run("cleanup_authentication_error_preserves_results", func(testContext *testing.T) {
		username := prefix + "_unprovisioned"
		session := newSession(testContext, "permission")
		scrollID := createScroll(testContext, 0, "5m")
		completedSlice(session, "forbidden", 0, scrollID)
		completedSlice(session, "healthy", 1, createScroll(testContext, 1, "5m"))
		storageID := prefix + "_forbidden"
		tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: firstURL, Username: username, Password: clusters[0].password})
		session.Slice("forbidden").ScrollTarget.StorageID = storageID
		for _, clusterIndex := range []int{0, 1} {
			for sliceIndex := range session.SliceLength() {
				key := fmt.Sprintf("trace.cleanup_%d|%s|%d", clusterIndex, clusters[clusterIndex].storageID, sliceIndex)
				completedSlice(session, key, clusterIndex, "")
			}
		}
		var output scrollCleanupLogBuffer
		log.SetOutput(&output)
		log.InitTestLogger()
		defer func() { log.SetOutput(nil); log.InitTestLogger() }()
		queryCtx := queryContext()
		metadata.SetStatus(queryCtx, "original-status", "original-query-status")
		_, list, _, _, done, err := queryRawWithScroll(queryCtx, newQuery(), session)
		require.NoError(testContext, err)
		require.True(testContext, done)
		require.Empty(testContext, list)
		require.Contains(testContext, output.String(), "scroll cleanup failed: session=")
		require.Contains(testContext, output.String(), "storage_id="+storageID)
		require.Contains(testContext, output.String(), "401")
		require.Equal(testContext, &metadata.Status{Code: "original-status", Message: "original-query-status"}, metadata.GetStatus(queryCtx))
		require.EqualValues(testContext, 1, scrollCount(testContext, 0))
		require.Zero(testContext, scrollCount(testContext, 1))
		invalidQuery := newQuery()
		invalidQuery.Start = "invalid-time"
		_, done, err = execute(testContext, session, invalidQuery)
		require.ErrorContains(testContext, err, "查询参数配置异常")
		require.NotContains(testContext, err.Error(), "Unauthorized")
		require.True(testContext, done)
		_, err = clusters[0].client.ClearScroll().ScrollId(scrollID).Do(ctx)
		require.NoError(testContext, err)
		assertNoScrolls(testContext)
	})

	testContext.Run("legacy_completed_target_backfill", func(testContext *testing.T) {
		session := newSession(testContext, "legacy")
		key := "trace.cleanup_0|" + clusters[0].storageID + "|0"
		completedSlice(session, key, 0, createScroll(testContext, 0, "5m"))
		session.Slice(key).ScrollTarget = nil
		readAll(testContext, session)
		require.NotNil(testContext, session.Slice(key).ScrollTarget)
		assertNoScrolls(testContext)
	})

	testContext.Run("bkdata_saved_space_route_and_headers", func(testContext *testing.T) {
		backendURL, err := url.Parse(clusters[1].address)
		require.NoError(testContext, err)
		proxy := httputil.NewSingleHostReverseProxy(backendURL)
		director := proxy.Director
		requestHeaders := make(chan http.Header, 1)
		requestPaths := make(chan string, 1)
		var gatewayPath string
		proxy.Director = func(request *http.Request) {
			requestHeaders <- request.Header.Clone()
			requestPaths <- request.URL.Path
			director(request)
			request.URL.Path = strings.TrimPrefix(request.URL.Path, gatewayPath)
			request.SetBasicAuth(clusters[1].username, clusters[1].password)
		}
		gateway := httptest.NewServer(proxy)
		testContext.Cleanup(gateway.Close)
		for key, value := range map[string]any{
			bkapi.BkDataAddressConfigPath:         gateway.URL,
			bkapi.BkDataTokenConfigPath:           clusters[1].password,
			bkapi.BkDataClusterSpaceUidConfigPath: map[string][]string{"saved-space": {"bkcc__saved"}},
		} {
			previous := viper.Get(key)
			viper.Set(key, value)
			testContext.Cleanup(func() { viper.Set(key, previous) })
		}
		queryURL, err := url.Parse(bkapi.GetBkDataAPI().QueryUrlForES("bkcc__saved"))
		require.NoError(testContext, err)
		gatewayPath = queryURL.Path
		require.Contains(testContext, gatewayPath, "/saved-space/query_sync/")
		session := newSession(testContext, "bkdata")
		completedSlice(session, "saved-bkdata", 1, createScroll(testContext, 1, "5m"))
		target := session.Slice("saved-bkdata").ScrollTarget
		target.SourceType, target.SpaceUID, target.TenantID = structured.BkData, "bkcc__saved", "saved-tenant"
		require.NoError(testContext, session.Stop(queryContext(), prometheus.ClearScroll))
		assertNoScrolls(testContext)
		select {
		case headers := <-requestHeaders:
			require.Equal(testContext, "bkcc__saved", headers.Get(metadata.SpaceUIDHeader))
			require.Equal(testContext, "saved-tenant", headers.Get(metadata.TenantIDHeader))
			var auth map[string]string
			require.NoError(testContext, json.Unmarshal([]byte(headers.Get(bkapi.BkDataAuthorization)), &auth))
			require.True(testContext, auth[bkapi.BkDataDataTokenKey] == clusters[1].password)
			require.NotEmpty(testContext, headers.Get(bkapi.BkAPIAuthorization))
			require.Equal(testContext, gatewayPath+"/_search/scroll/", <-requestPaths)
		default:
			testContext.Fatal("BkData cleanup did not reach the real ES forwarding gateway")
		}
	})

	testContext.Run("global_deadline_with_real_network_delay", func(testContext *testing.T) {
		session := newSession(testContext, "deadline")
		var delayedRequests atomic.Int64
		for clusterIndex, cluster := range clusters {
			scrollID := createScroll(testContext, clusterIndex, "5m")
			completedSlice(session, cluster.storageID, clusterIndex, scrollID)
			backendURL, err := url.Parse(cluster.address)
			require.NoError(testContext, err)
			proxy := httputil.NewSingleHostReverseProxy(backendURL)
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodDelete && strings.Contains(request.URL.Path, "_search/scroll") {
					delayedRequests.Add(1)
					body, readErr := io.ReadAll(request.Body)
					if readErr != nil {
						return
					}
					request.Body = io.NopCloser(bytes.NewReader(body))
					timer := time.NewTimer(3 * redisUtil.ScrollCleanupTimeout)
					defer timer.Stop()
					select {
					case <-request.Context().Done():
						return
					case <-timer.C:
					}
				}
				proxy.ServeHTTP(response, request)
			}))
			testContext.Cleanup(server.Close)
			storageID := cluster.storageID + "_delayed"
			tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: server.URL, Username: cluster.username, Password: cluster.password})
			session.Slice(cluster.storageID).ScrollTarget.StorageID = storageID
		}
		for clusterIndex, cluster := range clusters {
			for sliceIndex := range session.SliceLength() {
				completedSlice(session, fmt.Sprintf("trace.cleanup_%d|%s|%d", clusterIndex, cluster.storageID, sliceIndex), clusterIndex, "")
			}
		}
		started := time.Now()
		list, done, err := execute(testContext, session, newQuery())
		duration := time.Since(started)
		require.NoError(testContext, err)
		require.True(testContext, done)
		require.Empty(testContext, list)
		require.GreaterOrEqual(testContext, duration, redisUtil.ScrollCleanupTimeout-100*time.Millisecond)
		require.Less(testContext, duration, redisUtil.ScrollCleanupTimeout+time.Second)
		require.EqualValues(testContext, 2, delayedRequests.Load())
		require.EqualValues(testContext, 1, scrollCount(testContext, 0))
		require.EqualValues(testContext, 1, scrollCount(testContext, 1))
		testContext.Logf("two_cluster_cleanup_duration=%s", duration)
		for clusterIndex, cluster := range clusters {
			_, err := cluster.client.ClearScroll().ScrollId(session.Slice(cluster.storageID).ScrollID).Do(ctx)
			require.NoError(testContext, err)
			require.Zero(testContext, scrollCount(testContext, clusterIndex))
		}
	})

	testContext.Run("completed_cleanup_survives_client_cancellation", func(testContext *testing.T) {
		session := newSession(testContext, "cancelled")
		completedSlice(session, "cancelled", 0, createScroll(testContext, 0, "5m"))
		cancelledCtx, cleanupCancel := context.WithCancel(queryContext())
		cleanupCancel()
		require.ErrorIs(testContext, session.Stop(cancelledCtx, prometheus.ClearScroll), context.Canceled)
		assertNoScrolls(testContext)
	})

	testContext.Run("continuous_short_downloads", func(testContext *testing.T) {
		for ordinal := range 12 {
			session := newSession(testContext, fmt.Sprintf("short-%d", ordinal))
			eventIDs := readAll(testContext, session)
			require.ElementsMatch(testContext, expectedEventIDs, eventIDs)
			assertNoScrolls(testContext)
		}
	})
}

type scrollCleanupLogBuffer struct {
	lock   sync.Mutex
	buffer bytes.Buffer
}

func (buffer *scrollCleanupLogBuffer) Write(content []byte) (int, error) {
	buffer.lock.Lock()
	defer buffer.lock.Unlock()
	return buffer.buffer.Write(content)
}

func (buffer *scrollCleanupLogBuffer) String() string {
	buffer.lock.Lock()
	defer buffer.lock.Unlock()
	return buffer.buffer.String()
}
