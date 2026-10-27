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
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

func TestQueryRawESBatchReadyMembersSearchBeforeSlowMapping(t *testing.T) {
	for _, test := range []struct {
		name      string
		limit     int
		slowFirst bool
		cancel    bool
	}{
		{"one_worker", 1, false, false},
		{"slow_first_two_workers", 2, true, false},
		{"four_workers", 4, false, false},
		{"cancel_after_ready_batch", 2, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			mock.Init()
			ctx, cancel := context.WithCancel(metadata.InitHashID(context.Background()))
			t.Cleanup(cancel)
			metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})
			const esURL = "http://127.0.0.1:19340"
			const traceID = "00000000000000000000000000000042"
			storageID := "ready-mapping-" + test.name
			oldLimit := QueryMaxRouting
			oldSettings := queryRawESBatchSettingsSnapshot.Load()
			QueryMaxRouting = test.limit
			queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{maxMembers: 2, maxBodyBytes: 1 << 20, maxConcurrentSearches: 4})
			t.Cleanup(func() { QueryMaxRouting = oldLimit; queryRawESBatchSettingsSnapshot.Store(oldSettings) })
			tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: esURL})

			releaseSlow := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseSlow) }) }
			t.Cleanup(release)
			slowStarted := make(chan struct{}, 1)
			batchStarted := make(chan []string, 1)
			var active, peak, mappingCalls, singleCalls, batchCalls atomic.Int32
			enter := func() func() {
				current := active.Add(1)
				for old := peak.Load(); current > old; old = peak.Load() {
					if peak.CompareAndSwap(old, current) {
						break
					}
				}
				return func() { active.Add(-1) }
			}
			rows := map[string]int{"ready_fast_a": 10, "ready_fast_b": 20, "ready_slow": 30}
			response := func(index string) string {
				return fmt.Sprintf(`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":%q,"_id":%q,"sort":[%d],"_source":{"start_time":%d,"trace_id":%q}}]}}`, index, index, rows[index], rows[index], traceID)
			}
			for index := range rows {
				index := index
				httpmock.RegisterResponder(http.MethodGet, esURL+"/"+index, func(req *http.Request) (*http.Response, error) {
					leave := enter()
					defer leave()
					mappingCalls.Add(1)
					if index == "ready_slow" {
						slowStarted <- struct{}{}
						select {
						case <-releaseSlow:
						case <-req.Context().Done():
							return nil, req.Context().Err()
						}
					}
					return httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`{"%s":{"settings":{},"mappings":{"properties":{"start_time":{"type":"date"},"trace_id":{"type":"keyword"}}}}}`, index)), nil
				})
				httpmock.RegisterResponder(http.MethodGet, esURL+"/"+index+"/_mapping/", func(req *http.Request) (*http.Response, error) {
					return nil, req.Context().Err()
				})
				httpmock.RegisterResponder(http.MethodPost, esURL+"/"+index+"/_search", func(*http.Request) (*http.Response, error) {
					leave := enter()
					defer leave()
					singleCalls.Add(1)
					return httpmock.NewStringResponse(http.StatusOK, response(index)), nil
				})
			}
			httpmock.RegisterResponder(http.MethodGet, esURL+"/_msearch?max_concurrent_searches=4", func(req *http.Request) (*http.Response, error) {
				leave := enter()
				defer leave()
				batchCalls.Add(1)
				body, err := io.ReadAll(req.Body)
				if err != nil {
					return nil, err
				}
				lines := strings.Split(strings.TrimSpace(string(body)), "\n")
				if len(lines) != 4 {
					return nil, fmt.Errorf("expected two search members, got %d lines", len(lines))
				}
				var indexes, responses []string
				for offset := 0; offset < len(lines); offset += 2 {
					var header struct {
						Index []string `json:"index"`
					}
					if err := json.Unmarshal([]byte(lines[offset]), &header); err != nil {
						return nil, err
					}
					if len(header.Index) != 1 {
						return nil, fmt.Errorf("expected one member-local target")
					}
					index := header.Index[0]
					indexes = append(indexes, index)
					responses = append(responses, response(index))
				}
				batchStarted <- indexes
				return httpmock.NewStringResponse(http.StatusOK, `{"responses":[`+strings.Join(responses, ",")+`]}`), nil
			})
			indexes := []string{"ready_fast_a", "ready_fast_b", "ready_slow"}
			if test.slowFirst {
				indexes = []string{"ready_slow", "ready_fast_a", "ready_fast_b"}
			}
			routes := make([]rawESBatchIntegrationRoute, 0, len(indexes))
			for ordinal, index := range indexes {
				routes = append(routes, rawESBatchIntegrationRoute{referenceName: fmt.Sprintf("r%d", ordinal), tableID: "trace." + index, index: index})
			}
			queryTs := rawESBatchIntegrationQuery(storageID, traceID, routes...)
			queryTs.IsESBatch = true
			queryTs.Limit = 2
			queryTs.OrderBy = structured.OrderBy{"-start_time", metadata.KeyTableID, metadata.KeyDocID}
			type result struct {
				total   int64
				list    []map[string]any
				options metadata.ResultTableOptions
				routes  []metadata.RouteInfo
				err     error
			}
			done := make(chan result, 1)
			go func() {
				total, list, options, routeInfo, err := queryRawWithInstance(ctx, queryTs)
				done <- result{total, list, options, routeInfo, err}
			}()
			select {
			case <-slowStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("slow mapping did not start")
			}
			select {
			case indexes := <-batchStarted:
				require.Equal(t, []string{"ready_fast_a", "ready_fast_b"}, indexes)
			case <-time.After(3 * time.Second):
				t.Fatal("ready batch waited for slow mapping")
			}
			select {
			case <-done:
				t.Fatal("request returned before accounting for the slow member")
			default:
			}
			if test.cancel {
				cancel()
			} else {
				release()
			}
			select {
			case result := <-done:
				require.NoError(t, result.err)
				require.Len(t, result.routes, 3)
				require.Len(t, result.list, 2)
				if test.cancel {
					require.EqualValues(t, 2, result.total)
					require.Len(t, result.options, 2)
					require.Equal(t, metadata.QueryRawPartial, metadata.GetStatus(ctx).Code)
					require.Equal(t, "trace.ready_fast_b", result.list[0][metadata.KeyTableID])
					require.Zero(t, singleCalls.Load())
				} else {
					require.EqualValues(t, 3, result.total)
					require.Len(t, result.options, 3)
					require.Equal(t, "trace.ready_slow", result.list[0][metadata.KeyTableID])
					require.Equal(t, "trace.ready_fast_b", result.list[1][metadata.KeyTableID])
					require.EqualValues(t, 1, singleCalls.Load())
				}
				for _, row := range result.list {
					require.Equal(t, "trace."+row["__index"].(string), row[metadata.KeyTableID])
				}
			case <-time.After(3 * time.Second):
				t.Fatal("query did not finish")
			}
			require.EqualValues(t, 3, mappingCalls.Load())
			require.EqualValues(t, 1, batchCalls.Load())
			require.LessOrEqual(t, peak.Load(), int32(test.limit))
			require.Zero(t, active.Load())
		})
	}
}

func TestQueryRawESBatchReadyBatchesPreserveAllMembers(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})
	const (
		esURL       = "http://127.0.0.1:19341"
		storageID   = "ready-mapping-many-members"
		traceID     = "00000000000000000000000000000042"
		memberCount = 201
		batchSize   = 16
	)
	oldLimit := QueryMaxRouting
	oldSettings := queryRawESBatchSettingsSnapshot.Load()
	QueryMaxRouting = 4
	queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{maxMembers: batchSize, maxBodyBytes: 1 << 20, maxConcurrentSearches: 4})
	t.Cleanup(func() {
		QueryMaxRouting = oldLimit
		queryRawESBatchSettingsSnapshot.Store(oldSettings)
	})
	tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: esURL})
	releaseSlow := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseSlow) }) }
	t.Cleanup(release)
	batchStarted := make(chan int, memberCount)
	var lock sync.Mutex
	searched := make(map[string]int)
	routes := make([]rawESBatchIntegrationRoute, 0, memberCount)
	for ordinal := range memberCount {
		index := fmt.Sprintf("ready_member_%03d", ordinal)
		routes = append(routes, rawESBatchIntegrationRoute{referenceName: index, tableID: "trace." + index, index: index})
		httpmock.RegisterResponder(http.MethodGet, esURL+"/"+index, func(*http.Request) (*http.Response, error) {
			if ordinal == 0 {
				<-releaseSlow
			}
			return httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`{"%s":{"settings":{},"mappings":{"properties":{"start_time":{"type":"date"},"trace_id":{"type":"keyword"}}}}}`, index)), nil
		})
	}
	httpmock.RegisterResponder(http.MethodGet, esURL+"/_msearch?max_concurrent_searches=4", func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		responses := make([]string, 0, len(lines)/2)
		for offset := 0; offset < len(lines); offset += 2 {
			var header struct {
				Index []string `json:"index"`
			}
			if err := json.Unmarshal([]byte(lines[offset]), &header); err != nil {
				return nil, err
			}
			if len(header.Index) != 1 {
				return nil, fmt.Errorf("expected one target per member")
			}
			index := header.Index[0]
			lock.Lock()
			searched[index]++
			lock.Unlock()
			responses = append(responses, fmt.Sprintf(`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":%q,"_id":%q,"_source":{"start_time":1,"trace_id":%q}}]}}`, index, index, traceID))
		}
		batchStarted <- len(responses)
		return httpmock.NewStringResponse(http.StatusOK, `{"responses":[`+strings.Join(responses, ",")+`]}`), nil
	})
	queryTs := rawESBatchIntegrationQuery(storageID, traceID, routes...)
	queryTs.IsESBatch = true
	queryTs.Limit = memberCount
	queryTs.OrderBy = structured.OrderBy{"-start_time", metadata.KeyTableID, metadata.KeyDocID}
	metricBefore := queryRawESBatchIntegrationMetricSnapshot(t)
	type result struct {
		total   int64
		rows    []map[string]any
		options metadata.ResultTableOptions
		err     error
	}
	done := make(chan result, 1)
	go func() {
		total, rows, options, _, err := queryRawWithInstance(ctx, queryTs)
		done <- result{total, rows, options, err}
	}()
	for range (memberCount - 1) / batchSize {
		select {
		case count := <-batchStarted:
			require.Equal(t, batchSize, count)
		case <-time.After(3 * time.Second):
			t.Fatal("full ready batch waited for slow mapping")
		}
	}
	release()
	select {
	case result := <-done:
		require.NoError(t, result.err)
		require.EqualValues(t, memberCount, result.total)
		require.Len(t, result.rows, memberCount)
		require.Len(t, result.options, memberCount)
		for ordinal, row := range result.rows {
			require.Equal(t, routes[ordinal].tableID, row[metadata.KeyTableID])
			require.Equal(t, routes[ordinal].index, row["__index"])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("query did not complete")
	}
	lock.Lock()
	defer lock.Unlock()
	require.Len(t, searched, memberCount)
	for _, count := range searched {
		require.Equal(t, 1, count)
	}
	metricAfter := queryRawESBatchIntegrationMetricSnapshot(t)
	require.Equal(t, metricBefore[metric.QueryRawESBatchEventPlannerFinalGroup]+1, metricAfter[metric.QueryRawESBatchEventPlannerFinalGroup])
	require.Equal(t, metricBefore[metric.QueryRawESBatchEventPlannerBatch]+13, metricAfter[metric.QueryRawESBatchEventPlannerBatch])
}

func TestQueryRawESBatchSingleMemberLimitKeepsFinalGroupMetrics(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})
	const (
		esURL     = "http://127.0.0.1:19342"
		storageID = "ready-mapping-single-limit"
		traceID   = "00000000000000000000000000000042"
	)
	oldSettings := queryRawESBatchSettingsSnapshot.Load()
	queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{maxMembers: 1, maxBodyBytes: 1 << 20, maxConcurrentSearches: 4})
	t.Cleanup(func() { queryRawESBatchSettingsSnapshot.Store(oldSettings) })
	tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: esURL})
	var calls atomic.Int32
	for _, index := range []string{"single_limit_first", "single_limit_second"} {
		registerRawESBatchMappingResponder(t, esURL, index)
		httpmock.RegisterResponder(http.MethodPost, esURL+"/"+index+"/_search", func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return httpmock.NewStringResponse(http.StatusOK, `{"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`), nil
		})
	}
	before := queryRawESBatchIntegrationMetricSnapshot(t)
	queryTs := rawESBatchExplicitTraceQuery(storageID, "single_limit_first", "single_limit_second", traceID)
	queryTs.IsESBatch = true
	_, _, options, _, err := queryRawWithInstance(ctx, queryTs)
	require.NoError(t, err)
	require.Len(t, options, 2)
	require.EqualValues(t, 2, calls.Load())
	after := queryRawESBatchIntegrationMetricSnapshot(t)
	require.Equal(t, before[metric.QueryRawESBatchEventPlannerFinalGroup]+1, after[metric.QueryRawESBatchEventPlannerFinalGroup])
	require.Equal(t, before[metric.QueryRawESBatchEventPlannerPackedSingle]+2, after[metric.QueryRawESBatchEventPlannerPackedSingle])
	require.Equal(t, before[metric.QueryRawESBatchEventPlannerFinalSplit], after[metric.QueryRawESBatchEventPlannerFinalSplit])
}
