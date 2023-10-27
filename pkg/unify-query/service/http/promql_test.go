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
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/mocktest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// fakeDataTable
func fakeDataTable(t *testing.T, metric string, bizID int) (*gomock.Controller, *gostub.Stubs) {
	stubs := gostub.New()
	fakeTable := map[consul.DataID]*consul.TableID{
		consul.DataID(1): {
			DB: "db1", Measurement: metric, IsSplitMeasurement: true,
		},
		consul.DataID(2): {
			DB: "db2", Measurement: metric, IsSplitMeasurement: true,
		},
	}
	tsDBRouter := influxdb.GetTsDBRouter()
	bizRouter := influxdb.GetBizRouter()
	metricRouter := influxdb.GetMetricRouter()
	for dataID, tableID := range fakeTable {
		tsDBRouter.AddTables(dataID, []*consul.TableID{tableID})
		bizRouter.AddRouter(int(bizID), dataID)
		metricRouter.AddRouter(metric, dataID)
	}
	ctrl := gomock.NewController(t)
	mockClient := mocktest.NewMockClient(ctrl)

	// select sum("value") as _value,time as _time from "metric" where bk_biz_id='2' and time >= 1654646340000000000 and time < 1654648199999000000 group by "key1",time(1m0s) limit 20000
	mockResponse := map[string]*decoder.Response{
		`db1`: {
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name:    metric,
							Columns: []string{"_time", "_value"},
							Tags: map[string]string{
								"key1": "value1",
							},
							Values: [][]interface{}{
								{"2022-06-08T00:00:00Z", 1},
								{"2022-06-08T00:01:00Z", 1},
								{"2022-06-08T00:02:00Z", 1},
								{"2022-06-08T00:03:00Z", 1},
								{"2022-06-08T00:04:00Z", 1},
								{"2022-06-08T00:05:00Z", 1},
								{"2022-06-08T00:06:00Z", 1},
								{"2022-06-08T00:07:00Z", 1},
								{"2022-06-08T00:08:00Z", 1},
								{"2022-06-08T00:09:00Z", 1},
							},
						},
						{
							Name:    metric,
							Columns: []string{"_time", "_value"},
							Tags: map[string]string{
								"key1": "value2",
							},
							Values: [][]interface{}{
								{"2022-06-08T00:00:00Z", 2},
								{"2022-06-08T00:01:00Z", 2},
								{"2022-06-08T00:02:00Z", 2},
								{"2022-06-08T00:03:00Z", 2},
								{"2022-06-08T00:04:00Z", 2},
								{"2022-06-08T00:05:00Z", 2},
							},
						},
					},
				},
			},
		},
		`db2`: {
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name:    metric,
							Columns: []string{"_time", "_value"},
							Tags: map[string]string{
								"key1": "value1",
							},
							Values: [][]interface{}{
								{"2022-06-08T00:00:00Z", 3},
								{"2022-06-08T00:01:00Z", 3},
								{"2022-06-08T00:02:00Z", 3},
								{"2022-06-08T00:03:00Z", 3},
								{"2022-06-08T00:04:00Z", 3},
							},
						},
						{
							Name:    metric,
							Columns: []string{"_time", "_value"},
							Tags: map[string]string{
								"key1": "value2",
							},
							Values: [][]interface{}{
								{"2022-06-08T00:00:00Z", 4},
								{"2022-06-08T00:01:00Z", 4},
								{"2022-06-08T00:02:00Z", 4},
							},
						},
					},
				},
			},
		},
	}

	mockClient.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, db, sql, precision, contentType string, chunked bool) (*decoder.Response, error) {
			if resp, ok := mockResponse[db]; ok {
				return resp, nil
			}
			return nil, fmt.Errorf("promql_test.go db: %s is no data", db)
		}).AnyTimes()

	_ = influxdb.InitGlobalInstance(context.Background(), &influxdb.Params{
		Timeout: 30 * time.Second,
	}, mockClient)

	promql.NewEngine(&promql.Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})

	return ctrl, stubs
}

// TestHandleRawPromQuery
func TestHandleRawPromQuery(t *testing.T) {
	log.InitTestLogger()

	metric := "metric"
	step := "1m"
	start := "1654646400"
	end := "1654648200"
	bizID := 2

	fakeDataTable(t, metric, bizID)

	testCases := map[string]struct {
		query    *structured.CombinedQueryParams
		expected *PromData
	}{
		"metric_multiple_table_id": {
			query: &structured.CombinedQueryParams{
				QueryList: []*structured.QueryParams{
					{
						AlignInfluxdbResult: true,
						FieldName:           structured.FieldName(metric),
						ReferenceName:       "a",
						Driver:              "influxdb",
						TimeField:           "time",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "bk_biz_id",
									Value:         []string{fmt.Sprintf("%d", bizID)},
									Operator:      "contains",
								},
							},
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method:     "sum",
								Dimensions: []string{"key1"},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function: "sum_over_time",
							Window:   structured.Window(step),
						},
					},
				},
				MetricMerge: "a",
				Start:       start,
				End:         end,
				Step:        step,
			},
			expected: &PromData{
				dimensions: map[string]bool{},
				Tables: []*TablesItem{
					{
						Name:    "_result0",
						Columns: []string{"_time", "_value"},
						Types:   []string{"float", "float"},
						GroupKeys: []string{
							"key1",
						},
						GroupValues: []string{
							"value1",
						},
						Values: [][]interface{}{
							{int64(1654646400000), float64(4)},
							{int64(1654646460000), float64(4)},
							{int64(1654646520000), float64(4)},
							{int64(1654646580000), float64(4)},
							{int64(1654646640000), float64(4)},
							{int64(1654646700000), float64(1)},
							{int64(1654646760000), float64(1)},
							{int64(1654646820000), float64(1)},
							{int64(1654646880000), float64(1)},
							{int64(1654646940000), float64(1)},
						},
					},
					{
						Name:    "_result1",
						Columns: []string{"_time", "_value"},
						Types:   []string{"float", "float"},
						GroupKeys: []string{
							"key1",
						},
						GroupValues: []string{
							"value2",
						},
						Values: [][]interface{}{
							{int64(1654646400000), float64(6)},
							{int64(1654646460000), float64(6)},
							{int64(1654646520000), float64(6)},
							{int64(1654646580000), float64(2)},
							{int64(1654646640000), float64(2)},
							{int64(1654646700000), float64(2)},
						},
					},
				},
			},
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			options, err := structured.GenerateOptions(c.query, false, []string{fmt.Sprintf("%d", bizID)}, "")
			assert.Nil(t, err)
			ctx, stmt, err := structured.QueryProm(ctx, c.query, options)
			assert.Nil(t, err)
			actual, err := HandleRawPromQuery(ctx, stmt, c.query)
			assert.Nil(t, err)
			assert.Equal(t, c.expected, actual)
		})
	}

}
