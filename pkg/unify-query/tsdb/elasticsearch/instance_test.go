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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestInstance_queryReference(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	ins, err := NewInstance(ctx, &InstanceOption{
		Address: mock.EsUrl,
		Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
		return
	}

	defaultEnd := time.UnixMilli(1722527999000)
	defaultStart := time.UnixMilli(1717171200000)

	db := "es_index"
	field := "dtEventTimeStamp"

	mock.Es.Set(map[string]any{
		`{"_source":{"includes":["group","user.first","user.last"]},"from":0,"query":{"bool":{"filter":[{"nested":{"path":"user","query":{"match_phrase":{"user.first":{"query":"John"}}}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1717171200,"include_lower":true,"include_upper":true,"to":1722527999}}},{"query_string":{"analyze_wildcard":true,"query":"group: fans"}}]}},"size":5}`: `{"took":1,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":0.0,"hits":[{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"aS3KjpEBbwEm76LbcH1G","_score":0.0,"_source":{"user":[{"last":"Smith","first":"John"},{"last":"White","first":"Alice"}],"group":"fans"}}]}}`,
	})

	for idx, c := range map[string]struct {
		query *metadata.Query
		start time.Time
		end   time.Time

		isReference bool

		expected interface{}
	}{
		"nested query + query string 测试": {
			query: &metadata.Query{
				DB:    db,
				Field: "group",
				From:  0,
				Size:  5,
				Orders: metadata.Orders{
					FieldTime: false,
				},
				StorageType: consul.ElasticsearchStorageType,
				Source:      []string{"group", "user.first", "user.last"},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "user.first",
							Operator:      "eq",
							Value:         []string{"John"},
						},
					},
				},
				QueryString: "group: fans",
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"nested aggregate + query 测试": {
			query: &metadata.Query{
				DB:    db,
				Field: "fields.field_name",
				//From:  0,
				//Size:  10,
				Orders: metadata.Orders{
					FieldTime: false,
				},
				StorageType: consul.ElasticsearchStorageType,
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
				StorageType: consul.ElasticsearchStorageType,
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
				StorageType: consul.ElasticsearchStorageType,
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
				StorageType: consul.ElasticsearchStorageType,
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
				DB:          db,
				Field:       field,
				From:        0,
				Size:        10,
				StorageType: consul.ElasticsearchStorageType,
				TimeField: metadata.TimeField{
					Name: "dtEventTimeStamp",
					Type: TimeFieldTypeTime,
					Unit: Millisecond,
				},
				Orders: metadata.Orders{
					FieldTime: false,
				},
			},
			start: defaultStart,
			end:   defaultEnd,
		},
		"使用 promql 计算平均值 sum(count_over_time(field[1m]))": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				From:        0,
				Size:        20,
				StorageType: consul.ElasticsearchStorageType,
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
				DB:          db,
				Field:       field,
				From:        0,
				Size:        3,
				StorageType: consul.ElasticsearchStorageType,
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
				DB:          db,
				Field:       field,
				From:        0,
				Size:        20,
				StorageType: consul.ElasticsearchStorageType,
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
				DB:          db,
				Field:       field,
				From:        0,
				Size:        20,
				StorageType: consul.ElasticsearchStorageType,
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
				DB:          db,
				Field:       field,
				From:        0,
				Size:        10,
				StorageType: consul.ElasticsearchStorageType,
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
			if len(c.query.Aggregates) > 0 {
				ss := ins.QuerySeriesSet(ctx, c.query, c.start, c.end)
				if err != nil {
					log.Fatalf(ctx, err.Error())
				}

				timeSeries, err := function.SeriesSetToTimeSeries(ss)
				if err != nil {
					log.Fatalf(ctx, err.Error())
				}

				fmt.Println("output:")
				fmt.Println(timeSeries.String())
			} else {
				var (
					wg   sync.WaitGroup
					size int64
				)
				dataCh := make(chan map[string]any)
				wg.Add(1)
				go func() {
					defer wg.Done()
					i := 0
					for d := range dataCh {
						i++
						var s []string
						for k, v := range d {
							s = append(s, fmt.Sprintf("%s: %v", k, v))
						}
						fmt.Println(i, " - ", strings.Join(s, ", "))
					}
				}()

				size, err = ins.QueryRawData(ctx, c.query, c.start, c.end, dataCh)
				close(dataCh)
				fmt.Printf("read data %d\n", size)

				wg.Wait()
				if err != nil {
					panic(err)
				}
			}

		})
	}
}

func TestInstance_getAlias(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	inst, err := NewInstance(ctx, &InstanceOption{
		Address: mock.EsUrl,
		Timeout: time.Minute,
	})
	if err != nil {
		log.Panicf(ctx, err.Error())
	}

	for name, c := range map[string]struct {
		start       time.Time
		end         time.Time
		timezone    string
		db          string
		needAddTime bool

		expected []string
	}{
		"3d with UTC": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 1, 3, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			expected:    []string{"db_test_20240101*", "db_test_20240102*", "db_test_20240103*"},
		},
		"change month with Asia/ShangHai": {
			start:       time.Date(2024, 1, 25, 7, 10, 5, 0, time.UTC),
			end:         time.Date(2024, 2, 2, 6, 1, 4, 10, time.UTC),
			needAddTime: true,
			timezone:    "Asia/ShangHai",
			expected:    []string{"db_test_20240125*", "db_test_20240126*", "db_test_20240127*", "db_test_20240128*", "db_test_20240129*", "db_test_20240130*", "db_test_20240131*", "db_test_20240201*", "db_test_20240202*"},
		},
		"2d with Asia/ShangHai": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 1, 3, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			timezone:    "Asia/ShangHai",
			expected:    []string{"db_test_20240102*", "db_test_20240103*", "db_test_20240104*"},
		},
		"14d with Asia/ShangHai": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 1, 15, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			timezone:    "Asia/ShangHai",
			expected:    []string{"db_test_20240102*", "db_test_20240103*", "db_test_20240104*", "db_test_20240105*", "db_test_20240106*", "db_test_20240107*", "db_test_20240108*", "db_test_20240109*", "db_test_20240110*", "db_test_20240111*", "db_test_20240112*", "db_test_20240113*", "db_test_20240114*", "db_test_20240115*", "db_test_20240116*"},
		},
		"16d with Asia/ShangHai": {
			start:       time.Date(2024, 1, 15, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 2, 10, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			timezone:    "Asia/ShangHai",
			expected:    []string{"db_test_202401*", "db_test_202402*"},
		},
		"15d with Asia/ShangHai": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 1, 16, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			timezone:    "Asia/ShangHai",
			expected:    []string{"db_test_202401*"},
		},
		"6m with Asia/ShangHai": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 7, 1, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			timezone:    "Asia/ShangHai",
			expected:    []string{"db_test_202401*", "db_test_202402*", "db_test_202403*", "db_test_202404*", "db_test_202405*", "db_test_202406*", "db_test_202407*"},
		},
		"7m with Asia/ShangHai": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 8, 1, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			timezone:    "Asia/ShangHai",
			expected:    []string{"db_test_202402*", "db_test_202403*", "db_test_202404*", "db_test_202405*", "db_test_202406*", "db_test_202407*", "db_test_202408*"},
		},
		"2m and db": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 3, 1, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			db:          "db_test,db_test_clone",
			expected:    []string{"db_test_202401*", "db_test_clone_202401*", "db_test_202402*", "db_test_clone_202402*", "db_test_202403*", "db_test_clone_202403*"},
		},
		"2m and db and not need add time": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 3, 1, 20, 0, 0, 0, time.UTC),
			needAddTime: false,
			db:          "db_test,db_test_clone",
			expected:    []string{"db_test", "db_test_clone"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if c.db == "" {
				c.db = "db_test"
			}
			ctx = metadata.InitHashID(ctx)
			actual, err := inst.getAlias(ctx, c.db, c.needAddTime, c.start, c.end, c.timezone)
			assert.Nil(t, err)
			assert.Equal(t, c.expected, actual)
		})
	}
}
