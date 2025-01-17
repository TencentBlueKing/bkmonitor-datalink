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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestNewSqlFactory(t *testing.T) {
	start := time.Unix(1717144141, 0)
	end := time.Unix(1717147741, 0)

	for name, c := range map[string]struct {
		query    *metadata.Query
		expected string

		start time.Time
		end   time.Time
	}{
		"sum-count_over_time-with-promql-1": {
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
						Window: time.Minute,
					},
				},
				BkSqlCondition: "gseIndex > 0",
				From:           0,
				Size:           0,
				Orders:         metadata.Orders{"ip": true},
			},
			expected: "SELECT `ip`, COUNT(`gseIndex`) AS `_value_`, MAX((`dtEventTimeStamp` - (`dtEventTimeStamp` % 60000))) AS `_timestamp_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1717144141000 AND `dtEventTimeStamp` < 1717147741000 AND `thedate` = '20240531' AND (gseIndex > 0) GROUP BY `ip`, (`dtEventTimeStamp` - (`dtEventTimeStamp` % 60000)) ORDER BY `_timestamp_` ASC, `ip` ASC",
		},
		"sum-with-promql-1": {
			query: &metadata.Query{
				DB:          "100133_ieod_logsearch4_errorlog_p",
				Measurement: "doris",
				Field:       "gseIndex",
				Aggregates: metadata.Aggregates{
					{
						Name: "sum",
						Dimensions: []string{
							"ip",
						},
					},
				},
				BkSqlCondition: "gseIndex > 0",
				From:           0,
				Size:           10,
				Orders:         nil,
			},
			expected: "SELECT `ip`, SUM(`gseIndex`) AS `_value_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1717144141000 AND `dtEventTimeStamp` < 1717147741000 AND `thedate` = '20240531' AND (gseIndex > 0) GROUP BY `ip` LIMIT 10",
		},
		"count-with-count-promql-1": {
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
						Window: time.Minute,
					},
				},
				BkSqlCondition: "gseIndex > 0",
			},
			expected: "SELECT `ip`, COUNT(`gseIndex`) AS `_value_`, MAX((`dtEventTimeStamp` - (`dtEventTimeStamp` % 60000))) AS `_timestamp_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1717144141000 AND `dtEventTimeStamp` < 1717147741000 AND `thedate` = '20240531' AND (gseIndex > 0) GROUP BY `ip`, (`dtEventTimeStamp` - (`dtEventTimeStamp` % 60000)) ORDER BY `_timestamp_` ASC",
		},
		"count-with-count-promql-2": {
			// 2024-12-07 21:36:40	UTC
			// 2024-12-08 05:36:40  Asia/ShangHai
			start: time.Unix(1733607400, 0),
			// 2024-12-11 17:49:35 	UTC
			// 2024-12-12 01:49:35  Asia/ShangHai
			end: time.Unix(1733939375, 0),
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
						Window: time.Minute,
					},
				},
				BkSqlCondition: "gseIndex > 0",
			},
			expected: "SELECT `ip`, COUNT(`gseIndex`) AS `_value_`, MAX((`dtEventTimeStamp` - (`dtEventTimeStamp` % 60000))) AS `_timestamp_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1733607400000 AND `dtEventTimeStamp` < 1733939375000 AND `thedate` >= '20241208' AND `thedate` <= '20241212' AND (gseIndex > 0) GROUP BY `ip`, (`dtEventTimeStamp` - (`dtEventTimeStamp` % 60000)) ORDER BY `_timestamp_` ASC",
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			if c.start.Unix() <= 0 {
				c.start = start
			}
			if c.end.Unix() <= 0 {
				c.end = end
			}

			log.Infof(ctx, "start: %s, end: %s", c.start, c.end)
			fact := NewQueryFactory(ctx, c.query).WithRangeTime(c.start, c.end)
			sql, err := fact.SQL()
			assert.Nil(t, err)
			assert.Equal(t, c.expected, sql)
		})
	}
}
