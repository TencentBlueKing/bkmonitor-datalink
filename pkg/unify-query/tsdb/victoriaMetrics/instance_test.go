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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
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
				log.Fatalf(ctx, err.Error())
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
				log.Fatalf(ctx, err.Error())
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
				log.Fatalf(ctx, err.Error())
				return
			}

			actual, _ := json.Marshal(res)
			assert.Equal(t, c.expected, string(actual))
		})
	}
}

func TestInstance_QueryLabelValues(t *testing.T) {
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
	ctx := metadata.InitHashID(context.Background())

	mock.Vm.Set(map[string]any{
		`query_range:17301804581730184058360count({__name__="container_cpu_usage_seconds_total_value", result_table_id="2_bcs_prom_computation_result_table_00000", container="unify-query"}) by (pod)`: `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":null,"cluster":"monitor-op","totalRecords":14,"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"status":"success","isPartial":false,"data":{"resultType":"matrix","result":[{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-5vsjr"},"values":[[1730181538,"1"],[1730181898,"1"],[1730182258,"1"],[1730182618,"1"],[1730182978,"1"],[1730183338,"1"],[1730183698,"1"],[1730184058,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-m9w6t"},"values":[[1730181538,"1"],[1730181898,"1"],[1730182258,"1"],[1730182618,"1"],[1730182978,"1"],[1730183338,"1"],[1730183698,"1"],[1730184058,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-nmx72"},"values":[[1730181538,"1"],[1730181898,"1"],[1730182258,"1"],[1730182618,"1"],[1730182978,"1"],[1730183338,"1"],[1730183698,"1"],[1730184058,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-6459767d5f-qq8nq"},"values":[[1730181538,"1"],[1730181898,"1"],[1730182258,"1"],[1730182618,"1"],[1730182978,"1"],[1730183338,"1"],[1730183698,"1"],[1730184058,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-778b5bdf95-7hpc9"},"values":[[1730180458,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-778b5bdf95-dpkwm"},"values":[[1730180458,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-778b5bdf95-x2wfd"},"values":[[1730180458,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-778b5bdf95-xpq5z"},"values":[[1730180458,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-85c54f79d8-6lfvd"},"values":[[1730180818,"1"],[1730181178,"1"],[1730181538,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-85c54f79d8-dz9t7"},"values":[[1730180818,"1"],[1730181178,"1"],[1730181538,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-85c54f79d8-nlzmc"},"values":[[1730180818,"1"],[1730181178,"1"],[1730181538,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-85c54f79d8-qzqfz"},"values":[[1730180818,"1"],[1730181178,"1"],[1730181538,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-test-66f7ccb78d-jf4m2"},"values":[[1730180458,"1"],[1730180818,"1"],[1730181178,"1"],[1730181538,"1"]]},{"metric":{"pod":"bk-datalink-unify-query-test-8445575f5d-mrppp"},"values":[[1730180458,"1"]]}]},"stats":{"seriesFetched":"14"}}],"select_fields_order":[],"sql":"count ({__name__=\"container_cpu_usage_seconds_total_value\", result_table_id=\"2_bcs_prom_computation_result_table_00000\", container=\"unify-query\"}) by(pod)","total_record_size":13920,"timetaken":0.0,"bksql_call_elapsed_time":0,"device":"vm","result_table_ids":["2_bcs_prom_computation_result_table_00000"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,
	})

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			start := time.Unix(c.start, 0)
			end := time.Unix(c.end, 0)

			query := &metadata.Query{
				VmRt:        vmRt,
				VmCondition: vmCondition,
			}

			res, err := instance.QueryLabelValues(ctx, query, c.name, start, end)
			if err != nil {
				log.Fatalf(ctx, err.Error())
				return
			}

			sort.Strings(res)
			actual, _ := json.Marshal(res)
			assert.Equal(t, c.expected, string(actual))
		})
	}
}
