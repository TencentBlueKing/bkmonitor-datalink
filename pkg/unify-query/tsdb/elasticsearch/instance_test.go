// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestInstance_queryReference(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	mock.Init()
	address := viper.GetString("mock.es.address")
	username := viper.GetString("mock.es.username")
	password := viper.GetString("mock.es.password")
	timeout := viper.GetDuration("mock.es.timeout")
	maxSize := viper.GetInt("mock.es.max_size")
	maxRouting := viper.GetInt("mock.es.max_routing")

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	metadata.GetQueryParams(ctx).SetDataSource(structured.BkLog)

	ins, err := NewInstance(ctx, &InstanceOption{
		Address:    address,
		Username:   username,
		Password:   password,
		MaxRouting: maxRouting,
		MaxSize:    maxSize,
	})
	if err != nil {
		t.Fatal(err)
		return
	}

	defaultStart := time.UnixMilli(1717027200000)
	defaultEnd := time.UnixMilli(1717027230000)

	db := "2_bklog_bkapigateway_esb_container1_*_read"
	field := "gseIndex"

	for idx, c := range map[string]struct {
		query *metadata.Query
		start time.Time
		end   time.Time

		isReference bool

		expected interface{}
	}{
		"nested query + query string 测试": {
			query: &metadata.Query{
				DB:    "2_bklog_nested_field_test_*_read",
				Field: "fields.field_name",
				From:  0,
				Size:  10,
				Orders: metadata.Orders{
					FieldTime: false,
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "fields.field_name",
							Operator:      "eq",
							Value:         []string{"bk-dev-4"},
						},
					},
				},
				QueryString: "fields.field_name: bk-dev-3",
			},
			start: time.UnixMilli(1717482000000),
			end:   time.UnixMilli(1717482160000),
		},
		"nested aggregate + query 测试": {
			query: &metadata.Query{
				DB:    "2_bklog_nested_field_test_*_read",
				Field: "fields.field_name",
				From:  0,
				Size:  10,
				Orders: metadata.Orders{
					FieldTime: false,
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "fields.field_name",
							Operator:      "eq",
							Value:         []string{"bk-dev-3"},
						},
					},
				},
				Aggregates: metadata.Aggregates{
					{
						Name:   Count,
						Window: time.Minute,
					},
				},
			},
			start: time.UnixMilli(1717482000000),
			end:   time.UnixMilli(1717482160000),
		},
		"统计 __ext.io_kubernetes_pod 不为空的文档数量": {
			query: &metadata.Query{
				DB:    db,
				Field: "__ext.io_kubernetes_pod",
				From:  0,
				Size:  10,
				Orders: metadata.Orders{
					FieldTime: false,
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "__ext.io_kubernetes_pod",
							Operator:      "ncontains",
							Value:         []string{""},
						},
					},
				},
				Aggregates: metadata.Aggregates{
					{
						Name: Count,
					},
				},
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"统计 __ext.io_kubernetes_pod 不为空的去重文档数量": {
			query: &metadata.Query{
				DB:    db,
				Field: "__ext.io_kubernetes_pod",
				From:  0,
				Size:  10,
				Orders: metadata.Orders{
					FieldTime: false,
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "__ext.io_kubernetes_pod",
							Operator:      "ncontains",
							Value:         []string{""},
						},
					},
				},
				Aggregates: metadata.Aggregates{
					{
						Name: Cardinality,
					},
				},
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"获取 10条 不 field 为空的原始数据": {
			query: &metadata.Query{
				DB:    db,
				Field: field,
				From:  0,
				Size:  10,
				Orders: metadata.Orders{
					FieldTime: false,
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: field,
							Operator:      "ncontains",
							Value:         []string{""},
						},
					},
				},
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"获取 10条 原始数据": {
			query: &metadata.Query{
				DB:    db,
				Field: field,
				From:  0,
				Size:  10,
				Orders: metadata.Orders{
					FieldTime: false,
				},
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"使用 promql 计算平均值 sum(count_over_time(field[1m]))": {
			query: &metadata.Query{
				DB:    db,
				Field: field,
				From:  0,
				Size:  20,
				Aggregates: metadata.Aggregates{
					{
						Name: Count,
						Dimensions: []string{
							"__ext.io_kubernetes_pod",
							"__ext.container_name",
						},
						Window: time.Minute * 2,
					},
				},
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"使用非时间聚合统计数量": {
			query: &metadata.Query{
				DB:    db,
				Field: field,
				From:  0,
				Size:  3,
				Aggregates: metadata.Aggregates{
					{
						Name: Count,
					},
				},
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"获取 50 分位值": {
			query: &metadata.Query{
				DB:    db,
				Field: field,
				From:  0,
				Size:  20,
				Aggregates: metadata.Aggregates{
					{
						Name: Percentiles,
						Args: []interface{}{
							50.0,
						},
					},
				},
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"获取 50, 90 分支值，同时按 1分钟时间聚合": {
			query: &metadata.Query{
				DB:    db,
				Field: field,
				From:  0,
				Size:  20,
				Aggregates: metadata.Aggregates{
					{
						Name: Percentiles,
						Args: []interface{}{
							50.0, 90.0,
						},
					},
					{
						Name:   DateHistogram,
						Window: time.Minute,
					},
				},
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"根据 field 字段聚合计算数量，同时根据值排序": {
			query: &metadata.Query{
				DB:    db,
				Field: field,
				From:  0,
				Size:  10,
				Aggregates: metadata.Aggregates{
					{
						Name: Count,
						Dimensions: []string{
							field,
						},
					},
				},
				Orders: map[string]bool{
					FieldValue: true,
				},
			},
			start: defaultStart,
			end:   defaultEnd,
		},
	} {
		t.Run(fmt.Sprintf("testing run: %s", idx), func(t *testing.T) {
			var output strings.Builder

			ss := ins.QueryRaw(ctx, c.query, c.start, c.end)
			if err != nil {
				log.Fatalf(ctx, err.Error())
				return
			}

			for ss.Next() {
				series := ss.At()
				lbs := series.Labels()
				it := series.Iterator(nil)
				output.WriteString("series: " + lbs.String() + "\n")
				for it.Next() == chunkenc.ValFloat {
					ts, val := it.At()
					tt := time.UnixMilli(ts)

					output.WriteString("sample: " + fmt.Sprintf("%g %s\n", val, tt.Format("2006-01-02 15:04:05")) + "\n")
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

			fmt.Println("output:")
			fmt.Println(output.String())
		})
	}
}
