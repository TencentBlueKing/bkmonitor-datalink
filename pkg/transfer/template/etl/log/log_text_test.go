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

// TextLogSuite
type TextLogSuite struct {
	testsuite.ETLSuite
}

// TestUsage :
func (s *TextLogSuite) TestUsage() {
	s.CTX = testsuite.PipelineConfigStringInfoContext(
		s.CTX, s.PipelineConfig,
		`{"data_id":1200005,"etl_config":"bk_log_text","option":{"encoding":"GBK","group_info_alias":"_private_"},"result_table_list":[{"field_list":[{"alias_name":"bk_biz_id","default_value":null,"description":"\u4e1a\u52a1ID\uff08\u4e34\u65f6\uff09\uff0c\u5f85\u7cfb\u7edf\u5185\u7f6e\u5b57\u6bb5\u5b8c\u5584\u914d\u7f6e","field_name":"_bizid_","is_config_by_user":true,"option":{"es_doc_values":false,"es_include_in_all":true,"es_index":true,"es_type":"keyword"},"tag":"metric","type":"float","unit":""},{"alias_name":"cloud_id","default_value":null,"description":"\u4e91\u533a\u57dfID","field_name":"_cloud_id_","is_config_by_user":true,"option":{"es_doc_values":true,"es_include_in_all":false,"es_index":true,"es_type":"keyword"},"tag":"metric","type":"float","unit":""},{"alias_name":"source_time","default_value":null,"description":"\u65f6\u95f4\u6233","field_name":"_utctime_","is_config_by_user":true,"option":{"es_doc_values":false,"es_format":"epoch_millis","es_include_in_all":true,"es_index":true,"es_type":"date","time_format":"datetime","time_zone":"0"},"tag":"metric","type":"timestamp","unit":""},{"alias_name":"msg_index","default_value":null,"description":"gse\u7d22\u5f15","field_name":"_msg_index_","is_config_by_user":true,"option":{"es_doc_values":true,"es_include_in_all":false,"es_index":true,"es_type":"long"},"tag":"metric","type":"float","unit":""},{"alias_name":"log","default_value":null,"description":"\u65e5\u5fd7\u5185\u5bb9","field_name":"log","is_config_by_user":true,"option":{"es_doc_values":false,"es_include_in_all":true,"es_index":true,"es_type":"text"},"tag":"metric","type":"string","unit":""},{"alias_name":"log_time","default_value":null,"description":"\u672c\u5730\u65f6\u95f4","field_name":"_time_","is_config_by_user":true,"option":{"es_doc_values":false,"es_include_in_all":true,"es_index":true,"es_type":"keyword"},"tag":"metric","type":"string","unit":""},{"alias_name":"path","default_value":null,"description":"\u65e5\u5fd7\u8def\u5f84","field_name":"_path_","is_config_by_user":true,"option":{"es_doc_values":true,"es_include_in_all":true,"es_index":true,"es_type":"keyword"},"tag":"dimension","type":"string","unit":""},{"alias_name":"","default_value":"","description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","field_name":"time","is_config_by_user":true,"option":{"es_format":"epoch_millis","es_include_in_all":false,"es_index":true,"es_type":"date"},"tag":"timestamp","type":"timestamp","unit":""},{"alias_name":"world_id","default_value":null,"description":"world_id","field_name":"_world_id_","is_config_by_user":true,"option":{"es_doc_values":false,"es_include_in_all":true,"es_index":true,"es_type":"keyword"},"tag":"metric","type":"float","unit":""},{"alias_name":"_iteration_idx","default_value":null,"description":"\u8fed\u4ee3ID","field_name":"_iteration_idx","is_config_by_user":true,"option":{"es_doc_values":true,"es_include_in_all":false,"es_index":true,"es_type":"keyword"},"tag":"metric","type":"float","unit":""}],"option":{"es_unique_field_list":["ip","path","msg_index","_iteration_idx"]},"result_table":"2_log.log57","schema_type":"free"}],"source_label":"bk_monitor","type_label":"log"}`,
	)

	processor, err := log.NewTextLogProcessor(s.CTX, "test")
	s.NoError(err)

	s.Run(`{"_bizid_":0,"_cloud_id_":0,"_dstdataid_":1200005,"_errorcode_":0,"_msg_index_":93,"_path_":"/tmp/test.log","_private_":[{"bk_app_code":"bk_log_search"}],"_server_":"127.0.0.1","_srcdataid_":1200005,"_time_":"2019-10-22 22:48:01","_type_":0,"_utctime_":"2019-10-22 14:48:01","_value_":["Tue Oct 22 22:48:00 CST 2019"],"_world_id_":-1}`,
		processor,
		func(result map[string]interface{}) {
			ts, ok := result["time"].(float64)
			s.True(ok)
			s.EqualRecord(result, map[string]interface{}{
				"time": ts,
				"dimensions": map[string]interface{}{
					"path": "/tmp/test.log",
				},
				"metrics": map[string]interface{}{
					"cloud_id":       0.0,
					"source_time":    1571755681.0,
					"msg_index":      93.0,
					"log":            "Tue Oct 22 22:48:00 CST 2019",
					"log_time":       "2019-10-22 22:48:01",
					"world_id":       -1.0,
					"_iteration_idx": 0.0,
					"bk_biz_id":      0.0,
				},
				"group_info": []map[string]string{
					{"bk_app_code": "bk_log_search"},
				},
			})
		},
	)
}

// TestTextLogSuite
func TestTextLogSuite(t *testing.T) {
	suite.Run(t, new(TextLogSuite))
}
