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
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// RegexpLogTest
type RegexpLogTest struct {
	testsuite.ETLSuite
}

// TestUsage :
func (s *RegexpLogTest) TestUsage() {
	s.CTX = testsuite.PipelineConfigStringInfoContext(
		s.CTX, s.PipelineConfig,
		`{"result_table_list":[{"option":{"es_unique_field_list":["ip","path","gseIndex","_iteration_idx"]},"schema_type":"free","result_table":"2_log.durant_log1000008","field_list":[{"default_value":null,"alias_name":"log","tag":"metric","description":"\u65e5\u5fd7\u5185\u5bb9","type":"string","is_config_by_user":true,"field_name":"log","unit":"","option":{"es_include_in_all":true,"es_type":"text","es_doc_values":false,"es_index":true}},{"default_value":"","field_name":"","tag":"","description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","type":"timestamp","is_config_by_user":true,"alias_name":"time","unit":"","option":{"es_include_in_all":false,"es_format":"epoch_millis","es_type":"date","es_index":true}},{"default_value":null,"field_name":"_bizid_","tag":"metric","description":"\u4e1a\u52a1ID","type":"int","is_config_by_user":true,"alias_name":"bk_biz_id","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"_cloudid_","tag":"metric","description":"\u4e91\u533a\u57dfID","type":"int","is_config_by_user":true,"alias_name":"cloudId","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_dstdataid_","tag":"metric","description":"\u76ee\u7684DataId","type":"int","is_config_by_user":true,"alias_name":"dstDataId","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"_errorcode_","tag":"metric","description":"\u9519\u8bef\u7801","type":"int","is_config_by_user":true,"alias_name":"errorCode","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_gseindex_","tag":"metric","description":"gse\u7d22\u5f15","type":"float","is_config_by_user":true,"alias_name":"gseIndex","unit":"","option":{"es_include_in_all":false,"es_type":"long","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_path_","tag":"dimension","description":"\u65e5\u5fd7\u8def\u5f84","type":"string","is_config_by_user":true,"alias_name":"path","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_server_","tag":"dimension","description":"IP\u5730\u5740","type":"string","is_config_by_user":true,"alias_name":"serverIp","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_srcdataid_","tag":"metric","description":"\u6e90DataId","type":"int","is_config_by_user":true,"alias_name":"srcDataId","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_time_","tag":"metric","description":"\u672c\u5730\u65f6\u95f4","type":"string","is_config_by_user":true,"alias_name":"logTime","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"_utctime_","tag":"metric","description":"\u65f6\u95f4\u6233","type":"timestamp","is_config_by_user":true,"alias_name":"dtEventTimeStamp","unit":"","option":{"time_format":"datetime","es_format":"epoch_millis","es_type":"date","es_doc_values":false,"es_include_in_all":true,"time_zone":"0","es_index":true}},{"default_value":null,"field_name":"_worldid_","tag":"metric","description":"worldID","type":"string","is_config_by_user":true,"alias_name":"worldId","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"value","tag":"metric","description":"","type":"float","is_config_by_user":true,"alias_name":"","unit":""},{"default_value":null,"field_name":"key","tag":"metric","description":"","type":"string","is_config_by_user":true,"alias_name":"","unit":""}]}],"source_label":"bk_monitor","type_label":"log","data_id":1200145,"etl_config":"bk_log_regexp","option":{"group_info_alias":"_private_","encoding":"UTF-8","separator_regexp":"(?P<key>\\w+):\\s+(?P<value>\\w+)"}}`,
	)

	processor, err := log.NewRegexpLogProcessor(s.CTX, "test")
	s.NoError(err)

	s.Run(`{"_bizid_":0,"_cloudid_":0,"_dstdataid_":1200124,"_errorcode_":0,"_gseindex_":1,"_path_":"/tmp/health_check.log","_private_":[{"bk_app_code":"bk_log_search"}],"_server_":"127.0.0.1","_srcdataid_":1200124,"_time_":"2019-10-08 17:41:49","_type_":0,"_utctime_":"2019-10-08 09:41:49","_value_":["option: 1"],"_worldid_":-1}`,
		processor,
		func(result map[string]interface{}) {
			ts := result["time"].(float64)
			s.EqualRecord(result, map[string]interface{}{
				"dimensions": map[string]interface{}{
					"path":     "/tmp/health_check.log",
					"serverIp": "127.0.0.1",
				},
				"metrics": map[string]interface{}{
					"log":              "option: 1",
					"bk_biz_id":        0.0,
					"cloudId":          0.0,
					"dstDataId":        1200124.0,
					"errorCode":        0.0,
					"gseIndex":         1.0,
					"srcDataId":        1200124.0,
					"logTime":          "2019-10-08 17:41:49",
					"dtEventTimeStamp": 1570527709.0,
					"worldId":          "-1",
					"_iteration_idx":   0.0,
					"key":              "option",
					"value":            1.0,
				},
				"time": ts,
				"group_info": []map[string]string{
					{"bk_app_code": "bk_log_search"},
				},
			})
		},
	)
}

// TestServletTest :
func TestRegexpLogTest(t *testing.T) {
	suite.Run(t, new(RegexpLogTest))
}
