// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	md "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

func TestQueryToMetric(t *testing.T) {
	spaceUid := influxdb.SpaceUid
	db := "result_table"
	measurement := "influxdb"
	tableID := influxdb.ResultTableInfluxDB
	field := "kube_pod_info"
	field01 := "kube_node_info"
	dataLabel := "influxdb"
	storageID := "2"
	clusterName := "default"

	mock.Init()
	ctx := md.InitHashID(context.Background())

	var testCases = map[string]struct {
		query  *Query
		metric *md.QueryMetric
	}{
		"test table id query": {
			query: &Query{
				TableID:       TableID(tableID),
				FieldName:     field,
				ReferenceName: "a",
				Start:         "0",
				End:           "300",
				Step:          "1m",
			},
			metric: &md.QueryMetric{
				QueryList: md.QueryList{
					&md.Query{
						TableID:      tableID,
						DB:           db,
						Measurement:  measurement,
						StorageID:    storageID,
						ClusterName:  clusterName,
						Field:        field,
						Fields:       []string{field},
						Measurements: []string{measurement},
					},
				},
				ReferenceName: "a",
				MetricName:    field,
				IsCount:       false,
			},
		},
		"test metric query": {
			query: &Query{
				FieldName:     field,
				ReferenceName: "a",
				Start:         "0",
				End:           "300",
				Step:          "1m",
			},
			metric: &md.QueryMetric{
				QueryList: md.QueryList{
					&md.Query{
						TableID:      tableID,
						DB:           db,
						Measurement:  measurement,
						StorageID:    storageID,
						ClusterName:  clusterName,
						Field:        field,
						Fields:       []string{field},
						Measurements: []string{measurement},
					},
				},
				ReferenceName: "a",
				MetricName:    field,
				IsCount:       false,
			},
		},
		"test two stage metric query": {
			query: &Query{
				TableID:       TableID(dataLabel),
				FieldName:     field,
				ReferenceName: "a",
				Start:         "0",
				End:           "300",
				Step:          "1m",
			},
			metric: &md.QueryMetric{
				QueryList: md.QueryList{
					&md.Query{
						TableID:      tableID,
						DB:           db,
						Measurement:  measurement,
						StorageID:    storageID,
						ClusterName:  clusterName,
						Field:        field,
						Fields:       []string{field},
						Measurements: []string{measurement},
					},
				},
				ReferenceName: "a",
				MetricName:    field,
				IsCount:       false,
			},
		},
		"test regexp metric query": {
			query: &Query{
				TableID:       TableID(tableID),
				FieldName:     "unify_query_.*_total",
				ReferenceName: "a",
				Start:         "0",
				End:           "300",
				Step:          "1m",
				IsRegexp:      true,
			},
			metric: &md.QueryMetric{
				QueryList: md.QueryList{
					&md.Query{
						TableID:      tableID,
						DB:           db,
						Measurement:  measurement,
						StorageID:    storageID,
						ClusterName:  clusterName,
						Field:        "unify_query_.*_total",
						Fields:       []string{field, field01},
						Measurements: []string{measurement},
					},
				},
				ReferenceName: "a",
				MetricName:    "unify_query_.*_total",
				IsCount:       false,
			},
		},
	}
	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = context.Background()
			metric, err := c.query.ToQueryMetric(ctx, spaceUid)
			assert.Nil(t, err)
			assert.Equal(t, 1, len(c.metric.QueryList))
			if err == nil {
				assert.Equal(t, c.metric.QueryList[0].TableID, metric.QueryList[0].TableID)
				assert.Equal(t, c.metric.QueryList[0].Field, metric.QueryList[0].Field)
				assert.Equal(t, c.metric.QueryList[0].Fields, metric.QueryList[0].Fields)
			}
		})
	}
}

func TestQueryTs_ToQueryReference(t *testing.T) {
	ctx := context.Background()
	mock.Init()
	err := featureFlag.MockFeatureFlag(
		ctx, `{
	"must-vm-query": {
		"variations": {
			"Default": false,
			"true": true,
			"false": false
		},
		"targeting": [{
			"query": "tableID in [\"system.cpu_detail\", \"system.disk\"] and name in [\"my_bro\"]",
			"percentage": {
				"true": 100,
				"false":0 
			}
		}],
		"defaultRule": {
			"variation": "Default"
		}
	},
	"vm-query": {
		"variations": {
			"Default": false,
			"true": true,
			"false": false
		},
		"targeting": [{
			"query": "spaceUid in [\"vm-query\"]",
			"percentage": {
				"true": 100,
				"false": 0
			}
		}],
		"defaultRule": {
			"variation": "Default"
		}
	}
}`,
	)

	influxdb.SetRedisClient(ctx)
	influxdb.SetSpaceTsDbMockData(ctx, ir.SpaceInfo{
		"vm-query": ir.Space{
			"system.cpu_detail": &ir.SpaceResultTable{
				TableId: "system.cpu_detail",
				Filters: []map[string]string{
					{
						"bk_biz_id": "2",
					},
				},
			},
			"system.disk": &ir.SpaceResultTable{
				TableId: "system.disk",
				Filters: []map[string]string{
					{
						"bk_biz_id": "2",
					},
				},
			},
			"system.cpu_summary": &ir.SpaceResultTable{
				TableId: "system.cpu_summary",
				Filters: []map[string]string{
					{
						"bk_biz_id": "2",
					},
				},
			},
			"script_tmpfs_monitor.group_default": &ir.SpaceResultTable{
				TableId: "script_tmpfs_monitor.group_default",
				Filters: []map[string]string{},
			},
		},
		"influxdb-query": ir.Space{
			"system.cpu_detail": &ir.SpaceResultTable{
				TableId: "system.cpu_detail",
				Filters: []map[string]string{
					{
						"bk_biz_id": "2",
					},
				},
			},
			"system.disk": &ir.SpaceResultTable{
				TableId: "system.disk",
				Filters: []map[string]string{
					{
						"bk_biz_id": "2",
					},
				},
			},
			"system.cpu_summary": &ir.SpaceResultTable{
				TableId: "system.cpu_summary",
				Filters: []map[string]string{
					{
						"bk_biz_id": "2",
					},
				},
			},
			"script_tmpfs_monitor.group_default": &ir.SpaceResultTable{
				TableId: "script_tmpfs_monitor.group_default",
				Filters: []map[string]string{},
			},
		},
	}, ir.ResultTableDetailInfo{
		"script_tmpfs_monitor.group_default": &ir.ResultTableDetail{
			TableId:         "script_tmpfs_monitor.group_default",
			DB:              "script_tmpfs_monitor",
			Measurement:     "group_default",
			Fields:          []string{"usage"},
			MeasurementType: redis.BkExporter,
		},
		"system.cpu_detail": &ir.ResultTableDetail{
			TableId:         "system.cpu_detail",
			DB:              "system",
			Measurement:     "cpu_detail",
			VmRt:            "100147_ieod_system_cpu_detail_raw",
			Fields:          []string{"usage"},
			MeasurementType: redis.BKTraditionalMeasurement,
		},
		"system.disk": &ir.ResultTableDetail{
			TableId:         "system.disk",
			DB:              "system",
			Measurement:     "disk",
			VmRt:            "100147_ieod_system_disk_raw",
			Fields:          []string{"usage"},
			MeasurementType: redis.BKTraditionalMeasurement,
		},
		"system.cpu_summary": &ir.ResultTableDetail{
			TableId:         "system.cpu_summary",
			DB:              "system",
			Measurement:     "cpu_summary",
			VmRt:            "100147_ieod_system_cpu_summary_raw",
			Fields:          []string{"usage"},
			MeasurementType: redis.BKTraditionalMeasurement,
		},
	}, nil, nil)

	for name, tc := range map[string]struct {
		ts     *QueryTs
		source string
		ok     bool
		expand *md.VmExpand
		ref    md.QueryReference
		promql string
	}{
		"vm 查询开启 + 多 tableID 都开启单指标单表 = 查询 VM": {
			source: "username:my_bro",
			ts: &QueryTs{
				SpaceUid: "vm-query",
				QueryList: []*Query{
					{
						TableID:       "system.cpu_detail",
						FieldName:     "usage",
						ReferenceName: "a",
					},
					{
						TableID:       "system.disk",
						FieldName:     "usage",
						ReferenceName: "b",
					},
				},
				MetricMerge: "a + b",
			},
			ok: true,
			expand: &md.VmExpand{
				ResultTableList: []string{"100147_ieod_system_cpu_detail_raw", "100147_ieod_system_disk_raw"},
				MetricFilterCondition: map[string]string{
					"a": `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
					"b": `bk_biz_id="2", result_table_id="100147_ieod_system_disk_raw", __name__="usage_value"`,
				},
			},
			ref: md.QueryReference{
				"a": &md.QueryMetric{
					QueryList: md.QueryList{
						{
							IsSingleMetric: true,
							Measurement:    "cpu_detail",
							Field:          "usage",
							VmCondition:    `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
						},
					},
				},
				"b": &md.QueryMetric{
					QueryList: md.QueryList{
						{
							IsSingleMetric: true,
							Measurement:    "disk",
							Field:          "usage",
							VmCondition:    `bk_biz_id="2", result_table_id="100147_ieod_system_disk_raw", __name__="usage_value"`,
						},
					},
				},
			},
		},
		"vm 查询开启 + tableID 开启单指标单表 = 查询 VM": {
			source: "username:my_bro",
			ts: &QueryTs{
				SpaceUid: "vm-query",
				QueryList: []*Query{
					{
						TableID:       "system.cpu_detail",
						FieldName:     "usage",
						ReferenceName: "a",
					},
				},
				MetricMerge: "a",
			},
			ok: true,
			expand: &md.VmExpand{
				ResultTableList: []string{"100147_ieod_system_cpu_detail_raw"},
				MetricFilterCondition: map[string]string{
					"a": `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
				},
			},
			ref: md.QueryReference{
				"a": &md.QueryMetric{
					QueryList: md.QueryList{
						{
							IsSingleMetric: true,
							Measurement:    "cpu_detail",
							Field:          "usage",
							VmCondition:    `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
						},
					},
				},
			},
		},
		"vm 查询开启 + 多 tableID 只有部份开启单指标单表 = 查询 InfluxDB": {
			source: "username:my_bro",
			ts: &QueryTs{
				SpaceUid: "vm-query",
				QueryList: []*Query{
					{
						TableID:       "system.cpu_detail",
						FieldName:     "usage",
						ReferenceName: "a",
					},
					{
						TableID:       "system.cpu_summary",
						FieldName:     "usage",
						ReferenceName: "b",
					},
				},
				MetricMerge: "a + b",
			},
			ref: md.QueryReference{
				"a": &md.QueryMetric{
					QueryList: md.QueryList{
						{
							IsSingleMetric: false,
							Measurement:    "cpu_detail",
							Field:          "usage",
							VmCondition:    `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
						},
					},
				},
				"b": &md.QueryMetric{
					QueryList: md.QueryList{
						{
							IsSingleMetric: false,
							Measurement:    "cpu_summary",
							Field:          "usage",
							VmCondition:    `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_summary_raw", __name__="usage_value"`,
						},
					},
				},
			},
		},
		"vm 查询开启 + tableID 未开启单指标单表 = 查询 InfluxDB": {
			source: "username:my_bro",
			ts: &QueryTs{
				SpaceUid: "vm-query",
				QueryList: []*Query{
					{
						TableID:       "system.cpu_summary",
						FieldName:     "usage",
						ReferenceName: "b",
					},
				},
				MetricMerge: "b",
			},
			ref: md.QueryReference{
				"b": &md.QueryMetric{
					QueryList: md.QueryList{
						{
							IsSingleMetric: false,
							Measurement:    "cpu_summary",
							Field:          "usage",
							VmCondition:    `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_summary_raw", __name__="usage_value"`,
						},
					},
				},
			},
		},
		"vm 查询开启 + 该用户未开启单指标单表 = 查询 InfluxDB": {
			source: "username:my_bro_1",
			ts: &QueryTs{
				SpaceUid: "vm-query",
				QueryList: []*Query{
					{
						TableID:       "system.cpu_detail",
						FieldName:     "usage",
						ReferenceName: "b",
					},
				},
				MetricMerge: "b",
			},
			ref: md.QueryReference{
				"b": &md.QueryMetric{
					QueryList: md.QueryList{
						{
							IsSingleMetric: false,
							Measurement:    "cpu_detail",
							Field:          "usage",
							VmCondition:    `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
						},
					},
				},
			},
		},
		"未开启 vm查询 + 多 tableID 都开启单指标单表 = 查询 VM": {
			source: "username:my_bro",
			ts: &QueryTs{
				SpaceUid: "influxdb-query",
				QueryList: []*Query{
					{
						TableID:       "system.cpu_detail",
						FieldName:     "usage",
						ReferenceName: "a",
					},
					{
						TableID:       "system.disk",
						FieldName:     "usage",
						ReferenceName: "b",
					},
				},
				MetricMerge: "a + b",
			},
			ok: true,
			expand: &md.VmExpand{
				ResultTableList: []string{"100147_ieod_system_cpu_detail_raw", "100147_ieod_system_disk_raw"},
				MetricFilterCondition: map[string]string{
					"a": `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
					"b": `bk_biz_id="2", result_table_id="100147_ieod_system_disk_raw", __name__="usage_value"`,
				},
			},
			ref: md.QueryReference{
				"a": &md.QueryMetric{
					QueryList: md.QueryList{
						{
							IsSingleMetric: true,
							Measurement:    "cpu_detail",
							Field:          "usage",
							VmCondition:    `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
						},
					},
				},
				"b": &md.QueryMetric{
					QueryList: md.QueryList{
						{
							IsSingleMetric: true,
							Measurement:    "disk",
							Field:          "usage",
							VmCondition:    `bk_biz_id="2", result_table_id="100147_ieod_system_disk_raw", __name__="usage_value"`,
						},
					},
				},
			},
		},
		"vm 查询开启 + tableID 未开启单指标单表 = 查询 InfluxDB - 1": {
			source: "username:my_bro",
			ts: &QueryTs{
				SpaceUid: "vm-query",
				QueryList: []*Query{
					{
						TableID:       "script_tmpfs_monitor.group_default",
						FieldName:     ".*",
						ReferenceName: "b",
						IsRegexp:      true,
					},
				},
				MetricMerge: "b",
			},
			ref: md.QueryReference{
				"b": &md.QueryMetric{
					QueryList: md.QueryList{
						{
							IsSingleMetric: false,
							Measurement:    "group_default",
							Field:          "metric_value",
							VmCondition:    `__name__=~".*_value"`,
						},
					},
				},
			},
		},
		"vm 聚合查询验证 - 1": {
			source: "username:my_bro",
			ts: &QueryTs{
				SpaceUid: "vm-query",
				QueryList: []*Query{
					{
						TableID:       "system.cpu_detail",
						FieldName:     "usage",
						ReferenceName: "a",
						TimeAggregation: TimeAggregation{
							Function: "count_over_time",
							Window:   "1m",
						},
						AggregateMethodList: AggregateMethodList{
							{
								Method:     "sum",
								Dimensions: []string{"ip"},
							},
						},
					},
				},
				MetricMerge: "a",
				Start:       "1718865258",
				End:         "1718868858",
				Step:        "1m",
			},
			ok:     true,
			promql: `sum by (ip) (count_over_time(a[1m]))`,
		},
		"vm 聚合查询验证 - 2": {
			source: "username:my_bro",
			ts: &QueryTs{
				SpaceUid: "vm-query",
				QueryList: []*Query{
					{
						TableID:       "system.cpu_detail",
						FieldName:     "usage",
						ReferenceName: "a",
						TimeAggregation: TimeAggregation{
							Function: "increase",
							Window:   "1m",
						},
						AggregateMethodList: AggregateMethodList{
							{
								Method:     "sum",
								Dimensions: []string{"ip"},
							},
						},
					},
				},
				MetricMerge: "a",
				Start:       "1718865258",
				End:         "1718868858",
				Step:        "1m",
			},
			ok:     true,
			promql: `sum by (ip) (increase(a[1m]))`,
		},
		"vm 聚合查询验证 - 3": {
			source: "username:my_bro",
			ts: &QueryTs{
				SpaceUid: "vm-query",
				QueryList: []*Query{
					{
						TableID:       "system.cpu_detail",
						FieldName:     "usage",
						ReferenceName: "a",
						TimeAggregation: TimeAggregation{
							Function: "sum_over_time",
							Window:   "1m",
						},
						AggregateMethodList: AggregateMethodList{
							{
								Method:     "sum",
								Dimensions: []string{"ip", "service"},
							},
							{
								Method: "topk",
								VArgsList: []interface{}{
									5,
								},
							},
						},
					},
				},
				MetricMerge: "a",
				Start:       "1718865258",
				End:         "1718868858",
				Step:        "1m",
			},
			ok:     true,
			promql: `topk(5, sum by (ip, service) (sum_over_time(a[1m])))`,
		},
		"非 vm 聚合查询验证 - 1": {
			source: "username:other",
			ts: &QueryTs{
				SpaceUid: "influxdb-query",
				QueryList: []*Query{
					{
						TableID:       "system.cpu_detail",
						FieldName:     "usage",
						ReferenceName: "a",
						TimeAggregation: TimeAggregation{
							Function: "count_over_time",
							Window:   "1m",
						},
						AggregateMethodList: AggregateMethodList{
							{
								Method:     "sum",
								Dimensions: []string{"ip"},
							},
						},
					},
				},
				MetricMerge: "a",
				Start:       "1718865258",
				End:         "1718868858",
				Step:        "1m",
			},
			ok:     false,
			promql: `sum by (ip) (last_over_time(a[1m]))`,
		},
		"非 vm 聚合查询验证 - 2": {
			source: "username:other",
			ts: &QueryTs{
				SpaceUid: "influxdb-query",
				QueryList: []*Query{
					{
						TableID:       "system.cpu_detail",
						FieldName:     "usage",
						ReferenceName: "a",
						TimeAggregation: TimeAggregation{
							Function: "increase",
							Window:   "1m",
						},
						AggregateMethodList: AggregateMethodList{
							{
								Method:     "sum",
								Dimensions: []string{"ip"},
							},
						},
					},
				},
				MetricMerge: "a",
				Start:       "1718865258",
				End:         "1718868858",
				Step:        "1m",
			},
			ok:     false,
			promql: `sum by (ip) (increase(a[1m]))`,
		},
		"非 vm 聚合查询验证 - 3": {
			source: "username:other",
			ts: &QueryTs{
				SpaceUid: "influxdb-query",
				QueryList: []*Query{
					{
						TableID:       "system.cpu_detail",
						FieldName:     "usage",
						ReferenceName: "a",
						TimeAggregation: TimeAggregation{
							Function: "sum_over_time",
							Window:   "1m",
						},
						AggregateMethodList: AggregateMethodList{
							{
								Method:     "sum",
								Dimensions: []string{"ip"},
							},
							{
								Method: "topk",
								VArgsList: []interface{}{
									1,
								},
							},
						},
					},
				},
				MetricMerge: "a",
				Start:       "1718865258",
				End:         "1718868858",
				Step:        "1m",
			},
			ok:     false,
			promql: `topk(1, sum by (ip) (last_over_time(a[1m])))`,
		},
		"非 vm 聚合查询验证 - 4": {
			source: "username:other",
			ts: &QueryTs{
				SpaceUid: "influxdb-query",
				QueryList: []*Query{
					{
						TableID:       "system.cpu_detail",
						FieldName:     "usage",
						ReferenceName: "a",
						TimeAggregation: TimeAggregation{
							Function: "sum_over_time",
							Window:   "1m",
						},
						AggregateMethodList: AggregateMethodList{
							{
								Method:     "sum",
								Dimensions: []string{"__ext.container"},
							},
							{
								Method: "topk",
								VArgsList: []interface{}{
									1,
								},
							},
						},
					},
				},
				MetricMerge: "a",
				Start:       "1718865258",
				End:         "1718868858",
				Step:        "1m",
			},
			ok:     false,
			promql: `topk(1, sum by (__ext__bk_46__container) (last_over_time(a[1m])))`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var (
				ref      md.QueryReference
				vmExpand *md.VmExpand
				ok       bool
			)
			ctx = md.InitHashID(ctx)

			md.SetUser(ctx, tc.source, tc.ts.SpaceUid, "")
			ref, err = tc.ts.ToQueryReference(ctx)
			assert.Nil(t, err)
			if err == nil {
				vmExpand = ref.ToVmExpand(ctx)
				ok = md.GetQueryParams(ctx).IsDirectQuery()
				assert.Equal(t, tc.ok, ok)

				if tc.expand != nil {
					assert.Equal(t, tc.expand, vmExpand)
				}

				if tc.promql != "" {
					promExprOpt := &PromExprOption{
						IgnoreTimeAggregationEnable: !ok,
					}

					promql, _ := tc.ts.ToPromExpr(ctx, promExprOpt)
					assert.Equal(t, tc.promql, promql.String())
				}

				for refName, v := range ref {
					for idx := range v.QueryList {
						if tcRef, refOk := tc.ref[refName]; refOk {
							assert.Equal(t, tcRef.QueryList[idx].Measurement, v.QueryList[idx].Measurement)
							assert.Equal(t, tcRef.QueryList[idx].Field, v.QueryList[idx].Field)
							assert.Equal(t, tcRef.QueryList[idx].IsSingleMetric, v.QueryList[idx].IsSingleMetric)
							assert.Equal(t, tcRef.QueryList[idx].VmCondition, v.QueryList[idx].VmCondition)
						}

					}
				}
			}
		})
	}
}

func TestTimeOffset(t *testing.T) {
	for name, c := range map[string]struct {
		t    int64
		tz   string
		step time.Duration
	}{
		"test align": {
			t:    1701306000, // 2023-11-30 09:00:00 +0800 ~ 2024-05-30 09:00:00 +0800
			tz:   "Asia/Shanghai",
			step: time.Hour * 3,
		},
		"test align -1": {
			t:    1703732400, // 2023-11-30 09:00:00 +0800 ~ 2024-05-30 09:00:00 +0800
			tz:   "Asia/Shanghai",
			step: time.Hour * 3,
		},
	} {
		t.Run(name, func(t *testing.T) {
			mt := time.Unix(c.t, 0)
			tz1, t1, err := timeOffset(mt, c.tz, c.step)

			assert.Nil(t, err)
			fmt.Println(c.tz, "=>", tz1)
			fmt.Println(mt.String(), "=>", t1.String())
		})
	}
}

func TestAggregations(t *testing.T) {
	for name, c := range map[string]struct {
		query *Query
		aggs  md.Aggregates
	}{
		"test query with sum count_over_time": {
			query: &Query{
				AggregateMethodList: AggregateMethodList{
					{
						Method:     "sum",
						Dimensions: []string{"dim-1"},
					},
				},
				TimeAggregation: TimeAggregation{
					Function: "count_over_time",
					Window:   "1m",
				},
				Step:     "1m",
				Timezone: "Asia/ShangHai",
			},
			aggs: md.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"dim-1"},
				},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			aggs, err := c.query.Aggregates()
			assert.Nil(t, err)
			if err == nil {
				assert.Equal(t, aggs, c.aggs)
			}
		})
	}
}
