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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
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
		"count-with-count-promql-3": {
			// 2024-12-07 21:36:40	UTC
			// 2024-12-08 05:36:40  Asia/ShangHai
			start: time.UnixMilli(1741276799999),
			// 2024-12-11 17:49:35 	UTC
			// 2024-12-12 01:49:35  Asia/ShangHai
			end: time.UnixMilli(1741967999999),
			query: &metadata.Query{
				DB:          "100680_alpha_server_perf_data_tglog",
				Measurement: "",
				Field:       "sum_Sub8MsFrames",
				Aggregates: metadata.Aggregates{
					{
						Name:     "count",
						Window:   time.Hour * 24,
						TimeZone: "Asia/Shanghai",
					},
				},
				BkSqlCondition: "`deployment` = 'alpha1-gp-3' and `datacenter` = 'qcloud-tj1'",
			},
			expected: "",
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

func TestWindowWithTimezone(t *testing.T) {
	mock.Init()

	testCases := []struct {
		name     string
		start    time.Time
		timezone string
		window   time.Duration

		expected time.Time
	}{
		{
			name:     "test window 1m - 1",
			start:    time.UnixMilli(1742267704000),
			timezone: "Asia/ShangHai",
			window:   time.Minute,

			expected: time.UnixMilli(1742267700000),
		},
		{
			name:   "test window 1d +8 - 1",
			start:  time.UnixMilli(1742267704000),
			window: time.Hour * 24,

			expected: time.UnixMilli(1742256000000),
		},
		{
			name:     "test window 1d +8 - 1",
			start:    time.UnixMilli(1742267704000),
			timezone: "Asia/ShangHai",
			window:   time.Hour * 24,

			expected: time.UnixMilli(1742227200000),
		},
		{
			name:     "test window 26h +8 - 1",
			start:    time.UnixMilli(1742267704000),
			timezone: "Asia/ShangHai",
			window:   time.Hour*24 + time.Hour*2,

			expected: time.UnixMilli(1742176800000),
		},
		{
			name:     "test window 3d +8 - 1",
			start:    time.UnixMilli(1742267704000),
			timezone: "Asia/ShangHai",
			window:   time.Hour * 24 * 3,

			expected: time.UnixMilli(1742140800000),
		},
		{
			name:     "test window 1m +8 - 2",
			start:    time.UnixMilli(1742266099000),
			timezone: "Asia/ShangHai",
			window:   time.Minute,

			expected: time.UnixMilli(1742266080000),
		},
		{
			name:     "test window 6h +8 - 2",
			start:    time.UnixMilli(1742266099000),
			timezone: "Asia/ShangHai",
			window:   time.Hour * 6,

			expected: time.UnixMilli(1742256000000),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cTime := tc.start.UnixMilli()
			window := tc.window.Milliseconds()

			var offset int64
			loc, _ := time.LoadLocation(tc.timezone)
			if window%(time.Hour*24).Milliseconds() == 0 {
				_, z := time.Now().In(loc).Zone()
				offset = int64(z) * 1000
			}

			nTime := cTime - ((cTime-offset)%window - offset)

			actual := time.UnixMilli(nTime)
			assert.Equal(t, tc.expected.UnixMilli(), actual.UnixMilli())
			log.Infof(context.TODO(), "%d - ((%d - %d) %% %d - %d", nTime, cTime, offset, window, offset)
			log.Infof(context.TODO(), "%s => %s", tc.start.In(loc).String(), actual.In(loc).String())
		})
	}
}
