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
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

func TestQueryRawESBatchTailRunsBeforeInFlightFullBatch(t *testing.T) {
	for _, test := range []struct {
		name        string
		limit       int
		tailCount   int
		splitTail   bool
		lastFailure string
		cancel      bool
	}{
		{name: "single_worker", limit: 1, tailCount: 1},
		{name: "single_tail", limit: 2, tailCount: 1},
		{name: "batch_tail", limit: 4, tailCount: 2},
		{name: "multiple_final_groups", limit: 2, tailCount: 2, splitTail: true},
		{name: "last_mapping_error", limit: 2, tailCount: 1, lastFailure: "error"},
		{name: "last_mapping_panic", limit: 4, tailCount: 2, lastFailure: "panic"},
		{name: "cancel_in_flight_full_batch", limit: 2, tailCount: 1, cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			mock.Init()
			ctx, cancel := context.WithCancel(metadata.InitHashID(context.Background()))
			defer cancel()
			metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			previousProvider := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() {
				_ = provider.Shutdown(context.Background())
				otel.SetTracerProvider(previousProvider)
			})
			const esURL = "http://127.0.0.1:19350"
			const traceID = "2025-03-08T12:34:56Z"
			const fullCount = 3
			storageID := "tail-ready-" + test.name
			oldLimit := QueryMaxRouting
			oldSettings := queryRawESBatchSettingsSnapshot.Load()
			QueryMaxRouting = test.limit
			queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{maxMembers: fullCount, maxBodyBytes: 1 << 20, maxConcurrentSearches: 4})
			t.Cleanup(func() {
				QueryMaxRouting = oldLimit
				queryRawESBatchSettingsSnapshot.Store(oldSettings)
			})
			tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: esURL})

			fullStarted := make(chan struct{})
			tailSearched := make(chan struct{}, test.tailCount)
			releaseFull := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseFull) }) }
			defer release()
			var active, peak atomic.Int32
			enter := func() func() {
				current := active.Add(1)
				for old := peak.Load(); current > old; old = peak.Load() {
					if peak.CompareAndSwap(old, current) {
						break
					}
				}
				return func() { active.Add(-1) }
			}
			var lock sync.Mutex
			searched := make(map[string]int)
			response := func(index string) string {
				lock.Lock()
				searched[index]++
				lock.Unlock()
				return fmt.Sprintf(`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":%q,"_id":%q,"sort":[1],"_source":{"start_time":1,"trace_id":%q}}]}}`, index, index, traceID)
			}
			memberCount := fullCount + test.tailCount
			if test.lastFailure != "" {
				memberCount++
			}
			routes := make([]rawESBatchIntegrationRoute, 0, memberCount)
			for ordinal := range memberCount {
				index := fmt.Sprintf("tail_member_%02d", ordinal)
				routes = append(routes, rawESBatchIntegrationRoute{referenceName: index, tableID: "trace." + index, index: index})
				httpmock.RegisterResponder(http.MethodGet, esURL+"/"+index, func(req *http.Request) (*http.Response, error) {
					leave := enter()
					defer leave()
					if ordinal >= fullCount && test.limit > 1 {
						select {
						case <-fullStarted:
						case <-req.Context().Done():
							return nil, req.Context().Err()
						}
					}
					if ordinal == memberCount-1 && test.lastFailure != "" {
						if test.lastFailure == "panic" {
							panic("synthetic preparation failure")
						}
						return httpmock.NewStringResponse(http.StatusForbidden, `{}`), nil
					}
					fieldType := "keyword"
					if test.splitTail && ordinal == memberCount-1 {
						fieldType = "date"
					}
					return httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`{"%s":{"settings":{},"mappings":{"properties":{"start_time":{"type":"date"},"trace_id":{"type":%q}}}}}`, index, fieldType)), nil
				})
				httpmock.RegisterResponder(http.MethodGet, esURL+"/"+index+"/_mapping/", httpmock.NewStringResponder(http.StatusForbidden, `{}`))
				httpmock.RegisterResponder(http.MethodPost, esURL+"/"+index+"/_search", func(*http.Request) (*http.Response, error) {
					leave := enter()
					defer leave()
					body := response(index)
					tailSearched <- struct{}{}
					return httpmock.NewStringResponse(http.StatusOK, body), nil
				})
			}
			httpmock.RegisterResponder(http.MethodGet, esURL+"/_msearch?max_concurrent_searches=4", func(req *http.Request) (*http.Response, error) {
				leave := enter()
				defer leave()
				body, err := io.ReadAll(req.Body)
				if err != nil {
					return nil, err
				}
				lines := strings.Split(strings.TrimSpace(string(body)), "\n")
				isFull := len(lines) == fullCount*2
				if isFull {
					close(fullStarted)
					if test.limit > 1 {
						select {
						case <-releaseFull:
						case <-req.Context().Done():
							return nil, req.Context().Err()
						}
					}
				}
				var responses []string
				for offset := 0; offset < len(lines); offset += 2 {
					var header struct {
						Index []string `json:"index"`
					}
					if err := json.Unmarshal([]byte(lines[offset]), &header); err != nil {
						return nil, err
					}
					if len(header.Index) != 1 {
						return nil, fmt.Errorf("expected one index per member")
					}
					responses = append(responses, response(header.Index[0]))
					if !isFull {
						tailSearched <- struct{}{}
					}
				}
				return httpmock.NewStringResponse(http.StatusOK, `{"responses":[`+strings.Join(responses, ",")+`]}`), nil
			})
			query := rawESBatchIntegrationQuery(storageID, traceID, routes...)
			query.IsESBatch = true
			type result struct {
				total   int64
				rows    []map[string]any
				options metadata.ResultTableOptions
				err     error
			}
			done := make(chan result, 1)
			go func() {
				total, rows, options, _, err := queryRawWithInstance(ctx, query)
				done <- result{total, rows, options, err}
			}()
			for range test.tailCount {
				select {
				case <-tailSearched:
				case <-time.After(3 * time.Second):
					t.Fatal("ready tail waited for the in-flight full batch")
				}
			}
			if test.limit > 1 {
				select {
				case <-done:
					t.Fatal("request returned before the full batch completed")
				default:
				}
			}
			if test.cancel {
				cancel()
			} else {
				release()
			}
			select {
			case result := <-done:
				require.NoError(t, result.err)
				if !test.cancel {
					require.EqualValues(t, fullCount+test.tailCount, result.total)
					require.Len(t, result.rows, fullCount+test.tailCount)
					require.Len(t, result.options, fullCount+test.tailCount)
					lock.Lock()
					counts := make(map[string]int, len(searched))
					for index, count := range searched {
						counts[index] = count
					}
					lock.Unlock()
					require.Len(t, counts, fullCount+test.tailCount)
					for _, count := range counts {
						require.Equal(t, 1, count)
					}
					for _, row := range result.rows {
						require.Equal(t, "trace."+row["__index"].(string), row[metadata.KeyTableID])
					}
				}
				if test.cancel || test.lastFailure != "" {
					require.Equal(t, metadata.QueryRawPartial, metadata.GetStatus(ctx).Code)
				} else {
					require.Nil(t, metadata.GetStatus(ctx))
				}
			case <-time.After(3 * time.Second):
				t.Fatal("query did not complete")
			}
			require.LessOrEqual(t, peak.Load(), int32(test.limit))
			require.Zero(t, active.Load())
			expectedTailTasks := 1
			if test.splitTail {
				expectedTailTasks = test.tailCount
			}
			tailSpans := 0
			for _, span := range recorder.Ended() {
				if span.Name() != "query-raw-es-tail-execute" {
					continue
				}
				tailSpans++
				attrs := make(map[string]any)
				for _, attr := range span.Attributes() {
					attrs[string(attr.Key)] = attr.Value.AsInterface()
				}
				require.Positive(t, attrs["es_batch_tail_ready_unix_millis"])
				require.GreaterOrEqual(t, attrs["es_batch_tail_wait_duration_ms"].(float64), float64(0))
				require.LessOrEqual(t, attrs["es_batch_tail_ready_unix_millis"].(int64), span.StartTime().UnixMilli())
			}
			require.Equal(t, expectedTailTasks, tailSpans)
		})
	}
}
