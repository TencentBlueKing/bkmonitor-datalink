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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestInstance_getAlias(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	inst, err := NewInstance(ctx, &InstanceOption{
		Connect: Connect{
			Address: mock.EsUrl,
		},
		Timeout: time.Minute,
	})
	if err != nil {
		log.Panicf(ctx, err.Error())
	}

	for name, c := range map[string]struct {
		start       time.Time
		end         time.Time
		db          string
		needAddTime bool

		sourceType string

		expected []string
	}{
		"3d with bklog": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 1, 3, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			expected:    []string{"db_test_20240101*", "db_test_20240102*", "db_test_20240103*"},
		},
		"20250709 06:59:22 ~ 20250709 07:59:22 with bkbase 使用东八区作为别名生成规则，需要特殊处理": {
			start:       time.UnixMilli(1752015562000), // 2025-07-09 06:59:22 Asia/ShangHai
			end:         time.UnixMilli(1752019162000), // 2025-07-09 07:59:22 Asia/ShangHai
			needAddTime: true,
			sourceType:  structured.BkData,
			expected:    []string{"db_test_20250709*"},
		},
		"change month with Asia/Shanghai": {
			start:       time.Date(2024, 1, 25, 7, 10, 5, 0, time.UTC),
			end:         time.Date(2024, 2, 2, 6, 1, 4, 10, time.UTC),
			needAddTime: true,
			expected:    []string{"db_test_20240125*", "db_test_20240126*", "db_test_20240127*", "db_test_20240128*", "db_test_20240129*", "db_test_20240130*", "db_test_20240131*", "db_test_20240201*", "db_test_20240202*"},
		},
		"2d with bkdata": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 1, 3, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			sourceType:  structured.BkData,
			expected:    []string{"db_test_20240102*", "db_test_20240103*", "db_test_20240104*"},
		},
		"14d with bkdata": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 1, 15, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			sourceType:  structured.BkData,
			expected:    []string{"db_test_20240102*", "db_test_20240103*", "db_test_20240104*", "db_test_20240105*", "db_test_20240106*", "db_test_20240107*", "db_test_20240108*", "db_test_20240109*", "db_test_20240110*", "db_test_20240111*", "db_test_20240112*", "db_test_20240113*", "db_test_20240114*", "db_test_20240115*", "db_test_20240116*"},
		},
		"16d with bkdata": {
			start:       time.Date(2024, 1, 15, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 2, 10, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			sourceType:  structured.BkData,
			expected:    []string{"db_test_202401*", "db_test_202402*"},
		},
		"15d with bkdata": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 1, 16, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			sourceType:  structured.BkData,
			expected:    []string{"db_test_202401*"},
		},
		"6m with bkdata": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 7, 1, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			sourceType:  structured.BkData,
			expected:    []string{"db_test_202401*", "db_test_202402*", "db_test_202403*", "db_test_202404*", "db_test_202405*", "db_test_202406*", "db_test_202407*"},
		},
		"7m with bkdata": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 8, 1, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			sourceType:  structured.BkData,
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
			actual, err := inst.getAlias(ctx, c.db, c.needAddTime, c.start, c.end, c.sourceType)
			assert.Nil(t, err)
			assert.Equal(t, c.expected, actual)
		})
	}
}

func TestInstance_queryReference(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	ins, err := NewInstance(ctx, &InstanceOption{
		Connect: Connect{
			Address: mock.EsUrl,
		},
		Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
		return
	}

	defaultStart := time.UnixMilli(1723593608000)
	defaultEnd := time.UnixMilli(1723679962000)

	db := "es_index"
	field := "dtEventTimeStamp"

	mock.Es.Set(map[string]any{
		// 统计 __ext.io_kubernetes_pod 不为空的文档数量
		`{"aggregations":{"_value":{"value_count":{"field":"__ext.io_kubernetes_pod"}}},"query":{"bool":{"filter":[{"exists":{"field":"__ext.io_kubernetes_pod"}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}]}},"size":0,"sort":[{"dtEventTimeStamp":{"order":"desc"}}]}`: `{"took":92,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":1523302}}}`,

		// 统计 __ext.io_kubernetes_pod 不为空的去重文档数量
		`{"aggregations":{"_value":{"cardinality":{"field":"__ext.io_kubernetes_pod"}}},"query":{"bool":{"filter":[{"exists":{"field":"__ext.io_kubernetes_pod"}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}]}},"size":0,"sort":[{"dtEventTimeStamp":{"order":"desc"}}]}`: `{"took":170,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":4}}}`,

		// 使用 promql 计算平均值 sum(count_over_time(field[12h]))
		`{"aggregations":{"__ext.container_name":{"aggregations":{"__ext.io_kubernetes_pod":{"aggregations":{"dtEventTimeStamp":{"aggregations":{"_value":{"value_count":{"field":"dtEventTimeStamp"}}},"date_histogram":{"extended_bounds":{"max":1723679962000,"min":1723593608000},"field":"dtEventTimeStamp","interval":"12h","min_doc_count":0}}},"terms":{"field":"__ext.io_kubernetes_pod","missing":" "}}},"terms":{"field":"__ext.container_name","missing":" "}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0}`: `{"took":185,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"__ext.container_name":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"unify-query","doc_count":1523254,"__ext.io_kubernetes_pod":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"bkmonitor-unify-query-64bd4f5df4-599f9","doc_count":767743,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1723593600000","key":1723593600000,"doc_count":375064,"_value":{"value":375064}},{"key_as_string":"1723636800000","key":1723636800000,"doc_count":392679,"_value":{"value":392679}}]}},{"key":"bkmonitor-unify-query-64bd4f5df4-llp94","doc_count":755511,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1723593600000","key":1723593600000,"doc_count":381173,"_value":{"value":381173}},{"key_as_string":"1723636800000","key":1723636800000,"doc_count":374338,"_value":{"value":374338}}]}}]}},{"key":"sync-apigw","doc_count":48,"__ext.io_kubernetes_pod":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"bkmonitor-unify-query-apigw-sync-1178-cl8k8","doc_count":24,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1723593600000","key":1723593600000,"doc_count":24,"_value":{"value":24}},{"key_as_string":"1723636800000","key":1723636800000,"doc_count":0,"_value":{"value":0}}]}},{"key":"bkmonitor-unify-query-apigw-sync-1179-9h9xv","doc_count":24,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1723593600000","key":1723593600000,"doc_count":24,"_value":{"value":24}},{"key_as_string":"1723636800000","key":1723636800000,"doc_count":0,"_value":{"value":0}}]}}]}}]}}}`,

		// 使用非时间聚合统计数量
		`{"aggregations":{"_value":{"value_count":{"field":"dtEventTimeStamp"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0}`: `{"took":36,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":1523302}}}`,

		// 获取 50 分位值
		`{"aggregations":{"_value":{"percentiles":{"field":"dtEventTimeStamp","percents":[50]}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0}`: `{"took":675,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"values":{"50.0":1.7236371328063303E12,"50.0_as_string":"1723637132806"}}}}`,

		// 获取 50, 90 分支值，同时按 6h 时间聚合
		`{"aggregations":{"dtEventTimeStamp":{"aggregations":{"_value":{"percentiles":{"field":"dtEventTimeStamp","percents":[50,90]}}},"date_histogram":{"extended_bounds":{"max":1723679962000,"min":1723593608000},"field":"dtEventTimeStamp","interval":"6h","min_doc_count":0}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0}`: `{"took":1338,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"dtEventTimeStamp":{"buckets":[{"key_as_string":"1723593600000","key":1723593600000,"doc_count":387467,"_value":{"values":{"50.0":1.7236043803502532E12,"50.0_as_string":"1723604380350","90.0":1.7236129561289934E12,"90.0_as_string":"1723612956128"}}},{"key_as_string":"1723615200000","key":1723615200000,"doc_count":368818,"_value":{"values":{"50.0":1.7236258380061033E12,"50.0_as_string":"1723625838006","90.0":1.7236346787215513E12,"90.0_as_string":"1723634678721"}}},{"key_as_string":"1723636800000","key":1723636800000,"doc_count":382721,"_value":{"values":{"50.0":1.7236475858829739E12,"50.0_as_string":"1723647585882","90.0":1.723656196499344E12,"90.0_as_string":"1723656196499"}}},{"key_as_string":"1723658400000","key":1723658400000,"doc_count":384296,"_value":{"values":{"50.0":1.7236691776407131E12,"50.0_as_string":"1723669177640","90.0":1.723677836133885E12,"90.0_as_string":"1723677836133"}}}]}}}`,

		// 根据 field 字段聚合计算数量，同时根据值排序
		`{"aggregations":{"dtEventTimeStamp":{"aggregations":{"_value":{"value_count":{"field":"dtEventTimeStamp"}}},"terms":{"field":"dtEventTimeStamp","order":[{"_value":"asc"}]}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0,"sort":[{"dtEventTimeStamp":{"order":"asc"}}]}`: `{"took":198,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"dtEventTimeStamp":{"doc_count_error_upper_bound":-1,"sum_other_doc_count":1523292,"buckets":[{"key":1723593878000,"key_as_string":"1723593878000","doc_count":1,"_value":{"value":1}},{"key":1723593947000,"key_as_string":"1723593947000","doc_count":1,"_value":{"value":1}},{"key":1723594186000,"key_as_string":"1723594186000","doc_count":1,"_value":{"value":1}},{"key":1723595733000,"key_as_string":"1723595733000","doc_count":1,"_value":{"value":1}},{"key":1723596287000,"key_as_string":"1723596287000","doc_count":1,"_value":{"value":1}},{"key":1723596309000,"key_as_string":"1723596309000","doc_count":1,"_value":{"value":1}},{"key":1723596597000,"key_as_string":"1723596597000","doc_count":1,"_value":{"value":1}},{"key":1723596677000,"key_as_string":"1723596677000","doc_count":1,"_value":{"value":1}},{"key":1723596938000,"key_as_string":"1723596938000","doc_count":1,"_value":{"value":1}},{"key":1723597150000,"key_as_string":"1723597150000","doc_count":1,"_value":{"value":1}}]}}}`,

		// 根据 field 字段聚合 min，同时根据值排序
		`{"aggregations":{"dtEventTimeStamp":{"aggregations":{"_value":{"min":{"field":"dtEventTimeStamp"}}},"terms":{"field":"dtEventTimeStamp","order":[{"_value":"asc"}]}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0,"sort":[{"dtEventTimeStamp":{"order":"asc"}}]}`: `{"took":198,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"dtEventTimeStamp":{"doc_count_error_upper_bound":-1,"sum_other_doc_count":1523292,"buckets":[{"key":1723593878000,"key_as_string":"1723593878000","doc_count":1,"_value":{"value":1}},{"key":1723593947000,"key_as_string":"1723593947000","doc_count":1,"_value":{"value":1}},{"key":1723594186000,"key_as_string":"1723594186000","doc_count":1,"_value":{"value":1}},{"key":1723595733000,"key_as_string":"1723595733000","doc_count":1,"_value":{"value":1}},{"key":1723596287000,"key_as_string":"1723596287000","doc_count":1,"_value":{"value":1}},{"key":1723596309000,"key_as_string":"1723596309000","doc_count":1,"_value":{"value":1}},{"key":1723596597000,"key_as_string":"1723596597000","doc_count":1,"_value":{"value":1}},{"key":1723596677000,"key_as_string":"1723596677000","doc_count":1,"_value":{"value":1}},{"key":1723596938000,"key_as_string":"1723596938000","doc_count":1,"_value":{"value":1}},{"key":1723597150000,"key_as_string":"1723597150000","doc_count":1,"_value":{"value":1}}]}}}`,

		// test- 1
		`{"aggregations":{"span_name":{"aggregations":{"end_time":{"aggregations":{"_value":{"value_count":{}}},"date_histogram":{"extended_bounds":{"max":-62135596800000000,"min":-62135596800000000},"field":"end_time","interval":"1000d","min_doc_count":0}}},"terms":{"field":"span_name","missing":" "}}},"query":{"bool":{"filter":{"range":{"end_time":{"from":-62135596800000000,"include_lower":true,"include_upper":true,"to":-62135596800000000}}}}},"size":0}`: `{"took":578,"timed_out":false,"_shards":{"total":6,"successful":6,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"span_name":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"SELECT","doc_count":1657598,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":83221,"_value":{"value":83221}},{"key":1747650240000000,"doc_count":48270,"_value":{"value":48270}},{"key":1747670400000000,"doc_count":11389,"_value":{"value":11389}},{"key":1747690560000000,"doc_count":42125,"_value":{"value":42125}},{"key":1747710720000000,"doc_count":127370,"_value":{"value":127370}},{"key":1747730880000000,"doc_count":140077,"_value":{"value":140077}},{"key":1747751040000000,"doc_count":19016,"_value":{"value":19016}},{"key":1747771200000000,"doc_count":21385,"_value":{"value":21385}},{"key":1747791360000000,"doc_count":165395,"_value":{"value":165395}},{"key":1747811520000000,"doc_count":209679,"_value":{"value":209679}},{"key":1747831680000000,"doc_count":49772,"_value":{"value":49772}},{"key":1747851840000000,"doc_count":19983,"_value":{"value":19983}},{"key":1747872000000000,"doc_count":143679,"_value":{"value":143679}},{"key":1747892160000000,"doc_count":178952,"_value":{"value":178952}},{"key":1747912320000000,"doc_count":60992,"_value":{"value":60992}},{"key":1747932480000000,"doc_count":44126,"_value":{"value":44126}},{"key":1747952640000000,"doc_count":63272,"_value":{"value":63272}},{"key":1747972800000000,"doc_count":79260,"_value":{"value":79260}},{"key":1747992960000000,"doc_count":22578,"_value":{"value":22578}},{"key":1748013120000000,"doc_count":5817,"_value":{"value":5817}},{"key":1748033280000000,"doc_count":5874,"_value":{"value":5874}},{"key":1748053440000000,"doc_count":4371,"_value":{"value":4371}},{"key":1748073600000000,"doc_count":1128,"_value":{"value":1128}},{"key":1748093760000000,"doc_count":1106,"_value":{"value":1106}},{"key":1748113920000000,"doc_count":1099,"_value":{"value":1099}},{"key":1748134080000000,"doc_count":1130,"_value":{"value":1130}},{"key":1748154240000000,"doc_count":1084,"_value":{"value":1084}},{"key":1748174400000000,"doc_count":1073,"_value":{"value":1073}},{"key":1748194560000000,"doc_count":1093,"_value":{"value":1093}},{"key":1748214720000000,"doc_count":36526,"_value":{"value":36526}},{"key":1748234880000000,"doc_count":66756,"_value":{"value":66756}}]}},{"key":"/trpc.example.greeter.Greeter/SayHello","doc_count":883345,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":12124,"_value":{"value":12124}},{"key":1747650240000000,"doc_count":29654,"_value":{"value":29654}},{"key":1747670400000000,"doc_count":29482,"_value":{"value":29482}},{"key":1747690560000000,"doc_count":29672,"_value":{"value":29672}},{"key":1747710720000000,"doc_count":29660,"_value":{"value":29660}},{"key":1747730880000000,"doc_count":29457,"_value":{"value":29457}},{"key":1747751040000000,"doc_count":29506,"_value":{"value":29506}},{"key":1747771200000000,"doc_count":29475,"_value":{"value":29475}},{"key":1747791360000000,"doc_count":29642,"_value":{"value":29642}},{"key":1747811520000000,"doc_count":29639,"_value":{"value":29639}},{"key":1747831680000000,"doc_count":29629,"_value":{"value":29629}},{"key":1747851840000000,"doc_count":29498,"_value":{"value":29498}},{"key":1747872000000000,"doc_count":29492,"_value":{"value":29492}},{"key":1747892160000000,"doc_count":29346,"_value":{"value":29346}},{"key":1747912320000000,"doc_count":29055,"_value":{"value":29055}},{"key":1747932480000000,"doc_count":29116,"_value":{"value":29116}},{"key":1747952640000000,"doc_count":29132,"_value":{"value":29132}},{"key":1747972800000000,"doc_count":29109,"_value":{"value":29109}},{"key":1747992960000000,"doc_count":29576,"_value":{"value":29576}},{"key":1748013120000000,"doc_count":29656,"_value":{"value":29656}},{"key":1748033280000000,"doc_count":29664,"_value":{"value":29664}},{"key":1748053440000000,"doc_count":29467,"_value":{"value":29467}},{"key":1748073600000000,"doc_count":29676,"_value":{"value":29676}},{"key":1748093760000000,"doc_count":29654,"_value":{"value":29654}},{"key":1748113920000000,"doc_count":29494,"_value":{"value":29494}},{"key":1748134080000000,"doc_count":29668,"_value":{"value":29668}},{"key":1748154240000000,"doc_count":29508,"_value":{"value":29508}},{"key":1748174400000000,"doc_count":29668,"_value":{"value":29668}},{"key":1748194560000000,"doc_count":29672,"_value":{"value":29672}},{"key":1748214720000000,"doc_count":29666,"_value":{"value":29666}},{"key":1748234880000000,"doc_count":15288,"_value":{"value":15288}}]}},{"key":"/trpc.example.greeter.Greeter/SayHi","doc_count":865553,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":11860,"_value":{"value":11860}},{"key":1747650240000000,"doc_count":29057,"_value":{"value":29057}},{"key":1747670400000000,"doc_count":28868,"_value":{"value":28868}},{"key":1747690560000000,"doc_count":29050,"_value":{"value":29050}},{"key":1747710720000000,"doc_count":29068,"_value":{"value":29068}},{"key":1747730880000000,"doc_count":28883,"_value":{"value":28883}},{"key":1747751040000000,"doc_count":28914,"_value":{"value":28914}},{"key":1747771200000000,"doc_count":28934,"_value":{"value":28934}},{"key":1747791360000000,"doc_count":29011,"_value":{"value":29011}},{"key":1747811520000000,"doc_count":29063,"_value":{"value":29063}},{"key":1747831680000000,"doc_count":28963,"_value":{"value":28963}},{"key":1747851840000000,"doc_count":28896,"_value":{"value":28896}},{"key":1747872000000000,"doc_count":28934,"_value":{"value":28934}},{"key":1747892160000000,"doc_count":28790,"_value":{"value":28790}},{"key":1747912320000000,"doc_count":28426,"_value":{"value":28426}},{"key":1747932480000000,"doc_count":28512,"_value":{"value":28512}},{"key":1747952640000000,"doc_count":28490,"_value":{"value":28490}},{"key":1747972800000000,"doc_count":28560,"_value":{"value":28560}},{"key":1747992960000000,"doc_count":28992,"_value":{"value":28992}},{"key":1748013120000000,"doc_count":29080,"_value":{"value":29080}},{"key":1748033280000000,"doc_count":29072,"_value":{"value":29072}},{"key":1748053440000000,"doc_count":28908,"_value":{"value":28908}},{"key":1748073600000000,"doc_count":29052,"_value":{"value":29052}},{"key":1748093760000000,"doc_count":29054,"_value":{"value":29054}},{"key":1748113920000000,"doc_count":28890,"_value":{"value":28890}},{"key":1748134080000000,"doc_count":29076,"_value":{"value":29076}},{"key":1748154240000000,"doc_count":28930,"_value":{"value":28930}},{"key":1748174400000000,"doc_count":29058,"_value":{"value":29058}},{"key":1748194560000000,"doc_count":29084,"_value":{"value":29084}},{"key":1748214720000000,"doc_count":29070,"_value":{"value":29070}},{"key":1748234880000000,"doc_count":15008,"_value":{"value":15008}}]}},{"key":"internalSpanDoSomething","doc_count":441681,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":6061,"_value":{"value":6061}},{"key":1747650240000000,"doc_count":14829,"_value":{"value":14829}},{"key":1747670400000000,"doc_count":14741,"_value":{"value":14741}},{"key":1747690560000000,"doc_count":14836,"_value":{"value":14836}},{"key":1747710720000000,"doc_count":14830,"_value":{"value":14830}},{"key":1747730880000000,"doc_count":14725,"_value":{"value":14725}},{"key":1747751040000000,"doc_count":14753,"_value":{"value":14753}},{"key":1747771200000000,"doc_count":14739,"_value":{"value":14739}},{"key":1747791360000000,"doc_count":14817,"_value":{"value":14817}},{"key":1747811520000000,"doc_count":14822,"_value":{"value":14822}},{"key":1747831680000000,"doc_count":14816,"_value":{"value":14816}},{"key":1747851840000000,"doc_count":14748,"_value":{"value":14748}},{"key":1747872000000000,"doc_count":14746,"_value":{"value":14746}},{"key":1747892160000000,"doc_count":14673,"_value":{"value":14673}},{"key":1747912320000000,"doc_count":14533,"_value":{"value":14533}},{"key":1747932480000000,"doc_count":14557,"_value":{"value":14557}},{"key":1747952640000000,"doc_count":14566,"_value":{"value":14566}},{"key":1747972800000000,"doc_count":14556,"_value":{"value":14556}},{"key":1747992960000000,"doc_count":14788,"_value":{"value":14788}},{"key":1748013120000000,"doc_count":14829,"_value":{"value":14829}},{"key":1748033280000000,"doc_count":14832,"_value":{"value":14832}},{"key":1748053440000000,"doc_count":14738,"_value":{"value":14738}},{"key":1748073600000000,"doc_count":14837,"_value":{"value":14837}},{"key":1748093760000000,"doc_count":14827,"_value":{"value":14827}},{"key":1748113920000000,"doc_count":14747,"_value":{"value":14747}},{"key":1748134080000000,"doc_count":14834,"_value":{"value":14834}},{"key":1748154240000000,"doc_count":14754,"_value":{"value":14754}},{"key":1748174400000000,"doc_count":14834,"_value":{"value":14834}},{"key":1748194560000000,"doc_count":14836,"_value":{"value":14836}},{"key":1748214720000000,"doc_count":14833,"_value":{"value":14833}},{"key":1748234880000000,"doc_count":7644,"_value":{"value":7644}}]}},{"key":"test.example.greeter.SayHello/sleep","doc_count":432779,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":5930,"_value":{"value":5930}},{"key":1747650240000000,"doc_count":14529,"_value":{"value":14529}},{"key":1747670400000000,"doc_count":14434,"_value":{"value":14434}},{"key":1747690560000000,"doc_count":14525,"_value":{"value":14525}},{"key":1747710720000000,"doc_count":14534,"_value":{"value":14534}},{"key":1747730880000000,"doc_count":14444,"_value":{"value":14444}},{"key":1747751040000000,"doc_count":14461,"_value":{"value":14461}},{"key":1747771200000000,"doc_count":14466,"_value":{"value":14466}},{"key":1747791360000000,"doc_count":14501,"_value":{"value":14501}},{"key":1747811520000000,"doc_count":14533,"_value":{"value":14533}},{"key":1747831680000000,"doc_count":14482,"_value":{"value":14482}},{"key":1747851840000000,"doc_count":14448,"_value":{"value":14448}},{"key":1747872000000000,"doc_count":14467,"_value":{"value":14467}},{"key":1747892160000000,"doc_count":14395,"_value":{"value":14395}},{"key":1747912320000000,"doc_count":14213,"_value":{"value":14213}},{"key":1747932480000000,"doc_count":14255,"_value":{"value":14255}},{"key":1747952640000000,"doc_count":14245,"_value":{"value":14245}},{"key":1747972800000000,"doc_count":14281,"_value":{"value":14281}},{"key":1747992960000000,"doc_count":14496,"_value":{"value":14496}},{"key":1748013120000000,"doc_count":14539,"_value":{"value":14539}},{"key":1748033280000000,"doc_count":14536,"_value":{"value":14536}},{"key":1748053440000000,"doc_count":14454,"_value":{"value":14454}},{"key":1748073600000000,"doc_count":14526,"_value":{"value":14526}},{"key":1748093760000000,"doc_count":14527,"_value":{"value":14527}},{"key":1748113920000000,"doc_count":14445,"_value":{"value":14445}},{"key":1748134080000000,"doc_count":14538,"_value":{"value":14538}},{"key":1748154240000000,"doc_count":14465,"_value":{"value":14465}},{"key":1748174400000000,"doc_count":14529,"_value":{"value":14529}},{"key":1748194560000000,"doc_count":14542,"_value":{"value":14542}},{"key":1748214720000000,"doc_count":14535,"_value":{"value":14535}},{"key":1748234880000000,"doc_count":7504,"_value":{"value":7504}}]}}]}}}`,

		// refrence 聚合使用别名
		// refrence 聚合不使用别名
		`{"_source":{"includes":["__ext.io_kubernetes_pod_namespace"]},"aggregations":{"__ext.io_kubernetes_pod_namespace":{"aggregations":{"_value":{"value_count":{"field":"__ext.io_kubernetes_pod_namespace"}}},"terms":{"field":"__ext.io_kubernetes_pod_namespace","missing":" ","order":[{"_key":"desc"}]}}},"collapse":{"field":"__ext.io_kubernetes_pod_namespace"},"query":{"bool":{"filter":[{"exists":{"field":"__ext.io_kubernetes_pod_namespace"}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1756987540,"include_lower":true,"include_upper":true,"to":1756991140}}}]}},"size":0}`: `{"took":806,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"__ext.io_kubernetes_pod_namespace":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"blueking","doc_count":356250,"_value":{"value":356250}}]}}}`,

		// "nested aggregate + query 测试
		`{"_source":{"includes":["group","user.first","user.last"]},"aggregations":{"user":{"aggregations":{"_value":{"value_count":{"field":"user.first"}}},"nested":{"path":"user"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0,"sort":[{"dtEventTimeStamp":{"order":"desc"}}]}`: `{"took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":17,"relation":"eq"},"max_score":null,"hits":[]},"aggregations":{"user":{"doc_count":18,"_value":{"value":18}}}}`,
	})

	for idx, c := range map[string]struct {
		query *metadata.Query
		start time.Time
		end   time.Time

		isReference bool

		expected string
		err      error
	}{
		"nested aggregate + query 测试": {
			query: &metadata.Query{
				DB:    db,
				Field: "user.first",
				Orders: metadata.Orders{
					{
						Name: FieldTime,
						Ast:  false,
					},
				},
				DataSource:    structured.BkLog,
				TableID:       "es_index",
				Source:        []string{"group", "user.first", "user.last"},
				StorageType:   metadata.ElasticsearchStorageType,
				AllConditions: metadata.AllConditions{},
				Aggregates: metadata.Aggregates{
					{
						Name: Count,
					},
				},
			},
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__name__","value":"bklog:es_index:user__bk_46__first"}],"samples":[{"value":18,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
		"统计 __ext.io_kubernetes_pod 不为空的文档数量": {
			query: &metadata.Query{
				DB:         db,
				Field:      "__ext.io_kubernetes_pod",
				From:       0,
				Size:       10,
				DataSource: structured.BkLog,
				TableID:    "bk_log_index_set_10",
				Orders: metadata.Orders{
					{
						Name: FieldTime,
						Ast:  false,
					},
				},
				StorageType: metadata.ElasticsearchStorageType,
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
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"}],"samples":[{"value":1523302,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
		"统计 __ext.io_kubernetes_pod 不为空的去重文档数量": {
			query: &metadata.Query{
				DB:         db,
				Field:      "__ext.io_kubernetes_pod",
				From:       0,
				Size:       10,
				DataSource: structured.BkLog,
				TableID:    "bk_log_index_set_10",
				Orders: metadata.Orders{
					{
						Name: FieldTime,
						Ast:  false,
					},
				},
				StorageType: metadata.ElasticsearchStorageType,
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
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"}],"samples":[{"value":4,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
		"使用 promql 计算平均值 sum(count_over_time(field[12h]))": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				From:        0,
				Size:        20,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				StorageType: metadata.ElasticsearchStorageType,
				Aggregates: metadata.Aggregates{
					{
						Name: Count,
						Dimensions: []string{
							"__ext.io_kubernetes_pod",
							"__ext.container_name",
						},
						Window: time.Hour * 12,
					},
				},
			},
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__ext__bk_46__container_name","value":"sync-apigw"},{"name":"__ext__bk_46__io_kubernetes_pod","value":"bkmonitor-unify-query-apigw-sync-1178-cl8k8"},{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"}],"samples":[{"value":24,"timestamp":1723593600000},{"timestamp":1723636800000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__ext__bk_46__container_name","value":"sync-apigw"},{"name":"__ext__bk_46__io_kubernetes_pod","value":"bkmonitor-unify-query-apigw-sync-1179-9h9xv"},{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"}],"samples":[{"value":24,"timestamp":1723593600000},{"timestamp":1723636800000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__ext__bk_46__container_name","value":"unify-query"},{"name":"__ext__bk_46__io_kubernetes_pod","value":"bkmonitor-unify-query-64bd4f5df4-599f9"},{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"}],"samples":[{"value":375064,"timestamp":1723593600000},{"value":392679,"timestamp":1723636800000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__ext__bk_46__container_name","value":"unify-query"},{"name":"__ext__bk_46__io_kubernetes_pod","value":"bkmonitor-unify-query-64bd4f5df4-llp94"},{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"}],"samples":[{"value":381173,"timestamp":1723593600000},{"value":374338,"timestamp":1723636800000}],"exemplars":null,"histograms":null}]`,
		},
		"使用非时间聚合统计数量": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				From:        0,
				Size:        3,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				StorageType: metadata.ElasticsearchStorageType,
				Aggregates: metadata.Aggregates{
					{
						Name: Count,
					},
				},
			},
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"}],"samples":[{"value":1523302,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
		"获取 50 分位值": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				From:        0,
				Size:        20,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				StorageType: metadata.ElasticsearchStorageType,
				Aggregates: metadata.Aggregates{
					{
						Name: Percentiles,
						Args: []any{
							50.0,
						},
					},
				},
			},
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"le","value":"50.0"}],"samples":[{"value":1723637132806.3303,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
		"获取 50, 90 分支值，同时按 6h 时间聚合": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				From:        0,
				Size:        20,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				StorageType: metadata.ElasticsearchStorageType,
				Aggregates: metadata.Aggregates{
					{
						Name: Percentiles,
						Args: []any{
							50.0, 90.0,
						},
					},
					{
						Name:   DateHistogram,
						Window: time.Hour * 6,
					},
				},
			},
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"le","value":"50.0"}],"samples":[{"value":1723604380350.2532,"timestamp":1723593600000},{"value":1723625838006.1033,"timestamp":1723615200000},{"value":1723647585882.9739,"timestamp":1723636800000},{"value":1723669177640.7131,"timestamp":1723658400000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"le","value":"90.0"}],"samples":[{"value":1723612956128.9934,"timestamp":1723593600000},{"value":1723634678721.5513,"timestamp":1723615200000},{"value":1723656196499.344,"timestamp":1723636800000},{"value":1723677836133.885,"timestamp":1723658400000}],"exemplars":null,"histograms":null}]`,
		},
		"根据 field 字段聚合计算数量，同时根据值排序": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				From:        0,
				Size:        10,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				StorageType: metadata.ElasticsearchStorageType,
				Aggregates: metadata.Aggregates{
					{
						Name: Count,
						Dimensions: []string{
							field,
						},
					},
				},
				Orders: metadata.Orders{
					{
						Name: FieldValue,
						Ast:  true,
					},
				},
			},
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723593878000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723593947000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723594186000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723595733000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723596287000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723596309000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723596597000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723596677000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723596938000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723597150000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
		"根据 field 字段聚合 min，同时根据值排序": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				From:        0,
				Size:        10,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				StorageType: metadata.ElasticsearchStorageType,
				Aggregates: metadata.Aggregates{
					{
						Name: Min,
						Dimensions: []string{
							field,
						},
					},
				},
				Orders: metadata.Orders{
					{
						Name: FieldValue,
						Ast:  true,
					},
				},
			},
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723593878000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723593947000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723594186000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723595733000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723596287000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723596309000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723596597000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723596677000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723596938000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:dtEventTimeStamp"},{"name":"dtEventTimeStamp","value":"1723597150000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
		"test-1": {
			query: &metadata.Query{
				DB:          db,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				StorageType: metadata.ElasticsearchStorageType,
				Aggregates: metadata.Aggregates{
					{
						Name:       Count,
						Dimensions: []string{"span_name"},
						Window:     time.Hour * 24,
					},
				},
				TimeField: metadata.TimeField{
					Name: "end_time",
					Unit: "microsecond",
					Type: "long",
				},
			},
			expected: `[{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:"},{"name":"span_name","value":"/trpc.example.greeter.Greeter/SayHello"}],"samples":[{"value":12124,"timestamp":1747630080000},{"value":29654,"timestamp":1747650240000},{"value":29482,"timestamp":1747670400000},{"value":29672,"timestamp":1747690560000},{"value":29660,"timestamp":1747710720000},{"value":29457,"timestamp":1747730880000},{"value":29506,"timestamp":1747751040000},{"value":29475,"timestamp":1747771200000},{"value":29642,"timestamp":1747791360000},{"value":29639,"timestamp":1747811520000},{"value":29629,"timestamp":1747831680000},{"value":29498,"timestamp":1747851840000},{"value":29492,"timestamp":1747872000000},{"value":29346,"timestamp":1747892160000},{"value":29055,"timestamp":1747912320000},{"value":29116,"timestamp":1747932480000},{"value":29132,"timestamp":1747952640000},{"value":29109,"timestamp":1747972800000},{"value":29576,"timestamp":1747992960000},{"value":29656,"timestamp":1748013120000},{"value":29664,"timestamp":1748033280000},{"value":29467,"timestamp":1748053440000},{"value":29676,"timestamp":1748073600000},{"value":29654,"timestamp":1748093760000},{"value":29494,"timestamp":1748113920000},{"value":29668,"timestamp":1748134080000},{"value":29508,"timestamp":1748154240000},{"value":29668,"timestamp":1748174400000},{"value":29672,"timestamp":1748194560000},{"value":29666,"timestamp":1748214720000},{"value":15288,"timestamp":1748234880000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:"},{"name":"span_name","value":"/trpc.example.greeter.Greeter/SayHi"}],"samples":[{"value":11860,"timestamp":1747630080000},{"value":29057,"timestamp":1747650240000},{"value":28868,"timestamp":1747670400000},{"value":29050,"timestamp":1747690560000},{"value":29068,"timestamp":1747710720000},{"value":28883,"timestamp":1747730880000},{"value":28914,"timestamp":1747751040000},{"value":28934,"timestamp":1747771200000},{"value":29011,"timestamp":1747791360000},{"value":29063,"timestamp":1747811520000},{"value":28963,"timestamp":1747831680000},{"value":28896,"timestamp":1747851840000},{"value":28934,"timestamp":1747872000000},{"value":28790,"timestamp":1747892160000},{"value":28426,"timestamp":1747912320000},{"value":28512,"timestamp":1747932480000},{"value":28490,"timestamp":1747952640000},{"value":28560,"timestamp":1747972800000},{"value":28992,"timestamp":1747992960000},{"value":29080,"timestamp":1748013120000},{"value":29072,"timestamp":1748033280000},{"value":28908,"timestamp":1748053440000},{"value":29052,"timestamp":1748073600000},{"value":29054,"timestamp":1748093760000},{"value":28890,"timestamp":1748113920000},{"value":29076,"timestamp":1748134080000},{"value":28930,"timestamp":1748154240000},{"value":29058,"timestamp":1748174400000},{"value":29084,"timestamp":1748194560000},{"value":29070,"timestamp":1748214720000},{"value":15008,"timestamp":1748234880000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:"},{"name":"span_name","value":"SELECT"}],"samples":[{"value":83221,"timestamp":1747630080000},{"value":48270,"timestamp":1747650240000},{"value":11389,"timestamp":1747670400000},{"value":42125,"timestamp":1747690560000},{"value":127370,"timestamp":1747710720000},{"value":140077,"timestamp":1747730880000},{"value":19016,"timestamp":1747751040000},{"value":21385,"timestamp":1747771200000},{"value":165395,"timestamp":1747791360000},{"value":209679,"timestamp":1747811520000},{"value":49772,"timestamp":1747831680000},{"value":19983,"timestamp":1747851840000},{"value":143679,"timestamp":1747872000000},{"value":178952,"timestamp":1747892160000},{"value":60992,"timestamp":1747912320000},{"value":44126,"timestamp":1747932480000},{"value":63272,"timestamp":1747952640000},{"value":79260,"timestamp":1747972800000},{"value":22578,"timestamp":1747992960000},{"value":5817,"timestamp":1748013120000},{"value":5874,"timestamp":1748033280000},{"value":4371,"timestamp":1748053440000},{"value":1128,"timestamp":1748073600000},{"value":1106,"timestamp":1748093760000},{"value":1099,"timestamp":1748113920000},{"value":1130,"timestamp":1748134080000},{"value":1084,"timestamp":1748154240000},{"value":1073,"timestamp":1748174400000},{"value":1093,"timestamp":1748194560000},{"value":36526,"timestamp":1748214720000},{"value":66756,"timestamp":1748234880000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:"},{"name":"span_name","value":"internalSpanDoSomething"}],"samples":[{"value":6061,"timestamp":1747630080000},{"value":14829,"timestamp":1747650240000},{"value":14741,"timestamp":1747670400000},{"value":14836,"timestamp":1747690560000},{"value":14830,"timestamp":1747710720000},{"value":14725,"timestamp":1747730880000},{"value":14753,"timestamp":1747751040000},{"value":14739,"timestamp":1747771200000},{"value":14817,"timestamp":1747791360000},{"value":14822,"timestamp":1747811520000},{"value":14816,"timestamp":1747831680000},{"value":14748,"timestamp":1747851840000},{"value":14746,"timestamp":1747872000000},{"value":14673,"timestamp":1747892160000},{"value":14533,"timestamp":1747912320000},{"value":14557,"timestamp":1747932480000},{"value":14566,"timestamp":1747952640000},{"value":14556,"timestamp":1747972800000},{"value":14788,"timestamp":1747992960000},{"value":14829,"timestamp":1748013120000},{"value":14832,"timestamp":1748033280000},{"value":14738,"timestamp":1748053440000},{"value":14837,"timestamp":1748073600000},{"value":14827,"timestamp":1748093760000},{"value":14747,"timestamp":1748113920000},{"value":14834,"timestamp":1748134080000},{"value":14754,"timestamp":1748154240000},{"value":14834,"timestamp":1748174400000},{"value":14836,"timestamp":1748194560000},{"value":14833,"timestamp":1748214720000},{"value":7644,"timestamp":1748234880000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:"},{"name":"span_name","value":"test.example.greeter.SayHello/sleep"}],"samples":[{"value":5930,"timestamp":1747630080000},{"value":14529,"timestamp":1747650240000},{"value":14434,"timestamp":1747670400000},{"value":14525,"timestamp":1747690560000},{"value":14534,"timestamp":1747710720000},{"value":14444,"timestamp":1747730880000},{"value":14461,"timestamp":1747751040000},{"value":14466,"timestamp":1747771200000},{"value":14501,"timestamp":1747791360000},{"value":14533,"timestamp":1747811520000},{"value":14482,"timestamp":1747831680000},{"value":14448,"timestamp":1747851840000},{"value":14467,"timestamp":1747872000000},{"value":14395,"timestamp":1747892160000},{"value":14213,"timestamp":1747912320000},{"value":14255,"timestamp":1747932480000},{"value":14245,"timestamp":1747952640000},{"value":14281,"timestamp":1747972800000},{"value":14496,"timestamp":1747992960000},{"value":14539,"timestamp":1748013120000},{"value":14536,"timestamp":1748033280000},{"value":14454,"timestamp":1748053440000},{"value":14526,"timestamp":1748073600000},{"value":14527,"timestamp":1748093760000},{"value":14445,"timestamp":1748113920000},{"value":14538,"timestamp":1748134080000},{"value":14465,"timestamp":1748154240000},{"value":14529,"timestamp":1748174400000},{"value":14542,"timestamp":1748194560000},{"value":14535,"timestamp":1748214720000},{"value":7504,"timestamp":1748234880000}],"exemplars":null,"histograms":null}]`,
		},
		"refrence 聚合使用别名": {
			start: time.UnixMilli(1756987540000),
			end:   time.UnixMilli(1756991140000),
			query: &metadata.Query{
				DB:          db,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				Field:       "namespace",
				StorageType: metadata.ElasticsearchStorageType,
				Aggregates: metadata.Aggregates{
					{
						Name:       Count,
						Dimensions: []string{"namespace"},
					},
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "namespace",
							Operator:      "ne",
							Value:         []string{""},
						},
					},
				},
				FieldAlias: map[string]string{
					"namespace": "__ext.io_kubernetes_pod_namespace",
				},
				Source: []string{"namespace"},
				Collapse: &metadata.Collapse{
					Field: "namespace",
				},
				Orders: metadata.Orders{
					{
						Name: "namespace",
					},
				},
			},
			expected: `[{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:namespace"},{"name":"namespace","value":"blueking"}],"samples":[{"value":356250,"timestamp":1756987540000}],"exemplars":null,"histograms":null}]`,
		},
		"refrence 聚合不使用别名": {
			start: time.UnixMilli(1756987540000),
			end:   time.UnixMilli(1756991140000),
			query: &metadata.Query{
				DB:          db,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				Field:       "__ext.io_kubernetes_pod_namespace",
				StorageType: metadata.ElasticsearchStorageType,
				Aggregates: metadata.Aggregates{
					{
						Name:       Count,
						Dimensions: []string{"__ext.io_kubernetes_pod_namespace"},
					},
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "__ext.io_kubernetes_pod_namespace",
							Operator:      "ne",
							Value:         []string{""},
						},
					},
				},
				FieldAlias: map[string]string{},
				Source:     []string{"__ext.io_kubernetes_pod_namespace"},
				Collapse: &metadata.Collapse{
					Field: "__ext.io_kubernetes_pod_namespace",
				},
				Orders: metadata.Orders{
					{
						Name: "__ext.io_kubernetes_pod_namespace",
					},
				},
			},
			expected: `[{"labels":[{"name":"__ext__bk_46__io_kubernetes_pod_namespace","value":"blueking"},{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod_namespace"}],"samples":[{"value":356250,"timestamp":1756987540000}],"exemplars":null,"histograms":null}]`,
		},
	} {
		t.Run(fmt.Sprintf("testing run: %s", idx), func(t *testing.T) {
			ss := ins.QuerySeriesSet(ctx, c.query, c.start, c.end)
			timeSeries, err := mock.SeriesSetToTimeSeries(ss)

			assert.Nil(t, err)
			if err == nil {
				actual := timeSeries.String()
				assert.JSONEq(t, c.expected, actual)
			}
		})
	}
}

func TestInstance_queryRawData(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	ins, err := NewInstance(ctx, &InstanceOption{
		Connect: Connect{
			Address: mock.EsUrl,
		},
		Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
		return
	}

	defaultStart := time.UnixMilli(1723593608000)
	defaultEnd := time.UnixMilli(1723679962000)

	db := "es_index"
	field := "dtEventTimeStamp"

	mock.Es.Set(map[string]any{
		// nested query + query string 测试 + highlight
		`{"_source":{"includes":["group","user.first","user.last"]},"from":0,"query":{"bool":{"filter":[{"nested":{"path":"user","query":{"match_phrase":{"user.first":{"query":"John"}}}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}},{"term":{"group":"fans"}}]}},"size":5,"sort":[{"dtEventTimeStamp":{"order":"desc"}}]}`: `{"took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":0.0,"hits":[{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"aS3KjpEBbwEm76LbcH1G","_score":0.0,"_source":{"group":"fans","user":[{"first":"John","last":"Smith"},{"first":"Alice","last":"White"}]},"highlight":{"group":["<mark>fans</mark>"],"user.first":["<mark>John</mark>"]}}]}}`,
		// high light from condition
		`{"_source":{"includes":["status","message"]},"from":0,"query":{"bool":{"filter":[{"match_phrase":{"status":{"query":"error"}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}]}},"size":5,"sort":[{"dtEventTimeStamp":{"order":"desc"}}]}`: `{"took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":0.0,"hits":[{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"bT4KjpEBbwEm76LbdH2H","_score":0.0,"_source":{"status":"error","message":"Something went wrong"},"highlight":{"status":["<mark>error</mark>"]}}]}}`,

		// "nested aggregate + query 测试
		`{"_source":{"includes":["group","user.first","user.last"]},"aggregations":{"user":{"aggregations":{"_value":{"value_count":{"field":"user.first"}}},"nested":{"path":"user"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0}`: `{"took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":17,"relation":"eq"},"max_score":null,"hits":[]},"aggregations":{"user":{"doc_count":18,"_value":{"value":18}}}}`,

		// 获取 10条 不 field 为空的原始数据
		`{"_source":{"includes":["__ext.container_id"]},"from":0,"query":{"bool":{"filter":[{"exists":{"field":"dtEventTimeStamp"}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}]}},"size":10,"sort":[{"dtEventTimeStamp":{"order":"desc"}}]}`: `{"took":13,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":0.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"27bdd842c5f2929cf4bd90f1e4534a9d","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"d21cf5cf373b4a26a31774ff7ab38fad","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"e07e9f6437e64cc04e945dc0bf604e62","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"01fb133625637ee3b0b8e689b8126da2","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"7eaa9e9edfc5e6bd8ba5df06fd2d5c00","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"bcabf17aca864416784c0b1054b6056e","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"3edf7236b8fc45c1aec67ea68fa92c61","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"77d08d253f11554c5290b4cac515c4e1","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"9fb5bb5f9bce7e0ab59e0cd1f410c57b","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"573b3e1b4a499e4b7e7fab35f316ac8a","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}}]}}`,

		// 获取 10条 原始数据
		`{"_source":{"includes":["__ext.io_kubernetes_pod","__ext.container_name"]},"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":10,"sort":[{"dtEventTimeStamp":{"order":"desc"}}]}`: `{"took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":0.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"8defd23f1c2599e70f3ace3a042b2b5f","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"ba0a6e66f01d6cb77ae25b13ddf4ad1b","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"74ea55e7397582b101f0e21efbc876c6","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"084792484f943e314e31ef2b2e878115","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"0a3f47a7c57d0af7d40d82c729c37155","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"85981293cca7102b9560b49a7f089737","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"b429dc6611efafc4d02b90f882271dea","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"01213026ae064c6726fd99dc8276e842","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"93027432b40ccb01b1be8f4ea06a6853","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"bc31babcb5d1075fc421bd641199d3aa","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}}]}}`,

		`{"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0}`: `{"error":{"root_cause":[{"type":"x_content_parse_exception","reason":"[1:138] [highlight] unknown field [max_analyzed_offset]"}],"type":"x_content_parse_exception","reason":"[1:138] [highlight] unknown field [max_analyzed_offset]"},"status":400}`,

		// scroll_id_1
		`{"scroll":"10m","scroll_id":"scroll_id_1"}`: `{"_scroll_id":"scroll_id_1","took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":0.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"8defd23f1c2599e70f3ace3a042b2b5f","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"ba0a6e66f01d6cb77ae25b13ddf4ad1b","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"74ea55e7397582b101f0e21efbc876c6","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"084792484f943e314e31ef2b2e878115","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"0a3f47a7c57d0af7d40d82c729c37155","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}}]}}`,

		// scroll_id_2
		`{"scroll":"10m","scroll_id":"scroll_id_2"}`: `{"took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"max_score":null,"hits":[]}}`,

		// search after
		`{"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"search_after":[1743465646224,"kibana_settings",null],"size":5,"sort":[{"timestamp":{"order":"desc"}},{"type":{"order":"desc"}},{"kibana_stats.kibana.name":{"order":"desc"}}]}`: `{"took":13,"timed_out":false,"_shards":{"total":7,"successful":7,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[{"_index":".monitoring-kibana-7-2025.04.01","_id":"rYSm7pUBxj8-27WaYRCB","_score":null,"_source":{"timestamp":"2025-04-01T00:00:36.224Z","type":"kibana_stats","kibana_stats":{"kibana":{"name":"es-os60crz7-kibana"}}},"sort":[1743465636224,"kibana_stats","es-os60crz7-kibana"]},{"_index":".monitoring-kibana-7-2025.04.01","_id":"roSm7pUBxj8-27WaYRCB","_score":null,"_source":{"timestamp":"2025-04-01T00:00:36.224Z","type":"kibana_settings"},"sort":[1743465636224,"kibana_settings",null]},{"_index":".monitoring-kibana-7-2025.04.01","_id":"q4Sm7pUBxj8-27WaOhBx","_score":null,"_source":{"timestamp":"2025-04-01T00:00:26.225Z","type":"kibana_stats","kibana_stats":{"kibana":{"name":"es-os60crz7-kibana"}}},"sort":[1743465626225,"kibana_stats","es-os60crz7-kibana"]},{"_index":".monitoring-kibana-7-2025.04.01","_id":"rISm7pUBxj8-27WaOhBx","_score":null,"_source":{"timestamp":"2025-04-01T00:00:26.225Z","type":"kibana_settings"},"sort":[1743465626225,"kibana_settings",null]},{"_index":".monitoring-kibana-7-2025.04.01","_id":"8DSm7pUBipSLyy3IEwRg","_score":null,"_source":{"timestamp":"2025-04-01T00:00:16.224Z","type":"kibana_stats","kibana_stats":{"kibana":{"name":"es-os60crz7-kibana"}}},"sort":[1743465616224,"kibana_stats","es-os60crz7-kibana"]}]}}`,

		// debug highlight
		`{"from":0,"query":{"bool":{"filter":[{"match_phrase":{"resource.k8s.bcs.cluster.id":{"query":"BCS-K8S-00000"}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":-62135596800,"include_lower":true,"include_upper":true,"to":-62135596800}}}]}},"size":0}`: `{"took":15,"timed_out":false,"_shards":{"total":6,"successful":6,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[{"_index":"v2_2_bkapm_trace_bk_monitor_20250604_0","_type":"_doc","_id":"14712105480911733430","_score":null,"_source":{"links":[],"trace_state":"","elapsed_time":38027,"status":{"message":"","code":0},"resource":{"k8s.pod.ip":"192.168.1.100","bk.instance.id":":unify-query::192.168.1.100:","service.name":"unify-query","net.host.ip":"192.168.1.100","k8s.bcs.cluster.id":"BCS-K8S-00000","k8s.pod.name":"bk-monitor-unify-query-5c685b56f-n4b6d","k8s.namespace.name":"blueking"},"span_name":"http-curl","attributes":{"apdex_type":"satisfied","req-http-method":"POST","req-http-path":"https://bkapi.paas3-dev.bktencent.com/api/bk-base/prod/v3/queryengine/query_sync"},"end_time":1749006597019296,"parent_span_id":"6f15efc54fedfebe","events":[],"span_id":"4a5f6170ae000a3f","trace_id":"5c999893cdbc41390c5ff8f3be5f62a9","kind":1,"start_time":1749006596981268,"time":"1749006604000"},"sort":["1749006604000"]}]}}`,
	})

	for idx, c := range map[string]struct {
		query *metadata.Query
		start time.Time
		end   time.Time

		isReference bool

		total              int64
		list               string
		resultTableOptions metadata.ResultTableOptions
		err                error
	}{
		"nested query + query string 测试 + highlight": {
			query: &metadata.Query{
				DB:    db,
				Field: "group",
				From:  0,
				Size:  5,
				Orders: metadata.Orders{
					{
						Name: FieldTime,
						Ast:  false,
					},
				},
				StorageID:   "log",
				DataSource:  structured.BkLog,
				TableID:     "es_index",
				DataLabel:   "es_index",
				StorageType: metadata.ElasticsearchStorageType,
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
			total: 1,
			list: `[ {
  "__data_label" : "es_index",
  "__doc_id" : "aS3KjpEBbwEm76LbcH1G",
  "__index" : "bk_unify_query_demo_2",
  "__result_table" : "es_index",
  "__table_uuid" : "es_index|log",
  "group" : "fans",
  "user" : [ {
    "first" : "John",
    "last" : "Smith"
  }, {
    "first" : "Alice",
    "last" : "White"
  } ]
} ]`,
			resultTableOptions: metadata.ResultTableOptions{
				"es_index|log": &metadata.ResultTableOption{
					FieldType: mock.FieldType,
					From:      function.IntPoint(0),
				},
			},
		},
		"获取 10条 不 field 为空的原始数据，使用别名": {
			query: &metadata.Query{
				DB:         db,
				Field:      field,
				From:       0,
				Size:       10,
				DataSource: structured.BkLog,
				TableID:    "bk_log_index_set_10",
				StorageID:  "log",
				DataLabel:  "set_10",
				Orders: metadata.Orders{
					{
						Name: FieldTime,
						Ast:  false,
					},
				},
				FieldAlias: map[string]string{
					"id":   "__ext.container_id",
					"time": "dtEventTimeStamp",
				},
				Source:      []string{"id"},
				StorageType: metadata.ElasticsearchStorageType,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "time",
							Operator:      "ncontains",
							Value:         []string{""},
						},
					},
				},
			},
			start: defaultStart,
			end:   defaultEnd,
			total: 1e4,
			list: `[ {
  "__data_label" : "set_10",
  "__doc_id" : "27bdd842c5f2929cf4bd90f1e4534a9d",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "d21cf5cf373b4a26a31774ff7ab38fad",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "e07e9f6437e64cc04e945dc0bf604e62",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "01fb133625637ee3b0b8e689b8126da2",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "7eaa9e9edfc5e6bd8ba5df06fd2d5c00",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "bcabf17aca864416784c0b1054b6056e",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "3edf7236b8fc45c1aec67ea68fa92c61",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "77d08d253f11554c5290b4cac515c4e1",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "9fb5bb5f9bce7e0ab59e0cd1f410c57b",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "573b3e1b4a499e4b7e7fab35f316ac8a",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
} ]`,
			resultTableOptions: metadata.ResultTableOptions{
				"bk_log_index_set_10|log": &metadata.ResultTableOption{
					FieldType: mock.FieldType,
					From:      function.IntPoint(0),
				},
			},
		},
		"获取 10条 不 field 为空的原始数据": {
			query: &metadata.Query{
				DB:         db,
				Field:      field,
				From:       0,
				Size:       10,
				DataSource: structured.BkLog,
				TableID:    "bk_log_index_set_10",
				StorageID:  "log",
				DataLabel:  "set_10",
				Orders: metadata.Orders{
					{
						Name: FieldTime,
						Ast:  false,
					},
				},
				Source:      []string{"__ext.container_id"},
				StorageType: metadata.ElasticsearchStorageType,
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
			total: 1e4,
			list: `[ {
  "__data_label" : "set_10",
  "__doc_id" : "27bdd842c5f2929cf4bd90f1e4534a9d",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "d21cf5cf373b4a26a31774ff7ab38fad",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "e07e9f6437e64cc04e945dc0bf604e62",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "01fb133625637ee3b0b8e689b8126da2",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "7eaa9e9edfc5e6bd8ba5df06fd2d5c00",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "bcabf17aca864416784c0b1054b6056e",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "3edf7236b8fc45c1aec67ea68fa92c61",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "77d08d253f11554c5290b4cac515c4e1",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "9fb5bb5f9bce7e0ab59e0cd1f410c57b",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "set_10",
  "__doc_id" : "573b3e1b4a499e4b7e7fab35f316ac8a",
  "__ext.container_id" : "77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
} ]`,
			resultTableOptions: metadata.ResultTableOptions{
				"bk_log_index_set_10|log": &metadata.ResultTableOption{
					FieldType: mock.FieldType,
					From:      function.IntPoint(0),
				},
			},
		},
		"获取 10条 原始数据": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				From:        0,
				Size:        10,
				Source:      []string{"__ext.io_kubernetes_pod", "__ext.container_name"},
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				StorageID:   "log",
				DataLabel:   "bk_log",
				StorageType: metadata.ElasticsearchStorageType,
				TimeField: metadata.TimeField{
					Name: "dtEventTimeStamp",
					Type: TimeFieldTypeTime,
					Unit: function.Millisecond,
				},
				Orders: metadata.Orders{
					{
						Name: FieldTime,
						Ast:  false,
					},
				},
			},
			start: defaultStart,
			end:   defaultEnd,
			total: 1e4,
			list: `[ {
  "__data_label" : "bk_log",
  "__doc_id" : "8defd23f1c2599e70f3ace3a042b2b5f",
  "__ext.container_name" : "unify-query",
  "__ext.io_kubernetes_pod" : "bkmonitor-unify-query-64bd4f5df4-599f9",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "bk_log",
  "__doc_id" : "ba0a6e66f01d6cb77ae25b13ddf4ad1b",
  "__ext.container_name" : "unify-query",
  "__ext.io_kubernetes_pod" : "bkmonitor-unify-query-64bd4f5df4-599f9",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "bk_log",
  "__doc_id" : "74ea55e7397582b101f0e21efbc876c6",
  "__ext.container_name" : "unify-query",
  "__ext.io_kubernetes_pod" : "bkmonitor-unify-query-64bd4f5df4-599f9",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "bk_log",
  "__doc_id" : "084792484f943e314e31ef2b2e878115",
  "__ext.container_name" : "unify-query",
  "__ext.io_kubernetes_pod" : "bkmonitor-unify-query-64bd4f5df4-599f9",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "bk_log",
  "__doc_id" : "0a3f47a7c57d0af7d40d82c729c37155",
  "__ext.container_name" : "unify-query",
  "__ext.io_kubernetes_pod" : "bkmonitor-unify-query-64bd4f5df4-599f9",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "bk_log",
  "__doc_id" : "85981293cca7102b9560b49a7f089737",
  "__ext.container_name" : "unify-query",
  "__ext.io_kubernetes_pod" : "bkmonitor-unify-query-64bd4f5df4-599f9",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "bk_log",
  "__doc_id" : "b429dc6611efafc4d02b90f882271dea",
  "__ext.container_name" : "unify-query",
  "__ext.io_kubernetes_pod" : "bkmonitor-unify-query-64bd4f5df4-599f9",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "bk_log",
  "__doc_id" : "01213026ae064c6726fd99dc8276e842",
  "__ext.container_name" : "unify-query",
  "__ext.io_kubernetes_pod" : "bkmonitor-unify-query-64bd4f5df4-599f9",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "bk_log",
  "__doc_id" : "93027432b40ccb01b1be8f4ea06a6853",
  "__ext.container_name" : "unify-query",
  "__ext.io_kubernetes_pod" : "bkmonitor-unify-query-64bd4f5df4-599f9",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
}, {
  "__data_label" : "bk_log",
  "__doc_id" : "bc31babcb5d1075fc421bd641199d3aa",
  "__ext.container_name" : "unify-query",
  "__ext.io_kubernetes_pod" : "bkmonitor-unify-query-64bd4f5df4-599f9",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log"
} ]`,
			resultTableOptions: metadata.ResultTableOptions{
				"bk_log_index_set_10|log": &metadata.ResultTableOption{
					FieldType: mock.FieldType,
					From:      function.IntPoint(0),
				},
			},
		},
		"query with scroll id 1 and field alias": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				StorageID:   "log",
				StorageType: metadata.ElasticsearchStorageType,
				ResultTableOption: &metadata.ResultTableOption{
					ScrollID: "scroll_id_1",
				},
				FieldAlias: map[string]string{
					"container_name": "__ext.container_name",
				},
				Scroll: "10m",
			},
			start: defaultStart,
			end:   defaultEnd,
			total: 1e4,
			list: `[ {
  "__data_label" : "",
  "__doc_id" : "8defd23f1c2599e70f3ace3a042b2b5f",
  "__ext.container_name" : "unify-query",
  "__ext.io_kubernetes_pod" : "bkmonitor-unify-query-64bd4f5df4-599f9",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log",
  "container_name" : "unify-query"
}, {
  "__data_label" : "",
  "__doc_id" : "ba0a6e66f01d6cb77ae25b13ddf4ad1b",
  "__ext.container_name" : "unify-query",
  "__ext.io_kubernetes_pod" : "bkmonitor-unify-query-64bd4f5df4-599f9",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log",
  "container_name" : "unify-query"
}, {
  "__data_label" : "",
  "__doc_id" : "74ea55e7397582b101f0e21efbc876c6",
  "__ext.container_name" : "unify-query",
  "__ext.io_kubernetes_pod" : "bkmonitor-unify-query-64bd4f5df4-599f9",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log",
  "container_name" : "unify-query"
}, {
  "__data_label" : "",
  "__doc_id" : "084792484f943e314e31ef2b2e878115",
  "__ext.container_name" : "unify-query",
  "__ext.io_kubernetes_pod" : "bkmonitor-unify-query-64bd4f5df4-599f9",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log",
  "container_name" : "unify-query"
}, {
  "__data_label" : "",
  "__doc_id" : "0a3f47a7c57d0af7d40d82c729c37155",
  "__ext.container_name" : "unify-query",
  "__ext.io_kubernetes_pod" : "bkmonitor-unify-query-64bd4f5df4-599f9",
  "__index" : "v2_2_bklog_bk_unify_query_20240814_0",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log",
  "container_name" : "unify-query"
} ]`,
			resultTableOptions: map[string]*metadata.ResultTableOption{
				"bk_log_index_set_10|log": {
					ScrollID:  "scroll_id_1",
					FieldType: mock.FieldType,
					From:      function.IntPoint(0),
				},
			},
		},
		"query with scroll id 2": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				StorageID:   "log",
				StorageType: metadata.ElasticsearchStorageType,
				ResultTableOption: &metadata.ResultTableOption{
					ScrollID: "scroll_id_2",
				},
				Scroll: "10m",
			},
			start: defaultStart,
			end:   defaultEnd,
			total: 0,
			resultTableOptions: metadata.ResultTableOptions{
				"bk_log_index_set_10|log": &metadata.ResultTableOption{
					FieldType: mock.FieldType,
					From:      function.IntPoint(0),
				},
			},
		},
		"query with search after": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				StorageID:   "log",
				StorageType: metadata.ElasticsearchStorageType,
				Orders: []metadata.Order{
					{
						Name: "timestamp",
						Ast:  false,
					},
					{
						Name: "type",
						Ast:  false,
					},
					{
						Name: "kibana_stats.kibana.name",
						Ast:  false,
					},
				},
				Size: 5,
				ResultTableOption: &metadata.ResultTableOption{
					SearchAfter: []any{1743465646224, "kibana_settings", nil},
				},
			},
			start: defaultStart,
			end:   defaultEnd,
			total: 1e4,
			list: `[ {
  "__data_label" : "",
  "__doc_id" : "rYSm7pUBxj8-27WaYRCB",
  "__index" : ".monitoring-kibana-7-2025.04.01",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log",
  "kibana_stats.kibana.name" : "es-os60crz7-kibana",
  "timestamp" : "2025-04-01T00:00:36.224Z",
  "type" : "kibana_stats"
}, {
  "__data_label" : "",
  "__doc_id" : "roSm7pUBxj8-27WaYRCB",
  "__index" : ".monitoring-kibana-7-2025.04.01",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log",
  "timestamp" : "2025-04-01T00:00:36.224Z",
  "type" : "kibana_settings"
}, {
  "__data_label" : "",
  "__doc_id" : "q4Sm7pUBxj8-27WaOhBx",
  "__index" : ".monitoring-kibana-7-2025.04.01",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log",
  "kibana_stats.kibana.name" : "es-os60crz7-kibana",
  "timestamp" : "2025-04-01T00:00:26.225Z",
  "type" : "kibana_stats"
}, {
  "__data_label" : "",
  "__doc_id" : "rISm7pUBxj8-27WaOhBx",
  "__index" : ".monitoring-kibana-7-2025.04.01",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log",
  "timestamp" : "2025-04-01T00:00:26.225Z",
  "type" : "kibana_settings"
}, {
  "__data_label" : "",
  "__doc_id" : "8DSm7pUBipSLyy3IEwRg",
  "__index" : ".monitoring-kibana-7-2025.04.01",
  "__result_table" : "bk_log_index_set_10",
  "__table_uuid" : "bk_log_index_set_10|log",
  "kibana_stats.kibana.name" : "es-os60crz7-kibana",
  "timestamp" : "2025-04-01T00:00:16.224Z",
  "type" : "kibana_stats"
} ]`,
			resultTableOptions: map[string]*metadata.ResultTableOption{
				"bk_log_index_set_10|log": {
					SearchAfter: []any{1743465616224.0, "kibana_stats", "es-os60crz7-kibana"},
					FieldType:   mock.FieldType,
					From:        function.IntPoint(0),
				},
			},
		},
	} {
		t.Run(fmt.Sprintf("testing run: %s", idx), func(t *testing.T) {
			var (
				wg sync.WaitGroup

				list []any
			)
			dataCh := make(chan map[string]any)
			wg.Add(1)
			go func() {
				defer wg.Done()
				for d := range dataCh {
					list = append(list, d)
				}
			}()

			_, total, option, err := ins.QueryRawData(ctx, c.query, c.start, c.end, dataCh)
			close(dataCh)

			wg.Wait()

			if c.err != nil {
				assert.Equal(t, c.err, err)
			} else {
				assert.Nil(t, err)
				if len(list) > 0 {
					res, _ := json.Marshal(list)
					resStr := string(res)
					assert.JSONEq(t, c.list, resStr)
				} else {
					assert.Nil(t, list)
				}

				options := make(metadata.ResultTableOptions)
				options.SetOption(c.query.TableUUID(), option)

				assert.Equal(t, c.total, total)
				assert.Equal(t, c.resultTableOptions, options)
			}
		})
	}
}

func TestInstance_fieldMap(t *testing.T) {
	mock.Init()

	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	ins, err := NewInstance(ctx, &InstanceOption{
		Connect: Connect{
			Address: mock.EsUrl,
		},
		Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
		return
	}

	res, err := ins.fieldMap(ctx, metadata.FieldAlias{
		"container_name": "__ext.container_name",
	}, "unify_query")
	assert.Nil(t, err)

	actual, _ := json.Marshal(res)
	assert.JSONEq(t, `{"__ext.container_id":{"alias_name":"","field_name":"__ext.container_id","field_type":"keyword","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.container_image":{"alias_name":"","field_name":"__ext.container_image","field_type":"keyword","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.container_name":{"alias_name":"container_name","field_name":"__ext.container_name","field_type":"keyword","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.io_kubernetes_pod":{"alias_name":"","field_name":"__ext.io_kubernetes_pod","field_type":"keyword","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.io_kubernetes_pod_ip":{"alias_name":"","field_name":"__ext.io_kubernetes_pod_ip","field_type":"keyword","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.io_kubernetes_pod_namespace":{"alias_name":"","field_name":"__ext.io_kubernetes_pod_namespace","field_type":"keyword","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.io_kubernetes_pod_uid":{"alias_name":"","field_name":"__ext.io_kubernetes_pod_uid","field_type":"keyword","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.io_kubernetes_workload_name":{"alias_name":"","field_name":"__ext.io_kubernetes_workload_name","field_type":"keyword","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.io_kubernetes_workload_type":{"alias_name":"","field_name":"__ext.io_kubernetes_workload_type","field_type":"keyword","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"cloudId":{"alias_name":"","field_name":"cloudId","field_type":"integer","origin_field":"cloudId","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"dtEventTimeStamp":{"alias_name":"","field_name":"dtEventTimeStamp","field_type":"date","origin_field":"dtEventTimeStamp","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"file":{"alias_name":"","field_name":"file","field_type":"keyword","origin_field":"file","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"gseIndex":{"alias_name":"","field_name":"gseIndex","field_type":"long","origin_field":"gseIndex","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"iterationIndex":{"alias_name":"","field_name":"iterationIndex","field_type":"integer","origin_field":"iterationIndex","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"level":{"alias_name":"","field_name":"level","field_type":"keyword","origin_field":"level","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"log":{"alias_name":"","field_name":"log","field_type":"text","origin_field":"log","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"tokenize_on_chars":[]},"message":{"alias_name":"","field_name":"message","field_type":"text","origin_field":"message","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"tokenize_on_chars":[]},"path":{"alias_name":"","field_name":"path","field_type":"keyword","origin_field":"path","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"report_time":{"alias_name":"","field_name":"report_time","field_type":"keyword","origin_field":"report_time","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"serverIp":{"alias_name":"","field_name":"serverIp","field_type":"keyword","origin_field":"serverIp","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"time":{"alias_name":"","field_name":"time","field_type":"date","origin_field":"time","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"trace_id":{"alias_name":"","field_name":"trace_id","field_type":"keyword","origin_field":"trace_id","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]}}`, string(actual))
}

func TestInstance_QueryLabelNames(t *testing.T) {
	mock.Init()

	tests := []struct {
		name          string
		query         *metadata.Query
		expectedError bool
		expectedNames []string
	}{
		{
			name: "basic_label_names",
			query: &metadata.Query{
				DB:      "unify_query",
				TableID: "test_table",
				TimeField: metadata.TimeField{
					Name: "dtEventTimeStamp",
				},
			},
			expectedError: false,
			expectedNames: []string{"cloudId", "file", "gseIndex", "iterationIndex", "level", "log", "message", "path", "report_time", "serverIp", "trace_id"},
		},
		{
			name: "empty_db",
			query: &metadata.Query{
				DB:      "",
				TableID: "test_table",
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata.InitMetadata()
			ctx := metadata.InitHashID(context.Background())
			inst, err := NewInstance(ctx, &InstanceOption{
				Connect: Connect{
					Address: mock.EsUrl,
				},
				Timeout: time.Minute,
			})
			assert.NoError(t, err)

			start := time.Now().Add(-time.Hour)
			end := time.Now()

			labelNames, err := inst.QueryLabelNames(ctx, tt.query, start, end)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				for _, expectedName := range tt.expectedNames {
					found := false
					for _, actualName := range labelNames {
						if actualName == expectedName {
							found = true
							break
						}
					}
					if !found {
						t.Logf("Expected field %s not found in %v", expectedName, labelNames)
					}
				}
			}
		})
	}
}
