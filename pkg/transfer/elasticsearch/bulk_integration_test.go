// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
)

type bulkTestServer struct {
	address string
	user    string
	secret  string
	major   int
}

func (s bulkTestServer) request(method, path, body string) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, s.address+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if s.user != "" {
		request.SetBasicAuth(s.user, s.secret)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	return response.StatusCode, data, err
}

func (s bulkTestServer) createIndex(t *testing.T, name string) {
	t.Helper()
	mapping := map[string]interface{}{"properties": map[string]interface{}{
		"id": map[string]string{"type": "keyword"}, "value": map[string]string{"type": "long"},
	}}
	if s.major <= 6 {
		mapping = map[string]interface{}{"doc": mapping}
	}
	body, err := json.Marshal(map[string]interface{}{
		"settings": map[string]int{"number_of_shards": 1, "number_of_replicas": 0}, "mappings": mapping,
	})
	require.NoError(t, err)
	status, data, err := s.request(http.MethodPut, "/"+name, string(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, string(data))
	t.Cleanup(func() {
		status, data, err := s.request(http.MethodDelete, "/"+name, "")
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, string(data))
	})
}

func (s bulkTestServer) setBlock(t *testing.T, index string, blocked bool) {
	t.Helper()
	body := fmt.Sprintf(`{"index.blocks.read_only_allow_delete":%t}`, blocked)
	status, data, err := s.request(http.MethodPut, "/"+index+"/_settings", body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, string(data))
}

func (s bulkTestServer) document(index, id string) (int, []byte, error) {
	kind := "_doc"
	if s.major <= 6 {
		kind = "doc"
	}
	return s.request(http.MethodGet, "/"+index+"/"+kind+"/"+id, "")
}

func (s bulkTestServer) handler(t *testing.T, version, index string) *BulkHandler {
	t.Helper()
	address, err := url.Parse(s.address)
	require.NoError(t, err)
	port, err := strconv.Atoi(address.Port())
	require.NoError(t, err)
	cluster := config.NewMetaClusterInfo().AsElasticSearchCluster()
	cluster.SetVersion(version)
	cluster.SetSchema(address.Scheme)
	cluster.SetDomain(address.Hostname())
	cluster.SetPort(port)
	cluster.AuthInfo["username"] = s.user
	cluster.AuthInfo["password"] = s.secret
	handler, err := NewBulkHandler(cluster, &config.MetaResultTableConfig{ResultTable: "doc"}, 600*time.Millisecond, []string{"id"}, FixedIndexRender(index))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, handler.Close()) })
	return handler
}

// 使用专用的本地 ES 实例：TRANSFER_ES8_URL=http://127.0.0.1:9200；
// 其他版本用相应数字，启用认证时另设同前缀的 _USERNAME 和 _PASSWORD。
// 测试只创建和删除带 transfer-bulk-test 前缀的临时索引。
func TestBulkIntegration(t *testing.T) {
	for _, major := range []int{5, 6, 7, 8, 9} {
		t.Run(fmt.Sprintf("v%d", major), func(t *testing.T) {
			prefix := fmt.Sprintf("TRANSFER_ES%d", major)
			server := bulkTestServer{address: os.Getenv(prefix + "_URL"), user: os.Getenv(prefix + "_USERNAME"), secret: os.Getenv(prefix + "_PASSWORD"), major: major}
			if server.address == "" {
				t.Skip("未配置本地 ES 地址：" + prefix + "_URL")
			}
			status, data, err := server.request(http.MethodGet, "/", "")
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status, string(data))
			var info struct {
				Version struct{ Number string }
			}
			require.NoError(t, json.Unmarshal(data, &info))
			require.True(t, strings.HasPrefix(info.Version.Number, strconv.Itoa(major)+"."))
			index := fmt.Sprintf("transfer-bulk-test-%d-%d", major, time.Now().UnixNano())
			blockedIndex := index + "-blocked"
			server.createIndex(t, index)
			server.createIndex(t, blockedIndex)

			for _, tc := range []struct {
				name      string
				value     interface{}
				wantCount int
				wantError bool
			}{
				{"all_success", 2, 2, false},
				{"mapping_error", "not-a-number", 1, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					handler := server.handler(t, info.Version.Number, index)
					results := []interface{}{
						&define.ETLRecord{Metrics: map[string]interface{}{"id": tc.name + "-good", "value": 1}},
						&define.ETLRecord{Metrics: map[string]interface{}{"id": tc.name + "-second", "value": tc.value}},
					}
					count, err := handler.Flush(context.Background(), results)
					require.Equal(t, tc.wantCount, count)
					if tc.wantError {
						var permanent *backoff.PermanentError
						require.ErrorAs(t, err, &permanent)
						require.ErrorContains(t, err, "status 400")
					} else {
						require.NoError(t, err)
					}
					// 同一 ID 写入两次应更新原文档，证明文档 ID 规则没有变化。
					count, err = handler.Flush(context.Background(), results[:1])
					require.NoError(t, err)
					require.Equal(t, 1, count)
					good, err := handler.asRecord(results[0].(*define.ETLRecord))
					require.NoError(t, err)
					status, data, err := server.document(index, good.Meta["_id"].(string))
					require.NoError(t, err)
					require.Equal(t, http.StatusOK, status, string(data))
					var document struct {
						Version int `json:"_version"`
					}
					require.NoError(t, json.Unmarshal(data, &document))
					require.Equal(t, 2, document.Version)
				})
			}

			// 新版本的只读磁盘保护返回逐条 429，直接用真实 ES 产生可重试失败。
			if major >= 8 {
				for _, tc := range []struct {
					name      string
					unblock   bool
					cancel    bool
					retries   int
					wantCount int
					wantError string
				}{
					{"retry_recovered", true, false, 2, 2, ""},
					{"retry_exhausted", false, false, 2, 1, "status 429"},
					{"retry_disabled", false, false, 0, 1, "status 429"},
					{"retry_cancelled", false, true, 2, -1, "context canceled"},
				} {
					t.Run(tc.name, func(t *testing.T) {
						server.setBlock(t, blockedIndex, true)
						t.Cleanup(func() { server.setBlock(t, blockedIndex, false) })
						handler := server.handler(t, info.Version.Number, index)
						handler.flushRetries = tc.retries
						id := tc.name
						good := NewRecord(map[string]interface{}{"value": 1})
						good.SetID(id)
						blocked := NewRecord(map[string]interface{}{"value": 2})
						blocked.SetID(id)
						blocked.Meta["_index"] = blockedIndex
						type outcome struct {
							count int
							err   error
						}
						done := make(chan outcome, 1)
						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						go func() {
							count, err := handler.flush(ctx, index, Records{good, blocked})
							done <- outcome{count, err}
						}()
						if tc.unblock || tc.cancel {
							require.Eventually(t, func() bool {
								status, _, err := server.document(index, id)
								return err == nil && status == http.StatusOK
							}, 5*time.Second, 10*time.Millisecond)
							if tc.unblock {
								server.setBlock(t, blockedIndex, false)
							} else {
								cancel()
							}
						}
						result := <-done
						if tc.cancel {
							// 服务端已写入时，客户端可能还在接收回执；取消发生的时机
							// 决定是否已确认这条成功，但不得把被阻止的文档算作成功。
							require.Contains(t, []int{0, 1}, result.count)
						} else {
							require.Equal(t, tc.wantCount, result.count)
						}
						if tc.wantError == "" {
							require.NoError(t, result.err)
						} else {
							require.ErrorContains(t, result.err, tc.wantError)
						}
						status, data, err := server.document(index, id)
						require.NoError(t, err)
						require.Equal(t, http.StatusOK, status, string(data))
						var document struct {
							Version int `json:"_version"`
						}
						require.NoError(t, json.Unmarshal(data, &document))
						// 成功文档的版本仍为 1，证明重试没有再次发送它。
						require.Equal(t, 1, document.Version)
					})
				}
			}

			for _, retries := range []int{0, 3} {
				t.Run(fmt.Sprintf("adapter_partial_success_retries_%d", retries), func(t *testing.T) {
					handler := server.handler(t, info.Version.Number, index)
					pipe := config.NewPipelineConfig()
					pipe.DataID = major
					ctx := config.PipelineConfigIntoContext(context.Background(), pipe)
					shipper := config.NewMetaClusterInfo()
					shipper.ClusterType = "elasticsearch"
					ctx = config.ShipperConfigIntoContext(ctx, shipper)
					adapter := pipeline.NewBulkBackendAdapter(ctx, "es-bulk-integration", handler, 2, time.Second, retries)
					t.Cleanup(func() { require.NoError(t, adapter.Close()) })
					successBefore := testutil.ToFloat64(adapter.CounterSuccesses)
					failBefore := testutil.ToFloat64(adapter.CounterFails)
					now := time.Now().Unix()
					for i, value := range []interface{}{1, "not-a-number"} {
						payload := define.NewJSONPayload(0)
						require.NoError(t, payload.From(&define.ETLRecord{Time: &now, Metrics: map[string]interface{}{"id": fmt.Sprintf("adapter-%d-%d", retries, i), "value": value}}))
						adapter.Push(payload, nil)
					}
					require.Eventually(t, func() bool {
						return testutil.ToFloat64(adapter.CounterSuccesses)-successBefore == 1 && testutil.ToFloat64(adapter.CounterFails)-failBefore == 1
					}, 5*time.Second, 10*time.Millisecond)
					id := handler.makeRecordID(map[string]interface{}{"id": fmt.Sprintf("adapter-%d-0", retries)})
					status, data, err := server.document(index, id)
					require.NoError(t, err)
					require.Equal(t, http.StatusOK, status, string(data))
					var document struct {
						Version int `json:"_version"`
					}
					require.NoError(t, json.Unmarshal(data, &document))
					require.Equal(t, 1, document.Version)
				})
			}

			if server.user != "" {
				t.Run("authentication_error", func(t *testing.T) {
					invalid := server
					invalid.secret += "-invalid"
					handler := invalid.handler(t, info.Version.Number, index)
					count, err := handler.Flush(context.Background(), []interface{}{&define.ETLRecord{Metrics: map[string]interface{}{"id": "unauthorized", "value": 1}}})
					require.Zero(t, count)
					require.ErrorContains(t, err, "HTTP 401")
				})
			}
		})
	}
}
