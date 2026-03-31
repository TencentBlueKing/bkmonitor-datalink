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
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	uqtrace "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

var (
	vmCondition metadata.VmCondition = `__name__="container_cpu_usage_seconds_total_value", result_table_id="2_bcs_prom_computation_result_table_00000", container="unify-query"`
	vmRt        string               = "2_bcs_prom_computation_result_table_00000"

	instance = &Instance{
		url:     mock.BkBaseUrl,
		timeout: time.Minute * 5,
		curl:    &curl.HttpCurl{},
	}
)

func TestInstance_DirectLabelValues(t *testing.T) {
	testCases := map[string]struct {
		name  string
		limit int

		expected string
	}{
		"test_1": {
			name:     "pod",
			expected: `["bk-datalink-unify-query-6459767d5f-5vsjr","bk-datalink-unify-query-6459767d5f-m9w6t","bk-datalink-unify-query-6459767d5f-nmx72","bk-datalink-unify-query-6459767d5f-qq8nq","bk-datalink-unify-query-778b5bdf95-7hpc9","bk-datalink-unify-query-778b5bdf95-dpkwm","bk-datalink-unify-query-778b5bdf95-x2wfd","bk-datalink-unify-query-778b5bdf95-xpq5z","bk-datalink-unify-query-7f4dd9fcf8-9g6kb","bk-datalink-unify-query-7f4dd9fcf8-9ggjq","bk-datalink-unify-query-7f4dd9fcf8-9x9bt","bk-datalink-unify-query-7f4dd9fcf8-rfhtb","bk-datalink-unify-query-8555c9f9b9-q7dct","bk-datalink-unify-query-8555c9f9b9-rhpvw","bk-datalink-unify-query-8555c9f9b9-vf8p6","bk-datalink-unify-query-8555c9f9b9-xspxj","bk-datalink-unify-query-85c54f79d8-6lfvd","bk-datalink-unify-query-85c54f79d8-dz9t7","bk-datalink-unify-query-85c54f79d8-nlzmc","bk-datalink-unify-query-85c54f79d8-qzqfz","bk-datalink-unify-query-b9c8f446d-8xk79","bk-datalink-unify-query-b9c8f446d-d48br","bk-datalink-unify-query-b9c8f446d-sch6k","bk-datalink-unify-query-b9c8f446d-tpdwn","bk-datalink-unify-query-test-66f7ccb78d-jf4m2","bk-datalink-unify-query-test-8445575f5d-mrppp","bk-datalink-unify-query-test-c8b988c78-xdcgq"]`,
		},
	}

	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	mock.Vm.Set(map[string]any{
		`label_values:17301804581730184058pod{__name__="container_cpu_usage_seconds_total_value", result_table_id="2_bcs_prom_computation_result_table_00000", container="unify-query"}`: `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":null,"cluster":"monitor-op","totalRecords":27,"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"status":"success","isPartial":false,"data":["bk-datalink-unify-query-6459767d5f-5vsjr","bk-datalink-unify-query-6459767d5f-m9w6t","bk-datalink-unify-query-6459767d5f-nmx72","bk-datalink-unify-query-6459767d5f-qq8nq","bk-datalink-unify-query-778b5bdf95-7hpc9","bk-datalink-unify-query-778b5bdf95-dpkwm","bk-datalink-unify-query-778b5bdf95-x2wfd","bk-datalink-unify-query-778b5bdf95-xpq5z","bk-datalink-unify-query-7f4dd9fcf8-9g6kb","bk-datalink-unify-query-7f4dd9fcf8-9ggjq","bk-datalink-unify-query-7f4dd9fcf8-9x9bt","bk-datalink-unify-query-7f4dd9fcf8-rfhtb","bk-datalink-unify-query-8555c9f9b9-q7dct","bk-datalink-unify-query-8555c9f9b9-rhpvw","bk-datalink-unify-query-8555c9f9b9-vf8p6","bk-datalink-unify-query-8555c9f9b9-xspxj","bk-datalink-unify-query-85c54f79d8-6lfvd","bk-datalink-unify-query-85c54f79d8-dz9t7","bk-datalink-unify-query-85c54f79d8-nlzmc","bk-datalink-unify-query-85c54f79d8-qzqfz","bk-datalink-unify-query-b9c8f446d-8xk79","bk-datalink-unify-query-b9c8f446d-d48br","bk-datalink-unify-query-b9c8f446d-sch6k","bk-datalink-unify-query-b9c8f446d-tpdwn","bk-datalink-unify-query-test-66f7ccb78d-jf4m2","bk-datalink-unify-query-test-8445575f5d-mrppp","bk-datalink-unify-query-test-c8b988c78-xdcgq"]}],"select_fields_order":[],"sql":"{__name__=\"container_cpu_usage_seconds_total_value\", result_table_id=\"2_bcs_prom_computation_result_table_00000\", container=\"unify-query\"}","total_record_size":3928,"timetaken":0.0,"bksql_call_elapsed_time":0,"device":"vm","result_table_ids":["2_bcs_prom_computation_result_table_00000"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,
	})

	start := time.Unix(1730180458, 0)
	end := time.Unix(1730184058, 0)

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			matchers, _ := parser.ParseMetricSelector("a")
			expand := &metadata.VmExpand{
				ResultTableList: []string{"2_bcs_prom_computation_result_table_00000"},
				MetricFilterCondition: map[string]string{
					"a": vmCondition.String(),
				},
			}
			metadata.SetExpand(ctx, expand)

			res, err := instance.DirectLabelValues(ctx, c.name, start, end, c.limit, matchers...)
			if err != nil {
				log.Fatalf(ctx, "%s", err.Error())
				return
			}

			actual, _ := json.Marshal(res)
			assert.Equal(t, c.expected, string(actual))
		})
	}
}

func TestInstance_DirectQueryRange(t *testing.T) {
	testCases := map[string]struct {
		promql   string
		expected string
	}{
		"test_1": {
			promql:   fmt.Sprintf(`sum(increase(%s[1m])) by (pod)`, vmCondition.ToMatch()),
			expected: `[{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-5vsjr"},"values":[[1730181358,"0.044650242"],[1730181658,"9.545676339999996"],[1730181958,"10.425697591999999"],[1730182258,"3.0199220649999887"],[1730182558,"6.747614702000007"],[1730182858,"6.133533471000021"],[1730183158,"8.81771028990002"],[1730183458,"10.132931282100003"],[1730183758,"3.5652613130999953"],[1730184058,"7.727230415000008"]]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-m9w6t"},"values":[[1730181658,"12.404203941999995"],[1730181958,"5.875412668000003"],[1730182258,"5.044704286999988"],[1730182558,"8.997354142000006"],[1730182858,"7.50822000010001"],[1730183158,"5.585088850900007"],[1730183458,"9.202267196999998"],[1730183758,"5.616763429000031"],[1730184058,"6.6475119441000174"]]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-nmx72"},"values":[[1730181358,"0.064899209"],[1730181658,"10.086907809000003"],[1730181958,"3.9349868299999997"],[1730182258,"7.714302543999992"],[1730182558,"5.458358707999992"],[1730182858,"9.634142672100012"],[1730183158,"6.135633479999996"],[1730183458,"8.975162369999993"],[1730183758,"5.904537907999952"],[1730184058,"6.029346786000019"]]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-qq8nq"},"values":[[1730181658,"7.466532553"],[1730181958,"5.734228741999999"],[1730182258,"5.188748330999999"],[1730182558,"6.4631616860000065"],[1730182858,"6.180980588000011"],[1730183158,"5.984101682000016"],[1730183458,"6.966740698999985"],[1730183758,"5.611999492999985"],[1730184058,"4.7183045600000355"]]},{"metric":{"pod":"bk-datalink-unify-query-778b5bdf95-x2wfd"},"values":[[1730180458,"10.2905197900036"]]},{"metric":{"pod":"bk-datalink-unify-query-85c54f79d8-6lfvd"},"values":[[1730180758,"7.1915653179999985"],[1730181058,"4.9068547820000035"],[1730181358,"6.607650964000001"]]},{"metric":{"pod":"bk-datalink-unify-query-85c54f79d8-dz9t7"},"values":[[1730180758,"5.833707484999998"],[1730181058,"4.507901762000003"],[1730181358,"4.412351498000007"]]},{"metric":{"pod":"bk-datalink-unify-query-85c54f79d8-nlzmc"},"values":[[1730180758,"7.477196797999998"],[1730181058,"7.134382289999998"],[1730181358,"4.839466496"]]},{"metric":{"pod":"bk-datalink-unify-query-85c54f79d8-qzqfz"},"values":[[1730180758,"6.606840334000001"],[1730181058,"5.034415747000004"],[1730181358,"4.916271132000006"]]},{"metric":{"pod":"bk-datalink-unify-query-test-66f7ccb78d-jf4m2"},"values":[[1730180458,"3.661851597"],[1730180758,"0.3898651899999992"],[1730181058,"0.41139455499999933"]]}]`,
		},
	}

	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	mock.Vm.Set(map[string]any{
		`query_range:17301804581730184058300sum(increase({__name__="container_cpu_usage_seconds_total_value", result_table_id="2_bcs_prom_computation_result_table_00000", container="unify-query"}[1m])) by (pod)`: `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":null,"cluster":"monitor-op","totalRecords":10,"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"status":"success","isPartial":false,"data":{"resultType":"matrix","result":[{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-5vsjr"},"values":[[1730181358,"0.044650242"],[1730181658,"9.545676339999996"],[1730181958,"10.425697591999999"],[1730182258,"3.0199220649999887"],[1730182558,"6.747614702000007"],[1730182858,"6.133533471000021"],[1730183158,"8.81771028990002"],[1730183458,"10.132931282100003"],[1730183758,"3.5652613130999953"],[1730184058,"7.727230415000008"]]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-m9w6t"},"values":[[1730181658,"12.404203941999995"],[1730181958,"5.875412668000003"],[1730182258,"5.044704286999988"],[1730182558,"8.997354142000006"],[1730182858,"7.50822000010001"],[1730183158,"5.585088850900007"],[1730183458,"9.202267196999998"],[1730183758,"5.616763429000031"],[1730184058,"6.6475119441000174"]]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-nmx72"},"values":[[1730181358,"0.064899209"],[1730181658,"10.086907809000003"],[1730181958,"3.9349868299999997"],[1730182258,"7.714302543999992"],[1730182558,"5.458358707999992"],[1730182858,"9.634142672100012"],[1730183158,"6.135633479999996"],[1730183458,"8.975162369999993"],[1730183758,"5.904537907999952"],[1730184058,"6.029346786000019"]]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-qq8nq"},"values":[[1730181658,"7.466532553"],[1730181958,"5.734228741999999"],[1730182258,"5.188748330999999"],[1730182558,"6.4631616860000065"],[1730182858,"6.180980588000011"],[1730183158,"5.984101682000016"],[1730183458,"6.966740698999985"],[1730183758,"5.611999492999985"],[1730184058,"4.7183045600000355"]]},{"metric":{"pod":"bk-datalink-unify-query-778b5bdf95-x2wfd"},"values":[[1730180458,"10.2905197900036"]]},{"metric":{"pod":"bk-datalink-unify-query-85c54f79d8-6lfvd"},"values":[[1730180758,"7.1915653179999985"],[1730181058,"4.9068547820000035"],[1730181358,"6.607650964000001"]]},{"metric":{"pod":"bk-datalink-unify-query-85c54f79d8-dz9t7"},"values":[[1730180758,"5.833707484999998"],[1730181058,"4.507901762000003"],[1730181358,"4.412351498000007"]]},{"metric":{"pod":"bk-datalink-unify-query-85c54f79d8-nlzmc"},"values":[[1730180758,"7.477196797999998"],[1730181058,"7.134382289999998"],[1730181358,"4.839466496"]]},{"metric":{"pod":"bk-datalink-unify-query-85c54f79d8-qzqfz"},"values":[[1730180758,"6.606840334000001"],[1730181058,"5.034415747000004"],[1730181358,"4.916271132000006"]]},{"metric":{"pod":"bk-datalink-unify-query-test-66f7ccb78d-jf4m2"},"values":[[1730180458,"3.661851597"],[1730180758,"0.3898651899999992"],[1730181058,"0.41139455499999933"]]}]},"stats":{"seriesFetched":"14"}}],"select_fields_order":[],"sql":"sum (increase({__name__=\"container_cpu_usage_seconds_total_value\", result_table_id=\"2_bcs_prom_computation_result_table_00000\", container=\"unify-query\"}[1m])) by(pod)","total_record_size":13832,"timetaken":0.0,"bksql_call_elapsed_time":0,"device":"vm","bk_biz_ids":["100801",555],"result_table_ids":["2_bcs_prom_computation_result_table_00000"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,
	})

	instance := &Instance{
		url:     mock.BkBaseUrl,
		timeout: time.Minute * 5,
		curl:    &curl.HttpCurl{},
	}
	start := time.Unix(1730180458, 0)
	end := time.Unix(1730184058, 0)
	step := time.Minute * 5

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)

			expand := &metadata.VmExpand{
				ResultTableList: []string{"2_bcs_prom_computation_result_table_00000"},
			}
			metadata.SetExpand(ctx, expand)

			res, _, err := instance.DirectQueryRange(ctx, c.promql, start, end, step)
			if err != nil {
				log.Fatalf(ctx, "%s", err.Error())
				return
			}

			actual, _ := json.Marshal(res)
			assert.Equal(t, c.expected, string(actual))
		})
	}
}

func TestInstance_DirectQuery(t *testing.T) {
	testCases := map[string]struct {
		promql   string
		expected string
	}{
		"test_1": {
			promql:   fmt.Sprintf(`sum(increase(%s[1m])) by (pod)`, vmCondition.ToMatch()),
			expected: `[{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-5vsjr"},"value":[1730184058,"7.727230415000008"]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-m9w6t"},"value":[1730184058,"6.6475119441000174"]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-nmx72"},"value":[1730184058,"6.029346786000019"]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-qq8nq"},"value":[1730184058,"4.7183045600000355"]}]`,
		},
	}

	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	mock.Vm.Set(map[string]any{
		`query:1730184058sum(increase({__name__="container_cpu_usage_seconds_total_value", result_table_id="2_bcs_prom_computation_result_table_00000", container="unify-query"}[1m])) by (pod)`: `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":null,"cluster":"monitor-op","totalRecords":4,"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"status":"success","isPartial":false,"data":{"resultType":"vector","result":[{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-5vsjr"},"value":[1730184058,"7.727230415000008"]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-m9w6t"},"value":[1730184058,"6.6475119441000174"]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-nmx72"},"value":[1730184058,"6.029346786000019"]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-qq8nq"},"value":[1730184058,"4.7183045600000355"]}]},"stats":{"seriesFetched":"4"}}],"select_fields_order":[],"sql":"sum (increase({__name__=\"container_cpu_usage_seconds_total_value\", result_table_id=\"2_bcs_prom_computation_result_table_00000\", container=\"unify-query\"}[1m])) by(pod)","total_record_size":3600,"timetaken":0.0,"bksql_call_elapsed_time":0,"device":"vm","result_table_ids":["2_bcs_prom_computation_result_table_00000"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,
	})

	end := time.Unix(1730184058, 0)

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)

			expand := &metadata.VmExpand{
				ResultTableList: []string{"2_bcs_prom_computation_result_table_00000"},
			}
			metadata.SetExpand(ctx, expand)

			res, err := instance.DirectQuery(ctx, c.promql, end)
			if err != nil {
				log.Fatalf(ctx, "%s", err.Error())
				return
			}

			actual, _ := json.Marshal(res)
			assert.Equal(t, c.expected, string(actual))
		})
	}
}

func TestInstance_QueryLabelValues(t *testing.T) {
	ctx := t.Context()
	testCases := map[string]struct {
		name     string
		expected string
		start    int64
		end      int64
	}{
		"test_1": {
			name:     "pod",
			expected: `["bk-datalink-unify-query-6459767d5f-5vsjr","bk-datalink-unify-query-6459767d5f-m9w6t","bk-datalink-unify-query-6459767d5f-nmx72","bk-datalink-unify-query-6459767d5f-qq8nq","bk-datalink-unify-query-778b5bdf95-7hpc9","bk-datalink-unify-query-778b5bdf95-dpkwm","bk-datalink-unify-query-778b5bdf95-x2wfd","bk-datalink-unify-query-778b5bdf95-xpq5z","bk-datalink-unify-query-85c54f79d8-6lfvd","bk-datalink-unify-query-85c54f79d8-dz9t7","bk-datalink-unify-query-85c54f79d8-nlzmc","bk-datalink-unify-query-85c54f79d8-qzqfz","bk-datalink-unify-query-test-66f7ccb78d-jf4m2","bk-datalink-unify-query-test-8445575f5d-mrppp"]`,
			start:    1730180458,
			end:      1730184058,
		},
	}

	mock.Init()

	mock.Vm.Set(map[string]any{
		`query_range:17301804581730184058360count({__name__="container_cpu_usage_seconds_total_value", result_table_id="2_bcs_prom_computation_result_table_00000", container="unify-query"}) by (pod)`: `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":null,"cluster":"monitor-op","totalRecords":14,"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"status":"success","isPartial":false,"data":{"resultType":"matrix","result":[{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-5vsjr"},"values":[[1730181538,"1"],[1730181898,"1"],[1730182258,"1"],[1730182618,"1"],[1730182978,"1"],[1730183338,"1"],[1730183698,"1"],[1730184058,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-m9w6t"},"values":[[1730181538,"1"],[1730181898,"1"],[1730182258,"1"],[1730182618,"1"],[1730182978,"1"],[1730183338,"1"],[1730183698,"1"],[1730184058,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-nmx72"},"values":[[1730181538,"1"],[1730181898,"1"],[1730182258,"1"],[1730182618,"1"],[1730182978,"1"],[1730183338,"1"],[1730183698,"1"],[1730184058,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-qq8nq"},"values":[[1730181538,"1"],[1730181898,"1"],[1730182258,"1"],[1730182618,"1"],[1730182978,"1"],[1730183338,"1"],[1730183698,"1"],[1730184058,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-778b5bdf95-7hpc9"},"values":[[1730180458,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-778b5bdf95-dpkwm"},"values":[[1730180458,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-778b5bdf95-x2wfd"},"values":[[1730180458,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-778b5bdf95-xpq5z"},"values":[[1730180458,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-85c54f79d8-6lfvd"},"values":[[1730180818,"1"],[1730181178,"1"],[1730181538,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-85c54f79d8-dz9t7"},"values":[[1730180818,"1"],[1730181178,"1"],[1730181538,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-85c54f79d8-nlzmc"},"values":[[1730180818,"1"],[1730181178,"1"],[1730181538,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-85c54f79d8-qzqfz"},"values":[[1730180818,"1"],[1730181178,"1"],[1730181538,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-test-66f7ccb78d-jf4m2"},"values":[[1730180458,"1"],[1730180818,"1"],[1730181178,"1"],[1730181538,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-test-8445575f5d-mrppp"},"values":[[1730180458,"1"]]}]},"stats":{"seriesFetched":"14"}}],"select_fields_order":[],"sql":"count ({__name__=\"container_cpu_usage_seconds_total_value\", result_table_id=\"2_bcs_prom_computation_result_table_00000\", container=\"unify-query\"}) by(pod)","total_record_size":13920,"timetaken":0.0,"bksql_call_elapsed_time":0,"device":"vm","result_table_ids":["2_bcs_prom_computation_result_table_00000"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,
	})

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			metadata.SetUser(ctx, &metadata.User{SpaceUID: influxdb.SpaceUid})
			start := time.Unix(c.start, 0)
			end := time.Unix(c.end, 0)

			query := &metadata.Query{
				VmRt:        vmRt,
				VmCondition: vmCondition,
			}

			res, err := instance.QueryLabelValues(ctx, query, c.name, start, end)
			if err != nil {
				log.Fatalf(ctx, "%s", err.Error())
				return
			}

			sort.Strings(res)
			actual, _ := json.Marshal(res)
			assert.Equal(t, c.expected, string(actual))
		})
	}
}

func TestVmResponse_VmQueryCluster(t *testing.T) {
	t.Run("with vm_query_cluster", func(t *testing.T) {
		raw := `{
			"result": true,
			"message": "成功",
			"code": "00",
			"data": {
				"result_table_scan_range": null,
				"cluster": "monitor-op",
				"totalRecords": 1,
				"list": [{
					"status": "success",
					"isPartial": false,
					"data": {
						"resultType": "vector",
						"result": [{"metric": {"pod": "test-pod"}, "value": [1730184058, "1.0"]}]
					}
				}],
				"select_fields_order": [],
				"sql": "test",
				"timetaken": 0.0,
				"bksql_call_elapsed_time": 0,
				"device": "vm",
				"result_table_ids": ["2_test_rt"],
				"vm_query_cluster": {
"query_cluster": "vm-query-history.example.com",
					"storage_cluster_list": ["vm-op1", "vm-op2"]
				}
			},
			"errors": null
		}`

		var resp VmResponse
		err := json.Unmarshal([]byte(raw), &resp)
		assert.NoError(t, err)

		assert.True(t, resp.Result)
		assert.Equal(t, OK, resp.Code)
		assert.NotNil(t, resp.Data.VmQueryCluster)
		assert.Equal(t, "vm-query-history.example.com", resp.Data.VmQueryCluster.QueryCluster)
		assert.Equal(t, []string{"vm-op1", "vm-op2"}, resp.Data.VmQueryCluster.StorageClusterList)
	})

	t.Run("without vm_query_cluster", func(t *testing.T) {
		raw := `{
			"result": true,
			"message": "成功",
			"code": "00",
			"data": {
				"result_table_scan_range": null,
				"cluster": "monitor-op",
				"totalRecords": 1,
				"list": [{
					"status": "success",
					"isPartial": false,
					"data": {
						"resultType": "vector",
						"result": [{"metric": {"pod": "test-pod"}, "value": [1730184058, "1.0"]}]
					}
				}],
				"select_fields_order": [],
				"sql": "test",
				"timetaken": 0.0,
				"bksql_call_elapsed_time": 0,
				"device": "vm",
				"result_table_ids": ["2_test_rt"]
			},
			"errors": null
		}`

		var resp VmResponse
		err := json.Unmarshal([]byte(raw), &resp)
		assert.NoError(t, err)

		assert.True(t, resp.Result)
		assert.Nil(t, resp.Data.VmQueryCluster)
	})

	t.Run("with full vm_query_cluster fields", func(t *testing.T) {
		raw := `{
			"result": true,
			"message": "成功",
			"code": "00",
			"data": {
				"result_table_scan_range": null,
				"cluster": "monitor-op",
				"totalRecords": 0,
				"list": [],
				"select_fields_order": [],
				"sql": "test",
				"timetaken": 0.0,
				"bksql_call_elapsed_time": 0,
				"device": "vm",
				"result_table_ids": ["2_test_rt"],
				"vm_query_cluster": {
"query_cluster": "vm-query-history.example.com",
					"storage_cluster_list": ["vm-op1", "vm-op2"]
				}
			},
			"errors": null
		}`

		var resp VmResponse
		err := json.Unmarshal([]byte(raw), &resp)
		assert.NoError(t, err)

		assert.NotNil(t, resp.Data.VmQueryCluster)
		assert.Equal(t, "vm-query-history.example.com", resp.Data.VmQueryCluster.QueryCluster)
		assert.Equal(t, []string{"vm-op1", "vm-op2"}, resp.Data.VmQueryCluster.StorageClusterList)
	})

	t.Run("with empty storage_cluster_list", func(t *testing.T) {
		raw := `{
			"result": true,
			"message": "成功",
			"code": "00",
			"data": {
				"result_table_scan_range": null,
				"cluster": "monitor-op",
				"totalRecords": 0,
				"list": [],
				"select_fields_order": [],
				"sql": "test",
				"timetaken": 0.0,
				"bksql_call_elapsed_time": 0,
				"device": "vm",
				"result_table_ids": ["2_test_rt"],
				"vm_query_cluster": {
"query_cluster": "vm-query.example.com",
					"storage_cluster_list": []
				}
			},
			"errors": null
		}`

		var resp VmResponse
		err := json.Unmarshal([]byte(raw), &resp)
		assert.NoError(t, err)

		assert.NotNil(t, resp.Data.VmQueryCluster)
		assert.Equal(t, "vm-query.example.com", resp.Data.VmQueryCluster.QueryCluster)
		assert.Empty(t, resp.Data.VmQueryCluster.StorageClusterList)
	})
}

func TestVmLableValuesResponse_VmQueryCluster(t *testing.T) {
	raw := `{
		"result": true,
		"message": "成功",
		"code": "00",
		"data": {
			"result_table_scan_range": null,
			"cluster": "monitor-op",
			"totalRecords": 2,
			"list": [{
				"status": "success",
				"isPartial": false,
				"data": ["val1", "val2"]
			}],
			"select_fields_order": [],
			"sql": "test",
			"timetaken": 0.0,
			"bksql_call_elapsed_time": 0,
			"device": "vm",
			"result_table_ids": ["2_test_rt"],
			"vm_query_cluster": {
				"query_cluster": "vm-query.example.com",
				"storage_cluster_list": ["storage-1"]
			}
		},
		"errors": null
	}`

	var resp VmLableValuesResponse
	err := json.Unmarshal([]byte(raw), &resp)
	assert.NoError(t, err)

	assert.NotNil(t, resp.Data.VmQueryCluster)
	assert.Equal(t, "vm-query.example.com", resp.Data.VmQueryCluster.QueryCluster)
	assert.Equal(t, []string{"storage-1"}, resp.Data.VmQueryCluster.StorageClusterList)
}

func TestVmSeriesResponse_VmQueryCluster(t *testing.T) {
	raw := `{
		"result": true,
		"message": "成功",
		"code": "00",
		"data": {
			"result_table_scan_range": null,
			"cluster": "monitor-op",
			"totalRecords": 1,
			"list": [{
				"status": "success",
				"isPartial": false,
				"data": [{"__name__": "cpu_usage", "pod": "test-pod"}]
			}],
			"select_fields_order": [],
			"sql": "test",
			"timetaken": 0.0,
			"bksql_call_elapsed_time": 0,
			"device": "vm",
			"result_table_ids": ["2_test_rt"],
			"vm_query_cluster": {
				"query_cluster": "vm-series.example.com",
				"storage_cluster_list": ["s1", "s2", "s3"]
			}
		},
		"errors": null
	}`

	var resp VmSeriesResponse
	err := json.Unmarshal([]byte(raw), &resp)
	assert.NoError(t, err)

	assert.NotNil(t, resp.Data.VmQueryCluster)
	assert.Equal(t, "vm-series.example.com", resp.Data.VmQueryCluster.QueryCluster)
	assert.Equal(t, []string{"s1", "s2", "s3"}, resp.Data.VmQueryCluster.StorageClusterList)
}

func TestInstance_DirectQuery_WithVmQueryCluster(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	// mock response 携带 vm_query_cluster
	mock.Vm.Set(map[string]any{
		`query:1730184058sum(increase({__name__="container_cpu_usage_seconds_total_value", result_table_id="2_bcs_prom_computation_result_table_00000", container="unify-query"}[1m])) by (pod)`: `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":null,"cluster":"monitor-op","totalRecords":1,"list":[{"status":"success","isPartial":false,"data":{"resultType":"vector","result":[{"metric":{"pod":"test-pod"},"value":[1730184058,"1.5"]}]}}],"select_fields_order":[],"sql":"test","timetaken":0.0,"bksql_call_elapsed_time":0,"device":"vm","result_table_ids":["2_bcs_prom_computation_result_table_00000"],"vm_query_cluster":{"query_cluster":"vm-query-history.example.com","storage_cluster_list":["vm-op1","vm-op2"]}},"errors":null}`,
	})

	end := time.Unix(1730184058, 0)

	ctx = metadata.InitHashID(ctx)
	expand := &metadata.VmExpand{
		ResultTableList: []string{"2_bcs_prom_computation_result_table_00000"},
	}
	metadata.SetExpand(ctx, expand)

	res, err := instance.DirectQuery(ctx, fmt.Sprintf(`sum(increase(%s[1m])) by (pod)`, vmCondition.ToMatch()), end)
	assert.NoError(t, err)
	assert.Len(t, res, 1)

	actual, _ := json.Marshal(res)
	assert.Equal(t, `[{"metric":{"pod":"test-pod"},"value":[1730184058,"1.5"]}]`, string(actual))
}

func TestInstance_DirectQueryRange_WithVmQueryCluster(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	// mock response 携带 vm_query_cluster
	mock.Vm.Set(map[string]any{
		`query_range:17301804581730184058300sum(increase({__name__="container_cpu_usage_seconds_total_value", result_table_id="2_bcs_prom_computation_result_table_00000", container="unify-query"}[1m])) by (pod)`: `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":null,"cluster":"monitor-op","totalRecords":1,"list":[{"status":"success","isPartial":false,"data":{"resultType":"matrix","result":[{"metric":{"pod":"test-pod"},"values":[[1730181358,"0.5"],[1730181658,"1.0"]]}]}}],"select_fields_order":[],"sql":"test","timetaken":0.0,"bksql_call_elapsed_time":0,"device":"vm","result_table_ids":["2_bcs_prom_computation_result_table_00000"],"vm_query_cluster":{"query_cluster":"vm-query-history.example.com","storage_cluster_list":["vm-op1","vm-op2"]}},"errors":null}`,
	})

	start := time.Unix(1730180458, 0)
	end := time.Unix(1730184058, 0)
	step := time.Minute * 5

	ctx = metadata.InitHashID(ctx)
	expand := &metadata.VmExpand{
		ResultTableList: []string{"2_bcs_prom_computation_result_table_00000"},
	}
	metadata.SetExpand(ctx, expand)

	res, _, err := instance.DirectQueryRange(ctx, fmt.Sprintf(`sum(increase(%s[1m])) by (pod)`, vmCondition.ToMatch()), start, end, step)
	assert.NoError(t, err)
	assert.Len(t, res, 1)
}

func spanAttrString(attrs []attribute.KeyValue, key string) (string, bool) {
	for _, kv := range attrs {
		if string(kv.Key) == key {
			return kv.Value.AsString(), true
		}
	}
	return "", false
}

func spanAttrStringSlice(attrs []attribute.KeyValue, key string) ([]string, bool) {
	for _, kv := range attrs {
		if string(kv.Key) == key {
			return kv.Value.AsStringSlice(), true
		}
	}
	return nil, false
}

func TestSpanSetVmQueryClusterIfPresent(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prevTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
	})

	t.Run("writes JSON for vm-data prefix", func(t *testing.T) {
		rec.Reset()
		_, span := uqtrace.NewSpan(context.Background(), "test-span")
		vc := &metadata.VmQueryCluster{
			QueryCluster:       "vm-query.example.com",
			StorageClusterList: []string{"s1", "s2"},
		}
		spanSetVmQueryClusterIfPresent(span, "vm-data", vc)
		var err error
		span.End(&err)

		ended := rec.Ended()
		require.Len(t, ended, 1)
		got, ok := spanAttrString(ended[0].Attributes(), "vm-data-vm-query-cluster")
		require.True(t, ok)
		want, jerr := json.Marshal(vc)
		require.NoError(t, jerr)
		assert.JSONEq(t, string(want), got)
	})

	t.Run("writes JSON for response- prefix", func(t *testing.T) {
		rec.Reset()
		_, span := uqtrace.NewSpan(context.Background(), "test-span")
		vc := &metadata.VmQueryCluster{
			QueryCluster:       "vm-instant.example.com",
			StorageClusterList: []string{"a"},
		}
		spanSetVmQueryClusterIfPresent(span, "response-", vc)
		var err error
		span.End(&err)

		ended := rec.Ended()
		require.Len(t, ended, 1)
		got, ok := spanAttrString(ended[0].Attributes(), "response--vm-query-cluster")
		require.True(t, ok)
		want, jerr := json.Marshal(vc)
		require.NoError(t, jerr)
		assert.JSONEq(t, string(want), got)
	})

	t.Run("nil does not set attribute", func(t *testing.T) {
		rec.Reset()
		_, span := uqtrace.NewSpan(context.Background(), "test-span")
		spanSetVmQueryClusterIfPresent(span, "vm-data", nil)
		var err error
		span.End(&err)

		ended := rec.Ended()
		require.Len(t, ended, 1)
		_, ok := spanAttrString(ended[0].Attributes(), "vm-data-vm-query-cluster")
		assert.False(t, ok)
	})
}

func TestSpanSetStorageListDiff(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prevTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
	})

	// These test cases mirror the JSON examples from the code review:
	// request:  {"rt_vm_1": {"table_id": "rt_1", "storage_name": "vm_op_1"}, ...}
	// response StorageClusterList: ["vm_op_1", "vm_op_2"]
	cases := map[string]struct {
		rtDetail              map[string]metadata.RtDetail
		responseVMClusterList []string
		wantStatus            string
		wantMissing           string // JSON string or empty
	}{
		"match: request cluster names equal response cluster names": {
			rtDetail: map[string]metadata.RtDetail{
				"rt_vm_1": {TableID: "rt_1", StorageName: "vm_op_1"},
				"rt_vm_2": {TableID: "rt_2", StorageName: "vm_op_2"},
			},
			responseVMClusterList: []string{"vm_op_1", "vm_op_2"},
			wantStatus:            "match",
			wantMissing:           "",
		},
		"mismatch: vm_op_2 missing from response": {
			rtDetail: map[string]metadata.RtDetail{
				"rt_vm_1": {TableID: "rt_1", StorageName: "vm_op_1"},
				"rt_vm_2": {TableID: "rt_2", StorageName: "vm_op_2"},
			},
			responseVMClusterList: []string{"vm_op_1"},
			wantStatus:            "mismatch",
			wantMissing:           `[{"cluster":"vm_op_2","vm_rt_list":["rt_vm_2"],"table_id_list":["rt_2"]}]`,
		},
		"response has extra cluster not in request: match (extra ignored)": {
			rtDetail: map[string]metadata.RtDetail{
				"rt_vm_1": {TableID: "rt_1", StorageName: "vm_op_1"},
			},
			responseVMClusterList: []string{"vm_op_1", "vm_op_2"},
			wantStatus:            "match",
			wantMissing:           "",
		},
		"empty request and response: match": {
			rtDetail:              nil,
			responseVMClusterList: nil,
			wantStatus:            "match",
			wantMissing:           "",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec.Reset()
			_, span := uqtrace.NewSpan(context.Background(), "test-span")
			spanSetStorageListDiff(span, tc.rtDetail, tc.responseVMClusterList)
			var err error
			span.End(&err)

			attrs := rec.Ended()[0].Attributes()

			status, _ := spanAttrString(attrs, "query-storage-status")
			assert.Equal(t, tc.wantStatus, status)

			// Check missing field
			if tc.wantMissing != "" {
				missing, ok := spanAttrString(attrs, "query-storage-missing")
				assert.True(t, ok)
				// Parse and compare as JSON to handle ordering
				var gotMissing, wantMissing any
				assert.NoError(t, json.Unmarshal([]byte(missing), &gotMissing))
				assert.NoError(t, json.Unmarshal([]byte(tc.wantMissing), &wantMissing))
				assert.Equal(t, wantMissing, gotMissing)
			} else {
				_, ok := spanAttrString(attrs, "query-storage-missing")
				assert.False(t, ok)
			}

			// extra field is no longer recorded
			_, hasExtra := spanAttrStringSlice(attrs, "query-storage-extra")
			assert.False(t, hasExtra)
		})
	}
}
