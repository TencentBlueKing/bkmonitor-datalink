// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/formatter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/log"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/uptimecheck"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// TextLogPipelineSuite :
type TextLogPipelineSuite struct {
	ETLPipelineSuite
}

// SetupTest :
func (s *TextLogPipelineSuite) SetupTest() {
	s.ConsulConfig = `{"result_table_list":[{"option":{"es_unique_field_list":["ip","path","gseIndex","_iteration_idx"]},"schema_type":"free","result_table":"2_log.log","shipper_list":[{"cluster_type":"test"}],"field_list":[{"default_value":null,"alias_name":"bk_biz_id","tag":"metric","description":"\u4e1a\u52a1ID\uff08\u4e34\u65f6\uff09\uff0c\u5f85\u7cfb\u7edf\u5185\u7f6e\u5b57\u6bb5\u5b8c\u5584\u914d\u7f6e","type":"float","is_config_by_user":true,"field_name":"_bizid_","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"alias_name":"gseIndex","tag":"metric","description":"gse\u7d22\u5f15","type":"float","is_config_by_user":true,"field_name":"_gseindex_","unit":"","option":{"es_include_in_all":false,"es_type":"long","es_doc_values":true,"es_index":true}},{"default_value":null,"alias_name":"log","tag":"metric","description":"\u65e5\u5fd7\u5185\u5bb9","type":"string","is_config_by_user":true,"field_name":"log","unit":"","option":{"es_include_in_all":true,"es_type":"text","es_doc_values":false,"es_index":true}},{"default_value":null,"alias_name":"path","tag":"dimension","description":"\u65e5\u5fd7\u8def\u5f84","type":"string","is_config_by_user":true,"field_name":"_path_","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":true,"es_index":true}}]}],"source_label":"bk_monitor","type_label":"log","data_id":1200151,"etl_config":"bk_log_text","mq_config":{},"option":{"group_info_alias":"_private_","encoding":"UTF-8"}}`
	s.PipelineName = "bk_log_text"
	s.ETLPipelineSuite.SetupTest()
}

// TestRun :
func (s *TextLogPipelineSuite) TestRun() {
	var wg sync.WaitGroup

	wg.Add(1)
	s.FrontendPulled = `{"_bizid_":0,"_cloudid_":0,"_dstdataid_":1200124,"_errorcode_":0,"_gseindex_":1,"_path_":"/tmp/health_check.log","_private_":[{"bk_app_code":"bk_log_search"}],"_server_":"127.0.0.1","_srcdataid_":1200124,"_time_":"2019-10-08 17:41:49","_type_":0,"_utctime_":"2019-10-08 09:41:49","_value_":["1", "2"],"_worldid_":-1}`
	wg.Add(2)
	pipe := s.BuildPipe(func(payload define.Payload) {
		wg.Done()
	}, func(i map[string]interface{}) {
		wg.Done()
	})

	s.RunPipe(pipe, wg.Wait)
}

// TestTextLogPipelineSuite :
func TestTextLogPipelineSuite(t *testing.T) {
	suite.Run(t, new(TextLogPipelineSuite))
}

// SeparatorLogPipelineSuite
type SeparatorLogPipelineSuite struct {
	ETLPipelineSuite
}

// SetupTest :
func (s *SeparatorLogPipelineSuite) SetupTest() {
	s.ConsulConfig = `{"result_table_list":[{"option":{"es_unique_field_list":["ip","path","gseIndex","_iteration_idx"]},"schema_type":"free","result_table":"2_log.durant_log1000008","shipper_list":[{"cluster_type":"test"}],"field_list":[{"default_value":null,"alias_name":"log","tag":"metric","description":"\u65e5\u5fd7\u5185\u5bb9","type":"string","is_config_by_user":true,"field_name":"log","unit":"","option":{"es_include_in_all":true,"es_type":"text","es_doc_values":false,"es_index":true}},{"default_value":"","field_name":"","tag":"","description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","type":"timestamp","is_config_by_user":true,"alias_name":"time","unit":"","option":{"es_include_in_all":false,"es_format":"epoch_millis","es_type":"date","es_index":true}},{"default_value":null,"field_name":"_bizid_","tag":"metric","description":"\u4e1a\u52a1ID","type":"int","is_config_by_user":true,"alias_name":"bk_biz_id","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"_cloudid_","tag":"metric","description":"\u4e91\u533a\u57dfID","type":"int","is_config_by_user":true,"alias_name":"cloudId","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_dstdataid_","tag":"metric","description":"\u76ee\u7684DataId","type":"int","is_config_by_user":true,"alias_name":"dstDataId","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"_errorcode_","tag":"metric","description":"\u9519\u8bef\u7801","type":"int","is_config_by_user":true,"alias_name":"errorCode","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_gseindex_","tag":"metric","description":"gse\u7d22\u5f15","type":"float","is_config_by_user":true,"alias_name":"gseIndex","unit":"","option":{"es_include_in_all":false,"es_type":"long","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_path_","tag":"dimension","description":"\u65e5\u5fd7\u8def\u5f84","type":"string","is_config_by_user":true,"alias_name":"path","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_server_","tag":"dimension","description":"IP\u5730\u5740","type":"string","is_config_by_user":true,"alias_name":"serverIp","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_srcdataid_","tag":"metric","description":"\u6e90DataId","type":"int","is_config_by_user":true,"alias_name":"srcDataId","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_time_","tag":"metric","description":"\u672c\u5730\u65f6\u95f4","type":"string","is_config_by_user":true,"alias_name":"logTime","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"_utctime_","tag":"metric","description":"\u65f6\u95f4\u6233","type":"timestamp","is_config_by_user":true,"alias_name":"dtEventTimeStamp","unit":"","option":{"time_format":"datetime","es_format":"epoch_millis","es_type":"date","es_doc_values":false,"es_include_in_all":true,"time_zone":"0","es_index":true}},{"default_value":null,"field_name":"_worldid_","tag":"metric","description":"worldID","type":"string","is_config_by_user":true,"alias_name":"worldId","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"int","tag":"metric","description":"","type":"int","is_config_by_user":true,"alias_name":"int_value","unit":""},{"default_value":null,"field_name":"string","tag":"metric","description":"","type":"string","is_config_by_user":true,"alias_name":"string_value","unit":""},{"default_value":null,"field_name":"bool","tag":"metric","description":"","type":"bool","is_config_by_user":true,"alias_name":"bool_value","unit":""}]}],"source_label":"bk_monitor","type_label":"log","data_id":1200145,"etl_config":"bk_log_separator","mq_config":{},"option":{"group_info_alias":"_private_","encoding":"UTF-8","separator":",","separator_field_list":["int","string","bool"]}}`
	s.PipelineName = "bk_log_separator"
	s.ETLPipelineSuite.SetupTest()
}

// TestRun :
func (s *SeparatorLogPipelineSuite) TestRun() {
	var wg sync.WaitGroup

	wg.Add(1)
	s.FrontendPulled = `{"_bizid_":0,"_cloudid_":0,"_dstdataid_":1200124,"_errorcode_":0,"_gseindex_":1,"_path_":"/tmp/health_check.log","_private_":[{"bk_app_code":"bk_log_search"}],"_server_":"127.0.0.1","_srcdataid_":1200124,"_time_":"2019-10-08 17:41:49","_type_":0,"_utctime_":"2019-10-08 09:41:49","_value_":["3,2,1"],"_worldid_":-1}`
	wg.Add(1)
	pipe := s.BuildPipe(func(payload define.Payload) {
		wg.Done()
	}, func(i map[string]interface{}) {
		wg.Done()
	})

	s.RunPipe(pipe, wg.Wait)
}

// TestSeparatorLogPipelineSuite
func TestSeparatorLogPipelineSuite(t *testing.T) {
	suite.Run(t, new(SeparatorLogPipelineSuite))
}

// SeparatorLogPipelineSuite
type RegexpLogPipelineSuite struct {
	ETLPipelineSuite
}

// SetupTest :
func (s *RegexpLogPipelineSuite) SetupTest() {
	s.ConsulConfig = `{"result_table_list":[{"option":{"es_unique_field_list":["ip","path","gseIndex","_iteration_idx"]},"schema_type":"free","result_table":"2_log.durant_log1000008","shipper_list":[{"cluster_type":"test"}],"field_list":[{"default_value":null,"alias_name":"log","tag":"metric","description":"\u65e5\u5fd7\u5185\u5bb9","type":"string","is_config_by_user":true,"field_name":"log","unit":"","option":{"es_include_in_all":true,"es_type":"text","es_doc_values":false,"es_index":true}},{"default_value":"","field_name":"","tag":"","description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","type":"timestamp","is_config_by_user":true,"alias_name":"time","unit":"","option":{"es_include_in_all":false,"es_format":"epoch_millis","es_type":"date","es_index":true}},{"default_value":null,"field_name":"_bizid_","tag":"metric","description":"\u4e1a\u52a1ID","type":"int","is_config_by_user":true,"alias_name":"bk_biz_id","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"_cloudid_","tag":"metric","description":"\u4e91\u533a\u57dfID","type":"int","is_config_by_user":true,"alias_name":"cloudId","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_dstdataid_","tag":"metric","description":"\u76ee\u7684DataId","type":"int","is_config_by_user":true,"alias_name":"dstDataId","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"_errorcode_","tag":"metric","description":"\u9519\u8bef\u7801","type":"int","is_config_by_user":true,"alias_name":"errorCode","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_gseindex_","tag":"metric","description":"gse\u7d22\u5f15","type":"float","is_config_by_user":true,"alias_name":"gseIndex","unit":"","option":{"es_include_in_all":false,"es_type":"long","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_path_","tag":"dimension","description":"\u65e5\u5fd7\u8def\u5f84","type":"string","is_config_by_user":true,"alias_name":"path","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_server_","tag":"dimension","description":"IP\u5730\u5740","type":"string","is_config_by_user":true,"alias_name":"serverIp","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_srcdataid_","tag":"metric","description":"\u6e90DataId","type":"int","is_config_by_user":true,"alias_name":"srcDataId","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true,"es_index":true}},{"default_value":null,"field_name":"_time_","tag":"metric","description":"\u672c\u5730\u65f6\u95f4","type":"string","is_config_by_user":true,"alias_name":"logTime","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"_utctime_","tag":"metric","description":"\u65f6\u95f4\u6233","type":"timestamp","is_config_by_user":true,"alias_name":"dtEventTimeStamp","unit":"","option":{"time_format":"datetime","es_format":"epoch_millis","es_type":"date","es_doc_values":false,"es_include_in_all":true,"time_zone":"0","es_index":true}},{"default_value":null,"field_name":"_worldid_","tag":"metric","description":"worldID","type":"string","is_config_by_user":true,"alias_name":"worldId","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":false,"es_index":true}},{"default_value":null,"field_name":"value","tag":"metric","description":"","type":"float","is_config_by_user":true,"alias_name":"","unit":""},{"default_value":null,"field_name":"key","tag":"metric","description":"","type":"string","is_config_by_user":true,"alias_name":"","unit":""}]}],"source_label":"bk_monitor","type_label":"log","data_id":1200145,"etl_config":"bk_log_Regexp","mq_config":{},"option":{"group_info_alias":"_private_","encoding":"UTF-8","separator_regexp":"(?P<key>\\w+):\\s+(?P<value>\\w+)"}}`
	s.PipelineName = "bk_log_regexp"
	s.ETLPipelineSuite.SetupTest()
}

// TestRun :
func (s *RegexpLogPipelineSuite) TestRun() {
	var wg sync.WaitGroup

	wg.Add(1)
	s.FrontendPulled = `{"_bizid_":0,"_cloudid_":0,"_dstdataid_":1200124,"_errorcode_":0,"_gseindex_":1,"_path_":"/tmp/health_check.log","_private_":[{"bk_app_code":"bk_log_search"}],"_server_":"127.0.0.1","_srcdataid_":1200124,"_time_":"2019-10-08 17:41:49","_type_":0,"_utctime_":"2019-10-08 09:41:49","_value_":["option: 1"],"_worldid_":-1}`
	wg.Add(1)
	pipe := s.BuildPipe(func(payload define.Payload) {
		wg.Done()
	}, func(i map[string]interface{}) {
		wg.Done()
	})

	s.RunPipe(pipe, wg.Wait)
}

// TestRegexpLogPipelineSuite
func TestRegexpLogPipelineSuite(t *testing.T) {
	suite.Run(t, new(RegexpLogPipelineSuite))
}
