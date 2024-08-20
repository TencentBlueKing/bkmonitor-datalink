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
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/bkapi"
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
	sourceType := viper.GetString("mock.es.source_type")

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	metadata.GetQueryParams(ctx).SetDataSource(structured.BkLog)

	if sourceType == "bkdata" {
		address = bkapi.GetBkDataApi().Url("es")
	}

	ins, err := NewInstance(ctx, &InstanceOption{
		Address:    address,
		Username:   username,
		Password:   password,
		MaxRouting: maxRouting,
		MaxSize:    maxSize,
		SourceType: sourceType,
	})
	if err != nil {
		t.Fatal(err)
		return
	}

	defaultEnd := time.Now()
	defaultStart := defaultEnd.Add(time.Hour * -1)

	//db := "2_bklog_bk_unify_query_*_read"
	//field := "gseIndex"

	// bkdata db
	defaultEnd = time.UnixMilli(1722527999000)
	defaultStart = time.UnixMilli(1717171200000)

	db := "39_bklog_bkaudit_plugin_20240723_66b8acde57_202407*"
	field := "dtEventTimeStamp"

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
			start: defaultStart,
			end:   defaultEnd,
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

func TestInstance_getAlias(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	inst, err := NewInstance(ctx, &InstanceOption{
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
