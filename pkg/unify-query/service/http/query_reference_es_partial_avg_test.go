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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	queryPkg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

func TestQueryReferenceESPartialAverageAcrossRoutes(t *testing.T) {
	mock.Init()
	promql.MockEngine()
	start := time.Unix(1700000000, 0)
	cut := start.Add(time.Hour)
	end := cut.Add(time.Hour)
	for _, tc := range []struct {
		name                                    string
		instant, grouped, disabled, singleRoute bool
		partialSum, partialCount, partialNew    bool
		method                                  string
		want                                    float64
		wantPartial, wantError                  bool
	}{
		{name: "different successful shards", instant: true, partialSum: true, partialCount: true, want: 10, wantPartial: true},
		{name: "sum becomes partial", partialSum: true, want: 10, wantPartial: true},
		{name: "count becomes partial", partialCount: true, want: 10, wantPartial: true},
		{name: "grouped different successful shards", grouped: true, partialSum: true, partialCount: true, want: 10, wantPartial: true},
		{name: "healthy weighted average", instant: true, want: 390.0 / 7},
		{name: "healthy grouped weighted average", grouped: true, want: 390.0 / 7},
		{name: "all routes partial", instant: true, partialSum: true, partialNew: true, wantError: true},
		{name: "disabled keeps route errors", instant: true, disabled: true, partialSum: true, partialCount: true, want: 10},
		{name: "single route native average", instant: true, singleRoute: true, partialNew: true, want: 10, wantPartial: true},
		{name: "sum still accepts partial shards", instant: true, method: "sum", partialSum: true, want: 30, wantPartial: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type document struct {
				time  time.Time
				shard int
				group string
				value float64
			}
			oldDocs := []document{
				{start.Add(time.Minute), 0, "api", 10}, {start.Add(2 * time.Minute), 0, "api", 10},
				{start.Add(3 * time.Minute), 1, "api", 90}, {start.Add(4 * time.Minute), 1, "api", 90},
				{start.Add(5 * time.Minute), 1, "api", 90}, {start.Add(6 * time.Minute), 1, "api", 90},
				{cut, 0, "api", 1000},
			}
			newDocs := []document{{cut.Add(time.Minute), 0, "api", 10}, {start.Add(time.Minute), 0, "api", 1000}}
			if tc.grouped {
				oldDocs = append(oldDocs, document{start.Add(time.Minute), 0, "worker", 100}, document{start.Add(2 * time.Minute), 0, "worker", 100}, document{start.Add(3 * time.Minute), 1, "worker", 200})
				newDocs = append(newDocs, document{cut.Add(time.Minute), 0, "worker", 30})
			}
			var callsMu sync.Mutex
			gotQueries := make(map[string][]string)
			selectedValues := make(map[string]map[string][]float64)
			for route, docs := range [][]document{oldDocs, newDocs} {
				route, docs := route, docs
				address := fmt.Sprintf("http://partial-average-%d.example.test", route)
				storageID := fmt.Sprintf("partial-average-%d", route)
				tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: address})
				httpmock.RegisterResponder(http.MethodGet, address+"/avg_route",
					httpmock.NewStringResponder(http.StatusOK, `{"avg_route":{"mappings":{"properties":{"event_time":{"type":"long"},"service":{"type":"keyword"},"value":{"type":"double"}}}}}`))
				httpmock.RegisterResponder(http.MethodPost, address+"/avg_route/_search", func(req *http.Request) (*http.Response, error) {
					var body struct {
						Query struct {
							Bool struct {
								Filter struct {
									Range map[string]struct {
										From, To     int64
										IncludeLower bool `json:"include_lower"`
										IncludeUpper bool `json:"include_upper"`
									} `json:"range"`
								} `json:"filter"`
							} `json:"bool"`
						} `json:"query"`
						Aggregations json.RawMessage `json:"aggregations"`
					}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						return nil, err
					}
					bounds, ok := body.Query.Bool.Filter.Range["event_time"]
					if !ok {
						return nil, fmt.Errorf("missing event_time range")
					}
					method := "avg"
					if strings.Contains(string(body.Aggregations), `"sum"`) {
						method = "sum"
					}
					if strings.Contains(string(body.Aggregations), `"value_count"`) {
						method = "count"
					}
					callsMu.Lock()
					gotQueries[storageID] = append(gotQueries[storageID], method)
					callsMu.Unlock()
					partial := route == 0 && (method == "sum" && tc.partialSum || method == "count" && tc.partialCount) || route == 1 && tc.partialNew
					assertPartialESNativeParameter(t, req, !tc.disabled)
					if route == 0 {
						assert.Equal(t, cut.UnixMilli(), bounds.To)
						assert.False(t, bounds.IncludeUpper)
					} else {
						assert.Equal(t, cut.UnixMilli(), bounds.From)
						assert.True(t, bounds.IncludeLower)
					}
					sums := map[string]float64{}
					counts := map[string]int{}
					var selected []float64
					for _, doc := range docs {
						timestamp := doc.time.UnixMilli()
						if timestamp < bounds.From || timestamp == bounds.From && !bounds.IncludeLower || timestamp > bounds.To || timestamp == bounds.To && !bounds.IncludeUpper {
							continue
						}
						if partial && (method == "count" && doc.shard != 1 || method != "count" && doc.shard != 0) {
							continue
						}
						group := ""
						if tc.grouped {
							group = doc.group
						}
						sums[group] += doc.value
						counts[group]++
						selected = append(selected, doc.value)
					}
					callsMu.Lock()
					if selectedValues[storageID] == nil {
						selectedValues[storageID] = make(map[string][]float64)
					}
					selectedValues[storageID][method] = selected
					callsMu.Unlock()
					metric := func(group string) float64 {
						switch method {
						case "sum":
							return sums[group]
						case "count":
							return float64(counts[group])
						default:
							return sums[group] / float64(counts[group])
						}
					}
					aggregations := map[string]any{"_value": map[string]any{"value": metric("")}}
					if tc.grouped {
						var buckets []map[string]any
						for group := range sums {
							buckets = append(buckets, map[string]any{"key": group, "doc_count": counts[group], "_value": map[string]any{"value": metric(group)}})
						}
						aggregations = map[string]any{"service": map[string]any{"buckets": buckets}}
					}
					shards := map[string]any{"total": 2, "successful": 2, "failed": 0}
					if partial {
						failedShard := 1
						if method == "count" {
							failedShard = 0
						}
						shards = map[string]any{"total": 2, "successful": 1, "failed": 1, "failures": []map[string]any{{"shard": failedShard, "reason": map[string]any{"type": "node_not_connected_exception", "reason": "test shard unavailable"}}}}
					}
					return httpmock.NewJsonResponse(http.StatusOK, map[string]any{"_shards": shards, "aggregations": aggregations})
				})
			}
			queryStart := start
			if tc.singleRoute {
				queryStart = cut
			}
			method := tc.method
			if method == "" {
				method = "avg"
			}
			aggregate := structured.AggregateMethod{Method: method}
			if tc.grouped {
				aggregate.Dimensions = []string{"service"}
			}
			query := &structured.QueryTs{
				SpaceUid: "bkcc__2", Start: strconv.FormatInt(queryStart.UnixMilli(), 10), End: strconv.FormatInt(end.UnixMilli(), 10), Step: "1h", Instant: tc.instant, MetricMerge: "a", AllowPartialESResults: !tc.disabled,
				QueryList: []*structured.Query{{TableID: "average.routes", FieldName: "value", ReferenceName: "a", AggregateMethodList: structured.AggregateMethodList{aggregate}}},
				TsDBMap: map[string]structured.TsDBs{"a": {{
					TableID: "average.routes", DB: "avg_route", StorageID: "partial-average-1", StorageType: metadata.ElasticsearchStorageType,
					TimeField: metadata.TimeField{Name: "event_time", Type: "long", Unit: "millisecond"},
					StorageClusterRecords: queryPkg.StorageClusterRecords{
						{StorageID: "partial-average-0", EnableTime: start.Add(-time.Hour).Unix()},
						{StorageID: "partial-average-1", EnableTime: cut.Unix()},
					},
				}}},
			}
			ctx := metadata.InitHashID(context.Background())
			metadata.SetUser(ctx, &metadata.User{SpaceUID: query.SpaceUid})
			body, err := json.Marshal(query)
			require.NoError(t, err)
			request := httptest.NewRequest(http.MethodPost, "/query/ts/reference", bytes.NewReader(body)).WithContext(ctx)
			recorder := runCheckHandler(t, request, HandlerQueryReference)
			if tc.wantError {
				require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
				return
			}
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var response struct {
				IsPartial bool             `json:"is_partial"`
				Status    *metadata.Status `json:"status"`
				Series    []struct {
					GroupValues []string    `json:"group_values"`
					Values      [][]float64 `json:"values"`
				} `json:"series"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, tc.wantPartial, response.IsPartial, recorder.Body.String())
			require.NotEmpty(t, response.Series, recorder.Body.String())
			for _, series := range response.Series {
				want := tc.want
				if tc.grouped && len(series.GroupValues) > 0 && series.GroupValues[0] == "worker" {
					want = 30
					if !tc.wantPartial {
						want = 107.5
					}
				}
				require.NotEmpty(t, series.Values)
				assert.InDelta(t, want, series.Values[0][1], 1e-9, recorder.Body.String())
			}
			if tc.wantPartial {
				require.NotNil(t, response.Status)
				assert.Equal(t, metadata.QueryTsPartial, response.Status.Code)
			}
			if !tc.wantPartial && !tc.disabled {
				assert.Nil(t, response.Status)
			}
			if tc.disabled {
				require.NotNil(t, response.Status)
				assert.Equal(t, metadata.QueryTsPartial, response.Status.Code)
			}
			if tc.singleRoute {
				assert.Empty(t, gotQueries["partial-average-0"])
				assert.Equal(t, []string{"avg"}, gotQueries["partial-average-1"])
			} else if method == "avg" {
				expectedOld := []string{"sum", "count"}
				if tc.disabled && tc.partialSum {
					expectedOld = []string{"sum"}
				}
				assert.Equal(t, expectedOld, gotQueries["partial-average-0"])
				assert.Equal(t, []string{"sum", "count"}, gotQueries["partial-average-1"])
			}
			if !tc.grouped && tc.partialSum && tc.partialCount && !tc.disabled {
				assert.Equal(t, []float64{10, 10}, selectedValues["partial-average-0"]["sum"])
				assert.Equal(t, []float64{90, 90, 90, 90}, selectedValues["partial-average-0"]["count"])
			}
		})
	}
}
