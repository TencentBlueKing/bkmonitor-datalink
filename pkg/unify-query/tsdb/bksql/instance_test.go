// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sqlExpr"
)

func TestInstance_QueryRaw(t *testing.T) {

	ctx := metadata.InitHashID(context.Background())
	ins := createTestInstance(ctx)

	mock.BkSQL.Set(map[string]any{
		"SELECT *, `login_rate` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` < 1730118889181 AND `thedate` = '20241028' AND `namespace` IN ('gz100', 'bgp2-new') LIMIT 10005":                                                                                          "{\"result\":true,\"message\":\"成功\",\"code\":\"00\",\"data\":{\"result_table_scan_range\":{\"132_lol_new_login_queue_login_1min\":{}},\"cluster\":\"default2\",\"totalRecords\":5,\"resource_use_summary\":{\"cpu_time_mills\":0,\"memory_bytes\":0,\"processed_bytes\":0,\"processed_rows\":0},\"source\":\"\",\"list\":[{\"thedate\":20241028,\"dtEventTime\":\"2024-10-28 20:31:00\",\"dtEventTimeStamp\":1730118660000,\"localTime\":\"2024-10-28 20:32:03\",\"_startTime_\":\"2024-10-28 20:31:00\",\"_endTime_\":\"2024-10-28 20:32:00\",\"namespace\":\"gz100\",\"login_rate\":269.0,\"_value_\":269.0,\"_timestamp_\":1730118660000},{\"thedate\":20241028,\"dtEventTime\":\"2024-10-28 20:28:00\",\"dtEventTimeStamp\":1730118480000,\"localTime\":\"2024-10-28 20:29:03\",\"_startTime_\":\"2024-10-28 20:28:00\",\"_endTime_\":\"2024-10-28 20:29:00\",\"namespace\":\"gz100\",\"login_rate\":271.0,\"_value_\":271.0,\"_timestamp_\":1730118480000},{\"thedate\":20241028,\"dtEventTime\":\"2024-10-28 20:29:00\",\"dtEventTimeStamp\":1730118540000,\"localTime\":\"2024-10-28 20:30:02\",\"_startTime_\":\"2024-10-28 20:29:00\",\"_endTime_\":\"2024-10-28 20:30:00\",\"namespace\":\"gz100\",\"login_rate\":267.0,\"_value_\":267.0,\"_timestamp_\":1730118540000},{\"thedate\":20241028,\"dtEventTime\":\"2024-10-28 20:30:00\",\"dtEventTimeStamp\":1730118600000,\"localTime\":\"2024-10-28 20:31:04\",\"_startTime_\":\"2024-10-28 20:30:00\",\"_endTime_\":\"2024-10-28 20:31:00\",\"namespace\":\"gz100\",\"login_rate\":274.0,\"_value_\":274.0,\"_timestamp_\":1730118600000},{\"thedate\":20241028,\"dtEventTime\":\"2024-10-28 20:27:00\",\"dtEventTimeStamp\":1730118420000,\"localTime\":\"2024-10-28 20:28:03\",\"_startTime_\":\"2024-10-28 20:27:00\",\"_endTime_\":\"2024-10-28 20:28:00\",\"namespace\":\"gz100\",\"login_rate\":279.0,\"_value_\":279.0,\"_timestamp_\":1730118420000}],\"select_fields_order\":[\"thedate\",\"dtEventTime\",\"dtEventTimeStamp\",\"localTime\",\"_startTime_\",\"_endTime_\",\"namespace\",\"login_rate\",\"_value_\",\"_timestamp_\"],\"sql\":\"SELECT *, `login_rate` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM mapleleaf_132.lol_new_login_queue_login_1min_132 WHERE ((`dtEventTimeStamp` >= 1730118415782) AND (`dtEventTimeStamp` < 1730118715782)) AND `namespace` IN ('gz100', 'bgp2-new') LIMIT 10005\",\"total_record_size\":5832,\"timetaken\":0.251,\"bksql_call_elapsed_time\":0,\"device\":\"tspider\",\"result_table_ids\":[\"132_lol_new_login_queue_login_1min\"]},\"errors\":null,\"trace_id\":\"c083ca92cee435138f9076e1c1f6faeb\",\"span_id\":\"735f314a259a981a\"}",
		"SELECT `namespace`, COUNT(`login_rate`) AS `_value_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` < 1730118889181 AND `thedate` = '20241028' GROUP BY `namespace` LIMIT 10005":                                                                                                                                  "{\"result\":true,\"message\":\"成功\",\"code\":\"00\",\"data\":{\"result_table_scan_range\":{\"132_lol_new_login_queue_login_1min\":{}},\"cluster\":\"default2\",\"totalRecords\":11,\"resource_use_summary\":{\"cpu_time_mills\":0,\"memory_bytes\":0,\"processed_bytes\":0,\"processed_rows\":0},\"source\":\"\",\"list\":[{\"namespace\":\"bgp2\",\"_value_\":5},{\"namespace\":\"cq100\",\"_value_\":5},{\"namespace\":\"gz100\",\"_value_\":5},{\"namespace\":\"hn0-new\",\"_value_\":5},{\"namespace\":\"hn1\",\"_value_\":5},{\"namespace\":\"hn10\",\"_value_\":5},{\"namespace\":\"nj100\",\"_value_\":5},{\"namespace\":\"njloadtest\",\"_value_\":5},{\"namespace\":\"pbe\",\"_value_\":5},{\"namespace\":\"tj100\",\"_value_\":5},{\"namespace\":\"tj101\",\"_value_\":5}],\"select_fields_order\":[\"namespace\",\"_value_\"],\"sql\":\"SELECT `namespace`, COUNT(`login_rate`) AS `_value_` FROM mapleleaf_132.lol_new_login_queue_login_1min_132 WHERE (`dtEventTimeStamp` >= 1730118589181) AND (`dtEventTimeStamp` < 1730118889181) GROUP BY `namespace` LIMIT 10005\",\"total_record_size\":3216,\"timetaken\":0.24,\"bksql_call_elapsed_time\":0,\"device\":\"tspider\",\"result_table_ids\":[\"132_lol_new_login_queue_login_1min\"]},\"errors\":null,\"trace_id\":\"5c70526f101a00531ef8fbaadc783693\",\"span_id\":\"2a31369ceb208970\"}",
		"SELECT COUNT(`login_rate`) AS `_value_`, MAX((`dtEventTimeStamp` - (`dtEventTimeStamp` % 60000))) AS `_timestamp_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` < 1730118889181 AND `thedate` = '20241028' GROUP BY (`dtEventTimeStamp` - (`dtEventTimeStamp` % 60000)) ORDER BY `_timestamp_` ASC LIMIT 10005": "{\"result\":true,\"message\":\"成功\",\"code\":\"00\",\"data\":{\"result_table_scan_range\":{\"132_lol_new_login_queue_login_1min\":{}},\"cluster\":\"default2\",\"totalRecords\":5,\"resource_use_summary\":{\"cpu_time_mills\":0,\"memory_bytes\":0,\"processed_bytes\":0,\"processed_rows\":0},\"source\":\"\",\"list\":[{\"_value_\":11,\"_timestamp_\":1730118600000},{\"_value_\":11,\"_timestamp_\":1730118660000},{\"_value_\":11,\"_timestamp_\":1730118720000},{\"_value_\":11,\"_timestamp_\":1730118780000},{\"_value_\":11,\"_timestamp_\":1730118840000}],\"select_fields_order\":[\"_value_\",\"_timestamp_\"],\"sql\":\"SELECT COUNT(`login_rate`) AS `_value_`, MAX(`dtEventTimeStamp` - (`dtEventTimeStamp` % 60000)) AS `_timestamp_` FROM mapleleaf_132.lol_new_login_queue_login_1min_132 WHERE (`dtEventTimeStamp` >= 1730118589181) AND (`dtEventTimeStamp` < 1730118889181) GROUP BY `dtEventTimeStamp` - (`dtEventTimeStamp` % 60000) ORDER BY `_timestamp_` LIMIT 10005\",\"total_record_size\":1424,\"timetaken\":0.231,\"bksql_call_elapsed_time\":0,\"device\":\"tspider\",\"result_table_ids\":[\"132_lol_new_login_queue_login_1min\"]},\"errors\":null,\"trace_id\":\"127866cb51f85a4a7f620eb0e66588b1\",\"span_id\":\"578f26767bbb78c8\"}",
	})

	end := time.UnixMilli(1730118889181)
	start := time.UnixMilli(1730118589181)

	datasource := "bkdata"
	db := "132_lol_new_login_queue_login_1min"
	field := "login_rate"
	tableID := db + ".default"

	for name, c := range map[string]struct {
		query    *metadata.Query
		expected string
	}{
		"query with in": {
			query: &metadata.Query{
				DataSource:     datasource,
				TableID:        tableID,
				DB:             db,
				DataLabel:      db,
				MetricName:     field,
				BkSqlCondition: "`namespace` IN ('gz100', 'bgp2\\-new')",
				OffsetInfo:     metadata.OffSetInfo{Limit: 10},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"gz100", "bgp2-new"},
						},
					},
				},
			},
			expected: `[{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_endTime_":"2024-10-28 20:32:00","_startTime_":"2024-10-28 20:31:00","_timestamp_":1730118660000,"_value_":269,"dtEventTime":"2024-10-28 20:31:00","dtEventTimeStamp":1730118660000,"localTime":"2024-10-28 20:32:03","login_rate":269,"namespace":"gz100","thedate":20241028},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_endTime_":"2024-10-28 20:29:00","_startTime_":"2024-10-28 20:28:00","_timestamp_":1730118480000,"_value_":271,"dtEventTime":"2024-10-28 20:28:00","dtEventTimeStamp":1730118480000,"localTime":"2024-10-28 20:29:03","login_rate":271,"namespace":"gz100","thedate":20241028},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_endTime_":"2024-10-28 20:30:00","_startTime_":"2024-10-28 20:29:00","_timestamp_":1730118540000,"_value_":267,"dtEventTime":"2024-10-28 20:29:00","dtEventTimeStamp":1730118540000,"localTime":"2024-10-28 20:30:02","login_rate":267,"namespace":"gz100","thedate":20241028},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_endTime_":"2024-10-28 20:31:00","_startTime_":"2024-10-28 20:30:00","_timestamp_":1730118600000,"_value_":274,"dtEventTime":"2024-10-28 20:30:00","dtEventTimeStamp":1730118600000,"localTime":"2024-10-28 20:31:04","login_rate":274,"namespace":"gz100","thedate":20241028},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_endTime_":"2024-10-28 20:28:00","_startTime_":"2024-10-28 20:27:00","_timestamp_":1730118420000,"_value_":279,"dtEventTime":"2024-10-28 20:27:00","dtEventTimeStamp":1730118420000,"localTime":"2024-10-28 20:28:03","login_rate":279,"namespace":"gz100","thedate":20241028}]`,
		},
		"count by namespace": {
			query: &metadata.Query{
				DataSource: datasource,
				TableID:    tableID,
				DB:         db,
				MetricName: field,
				DataLabel:  db,
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"namespace"},
					},
				},
			},
			expected: `[{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_value_":5,"namespace":"bgp2"},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_value_":5,"namespace":"cq100"},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_value_":5,"namespace":"gz100"},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_value_":5,"namespace":"hn0-new"},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_value_":5,"namespace":"hn1"},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_value_":5,"namespace":"hn10"},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_value_":5,"namespace":"nj100"},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_value_":5,"namespace":"njloadtest"},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_value_":5,"namespace":"pbe"},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_value_":5,"namespace":"tj100"},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_value_":5,"namespace":"tj101"}]`,
		},
		"count with 1m": {
			query: &metadata.Query{
				DataSource: datasource,
				TableID:    tableID,
				DB:         db,
				MetricName: field,
				DataLabel:  db,
				Aggregates: metadata.Aggregates{
					{
						Name:   "count",
						Window: time.Minute,
					},
				},
			},
			expected: `[{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_timestamp_":1730118600000,"_value_":11},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_timestamp_":1730118660000,"_value_":11},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_timestamp_":1730118720000,"_value_":11},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_timestamp_":1730118780000,"_value_":11},{"__data_label":"132_lol_new_login_queue_login_1min","__index":"132_lol_new_login_queue_login_1min","__result_table":"132_lol_new_login_queue_login_1min.default","_timestamp_":1730118840000,"_value_":11}]`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			if c.query.DB == "" {
				c.query.DB = db
			}
			if c.query.Field == "" {
				c.query.Field = field
			}

			dataCh := make(chan map[string]any)

			go func() {
				defer func() {
					close(dataCh)
				}()

				_, err := ins.QueryRawData(ctx, c.query, start, end, dataCh)
				assert.Nil(t, err)
			}()

			list := make([]map[string]any, 0)
			for d := range dataCh {
				list = append(list, d)
			}

			actual, err := json.Marshal(list)
			assert.Nil(t, err)

			assert.Equal(t, c.expected, string(actual))
		})
	}
}

func TestInstance_bkSql(t *testing.T) {
	mock.Init()

	start := time.UnixMilli(1718189940000)
	end := time.UnixMilli(1718193555000)

	testCases := []struct {
		start time.Time
		end   time.Time
		query *metadata.Query

		expected string
	}{
		{
			query: &metadata.Query{
				DB:             "132_lol_new_login_queue_login_1min",
				Field:          "login_rate",
				BkSqlCondition: "`namespace` IN ('bgp2-new', 'gz100')",
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"namespace"},
						Window:     time.Second * 15,
					},
				},
				AllConditions: metadata.AllConditions{
					[]metadata.ConditionField{
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionContains,
							Value:         []string{"bgp2-new", "gz100"},
						},
					},
				},
			},
			expected: "SELECT `namespace`, COUNT(`login_rate`) AS `_value_`, MAX((`dtEventTimeStamp` - (`dtEventTimeStamp` % 15000))) AS `_timestamp_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `thedate` = '20240612' AND `namespace` IN ('bgp2-new', 'gz100') GROUP BY `namespace`, (`dtEventTimeStamp` - (`dtEventTimeStamp` % 15000)) ORDER BY `_timestamp_` ASC",
		},
		{
			query: &metadata.Query{
				DB:    "132_lol_new_login_queue_login_1min",
				Field: "login_rate",
				AllConditions: metadata.AllConditions{
					[]metadata.ConditionField{
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test", "test2"},
							IsWildcard:    true,
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test", "test2"},
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test"},
							IsWildcard:    true,
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test"},
						},
					},
				},
				BkSqlCondition: "(`namespace` NOT LIKE '%test%' AND `namespace` NOT LIKE '%test2%') AND `namespace` NOT IN ('test', 'test2') AND (`namespace` NOT LIKE '%test%') AND (`namespace` != 'test')",
			},
			expected: "SELECT *, `login_rate` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `thedate` = '20240612' AND (`namespace` NOT LIKE '%test%' AND `namespace` NOT LIKE '%test2%') AND `namespace` NOT IN ('test', 'test2') AND (`namespace` NOT LIKE '%test%') AND (`namespace` != 'test')",
		},
		{
			query: &metadata.Query{
				DB:          "132_lol_new_login_queue_login_1min",
				Measurement: sqlExpr.Doris,
				Field:       "login_rate",
				AllConditions: metadata.AllConditions{
					[]metadata.ConditionField{
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test", "test2"},
							IsWildcard:    true,
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test", "test2"},
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test"},
							IsWildcard:    true,
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test"},
						},
						{
							DimensionName: "text",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test", "test2"},
							IsWildcard:    true,
						},
						{
							DimensionName: "text",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test", "test2"},
						},
						{
							DimensionName: "text",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test"},
							IsWildcard:    true,
						},
						{
							DimensionName: "text",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test"},
						},
					},
				},
				BkSqlCondition: "(`namespace` NOT LIKE '%test%' AND `namespace` NOT LIKE '%test2%') AND `namespace` NOT IN ('test', 'test2') AND (`namespace` NOT LIKE '%test%') AND (`namespace` != 'test') AND (`text` NOT LIKE '%test%' AND `text` NOT LIKE '%test2%') AND (`text` NOT MATCH_PHRASE_PREFIX 'test' AND `text` NOT MATCH_PHRASE_PREFIX 'test2') AND (`text` NOT LIKE '%test%') AND (`text` NOT MATCH_PHRASE_PREFIX 'test')",
			},
			expected: "SELECT *, `login_rate` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `132_lol_new_login_queue_login_1min`.doris WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `thedate` = '20240612' AND (`namespace` NOT LIKE '%test%' AND `namespace` NOT LIKE '%test2%') AND `namespace` NOT IN ('test', 'test2') AND (`namespace` NOT LIKE '%test%') AND (`namespace` != 'test') AND (`text` NOT LIKE '%test%' AND `text` NOT LIKE '%test2%') AND (`text` NOT MATCH_PHRASE_PREFIX 'test' AND `text` NOT MATCH_PHRASE_PREFIX 'test2') AND (`text` NOT LIKE '%test%') AND (`text` NOT MATCH_PHRASE_PREFIX 'test')",
		},
		{
			query: &metadata.Query{
				DB:    "132_lol_new_login_queue_login_1min",
				Field: "login_rate",
				AllConditions: metadata.AllConditions{
					[]metadata.ConditionField{
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionContains,
							Value:         []string{"test", "test2"},
							IsWildcard:    true,
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionContains,
							Value:         []string{"test", "test2"},
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionContains,
							Value:         []string{"test"},
							IsWildcard:    true,
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionContains,
							Value:         []string{"test"},
						},
					},
				},
				BkSqlCondition: "(`namespace` LIKE '%test%' OR `namespace` LIKE '%test2%') AND `namespace` IN ('test', 'test2') AND (`namespace` LIKE '%test%') AND (`namespace` = 'test')",
			},
			expected: "SELECT *, `login_rate` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `thedate` = '20240612' AND (`namespace` LIKE '%test%' OR `namespace` LIKE '%test2%') AND `namespace` IN ('test', 'test2') AND (`namespace` LIKE '%test%') AND (`namespace` = 'test')",
		},
		{
			query: &metadata.Query{
				DB:    "132_hander_opmon_avg",
				Field: "value",
				Aggregates: metadata.Aggregates{
					{
						Name: "sum",
					},
				},
			},

			expected: "SELECT SUM(`value`) AS `_value_` FROM `132_hander_opmon_avg` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `thedate` = '20240612'",
		},
		{
			query: &metadata.Query{
				DB:          "100133_ieod_logsearch4_errorlog_p",
				Measurement: "doris",
				Field:       "value",
				Size:        5,
			},
			expected: "SELECT *, `value` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `thedate` = '20240612' LIMIT 5",
		},
		{
			query: &metadata.Query{
				DB:          "100133_ieod_logsearch4_errorlog_p",
				Measurement: "doris",
				Field:       "value",
				Orders: map[string]bool{
					"_time": false,
				},
			},
			expected: "SELECT *, `value` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `thedate` = '20240612' ORDER BY `_timestamp_` DESC",
		},
		{
			query: &metadata.Query{
				DB:          "100133_ieod_logsearch4_errorlog_p",
				Measurement: "doris",
				Field:       "gseIndex",
				Aggregates: metadata.Aggregates{
					{
						Name: "count",
						Dimensions: []string{
							"ip",
						},
					},
				},
				Size: 5,
			},

			expected: "SELECT `ip`, COUNT(`gseIndex`) AS `_value_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `thedate` = '20240612' GROUP BY `ip` LIMIT 5",
		},
		{
			start: time.Unix(1733756400, 0),
			end:   time.Unix(1733846399, 0),
			query: &metadata.Query{
				DB:    "101068_MatchFullLinkTimeConsumptionFlow_CostTime",
				Field: "matchstep_start_to_fail_0_100",
				Aggregates: metadata.Aggregates{
					{
						Name: "count",
					},
				},
			},

			expected: "SELECT COUNT(`matchstep_start_to_fail_0_100`) AS `_value_` FROM `101068_MatchFullLinkTimeConsumptionFlow_CostTime` WHERE `dtEventTimeStamp` >= 1733756400000 AND `dtEventTimeStamp` < 1733846399000 AND `thedate` >= '20241209' AND `thedate` <= '20241210'",
		},
		{
			start: time.Unix(1733756400, 0),
			end:   time.Unix(1733846399, 0),
			query: &metadata.Query{
				DB:    "101068_MatchFullLinkTimeConsumptionFlow_CostTime",
				Field: "matchstep_start_to_fail_0_100",
				Aggregates: metadata.Aggregates{
					{
						Name:   "count",
						Window: time.Hour,
					},
				},
			},

			expected: "SELECT COUNT(`matchstep_start_to_fail_0_100`) AS `_value_`, MAX((`dtEventTimeStamp` - (`dtEventTimeStamp` % 3600000))) AS `_timestamp_` FROM `101068_MatchFullLinkTimeConsumptionFlow_CostTime` WHERE `dtEventTimeStamp` >= 1733756400000 AND `dtEventTimeStamp` < 1733846399000 AND `thedate` >= '20241209' AND `thedate` <= '20241210' GROUP BY (`dtEventTimeStamp` - (`dtEventTimeStamp` % 3600000)) ORDER BY `_timestamp_` ASC",
		},
	}

	for i, c := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			if c.start.Unix() <= 0 {
				c.start = start
			}
			if c.end.Unix() <= 0 {
				c.end = end
			}

			fieldsMap := map[string]string{
				"text": sqlExpr.DorisTypeText,
			}

			condition, err := sqlExpr.GetSQLExpr(c.query.Measurement).WithFieldsMap(fieldsMap).ParserAllConditions(c.query.AllConditions)
			assert.Nil(t, err)
			if err == nil {
				assert.Equal(t, c.query.BkSqlCondition, condition)
			}

			fact := bksql.NewQueryFactory(ctx, c.query).WithFieldsMap(fieldsMap).WithRangeTime(c.start, c.end)
			sql, err := fact.SQL()
			assert.Nil(t, err)
			assert.Equal(t, c.expected, sql)
		})
	}
}

func TestInstance_bkSql_EdgeCases(t *testing.T) {
	mock.Init()

	// 基础时间范围
	baseStart := time.UnixMilli(1718189940000)
	baseEnd := time.UnixMilli(1718193555000)

	// 跨天时间范围
	crossDayStart := time.Unix(1733756400, 0) // 2024-12-09 00:00:00
	crossDayEnd := time.Unix(1733846399, 0)   // 2024-12-09 23:59:59

	testCases := []struct {
		name     string
		start    time.Time
		end      time.Time
		query    *metadata.Query
		expected string
		err      error
	}{
		// 测试用例1: 无聚合函数的原始查询
		{
			name: "mysql raw query without aggregation",
			query: &metadata.Query{
				DB:    "test_db",
				Field: "value",
			},
			expected: "SELECT *, `value` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `test_db` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `thedate` = '20240612'",
		},

		// 测试用例2: 多聚合函数组合
		{
			name: "mysql multiple aggregates",
			query: &metadata.Query{
				DB:    "metrics_db",
				Field: "temperature",
				Aggregates: metadata.Aggregates{
					{Name: "max"},
					{Name: "min"},
				},
			},
			expected: "SELECT MAX(`temperature`) AS `_value_`, MIN(`temperature`) AS `_value_` FROM `metrics_db` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `thedate` = '20240612'",
		},

		// 测试用例3: 复杂条件组合
		{
			name: "mysql complex conditions",
			query: &metadata.Query{
				DB:    "security_logs",
				Field: "duration",
				AllConditions: metadata.AllConditions{
					[]metadata.ConditionField{
						{
							DimensionName: "severity",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"high", "critical"},
						},
						{
							DimensionName: "source_ip",
							Operator:      metadata.ConditionNotContains,
							Value:         []string{"192.168.1.1"},
						},
					},
				},
			},
			expected: "SELECT *, `duration` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `security_logs` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `thedate` = '20240612' AND `severity` IN ('high', 'critical') AND `source_ip` != '192.168.1.1'",
		},

		// 测试用例4: 多字段排序
		{
			name: "mysql multiple order fields",
			query: &metadata.Query{
				DB:    "transaction_logs",
				Field: "amount",
				Orders: map[string]bool{
					"timestamp":  true,
					"account_id": false,
				},
			},
			expected: "SELECT *, `amount` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `transaction_logs` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `thedate` = '20240612' ORDER BY `account_id` DESC, `timestamp` ASC",
		},

		// 测试用例5: 特殊字符转义
		{
			name: "mysql special characters in fields",
			query: &metadata.Query{
				DB:          "special_metrics",
				Measurement: "select", // 保留字作为measurement
				Field:       "*",
				Aggregates: metadata.Aggregates{
					{Name: "sum"},
				},
			},
			expected: "SELECT SUM(`*`) AS `_value_` FROM `special_metrics`.select WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `thedate` = '20240612'",
		},

		// 测试用例6: 零窗口时间
		{
			name: "mysql zero window size",
			query: &metadata.Query{
				DB:    "time_series_data",
				Field: "value",
				Aggregates: metadata.Aggregates{
					{
						Name:   "avg",
						Window: 0,
					},
				},
			},
			expected: "SELECT AVG(`value`) AS `_value_` FROM `time_series_data` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `thedate` = '20240612'",
		},

		// 测试用例7: 跨多天的时间范围
		{
			name:  "mysql multi-day time range",
			start: crossDayStart,
			end:   crossDayEnd,
			query: &metadata.Query{
				DB:    "daily_metrics",
				Field: "active_users",
				Aggregates: metadata.Aggregates{
					{Name: "count"},
				},
			},
			expected: "SELECT COUNT(`active_users`) AS `_value_` FROM `daily_metrics` WHERE `dtEventTimeStamp` >= 1733756400000 AND `dtEventTimeStamp` < 1733846399000 AND `thedate` >= '20241209' AND `thedate` <= '20241210'",
		},

		// 测试用例8: 默认处理 object 字段
		{
			name: "mysql default multiple order fields",
			query: &metadata.Query{
				DB:    "transaction_logs",
				Field: "__ext.container_id",
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "__ext.container_id",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"1234567890"},
						},
					},
				},
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"__ext.container_id", "test"},
					},
				},
				Orders: map[string]bool{
					"timestamp":          true,
					"__ext.container_id": false,
				},
			},
			err: fmt.Errorf("query is not support object with __ext.container_id"),
		},

		// 测试用例9: doris 处理 object 字段
		{
			name: "doris default multiple order fields",
			query: &metadata.Query{
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: sqlExpr.Doris,
				Field:       "__ext.container_id",
				Size:        3,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "__ext.io_kubernetes_workload_name",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"bkm-daemonset-worker"},
						},
						{
							DimensionName: "bk_host_id",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"267730"},
						},
					},
				},
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"__ext.io_kubernetes_workload_name", "__ext.io_kubernetes_workload_type"},
					},
				},
				Orders: map[string]bool{
					"__ext.io_kubernetes_workload_name": false,
				},
			},
			start:    time.Unix(1741334700, 0),
			end:      time.Unix(1741335000, 0),
			expected: "SELECT CAST(__ext[\"io_kubernetes_workload_name\"] AS STRING) AS `__ext__bk_46__io_kubernetes_workload_name`, CAST(__ext[\"io_kubernetes_workload_type\"] AS STRING) AS `__ext__bk_46__io_kubernetes_workload_type`, COUNT(CAST(__ext[\"container_id\"] AS STRING)) AS `_value_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1741334700000 AND `dtEventTimeStamp` < 1741335000000 AND `thedate` = '20250307' AND CAST(__ext[\"io_kubernetes_workload_name\"] AS STRING) = 'bkm-daemonset-worker' AND `bk_host_id` = '267730' GROUP BY CAST(__ext[\"io_kubernetes_workload_name\"] AS STRING), CAST(__ext[\"io_kubernetes_workload_type\"] AS STRING) ORDER BY CAST(__ext[\"io_kubernetes_workload_name\"] AS STRING) DESC LIMIT 3",
		},
		// 测试用例10: doris 处理 object 字段 + 时间聚合
		{
			name: "doris default multiple order fields and time aggregate",
			query: &metadata.Query{
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: sqlExpr.Doris,
				Field:       "__ext.container_id",
				Size:        3,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "__ext.io_kubernetes_workload_name",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"bkm-daemonset-worker"},
						},
						{
							DimensionName: "bk_host_id",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"267730"},
						},
					},
				},
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"__ext.io_kubernetes_workload_name", "__ext.io_kubernetes_workload_type"},
						Window:     time.Minute,
					},
				},
				Orders: map[string]bool{
					"__ext.io_kubernetes_workload_name": false,
				},
			},
			start:    time.Unix(1741334700, 0),
			end:      time.Unix(1741335000, 0),
			expected: "SELECT CAST(__ext[\"io_kubernetes_workload_name\"] AS STRING) AS `__ext__bk_46__io_kubernetes_workload_name`, CAST(__ext[\"io_kubernetes_workload_type\"] AS STRING) AS `__ext__bk_46__io_kubernetes_workload_type`, COUNT(CAST(__ext[\"container_id\"] AS STRING)) AS `_value_`, CAST(__shard_key__ / 1000 / 1 AS INT) * 1 * 60 * 1000 AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1741334700000 AND `dtEventTimeStamp` < 1741335000000 AND `thedate` = '20250307' AND CAST(__ext[\"io_kubernetes_workload_name\"] AS STRING) = 'bkm-daemonset-worker' AND `bk_host_id` = '267730' GROUP BY CAST(__ext[\"io_kubernetes_workload_name\"] AS STRING), CAST(__ext[\"io_kubernetes_workload_type\"] AS STRING), _timestamp_ ORDER BY CAST(__ext[\"io_kubernetes_workload_name\"] AS STRING) DESC, `_timestamp_` ASC LIMIT 3",
		},
		// 测试用例11: doris 处理 object 字段 + 时间聚合 5m
		{
			name: "doris default multiple order fields and time aggregate 5m",
			query: &metadata.Query{
				DB:            "5000140_bklog_container_log_demo_analysis",
				Measurement:   sqlExpr.Doris,
				Field:         "__ext.container_id",
				Size:          3,
				AllConditions: metadata.AllConditions{},
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"__ext.io_kubernetes_workload_name"},
						Window:     time.Minute * 5,
					},
				},
				Orders: map[string]bool{
					"__ext.io_kubernetes_workload_name": false,
				},
			},
			start:    time.Unix(1741334700, 0),
			end:      time.Unix(1741335000, 0),
			expected: "SELECT CAST(__ext[\"io_kubernetes_workload_name\"] AS STRING) AS `__ext__bk_46__io_kubernetes_workload_name`, COUNT(CAST(__ext[\"container_id\"] AS STRING)) AS `_value_`, CAST(__shard_key__ / 1000 / 5 AS INT) * 5 * 60 * 1000 AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1741334700000 AND `dtEventTimeStamp` < 1741335000000 AND `thedate` = '20250307' GROUP BY CAST(__ext[\"io_kubernetes_workload_name\"] AS STRING), _timestamp_ ORDER BY CAST(__ext[\"io_kubernetes_workload_name\"] AS STRING) DESC, `_timestamp_` ASC LIMIT 3",
		},
		// 测试用例12: doris 处理 object 字段 + 时间聚合 15s
		{
			name: "doris default multiple order fields and time aggregate 15s",
			query: &metadata.Query{
				DB:            "5000140_bklog_container_log_demo_analysis",
				Measurement:   sqlExpr.Doris,
				Field:         "__ext.container_id",
				Size:          3,
				AllConditions: metadata.AllConditions{},
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"__ext.io_kubernetes_workload_name"},
						Window:     time.Second * 15,
					},
				},
				Orders: map[string]bool{
					"__ext.io_kubernetes_workload_name": false,
				},
			},
			start:    time.Unix(1741334700, 0),
			end:      time.Unix(1741335000, 0),
			expected: "SELECT CAST(__ext[\"io_kubernetes_workload_name\"] AS STRING) AS `__ext__bk_46__io_kubernetes_workload_name`, COUNT(CAST(__ext[\"container_id\"] AS STRING)) AS `_value_`, CAST(dtEventTimeStamp / 1000 / 15 AS INT) * 15 * 1000 AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1741334700000 AND `dtEventTimeStamp` < 1741335000000 AND `thedate` = '20250307' GROUP BY CAST(__ext[\"io_kubernetes_workload_name\"] AS STRING), _timestamp_ ORDER BY CAST(__ext[\"io_kubernetes_workload_name\"] AS STRING) DESC, `_timestamp_` ASC LIMIT 3",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())

			// 设置默认时间范围
			start := tc.start
			if start.IsZero() {
				start = baseStart
			}
			end := tc.end
			if end.IsZero() {
				end = baseEnd
			}

			// SQL生成验证
			fact := bksql.NewQueryFactory(ctx, tc.query).WithFieldsMap(map[string]string{
				"text": sqlExpr.DorisTypeText,
			}).WithRangeTime(start, end)
			generatedSQL, err := fact.SQL()

			if tc.err != nil {
				assert.Equal(t, tc.err, err)
			} else {
				assert.Nil(t, err)
				if err == nil {
					assert.Equal(t, tc.expected, generatedSQL)

					// 验证时间条件
					if tc.start.IsZero() && tc.end.IsZero() {
						assert.Contains(t, generatedSQL, fmt.Sprintf("`dtEventTimeStamp` >= %d", baseStart.UnixMilli()))
						assert.Contains(t, generatedSQL, fmt.Sprintf("`dtEventTimeStamp` < %d", baseEnd.UnixMilli()))
					}
				}
			}
		})
	}
}

// 测试正常标签名查询
func TestInstance_QueryLabelNames_Normal(t *testing.T) {
	// 初始化测试实例
	ctx := metadata.InitHashID(context.Background())
	instance := createTestInstance(ctx)

	end := time.Unix(1740553771, 0)
	start := time.Unix(1740551971, 0)

	// mock 查询数据
	mock.BkSQL.Set(map[string]any{
		"SELECT *, `dtEventTimeStamp` AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1740551971000 AND `dtEventTimeStamp` < 1740553771000 AND `thedate` = '20250226' LIMIT 1": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{"5000140_bklog_container_log_demo_analysis":{"start":"2025022600","end":"2025022623"}},"cluster":"doris_bklog","totalRecords":1,"external_api_call_time_mills":{"bkbase_meta_api":8},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"thedate":20250226,"dteventtimestamp":1740552000000,"dteventtime":"2025-02-26 14:40:00","localtime":"2025-02-26 14:45:01","_starttime_":"2025-02-26 14:40:00","_endtime_":"2025-02-26 14:40:00","bk_host_id":5279498,"__ext":"{\"container_id\":\"101e58e9940c78a374e4ca3fe28d2360a8dd38b5b93937f7996902c203ac7812\",\"container_name\":\"ds\",\"bk_bcs_cluster_id\":\"BCS-K8S-26678\",\"io_kubernetes_pod\":\"ds-pro-z-instance-season-p-qvq6l-8fbrq\",\"container_image\":\"proz-tcr.tencentcloudcr.com/a1_proz/proz-ds@sha256:0ccc969d0614c41e9418ab81f444a26db743e82d3a2a2cc2d12e549391c5768f\",\"io_kubernetes_pod_namespace\":\"ds9204\",\"io_kubernetes_workload_type\":\"GameServer\",\"io_kubernetes_pod_uid\":\"78e5a0cf-fdec-43aa-9c64-5e58c35c949d\",\"io_kubernetes_workload_name\":\"ds-pro-z-instance-season-p-qvq6l-8fbrq\",\"labels\":{\"agones_dev_gameserver\":\"ds-pro-z-instance-season-p-qvq6l-8fbrq\",\"agones_dev_role\":\"gameserver\",\"agones_dev_safe_to_evict\":\"false\",\"component\":\"ds\",\"part_of\":\"projectz\"}}","cloudid":0,"path":"/proz/LinuxServer/ProjectZ/Saved/Logs/Stats/ObjectStat_ds-pro-z-instance-season-p-qvq6l-8fbrq-0_2025.02.26-04.25.48.368.log","gseindex":1399399185,"iterationindex":185,"log":"[2025.02.26-14.40.00:711][937]                       BTT_SetLocationWarpTarget_C 35","time":1740552000,"_timestamp_":1740552000000}],"stage_elapsed_time_mills":{"check_query_syntax":1,"query_db":54,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":9,"connect_db":34,"match_query_routing_rule":0,"check_permission":9,"check_query_semantic":0,"pick_valid_storage":0},"select_fields_order":["thedate","dteventtimestamp","dteventtime","localtime","_starttime_","_endtime_","bk_host_id","__ext","cloudid","path","gseindex","iterationindex","log","time","_timestamp_"],"total_record_size":4512,"timetaken":0.107,"result_schema":[{"field_type":"int","field_name":"__c0","field_alias":"thedate","field_index":0},{"field_type":"long","field_name":"__c1","field_alias":"dteventtimestamp","field_index":1},{"field_type":"string","field_name":"__c2","field_alias":"dteventtime","field_index":2},{"field_type":"string","field_name":"__c3","field_alias":"localtime","field_index":3},{"field_type":"string","field_name":"__c4","field_alias":"_starttime_","field_index":4},{"field_type":"string","field_name":"__c5","field_alias":"_endtime_","field_index":5},{"field_type":"int","field_name":"__c6","field_alias":"bk_host_id","field_index":6},{"field_type":"string","field_name":"__c7","field_alias":"__ext","field_index":7},{"field_type":"int","field_name":"__c8","field_alias":"cloudid","field_index":8},{"field_type":"string","field_name":"__c10","field_alias":"path","field_index":10},{"field_type":"long","field_name":"__c11","field_alias":"gseindex","field_index":11},{"field_type":"int","field_name":"__c12","field_alias":"iterationindex","field_index":12},{"field_type":"string","field_name":"__c13","field_alias":"log","field_index":13},{"field_type":"long","field_name":"__c14","field_alias":"time","field_index":14},{"field_type":"long","field_name":"__c15","field_alias":"_timestamp_","field_index":15}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["5000140_bklog_container_log_demo_analysis"]},"errors":null,"trace_id":"3465b590d66a21d3aae7841d36aaec3d","span_id":"34296e9388f3258a"}`,
	})

	// 测试用例
	tests := []struct {
		name string
		qry  *metadata.Query

		expectedNames []string
		expectError   bool
	}{
		{
			name: "normal-case",
			qry: &metadata.Query{
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: "doris",
			},
			expectedNames: []string{
				"dteventtimestamp", "dteventtime", "localtime", "_starttime_", "_endtime_", "bk_host_id", "__ext", "cloudid", "path", "gseindex", "iterationindex", "log", "time",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 执行测试
			ctx = metadata.InitHashID(ctx)
			names, err := instance.QueryLabelNames(ctx, tt.qry, start, end)

			// 验证结果
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.ElementsMatch(t, tt.expectedNames, names)
			}
		})
	}
}

// 测试正常标签名查询
func TestInstance_QueryLabelValues_Normal(t *testing.T) {
	// 初始化测试实例
	ctx := metadata.InitHashID(context.Background())
	instance := createTestInstance(ctx)

	end := time.Unix(1740553771, 0)
	start := time.Unix(1740551971, 0)

	// mock 查询数据
	mock.BkSQL.Set(map[string]any{
		"SELECT `bk_host_id`, COUNT(*) AS `_value_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1740551971000 AND `dtEventTimeStamp` < 1740553771000 AND `thedate` = '20250226' GROUP BY `bk_host_id` LIMIT 2": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{"5000140_bklog_container_log_demo_analysis":{"start":"2025022600","end":"2025022623"}},"cluster":"doris_bklog","totalRecords":26,"external_api_call_time_mills":{"bkbase_meta_api":6},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"bk_host_id":5843771,"_value_":6520005},{"bk_host_id":4580470,"_value_":703143}],"stage_elapsed_time_mills":{"check_query_syntax":1,"query_db":204,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":6,"connect_db":39,"match_query_routing_rule":0,"check_permission":6,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["bk_host_id","_value_"],"total_record_size":6952,"timetaken":0.257,"result_schema":[{"field_type":"int","field_name":"__c0","field_alias":"bk_host_id","field_index":0},{"field_type":"long","field_name":"__c1","field_alias":"_value_","field_index":1}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["5000140_bklog_container_log_demo_analysis"]},"errors":null,"trace_id":"3592ea81c52ab826aba587d91e5054b6","span_id":"f21eca23481c778d"}`,
	})

	// 测试用例
	tests := []struct {
		name string
		qry  *metadata.Query
		key  string

		expectedNames []string
		expectError   bool
	}{
		{
			name: "normal-case",
			qry: &metadata.Query{
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: "doris",
				Size:        2,
			},
			key: "bk_host_id",
			expectedNames: []string{
				"5843771", "4580470",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 执行测试
			ctx = metadata.InitHashID(ctx)
			names, err := instance.QueryLabelValues(ctx, tt.qry, tt.key, start, end)

			// 验证结果
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.ElementsMatch(t, tt.expectedNames, names)
			}
		})
	}
}

// 创建测试用Instance
func createTestInstance(ctx context.Context) *bksql.Instance {
	mock.Init()

	ins, err := bksql.NewInstance(ctx, &bksql.Options{
		Address:   mock.BkSQLUrl,
		Timeout:   time.Minute,
		MaxLimit:  1e4,
		Tolerance: 5,
		Curl:      &curl.HttpCurl{Log: log.DefaultLogger},
	})
	if err != nil {
		log.Fatalf(ctx, err.Error())
		return nil
	}
	return ins
}
