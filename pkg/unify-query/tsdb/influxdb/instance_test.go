// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestInstance_MakeSQL(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	db := "_internal"
	measurement := "database"
	field := "numSeries"

	testCases := map[string]struct {
		query    *metadata.Query
		err      error
		expected string
	}{
		"test query without timezone": {
			query:    &metadata.Query{},
			expected: `SELECT "numSeries" AS _value, "time" AS _time, *::tag FROM "database" WHERE time > 1718175000000000000 and time < 1718175600000000000 LIMIT 10`,
		},
		"test query with offset": {
			query: &metadata.Query{
				OffsetInfo: metadata.OffSetInfo{
					Limit:  100,
					SLimit: 50,
				},
			},
			expected: `SELECT "numSeries" AS _value, "time" AS _time, *::tag FROM "database" WHERE time > 1718175000000000000 and time < 1718175600000000000 LIMIT 10 SLIMIT 50`,
		},
		"test query with timezone": {
			query: &metadata.Query{
				Timezone: "Asia/Shanghai",
			},
			expected: `SELECT "numSeries" AS _value, "time" AS _time, *::tag FROM "database" WHERE time > 1718175000000000000 and time < 1718175600000000000 LIMIT 10 TZ('Asia/Shanghai')`,
		},
		"test query aggregation": {
			query: &metadata.Query{
				Aggregates: metadata.Aggregates{
					{
						Name:   "count",
						Window: time.Minute * 5,
						Dimensions: []string{
							"database",
						},
					},
				},
			},
			expected: `SELECT count("numSeries") AS _value, "time" AS _time FROM "database" WHERE time > 1718175000000000000 and time < 1718175600000000000 GROUP BY "database", time(5m0s) LIMIT 10`,
		},
		"test aggregation query reference": {
			query: &metadata.Query{
				Aggregates: metadata.Aggregates{
					{
						Name: "count",
						Dimensions: []string{
							"database",
						},
					},
				},
			},
			expected: `SELECT count("numSeries") AS _value, "time" AS _time FROM "database" WHERE time > 1718175000000000000 and time < 1718175600000000000 GROUP BY "database" LIMIT 10`,
		},
	}
	start := time.UnixMilli(1718175000000)
	end := time.UnixMilli(1718175600000)
	option := &Options{
		Host:     "127.0.0.1",
		Port:     80,
		Timeout:  time.Hour,
		MaxLimit: 1e1,
	}

	for n, c := range testCases {
		t.Run(n, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			instance, err := NewInstance(ctx, option)
			if err != nil {
				log.Fatalf(ctx, err.Error())
			}
			if c.query.DB == "" {
				c.query.DB = db
			}
			if c.query.Measurement == "" {
				c.query.Measurement = measurement
			}
			if c.query.Field == "" {
				c.query.Field = field
			}
			sql, err := instance.makeSQL(ctx, c.query, start, end)
			if c.err != nil {
				assert.Equal(t, c.err, err)
			} else {
				assert.Equal(t, c.expected, sql)
			}
		})
	}
}
