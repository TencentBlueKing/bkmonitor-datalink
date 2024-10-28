// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestInstance_QueryRaw(t *testing.T) {

	ctx := context.Background()
	mock.Init()

	ins, err := NewInstance(ctx, &Options{
		Address:   mock.BkSQLUrl,
		Timeout:   time.Minute,
		MaxLimit:  1e4,
		Tolerance: 5,
		Curl:      &curl.HttpCurl{Log: log.DefaultLogger},
	})
	if err != nil {
		log.Fatalf(ctx, err.Error())
		return
	}

	mock.BkSQL.Set(map[string]any{
		"SELECT *, `login_rate` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` < 1730118889181 AND (namespace = 'gz100' OR namespace = 'bgp2\\-new') LIMIT 10005":                                                                             "{\"result\":true,\"message\":\"成功\",\"code\":\"00\",\"data\":{\"result_table_scan_range\":{\"132_lol_new_login_queue_login_1min\":{}},\"cluster\":\"default2\",\"totalRecords\":5,\"resource_use_summary\":{\"cpu_time_mills\":0,\"memory_bytes\":0,\"processed_bytes\":0,\"processed_rows\":0},\"source\":\"\",\"list\":[{\"thedate\":20241028,\"dtEventTime\":\"2024-10-28 20:31:00\",\"dtEventTimeStamp\":1730118660000,\"localTime\":\"2024-10-28 20:32:03\",\"_startTime_\":\"2024-10-28 20:31:00\",\"_endTime_\":\"2024-10-28 20:32:00\",\"namespace\":\"gz100\",\"login_rate\":269.0,\"_value_\":269.0,\"_timestamp_\":1730118660000},{\"thedate\":20241028,\"dtEventTime\":\"2024-10-28 20:28:00\",\"dtEventTimeStamp\":1730118480000,\"localTime\":\"2024-10-28 20:29:03\",\"_startTime_\":\"2024-10-28 20:28:00\",\"_endTime_\":\"2024-10-28 20:29:00\",\"namespace\":\"gz100\",\"login_rate\":271.0,\"_value_\":271.0,\"_timestamp_\":1730118480000},{\"thedate\":20241028,\"dtEventTime\":\"2024-10-28 20:29:00\",\"dtEventTimeStamp\":1730118540000,\"localTime\":\"2024-10-28 20:30:02\",\"_startTime_\":\"2024-10-28 20:29:00\",\"_endTime_\":\"2024-10-28 20:30:00\",\"namespace\":\"gz100\",\"login_rate\":267.0,\"_value_\":267.0,\"_timestamp_\":1730118540000},{\"thedate\":20241028,\"dtEventTime\":\"2024-10-28 20:30:00\",\"dtEventTimeStamp\":1730118600000,\"localTime\":\"2024-10-28 20:31:04\",\"_startTime_\":\"2024-10-28 20:30:00\",\"_endTime_\":\"2024-10-28 20:31:00\",\"namespace\":\"gz100\",\"login_rate\":274.0,\"_value_\":274.0,\"_timestamp_\":1730118600000},{\"thedate\":20241028,\"dtEventTime\":\"2024-10-28 20:27:00\",\"dtEventTimeStamp\":1730118420000,\"localTime\":\"2024-10-28 20:28:03\",\"_startTime_\":\"2024-10-28 20:27:00\",\"_endTime_\":\"2024-10-28 20:28:00\",\"namespace\":\"gz100\",\"login_rate\":279.0,\"_value_\":279.0,\"_timestamp_\":1730118420000}],\"select_fields_order\":[\"thedate\",\"dtEventTime\",\"dtEventTimeStamp\",\"localTime\",\"_startTime_\",\"_endTime_\",\"namespace\",\"login_rate\",\"_value_\",\"_timestamp_\"],\"sql\":\"SELECT *, `login_rate` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM mapleleaf_132.lol_new_login_queue_login_1min_132 WHERE ((`dtEventTimeStamp` >= 1730118415782) AND (`dtEventTimeStamp` < 1730118715782)) AND ((namespace = 'gz100') OR (namespace = 'bgp2-new')) LIMIT 10005\",\"total_record_size\":5832,\"timetaken\":0.251,\"bksql_call_elapsed_time\":0,\"device\":\"tspider\",\"result_table_ids\":[\"132_lol_new_login_queue_login_1min\"]},\"errors\":null,\"trace_id\":\"c083ca92cee435138f9076e1c1f6faeb\",\"span_id\":\"735f314a259a981a\"}",
		"SELECT `namespace`, COUNT(`login_rate`) AS `_value_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` < 1730118889181 GROUP BY `namespace` LIMIT 10005":                                                                                                                                  "{\"result\":true,\"message\":\"成功\",\"code\":\"00\",\"data\":{\"result_table_scan_range\":{\"132_lol_new_login_queue_login_1min\":{}},\"cluster\":\"default2\",\"totalRecords\":11,\"resource_use_summary\":{\"cpu_time_mills\":0,\"memory_bytes\":0,\"processed_bytes\":0,\"processed_rows\":0},\"source\":\"\",\"list\":[{\"namespace\":\"bgp2\",\"_value_\":5},{\"namespace\":\"cq100\",\"_value_\":5},{\"namespace\":\"gz100\",\"_value_\":5},{\"namespace\":\"hn0-new\",\"_value_\":5},{\"namespace\":\"hn1\",\"_value_\":5},{\"namespace\":\"hn10\",\"_value_\":5},{\"namespace\":\"nj100\",\"_value_\":5},{\"namespace\":\"njloadtest\",\"_value_\":5},{\"namespace\":\"pbe\",\"_value_\":5},{\"namespace\":\"tj100\",\"_value_\":5},{\"namespace\":\"tj101\",\"_value_\":5}],\"select_fields_order\":[\"namespace\",\"_value_\"],\"sql\":\"SELECT `namespace`, COUNT(`login_rate`) AS `_value_` FROM mapleleaf_132.lol_new_login_queue_login_1min_132 WHERE (`dtEventTimeStamp` >= 1730118589181) AND (`dtEventTimeStamp` < 1730118889181) GROUP BY `namespace` LIMIT 10005\",\"total_record_size\":3216,\"timetaken\":0.24,\"bksql_call_elapsed_time\":0,\"device\":\"tspider\",\"result_table_ids\":[\"132_lol_new_login_queue_login_1min\"]},\"errors\":null,\"trace_id\":\"5c70526f101a00531ef8fbaadc783693\",\"span_id\":\"2a31369ceb208970\"}",
		"SELECT COUNT(`login_rate`) AS `_value_`, MAX((`dtEventTimeStamp` - (`dtEventTimeStamp` % 60000))) AS `_timestamp_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` < 1730118889181 GROUP BY (`dtEventTimeStamp` - (`dtEventTimeStamp` % 60000)) ORDER BY `_timestamp_` ASC LIMIT 10005": "{\"result\":true,\"message\":\"成功\",\"code\":\"00\",\"data\":{\"result_table_scan_range\":{\"132_lol_new_login_queue_login_1min\":{}},\"cluster\":\"default2\",\"totalRecords\":5,\"resource_use_summary\":{\"cpu_time_mills\":0,\"memory_bytes\":0,\"processed_bytes\":0,\"processed_rows\":0},\"source\":\"\",\"list\":[{\"_value_\":11,\"_timestamp_\":1730118600000},{\"_value_\":11,\"_timestamp_\":1730118660000},{\"_value_\":11,\"_timestamp_\":1730118720000},{\"_value_\":11,\"_timestamp_\":1730118780000},{\"_value_\":11,\"_timestamp_\":1730118840000}],\"select_fields_order\":[\"_value_\",\"_timestamp_\"],\"sql\":\"SELECT COUNT(`login_rate`) AS `_value_`, MAX(`dtEventTimeStamp` - (`dtEventTimeStamp` % 60000)) AS `_timestamp_` FROM mapleleaf_132.lol_new_login_queue_login_1min_132 WHERE (`dtEventTimeStamp` >= 1730118589181) AND (`dtEventTimeStamp` < 1730118889181) GROUP BY `dtEventTimeStamp` - (`dtEventTimeStamp` % 60000) ORDER BY `_timestamp_` LIMIT 10005\",\"total_record_size\":1424,\"timetaken\":0.231,\"bksql_call_elapsed_time\":0,\"device\":\"tspider\",\"result_table_ids\":[\"132_lol_new_login_queue_login_1min\"]},\"errors\":null,\"trace_id\":\"127866cb51f85a4a7f620eb0e66588b1\",\"span_id\":\"578f26767bbb78c8\"}",
	})

	end := time.UnixMilli(1730118889181)
	start := time.UnixMilli(1730118589181)

	datasource := "bkdata"
	db := "132_lol_new_login_queue_login_1min"
	field := "login_rate"
	tableID := db + ".default"

	for idx, c := range []struct {
		query    *metadata.Query
		expected string
	}{
		{
			query: &metadata.Query{
				DataSource:     datasource,
				TableID:        tableID,
				DB:             db,
				MetricName:     field,
				BkSqlCondition: "namespace = 'gz100' OR namespace = 'bgp2\\-new'",
				OffsetInfo:     metadata.OffSetInfo{Limit: 10},
			},
			expected: `[{"labels":[{"name":"__name__","value":"bkdata:132_lol_new_login_queue_login_1min:default:login_rate"},{"name":"namespace","value":"gz100"}],"samples":[{"value":269,"timestamp":1730118660000},{"value":271,"timestamp":1730118480000},{"value":267,"timestamp":1730118540000},{"value":274,"timestamp":1730118600000},{"value":279,"timestamp":1730118420000}],"exemplars":null,"histograms":null}]`,
		},
		{
			query: &metadata.Query{
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"namespace"},
					},
				},
			},
			expected: `[{"labels":[{"name":"__name__","value":":"},{"name":"namespace","value":"bgp2"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":":"},{"name":"namespace","value":"cq100"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":":"},{"name":"namespace","value":"gz100"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":":"},{"name":"namespace","value":"hn0-new"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":":"},{"name":"namespace","value":"hn1"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":":"},{"name":"namespace","value":"hn10"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":":"},{"name":"namespace","value":"nj100"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":":"},{"name":"namespace","value":"njloadtest"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":":"},{"name":"namespace","value":"pbe"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":":"},{"name":"namespace","value":"tj100"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":":"},{"name":"namespace","value":"tj101"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null}]`,
		},
		{
			query: &metadata.Query{
				Aggregates: metadata.Aggregates{
					{
						Name:   "count",
						Window: time.Minute,
					},
				},
			},
			expected: `[{"labels":[{"name":"__name__","value":":"}],"samples":[{"value":11,"timestamp":1730118600000},{"value":11,"timestamp":1730118660000},{"value":11,"timestamp":1730118720000},{"value":11,"timestamp":1730118780000},{"value":11,"timestamp":1730118840000}],"exemplars":null,"histograms":null}]`,
		},
	} {
		t.Run(fmt.Sprintf("%d", idx), func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			if c.query.DB == "" {
				c.query.DB = db
			}
			if c.query.Field == "" {
				c.query.Field = field
			}
			ss := ins.QuerySeriesSet(ctx, c.query, start, end)

			var timeSeries []prompb.TimeSeries
			for ss.Next() {
				series := ss.At()
				lbs := series.Labels()
				newLbs := make([]prompb.Label, 0, len(lbs))
				for _, lb := range lbs {
					newLbs = append(newLbs, prompb.Label{
						Name:  lb.Name,
						Value: lb.Value,
					})
				}

				var newSamples []prompb.Sample
				it := series.Iterator(nil)
				for it.Next() == chunkenc.ValFloat {
					ts, val := it.At()

					newSamples = append(newSamples, prompb.Sample{
						Value:     val,
						Timestamp: ts,
					})

				}
				if it.Err() != nil {
					panic(it.Err())
				}

				timeSeries = append(timeSeries, prompb.TimeSeries{Labels: newLbs, Samples: newSamples})
			}

			if ws := ss.Warnings(); len(ws) > 0 {
				panic(ws)
			}

			if ss.Err() != nil {
				log.Errorf(ctx, ss.Err().Error())
			}

			actual, _ := json.Marshal(timeSeries)
			assert.Equal(t, c.expected, string(actual))
		})
	}
}

func TestInstance_bkSql(t *testing.T) {
	mock.Init()

	start := time.UnixMilli(1718189940000)
	end := time.UnixMilli(1718193555000)

	testCases := []struct {
		query *metadata.Query

		expected string
	}{
		{
			query: &metadata.Query{
				DB:             "132_lol_new_login_queue_login_1min",
				Field:          "login_rate",
				BkSqlCondition: "namespace REGEXP '^(bgp2\\-new|gz100)$'",
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"namespace"},
						Window:     time.Second * 15,
					},
				},
			},
			expected: "SELECT `namespace`, COUNT(`login_rate`) AS `_value_`, MAX((`dtEventTimeStamp` - (`dtEventTimeStamp` % 15000))) AS `_timestamp_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND (namespace REGEXP '^(bgp2\\-new|gz100)$') GROUP BY `namespace`, (`dtEventTimeStamp` - (`dtEventTimeStamp` % 15000)) ORDER BY `_timestamp_` ASC",
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

			expected: "SELECT SUM(`value`) AS `_value_` FROM `132_hander_opmon_avg` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000",
		},
		{
			query: &metadata.Query{
				DB:          "100133_ieod_logsearch4_errorlog_p",
				Measurement: "doris",
				Field:       "value",
				Size:        5,
			},
			expected: "SELECT *, `value` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 LIMIT 5",
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
			expected: "SELECT *, `value` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 ORDER BY `_timestamp_` DESC",
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

			expected: "SELECT `ip`, COUNT(`gseIndex`) AS `_value_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 GROUP BY `ip` LIMIT 5",
		},
	}

	for i, c := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			sql, err := NewQueryFactory(ctx, c.query).WithRangeTime(start, end).SQL()
			assert.Nil(t, err)
			if err == nil {
				assert.Equal(t, c.expected, sql)
			}
		})
	}
}
