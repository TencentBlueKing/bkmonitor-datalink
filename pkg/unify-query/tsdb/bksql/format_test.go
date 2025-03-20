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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql"
)

func TestNewSqlFactory(t *testing.T) {
	start := time.Unix(1741795260, 0)
	end := time.Unix(1741796260, 0)

	for name, c := range map[string]struct {
		query    *metadata.Query
		expected string

		start time.Time
		end   time.Time
	}{
		"doris sum-count_over_time-with-promql-1": {
			query: &metadata.Query{
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: "doris",
				Field:       "gseIndex",
				Aggregates: metadata.Aggregates{
					{
						Name: "count",
						Dimensions: []string{
							"level",
						},
						Window: time.Minute,
					},
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "gseIndex",
							Operator:      metadata.ConditionGt,
							Value:         []string{"0"},
						},
						{
							DimensionName: "level",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"ERROR"},
						},
					},
				},
				From:   0,
				Size:   0,
				Orders: metadata.Orders{"level": true},
			},
			expected: "SELECT `level`, COUNT(`gseIndex`) AS `_value_`, CAST(__shard_key__ / 1000 / 1 AS INT) * 1 * 60 * 1000 AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1741795260000 AND `dtEventTimeStamp` < 1741796260000 AND `thedate` = '20250313' AND `gseIndex` > 0 AND `level` = 'ERROR' GROUP BY `level`, _timestamp_ ORDER BY `_timestamp_` ASC, `level` ASC",
		},
		"doris sum-count_over_time-with-promql-seconds": {
			query: &metadata.Query{
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: "doris",
				Field:       "gseIndex",
				Aggregates: metadata.Aggregates{
					{
						Name: "count",
						Dimensions: []string{
							"level",
						},
						Window: time.Minute + time.Second*15,
					},
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "gseIndex",
							Operator:      metadata.ConditionGt,
							Value:         []string{"0"},
						},
						{
							DimensionName: "level",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"ERROR"},
						},
					},
				},
				From:   0,
				Size:   0,
				Orders: metadata.Orders{"level": true},
			},
			expected: "SELECT `level`, COUNT(`gseIndex`) AS `_value_`, CAST(dtEventTimeStamp / 75000 AS INT) * 75000  AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1741795260000 AND `dtEventTimeStamp` < 1741796260000 AND `thedate` = '20250313' AND `gseIndex` > 0 AND `level` = 'ERROR' GROUP BY `level`, _timestamp_ ORDER BY `_timestamp_` ASC, `level` ASC",
		},
		"doris sum-with-promql-1": {
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
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "gseIndex",
							Operator:      metadata.ConditionGt,
							Value:         []string{"0"},
						},
					},
				},
				From:   0,
				Size:   10,
				Orders: nil,
			},
			expected: "SELECT `ip`, SUM(`gseIndex`) AS `_value_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1741795260000 AND `dtEventTimeStamp` < 1741796260000 AND `thedate` = '20250313' AND `gseIndex` > 0 GROUP BY `ip` LIMIT 10",
		},
		"doris count-with-count-promql-1": {
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
			},
			expected: "SELECT `ip`, COUNT(`gseIndex`) AS `_value_`, CAST(__shard_key__ / 1000 / 1 AS INT) * 1 * 60 * 1000 AS `_timestamp_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1741795260000 AND `dtEventTimeStamp` < 1741796260000 AND `thedate` = '20250313' GROUP BY `ip`, _timestamp_ ORDER BY `_timestamp_` ASC",
		},
		"doris count-with-count-promql-2": {
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
				AllConditions: metadata.AllConditions{
					[]metadata.ConditionField{
						{
							DimensionName: "gseIndex",
							Value:         []string{"0"},
							Operator:      metadata.ConditionGt,
						},
					},
				},
			},
			expected: "SELECT `ip`, COUNT(`gseIndex`) AS `_value_`, CAST(__shard_key__ / 1000 / 1 AS INT) * 1 * 60 * 1000 AS `_timestamp_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1733607400000 AND `dtEventTimeStamp` < 1733939375000 AND `thedate` >= '20241208' AND `thedate` <= '20241212' AND `gseIndex` > 0 GROUP BY `ip`, _timestamp_ ORDER BY `_timestamp_` ASC",
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
			fact := bksql.NewQueryFactory(ctx, c.query).WithRangeTime(c.start, c.end)
			sql, err := fact.SQL()
			assert.Nil(t, err)
			assert.Equal(t, c.expected, sql)
		})
	}
}
