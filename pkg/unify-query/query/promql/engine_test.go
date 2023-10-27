// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
)

// TestQueryRange
func TestQueryRange(t *testing.T) {
	ctrl, stubs := FakeData(t)
	defer ctrl.Finish()
	defer stubs.Reset()

	var totalSQL string
	var err error
	stubs.Stub(&MakeInfluxdbQuerys, func(ctx context.Context, hints *storage.SelectHints, matchers ...*labels.Matcher) ([]influxdb.SQLInfo, error) {
		var sqlInfos []influxdb.SQLInfo
		sqlInfos, err = makeInfluxdbQuery(ctx, hints, matchers...)
		if len(sqlInfos) > 0 {
			totalSQL = sqlInfos[0].SQL
		}
		return sqlInfos, err
	})

	start := time.Unix(100000, 0)
	end := time.Unix(10000000, 0)
	testCases := map[string]struct {
		promQL   string
		interval time.Duration
		aggrs    AggrMethods
		iscount  bool
		influxQL string
	}{
		"avg": {
			promQL:   `avg(t) by (a, b)`,
			interval: 5 * time.Minute,
			influxQL: `select last("value") as _value,time as _time from "test_metric" where time >= 99700000000000 and time < 9999999999000000 group by *,time(5m0s)`,
		},
		"avg by avg_over_time": {
			promQL: `avg by (a, b) (avg_over_time(t[30s]))`,
			aggrs: AggrMethods{
				{
					Name:       "avg",
					Dimensions: []string{"a", "b"},
				},
			},
			interval: time.Second * 10,
			influxQL: `select mean("value") as _value,time as _time from "test_metric" where time >= 99970000000000 and time < 9999999999000000 group by "a","b",time(30s)`,
		},
		"avg without avg_over_time": {
			promQL: `avg without(a, b) (avg_over_time(t[30s]))`,
			aggrs: AggrMethods{
				{
					Name:       "avg",
					Dimensions: []string{"a", "b"},
					Without:    true,
				},
			},
			interval: time.Second * 10,
			influxQL: `select "value" as _value,time as _time,*::tag from "test_metric" where time >= 99970000000000 and time < 9999999999000000`,
		},
		"irate": {
			promQL: `avg(irate(t[30s]))`,
			aggrs: AggrMethods{
				{
					Name: "avg",
				},
			},
			interval: time.Minute,
			influxQL: `select "value" as _value,time as _time,*::tag from "test_metric" where time >= 99970000000000 and time < 9999999999000000`,
		},
		// todo: 该方法 saas 屏蔽了，是否需要开放
		"count_value": {
			promQL:   `count_values("test", t)`,
			interval: time.Minute,
			influxQL: `select "value" as _value,time as _time,*::tag from "test_metric" where time >= 99700000000000 and time < 9999999999000000`,
		},
		// todo: 该方法被 saas 屏蔽了，不能单独使用，必须配合聚合函数
		"count_over_time": {
			promQL:   `count_over_time(t[1m])`,
			interval: time.Minute,
			influxQL: `select "value" as _value,time as _time,*::tag from "test_metric" where time >= 99940000000000 and time < 9999999999000000`,
		},
		"count": {
			promQL: `count(t)`,
			aggrs: AggrMethods{
				{
					Name: "count",
				},
			},
			interval: time.Minute,
			influxQL: `select "value" as _value,time as _time,*::tag from "test_metric" where time >= 99700000000000 and time < 9999999999000000`,
		},
		"sum count_over_time": {
			promQL:  `sum(sum_over_time(t[1m]))`,
			iscount: true,
			aggrs: AggrMethods{
				{
					Name: "sum",
				},
			},
			interval: time.Minute,
			influxQL: `select count("value") as _value,time as _time from "test_metric" where time >= 99940000000000 and time < 9999999999000000 group by time(1m0s)`,
		},
		"count count_over_time": {
			promQL: `count(count_over_time(t[1m]))`,
			aggrs: AggrMethods{
				{
					Name: "count",
				},
			},
			interval: time.Minute,
			influxQL: `select "value" as _value,time as _time,*::tag from "test_metric" where time >= 99940000000000 and time < 9999999999000000`,
		},
	}

	NewEngine(&Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           5000,
		LookbackDelta:        5 * time.Minute,
		EnableNegativeOffset: true,
	})
	queryInfo := &QueryInfo{
		DataIDList: []consul.DataID{100001},
	}
	for i, c := range testCases {
		t.Run(i, func(t *testing.T) {
			ctx := context.Background()
			queryInfo.AggregateMethodList = c.aggrs
			queryInfo.IsCount = c.iscount
			ctx, err = QueryInfoIntoContext(ctx, "t", "test_metric", queryInfo)
			assert.Nil(t, err)
			_, err = QueryRange(ctx, c.promQL, start, end, c.interval)
			assert.Equal(t, c.influxQL, totalSQL)
		})
	}
}
