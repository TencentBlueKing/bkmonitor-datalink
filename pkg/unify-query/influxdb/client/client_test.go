// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
)

// TestDecodeResp
func TestDecodeResp(t *testing.T) {
	testCases := map[string]struct {
		data      *decoder.Response
		actual    *decoder.Response
		chunked   bool
		chunkSize int
	}{
		"aggr data": {
			data: &decoder.Response{
				Results: []decoder.Result{
					{
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Tags:    map[string]string{"ip": "127.0.0.1"},
								Columns: []string{"_time", "_value"},
								// Vals 中的值这里用字符串：因为ChunkResponse 使用了json.UseNumber，这里方便断言，用字符串做值
								Values: [][]any{
									{"2022-04-14T01:00:00Z", "1"},
									{"2022-04-14T01:01:00Z", "1"},
									{"2022-04-14T01:02:00Z", "1"},
									{"2022-04-14T01:03:00Z", "1"},
									{"2022-04-14T01:04:00Z", "1"},
									{"2022-04-14T01:05:00Z", "1"},
									{"2022-04-14T01:06:00Z", "1"},
								},
							}, {
								Name:    "metric_name",
								Tags:    map[string]string{"ip": "127.0.0.1"},
								Columns: []string{"_time", "_value"},
								Values: [][]any{
									{"2022-04-14T01:00:00Z", "2"},
									{"2022-04-14T01:01:00Z", "2"},
									{"2022-04-14T01:02:00Z", "2"},
									{"2022-04-14T01:03:00Z", "2"},
									{"2022-04-14T01:04:00Z", "2"},
									{"2022-04-14T01:05:00Z", "2"},
									{"2022-04-14T01:06:00Z", "2"},
								},
							},
						},
					},
				},
			},
			actual: &decoder.Response{
				Results: []decoder.Result{
					{
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Tags:    map[string]string{"ip": "127.0.0.1"},
								Columns: []string{"_time", "_value"},
								// Vals 中的值这里用字符串：因为ChunkResponse 使用了json.UseNumber，这里方便断言，用字符串做值
								Values: [][]any{
									{"2022-04-14T01:00:00Z", "1"},
									{"2022-04-14T01:01:00Z", "1"},
								},
								Partial: true,
							},
						},
						Partial: true,
					},
					{
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Tags:    map[string]string{"ip": "127.0.0.1"},
								Columns: []string{"_time", "_value"},
								// Vals 中的值这里用字符串：因为ChunkResponse 使用了json.UseNumber，这里方便断言，用字符串做值
								Values: [][]any{
									{"2022-04-14T01:02:00Z", "1"},
									{"2022-04-14T01:03:00Z", "1"},
								},
								Partial: true,
							},
						},
						Partial: true,
					},
					{
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Tags:    map[string]string{"ip": "127.0.0.1"},
								Columns: []string{"_time", "_value"},
								// Vals 中的值这里用字符串：因为ChunkResponse 使用了json.UseNumber，这里方便断言，用字符串做值
								Values: [][]any{
									{"2022-04-14T01:04:00Z", "1"},
									{"2022-04-14T01:05:00Z", "1"},
								},
								Partial: true,
							},
						},
						Partial: true,
					},
					{
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Tags:    map[string]string{"ip": "127.0.0.1"},
								Columns: []string{"_time", "_value"},
								// Vals 中的值这里用字符串：因为ChunkResponse 使用了json.UseNumber，这里方便断言，用字符串做值
								Values: [][]any{
									{"2022-04-14T01:06:00Z", "1"},
								},
								Partial: false,
							},
						},
						Partial: true,
					},
					{
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Tags:    map[string]string{"ip": "127.0.0.1"},
								Columns: []string{"_time", "_value"},
								Values: [][]any{
									{"2022-04-14T01:00:00Z", "2"},
									{"2022-04-14T01:01:00Z", "2"},
								},
								Partial: true,
							},
						},
						Partial: true,
					},
					{
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Tags:    map[string]string{"ip": "127.0.0.1"},
								Columns: []string{"_time", "_value"},
								Values: [][]any{
									{"2022-04-14T01:02:00Z", "2"},
									{"2022-04-14T01:03:00Z", "2"},
								},
								Partial: true,
							},
						},
						Partial: true,
					}, {
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Tags:    map[string]string{"ip": "127.0.0.1"},
								Columns: []string{"_time", "_value"},
								Values: [][]any{
									{"2022-04-14T01:04:00Z", "2"},
									{"2022-04-14T01:05:00Z", "2"},
								},
								Partial: true,
							},
						},
						Partial: true,
					}, {
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Tags:    map[string]string{"ip": "127.0.0.1"},
								Columns: []string{"_time", "_value"},
								Values: [][]any{
									{"2022-04-14T01:06:00Z", "2"},
								},
								Partial: false,
							},
						},
						Partial: false,
					},
				},
				Err: "",
			},
			chunked:   true,
			chunkSize: 2,
		},
		"unaggr data": {
			data: &decoder.Response{
				Results: []decoder.Result{
					{
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Columns: []string{"_time", "_value", "ip"},
								// Vals 中的值这里用字符串：因为ChunkResponse 使用了json.UseNumber，这里方便断言，用字符串做值
								Values: [][]any{
									{"2022-04-14T01:00:00Z", "1", "127.0.0.1"},
									{"2022-04-14T01:00:00Z", "2", "127.0.0.1"},
									{"2022-04-14T01:01:00Z", "1", "127.0.0.1"},
									{"2022-04-14T01:01:00Z", "2", "127.0.0.1"},
									{"2022-04-14T01:02:00Z", "1", "127.0.0.1"},
									{"2022-04-14T01:02:00Z", "2", "127.0.0.1"},
									{"2022-04-14T01:03:00Z", "1", "127.0.0.1"},
									{"2022-04-14T01:03:00Z", "2", "127.0.0.1"},
									{"2022-04-14T01:04:00Z", "1", "127.0.0.1"},
									{"2022-04-14T01:04:00Z", "2", "127.0.0.1"},
									{"2022-04-14T01:05:00Z", "1", "127.0.0.1"},
									{"2022-04-14T01:05:00Z", "2", "127.0.0.1"},
									{"2022-04-14T01:06:00Z", "1", "127.0.0.1"},
									{"2022-04-14T01:06:00Z", "2", "127.0.0.1"},
								},
							},
						},
					},
				},
			},
			actual: &decoder.Response{
				Results: []decoder.Result{
					{
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Columns: []string{"_time", "_value", "ip"},
								// Vals 中的值这里用字符串：因为ChunkResponse 使用了json.UseNumber，这里方便断言，用字符串做值
								Values: [][]any{
									{"2022-04-14T01:00:00Z", "1", "127.0.0.1"},
									{"2022-04-14T01:00:00Z", "2", "127.0.0.1"},
								},
								Partial: true,
							},
						},
						Partial: true,
					},
					{
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Columns: []string{"_time", "_value", "ip"},
								// Vals 中的值这里用字符串：因为ChunkResponse 使用了json.UseNumber，这里方便断言，用字符串做值
								Values: [][]any{
									{"2022-04-14T01:01:00Z", "1", "127.0.0.1"},
									{"2022-04-14T01:01:00Z", "2", "127.0.0.1"},
								},
								Partial: true,
							},
						},
						Partial: true,
					},
					{
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Columns: []string{"_time", "_value", "ip"},
								// Vals 中的值这里用字符串：因为ChunkResponse 使用了json.UseNumber，这里方便断言，用字符串做值
								Values: [][]any{
									{"2022-04-14T01:02:00Z", "1", "127.0.0.1"},
									{"2022-04-14T01:02:00Z", "2", "127.0.0.1"},
								},
								Partial: true,
							},
						},
						Partial: true,
					}, {
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Columns: []string{"_time", "_value", "ip"},
								// Vals 中的值这里用字符串：因为ChunkResponse 使用了json.UseNumber，这里方便断言，用字符串做值
								Values: [][]any{
									{"2022-04-14T01:03:00Z", "1", "127.0.0.1"},
									{"2022-04-14T01:03:00Z", "2", "127.0.0.1"},
								},
								Partial: true,
							},
						},
						Partial: true,
					}, {
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Columns: []string{"_time", "_value", "ip"},
								// Vals 中的值这里用字符串：因为ChunkResponse 使用了json.UseNumber，这里方便断言，用字符串做值
								Values: [][]any{
									{"2022-04-14T01:04:00Z", "1", "127.0.0.1"},
									{"2022-04-14T01:04:00Z", "2", "127.0.0.1"},
								},
								Partial: true,
							},
						},
						Partial: true,
					}, {
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Columns: []string{"_time", "_value", "ip"},
								// Vals 中的值这里用字符串：因为ChunkResponse 使用了json.UseNumber，这里方便断言，用字符串做值
								Values: [][]any{
									{"2022-04-14T01:05:00Z", "1", "127.0.0.1"},
									{"2022-04-14T01:05:00Z", "2", "127.0.0.1"},
								},
								Partial: true,
							},
						},
						Partial: true,
					}, {
						StatementID: 0,
						Series: []*decoder.Row{
							{
								Name:    "metric_name",
								Columns: []string{"_time", "_value", "ip"},
								// Vals 中的值这里用字符串：因为ChunkResponse 使用了json.UseNumber，这里方便断言，用字符串做值
								Values: [][]any{
									{"2022-04-14T01:06:00Z", "1", "127.0.0.1"},
									{"2022-04-14T01:06:00Z", "2", "127.0.0.1"},
								},
								Partial: false,
							},
						},
						Partial: false,
					},
				},
			},
			chunked:   true,
			chunkSize: 2,
		},
	}

	handlerGen := func(clientResp *decoder.Response, expectChunk bool, expectChunkSize int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {

			assert.Equal(t, "db", r.FormValue("db"))
			assert.Equal(t, "sql", r.FormValue("q"))
			chunkedStr := r.FormValue("chunked")
			assert.Equal(t, fmt.Sprint(expectChunk), chunkedStr)
			chunkSizeStr := r.FormValue("chunk_size")
			chunkSize, err := strconv.Atoi(chunkSizeStr)
			assert.NoError(t, err)
			assert.Equal(t, expectChunkSize, chunkSize)

			if clientResp.Err != "" {
				w.WriteHeader(http.StatusBadRequest)
				data, _ := json.Marshal(clientResp)
				_, _ = w.Write(data)
				return
			}

			w.Header().Set("Content-type", "application/json")
			w.WriteHeader(http.StatusOK)
			if chunkedStr != "true" {
				data, _ := json.Marshal(clientResp)
				_, _ = w.Write(data)
				return
			}

			for _, res := range clientResp.Results {
				var baseResult = decoder.Result{
					StatementID: res.StatementID,
					Messages:    res.Messages,
					Err:         res.Err,
					Partial:     true,
				}

				// 否则按照chunkSize按点数返回
				for si, series := range res.Series {
					var resp = new(decoder.Response)
					resp.Err = clientResp.Err

					// chunkSize 等于0，一条条的series返回
					if chunkSize <= 0 {
						baseResult.Series = []*decoder.Row{series}
						resp.Results = []decoder.Result{baseResult}
						data, _ := json.Marshal(resp)
						_, _ = w.Write(data)
						w.(http.Flusher).Flush()
						continue
					}

					// 否则按照chunkSize发送
					lenVals := len(series.Values)
					for i := 0; i < lenVals; i += chunkSize {
						// 一次最多发送一条series
						baseResult.Series = []*decoder.Row{
							{
								Name:    series.Name,
								Tags:    series.Tags,
								Columns: series.Columns,
								Values:  make([][]any, 0),
							},
						}
						if i+chunkSize < lenVals {
							baseResult.Series[0].Values = series.Values[i : i+chunkSize]
							baseResult.Series[0].Partial = true
						} else {
							baseResult.Series[0].Values = series.Values[i:]
							baseResult.Series[0].Partial = false
							if si == len(res.Series)-1 {
								baseResult.Partial = false
							}
						}
						resp.Results = []decoder.Result{baseResult}
						data, _ := json.Marshal(resp)
						w.Write(data)
						w.Write([]byte("\n"))
						w.(http.Flusher).Flush()
					}
				}
			}
		}
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {

			mux := http.NewServeMux()
			mux.Handle("/query", handlerGen(testCase.data, testCase.chunked, testCase.chunkSize))
			s := httptest.NewServer(mux)
			defer s.Close()

			c := NewBasicClient(s.URL, "", "", "application/json", testCase.chunkSize)
			resp, err := c.Query(context.Background(), "db", "sql", "", "application/json", testCase.chunked)

			actual := testCase.actual

			assert.NoError(t, err)
			assert.Equal(t, len(actual.Results), len(resp.Results))
			assert.Equal(t, actual.Err, resp.Err)

			for i, r := range testCase.actual.Results {
				assert.Equal(t, r.Err, resp.Results[i].Err)
				assert.Equal(t, len(r.Messages), len(resp.Results[i].Messages))
				assert.Equal(t, r.Partial, resp.Results[i].Partial)

				actualSeries := resp.Results[i].Series
				for j, series := range r.Series {
					//series.SameSeries(actualSeries[j])
					//assert.Equal(t, tagsHash(series), tagsHash(actualSeries[j]))
					assert.Equal(t, series.Name, actualSeries[j].Name)
					assert.Equal(t, series.Columns, actualSeries[j].Columns)
					assert.Equal(t, series.Partial, actualSeries[j].Partial)
					assert.Equal(t, series.Tags, actualSeries[j].Tags)
					assert.Equal(t, series.Values, actualSeries[j].Values)
				}
			}

		})
	}

}
