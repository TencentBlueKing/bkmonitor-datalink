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
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

func TestRawQueryDispatcherPositiveLimit(t *testing.T) {
	const (
		taskCount = 200
		limit     = 4
	)
	var (
		active    atomic.Int32
		maxActive atomic.Int32
		completed atomic.Int32
	)
	started := make(chan struct{}, taskCount)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() {
			close(release)
		})
	})

	tasks := make([]func(), 0, taskCount)
	for range taskCount {
		tasks = append(tasks, func() {
			current := active.Add(1)
			defer active.Add(-1)
			defer completed.Add(1)
			for {
				max := maxActive.Load()
				if current <= max || maxActive.CompareAndSwap(max, current) {
					break
				}
			}
			started <- struct{}{}
			<-release
		})
	}

	baselineGoroutines := runtime.NumGoroutine()
	done := make(chan struct{})
	go func() {
		rawQueryDispatcher{limit: limit}.run(tasks)
		close(done)
	}()

	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for range limit {
		select {
		case <-started:
		case <-timer.C:
			t.Fatal("dispatcher did not start the bounded worker set")
		}
	}

	// Let a goroutine-per-task implementation finish spawning before sampling.
	// The fixed worker implementation remains at limit workers plus the caller.
	for range taskCount {
		runtime.Gosched()
	}
	goroutineDelta := runtime.NumGoroutine() - baselineGoroutines
	assert.Less(t, goroutineDelta, taskCount/4, "dispatcher parked O(taskCount) goroutines")

	releaseOnce.Do(func() {
		close(release)
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not finish")
	}

	assert.Zero(t, active.Load())
	assert.Equal(t, int32(limit), maxActive.Load())
	assert.Equal(t, int32(taskCount), completed.Load())
}

func TestRawQueryDispatcherReturnsWhenTasksObserveContextCancellation(t *testing.T) {
	const (
		taskCount = 200
		limit     = 4
	)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var (
		active    atomic.Int32
		completed atomic.Int32
	)
	started := make(chan struct{}, taskCount)
	tasks := make([]func(), 0, taskCount)
	for range taskCount {
		tasks = append(tasks, func() {
			active.Add(1)
			defer active.Add(-1)
			started <- struct{}{}
			<-ctx.Done()
			completed.Add(1)
		})
	}

	done := make(chan struct{})
	go func() {
		rawQueryDispatcher{limit: limit}.run(tasks)
		close(done)
	}()

	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for range limit {
		select {
		case <-started:
		case <-timer.C:
			t.Fatal("dispatcher did not start the bounded worker set")
		}
	}
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("dispatcher workers did not exit after their tasks observed cancellation")
	}
	assert.Zero(t, active.Load())
	assert.Equal(t, int32(taskCount), completed.Load())
}

func TestRawQueryDispatcherNonPositiveIsUnlimited(t *testing.T) {
	const taskCount = 4
	started := make(chan struct{}, taskCount)
	release := make(chan struct{})
	tasks := make([]func(), 0, taskCount)
	for range taskCount {
		tasks = append(tasks, func() {
			started <- struct{}{}
			<-release
		})
	}

	done := make(chan struct{})
	go func() {
		rawQueryDispatcher{limit: 0}.run(tasks)
		close(done)
	}()

	for range taskCount {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("non-positive routing limit did not start all tasks")
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not finish")
	}
}

func TestExecuteQueryRawWithESBatchSharesLimitAcrossExecutionKinds(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})

	const (
		limit               = 2
		candidateESURL      = "http://127.0.0.1:93009"
		directFirstESURL    = "http://127.0.0.1:93012"
		directSecondESURL   = "http://127.0.0.1:93013"
		candidateStorage    = "query-raw-es-batch-shared-limit-candidate"
		directFirstStorage  = "query-raw-es-batch-shared-limit-direct-first"
		directSecondStorage = "query-raw-es-batch-shared-limit-direct-second"
		firstTraceID        = "00000000000000000000000000000042"
		secondTraceID       = "00000000000000000000000000000043"
	)
	oldLimit := QueryMaxRouting
	oldSettings := queryRawESBatchSettingsSnapshot.Load()
	QueryMaxRouting = limit
	queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{
		maxMembers:            16,
		maxBodyBytes:          1 << 20,
		maxConcurrentSearches: 4,
	})
	t.Cleanup(func() {
		QueryMaxRouting = oldLimit
		queryRawESBatchSettingsSnapshot.Store(oldSettings)
	})

	for storageID, address := range map[string]string{
		candidateStorage:    candidateESURL,
		directFirstStorage:  directFirstESURL,
		directSecondStorage: directSecondESURL,
	} {
		tsdb.SetStorage(storageID, &tsdb.Storage{
			Type:    metadata.ElasticsearchStorageType,
			Address: address,
		})
	}

	routes := []rawESBatchIntegrationRoute{
		{referenceName: "a", tableID: "trace.candidate.first", index: "trace_limit_candidate_first"},
		{referenceName: "b", tableID: "trace.direct.first", index: "trace_limit_direct_first"},
		{referenceName: "c", tableID: "trace.candidate.second", index: "trace_limit_candidate_second"},
		{referenceName: "d", tableID: "trace.direct.second", index: "trace_limit_direct_second"},
		{referenceName: "e", tableID: "trace.candidate.third", index: "trace_limit_candidate_third"},
		{referenceName: "f", tableID: "trace.candidate.fourth", index: "trace_limit_candidate_fourth"},
	}
	queryTs := rawESBatchIntegrationQuery(candidateStorage, firstTraceID, routes...)
	queryTs.IsESBatch = true
	queryTs.TsDBMap["b"][0].StorageID = directFirstStorage
	queryTs.TsDBMap["d"][0].StorageID = directSecondStorage
	for _, index := range []int{4, 5} {
		queryTs.QueryList[index].Conditions.FieldList[0].Value = []string{secondTraceID}
	}

	var (
		active            atomic.Int32
		maxActive         atomic.Int32
		mappingCalls      atomic.Int32
		singleSearchCalls atomic.Int32
		batchCalls        atomic.Int32
	)
	trackActive := func() func() {
		current := active.Add(1)
		for {
			max := maxActive.Load()
			if current <= max || maxActive.CompareAndSwap(max, current) {
				break
			}
		}
		return func() {
			active.Add(-1)
		}
	}

	phaseOneStarted := make(chan string, len(routes)*2)
	phaseOneRelease := make(chan struct{})
	batchStarted := make(chan struct{}, 2)
	batchRelease := make(chan struct{})
	var phaseOneReleaseOnce, batchReleaseOnce sync.Once
	t.Cleanup(func() {
		phaseOneReleaseOnce.Do(func() {
			close(phaseOneRelease)
		})
		batchReleaseOnce.Do(func() {
			close(batchRelease)
		})
	})

	for _, route := range routes {
		route := route
		executionKind := "prepare"
		routeESURL := candidateESURL
		if route.referenceName == "b" || route.referenceName == "d" {
			executionKind = "direct"
		}
		if route.referenceName == "b" {
			routeESURL = directFirstESURL
		}
		if route.referenceName == "d" {
			routeESURL = directSecondESURL
		}
		httpmock.RegisterResponder(
			http.MethodGet,
			routeESURL+"/"+route.index,
			func(*http.Request) (*http.Response, error) {
				done := trackActive()
				defer done()
				mappingCalls.Add(1)
				phaseOneStarted <- executionKind
				<-phaseOneRelease
				return httpmock.NewStringResponse(
					http.StatusOK,
					`{"`+route.index+`":{"settings":{},"mappings":{"properties":{"start_time":{"type":"date"},"trace_id":{"type":"keyword"}}}}}`,
				), nil
			},
		)
		httpmock.RegisterResponder(
			http.MethodPost,
			routeESURL+"/"+route.index+"/_search",
			func(*http.Request) (*http.Response, error) {
				done := trackActive()
				defer done()
				singleSearchCalls.Add(1)
				phaseOneStarted <- executionKind
				<-phaseOneRelease
				return httpmock.NewStringResponse(
					http.StatusOK,
					`{"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"`+route.index+`","_id":"single","_source":{"start_time":1,"trace_id":"`+firstTraceID+`"}}]}}`,
				), nil
			},
		)
	}
	httpmock.RegisterResponder(
		http.MethodGet,
		candidateESURL+"/_msearch?max_concurrent_searches=4",
		func(*http.Request) (*http.Response, error) {
			done := trackActive()
			defer done()
			batchCalls.Add(1)
			batchStarted <- struct{}{}
			<-batchRelease
			return httpmock.NewStringResponse(
				http.StatusOK,
				`{"responses":[`+
					`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"candidate","_id":"first","_source":{"start_time":1,"trace_id":"`+firstTraceID+`"}}]}},`+
					`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"candidate","_id":"second","_source":{"start_time":1,"trace_id":"`+firstTraceID+`"}}]}}`+
					`]}`,
			), nil
		},
	)

	type queryResult struct {
		total int64
		list  []map[string]any
		err   error
	}
	resultCh := make(chan queryResult, 1)
	go func() {
		total, list, _, _, err := queryRawWithInstance(ctx, queryTs)
		resultCh <- queryResult{total: total, list: list, err: err}
	}()

	firstKinds := make([]string, 0, limit)
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for range limit {
		select {
		case kind := <-phaseOneStarted:
			firstKinds = append(firstKinds, kind)
		case <-timer.C:
			t.Fatal("mixed preparation and direct execution did not reach the shared limit")
		}
	}
	assert.ElementsMatch(t, []string{"prepare", "direct"}, firstKinds)
	assert.Equal(t, int32(limit), active.Load())
	phaseOneReleaseOnce.Do(func() {
		close(phaseOneRelease)
	})

	for range limit {
		select {
		case <-batchStarted:
		case <-time.After(time.Second):
			t.Fatal("batch execution did not reach the shared limit")
		}
	}
	assert.Equal(t, int32(limit), active.Load())
	batchReleaseOnce.Do(func() {
		close(batchRelease)
	})

	select {
	case result := <-resultCh:
		require.NoError(t, result.err)
		assert.EqualValues(t, len(routes), result.total)
		assert.Len(t, result.list, len(routes))
	case <-time.After(3 * time.Second):
		t.Fatal("query execution did not finish")
	}
	assert.Zero(t, active.Load())
	assert.Equal(t, int32(limit), maxActive.Load())
	assert.EqualValues(t, len(routes), mappingCalls.Load())
	assert.EqualValues(t, 2, singleSearchCalls.Load())
	assert.EqualValues(t, 2, batchCalls.Load())
}

func TestStableRawESBatchQueriesIgnoresRouteInputOrder(t *testing.T) {
	first := &metadata.Query{
		TableID:     "trace.first",
		StorageID:   "3",
		DB:          "index_first",
		QueryString: "trace_id:first",
	}
	second := &metadata.Query{
		TableID:     "trace.second",
		StorageID:   "3",
		DB:          "index_second",
		QueryString: "trace_id:second",
	}

	forward := stableRawESBatchQueries(metadata.QueryList{first, second})
	reverse := stableRawESBatchQueries(metadata.QueryList{second, first})

	require.Len(t, forward, 2)
	require.Len(t, reverse, 2)
	assert.Equal(t, forward[0].TableID, reverse[0].TableID)
	assert.Equal(t, forward[1].TableID, reverse[1].TableID)
}

func TestRawQueryExecutionSinkGuardTaskConvertsPanicToError(t *testing.T) {
	errCh := make(chan error, 1)
	sink := &rawQueryExecutionSink{errCh: errCh}

	sink.guardTask(func() {
		panic("must not escape")
	})()

	select {
	case err := <-errCh:
		assert.EqualError(t, err, "query raw execution task panicked")
	default:
		t.Fatal("panic was not converted to an execution error")
	}
}

func TestRunRawQueryExecutionProducerConvertsOuterPanicAndClosesChannels(t *testing.T) {
	dataCh := make(chan map[string]any)
	errCh := make(chan error, 1)

	runRawQueryExecutionProducer(dataCh, errCh, func() {
		panic("sensitive panic value")
	})

	_, dataOpen := <-dataCh
	require.False(t, dataOpen)
	err, errOpen := <-errCh
	require.True(t, errOpen)
	assert.EqualError(t, err, "query raw ES batch execution panicked")
	_, errOpen = <-errCh
	require.False(t, errOpen)
}
