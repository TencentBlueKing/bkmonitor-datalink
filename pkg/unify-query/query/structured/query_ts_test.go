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
						DataSource:     BkMonitor,
						TableID:        tableID,
						DB:             db,
						Measurement:    field,
						StorageID:      storageID,
						StorageType:    consul.InfluxDBStorageType,
						MetricName:     field,
						ClusterName:    clusterName,
						Field:          promql.StaticField,
						Fields:         []string{promql.StaticField},
						Measurements:   []string{field},
						Timezone:       "UTC",
						VmCondition:    `__name__="kube_pod_info_value"`,
						VmConditionNum: 1,
						DataLabel:      "influxdb",
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
						DataSource:     BkMonitor,
						TableID:        tableID,
						DB:             db,
						StorageType:    consul.InfluxDBStorageType,
						StorageID:      storageID,
						MetricName:     field,
						ClusterName:    clusterName,
						Field:          promql.StaticField,
						Fields:         []string{promql.StaticField},
						Measurement:    field,
						Measurements:   []string{field},
						Timezone:       "UTC",
						VmCondition:    `__name__="kube_pod_info_value"`,
						VmConditionNum: 1,
						DataLabel:      "influxdb",
					},
					{
						DataSource:     BkMonitor,
						StorageType:    consul.VictoriaMetricsStorageType,
						StorageID:      "2",
						TableID:        "result_table.vm",
						MetricName:     field,
						VmRt:           "2_bcs_prom_computation_result_table",
						Measurement:    field,
						Measurements:   []string{field},
						Field:          promql.StaticField,
						Fields:         []string{promql.StaticField},
						Timezone:       "UTC",
						VmCondition:    `result_table_id="2_bcs_prom_computation_result_table", __name__="kube_pod_info_value"`,
						VmConditionNum: 2,
						DataLabel:      "vm",
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
						DataSource:     BkMonitor,
						TableID:        tableID,
						DataLabel:      "influxdb",
						DB:             db,
						StorageType:    consul.InfluxDBStorageType,
						StorageID:      storageID,
						MetricName:     field,
						ClusterName:    clusterName,
						Field:          promql.StaticField,
						Fields:         []string{promql.StaticField},
						Measurement:    field,
						Measurements:   []string{field},
						Timezone:       "UTC",
						VmCondition:    `__name__="kube_pod_info_value"`,
						VmConditionNum: 1,
					},
					{
						DataSource:     BkMonitor,
						StorageType:    consul.VictoriaMetricsStorageType,
						StorageID:      "2",
						TableID:        "result_table.vm",
						MetricName:     field,
						VmRt:           "2_bcs_prom_computation_result_table",
						Measurement:    field,
						Measurements:   []string{field},
						Field:          promql.StaticField,
						Fields:         []string{promql.StaticField},
						Timezone:       "UTC",
						VmCondition:    `result_table_id="2_bcs_prom_computation_result_table", __name__="kube_pod_info_value"`,
						VmConditionNum: 2,
						DataLabel:      "vm",
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
						DataSource:     BkMonitor,
						TableID:        tableID,
						DB:             db,
						StorageType:    consul.InfluxDBStorageType,
						StorageID:      storageID,
						MetricName:     "kube_.*",
						ClusterName:    clusterName,
						Field:          promql.StaticField,
						Fields:         []string{promql.StaticField},
						Measurement:    "kube_.*",
						Measurements:   []string{field, field1, "kube_node_status_condition"},
						Timezone:       "UTC",
						VmCondition:    `__name__=~"kube_.*_value"`,
						VmConditionNum: 1,
						DataLabel:      "influxdb",
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
						MetricName:  "kube_.*",
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
								DataSource:     BkMonitor,
								Condition:      "bk_biz_id='2'",
								Timezone:       "UTC",
								Fields:         []string{"usage"},
								TableID:        "system.cpu_detail",
								DataLabel:      "cpu_detail",
								MetricName:     "usage",
								VmRt:           "100147_ieod_system_cpu_detail_raw",
								VmConditionNum: 3,
								VmCondition:    `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
								StorageID:      "2",
								StorageType:    consul.VictoriaMetricsStorageType,
								Field:          "usage",
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
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
								DataSource:     BkMonitor,
								Condition:      "bk_biz_id='2'",
								Timezone:       "UTC",
								Fields:         []string{"usage"},
								TableID:        "system.disk",
								DataLabel:      "disk",
								MetricName:     "usage",
								VmRt:           "100147_ieod_system_disk_raw",
								VmConditionNum: 3,
								VmCondition:    `bk_biz_id="2", result_table_id="100147_ieod_system_disk_raw", __name__="usage_value"`,
								StorageID:      "2",
								StorageType:    consul.VictoriaMetricsStorageType,
								Field:          "usage",
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
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
								DataSource:     BkMonitor,
								Condition:      "bk_biz_id='2'",
								Timezone:       "UTC",
								Fields:         []string{"usage"},
								TableID:        "system.cpu_summary",
								DataLabel:      "cpu_summary",
								MetricName:     "usage",
								ClusterName:    "default",
								DB:             "system",
								Measurement:    "cpu_summary",
								Measurements:   []string{"cpu_summary"},
								VmConditionNum: 2,
								VmCondition:    `bk_biz_id="2", __name__="usage_value"`,
								StorageID:      "2",
								StorageType:    consul.InfluxDBStorageType,
								Field:          "usage",
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
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
								DataSource:     BkMonitor,
								Condition:      "bk_biz_id='2'",
								Timezone:       "UTC",
								Fields:         []string{"usage"},
								TableID:        "system.disk",
								DataLabel:      "disk",
								MetricName:     "usage",
								VmRt:           "100147_ieod_system_disk_raw",
								VmConditionNum: 3,
								VmCondition:    `bk_biz_id="2", result_table_id="100147_ieod_system_disk_raw", __name__="usage_value"`,
								StorageID:      "2",
								StorageType:    consul.VictoriaMetricsStorageType,
								Field:          "usage",
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
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
								DataSource:     BkMonitor,
								Condition:      "bk_biz_id='2'",
								Timezone:       "UTC",
								Fields:         []string{"usage"},
								TableID:        "system.cpu_summary",
								DataLabel:      "cpu_summary",
								MetricName:     "usage",
								DB:             "system",
								Measurement:    "cpu_summary",
								Measurements:   []string{"cpu_summary"},
								ClusterName:    "default",
								VmConditionNum: 2,
								VmCondition:    `bk_biz_id="2", __name__="usage_value"`,
								StorageID:      "2",
								StorageType:    consul.InfluxDBStorageType,
								Field:          "usage",
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
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
								DataSource:     BkMonitor,
								Condition:      "bk_obj_id!='0' and bk_biz_id='2'",
								Timezone:       "UTC",
								Fields:         []string{"usage"},
								TableID:        "system.cpu_detail",
								DataLabel:      "cpu_detail",
								MetricName:     "usage",
								VmRt:           "100147_ieod_system_cpu_detail_cmdb",
								VmConditionNum: 4,
								VmCondition:    `bk_biz_id="2", bk_obj_id!="0", result_table_id="100147_ieod_system_cpu_detail_cmdb", __name__="usage_value"`,
								StorageID:      "2",
								StorageType:    consul.VictoriaMetricsStorageType,
								Field:          "usage",
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
										},
										{
											DimensionName: "bk_obj_id",
											Operator:      Ncontains,
											Value:         []string{"0"},
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
								DataSource:     BkMonitor,
								Condition:      "bk_biz_id='2'",
								Timezone:       "UTC",
								Fields:         []string{"usage"},
								TableID:        "system.cpu_detail",
								DataLabel:      "cpu_detail",
								MetricName:     "usage",
								VmRt:           "100147_ieod_system_cpu_detail_cmdb",
								VmConditionNum: 3,
								VmCondition:    `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_cmdb", __name__="usage_value"`,
								StorageID:      "2",
								StorageType:    consul.VictoriaMetricsStorageType,
								Field:          "usage",
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
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
								DataSource:     BkMonitor,
								Condition:      "bk_biz_id='2'",
								Timezone:       "UTC",
								Fields:         []string{"usage"},
								TableID:        "system.cpu_detail",
								DataLabel:      "cpu_detail",
								MetricName:     "usage",
								VmRt:           "100147_ieod_system_cpu_detail_raw",
								VmConditionNum: 3,
								VmCondition:    `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
								StorageID:      "2",
								StorageType:    consul.VictoriaMetricsStorageType,
								Field:          "usage",
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
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
								DataSource:     BkMonitor,
								Condition:      "bk_biz_id='2'",
								Timezone:       "UTC",
								Fields:         []string{"usage"},
								TableID:        "system.cpu_detail",
								DataLabel:      "cpu_detail",
								MetricName:     "usage",
								VmRt:           "100147_ieod_system_cpu_detail_raw",
								VmConditionNum: 3,
								VmCondition:    `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
								StorageID:      "2",
								StorageType:    consul.VictoriaMetricsStorageType,
								Field:          "usage",
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
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
								DataSource:     BkMonitor,
								Condition:      "bk_biz_id='2'",
								Timezone:       "UTC",
								Fields:         []string{"usage"},
								TableID:        "system.cpu_detail",
								DataLabel:      "cpu_detail",
								MetricName:     "usage",
								VmRt:           "100147_ieod_system_cpu_detail_raw",
								VmConditionNum: 3,
								VmCondition:    `bk_biz_id="2", result_table_id="100147_ieod_system_cpu_detail_raw", __name__="usage_value"`,
								StorageID:      "2",
								StorageType:    consul.VictoriaMetricsStorageType,
								Field:          "usage",
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
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
								DataSource:     BkMonitor,
								Condition:      "bk_biz_id='2'",
								Timezone:       "UTC",
								Fields:         []string{"usage"},
								TableID:        "system.cpu_summary",
								DataLabel:      "cpu_summary",
								MetricName:     "usage",
								VmConditionNum: 2,
								VmCondition:    `bk_biz_id="2", __name__="usage_value"`,
								StorageID:      "2",
								DB:             "system",
								Measurement:    "cpu_summary",
								Measurements:   []string{"cpu_summary"},
								ClusterName:    "default",
								StorageType:    consul.InfluxDBStorageType,
								Field:          "usage",
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
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
								DataSource:     BkMonitor,
								Condition:      "bk_biz_id='2'",
								Timezone:       "UTC",
								Fields:         []string{"usage"},
								TableID:        "system.cpu_summary",
								DataLabel:      "cpu_summary",
								MetricName:     "usage",
								DB:             "system",
								Measurement:    "cpu_summary",
								Measurements:   []string{"cpu_summary"},
								ClusterName:    "default",
								VmConditionNum: 2,
								VmCondition:    `bk_biz_id="2", __name__="usage_value"`,
								StorageID:      "2",
								StorageType:    consul.InfluxDBStorageType,
								Field:          "usage",
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
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
								DataSource:     BkMonitor,
								Condition:      "bk_biz_id='2'",
								Timezone:       "UTC",
								Fields:         []string{"usage"},
								TableID:        "system.cpu_summary",
								MetricName:     "usage",
								DB:             "system",
								Measurement:    "cpu_summary",
								Measurements:   []string{"cpu_summary"},
								ClusterName:    "default",
								VmConditionNum: 2,
								VmCondition:    `bk_biz_id="2", __name__="usage_value"`,
								StorageID:      "2",
								StorageType:    consul.InfluxDBStorageType,
								Field:          "usage",
								DataLabel:      "cpu_summary",
								AllConditions: md.AllConditions{
									{
										{
											DimensionName: "bk_biz_id",
											Operator:      ConditionEqual,
											Value:         []string{"2"},
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
								DataLabel:      "es",
								DB:             "es_index",
								MetricName:     "usage",
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
								MetricName:     "usage",
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

// TestQueryTs_LabelMap 测试 LabelMap 函数在各种条件操作符下的行为
func TestQueryTs_LabelMap(t *testing.T) {
	testCases := []struct {
		name     string
		queryTs  *QueryTs
		expected map[string][]string
	}{
		{
			name: "ConditionEqual - 单个值",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "status",
									Value:         []string{"error"},
									Operator:      ConditionEqual,
								},
							},
						},
					},
				},
			},
			expected: map[string][]string{
				"status": {"error"},
			},
		},
		{
			name: "ConditionEqual - 多个值",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "level",
									Value:         []string{"error", "warning", "info"},
									Operator:      ConditionEqual,
								},
							},
						},
					},
				},
			},
			expected: map[string][]string{
				"level": {"error", "warning", "info"},
			},
		},
		{
			name: "ConditionNotEqual - 因为是negative的操作符会被忽略",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "status",
									Value:         []string{"success"},
									Operator:      ConditionNotEqual,
								},
							},
						},
					},
				},
			},
			expected: map[string][]string{},
		},
		{
			name: "ConditionNotContains - 因为是negative的操作符会被忽略",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "message",
									Value:         []string{"debug"},
									Operator:      ConditionNotContains,
								},
							},
						},
					},
				},
			},
			expected: map[string][]string{},
		},
		{
			name: "ConditionContains - 应该被包含",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "content",
									Value:         []string{"keyword"},
									Operator:      ConditionContains,
								},
							},
						},
					},
				},
			},
			expected: map[string][]string{
				"content": {"keyword"},
			},
		},
		{
			name: "ConditionExact - 应该被包含",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "id",
									Value:         []string{"12345"},
									Operator:      ConditionExact,
								},
							},
						},
					},
				},
			},
			expected: map[string][]string{
				"id": {"12345"},
			},
		},
		{
			name: "ConditionRegEqual - 应该被包含",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "pattern",
									Value:         []string{".*error.*"},
									Operator:      ConditionRegEqual,
								},
							},
						},
					},
				},
			},
			expected: map[string][]string{
				"pattern": {".*error.*"},
			},
		},
		{
			name: "ConditionNotRegEqual - 因为是negative的操作符会被忽略",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "exclude_pattern",
									Value:         []string{".*debug.*"},
									Operator:      ConditionNotRegEqual,
								},
							},
						},
					},
				},
			},
			expected: map[string][]string{},
		},
		{
			name: "数值比较操作符 - 应该被包含",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "cpu_usage",
									Value:         []string{"80"},
									Operator:      ConditionGt,
								},
								{
									DimensionName: "memory_usage",
									Value:         []string{"90"},
									Operator:      ConditionGte,
								},
								{
									DimensionName: "disk_usage",
									Value:         []string{"50"},
									Operator:      ConditionLt,
								},
								{
									DimensionName: "network_usage",
									Value:         []string{"60"},
									Operator:      ConditionLte,
								},
							},
						},
					},
				},
			},
			expected: map[string][]string{
				"cpu_usage":     {"80"},
				"memory_usage":  {"90"},
				"disk_usage":    {"50"},
				"network_usage": {"60"},
			},
		},
		{
			name: "QueryString 解析",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						QueryString: "level: error",
					},
				},
			},
			expected: map[string][]string{
				"level": {"error"},
			},
		},
		{
			name: "QueryString 和 Conditions 组合",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						QueryString: "service: web-server",
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "status",
									Value:         []string{"500"},
									Operator:      ConditionEqual,
								},
							},
						},
					},
				},
			},
			expected: map[string][]string{
				"service": {"web-server"},
				"status":  {"500"},
			},
		},
		{
			name: "多个查询组合",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						QueryString: "app: frontend",
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "level",
									Value:         []string{"error"},
									Operator:      ConditionEqual, //会被包含
								},
							},
						},
					},
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "component",
									Value:         []string{"database"},
									Operator:      ConditionNotEqual, // 会被忽略
								},
							},
						},
					},
				},
			},
			expected: map[string][]string{
				"app":   {"frontend"},
				"level": {"error"},
			},
		},
		{
			name: "空值过滤",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "empty_field",
									Value:         []string{""},
									Operator:      ConditionEqual,
								},
								{
									DimensionName: "valid_field",
									Value:         []string{"value"},
									Operator:      ConditionEqual,
								},
							},
						},
					},
				},
			},
			expected: map[string][]string{
				"valid_field": {"value"},
			},
		},
		{
			name: "重复值去重",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "status",
									Value:         []string{"error"},
									Operator:      ConditionEqual,
								},
								{
									DimensionName: "status",
									Value:         []string{"error"},
									Operator:      ConditionNotEqual,
								},
							},
						},
					},
				},
			},
			expected: map[string][]string{
				"status": {"error"},
			},
		},
		{
			name: "复杂 QueryString - 多个字段",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						QueryString: "level: error AND service: web",
					},
				},
			},
			expected: map[string][]string{
				"level":   {"error"},
				"service": {"web"},
			},
		},
		{
			name: "QueryString 带引号",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						QueryString: `message: "error occurred"`,
					},
				},
			},
			expected: map[string][]string{
				"message": {"error occurred"},
			},
		},
		{
			name: "QueryString 带单引号",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						QueryString: `status: 'failed'`,
					},
				},
			},
			expected: map[string][]string{
				"status": {"'failed'"},
			},
		},
		{
			name: "空 QueryString 和空 Conditions",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						QueryString: "",
						Conditions:  Conditions{},
					},
				},
			},
			expected: map[string][]string{},
		},
		{
			name: "全字段匹配",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						QueryString: "test",
						Conditions:  Conditions{},
					},
				},
			},
			expected: map[string][]string{
				"": {"test"},
			},
		},
		{
			name: "通配符 QueryString",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						QueryString: "*",
					},
				},
			},
			expected: map[string][]string{},
		},
		{
			name: "嵌套字段名",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "user.profile.name",
									Value:         []string{"john"},
									Operator:      ConditionEqual,
								},
								{
									DimensionName: "resource.k8s.pod.name",
									Value:         []string{"web-pod-123"},
									Operator:      ConditionContains,
								},
							},
						},
					},
				},
			},
			expected: map[string][]string{
				"user.profile.name":     {"john"},
				"resource.k8s.pod.name": {"web-pod-123"},
			},
		},
		{
			name: "特殊字符在值中",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "url",
									Value:         []string{"https://example.com/api?param=value&other=123"},
									Operator:      ConditionEqual,
								},
								{
									DimensionName: "regex_pattern",
									Value:         []string{"^[a-zA-Z0-9]+$"},
									Operator:      ConditionRegEqual,
								},
							},
						},
					},
				},
			},
			expected: map[string][]string{
				"url":           {"https://example.com/api?param=value&other=123"},
				"regex_pattern": {"^[a-zA-Z0-9]+$"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, _ := tc.queryTs.LabelMap()
			assert.Equal(t, tc.expected, result, "LabelMap result should match expected")
		})
	}
}

// TestQuery_LabelMap 测试 Query.LabelMap 函数（包含 QueryString 和 Conditions 的组合）
func TestQuery_LabelMap(t *testing.T) {
	testCases := []struct {
		name     string
		query    Query
		expected map[string][]string
	}{
		{
			name: "只有 Conditions",
			query: Query{
				Conditions: Conditions{
					FieldList: []ConditionField{
						{
							DimensionName: "status",
							Value:         []string{"error"},
							Operator:      ConditionEqual,
						},
					},
				},
			},
			expected: map[string][]string{
				"status": {"error"},
			},
		},
		{
			name: "只有 QueryString",
			query: Query{
				QueryString: "level:warning",
			},
			expected: map[string][]string{
				"level": {"warning"},
			},
		},
		{
			name: "QueryString 和 Conditions 组合",
			query: Query{
				QueryString: "service:web",
				Conditions: Conditions{
					FieldList: []ConditionField{
						{
							DimensionName: "status",
							Value:         []string{"error"},
							Operator:      ConditionEqual,
						},
					},
				},
			},
			expected: map[string][]string{
				"service": {"web"},
				"status":  {"error"},
			},
		},
		{
			name: "QueryString 和 Conditions 有重复字段",
			query: Query{
				QueryString: "level:error",
				Conditions: Conditions{
					FieldList: []ConditionField{
						{
							DimensionName: "level",
							Value:         []string{"warning"},
							Operator:      ConditionEqual,
						},
					},
				},
			},
			expected: map[string][]string{
				"level": {"warning", "error"},
			},
		},
		{
			name: "QueryString 和 Conditions 有重复字段和值（去重）",
			query: Query{
				QueryString: "level:error",
				Conditions: Conditions{
					FieldList: []ConditionField{
						{
							DimensionName: "level",
							Value:         []string{"error"},
							Operator:      ConditionEqual,
						},
					},
				},
			},
			expected: map[string][]string{
				"level": {"error"},
			},
		},
		{
			name: "复杂 QueryString 和多个 Conditions",
			query: Query{
				QueryString: "service:web AND component:database",
				Conditions: Conditions{
					FieldList: []ConditionField{
						{
							DimensionName: "status",
							Value:         []string{"error", "warning"},
							Operator:      ConditionEqual, // 会被包含
						},
						{
							DimensionName: "region",
							Value:         []string{"us-east-1"},
							Operator:      ConditionNotEqual, // 会被忽略
						},
					},
				},
			},
			expected: map[string][]string{
				"service":   {"web"},
				"component": {"database"},
				"status":    {"error", "warning"},
			},
		},
		{
			name: "空 QueryString 和空 Conditions",
			query: Query{
				QueryString: "",
				Conditions:  Conditions{},
			},
			expected: map[string][]string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.query.LabelMap()
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result, "Query.LabelMap result should match expected")
		})
	}
}
