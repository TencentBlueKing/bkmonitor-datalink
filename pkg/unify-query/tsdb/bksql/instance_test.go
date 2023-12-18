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

	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestInstance_QueryRaw(t *testing.T) {

	ctx := mock.Init(context.Background())
	cli := mockClient()

	ins := &Instance{
		Ctx:          ctx,
		IntervalTime: 3e2 * time.Millisecond,
		Timeout:      3e1 * time.Second,
		Client:       cli,
		Tolerance:    5,
	}
	end := time.Now()

	for idx, c := range []struct {
		query *metadata.Query
		hints *storage.SelectHints
	}{
		{
			query: &metadata.Query{
				Measurement:    "132_hander_opmon_avg",
				Field:          "value",
				BkSqlCondition: "instance = '5744'",
			},
			hints: &storage.SelectHints{
				Start: end.Add(time.Minute * -5).UnixMilli(),
				End:   end.UnixMilli(),
			},
		},
		{
			query: &metadata.Query{
				Measurement:    "132_hander_opmon_avg",
				Field:          "value",
				BkSqlCondition: "instance = '5744' OR instance = '11211'",
				AggregateMethodList: []metadata.AggrMethod{
					{
						Name:       "sum",
						Dimensions: []string{"instance", "application"},
					},
				},
			},
			hints: &storage.SelectHints{
				Start: end.Add(time.Minute * -30).UnixMilli(),
				End:   end.UnixMilli(),
				Step:  120000,
				Func:  "count_over_time",
				Range: 60000,
			},
		},
	} {
		t.Run(fmt.Sprintf("%d", idx), func(t *testing.T) {
			ss := ins.QueryRaw(ctx, c.query, c.hints)
			for ss.Next() {
				series := ss.At()
				lbs := series.Labels()
				it := series.Iterator(nil)
				fmt.Printf("%s\n", lbs)
				fmt.Printf("------------------------------------------------\n")
				for it.Next() == chunkenc.ValFloat {
					ts, val := it.At()
					fmt.Printf("%g %d\n", val, ts)
				}
				if it.Err() != nil {
					panic(it.Err())
				}
			}

			if ws := ss.Warnings(); len(ws) > 0 {
				panic(ws)
			}

			if ss.Err() != nil {
				panic(ss.Err())
			}
		})
	}
}

func TestInstance_bkSql(t *testing.T) {
	testCases := []struct {
		query *metadata.Query
		hints *storage.SelectHints
		sql   string
	}{
		{
			query: &metadata.Query{
				Measurement:    "132_hander_opmon_avg",
				Field:          "value",
				BkSqlCondition: "instance = '5744' OR instance = '11211'",
				AggregateMethodList: []metadata.AggrMethod{
					{
						Name:       "sum",
						Dimensions: []string{"instance", "application"},
					},
				},
			},
			hints: &storage.SelectHints{
				Start: 1701092460000,
				End:   1701096060000,
				Step:  120000,
				Func:  "count_over_time",
				Range: 60000,
			},
			sql: "SELECT COUNT(`value`) AS `value`, instance, application, minute1, MAX(dtEventTimeStamp) AS dtEventTimeStamp FROM 132_hander_opmon_avg WHERE dtEventTimeStamp >= 1701092460000 AND dtEventTimeStamp < 1701096060000 AND (instance = '5744' OR instance = '11211') GROUP BY instance, application, minute1 ORDER BY dtEventTimeStamp ASC LIMIT 200000",
		},
	}

	ins := Instance{}

	for i, c := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			ctx := mock.Init(context.Background())
			sql, _ := ins.bkSql(ctx, c.query, c.hints)
			assert.Equal(t, sql, c.sql)
		})
	}
}
