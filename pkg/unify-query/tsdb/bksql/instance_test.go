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
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestInstance_QueryRaw(t *testing.T) {

	ctx := context.Background()
	mock.Init()

	ins, err := NewInstance(ctx, Options{
		Address:   "localhost",
		Timeout:   time.Minute,
		MaxLimit:  1e4,
		Tolerance: 5,
	})
	if err != nil {
		log.Fatalf(ctx, err.Error())
	}
	ins.client = mockClient()
	if err != nil {
		log.Fatalf(ctx, err.Error())
	}
	end := time.Now()
	start := end.Add(time.Minute * -5)

	db := "132_lol_new_login_queue_login_1min"
	field := "login_rate"

	for idx, c := range []struct {
		query    *metadata.Query
		expected string
	}{
		{
			query: &metadata.Query{
				BkSqlCondition: "namespace = 'gz100' OR namespace = 'bgp2\\-new'",
				OffsetInfo:     metadata.OffSetInfo{Limit: 10},
			},
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
			ss := ins.QueryRaw(ctx, c.query, start, end)
			for ss.Next() {
				series := ss.At()
				lbs := series.Labels()
				it := series.Iterator(nil)
				fmt.Printf("%s\n", lbs)
				fmt.Printf("------------------------------------------------\n")
				for it.Next() == chunkenc.ValFloat {
					ts, val := it.At()
					tt := time.UnixMilli(ts)

					fmt.Printf("%g %s\n", val, tt.Format("2006-01-02 15:04:05"))
				}
				if it.Err() != nil {
					panic(it.Err())
				}
			}

			if ws := ss.Warnings(); len(ws) > 0 {
				panic(ws)
			}

			if ss.Err() != nil {
				log.Errorf(ctx, ss.Err().Error())
			}
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
			expected: "SELECT COUNT(`login_rate`) AS `_value_`, MAX((`dtEventTimeStamp` - (`dtEventTimeStamp` % 15000))) AS `_timestamp_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND (namespace REGEXP '^(bgp2\\-new|gz100)$') GROUP BY `namespace`, (`dtEventTimeStamp` - (`dtEventTimeStamp` % 15000)) ORDER BY `_timestamp_` ASC LIMIT 200005",
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

			expected: "SELECT SUM(`value`) AS `_value_` FROM `132_hander_opmon_avg` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 ORDER BY `_timestamp_` ASC LIMIT 200005",
		},
		{
			query: &metadata.Query{
				DB:    "100133_ieod_logsearch4_errorlog_p.doris",
				Field: "value",
				Size:  5,
			},

			expected: "SELECT *, `value` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `100133_ieod_logsearch4_errorlog_p.doris` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 ORDER BY `_timestamp_` ASC LIMIT 5",
		},
		{
			query: &metadata.Query{
				DB:    "100133_ieod_logsearch4_errorlog_p.doris",
				Field: "gseIndex",
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

			expected: "SELECT COUNT(`gseIndex`) AS `_value_` FROM `100133_ieod_logsearch4_errorlog_p.doris` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 GROUP BY `ip` ORDER BY `_timestamp_` ASC LIMIT 5",
		},
	}

	ins := &Instance{}

	for i, c := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			sql, err := ins.bkSql(ctx, c.query, start, end)
			assert.Nil(t, err)
			if err == nil {
				assert.Equal(t, c.expected, sql)
			}
		})
	}
}
