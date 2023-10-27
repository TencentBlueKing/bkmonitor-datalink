// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package log_test

import (
	"context"
	"testing"

	"github.com/cstockton/go-conv"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// JSONLogTest
type JSONLogTest struct {
	testsuite.ETLSuite
}

// TestUsage :
func (s *JSONLogTest) TestUsage() {
	s.CTX = testsuite.PipelineConfigStringInfoContext(
		s.CTX, s.PipelineConfig,
		`{"result_table_list":[{"option":{"es_unique_field_list":["ip","path","gseIndex","_iteration_idx"]},"schema_type":"free","result_table":"2_log.durant_log1000008","field_list":[{"default_value":null,"alias_name":"log","tag":"metric","description":"\u65e5\u5fd7\u5185\u5bb9","type":"string","is_config_by_user":true,"field_name":"log","unit":"","option":{"es_include_in_all":true,"es_type":"text","es_doc_values":false,"es_index":true}},{"default_value":"","field_name":"","tag":"","description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","type":"timestamp","is_config_by_user":true,"alias_name":"time","unit":"","option":{"es_include_in_all":false,"es_format":"epoch_millis","es_type":"date","es_index":true}},{"default_value":null,"field_name":"_server_","tag":"dimension","description":"IP\u5730\u5740","type":"string","is_config_by_user":true,"alias_name":"serverIp","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_time_","tag":"metric","description":"\u672c\u5730\u65f6\u95f4","type":"string","is_config_by_user":true,"alias_name":"logTime","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"k1","tag":"metric","description":"","type":"string","is_config_by_user":true,"alias_name":"","unit":""},{"default_value":null,"field_name":"k2","tag":"metric","description":"","type":"string","is_config_by_user":true,"alias_name":"","unit":""}]}],"source_label":"bk_monitor","type_label":"log","data_id":1200145,"etl_config":"bk_log_regexp","option":{"group_info_alias":"_private_","encoding":"UTF-8","separator_regexp":"(?P<key>\\w+):\\s+(?P<value>\\w+)"}}`,
	)
	processor, err := log.NewJSONLogProcessor(s.CTX, "test")
	s.NoError(err)
	cases := []map[string]interface{}{
		{
			"dimensions": map[string]interface{}{
				"serverIp": "127.0.0.1",
			},
			"metrics": map[string]interface{}{
				"log":            "{\"k1\":\"v1\"}",
				"_iteration_idx": 0.0,
				"logTime":        "2019-10-08 17:41:49",
				"k1":             "v1",
				"k2":             nil,
			},

			"group_info": []map[string]string{
				{"bk_app_code": "bk_log_search"},
			},
		},
		{
			"dimensions": map[string]interface{}{
				"serverIp": "127.0.0.1",
			},
			"metrics": map[string]interface{}{
				"log":            "{\"k2\":\"v2\"}",
				"logTime":        "2019-10-08 17:41:49",
				"_iteration_idx": 1.0,
				"k2":             "v2",
				"k1":             nil,
			},

			"group_info": []map[string]string{
				{"bk_app_code": "bk_log_search"},
			},
		},
	}

	s.RunN(2, `{"_bizid_":0,"_cloudid_":0,"_dstdataid_":1200124,"_errorcode_":0,"_gseindex_":1,"_path_":"/tmp/health_check.log","_private_":[{"bk_app_code":"bk_log_search"}],"_server_":"127.0.0.1","_srcdataid_":1200124,"_time_":"2019-10-08 17:41:49","_type_":0,"_utctime_":"2019-10-08 09:41:49","_value_":["{\"k1\":\"v1\"}","{\"k2\":\"v2\"}"],"_worldid_":-1}`,
		processor, func(result map[string]interface{}) {
			metrics := result["metrics"].(map[string]interface{})
			idx := conv.Int(metrics["_iteration_idx"])
			excepts := cases[idx]
			excepts["time"] = result["time"].(float64)
			s.EqualRecord(result, excepts)
		},
	)
}

// TestServletTest :
func TestJsonLogTest(t *testing.T) {
	suite.Run(t, new(JSONLogTest))
}

// BenchmarkNewJsonLogProcessor :
func BenchmarkNewJsonLogProcessor(b *testing.B) {
	testsuite.ETLBenchmarkTest(b, func(ctx context.Context, name string) define.DataProcessor {
		processor, err := log.NewJSONLogProcessor(ctx, name)
		utils.CheckError(err)
		return processor
	}, []byte(`{"available":1.000000,"bkmonitorbeat":{"address":["127.0.0.1"],"hostname":"VM_1_10_centos","name":"VM_1_10_centos","version":"1.4.9"},"bizid":0,"bk_biz_id":2,"bk_cloud_id":0,"cloudid":0,"dataid":1009,"error_code":0,"gseindex":440779,"ip":"127.0.0.1","node_id":6,"status":0,"target_host":"127.0.0.1","target_port":8001,"task_duration":0,"task_id":16,"task_type":"tcp","timestamp":1554652696,"type":"uptimecheckbeat"}`))
}
