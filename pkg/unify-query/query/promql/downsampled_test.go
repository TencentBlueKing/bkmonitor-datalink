// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/hashicorp/consul/api"
	"github.com/influxdata/influxql"
	"github.com/prashantv/gostub"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/mocktest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// mockDownSampledData
func mockDownSampledData(t *testing.T, results map[string]decoder.Response) (*gomock.Controller, *gostub.Stubs) {
	log.InitTestLogger()
	ctrl := gomock.NewController(t)

	res, _ := json.Marshal(results)
	fmt.Println(string(res))

	mockClient := mocktest.NewMockClient(ctrl)
	mockClient.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, db, sql, precision, contentType string, chunked bool) (*decoder.Response, error) {
		statement := influxql.MustParseStatement(sql).(*influxql.SelectStatement)
		measurement := statement.Sources[0].(*influxql.Measurement)

		var window string
		for _, d := range statement.Dimensions {
			switch a := d.Expr.(type) {
			case *influxql.Call:
				window = a.Args[0].String()
			}
		}

		var aggr string
		var field string
		for _, f := range statement.Fields {
			switch a := f.Expr.(type) {
			case *influxql.Call:
				aggr = a.Name
				field = a.Args[0].String()
			}
		}

		k := fmt.Sprintf("%s|%s|%s|%s", aggr, field, measurement.RetentionPolicy, window)

		result, ok := results[k]
		fmt.Printf("load result with: %s\n", k)
		if !ok {
			var kList []string
			for l := range results {
				kList = append(kList, l)
			}
			return nil, fmt.Errorf("result key %s is not exist in %v\n", k, kList)
		}
		return &result, nil
	}).AnyTimes()

	_ = influxdb.InitGlobalInstance(context.Background(), &influxdb.Params{
		Timeout: 30 * time.Second,
	}, mockClient)

	stubs := gostub.New()
	stubs = gostub.Stub(&influxdb.GetTableIDByDBAndMeasurement, func(db, measurement string) *consul.TableID {
		return &consul.TableID{
			DB:                 db,
			Measurement:        measurement,
			IsSplitMeasurement: true,
		}
	})
	return ctrl, stubs
}

// point
type point struct {
	values []float64
	max    float64
	min    float64
	sum    float64
	count  int64
	avg    float64
	start  int64
	end    int64
}

// NewPoint
func NewPoint(values []float64) *point {
	var p = &point{}
	p.values = values
	for i, v := range p.values {
		p.sum += v
		p.count++
		if i == 0 || v > p.max {
			p.max = v
		}
		if i == 0 || v < p.min {
			p.min = v
		}
	}
	return p
}

// String
func (p *point) String() string {
	return fmt.Sprintf("start: %d, end: %d, values: %+v, min: %f, max: %f, count: %d, sum: %f, avg: %f", p.start, p.end, p.values, p.min, p.max, p.count, p.sum, p.avg)
}

// Value
func (p *point) Value(aggr string) float64 {
	switch aggr {
	case "max":
		return p.max
	case "min":
		return p.min
	case "count":
		return float64(p.count)
	case "sum":
		return p.sum
	default:
		return p.sum / float64(p.count)
	}
}

// createRpData
func createRpData(max int64, aggr, field, rp, window string) map[string]decoder.Response {
	baseT := time.Unix(0, 0)
	columns := []string{"_time", "_value", "tag"}
	var tags = []string{"1", "2"}

	t, _ := time.ParseDuration(window)

	newAggr := aggr
	if rp != "" {
		field = fmt.Sprintf("%s_%s", aggr, field)
		if aggr == COUNT {
			newAggr = SUM
		}
	}

	key := strings.Join([]string{newAggr, field, rp, window}, "|")
	pre := int64(t.Minutes())
	var series []*decoder.Row
	for j, tag := range tags {
		var values [][]interface{}
		var points []float64
		var now time.Time
		for i := int64(0); i < max; i++ {
			v := int64(10)
			points = append(points, float64(i*int64(j)+v))
			if (i % pre) == 0 {
				now = baseT.Add(time.Duration(i) * time.Minute)
			}
			if (i%pre) == (pre-1) || (i == max-1) {
				p := NewPoint(points)
				p.start = now.Unix()
				p.end = i * int64(time.Minute.Seconds())
				nv := p.Value(aggr)
				//fmt.Printf("%s\n", p)
				points = []float64{}
				values = append(values, []interface{}{
					now.Format(time.RFC3339Nano), nv, tag,
				})
			}
		}
		series = append(series, &decoder.Row{
			Tags: map[string]string{
				"tag": tag,
			},
			Name:    fmt.Sprintf("%s_%d", key, j),
			Columns: columns,
			Values:  values,
		})
	}

	result := make(map[string]decoder.Response)
	result[key] = decoder.Response{
		Results: []decoder.Result{
			{
				Series: series,
			},
		},
	}

	return result
}

// mergeMap
func mergeMap(mObj ...map[string]decoder.Response) map[string]decoder.Response {
	newObj := map[string]decoder.Response{}
	for _, m := range mObj {
		for k, v := range m {
			newObj[k] = v
		}
	}
	return newObj
}

// mockMetaData
func mockMetaData(t *testing.T) (*gomock.Controller, *gostub.Stubs) {
	ctrl := gomock.NewController(t)
	database := "2_bkapm_metric_apm_test_have_data"
	prefixPath := fmt.Sprintf("%s/downsampled/%s", consul.MetadataPath, database)
	data := api.KVPairs{
		{
			Key:   fmt.Sprintf("%s/cq", prefixPath),
			Value: []byte(`{"tag_name":"","tag_value":[""],"enable":true}`),
		},
		{
			Key:   fmt.Sprintf("%s/rp/5m", prefixPath),
			Value: []byte(`{"duration":"720h","resolution":300}`),
		},
		{
			Key:   fmt.Sprintf("%s/rp/1h", prefixPath),
			Value: []byte(`{"duration":"720h","resolution":3600}`),
		},
		{
			Key:   fmt.Sprintf("%s/rp/12h", prefixPath),
			Value: []byte(`{"duration":"720h","resolution":43200}`),
		},
		{
			Key:   fmt.Sprintf("%s/cq/__all__/value/max/5m", prefixPath),
			Value: []byte(`{"source_rp":"autogen"}`),
		},
		{
			Key:   fmt.Sprintf("%s/cq/__all__/value/max/1h", prefixPath),
			Value: []byte(`{"source_rp":"5m"}`),
		},
		{
			Key:   fmt.Sprintf("%s/cq/__all__/value/count/5m", prefixPath),
			Value: []byte(`{"source_rp":"autogen"}`),
		},
		{
			Key:   fmt.Sprintf("%s/cq/__all__/value/count/1h", prefixPath),
			Value: []byte(`{"source_rp":"5m"}`),
		},
		{
			Key:   fmt.Sprintf("%s/cq/__all__/value/mean/1h", prefixPath),
			Value: []byte(`{"source_rp":"5m"}`),
		},
		{
			Key:   fmt.Sprintf("%s/cq/__all__/value/sum/1h", prefixPath),
			Value: []byte(`{"source_rp":"5m"}`),
		},
	}
	stubs := gostub.Stub(&consul.GetDataWithPrefix, func(prefix string) (api.KVPairs, error) {
		return data, nil
	})

	consul.LoadDownsampledInfo()
	return ctrl, stubs
}

// TestDownSampledQuery
func TestDownSampledQuery(t *testing.T) {
	metric := "bk_apm_duration"
	max := int64(60)

	field := "value"
	results := mergeMap(
		createRpData(max, "sum", field, "", "2m"),
		createRpData(max, "sum", field, "", "5m"),
		createRpData(max, "sum", field, "1h", "1h"),
		createRpData(max, "count", field, "", "2m"),
		createRpData(max, "count", field, "5m", "5m"),
		createRpData(max, "count", field, "5m", "10m"),
		createRpData(max, "mean", field, "", "2m"),
		createRpData(max, "mean", field, "", "10m"),
		createRpData(max, "max", field, "1h", "1h"),
	)
	ctrl, stubs := mockDownSampledData(t, results)
	defer ctrl.Finish()
	defer stubs.Reset()

	ctrl, stubs = mockMetaData(t)
	defer ctrl.Finish()
	defer stubs.Reset()

	testCases := map[string]struct {
		window   string
		function string
		iscount  bool
		aggr     string
		minutes  int64
		sql      string
		tables   *Tables
	}{
		"sum_2m": {
			window:   "2m",
			function: "sum",
			aggr:     "sum_over_time",
			minutes:  10,
			tables: &Tables{
				Tables: []*Table{
					{
						Name:        "series0",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"1"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(20)},
							{2 * time.Minute.Milliseconds(), float64(20)},
							{4 * time.Minute.Milliseconds(), float64(20)},
							{6 * time.Minute.Milliseconds(), float64(20)},
							{8 * time.Minute.Milliseconds(), float64(20)},
						},
					},
					{
						Name:        "series1",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"2"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(21)}, // 10+11
							{2 * time.Minute.Milliseconds(), float64(25)}, // 12+13
							{4 * time.Minute.Milliseconds(), float64(29)}, // 14+15
							{6 * time.Minute.Milliseconds(), float64(33)}, // 16+17
							{8 * time.Minute.Milliseconds(), float64(37)}, // 18+19
						},
					},
				},
			},
		},
		"sum_5m": {
			window:   "5m",
			function: "sum",
			aggr:     "sum_over_time",
			minutes:  20,
			tables: &Tables{
				Tables: []*Table{
					{
						Name:        "series0",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"1"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(50)},
							{5 * time.Minute.Milliseconds(), float64(50)},
							{10 * time.Minute.Milliseconds(), float64(50)},
							{15 * time.Minute.Milliseconds(), float64(50)},
						},
					},
					{
						Name:        "series1",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"2"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(60)},
							{5 * time.Minute.Milliseconds(), float64(85)},
							{10 * time.Minute.Milliseconds(), float64(110)},
							{15 * time.Minute.Milliseconds(), float64(135)},
						},
					},
				},
			},
		},
		"sum_1h": {
			window:   "1h",
			function: "sum",
			aggr:     "sum_over_time",
			minutes:  60,
			tables: &Tables{
				Tables: []*Table{
					{
						Name:        "series0",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"1"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(600)},
						},
					},
					{
						Name:        "series1",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"2"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(2370)},
						},
					},
				},
			},
		},
		"count_2m": {
			iscount:  true,
			window:   "2m",
			function: "sum",
			aggr:     "sum_over_time",
			minutes:  10,
			tables: &Tables{
				Tables: []*Table{
					{
						Name:        "series0",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"1"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(2)},
							{2 * time.Minute.Milliseconds(), float64(2)},
							{4 * time.Minute.Milliseconds(), float64(2)},
							{6 * time.Minute.Milliseconds(), float64(2)},
							{8 * time.Minute.Milliseconds(), float64(2)},
						},
					},
					{
						Name:        "series1",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"2"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(2)},
							{2 * time.Minute.Milliseconds(), float64(2)},
							{4 * time.Minute.Milliseconds(), float64(2)},
							{6 * time.Minute.Milliseconds(), float64(2)},
							{8 * time.Minute.Milliseconds(), float64(2)},
						},
					},
				},
			},
		},
		"count_5m": {
			iscount:  true,
			window:   "5m",
			function: "sum",
			aggr:     "sum_over_time",
			minutes:  20,
			tables: &Tables{
				Tables: []*Table{
					{
						Name:        "series0",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"1"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(5)},
							{5 * time.Minute.Milliseconds(), float64(5)},
							{10 * time.Minute.Milliseconds(), float64(5)},
							{15 * time.Minute.Milliseconds(), float64(5)},
						},
					},
					{
						Name:        "series1",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"2"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(5)},
							{5 * time.Minute.Milliseconds(), float64(5)},
							{10 * time.Minute.Milliseconds(), float64(5)},
							{15 * time.Minute.Milliseconds(), float64(5)},
						},
					},
				},
			},
		},
		"count_10m": {
			iscount:  true,
			window:   "10m",
			function: "sum",
			aggr:     "sum_over_time",
			minutes:  20,
			tables: &Tables{
				Tables: []*Table{
					{
						Name:        "series0",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"1"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(10)},
							{10 * time.Minute.Milliseconds(), float64(10)},
						},
					},
					{
						Name:        "series1",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"2"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(10)},
							{10 * time.Minute.Milliseconds(), float64(10)},
						},
					},
				},
			},
		},
		"avg_2m": {
			window:   "2m",
			function: "mean",
			aggr:     "avg_over_time",
			minutes:  10,
			tables: &Tables{
				Tables: []*Table{
					{
						Name:        "series0",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"1"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(10)},
							{2 * time.Minute.Milliseconds(), float64(10)},
							{4 * time.Minute.Milliseconds(), float64(10)},
							{6 * time.Minute.Milliseconds(), float64(10)},
							{8 * time.Minute.Milliseconds(), float64(10)},
						},
					},
					{
						Name:        "series1",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"2"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(10.5)}, // 10+11
							{2 * time.Minute.Milliseconds(), float64(12.5)}, // 12+13
							{4 * time.Minute.Milliseconds(), float64(14.5)}, // 14+15
							{6 * time.Minute.Milliseconds(), float64(16.5)}, // 16+17
							{8 * time.Minute.Milliseconds(), float64(18.5)}, // 18+19
						},
					},
				},
			},
		},
		"avg_10m": {
			window:   "10m",
			function: "mean",
			aggr:     "avg_over_time",
			minutes:  40,
			tables: &Tables{
				Tables: []*Table{
					{
						Name:        "series0",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"1"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(10)}, // (10+10+10+10+10+10+10+10+10+10)/10
							{10 * time.Minute.Milliseconds(), float64(10)},
							{20 * time.Minute.Milliseconds(), float64(10)},
							{30 * time.Minute.Milliseconds(), float64(10)},
						},
					},
					{
						Name:        "series1",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"2"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(14.5)}, // (10+11+12+13+14+15+16+17+18+19)/10
							{10 * time.Minute.Milliseconds(), float64(24.5)},
							{20 * time.Minute.Milliseconds(), float64(34.5)},
							{30 * time.Minute.Milliseconds(), float64(44.5)},
						},
					},
				},
			},
		},
		"max_1h": {
			window:   "1h",
			function: "max",
			aggr:     "max_over_time",
			minutes:  60,
			tables: &Tables{
				Tables: []*Table{
					{
						Name:        "series0",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"1"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(10)},
						},
					},
					{
						Name:        "series1",
						Headers:     []string{"_time", "_value"},
						Types:       []string{"float", "float"},
						GroupKeys:   []string{"tag"},
						GroupValues: []string{"2"},
						Data: [][]interface{}{
							{0 * time.Minute.Milliseconds(), float64(69)},
						},
					},
				},
			},
		},
	}

	NewEngine(&Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})

	var err error
	var interval time.Duration
	var dTmp model.Duration
	for name, testCase := range testCases {
		var tables *Tables
		t.Run(name, func(t *testing.T) {
			queryInfo := QueryInfo{
				DB:          "2_bkapm_metric_apm_test_have_data",
				Measurement: "__default__",
				AggregateMethodList: AggrMethods{
					{
						Name: testCase.function,
						Dimensions: []string{
							"tag",
						},
					},
				},
				IsCount: testCase.iscount,
			}

			ctx := context.Background()
			ctx, err = QueryInfoIntoContext(ctx, "t1", metric, &queryInfo)
			assert.Nil(t, err)

			dTmp, err = model.ParseDuration(testCase.window)
			interval = time.Duration(dTmp)
			offset := interval - time.Second

			function := testCase.function
			if function == "mean" {
				function = "avg"
			}
			sql := fmt.Sprintf("%s(%s(t1[%s] offset -%s)) by (tag)", function, testCase.aggr, testCase.window, offset)
			tables, err = QueryRange(ctx, sql, time.Unix(0, 0), time.Unix(testCase.minutes*60, 0), interval)
			assert.Nil(t, err)
			assert.Equal(t, testCase.tables, tables, name)
		})
	}

}

// TestDownsampledInfluxQL
func TestDownsampledInfluxQL(t *testing.T) {
	log.InitTestLogger()

	ctrl, stubs := FakeData(t)
	defer ctrl.Finish()
	defer stubs.Reset()

	var totalSQL string
	var err error
	// mock掉sql处理函数，以确认生成sql的内容
	stubs.Stub(&MakeInfluxdbQuerys, func(ctx context.Context, hints *storage.SelectHints, matchers ...*labels.Matcher) ([]influxdb.SQLInfo, error) {
		var sqlInfos []influxdb.SQLInfo
		sqlInfos, err = makeInfluxdbQuery(ctx, hints, matchers...)
		if len(sqlInfos) > 0 {
			totalSQL = sqlInfos[0].SQL
		}
		return sqlInfos, err
	})

	lastModifyTime := "2022-04-17 19:30:00+0800"
	database := "2_bkapm_metric_apm_test_have_data"
	measurement := "bk_apm_duration"
	prefixPath := fmt.Sprintf("%s/downsampled/%s", consul.MetadataPath, database)
	data := api.KVPairs{
		{
			Key:   fmt.Sprintf("%s/cq", prefixPath),
			Value: []byte(fmt.Sprintf(`{"tag_name":"","tag_value":[""],"enable":true,"last_modify_time":"%s"}`, lastModifyTime)),
		},
		{
			Key:   fmt.Sprintf("%s/rp/5m", prefixPath),
			Value: []byte(fmt.Sprintf(`{"duration":"720h","resolution":300,"last_modify_time":"%s"}`, lastModifyTime)),
		},
		{
			Key:   fmt.Sprintf("%s/rp/1h", prefixPath),
			Value: []byte(fmt.Sprintf(`{"duration":"720h","resolution":3600,"last_modify_time":"%s"}`, lastModifyTime)),
		},
		{
			Key:   fmt.Sprintf("%s/rp/12h", prefixPath),
			Value: []byte(fmt.Sprintf(`{"duration":"720h","resolution":43200,"last_modify_time":"%s"}`, lastModifyTime)),
		},
		{
			Key:   fmt.Sprintf("%s/cq/__all__/value/max/5m", prefixPath),
			Value: []byte(fmt.Sprintf(`{"source_rp":"autogen","last_modify_time":"%s"}`, lastModifyTime)),
		},
		{
			Key:   fmt.Sprintf("%s/cq/__all__/value/max/1h", prefixPath),
			Value: []byte(fmt.Sprintf(`{"source_rp":"5m","last_modify_time":"%s"}`, lastModifyTime)),
		},
		{
			Key:   fmt.Sprintf("%s/cq/__all__/value/count/1h", prefixPath),
			Value: []byte(fmt.Sprintf(`{"source_rp":"autogen","last_modify_time":"%s"}`, lastModifyTime)),
		},
		{
			Key:   fmt.Sprintf("%s/cq/__all__/value/mean/5m", prefixPath),
			Value: []byte(fmt.Sprintf(`{"source_rp":"autogen","last_modify_time":"%s"}`, lastModifyTime)),
		},
		{
			Key:   fmt.Sprintf("%s/cq/__all__/value/mean/1h", prefixPath),
			Value: []byte(fmt.Sprintf(`{"source_rp":"5m","last_modify_time":"%s"}`, lastModifyTime)),
		},
		{
			Key:   fmt.Sprintf("%s/cq/__all__/value/sum/5m", prefixPath),
			Value: []byte(fmt.Sprintf(`{"source_rp":"autogen","last_modify_time":"%s"}`, lastModifyTime)),
		},
		{
			Key:   fmt.Sprintf("%s/cq/__all__/value/sum/1h", prefixPath),
			Value: []byte(fmt.Sprintf(`{"source_rp":"5m","last_modify_time":"%s"}`, time.Now().Format(LastModifyTimeFormat))),
		},
		// 增加干扰数据，resolution=0，预期会将此不正常的数据过滤掉。
		{
			Key:   fmt.Sprintf("%s/rp/0", prefixPath),
			Value: []byte(fmt.Sprintf(`{"duration":"720h","resolution":0,"last_modify_time":"%s"}`, lastModifyTime)),
		},
		{
			Key:   fmt.Sprintf("%s/cq/__all__/value/max/0", prefixPath),
			Value: []byte(fmt.Sprintf(`{"source_rp":"autogen","last_modify_time":"%s"}`, lastModifyTime)),
		},
	}
	stubs = gostub.Stub(&consul.GetDataWithPrefix, func(prefix string) (api.KVPairs, error) {
		return data, nil
	})

	consul.LoadDownsampledInfo()

	testCases := map[string]struct {
		window string
		aggr   string
		sql    string
	}{
		"max_30s": {
			window: "30s",
			aggr:   "max",
			sql:    "select max(\"value\") as _value,time as _time from \"bk_apm_duration\" where time >= 1621496574000000000 and time < 1621496963999000000 group by time(30s)",
		},
		"max_10m": {
			window: "10m",
			aggr:   "max",
			sql:    "select max(\"max_value\") as _value,time as _time from \"5m\".\"bk_apm_duration\" where time >= 1621496004000000000 and time < 1621496963999000000 group by time(10m0s)",
		},
		"max_1m": {
			window: "1m",
			aggr:   "max",
			sql:    "select max(\"value\") as _value,time as _time from \"bk_apm_duration\" where time >= 1621496544000000000 and time < 1621496963999000000 group by time(1m0s)",
		},
		"max_2h": {
			window: "2h",
			aggr:   "max",
			sql:    "select max(\"max_value\") as _value,time as _time from \"1h\".\"bk_apm_duration\" where time >= 1621489404000000000 and time < 1621496963999000000 group by time(2h0m0s)",
		},
		"count_2m": {
			window: "2m",
			aggr:   "count",
			sql:    "select count(\"value\") as _value,time as _time from \"bk_apm_duration\" where time >= 1621496484000000000 and time < 1621496963999000000 group by time(2m0s)",
		},
		"count_2h": {
			window: "2h",
			aggr:   "count",
			sql:    "select sum(\"count_value\") as _value,time as _time from \"1h\".\"bk_apm_duration\" where time >= 1621489404000000000 and time < 1621496963999000000 group by time(2h0m0s)",
		},
		"avg_8m": {
			window: "8m",
			aggr:   "avg",
			sql:    "select mean(\"value\") as _value,time as _time from \"bk_apm_duration\" where time >= 1621496124000000000 and time < 1621496963999000000 group by time(8m0s)",
		},
		"avg_10m": {
			window: "10m",
			aggr:   "avg",
			sql:    "select mean(\"mean_value\") as _value,time as _time from \"5m\".\"bk_apm_duration\" where time >= 1621496004000000000 and time < 1621496963999000000 group by time(10m0s)",
		},
		"avg_2h": {
			window: "2h",
			aggr:   "avg",
			sql:    "select mean(\"mean_value\") as _value,time as _time from \"1h\".\"bk_apm_duration\" where time >= 1621489404000000000 and time < 1621496963999000000 group by time(2h0m0s)",
		},
		// 1h3m 虽然 > 1h，但是不能被1h整除，所以需要取前一个 5m
		"avg_1h30m": {
			window: "1h30m",
			aggr:   "avg",
			sql:    "select mean(\"mean_value\") as _value,time as _time from \"5m\".\"bk_apm_duration\" where time >= 1621491204000000000 and time < 1621496963999000000 group by time(1h30m0s)",
		},
		"min_1h": {
			window: "1h",
			aggr:   "min",
			sql:    "select min(\"value\") as _value,time as _time from \"bk_apm_duration\" where time >= 1621493004000000000 and time < 1621496963999000000 group by time(1h0m0s)",
		},
		"rate_1m": {
			window: "1m",
			aggr:   "rate",
			sql:    `select "value" as _value,time as _time,*::tag from "bk_apm_duration" where time >= 1621496544000000000 and time < 1621496963999000000`,
		},
		"rate_30s": {
			window: "30s",
			aggr:   "rate",
			sql:    `select "value" as _value,time as _time,*::tag from "bk_apm_duration" where time >= 1621496574000000000 and time < 1621496963999000000`,
		},
		"rate_2h": {
			window: "2h",
			aggr:   "rate",
			sql:    `select "value" as _value,time as _time,*::tag from "bk_apm_duration" where time >= 1621489404000000000 and time < 1621496963999000000`,
		},
		// 1h 精度的数据修改时间为现在，所以往前推一个位置
		"sum_2h": {
			window: "2h",
			aggr:   "sum",
			sql:    "select sum(\"sum_value\") as _value,time as _time from \"5m\".\"bk_apm_duration\" where time >= 1621489404000000000 and time < 1621496963999000000 group by time(2h0m0s)",
		},
	}

	NewEngine(&Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           5000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			var (
				funcAggr             = ""
				timeAggr             = testCase.aggr
				methods  AggrMethods = nil
				isCount  bool        = false
				sql      string
			)
			for _, f := range []string{"min", "max", "count", "sum", "avg"} {
				if f == testCase.aggr {
					if f == "count" {
						funcAggr = SUM
						isCount = true
					} else {
						funcAggr = f
					}
					timeAggr = fmt.Sprintf("%s_over_time", funcAggr)
					break
				}
			}

			if funcAggr != "" {
				methods = AggrMethods{
					{
						Name: funcAggr,
					},
				}
			}

			queryInfo := QueryInfo{
				DataIDList:          []consul.DataID{180001},
				AggregateMethodList: methods,
				IsCount:             isCount,
			}

			log.Debugf(context.TODO(), "queryInfo: %+v", queryInfo)

			ctx := context.Background()
			ctx, err = QueryInfoIntoContext(ctx, "t1", measurement, &queryInfo)
			assert.Nil(t, err)

			sql = fmt.Sprintf("%s(t1[%s])", timeAggr, testCase.window)
			if funcAggr != "" {
				sql = fmt.Sprintf("%s(%s)", funcAggr, sql)
			}

			_, err = QueryRange(ctx, sql, time.Unix(1621496604, 0), time.Unix(1621496964, 0), time.Minute-time.Second)
			assert.Nil(t, err)
			assert.Equal(t, testCase.sql, totalSQL)
		})
	}
}

// TestIndependentRPInfluxQL
func TestIndependentRPInfluxQL(t *testing.T) {
	log.InitTestLogger()

	ctrl, stubs := FakeData(t)
	defer ctrl.Finish()
	defer stubs.Reset()

	var totalSQL string
	var err error
	// mock掉sql处理函数，以确认生成sql的内容
	stubs.Stub(&MakeInfluxdbQuerys, func(ctx context.Context, hints *storage.SelectHints, matchers ...*labels.Matcher) ([]influxdb.SQLInfo, error) {
		var sqlInfos []influxdb.SQLInfo
		sqlInfos, err = makeInfluxdbQuery(ctx, hints, matchers...)
		if len(sqlInfos) > 0 {
			totalSQL = sqlInfos[0].SQL
		}
		return sqlInfos, err
	})

	data := api.KVPairs{
		// 添加独立rp
		{
			Key:   fmt.Sprintf("%s/downsampled/%s/rp/autogen", consul.MetadataPath, "2_bkapm_metric_apm_test_have_data"),
			Value: []byte(fmt.Sprintf(`{"duration":"720h","resolution":1,"measurement":"__default__"}`)),
		},
	}
	stubs = gostub.Stub(&consul.GetDataWithPrefix, func(prefix string) (api.KVPairs, error) {
		return data, nil
	})

	consul.LoadDownsampledInfo()

	NewEngine(&Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           5000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})

	testCases := map[string]struct {
		window     string
		aggr       string
		metricName string
		queryInfo  QueryInfo
		sql        string
	}{
		// 测试单库单表,配置全库的独立rp
		"__default__ rp": {
			window:     "1m",
			aggr:       "rate",
			metricName: "bk_apm_duration",
			queryInfo: QueryInfo{
				DataIDList: []consul.DataID{180001},
				OffsetInfo: OffSetInfo{
					Limit: 20000,
				},
			},
			sql: "select \"value\" as _value,time as _time,*::tag from \"autogen\".\"bk_apm_duration\" where time >= 1621496544000000000 and time < 1621496963999000000",
		},
		// 未命中rp，则使用influxdb默认rp
		"auto rp": {
			window:     "1m",
			aggr:       "rate",
			metricName: "value",
			queryInfo: QueryInfo{
				DB:          "test_db",
				Measurement: "test_measurement",
			},
			sql: "select \"value\" as _value,time as _time,*::tag from \"test_measurement\" where time >= 1621496544000000000 and time < 1621496963999000000",
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			sql := fmt.Sprintf("%s(t1[%s])", testCase.aggr, testCase.window)
			ctx := context.Background()
			ctx, err = QueryInfoIntoContext(ctx, "t1", testCase.metricName, &testCase.queryInfo)
			assert.Nil(t, err)

			_, err = QueryRange(ctx, sql, time.Unix(1621496604, 0), time.Unix(1621496964, 0), time.Minute-time.Second)
			assert.Nil(t, err)
			assert.Equal(t, testCase.sql, totalSQL)
		})
	}
}
