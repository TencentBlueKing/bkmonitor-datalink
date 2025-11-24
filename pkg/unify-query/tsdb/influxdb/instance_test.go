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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
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
			expected: `SELECT "numSeries" AS _value, *::tag, "time" AS _time FROM "database" WHERE time > 1718175000000000000 and time < 1718175600000000000 LIMIT 10`,
		},
		"test query with offset": {
			query: &metadata.Query{
				OffsetInfo: metadata.OffSetInfo{
					Limit:  100,
					SLimit: 50,
				},
			},
			expected: `SELECT "numSeries" AS _value, *::tag, "time" AS _time FROM "database" WHERE time > 1718175000000000000 and time < 1718175600000000000 LIMIT 10 SLIMIT 50`,
		},
		"test query with timezone": {
			query: &metadata.Query{
				Timezone: "Asia/Shanghai",
			},
			expected: `SELECT "numSeries" AS _value, *::tag, "time" AS _time FROM "database" WHERE time > 1718175000000000000 and time < 1718175600000000000 LIMIT 10 TZ('Asia/Shanghai')`,
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
		Port:     6371,
		Timeout:  time.Minute * 5,
		MaxLimit: 1e1,
	}
	instance, err := NewInstance(ctx, option)

	for n, c := range testCases {
		t.Run(n, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			if err != nil {
				log.Fatalf(ctx, "%s", err.Error())
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

func TestInstance_QuerySeriesSet(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	option := &Options{
		Host:     "127.0.0.1",
		Port:     12302,
		Timeout:  time.Minute * 5,
		MaxLimit: 1e1,
		Curl:     &curl.HttpCurl{},
	}
	instance, _ := NewInstance(ctx, option)
	influxdb.MockSpaceRouter(ctx)

	mock.InfluxDB.Set(map[string]any{
		`SELECT count("free") AS _value, "time" AS _time FROM swap WHERE time > 1730197718000000000 and time < 1730201318000000000 GROUP BY "bk_biz_id", time(1m0s) LIMIT 10`: `{"results":[{"statement_id":0,"series":[{"name":"swap","tags":{"bk_biz_id":"2"},"columns":["_time","_value"],"values":[["2024-10-29T18:57:00+08:00",92],["2024-10-29T18:58:00+08:00",92],["2024-10-29T18:59:00+08:00",92],["2024-10-29T19:00:00+08:00",92],["2024-10-29T19:01:00+08:00",92],["2024-10-29T19:02:00+08:00",92],["2024-10-29T19:03:00+08:00",92],["2024-10-29T19:04:00+08:00",92],["2024-10-29T19:05:00+08:00",92],["2024-10-29T19:06:00+08:00",92],["2024-10-29T19:07:00+08:00",92],["2024-10-29T19:08:00+08:00",92],["2024-10-29T19:09:00+08:00",92],["2024-10-29T19:10:00+08:00",92],["2024-10-29T19:11:00+08:00",92],["2024-10-29T19:12:00+08:00",92],["2024-10-29T19:13:00+08:00",92],["2024-10-29T19:14:00+08:00",92],["2024-10-29T19:15:00+08:00",92],["2024-10-29T19:16:00+08:00",92],["2024-10-29T19:17:00+08:00",92],["2024-10-29T19:18:00+08:00",92],["2024-10-29T19:19:00+08:00",92],["2024-10-29T19:20:00+08:00",92],["2024-10-29T19:21:00+08:00",92],["2024-10-29T19:22:00+08:00",92],["2024-10-29T19:23:00+08:00",92],["2024-10-29T19:24:00+08:00",92],["2024-10-29T19:25:00+08:00",92],["2024-10-29T19:26:00+08:00",92],["2024-10-29T19:27:00+08:00",92],["2024-10-29T19:28:00+08:00",92],["2024-10-29T19:29:00+08:00",92],["2024-10-29T19:30:00+08:00",92],["2024-10-29T19:31:00+08:00",92],["2024-10-29T19:32:00+08:00",92],["2024-10-29T19:33:00+08:00",92],["2024-10-29T19:34:00+08:00",92],["2024-10-29T19:35:00+08:00",92],["2024-10-29T19:36:00+08:00",92],["2024-10-29T19:37:00+08:00",92],["2024-10-29T19:38:00+08:00",92],["2024-10-29T19:39:00+08:00",92],["2024-10-29T19:40:00+08:00",92],["2024-10-29T19:41:00+08:00",92],["2024-10-29T19:42:00+08:00",92],["2024-10-29T19:43:00+08:00",92],["2024-10-29T19:44:00+08:00",92],["2024-10-29T19:45:00+08:00",92],["2024-10-29T19:46:00+08:00",92],["2024-10-29T19:47:00+08:00",92],["2024-10-29T19:48:00+08:00",92],["2024-10-29T19:49:00+08:00",92],["2024-10-29T19:50:00+08:00",92],["2024-10-29T19:51:00+08:00",92],["2024-10-29T19:52:00+08:00",92],["2024-10-29T19:53:00+08:00",92],["2024-10-29T19:54:00+08:00",92],["2024-10-29T19:55:00+08:00",92],["2024-10-29T19:56:00+08:00",92],["2024-10-29T19:57:00+08:00",92],["2024-10-29T19:58:00+08:00",92]]}]}]}`,
	})

	testCases := map[string]struct {
		query    *metadata.Query
		expected string
	}{
		"test_1": {
			query: &metadata.Query{
				DataSource:      "bkmonitor",
				TableID:         "system.swap",
				DB:              "system",
				Measurement:     "swap",
				Measurements:    []string{"swap"},
				Field:           "free",
				Fields:          []string{"free"},
				MeasurementType: redis.BKTraditionalMeasurement,
				MetricNames:     []string{"bkmonitor:system:swap:free"},
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Window:     time.Minute,
						Dimensions: []string{"bk_biz_id"},
					},
				},
			},
			expected: `[{"labels":[{"name":"__name__","value":"bkmonitor:system:swap:free"},{"name":"bk_biz_id","value":"2"}],"samples":[{"value":92,"timestamp":1730199420000},{"value":92,"timestamp":1730199480000},{"value":92,"timestamp":1730199540000},{"value":92,"timestamp":1730199600000},{"value":92,"timestamp":1730199660000},{"value":92,"timestamp":1730199720000},{"value":92,"timestamp":1730199780000},{"value":92,"timestamp":1730199840000},{"value":92,"timestamp":1730199900000},{"value":92,"timestamp":1730199960000},{"value":92,"timestamp":1730200020000},{"value":92,"timestamp":1730200080000},{"value":92,"timestamp":1730200140000},{"value":92,"timestamp":1730200200000},{"value":92,"timestamp":1730200260000},{"value":92,"timestamp":1730200320000},{"value":92,"timestamp":1730200380000},{"value":92,"timestamp":1730200440000},{"value":92,"timestamp":1730200500000},{"value":92,"timestamp":1730200560000},{"value":92,"timestamp":1730200620000},{"value":92,"timestamp":1730200680000},{"value":92,"timestamp":1730200740000},{"value":92,"timestamp":1730200800000},{"value":92,"timestamp":1730200860000},{"value":92,"timestamp":1730200920000},{"value":92,"timestamp":1730200980000},{"value":92,"timestamp":1730201040000},{"value":92,"timestamp":1730201100000},{"value":92,"timestamp":1730201160000},{"value":92,"timestamp":1730201220000},{"value":92,"timestamp":1730201280000},{"value":92,"timestamp":1730201340000},{"value":92,"timestamp":1730201400000},{"value":92,"timestamp":1730201460000},{"value":92,"timestamp":1730201520000},{"value":92,"timestamp":1730201580000},{"value":92,"timestamp":1730201640000},{"value":92,"timestamp":1730201700000},{"value":92,"timestamp":1730201760000},{"value":92,"timestamp":1730201820000},{"value":92,"timestamp":1730201880000},{"value":92,"timestamp":1730201940000},{"value":92,"timestamp":1730202000000},{"value":92,"timestamp":1730202060000},{"value":92,"timestamp":1730202120000},{"value":92,"timestamp":1730202180000},{"value":92,"timestamp":1730202240000},{"value":92,"timestamp":1730202300000},{"value":92,"timestamp":1730202360000},{"value":92,"timestamp":1730202420000},{"value":92,"timestamp":1730202480000},{"value":92,"timestamp":1730202540000},{"value":92,"timestamp":1730202600000},{"value":92,"timestamp":1730202660000},{"value":92,"timestamp":1730202720000},{"value":92,"timestamp":1730202780000},{"value":92,"timestamp":1730202840000},{"value":92,"timestamp":1730202900000},{"value":92,"timestamp":1730202960000},{"value":92,"timestamp":1730203020000},{"value":92,"timestamp":1730203080000}],"exemplars":null,"histograms":null}]`,
		},
	}

	start := time.Unix(1730197718, 0)
	end := time.Unix(1730201318, 0)

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			ss := instance.QuerySeriesSet(ctx, c.query, start, end)

			timeSeries, err := mock.SeriesSetToTimeSeries(ss)
			if err != nil {
				log.Fatalf(ctx, "%s", err.Error())
			}
			assert.Equal(t, c.expected, timeSeries.String())
		})
	}
}
