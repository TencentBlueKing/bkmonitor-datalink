// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/storage"
	promRemote "github.com/prometheus/prometheus/storage/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	routing "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	tsdbService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

type rangeTestDoc struct {
	at    time.Time
	group string
	value *float64
}

func TestReadRangeAggregateSamples(t *testing.T) {
	for _, tc := range []struct {
		name      string
		samples   []prompb.Sample
		wantError bool
	}{
		{name: "identical_duplicate", samples: []prompb.Sample{{Timestamp: 1000, Value: 24}, {Timestamp: 1000, Value: 24}}},
		{name: "different_value", samples: []prompb.Sample{{Timestamp: 1000, Value: 24}, {Timestamp: 1000, Value: 48}}, wantError: true},
		{name: "different_timestamp", samples: []prompb.Sample{{Timestamp: 1000, Value: 24}, {Timestamp: 2000, Value: 24}}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := promRemote.FromQueryResult(true, &prompb.QueryResult{Timeseries: []*prompb.TimeSeries{{Samples: tc.samples}}})
			values, err := readRangeAggregate(set)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, values, 1)
			for _, v := range values {
				assert.Equal(t, float64(24), v.value)
			}
		})
	}
}

// 以同一批原始文档模拟新旧 ES 的迁移重叠，实际执行路由发现、Select、ES DSL 和响应解析。
func TestClusterSwitchReferenceAggregation(t *testing.T) {
	metadata.InitMetadata()
	oldMax := tsdbService.EsMaxSize
	tsdbService.EsMaxSize = 10000
	t.Cleanup(func() { tsdbService.EsMaxSize = oldMax })
	start := time.Date(2026, 10, 27, 14, 0, 0, 0, time.UTC)
	cutover := start.Add(time.Hour)
	end := cutover.Add(time.Hour)
	value := func(v float64) *float64 { return &v }
	docs := []rangeTestDoc{
		{start, "common", value(10)},
		{cutover.Add(-time.Microsecond), "common", value(20)},
		{cutover, "common", value(30)},
		{cutover.Add(time.Microsecond), "common", value(40)},
		{end, "common", value(50)},
		{end.Add(time.Microsecond), "common", value(1000)},
		{cutover, "new", value(7)},
		{cutover, "missing", nil},
	}
	for _, tc := range []struct {
		name, method, field    string
		start, end             time.Time
		switches               []time.Time
		want                   map[string]float64
		failRoute              int
		failMethod             string
		wantPartial, wantError bool
		ungrouped              bool
	}{
		{name: "count_ungrouped", method: "count", field: "*", start: start, end: end, ungrouped: true, want: map[string]float64{"": 7}},
		{name: "avg_ungrouped", method: "avg", start: start, end: end, ungrouped: true, want: map[string]float64{"": 157.0 / 6}},
		{name: "count_before", method: "count", field: "*", start: start, end: cutover.Add(-time.Microsecond), want: map[string]float64{"common": 2}},
		{name: "count_after", method: "count", field: "*", start: cutover, end: end, want: map[string]float64{"common": 3, "new": 1, "missing": 1}},
		{name: "count_across_overlap", method: "count", field: "*", start: start, end: end, want: map[string]float64{"common": 5, "new": 1, "missing": 1}},
		{name: "sum_across_overlap", method: "sum", start: start, end: end, want: map[string]float64{"common": 150, "new": 7, "missing": 0}},
		{name: "min_across", method: "min", start: start, end: end, want: map[string]float64{"common": 10, "new": 7}},
		{name: "max_across", method: "max", start: start, end: end, want: map[string]float64{"common": 50, "new": 7}},
		{name: "avg_unequal_counts_and_null", method: "avg", start: start, end: end, want: map[string]float64{"common": 30, "new": 7}},
		{name: "end_at_cutover", method: "count", field: "*", start: start, end: cutover, want: map[string]float64{"common": 3, "new": 1, "missing": 1}},
		{name: "point_at_cutover", method: "count", field: "*", start: cutover, end: cutover, want: map[string]float64{"common": 1, "new": 1, "missing": 1}},
		{name: "one_microsecond_either_side", method: "sum", start: cutover.Add(-time.Microsecond), end: cutover.Add(time.Microsecond), want: map[string]float64{"common": 90, "new": 7, "missing": 0}},
		{name: "multiple_switches", method: "count", field: "*", start: start, end: end, switches: []time.Time{cutover, cutover.Add(30 * time.Minute)}, want: map[string]float64{"common": 5, "new": 1, "missing": 1}},
		{name: "sum_partial_failure", method: "sum", start: start, end: end, failRoute: 2, failMethod: "sum", wantPartial: true, want: map[string]float64{"common": 30}},
		{name: "avg_count_failure_discards_sum", method: "avg", start: start, end: end, failRoute: 2, failMethod: "value_count", wantPartial: true, want: map[string]float64{"common": 15}},
		{name: "all_routes_fail", method: "sum", start: start, end: end, failRoute: -1, failMethod: "sum", wantError: true},
		{name: "cardinality_rejected", method: "cardinality", start: start, end: end, wantError: true},
		{name: "percentiles_rejected", method: "percentiles", start: start, end: end, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.field == "" {
				tc.field = "value"
			}
			if tc.switches == nil {
				tc.switches = []time.Time{cutover}
			}
			starts := append([]time.Time{start.Add(-time.Hour)}, tc.switches...)
			records := make(routing.StorageClusterRecords, 0, len(starts))
			var mu sync.Mutex
			searches := 0
			for i, at := range starts {
				index := i + 1
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if !strings.Contains(r.URL.Path, "_search") {
						fmt.Fprint(w, `{"logs":{"settings":{},"mappings":{"properties":{"event_time":{"type":"long"},"value":{"type":"double"},"group":{"type":"keyword"}}}}}`)
						return
					}
					var body map[string]any
					decoder := json.NewDecoder(r.Body)
					decoder.UseNumber()
					if err := decoder.Decode(&body); err != nil {
						t.Error(err)
						http.Error(w, "invalid body", 400)
						return
					}
					mu.Lock()
					searches++
					mu.Unlock()
					aggs := rangeTestAggregations(body)
					encoded, _ := json.Marshal(aggs)
					if (tc.failRoute == -1 || tc.failRoute == index) && strings.Contains(string(encoded), `"`+tc.failMethod+`"`) {
						w.WriteHeader(http.StatusBadRequest)
						fmt.Fprint(w, `{"error":{"type":"test_failure","reason":"fixture backend failure"},"status":400}`)
						return
					}
					var selected []rangeTestDoc
					for _, doc := range docs {
						if rangeTestMatches(body["query"], doc.at.UnixMicro()) {
							selected = append(selected, doc)
						}
					}
					response := map[string]any{"aggregations": rangeTestAggregate(t, aggs, selected), "hits": map[string]any{"total": map[string]any{"value": len(selected), "relation": "eq"}, "hits": []any{}}}
					if err := json.NewEncoder(w).Encode(response); err != nil {
						t.Error(err)
					}
				}))
				t.Cleanup(server.Close)
				id := fmt.Sprintf("range-test-%s-%d", tc.name, i)
				tsdb.SetStorage(id, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: server.URL})
				records = append(records, routing.Record{StorageID: id, StorageType: metadata.ElasticsearchStorageType, DB: "logs", Measurement: "__default__", EnableTime: at.Unix()})
			}
			routes := (&routing.TsDBV2{StorageClusterRecords: records}).GetStorageIDRangesWithDirectionalOverlap(tc.start, tc.end, 0, 0)
			dimensions := []string{"group"}
			if tc.ungrouped {
				dimensions = nil
			}
			var queryList []*metadata.Query
			for _, route := range routes {
				queryList = append(queryList, &metadata.Query{
					TableID: "test.rt", DB: "logs", Measurement: "__default__", Field: tc.field,
					StorageID: route.StorageID, StorageType: metadata.ElasticsearchStorageType,
					TimeField:  metadata.TimeField{Name: "event_time", Type: "long", Unit: "microsecond"},
					Aggregates: metadata.Aggregates{{Name: tc.method, Dimensions: dimensions}},
					RouteStart: route.Start, RouteEnd: route.End, RouteEndInclusive: route.EndInclusive, RouteQueryStart: route.QueryStart, RouteQueryEnd: route.QueryEnd,
				})
			}
			ctx := metadata.InitHashID(context.Background())
			metadata.GetQueryParams(ctx).SetTime(tc.start, tc.start, tc.end, time.Minute, "microsecond", "UTC").SetIsReference(true)
			metadata.SetQueryReference(ctx, metadata.QueryReference{"q0": {&metadata.QueryMetric{QueryList: queryList}}})
			querier := NewQuerier(ctx, tc.start, tc.end, 2, time.Minute)
			set := querier.Select(true, &storage.SelectHints{}, labels.MustNewMatcher(labels.MatchEqual, labels.MetricName, "q0"))
			result, err := mock.SeriesSetToTimeSeries(set)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			actual := make(map[string]float64)
			for _, series := range result {
				require.Len(t, series.Samples, 1)
				assert.Equal(t, tc.start.UnixMilli(), series.Samples[0].Timestamp)
				if tc.ungrouped {
					actual[""] = series.Samples[0].Value
				}
				for _, label := range series.Labels {
					if label.Name == "group" {
						actual[label.Value] = series.Samples[0].Value
					}
				}
			}
			assert.Equal(t, tc.want, actual)
			status := metadata.GetStatus(ctx)
			if tc.wantPartial {
				require.NotNil(t, status)
				assert.Equal(t, metadata.QueryTsPartial, status.Code)
			} else if status != nil {
				assert.NotEqual(t, metadata.QueryTsPartial, status.Code)
			}
			assert.Positive(t, searches)
		})
	}
}

func rangeTestAggregations(body map[string]any) map[string]any {
	if a, ok := body["aggregations"].(map[string]any); ok {
		return a
	}
	a, _ := body["aggs"].(map[string]any)
	return a
}

func rangeTestMatches(query any, at int64) bool {
	switch node := query.(type) {
	case []any:
		for _, v := range node {
			if !rangeTestMatches(v, at) {
				return false
			}
		}
	case map[string]any:
		if ranges, ok := node["range"].(map[string]any); ok {
			for _, v := range ranges {
				r := v.(map[string]any)
				for key, bound := range r {
					n, ok := bound.(json.Number)
					if !ok {
						continue
					}
					value, _ := n.Int64()
					switch key {
					case "from":
						if at < value || (at == value && r["include_lower"] == false) {
							return false
						}
					case "to":
						if at > value || (at == value && r["include_upper"] == false) {
							return false
						}
					case "gte":
						if at < value {
							return false
						}
					case "gt":
						if at <= value {
							return false
						}
					case "lte":
						if at > value {
							return false
						}
					case "lt":
						if at >= value {
							return false
						}
					}
				}
			}
		}
		for key, v := range node {
			if key != "range" && !rangeTestMatches(v, at) {
				return false
			}
		}
	}
	return true
}

func rangeTestAggregate(t *testing.T, aggs map[string]any, docs []rangeTestDoc) map[string]any {
	result := map[string]any{}
	for name, raw := range aggs {
		agg := raw.(map[string]any)
		if _, ok := agg["terms"]; ok {
			groups := map[string][]rangeTestDoc{}
			for _, doc := range docs {
				groups[doc.group] = append(groups[doc.group], doc)
			}
			buckets := []any{}
			for group, members := range groups {
				bucket := rangeTestAggregate(t, rangeTestAggregations(agg), members)
				bucket["key"] = group
				bucket["doc_count"] = len(members)
				buckets = append(buckets, bucket)
			}
			result[name] = map[string]any{"buckets": buckets}
			continue
		}
		for method, rawOptions := range agg {
			options, ok := rawOptions.(map[string]any)
			if !ok {
				continue
			}
			count, sum, min, max := 0, 0.0, math.Inf(1), math.Inf(-1)
			for _, doc := range docs {
				if options["field"] == "_index" {
					count++
					continue
				}
				if doc.value != nil {
					count++
					sum += *doc.value
					min = math.Min(min, *doc.value)
					max = math.Max(max, *doc.value)
				}
			}
			var value any
			switch method {
			case "value_count":
				value = count
			case "sum":
				value = sum
			case "min":
				if count > 0 {
					value = min
				}
			case "max":
				if count > 0 {
					value = max
				}
			case "avg":
				if count > 0 {
					value = sum / float64(count)
				}
			default:
				t.Errorf("unexpected aggregation %s", method)
			}
			result[name] = map[string]any{"value": value}
		}
	}
	return result
}

func TestRoutedRangeAggregationClassification(t *testing.T) {
	for _, tc := range []struct {
		name, backend string
		agg           metadata.Aggregate
		want          bool
	}{
		{name: "no_time_bucket", backend: metadata.ElasticsearchStorageType, agg: metadata.Aggregate{Name: "sum"}, want: true},
		{name: "windowed_sum", backend: metadata.ElasticsearchStorageType, agg: metadata.Aggregate{Name: "sum", Window: time.Minute}},
		{name: "explicit_histogram", backend: metadata.ElasticsearchStorageType, agg: metadata.Aggregate{Name: "date_histogram", Without: true, Window: time.Minute}},
		{name: "es_without_time_bucket", backend: metadata.ElasticsearchStorageType, agg: metadata.Aggregate{Name: "sum", Without: true, Window: time.Minute}, want: true},
		{name: "doris_still_has_time_bucket", backend: metadata.BkSqlStorageType, agg: metadata.Aggregate{Name: "sum", Without: true, Window: time.Minute}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := &metadata.Query{StorageType: tc.backend, Aggregates: metadata.Aggregates{tc.agg}}
			list := QueryList{&Query{qry: query, queryStart: time.Unix(1, 0), queryEnd: time.Unix(100, 0)}}
			assert.Equal(t, tc.want, list.hasRoutedRangeAggregate())
			reference := metadata.QueryReference{"q0": {&metadata.QueryMetric{QueryList: []*metadata.Query{query}}}}
			window, _ := reference.GetMaxWindowAndTimezone()
			if tc.want {
				assert.Zero(t, window)
			} else {
				assert.Equal(t, time.Minute, window)
			}
			list[0].queryStart = time.Time{}
			list[0].queryEnd = time.Time{}
			assert.False(t, list.hasRoutedRangeAggregate(), "普通无迁移查询保持原路径")
		})
	}
}
