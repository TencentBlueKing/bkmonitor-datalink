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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

func TestQueryRawESBatchReadyExecution(t *testing.T) {
	for _, test := range []struct {
		name        string
		workers     int
		tailCount   int
		slowMapping bool
	}{
		{name: "ready_batch_before_slow_mapping", workers: 2, tailCount: 1, slowMapping: true},
		{name: "single_worker_without_deadlock", workers: 1, tailCount: 1, slowMapping: true},
		{name: "single_tail_before_slow_batch", workers: 2, tailCount: 1},
		{name: "batch_tail_before_slow_batch", workers: 4, tailCount: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			mock.Init()
			ctx, cancel := context.WithCancel(metadata.InitHashID(context.Background()))
			defer cancel()
			metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})
			const esURL = "http://127.0.0.1:19340"
			const traceID = "00000000000000000000000000000042"
			const fullCount = 3
			memberCount := fullCount + test.tailCount
			storageID := "ready-execution-" + test.name
			oldLimit := QueryMaxRouting
			oldSettings := queryRawESBatchSettingsSnapshot.Load()
			QueryMaxRouting = test.workers
			queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{maxMembers: fullCount, maxBodyBytes: 1 << 20, maxConcurrentSearches: 4})
			t.Cleanup(func() {
				QueryMaxRouting = oldLimit
				queryRawESBatchSettingsSnapshot.Store(oldSettings)
			})
			tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: esURL})

			fullStarted := make(chan struct{})
			tailSearched := make(chan struct{}, test.tailCount)
			slowStarted := make(chan struct{}, 1)
			releaseSlow := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseSlow) }) }
			wait := func(signal <-chan struct{}, message string) {
				t.Helper()
				select {
				case <-signal:
				case <-time.After(3 * time.Second):
					t.Fatal(message)
				}
			}
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
			indexes := make([]string, memberCount)
			times := make(map[string]int, memberCount)
			expectedSearches := make(map[string]int, memberCount)
			routes := make([]rawESBatchIntegrationRoute, 0, memberCount)
			response := func(index string) string {
				lock.Lock()
				searched[index]++
				lock.Unlock()
				return fmt.Sprintf(`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":%q,"_id":%q,"sort":[%d],"_source":{"start_time":%d,"trace_id":%q}}]}}`, index, index, times[index], times[index], traceID)
			}
			for ordinal := range memberCount {
				index := fmt.Sprintf("ready_member_%02d", ordinal)
				indexes[ordinal] = index
				times[index] = ordinal + 1
				expectedSearches[index] = 1
				routes = append(routes, rawESBatchIntegrationRoute{referenceName: index, tableID: "trace." + index, index: index})
				httpmock.RegisterResponder(http.MethodGet, esURL+"/"+index, func(req *http.Request) (*http.Response, error) {
					leave := enter()
					defer leave()
					if ordinal >= fullCount {
						signal := fullStarted
						if test.slowMapping {
							slowStarted <- struct{}{}
							signal = releaseSlow
						}
						select {
						case <-signal:
						case <-req.Context().Done():
							return nil, req.Context().Err()
						}
					}
					return httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`{"%s":{"settings":{},"mappings":{"properties":{"start_time":{"type":"date"},"trace_id":{"type":"keyword"}}}}}`, index)), nil
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
			// 固定请求中的 ES 并发上限；错误的参数无法匹配 responder，会导致结果断言失败。
			httpmock.RegisterResponder(http.MethodGet, esURL+"/_msearch?max_concurrent_searches=4", func(req *http.Request) (*http.Response, error) {
				leave := enter()
				defer leave()
				body, err := io.ReadAll(req.Body)
				if err != nil {
					return nil, err
				}
				lines := strings.Split(strings.TrimSpace(string(body)), "\n")
				if len(lines)%2 != 0 {
					return nil, fmt.Errorf("invalid NDJSON line count: %d", len(lines))
				}
				isFull := len(lines) == fullCount*2
				if isFull {
					close(fullStarted)
					if !test.slowMapping {
						select {
						case <-releaseSlow:
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
			query.Limit = 2
			query.OrderBy = structured.OrderBy{"-start_time", metadata.KeyTableID, metadata.KeyDocID}
			type queryResult struct {
				total   int64
				rows    []map[string]any
				options metadata.ResultTableOptions
				routes  []metadata.RouteInfo
				err     error
			}
			done := make(chan queryResult, 1)
			exited := make(chan struct{})
			go func() {
				defer close(exited)
				total, rows, options, routeInfo, err := queryRawWithInstance(ctx, query)
				done <- queryResult{total, rows, options, routeInfo, err}
			}()
			// 失败时也释放阻塞并等待查询退出，避免影响后续使用全局 mock 和配置的用例。
			defer func() {
				cancel()
				release()
				wait(exited, "查询未能退出")
			}()

			// 用阻塞通道验证执行先后，不依赖耗时阈值或 sleep。
			wait(fullStarted, "已就绪满批仍在等待慢 mapping")
			if test.slowMapping {
				wait(slowStarted, "慢 mapping 未开始")
			} else {
				for range test.tailCount {
					wait(tailSearched, "已就绪尾批仍在等待慢满批")
				}
			}
			select {
			case <-done:
				t.Fatal("慢成员未完成时查询就已返回")
			default:
			}
			release()
			wait(exited, "查询未完成")
			result := <-done
			require.NoError(t, result.err)
			require.Nil(t, metadata.GetStatus(ctx))
			require.EqualValues(t, memberCount, result.total)
			require.Len(t, result.options, memberCount)
			require.Len(t, result.routes, memberCount)
			require.Len(t, result.rows, 2)
			for ordinal, row := range result.rows {
				index := indexes[memberCount-1-ordinal]
				require.Equal(t, index, row["__index"])
				require.Equal(t, "trace."+index, row[metadata.KeyTableID])
			}
			require.Equal(t, expectedSearches, searched)
			require.LessOrEqual(t, peak.Load(), int32(test.workers))
			require.Zero(t, active.Load())
		})
	}
}
