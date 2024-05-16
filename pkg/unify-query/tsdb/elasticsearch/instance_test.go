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
	"os"
	"testing"
	"time"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestInstance_queryReference(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	mock.Init()

	err := os.Setenv("UNIFY-QUERY-CONFIG-FILE-PATH", "")
	if err != nil {
		log.Fatalf(ctx, err.Error())
		return
	}

	viper.SetDefault("mock.test", "_raw")

	a := viper.GetString("mock.test")
	fmt.Println(a)

	address := viper.GetString("mock.es.address")
	username := viper.GetString("mock.es.username")
	password := viper.GetString("mock.es.password")
	timeout := viper.GetDuration("mock.es.timeout")
	maxSize := viper.GetInt("mock.es.max_size")
	maxRouting := viper.GetInt("mock.es.max_routing")

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

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

	defaultEnd := time.Now()
	defaultStart := defaultEnd.Add(-1 * time.Hour)

	db := "2_bklog_bkapigateway_esb_container1"
	field := "gseIndex"

	for idx, c := range map[string]struct {
		query *metadata.Query
		start time.Time
		end   time.Time

		expected interface{}
	}{
		"统计 __ext.io_kubernetes_pod 不为空的文档数量": {
			query: &metadata.Query{
				QueryString: "",
				DB:          db,
				Field:       "__ext.io_kubernetes_pod",
				From:        0,
				Size:        10,
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
				AggregateMethodList: metadata.AggregateMethodList{
					{
						Name: Count,
					},
				},
				IsNotPromQL: true,
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"统计 __ext.io_kubernetes_pod 不为空的去重文档数量": {
			query: &metadata.Query{
				QueryString: "",
				DB:          db,
				Field:       "__ext.io_kubernetes_pod",
				From:        0,
				Size:        10,
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
				AggregateMethodList: metadata.AggregateMethodList{
					{
						Name: Cardinality,
					},
				},
				IsNotPromQL: true,
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"获取 10条 不 field 为空的原始数据": {
			query: &metadata.Query{
				QueryString: "",
				DB:          db,
				Field:       field,
				From:        0,
				Size:        10,
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
				IsNotPromQL: true,
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"获取 10条 原始数据": {
			query: &metadata.Query{
				QueryString: "",
				DB:          db,
				Field:       field,
				From:        0,
				Size:        10,
				Orders: metadata.Orders{
					FieldTime: false,
				},
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"使用 promql 计算平均值 avg(avg_over_time(field[1m]))": {
			query: &metadata.Query{
				QueryString: "",
				DB:          db,
				Field:       field,
				From:        0,
				Size:        20,
				TimeAggregation: &metadata.TimeAggregation{
					Function:       AvgOT,
					WindowDuration: time.Minute * 1,
				},
				AggregateMethodList: metadata.AggregateMethodList{
					{
						Name: Avg,
						Dimensions: []string{
							"__ext.io_kubernetes_pod",
							"__ext.container_name",
						},
					},
				},
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"使用非时间聚合统计数量": {
			query: &metadata.Query{
				QueryString: "",
				DB:          db,
				Field:       field,
				From:        0,
				Size:        3,
				AggregateMethodList: metadata.AggregateMethodList{
					{
						Name: Count,
					},
				},
				IsNotPromQL: true,
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"获取 50 分位值": {
			query: &metadata.Query{
				QueryString: "",
				DB:          db,
				Field:       field,
				From:        0,
				Size:        20,
				AggregateMethodList: metadata.AggregateMethodList{
					{
						Name: Percentiles,
						Args: []interface{}{
							50.0,
						},
					},
				},
				IsNotPromQL: true,
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"获取 50, 90 分支值，同时按 1分钟时间聚合": {
			query: &metadata.Query{
				QueryString: "",
				DB:          db,
				Field:       field,
				From:        0,
				Size:        20,
				AggregateMethodList: metadata.AggregateMethodList{
					{
						Name: DateHistogram,
						Args: []interface{}{
							"1m",
						},
					},
					{
						Name: Percentiles,
						Args: []interface{}{
							50.0, 90.0,
						},
					},
				},
				IsNotPromQL: true,
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"根据 field 字段聚合计算数量，同时根据值排序": {
			query: &metadata.Query{
				QueryString: "",
				DB:          db,
				Field:       field,
				From:        0,
				Size:        10,
				AggregateMethodList: metadata.AggregateMethodList{
					{
						Name: Count,
						Dimensions: []string{
							field,
						},
					},
				},
				IsNotPromQL: true,
				Orders: map[string]bool{
					FieldValue: true,
				},
			},
			start: defaultStart,
			end:   defaultEnd,
		},
	} {
		t.Run(fmt.Sprintf("testing run: %s", idx), func(t *testing.T) {
			refName := "a"
			reference := metadata.QueryReference{
				refName: &metadata.QueryMetric{
					QueryList: metadata.QueryList{
						c.query,
					},
					ReferenceName: refName,
				},
			}
			err = metadata.SetQueryReference(ctx, reference)
			if err != nil {
				log.Fatalf(ctx, err.Error())
				return
			}

			matrix, err := ins.QueryRange(ctx, refName, c.start, c.end, 0)
			if err != nil {
				log.Fatalf(ctx, err.Error())
				return
			}

			fmt.Printf("is promql: %vs\n", !c.query.IsNotPromQL)
			left := c.end.Unix() - c.start.Unix()
			fmt.Printf("range time: %ds\n", left)

			fmt.Println(matrix.String())

			//for _, r := range matrix {
			//	lbs := make([]string, 0)
			//	for _, lb := range r.Metric {
			//		lbs = append(lbs, fmt.Sprintf("%s=%s", lb.Name, lb.Value))
			//	}
			//
			//	fmt.Printf("name: %s\n", strings.Join(lbs, ", "))
			//	fmt.Printf("sample num: %d\n", len(r.String()))
			//	i := 0
			//	for {
			//		if len(r.GetSamples()) > i {
			//			sample := r.GetSamples()[i]
			//			fmt.Printf("sample example %d: timestamp: %d, value: %.f\n", i+1, sample.GetTimestamp(), sample.GetValue())
			//			if i >= 4 {
			//				break
			//			}
			//		} else {
			//			break
			//		}
			//		i++
			//	}
			//}
		})
	}
}
