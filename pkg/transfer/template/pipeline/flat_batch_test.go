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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/formatter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/uptimecheck"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

type FlatBatchPipelineSuite struct {
	ETLPipelineSuite
}

func (s *FlatBatchPipelineSuite) SetupTest() {
	s.ConsulConfig = `{"etl_config":"bk_flat_batch","result_table_list":[{"schema_type":"fixed","shipper_list":[{"cluster_config":{"domain_name":"influxdb.service.consul","port":5260},"storage_config":{"real_table_name":"heartbeat","database":"uptimecheck"},"cluster_type":"influxdb"}],"result_table":"flat.batch","field_list":[{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_biz_id"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"index"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"data"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_cloud_id"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"metric","field_name":"testM"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"testD"},{"default_value":null,"type":"timestamp","is_config_by_user":true,"tag":"","field_name":"time"}]}],"mq_config":{"cluster_config":{"domain_name":"kafka.service.consul","port":9092},"storage_config":{"topic":"0bkmonitor_10080","partition":1},"cluster_type":"kafka"},"data_id":1008}`
	s.PipelineName = "bk_flat_batch_cluster"
	s.ETLPipelineSuite.SetupTest()
}

func (s *FlatBatchPipelineSuite) TestRun() {
	s.StoreHost(&models.CCHostInfo{
		IP:      "127.0.0.1",
		CloudID: 0,
	}).AnyTimes()
	var wg sync.WaitGroup

	wg.Add(1)
	s.FrontendPulled = `{"bizid":0,"bk_biz_id":2,"bk_cloud_id":0,"cloudid":0,"ip":"127.0.0.1","testM":10086,"testD":"testD","timestamp":1554094763,"items":[{"index":1,"data":"hello"},{"index":2,"data":"world"}],"group_info":[{"tag":"aaa","tag1":"aaa1"},{"tag":"bbb","tag1":"bbb1"}]}`
	wg.Add(2)
	pipe := s.BuildPipe(func(payload define.Payload) {
		wg.Done()
	}, func(i map[string]interface{}) {
		wg.Done()
	})

	s.RunPipe(pipe, wg.Wait)
}

func TestFlatBatchPipelineSuite(t *testing.T) {
	suite.Run(t, new(FlatBatchPipelineSuite))
}

type FlatBatchPipelineRawSuite struct {
	ETLPipelineSuite
}

func (s *FlatBatchPipelineRawSuite) SetupTest() {
	s.ConsulConfig = `{"etl_config":"bk_flat_batch","result_table_list":[{"schema_type":"fixed","shipper_list":[{"cluster_config":{"domain_name":"influxdb.service.consul","port":5260},"storage_config":{"real_table_name":"heartbeat","database":"uptimecheck"},"cluster_type":"influxdb"}],"result_table":"flat.batch","field_list":[{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_biz_id"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"index"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"data"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_cloud_id"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"metric","field_name":"testM"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"testD"},{"default_value":null,"type":"timestamp","is_config_by_user":true,"tag":"","field_name":"time"}]}],"mq_config":{"cluster_config":{"domain_name":"kafka.service.consul","port":9092},"storage_config":{"topic":"0bkmonitor_10080","partition":1},"cluster_type":"kafka"},"data_id":1008}`
	s.PipelineName = "bk_flat_batch"
	s.ETLPipelineSuite.SetupTest()
}

func (s *FlatBatchPipelineRawSuite) TestRun() {
	s.StoreHost(&models.CCHostInfo{
		IP:      "127.0.0.1",
		CloudID: 0,
	}).AnyTimes()
	var wg sync.WaitGroup

	wg.Add(1)
	s.FrontendPulled = `{"bizid":0,"bk_biz_id":2,"bk_cloud_id":0,"cloudid":0,"ip":"127.0.0.1","testM":10086,"testD":"testD","timestamp":1554094763, "index":1,"data":"hello","group_info":[{"tag":"aaa","tag1":"aaa1"},{"tag":"bbb","tag1":"bbb1"}]}`
	wg.Add(1)
	pipe := s.BuildPipe(func(payload define.Payload) {
		wg.Done()
	}, func(i map[string]interface{}) {
		wg.Done()
	})

	s.RunPipe(pipe, wg.Wait)
}

func TestFlatBatchPipelineRawSuite(t *testing.T) {
	suite.Run(t, new(FlatBatchPipelineRawSuite))
}

// LogPipelineSuite
type LogPipelineSuite struct {
	ETLPipelineSuite
}

// SetupTest :
func (s *LogPipelineSuite) SetupTest() {
	//s.ConsulConfig = `{"result_table_list":[{"option":{"es_unique_field_list":["serverIp","path","gseIndex","iterationIndex"]},"schema_type":"free","shipper_list":[{"cluster_config":{"is_ssl_verify":false,"cluster_name":"es_cluster1","version":"5.4","cluster_id":3,"registered_system":"_default","custom_option":"","schema":null,"domain_name":"es.service.consul","port":10004},"storage_config":{"slice_size":500,"date_format":"%Y%m%d","index_settings":{"number_of_replicas":1,"number_of_shards":1},"slice_gap":1440,"retention":1,"base_index":"2_bklog_yakov_test","mapping_settings":{"_all":{"enabled":true},"dynamic_templates":[{"strings_as_keywords":{"match_mapping_type":"string","mapping":{"norms":"false","type":"keyword"}}}]},"index_datetime_format":"20060102"},"auth_info":{"username":"","password":""},"cluster_type":"elasticsearch"}],"result_table":"2_bklog.yakov_test","field_list":[{"default_value":null,"alias_name":"cloudId","tag":"dimension","description":"\u4e91\u533a\u57dfID","type":"float","is_config_by_user":true,"field_name":"cloudid","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true}},{"default_value":null,"alias_name":"dtEventTimeStamp","tag":"dimension","description":"\u6570\u636e\u65f6\u95f4","type":"int","is_config_by_user":true,"field_name":"time","unit":"","option":{"time_format":"epoch_second","es_format":"epoch_millis","es_type":"date","es_doc_values":true,"es_include_in_all":true,"time_zone":"0"}},{"default_value":null,"alias_name":"gseIndex","tag":"dimension","description":"gse_index","type":"float","is_config_by_user":true,"field_name":"gseindex","unit":"","option":{"es_include_in_all":false,"es_type":"long","es_doc_values":true}},{"default_value":null,"alias_name":"iterationIndex","tag":"dimension","description":"\u81ea\u589eID","type":"float","is_config_by_user":true,"field_name":"iterationindex","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true}},{"default_value":null,"alias_name":"log","tag":"metric","description":"\u65e5\u5fd7\u5185\u5bb9","type":"string","is_config_by_user":true,"field_name":"data","unit":"","option":{"es_include_in_all":true,"es_type":"text","es_doc_values":false}},{"default_value":null,"alias_name":"path","tag":"dimension","description":"\u65e5\u5fd7\u8def\u5f84","type":"string","is_config_by_user":true,"field_name":"filename","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":true}},{"default_value":null,"alias_name":"serverIp","tag":"dimension","description":"ip","type":"string","is_config_by_user":true,"field_name":"ip","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true}},{"default_value":"","alias_name":"","tag":"timestamp","description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","type":"timestamp","is_config_by_user":true,"field_name":"time","unit":"","option":{"es_include_in_all":false,"es_format":"epoch_millis","es_type":"date","es_index":true}}]}],"source_label":"bk_monitor","type_label":"log","data_id":1200177,"mq_config":{"cluster_config":{"is_ssl_verify":false,"cluster_name":"kafka_cluster1","version":null,"cluster_id":1,"registered_system":"_default","custom_option":"","schema":null,"domain_name":"kafka.service.consul","port":9092},"storage_config":{"topic":"0bkmonitor_12001770","partition":1},"auth_info":{"username":"","password":""},"cluster_type":"kafka"},"etl_config":"flat_batch","option":{"encoding":"UTF-8"}}`
	s.ConsulConfig = `{"result_table_list":[{"option":{"es_unique_field_list":["serverIp","path","gseIndex","iterationIndex"]},"schema_type":"free","shipper_list":[{"cluster_config":{"is_ssl_verify":false,"cluster_name":"es_cluster1","version":"5.4","cluster_id":3,"registered_system":"_default","custom_option":"","schema":null,"domain_name":"es.service.consul","port":10004},"storage_config":{"slice_size":500,"date_format":"%Y%m%d","index_settings":{"number_of_replicas":1,"number_of_shards":1},"slice_gap":1440,"retention":1,"base_index":"2_bklog_yakov_test","mapping_settings":{"_all":{"enabled":true},"dynamic_templates":[{"strings_as_keywords":{"match_mapping_type":"string","mapping":{"norms":"false","type":"keyword"}}}]},"index_datetime_format":"20060102"},"auth_info":{"username":"","password":""},"cluster_type":"elasticsearch"}],"result_table":"2_bklog.yakov_test","field_list":[{"default_value":null,"alias_name":"cloudId","tag":"dimension","description":"\u4e91\u533a\u57dfID","type":"float","is_config_by_user":true,"field_name":"cloudid","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true}},{"default_value":null,"alias_name":"dtEventTimeStamp","tag":"dimension","description":"\u6570\u636e\u65f6\u95f4","type":"int","is_config_by_user":true,"field_name":"time","unit":"","option":{"time_format":"epoch_second","es_format":"epoch_millis","es_type":"date","es_doc_values":true,"es_include_in_all":true,"time_zone":"0"}},{"default_value":null,"alias_name":"gseIndex","tag":"dimension","description":"gse_index","type":"float","is_config_by_user":true,"field_name":"gseindex","unit":"","option":{"es_include_in_all":false,"es_type":"long","es_doc_values":true}},{"default_value":null,"alias_name":"iterationIndex","tag":"dimension","description":"\u81ea\u589eID","type":"float","is_config_by_user":true,"field_name":"iterationindex","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true}},{"default_value":null,"alias_name":"log","tag":"metric","description":"\u65e5\u5fd7\u5185\u5bb9","type":"string","is_config_by_user":true,"field_name":"data","unit":"","option":{"es_include_in_all":true,"es_type":"text","es_doc_values":false}},{"default_value":null,"alias_name":"path","tag":"dimension","description":"\u65e5\u5fd7\u8def\u5f84","type":"string","is_config_by_user":true,"field_name":"filename","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":true}},{"default_value":null,"alias_name":"serverIp","tag":"dimension","description":"ip","type":"string","is_config_by_user":true,"field_name":"ip","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true}},{"default_value":"","alias_name":"","tag":"timestamp","description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","type":"timestamp","is_config_by_user":true,"field_name":"time","unit":"","option":{"es_include_in_all":false,"es_format":"epoch_millis","es_type":"date","es_index":true}}]},{"option":{"es_unique_field_list":["serverIp","path","gseIndex","iterationIndex"]},"schema_type":"free","shipper_list":[{"cluster_config":{"is_ssl_verify":false,"cluster_name":"es_cluster2","version":"5.4","cluster_id":3,"registered_system":"_default","custom_option":"","schema":null,"domain_name":"es.service.consul","port":10004},"storage_config":{"slice_size":500,"date_format":"%Y%m%d","index_settings":{"number_of_replicas":1,"number_of_shards":1},"slice_gap":1440,"retention":1,"base_index":"2_bklog_yakov_test","mapping_settings":{"_all":{"enabled":true},"dynamic_templates":[{"strings_as_keywords":{"match_mapping_type":"string","mapping":{"norms":"false","type":"keyword"}}}]},"index_datetime_format":"20060102"},"auth_info":{"username":"","password":""},"cluster_type":"elasticsearch"}],"result_table":"2_bklog.yakov_test","field_list":[{"default_value":null,"alias_name":"cloudId","tag":"dimension","description":"\u4e91\u533a\u57dfID","type":"float","is_config_by_user":true,"field_name":"cloudid","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true}},{"default_value":null,"alias_name":"dtEventTimeStamp","tag":"dimension","description":"\u6570\u636e\u65f6\u95f4","type":"int","is_config_by_user":true,"field_name":"time","unit":"","option":{"time_format":"epoch_second","es_format":"epoch_millis","es_type":"date","es_doc_values":true,"es_include_in_all":true,"time_zone":"0"}},{"default_value":null,"alias_name":"gseIndex","tag":"dimension","description":"gse_index","type":"float","is_config_by_user":true,"field_name":"gseindex","unit":"","option":{"es_include_in_all":false,"es_type":"long","es_doc_values":true}},{"default_value":null,"alias_name":"iterationIndex","tag":"dimension","description":"\u81ea\u589eID","type":"float","is_config_by_user":true,"field_name":"iterationindex","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true}},{"default_value":null,"alias_name":"log","tag":"metric","description":"\u65e5\u5fd7\u5185\u5bb9","type":"string","is_config_by_user":true,"field_name":"data","unit":"","option":{"es_include_in_all":true,"es_type":"text","es_doc_values":false}},{"default_value":null,"alias_name":"path","tag":"dimension","description":"\u65e5\u5fd7\u8def\u5f84","type":"string","is_config_by_user":true,"field_name":"filename","unit":"","option":{"es_include_in_all":true,"es_type":"keyword","es_doc_values":true}},{"default_value":null,"alias_name":"serverIp","tag":"dimension","description":"ip","type":"string","is_config_by_user":true,"field_name":"ip","unit":"","option":{"es_include_in_all":false,"es_type":"keyword","es_doc_values":true}},{"default_value":"","alias_name":"","tag":"timestamp","description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","type":"timestamp","is_config_by_user":true,"field_name":"time","unit":"","option":{"es_include_in_all":false,"es_format":"epoch_millis","es_type":"date","es_index":true}}]}],"source_label":"bk_monitor","type_label":"log","data_id":1200177,"mq_config":{"cluster_config":{"is_ssl_verify":false,"cluster_name":"kafka_cluster1","version":null,"cluster_id":1,"registered_system":"_default","custom_option":"","schema":null,"domain_name":"kafka.service.consul","port":9092},"storage_config":{"topic":"0bkmonitor_12001770","partition":1},"auth_info":{"username":"","password":""},"cluster_type":"kafka"},"etl_config":"bk_flat_batch_cluster","option":{"encoding":"UTF-8","is_log_cluster":true}}`
	s.PipelineName = "bk_flat_batch_cluster"
	s.ETLPipelineSuite.SetupTest()
}

// TestRun :
func (s *LogPipelineSuite) TestRun() {
	s.StoreHost(&models.CCHostInfo{
		IP:      "127.0.0.1",
		CloudID: 0,
	}).AnyTimes()
	var wg sync.WaitGroup

	wg.Add(2)

	wg.Add(1)
	s.FrontendPulled = `{"bkmonitorbeat":{"address":["127.0.0.1"],"hostname":"rbtnode1","name":"rbtnode1","version":"7.0.10"},"bizid":0,"cloudid":0,"dataid":1200177,"datetime":"2019-11-21 21:12:00","ext":{"ext_time":"2100","ext_user":"yakov"},"filename":"/tmp/durant.log","group_info":[{"group_info_key1":"value1","group_info_key2":"value2"}],"gseindex":809,"ip":"127.0.0.1","items":[{"data":"Thu Nov 21 21:12:00 CST 2019","iterationindex":0}],"time":1574341920,"utctime":"2019-11-21 13:12:00"}`
	wg.Add(1)
	pipe := s.BuildPipe(func(payload define.Payload) {
		wg.Done()
	}, func(i map[string]interface{}) {
		wg.Done()
	})

	s.RunPipe(pipe, wg.Wait)
}

// TestLogPipelineSuite
func TestLogPipelineSuite(t *testing.T) {
	suite.Run(t, new(LogPipelineSuite))
}
