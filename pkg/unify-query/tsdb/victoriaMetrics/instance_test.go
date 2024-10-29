// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package victoriaMetrics

import (
	"context"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestInstance_DirectLabelValues(t *testing.T) {
	testCases := map[string]struct {
		name  string
		limit int
		match string

		expected []string
	}{
		"test_1": {
			name:     "pod",
			match:    `container_cpu_usage_seconds_total{container="unify-query"}`,
			expected: []string{"bk-datalink-unify-query-54b9d9b49b-6lvvz", "bk-datalink-unify-query-54b9d9b49b-c7fls", "bk-datalink-unify-query-54b9d9b49b-fhjxl", "bk-datalink-unify-query-54b9d9b49b-jqzl8", "bk-datalink-unify-query-975bc68f-tqp2x", "bk-datalink-unify-query-975bc68f-tvzwd", "bk-datalink-unify-query-9c9d779fc-5wmsq", "bk-datalink-unify-query-9c9d779fc-6k7rq", "bk-datalink-unify-query-9c9d779fc-7kg8g"},
		},
	}

	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	mock.Vm.Set(map[string]any{
		`label_values:17301804581730184058pod{container_cpu_usage_seconds_total{container="unify-query"}}`: `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":null,"cluster":"monitor-op","totalRecords":548,"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"status":"success","isPartial":false,"data":["bk-datalink-unify-query-54b9d9b49b-6lvvz","bk-datalink-unify-query-54b9d9b49b-c7fls","bk-datalink-unify-query-54b9d9b49b-fhjxl","bk-datalink-unify-query-54b9d9b49b-jqzl8","bk-datalink-unify-query-975bc68f-tqp2x","bk-datalink-unify-query-975bc68f-tvzwd","bk-datalink-unify-query-9c9d779fc-5wmsq","bk-datalink-unify-query-9c9d779fc-6k7rq","bk-datalink-unify-query-9c9d779fc-7kg8g"]}],"select_fields_order":[],"sql":"{container=\"unify-query\", result_table_id=\"vm_rt\", __name__=\"container_cpu_usage_seconds_total_value\"}","total_record_size":68760,"timetaken":0.0,"bksql_call_elapsed_time":0,"device":"vm","result_table_ids":["2_bcs_prom_computation_result_table_00000"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,
	})

	instance := &Instance{
		url:     mock.VmUrl,
		timeout: time.Minute,
		curl:    &curl.HttpCurl{},
	}
	start := time.Unix(1730180458, 0)
	end := time.Unix(1730184058, 0)

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			matchers, _ := parser.ParseMetricSelector("a")
			expand := &metadata.VmExpand{
				ResultTableList: []string{"vm_rt"},
				MetricFilterCondition: map[string]string{
					"a": c.match,
				},
			}
			metadata.SetExpand(ctx, expand)

			lb, err := instance.DirectLabelValues(ctx, c.name, start, end, c.limit, matchers...)
			if err != nil {
				log.Fatalf(ctx, err.Error())
				return
			}

			assert.Equal(t, c.expected, lb)
		})
	}
}
