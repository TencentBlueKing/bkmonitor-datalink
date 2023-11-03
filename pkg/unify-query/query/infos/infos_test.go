// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package infos

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

// sqlExpected
type sqlExpected struct {
	db  string
	sql string
}

func mockSpace() {
	fmt.Println("mock space")
	fmt.Println("------------------------------------------------------------------------------------------------")
	ctx := context.Background()
	spaceId := "bkcc__2"
	path := "infos_test.db"
	bucketName := "infos_test"
	mock.SetSpaceTsDbMockData(
		ctx, path, bucketName,
		ir.SpaceInfo{
			spaceId: ir.Space{
				"2_bkmonitor_time_series_1573076.__default__": &ir.SpaceResultTable{
					TableId: "2_bkmonitor_time_series_1573076.__default__",
					Filters: []map[string]string{},
				},
				"2_bkmonitor_time_series_1572904.__default__": &ir.SpaceResultTable{
					TableId: "2_bkmonitor_time_series_1572904.__default__",
					Filters: []map[string]string{},
				},
				"system.cpu_summary": &ir.SpaceResultTable{
					TableId: "system.cpu_summary",
					Filters: []map[string]string{},
				},
			},
		},
		ir.ResultTableDetailInfo{
			"2_bkmonitor_time_series_1573076.__default__": &ir.ResultTableDetail{
				TableId:         "2_bkmonitor_time_series_1573076.__default__",
				Fields:          []string{"metric", "metric2"},
				MeasurementType: redis.BkExporter,
				StorageId:       0,
				DB:              "2_bkmonitor_time_series_1573076",
				Measurement:     "__default__",
			},
			"2_bkmonitor_time_series_1572904.__default__": &ir.ResultTableDetail{
				TableId:         "2_bkmonitor_time_series_1572904.__default__",
				Fields:          []string{"metric", "metric2"},
				MeasurementType: redis.BkSplitMeasurement,
				StorageId:       0,
				DB:              "2_bkmonitor_time_series_1572904",
				Measurement:     "__default__",
			},
			"system.cpu_summary": &ir.ResultTableDetail{
				TableId:         "system.cpu_summary",
				Fields:          []string{"metric", "metric2"},
				MeasurementType: redis.BKTraditionalMeasurement,
				StorageId:       0,
				DB:              "system",
				Measurement:     "cpu_summary",
			},
		},
		nil, nil)
}

// fakeData
func fakeData() {
	fmt.Println("fake data")
	fmt.Println("------------------------------------------------------------------------------------------------")
	tableRouter := influxdb.GetTableRouter()
	for _, tableID := range []*consul.TableID{
		{
			DB:                 "system",
			Measurement:        "cpu_detail",
			IsSplitMeasurement: false,
		},
		{
			DB:                 "2_bkmonitor_time_series_1582625",
			Measurement:        "__default__",
			IsSplitMeasurement: true,
		},
	} {
		tableRouter.AddTableID(tableID)
	}

	data := map[*consul.TableID]struct {
		dataID    consul.DataID
		tableInfo *consul.InfluxdbTableInfo
		metrics   []string
		bizID     int
	}{
		{
			DB:                 "system",
			Measurement:        "cpu_detail",
			IsSplitMeasurement: false,
		}: {
			dataID: 1001,
			bizID:  0,
		},
		{
			DB:                 "system",
			Measurement:        "cpu_summary",
			IsSplitMeasurement: false,
		}: {
			dataID: 1001,
			bizID:  0,
		},
		{
			DB:                 "2_bkmonitor_time_series_1572904",
			Measurement:        "__default__",
			IsSplitMeasurement: true,
		}: {
			dataID: 1572904,
			tableInfo: &consul.InfluxdbTableInfo{
				PivotTable: false,
			},
			metrics: []string{
				"cmdb_collector_middleware_analyze_duration_count",
				"cmdb_apimachinary_requests_duration_millisecond_count",
			},
			bizID: 2,
		},
		{
			DB:          "2_bkmonitor_time_series_1573076",
			Measurement: "__default__",
		}: {
			tableInfo: &consul.InfluxdbTableInfo{
				PivotTable: true,
			},
			bizID: 2,
		},
	}

	influxdbTableInfo := make(map[string]*consul.InfluxdbTableInfo, len(data))
	for t, d := range data {
		// tableInfo
		tableInfo := &consul.InfluxdbTableInfo{}
		if d.tableInfo != nil {
			tableInfo = d.tableInfo
		}
		influxdbTableInfo[t.String()] = tableInfo

		// tsDBRouter
		influxdb.GetTsDBRouter().AddTables(d.dataID, []*consul.TableID{t})

		// metricRouter
		if len(d.metrics) > 0 {
			for _, m := range d.metrics {
				influxdb.GetMetricRouter().AddRouter(m, d.dataID)
			}
		}

		// bizRouter
		influxdb.GetBizRouter().AddRouter(d.bizID, d.dataID)

		// tableRouter
		influxdb.GetTableRouter().AddTableID(t)
	}

	influxdb.SetTablesInfo(influxdbTableInfo)

	fmt.Println("------------------------------------------------------------------------------------------------")
	d := influxdb.Print()
	fmt.Println(d)
}

// TestGetSQL
func TestGetSQL(t *testing.T) {
	log.InitTestLogger()

	fakeData()
	mockSpace()

	testCases := map[string]struct {
		infoType InfoType
		params   *Params
		expected []sqlExpected
		spaceUid string
	}{
		"series.default": {
			infoType: Series,
			params: &Params{
				TableID: "system.cpu_detail",
				Limit:   5,
			},
			expected: []sqlExpected{
				{
					db:  `system`,
					sql: `show series from cpu_detail where time >= %d and time < %d limit 5`,
				},
			},
		},
		"series.default.spaceUid": {
			infoType: Series,
			spaceUid: "bkcc__2",
			params: &Params{
				TableID: "system.cpu_summary",
				Limit:   5,
			},
			expected: []sqlExpected{
				{
					db:  `system`,
					sql: `show series from cpu_summary where time >= %d and time < %d limit 5`,
				},
			},
		},
		// TableID 不能为空
		"series.default.metric": {
			infoType: Series,
			params: &Params{
				Metric: "usage",
				Limit:  5,
			},
			expected: []sqlExpected{},
		},
		"series.pivot_table": {
			infoType: Series,
			params: &Params{
				TableID: "2_bkmonitor_time_series_1573076.__default__",
				Limit:   10,
			},
			expected: []sqlExpected{
				{
					db:  `2_bkmonitor_time_series_1573076`,
					sql: `show series from __default__ where time >= %d and time < %d limit 10`,
				},
			},
		},
		"series.pivot_table.spaceUid": {
			spaceUid: "bkcc__2",
			infoType: Series,
			params: &Params{
				TableID: "2_bkmonitor_time_series_1573076.__default__",
				Limit:   10,
			},
			expected: []sqlExpected{
				{
					db:  `2_bkmonitor_time_series_1573076`,
					sql: `show series from __default__ where time >= %d and time < %d limit 10`,
				},
			},
		},
		"series.pivot_table.metric": {
			infoType: Series,
			params: &Params{
				TableID: "2_bkmonitor_time_series_1573076.__default__",
				Metric:  "metric",
				Limit:   10,
			},
			expected: []sqlExpected{
				{
					db:  `2_bkmonitor_time_series_1573076`,
					sql: `show series from __default__ where time >= %d and time < %d and metric_name = 'metric' limit 10`,
				},
			},
		},
		"series.pivot_table.metric.spaceUid": {
			spaceUid: "bkcc__2",
			infoType: Series,
			params: &Params{
				TableID: "2_bkmonitor_time_series_1573076.__default__",
				Metric:  "metric",
				Limit:   10,
			},
			expected: []sqlExpected{
				{
					db:  `2_bkmonitor_time_series_1573076`,
					sql: `show series from __default__ where time >= %d and time < %d and metric_name = 'metric' limit 10`,
				},
			},
		},
		"series.is_split_measurement": {
			infoType: Series,
			params: &Params{
				TableID: "2_bkmonitor_time_series_1572904.__default__",
				Keys: []string{
					"pod_id",
				},
				Conditions: structured.Conditions{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"2"},
						},
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"4"},
						},
					},
					ConditionList: []string{"or"},
				},
				Limit: 10,
			},
			expected: []sqlExpected{
				{
					db:  `2_bkmonitor_time_series_1572904`,
					sql: `show series where (bk_biz_id='2' or bk_biz_id='4') and time >= %d and time < %d limit 10`,
				},
			},
		},
		"series.is_split_measurement.metric": {
			infoType: Series,
			params: &Params{
				TableID: "",
				Metric:  "cmdb_collector_middleware_analyze_duration_count",
				Keys: []string{
					"pod_id",
				},
				Conditions: structured.Conditions{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"2"},
						},
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"4"},
						},
					},
					ConditionList: []string{"or"},
				},
				Limit: 10,
			},
			expected: []sqlExpected{
				{
					db:  `2_bkmonitor_time_series_1572904`,
					sql: `show series from cmdb_collector_middleware_analyze_duration_count where (bk_biz_id='2' or bk_biz_id='4') and time >= %d and time < %d limit 10`,
				},
			},
		},
		"tag_keys.default": {
			infoType: TagKeys,
			params: &Params{
				TableID: "system.cpu_detail",
				Limit:   5,
			},
			expected: []sqlExpected{
				{
					db:  `system`,
					sql: `show tag keys from cpu_detail where time >= %d and time < %d limit 5`,
				},
			},
		},
		"tag_keys.is_split_measurement": {
			infoType: TagKeys,
			params: &Params{
				TableID: "",
				Metric:  "cmdb_collector_middleware_analyze_duration_count",
				Conditions: structured.Conditions{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"2"},
						},
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"4"},
						},
					},
					ConditionList: []string{"or"},
				},
				Limit: 10,
			},
			expected: []sqlExpected{
				{
					db:  `2_bkmonitor_time_series_1572904`,
					sql: `show tag keys from cmdb_collector_middleware_analyze_duration_count where (bk_biz_id='2' or bk_biz_id='4') and time >= %d and time < %d limit 10`,
				},
			},
		},
		"tag_value.default": {
			infoType: TagValues,
			params: &Params{
				TableID: "system.cpu_summary",
				Limit:   10,
				Conditions: structured.Conditions{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"2"},
						},
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"4"},
						},
					},
					ConditionList: []string{"or"},
				},
				Keys: []string{"bk_biz_id", "ip"},
			},
			expected: []sqlExpected{
				{
					db:  "system",
					sql: `show tag values from cpu_summary with key in ("bk_biz_id","ip") where (bk_biz_id='2' or bk_biz_id='4') and time >= %d and time < %d limit 10`,
				},
			},
		},
		"tag_value.default.space": {
			spaceUid: "bkcc__2",
			infoType: TagValues,
			params: &Params{
				TableID: "system.cpu_summary",
				Limit:   10,
				Conditions: structured.Conditions{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"2"},
						},
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"4"},
						},
					},
					ConditionList: []string{"or"},
				},
				Keys: []string{"bk_biz_id", "ip"},
			},
			expected: []sqlExpected{
				{
					db:  "system",
					sql: `show tag values from cpu_summary with key in ("bk_biz_id","ip") where (bk_biz_id='2' or bk_biz_id='4') and time >= %d and time < %d limit 10`,
				},
			},
		},
		"tag_values.pivot_table": {
			infoType: TagValues,
			params: &Params{
				TableID: "2_bkmonitor_time_series_1573076.__default__",
				Keys:    []string{"bk_biz_id", "ip"},
				Limit:   10,
			},
			expected: []sqlExpected{
				{
					db:  `2_bkmonitor_time_series_1573076`,
					sql: `show tag values from __default__ with key in ("bk_biz_id","ip") where time >= %d and time < %d limit 10`,
				},
			},
		},
		"tag_values.pivot_table.space": {
			spaceUid: "bkcc__2",
			infoType: TagValues,
			params: &Params{
				TableID: "2_bkmonitor_time_series_1573076.__default__",
				Keys:    []string{"bk_biz_id", "ip"},
				Limit:   10,
			},
			expected: []sqlExpected{
				{
					db:  `2_bkmonitor_time_series_1573076`,
					sql: `show tag values from __default__ with key in ("bk_biz_id","ip") where time >= %d and time < %d limit 10`,
				},
			},
		},
		"tag_values.is_split_measurement.table_id": {
			infoType: TagValues,
			params: &Params{
				TableID: "2_bkmonitor_time_series_1572904.__default__",
				Conditions: structured.Conditions{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"2"},
						},
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"4"},
						},
					},
					ConditionList: []string{"or"},
				},
				Keys:  []string{"bk_biz_id", "bcs_cluster_id", "bk_container"},
				Limit: 10,
			},
			expected: []sqlExpected{
				{
					db:  `2_bkmonitor_time_series_1572904`,
					sql: `show tag values with key in ("bk_biz_id","bcs_cluster_id","bk_container") where (bk_biz_id='2' or bk_biz_id='4') and time >= %d and time < %d limit 10`,
				},
			},
		},
		"tag_values.is_split_measurement.table_id.spaceUid": {
			spaceUid: "bkcc__2",
			infoType: TagValues,
			params: &Params{
				TableID: "2_bkmonitor_time_series_1572904.__default__",
				Conditions: structured.Conditions{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"2"},
						},
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"4"},
						},
					},
					ConditionList: []string{"or"},
				},
				Keys:  []string{"bk_biz_id", "bcs_cluster_id", "bk_container"},
				Limit: 10,
			},
			expected: []sqlExpected{
				{
					db:  `2_bkmonitor_time_series_1572904`,
					sql: `show tag values with key in ("bk_biz_id","bcs_cluster_id","bk_container") where (bk_biz_id='2' or bk_biz_id='4') and time >= %d and time < %d limit 10`,
				},
			},
		},
		"tag_values.is_split_measurement": {
			infoType: TagValues,
			params: &Params{
				TableID: "",
				Metric:  "cmdb_collector_middleware_analyze_duration_count",
				Conditions: structured.Conditions{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"2"},
						},
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"4"},
						},
					},
					ConditionList: []string{"or"},
				},
				Keys:  []string{"bk_biz_id", "bcs_cluster_id", "bk_container"},
				Limit: 10,
			},
			expected: []sqlExpected{
				{
					db:  `2_bkmonitor_time_series_1572904`,
					sql: `show tag values from cmdb_collector_middleware_analyze_duration_count with key in ("bk_biz_id","bcs_cluster_id","bk_container") where (bk_biz_id='2' or bk_biz_id='4') and time >= %d and time < %d limit 10`,
				},
			},
		},
		"field_keys.is_split_measurement": {
			infoType: FieldKeys,
			params: &Params{
				TableID: "",
				Metric:  "cmdb_collector_middleware_analyze_duration_count",
				Conditions: structured.Conditions{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"2"},
						},
					},
					ConditionList: []string{},
				},
				Keys:  []string{"bk_biz_id", "ip"},
				Limit: 10,
			},
			expected: []sqlExpected{
				{
					db:  `2_bkmonitor_time_series_1572904`,
					sql: `show field keys from cmdb_collector_middleware_analyze_duration_count`,
				},
			},
		},
		"time_series.default": {
			infoType: TimeSeries,
			params: &Params{
				TableID: "system.cpu_detail",
				Keys:    []string{"interrupt", "guest", "usage"},
				Conditions: structured.Conditions{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"2"},
						},
					},
				},
				Limit: 1,
			},
			expected: []sqlExpected{
				{
					db:  `system`,
					sql: `select interrupt, *::tag from cpu_detail where bk_biz_id='2' and time >= %d and time < %d limit 1`,
				},
				{
					db:  `system`,
					sql: `select guest, *::tag from cpu_detail where bk_biz_id='2' and time >= %d and time < %d limit 1`,
				},
				{
					db:  `system`,
					sql: `select usage, *::tag from cpu_detail where bk_biz_id='2' and time >= %d and time < %d limit 1`,
				},
			},
		},
		"time_series.pivot_table": {
			infoType: TimeSeries,
			params: &Params{
				TableID: "2_bkmonitor_time_series_1573076.__default__",
				Keys:    []string{"metric1", "metric2"},
				Conditions: structured.Conditions{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"2"},
						},
					},
				},
				Limit: 1,
			},
			expected: []sqlExpected{
				{
					db:  `2_bkmonitor_time_series_1573076`,
					sql: `select metric_value, *::tag from __default__ where bk_biz_id='2' and time >= %d and time < %d and metric_name = 'metric1' limit 1`,
				},
				{
					db:  `2_bkmonitor_time_series_1573076`,
					sql: `select metric_value, *::tag from __default__ where bk_biz_id='2' and time >= %d and time < %d and metric_name = 'metric2' limit 1`,
				},
			},
		},
		"time_series.is_split_measurement": {
			infoType: TimeSeries,
			params: &Params{
				TableID: "2_bkmonitor_time_series_1572904.__default__",
				Keys:    []string{"cmdb_collector_middleware_analyze_duration_count", "cmdb_apimachinary_requests_duration_millisecond_count"},
				Conditions: structured.Conditions{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "bk_biz_id",
							Operator:      "eq",
							Value:         []string{"2"},
						},
					},
				},
				Limit: 1,
			},
			expected: []sqlExpected{
				{
					db:  `2_bkmonitor_time_series_1572904`,
					sql: `select value, *::tag from cmdb_collector_middleware_analyze_duration_count where bk_biz_id='2' and time >= %d and time < %d limit 1`,
				},
				{
					db:  `2_bkmonitor_time_series_1572904`,
					sql: `select value, *::tag from cmdb_apimachinary_requests_duration_millisecond_count where bk_biz_id='2' and time >= %d and time < %d limit 1`,
				},
			},
		},
	}

	end := time.Now()
	start := end.Add(-time.Minute * 10)

	var step int64 = 1e9
	startTime := start.UnixNano() / step
	endTime := end.UnixNano() / step
	fmt.Println(startTime, endTime)

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			c.params.Start = fmt.Sprintf("%d", startTime)
			c.params.End = fmt.Sprintf("%d", endTime)
			sqlInfos, err := makeInfluxQLList(context.Background(), c.infoType, c.params, c.spaceUid)
			assert.Nil(t, err)
			assert.Equal(t, len(c.expected), len(sqlInfos))
			if len(c.expected) == len(sqlInfos) {
				for i, e := range c.expected {
					assert.Equal(t, e.db, sqlInfos[i].DB)
					var expected = e.sql
					if c.infoType != FieldKeys {
						expected = fmt.Sprintf(e.sql, startTime*step, endTime*step)
					}
					assert.Equal(t, expected, sqlInfos[i].SQL)
					t.Log(fmt.Sprintf("db: %s, sql: %s", sqlInfos[i].DB, sqlInfos[i].SQL))
				}
			}
		})
	}
}
