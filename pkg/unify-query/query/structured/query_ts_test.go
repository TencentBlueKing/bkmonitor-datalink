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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	md "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

func TestQueryToMetric(t *testing.T) {

	db := "result_table"
	tableID := influxdb.ResultTableInfluxDB
	field := "kube_pod_info"
	field1 := "kube_node_info"
	dataLabel := "influxdb"
	storageID := "2"
	clusterName := "default"

	mock.Init()
	ctx := md.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)

	start := "1741056443"
	end := "1741060043"

	var testCases = map[string]struct {
		spaceUID string
		query    *Query
		metric   *md.QueryMetric
	}{
		"test table id query": {
			query: &Query{
				TableID:       TableID(tableID),
				FieldName:     field,
				ReferenceName: "a",
				Start:         start,
				End:           end,
				Step:          "1m",
			},
			metric: &md.QueryMetric{
				QueryList: md.QueryList{
					&md.Query{
						DataSource:      BkMonitor,
						TableID:         tableID,
						DB:              db,
						Measurement:     field,
						StorageID:       storageID,
						StorageType:     consul.InfluxDBStorageType,
						ClusterName:     clusterName,
						MeasurementType: redis.BkSplitMeasurement,
						Field:           promql.StaticField,
						Fields:          []string{promql.StaticField},
						MetricNames:     []string{"kube_pod_info"},
						Measurements:    []string{field},
						Timezone:        "UTC",
						VmCondition:     `__name__="kube_pod_info_value"`,
						VmConditionNum:  1,
						DataLabel:       "influxdb",
					},
				},
				ReferenceName: "a",
				MetricName:    field,
			},
		},
		"test metric query": {
			query: &Query{
				FieldName:     field,
				ReferenceName: "a",
				Start:         start,
				End:           end,
				Step:          "1m",
			},
			metric: &md.QueryMetric{
				QueryList: md.QueryList{
					{
						DataSource:      BkMonitor,
						TableID:         tableID,
						DB:              db,
						StorageType:     consul.InfluxDBStorageType,
						StorageID:       storageID,
						ClusterName:     clusterName,
						Field:           promql.StaticField,
						MeasurementType: redis.BkSplitMeasurement,
						Fields:          []string{promql.StaticField},
						MetricNames:     []string{"kube_pod_info"},
						Measurement:     field,
						Measurements:    []string{field},
						Timezone:        "UTC",
						VmCondition:     `__name__="kube_pod_info_value"`,
						VmConditionNum:  1,
						DataLabel:       "influxdb",
					},
					{
						DataSource:      BkMonitor,
						StorageType:     consul.VictoriaMetricsStorageType,
						StorageID:       "2",
						TableID:         "result_table.vm",
						VmRt:            "2_bcs_prom_computation_result_table",
						Measurement:     field,
						Measurements:    []string{field},
						Field:           promql.StaticField,
						MeasurementType: redis.BkSplitMeasurement,
						Fields:          []string{promql.StaticField},
						MetricNames:     []string{"kube_pod_info"},
						Timezone:        "UTC",
						VmCondition:     `result_table_id="2_bcs_prom_computation_result_table", __name__="kube_pod_info_value"`,
						VmConditionNum:  2,
						DataLabel:       "vm",
					},
				},
				ReferenceName: "a",
				MetricName:    field,
				IsCount:       false,
			},
		},
		"test data label metric query": {
			query: &Query{
				TableID:       TableID(dataLabel),
				FieldName:     field,
				ReferenceName: "a",
				Start:         start,
				End:           end,
				Step:          "1m",
			},
			metric: &md.QueryMetric{
				QueryList: md.QueryList{
					{
						DataSource:      BkMonitor,
						TableID:         tableID,
						DataLabel:       "influxdb",
						DB:              db,
						StorageType:     consul.InfluxDBStorageType,
						StorageID:       storageID,
						ClusterName:     clusterName,
						Field:           promql.StaticField,
						MeasurementType: redis.BkSplitMeasurement,
						Fields:          []string{promql.StaticField},
						MetricNames:     []string{"kube_pod_info"},
						Measurement:     field,
						Measurements:    []string{field},
						Timezone:        "UTC",
						VmCondition:     `__name__="kube_pod_info_value"`,
						VmConditionNum:  1,
					},
					{
						DataSource:      BkMonitor,
						StorageType:     consul.VictoriaMetricsStorageType,
						StorageID:       "2",
						TableID:         "result_table.vm",
						VmRt:            "2_bcs_prom_computation_result_table",
						Measurement:     field,
						Measurements:    []string{field},
						Field:           promql.StaticField,
						MeasurementType: redis.BkSplitMeasurement,
						Fields:          []string{promql.StaticField},
						MetricNames:     []string{"kube_pod_info"},
						Timezone:        "UTC",
						VmCondition:     `result_table_id="2_bcs_prom_computation_result_table", __name__="kube_pod_info_value"`,
						VmConditionNum:  2,
						DataLabel:       "vm",
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
				FieldName:     "kube_.*",
				ReferenceName: "a",
				Start:         start,
				End:           end,
				Step:          "1m",
				IsRegexp:      true,
			},
			metric: &md.QueryMetric{
				QueryList: md.QueryList{
					{
						DataSource:      BkMonitor,
						TableID:         tableID,
						DB:              db,
						StorageType:     consul.InfluxDBStorageType,
						StorageID:       storageID,
						ClusterName:     clusterName,
						Field:           promql.StaticField,
						MeasurementType: redis.BkSplitMeasurement,
						Fields:          []string{promql.StaticField},
						MetricNames:     []string{"kube_pod_info", "kube_node_info", "kube_node_status_condition"},
						Measurement:     "kube_.*",
						Measurements:    []string{field, field1, "kube_node_status_condition"},
						Timezone:        "UTC",
						VmCondition:     `__name__=~"kube_.*_value"`,
						VmConditionNum:  1,
						DataLabel:       "influxdb",
					},
				},
				ReferenceName: "a",
				MetricName:    "kube_.*",
				IsCount:       false,
			},
		},
		"test bk data match table id": {
			query: &Query{
				DataSource:    BkData,
				TableID:       "2_table_id",
				FieldName:     "kube_.*",
				ReferenceName: "a",
			},
			metric: &md.QueryMetric{
				QueryList: md.QueryList{
					{
						DataSource:  BkData,
						TableID:     "2_table_id",
						StorageType: consul.BkSqlStorageType,
						DB:          "2_table_id",
						Field:       "kube_.*",
					},
				},
				ReferenceName: "a",
				MetricName:    "kube_.*",
			},
		},
		"test bk data not match table id": {
			query: &Query{
				DataSource:    BkData,
				TableID:       "3_table_id",
				FieldName:     "kube_.*",
				ReferenceName: "a",
			},
			metric: &md.QueryMetric{
				ReferenceName: "a",
				MetricName:    "kube_.*",
			},
		},
		"test bk data not match table id - 1": {
			spaceUID: "bkci__2",
			query: &Query{
				DataSource:    BkData,
				TableID:       "2_table_id",
				FieldName:     "kube_.*",
				ReferenceName: "a",
			},
			metric: &md.QueryMetric{
				ReferenceName: "a",
				MetricName:    "kube_.*",
			},
		},
	}
	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = md.InitHashID(ctx)
			spaceUID := c.spaceUID
			if spaceUID == "" {
				spaceUID = influxdb.SpaceUid
			}

			metric, err := c.query.ToQueryMetric(ctx, spaceUID)
			assert.Nil(t, err)

			assert.Equal(t, c.metric.ToJson(true), metric.ToJson(true))
		})
	}
}

func TestQueryTs_ToQueryReference(t *testing.T) {
	mock.Init()
	ctx := md.InitHashID(context.Background())

	influxdb.MockSpaceRouter(ctx)

	for name, tc := range map[string]struct {
		ts *QueryTs

		isDirectQuery bool
		expand        *md.VmExpand
		ref           md.QueryReference
		promql        string
	}{
		"非单指标单表 - 多 tableID 都查询 vm": {
			ts: &QueryTs{
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

			isDirectQuery: true,
			promql:        "a + b",
			expand: &md.VmExpand{
				ResultTableList: []string{"100147_ieod_system_cpu_detail_raw", "100147_ieod_system_disk_raw"},
				MetricFilterCondition: map[string]string{
					"a": `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
					"b": `bk_biz_id="2", result_table_id="100147_ieod_system_disk_raw", __name__="usage_value"`,
				},
			},
			ref: md.QueryReference{
				"a": {
					{
						QueryList: md.QueryList{
							{
								DataSource:      BkMonitor,
								Condition:       "bk_biz_id='2'",
								Timezone:        "UTC",
								Fields:          []string{"usage"},
								TableID:         "system.cpu_detail",
								MetricNames:     []string{"bkmonitor:system:cpu_detail:usage"},
								DataLabel:       "cpu_detail",
								VmRt:            "100147_ieod_system_cpu_detail_raw",
								VmConditionNum:  3,
								VmCondition:     `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
								StorageID:       "2",
								StorageType:     consul.VictoriaMetricsStorageType,
								Field:           "usage",
								MeasurementType: redis.BKTraditionalMeasurement,
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
											IsForceEq:     true,
										},
									},
								},
							},
						},
						MetricName:    "usage",
						ReferenceName: "a",
					},
				},
				"b": {
					{
						QueryList: md.QueryList{
							{
								DataSource:      BkMonitor,
								Condition:       "bk_biz_id='2'",
								Timezone:        "UTC",
								Fields:          []string{"usage"},
								MetricNames:     []string{"bkmonitor:system:disk:usage"},
								TableID:         "system.disk",
								DataLabel:       "disk",
								VmRt:            "100147_ieod_system_disk_raw",
								CmdbLevelVmRt:   "rt_by_cmdb_level",
								VmConditionNum:  3,
								VmCondition:     `bk_biz_id="2", result_table_id="100147_ieod_system_disk_raw", __name__="usage_value"`,
								StorageID:       "2",
								StorageType:     consul.VictoriaMetricsStorageType,
								Field:           "usage",
								MeasurementType: redis.BKTraditionalMeasurement,
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
											IsForceEq:     true,
										},
									},
								},
							},
						},
						MetricName:    "usage",
						ReferenceName: "b",
					},
				},
			},
		},
		"非单指标单表 - 多 tableID 部分查询VM": {
			ts: &QueryTs{
				QueryList: []*Query{
					{
						TableID:       "system.cpu_summary",
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
			isDirectQuery: true,
			promql:        "a + b",
			expand: &md.VmExpand{
				ResultTableList: []string{"100147_ieod_system_disk_raw"},
				MetricFilterCondition: map[string]string{
					"a": ``,
					"b": `bk_biz_id="2", result_table_id="100147_ieod_system_disk_raw", __name__="usage_value"`,
				},
			},
			ref: md.QueryReference{
				"a": {
					{
						QueryList: md.QueryList{
							{
								DataSource:      BkMonitor,
								Condition:       "bk_biz_id='2'",
								Timezone:        "UTC",
								Fields:          []string{"usage"},
								MetricNames:     []string{"bkmonitor:system:cpu_summary:usage"},
								TableID:         "system.cpu_summary",
								DataLabel:       "cpu_summary",
								ClusterName:     "default",
								DB:              "system",
								Measurement:     "cpu_summary",
								Measurements:    []string{"cpu_summary"},
								VmConditionNum:  2,
								VmCondition:     `bk_biz_id="2", __name__="usage_value"`,
								StorageID:       "2",
								StorageType:     consul.InfluxDBStorageType,
								Field:           "usage",
								MeasurementType: redis.BKTraditionalMeasurement,
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
											IsForceEq:     true,
										},
									},
								},
							},
						},
						MetricName:    "usage",
						ReferenceName: "a",
					},
				},
				"b": {
					{
						QueryList: md.QueryList{
							{
								DataSource:      BkMonitor,
								Condition:       "bk_biz_id='2'",
								Timezone:        "UTC",
								Fields:          []string{"usage"},
								MetricNames:     []string{"bkmonitor:system:disk:usage"},
								TableID:         "system.disk",
								DataLabel:       "disk",
								VmRt:            "100147_ieod_system_disk_raw",
								CmdbLevelVmRt:   "rt_by_cmdb_level",
								VmConditionNum:  3,
								VmCondition:     `bk_biz_id="2", result_table_id="100147_ieod_system_disk_raw", __name__="usage_value"`,
								StorageID:       "2",
								StorageType:     consul.VictoriaMetricsStorageType,
								Field:           "usage",
								MeasurementType: redis.BKTraditionalMeasurement,
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
											IsForceEq:     true,
										},
									},
								},
							},
						},
						MetricName:    "usage",
						ReferenceName: "b",
					},
				},
			},
		},
		"tableID 未开启 VM 查询 = 查询 InfluxDB": {
			ts: &QueryTs{
				QueryList: []*Query{
					{
						TableID:       "system.cpu_summary",
						FieldName:     "usage",
						ReferenceName: "b",
					},
				},
				MetricMerge: "b",
			},
			promql: "b",
			ref: md.QueryReference{
				"b": {
					{
						QueryList: md.QueryList{
							{
								DataSource:      BkMonitor,
								Condition:       "bk_biz_id='2'",
								Timezone:        "UTC",
								Fields:          []string{"usage"},
								MetricNames:     []string{"bkmonitor:system:cpu_summary:usage"},
								TableID:         "system.cpu_summary",
								DataLabel:       "cpu_summary",
								DB:              "system",
								Measurement:     "cpu_summary",
								Measurements:    []string{"cpu_summary"},
								ClusterName:     "default",
								VmConditionNum:  2,
								VmCondition:     `bk_biz_id="2", __name__="usage_value"`,
								StorageID:       "2",
								StorageType:     consul.InfluxDBStorageType,
								Field:           "usage",
								MeasurementType: redis.BKTraditionalMeasurement,
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
											IsForceEq:     true,
										},
									},
								},
							},
						},
						ReferenceName: "b",
						MetricName:    "usage",
					},
				},
			},
		},
		"bk_inst_id / bk_obj_id 作为条件 = 查询 VM cmdb level rt": {
			ts: &QueryTs{
				QueryList: []*Query{
					{
						TableID:       "system.disk",
						FieldName:     "usage",
						ReferenceName: "b",
						Conditions: Conditions{FieldList: []ConditionField{
							{
								DimensionName: "bk_obj_id",
								Operator:      Ncontains,
								Value:         []string{"0"},
								IsForceEq:     true,
							},
						}},
					},
				},
				MetricMerge: "b",
			},
			promql:        "b",
			isDirectQuery: true,
			expand: &md.VmExpand{
				ResultTableList: []string{"rt_by_cmdb_level"},
				MetricFilterCondition: map[string]string{
					"b": `bk_biz_id="2", bk_obj_id!="0", result_table_id="rt_by_cmdb_level", __name__="usage_value"`,
				},
			},
			ref: md.QueryReference{
				"b": {
					{
						QueryList: md.QueryList{
							{
								DataSource:      BkMonitor,
								Condition:       "bk_obj_id!='0' and bk_biz_id='2'",
								Timezone:        "UTC",
								Fields:          []string{"usage"},
								MetricNames:     []string{"bkmonitor:system:disk:usage"},
								TableID:         "system.disk",
								DataLabel:       "disk",
								VmRt:            "rt_by_cmdb_level",
								CmdbLevelVmRt:   "rt_by_cmdb_level",
								VmConditionNum:  4,
								VmCondition:     `bk_biz_id="2", bk_obj_id!="0", result_table_id="rt_by_cmdb_level", __name__="usage_value"`,
								StorageID:       "2",
								StorageType:     consul.VictoriaMetricsStorageType,
								Field:           "usage",
								MeasurementType: redis.BKTraditionalMeasurement,
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
											IsForceEq:     true,
										},
										{
											DimensionName: "bk_obj_id",
											Operator:      Ncontains,
											Value:         []string{"0"},
											IsForceEq:     true,
										},
									},
								},
							},
						},
						ReferenceName: "b",
						MetricName:    "usage",
					},
				},
			},
		},
		"bk_inst_id / bk_obj_id 作为条件 = 查询 VM": {
			ts: &QueryTs{
				QueryList: []*Query{
					{
						TableID:       "system.cpu_detail",
						FieldName:     "usage",
						ReferenceName: "b",
						Conditions: Conditions{FieldList: []ConditionField{
							{
								DimensionName: "bk_obj_id",
								Operator:      Ncontains,
								Value:         []string{"0"},
								IsForceEq:     true,
							},
						}},
					},
				},
				MetricMerge: "b",
			},
			promql:        "b",
			isDirectQuery: true,
			expand: &md.VmExpand{
				ResultTableList: []string{"100147_ieod_system_cpu_detail_cmdb"},
				MetricFilterCondition: map[string]string{
					"b": `bk_biz_id="2", bk_obj_id!="0", result_table_id="100147_ieod_system_cpu_detail_cmdb", __name__="usage_value"`,
				},
			},
			ref: md.QueryReference{
				"b": {
					{
						QueryList: md.QueryList{
							{
								DataSource:      BkMonitor,
								Condition:       "bk_obj_id!='0' and bk_biz_id='2'",
								Timezone:        "UTC",
								Fields:          []string{"usage"},
								MetricNames:     []string{"bkmonitor:system:cpu_detail:usage"},
								TableID:         "system.cpu_detail",
								DataLabel:       "cpu_detail",
								VmRt:            "100147_ieod_system_cpu_detail_cmdb",
								VmConditionNum:  4,
								VmCondition:     `bk_biz_id="2", bk_obj_id!="0", result_table_id="100147_ieod_system_cpu_detail_cmdb", __name__="usage_value"`,
								StorageID:       "2",
								StorageType:     consul.VictoriaMetricsStorageType,
								Field:           "usage",
								MeasurementType: redis.BKTraditionalMeasurement,
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
											IsForceEq:     true,
										},
										{
											DimensionName: "bk_obj_id",
											Operator:      Ncontains,
											Value:         []string{"0"},
											IsForceEq:     true,
										},
									},
								},
							},
						},
						ReferenceName: "b",
						MetricName:    "usage",
					},
				},
			},
		},
		"bk_inst_id / bk_obj_id 作为聚合 = 查询 VM": {
			ts: &QueryTs{
				QueryList: []*Query{
					{
						TableID:       "system.cpu_detail",
						FieldName:     "usage",
						ReferenceName: "b",
						AggregateMethodList: AggregateMethodList{
							{
								Method: "increase",
								Window: "1m",
							},
							{
								Method: "sum",
								Dimensions: []string{
									"bk_inst_id",
								},
							},
						},
					},
				},
				MetricMerge: "b",
			},
			promql:        "sum by (bk_inst_id) (increase(b[1m]))",
			isDirectQuery: true,
			expand: &md.VmExpand{
				ResultTableList: []string{"100147_ieod_system_cpu_detail_cmdb"},
				MetricFilterCondition: map[string]string{
					"b": `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_cmdb", __name__="usage_value"`,
				},
			},
			ref: md.QueryReference{
				"b": {
					{
						QueryList: md.QueryList{
							{
								DataSource:      BkMonitor,
								Condition:       "bk_biz_id='2'",
								Timezone:        "UTC",
								Fields:          []string{"usage"},
								MetricNames:     []string{"bkmonitor:system:cpu_detail:usage"},
								TableID:         "system.cpu_detail",
								DataLabel:       "cpu_detail",
								VmRt:            "100147_ieod_system_cpu_detail_cmdb",
								VmConditionNum:  3,
								VmCondition:     `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_cmdb", __name__="usage_value"`,
								StorageID:       "2",
								StorageType:     consul.VictoriaMetricsStorageType,
								Field:           "usage",
								MeasurementType: redis.BKTraditionalMeasurement,
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
											IsForceEq:     true,
										},
									},
								},
							},
						},
						ReferenceName: "b",
						MetricName:    "usage",
					},
				},
			},
		},
		"vm 聚合查询验证 - 1": {
			ts: &QueryTs{
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
			isDirectQuery: true,
			promql:        `sum by (ip) (count_over_time(a[1m]))`,
			expand: &md.VmExpand{
				ResultTableList: []string{"100147_ieod_system_cpu_detail_raw"},
				MetricFilterCondition: map[string]string{
					"a": `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
				},
			},
			ref: md.QueryReference{
				"a": {
					{
						QueryList: md.QueryList{
							{
								DataSource:      BkMonitor,
								Condition:       "bk_biz_id='2'",
								Timezone:        "UTC",
								Fields:          []string{"usage"},
								MetricNames:     []string{"bkmonitor:system:cpu_detail:usage"},
								TableID:         "system.cpu_detail",
								DataLabel:       "cpu_detail",
								VmRt:            "100147_ieod_system_cpu_detail_raw",
								VmConditionNum:  3,
								VmCondition:     `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
								StorageID:       "2",
								StorageType:     consul.VictoriaMetricsStorageType,
								Field:           "usage",
								MeasurementType: redis.BKTraditionalMeasurement,
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
											IsForceEq:     true,
										},
									},
								},
								Aggregates: md.Aggregates{
									{
										Name:       "count",
										Dimensions: []string{"ip"},
										Window:     time.Minute,
									},
								},
							},
						},
						MetricName:    "usage",
						ReferenceName: "a",
					},
				},
			},
		},
		"vm 聚合查询验证 - 2": {
			ts: &QueryTs{

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
			isDirectQuery: true,
			promql:        `sum by (ip) (increase(a[1m]))`,
			expand: &md.VmExpand{
				ResultTableList: []string{"100147_ieod_system_cpu_detail_raw"},
				MetricFilterCondition: map[string]string{
					"a": `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
				},
			},
			ref: md.QueryReference{
				"a": {
					{
						QueryList: md.QueryList{
							{
								DataSource:      BkMonitor,
								Condition:       "bk_biz_id='2'",
								Timezone:        "UTC",
								Fields:          []string{"usage"},
								MetricNames:     []string{"bkmonitor:system:cpu_detail:usage"},
								TableID:         "system.cpu_detail",
								DataLabel:       "cpu_detail",
								VmRt:            "100147_ieod_system_cpu_detail_raw",
								VmConditionNum:  3,
								VmCondition:     `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
								StorageID:       "2",
								StorageType:     consul.VictoriaMetricsStorageType,
								Field:           "usage",
								MeasurementType: redis.BKTraditionalMeasurement,
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
											IsForceEq:     true,
										},
									},
								},
							},
						},
						MetricName:    "usage",
						ReferenceName: "a",
					},
				},
			},
		},
		"vm 聚合查询验证 - 3": {
			ts: &QueryTs{
				QueryList: []*Query{
					{
						DataSource:    BkMonitor,
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
			isDirectQuery: true,
			promql:        `topk(5, sum by (ip, service) (sum_over_time(a[1m])))`,
			expand: &md.VmExpand{
				ResultTableList: []string{"100147_ieod_system_cpu_detail_raw"},
				MetricFilterCondition: map[string]string{
					"a": `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
				},
			},
			ref: md.QueryReference{
				"a": {
					{
						QueryList: md.QueryList{
							{
								DataSource:      BkMonitor,
								Condition:       "bk_biz_id='2'",
								Timezone:        "UTC",
								Fields:          []string{"usage"},
								MetricNames:     []string{"bkmonitor:system:cpu_detail:usage"},
								TableID:         "system.cpu_detail",
								DataLabel:       "cpu_detail",
								VmRt:            "100147_ieod_system_cpu_detail_raw",
								VmConditionNum:  3,
								VmCondition:     `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
								StorageID:       "2",
								StorageType:     consul.VictoriaMetricsStorageType,
								Field:           "usage",
								MeasurementType: redis.BKTraditionalMeasurement,
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
											IsForceEq:     true,
										},
									},
								},
								Aggregates: md.Aggregates{
									{
										Name:       "sum",
										Dimensions: []string{"ip", "service"},
										Window:     time.Minute,
									},
								},
							},
						},
						MetricName:    "usage",
						ReferenceName: "a",
					},
				},
			},
		},
		"非 vm 聚合查询验证 - 1": {
			ts: &QueryTs{
				QueryList: []*Query{
					{
						DataSource:    BkMonitor,
						TableID:       "system.cpu_summary",
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
			isDirectQuery: false,
			promql:        `sum by (ip) (last_over_time(a[1m]))`,
			ref: md.QueryReference{
				"a": {
					{
						QueryList: md.QueryList{
							{
								DataSource:      BkMonitor,
								Condition:       "bk_biz_id='2'",
								Timezone:        "UTC",
								Fields:          []string{"usage"},
								MetricNames:     []string{"bkmonitor:system:cpu_summary:usage"},
								TableID:         "system.cpu_summary",
								DataLabel:       "cpu_summary",
								VmConditionNum:  2,
								VmCondition:     `bk_biz_id="2", __name__="usage_value"`,
								StorageID:       "2",
								DB:              "system",
								Measurement:     "cpu_summary",
								Measurements:    []string{"cpu_summary"},
								ClusterName:     "default",
								StorageType:     consul.InfluxDBStorageType,
								Field:           "usage",
								MeasurementType: redis.BKTraditionalMeasurement,
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
											IsForceEq:     true,
										},
									},
								},
								Aggregates: md.Aggregates{
									{
										Name:       "count",
										Dimensions: []string{"ip"},
										Window:     time.Minute,
									},
								},
							},
						},
						MetricName:    "usage",
						ReferenceName: "a",
					},
				},
			},
		},
		"非 vm 聚合查询验证 - 2": {
			ts: &QueryTs{
				QueryList: []*Query{
					{
						TableID:       "system.cpu_summary",
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
			isDirectQuery: false,
			promql:        `sum by (ip) (increase(a[1m]))`,
			ref: md.QueryReference{
				"a": {
					{
						QueryList: md.QueryList{
							{
								DataSource:      BkMonitor,
								Condition:       "bk_biz_id='2'",
								Timezone:        "UTC",
								Fields:          []string{"usage"},
								MetricNames:     []string{"bkmonitor:system:cpu_summary:usage"},
								TableID:         "system.cpu_summary",
								DataLabel:       "cpu_summary",
								DB:              "system",
								Measurement:     "cpu_summary",
								Measurements:    []string{"cpu_summary"},
								ClusterName:     "default",
								VmConditionNum:  2,
								VmCondition:     `bk_biz_id="2", __name__="usage_value"`,
								StorageID:       "2",
								StorageType:     consul.InfluxDBStorageType,
								Field:           "usage",
								MeasurementType: redis.BKTraditionalMeasurement,
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
											IsForceEq:     true,
										},
									},
								},
							},
						},
						MetricName:    "usage",
						ReferenceName: "a",
					},
				},
			},
		},
		"非 vm 聚合查询验证 - 3": {
			ts: &QueryTs{
				QueryList: []*Query{
					{
						DataSource:    BkMonitor,
						TableID:       "system.cpu_summary",
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
			isDirectQuery: false,
			promql:        `topk(1, sum by (ip) (last_over_time(a[1m])))`,
			ref: md.QueryReference{
				"a": {
					{
						QueryList: md.QueryList{
							{
								DataSource:      BkMonitor,
								Condition:       "bk_biz_id='2'",
								Timezone:        "UTC",
								Fields:          []string{"usage"},
								MetricNames:     []string{"bkmonitor:system:cpu_summary:usage"},
								TableID:         "system.cpu_summary",
								DB:              "system",
								Measurement:     "cpu_summary",
								Measurements:    []string{"cpu_summary"},
								ClusterName:     "default",
								VmConditionNum:  2,
								VmCondition:     `bk_biz_id="2", __name__="usage_value"`,
								StorageID:       "2",
								StorageType:     consul.InfluxDBStorageType,
								Field:           "usage",
								MeasurementType: redis.BKTraditionalMeasurement,
								DataLabel:       "cpu_summary",
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
											IsForceEq:     true,
										},
									},
								},
								Aggregates: md.Aggregates{
									{
										Name:       "sum",
										Dimensions: []string{"ip"},
										Window:     time.Minute,
									},
								},
							},
						},
						MetricName:    "usage",
						ReferenceName: "a",
					},
				},
			},
		},
		"es 聚合查询验证 - 4": {
			ts: &QueryTs{
				QueryList: []*Query{
					{
						DataSource:    BkLog,
						TableID:       "result_table.es",
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
			isDirectQuery: false,
			promql:        `topk(1, sum by (__ext__bk_46__container) (last_over_time(a[1m])))`,
			ref: md.QueryReference{
				"a": {
					{
						QueryList: md.QueryList{
							{
								DataSource:     BkLog,
								Timezone:       "UTC",
								TableID:        "result_table.es",
								Fields:         []string{"usage"},
								MetricNames:    []string{"bklog:result_table:es:usage"},
								DataLabel:      "es",
								DB:             "es_index",
								VmConditionNum: 1,
								VmCondition:    `__name__="usage_value"`,
								StorageID:      "3",
								StorageIDs: []string{
									"3",
								},
								Field:       "usage",
								StorageType: consul.ElasticsearchStorageType,
								Aggregates: md.Aggregates{
									{
										Name:       "sum",
										Dimensions: []string{"__ext.container"},
										Window:     time.Minute,
									},
								},
							},
						},
						MetricName:    "usage",
						ReferenceName: "a",
					},
				},
			},
		},
		"es 高亮查询": {
			ts: &QueryTs{
				QueryList: []*Query{
					{
						DataSource:    BkLog,
						TableID:       "result_table.es",
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
				HighLight: &md.HighLight{
					Enable:            true,
					MaxAnalyzedOffset: 100,
				},
			},
			isDirectQuery: false,
			promql:        `topk(1, sum by (__ext__bk_46__container) (last_over_time(a[1m])))`,
			ref: md.QueryReference{
				"a": {
					{
						QueryList: md.QueryList{
							{
								DataSource:     BkLog,
								Timezone:       "UTC",
								TableID:        "result_table.es",
								DataLabel:      "es",
								DB:             "es_index",
								VmConditionNum: 1,
								VmCondition:    `__name__="usage_value"`,
								StorageID:      "3",
								StorageIDs: []string{
									"3",
								},
								Field:       "usage",
								Fields:      []string{"usage"},
								MetricNames: []string{"bklog:result_table:es:usage"},
								StorageType: consul.ElasticsearchStorageType,
								Aggregates: md.Aggregates{
									{
										Name:       "sum",
										Dimensions: []string{"__ext.container"},
										Window:     time.Minute,
									},
								},
							},
						},
						MetricName:    "usage",
						ReferenceName: "a",
					},
				},
			},
		},

		"influxdb bk exporter 类型聚合查询": {
			ts: &QueryTs{
				QueryList: []*Query{
					{
						DataSource:    BkMonitor,
						TableID:       "bk.exporter",
						FieldName:     ".*",
						IsRegexp:      true,
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
			isDirectQuery: false,
			promql:        `sum by (ip) (last_over_time({__name__=~"a"}[1m]))`,
			ref: md.QueryReference{
				"a": {
					{
						QueryList: md.QueryList{
							{
								DataSource:      BkMonitor,
								Condition:       "metric_name =~ /.*/",
								Timezone:        "UTC",
								Fields:          []string{"metric_value"},
								TableID:         "bk.exporter",
								VmConditionNum:  1,
								VmCondition:     `__name__=~".*_value"`,
								StorageID:       "2",
								DB:              "bk",
								Measurement:     "exporter",
								Measurements:    []string{"exporter"},
								ClusterName:     "default",
								StorageType:     consul.InfluxDBStorageType,
								Field:           "metric_value",
								MeasurementType: redis.BkExporter,
								Aggregates: md.Aggregates{
									{
										Name:       "count",
										Dimensions: []string{"ip"},
										Window:     time.Minute,
									},
								},
							},
						},
						MetricName:    ".*",
						ReferenceName: "a",
					},
				},
			},
		},
		"influxdb bk standard_v2_time_series 类型聚合查询": {
			ts: &QueryTs{
				QueryList: []*Query{
					{
						DataSource:    BkMonitor,
						TableID:       "bk.standard_v2_time_series",
						FieldName:     ".*",
						IsRegexp:      true,
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
			isDirectQuery: false,
			promql:        `sum by (ip) (last_over_time({__name__=~"a"}[1m]))`,
			ref: md.QueryReference{
				"a": {
					{
						QueryList: md.QueryList{
							{
								DataSource:      BkMonitor,
								Condition:       "",
								Timezone:        "UTC",
								Fields:          []string{"usage", "free"},
								MetricNames:     []string{"bkmonitor:bk:standard_v2_time_series:usage", "bkmonitor:bk:standard_v2_time_series:free"},
								TableID:         "bk.standard_v2_time_series",
								VmConditionNum:  1,
								VmCondition:     `__name__=~".*_value"`,
								StorageID:       "2",
								DB:              "bk",
								Measurement:     "standard_v2_time_series",
								Measurements:    []string{"standard_v2_time_series"},
								ClusterName:     "default",
								StorageType:     consul.InfluxDBStorageType,
								MeasurementType: redis.BkStandardV2TimeSeries,
								Field:           ".*",
								Aggregates: md.Aggregates{
									{
										Name:       "count",
										Dimensions: []string{"ip"},
										Window:     time.Minute,
									},
								},
							},
						},
						MetricName:    ".*",
						ReferenceName: "a",
					},
				},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var (
				ref      md.QueryReference
				vmExpand *md.VmExpand
			)
			ctx = md.InitHashID(ctx)

			md.SetUser(ctx, &md.User{SpaceUID: influxdb.SpaceUid})
			ref, err := tc.ts.ToQueryReference(ctx)
			assert.Nil(t, err)
			assert.Equal(t, tc.ref, ref)

			vmExpand = ref.ToVmExpand(ctx)
			isDirectQuery := md.GetQueryParams(ctx).IsDirectQuery()

			assert.Equal(t, tc.isDirectQuery, isDirectQuery)
			assert.Equal(t, tc.expand, vmExpand)

			promExprOpt := &PromExprOption{
				IgnoreTimeAggregationEnable: !isDirectQuery,
			}

			promql, _ := tc.ts.ToPromExpr(ctx, promExprOpt)
			assert.Equal(t, tc.promql, promql.String())
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
				Timezone: "Asia/Shanghai",
			},
			aggs: md.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"dim-1"},
					Window:     time.Minute,
					TimeZone:   "Asia/Shanghai",
				},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			aggs, err := c.query.Aggregates()
			assert.Nil(t, err)
			assert.Equal(t, c.aggs, aggs)
		})
	}
}

func TestGetMaxWindow(t *testing.T) {
	tests := []struct {
		name        string
		queryList   []*Query
		expected    time.Duration
		expectError bool
	}{
		{
			name: "Normal case with multiple windows",
			queryList: []*Query{
				{
					AggregateMethodList: []AggregateMethod{
						{Window: "5m"},
						{Window: "10m"},
					},
				},
				{
					AggregateMethodList: []AggregateMethod{
						{Window: "15m"},
						{Window: "20m"},
					},
				},
			},
			expected:    20 * time.Minute,
			expectError: false,
		},
		{
			name:        "Empty QueryList",
			queryList:   []*Query{},
			expected:    0,
			expectError: false,
		},
		{
			name: "Invalid Window",
			queryList: []*Query{
				{
					AggregateMethodList: []AggregateMethod{
						{Window: "invalid"},
					},
				},
			},
			expected:    0,
			expectError: true,
		},
		{
			name: "Multiple Windows with one invalid",
			queryList: []*Query{
				{
					AggregateMethodList: []AggregateMethod{
						{Window: "5m"},
						{Window: "invalid"},
					},
				},
				{
					AggregateMethodList: []AggregateMethod{
						{Window: "15m"},
						{Window: "20m"},
					},
				},
			},
			expected:    0,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &QueryTs{
				QueryList: tt.queryList,
			}
			result, err := q.GetMaxWindow()
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestOrderBy(t *testing.T) {
	data := []map[string]any{
		{
			"__data_label": "bkdata_index_set_627506",
			"log_count":    292,
			"minute1":      "202507221020",
		},
		{
			"__data_label": "bkdata_index_set_627506",
			"log_count":    1909,
			"minute1":      "202507221019",
		},
		{
			"__data_label": "bkdata_index_set_627506",
			"log_count":    499,
			"minute1":      "202507221018",
		},
	}

	queryTs := &QueryTs{OrderBy: OrderBy{
		"-gseIndex",
		"-iterationIndex",
	}}

	queryTs.OrderBy.Orders().SortSliceList(data)

	assert.Equal(t, []map[string]any{
		{
			"__data_label": "bkdata_index_set_627506",
			"log_count":    292,
			"minute1":      "202507221020",
		},
		{
			"__data_label": "bkdata_index_set_627506",
			"log_count":    1909,
			"minute1":      "202507221019",
		},
		{
			"__data_label": "bkdata_index_set_627506",
			"log_count":    499,
			"minute1":      "202507221018",
		},
	}, data)
}
