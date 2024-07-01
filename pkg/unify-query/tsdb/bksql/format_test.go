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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestNewSqlFactory(t *testing.T) {
	start := time.Unix(1717144141, 0)
	end := time.Unix(1717147741, 0)
	step := time.Minute

	for name, c := range map[string]struct {
		query    *metadata.Query
		expected string
	}{
		"sum-count_over_time-with-promql-1": {
			query: &metadata.Query{
				DB:          "100133_ieod_logsearch4_errorlog_p.doris",
				Measurement: "100133_ieod_logsearch4_errorlog_p.doris",
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
				Orders:         metadata.Orders{"_time": true},
			},
			expected: "SELECT MAX((`dtEventTimeStamp`- (`dtEventTimeStamp` % 60000))) AS `_timestamp_`, count(`gseIndex`) AS `_value_`, `ip` FROM 100133_ieod_logsearch4_errorlog_p.doris WHERE dtEventTimeStamp >= 1717144141000 AND dtEventTimeStamp < 1717147741000 AND (gseIndex > 0) GROUP BY (`dtEventTimeStamp`- (`dtEventTimeStamp` % 60000)), `ip`",
		},
		"sum-with-promql-1": {
			query: &metadata.Query{
				DB:          "100133_ieod_logsearch4_errorlog_p.doris",
				Measurement: "100133_ieod_logsearch4_errorlog_p.doris",
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
			expected: "SELECT * FROM 100133_ieod_logsearch4_errorlog_p.doris WHERE dtEventTimeStamp >= 1717144141000 AND dtEventTimeStamp < 1717147741000 AND gseIndex > 0 LIMIT 10",
		},
		"count-without-promql-1": {
			query: &metadata.Query{
				DB:          "100133_ieod_logsearch4_errorlog_p.doris",
				Measurement: "100133_ieod_logsearch4_errorlog_p.doris",
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
			expected: "SELECT count(`gseIndex`) AS `_value_`, `ip` FROM 100133_ieod_logsearch4_errorlog_p.doris WHERE dtEventTimeStamp >= 1717144141000 AND dtEventTimeStamp < 1717147741000 AND gseIndex > 0 GROUP BY `ip`",
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			fact := NewQueryFactory(ctx, c.query).WithRangeTime(start, end, step)
			err := fact.ParserQuery()
			assert.Nil(t, err)
			assert.Equal(t, c.expected, fact.SQL())

			inst := &Instance{}
			oldSql, _ := inst.bkSql(ctx, c.query, start, end)
			assert.Equal(t, fact.SQL(), oldSql)
		})
	}
}
