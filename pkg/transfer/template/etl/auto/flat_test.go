// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package auto_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/auto"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// FlatTestSuite
type FlatTestSuite struct {
	testsuite.ETLSuite
}

// TestUsage
func (s *FlatTestSuite) TestUsage() {
	s.CTX = testsuite.PipelineConfigStringInfoContext(
		s.CTX, s.PipelineConfig,
		`{"result_table_list":[{"option":{},"schema_type":"fixed","shipper_list":[],"result_table":"uptimecheck.heartbeat","field_list":[{"default_value":null,"alias_name":"","tag":"dimension","description":"\u4e1a\u52a1ID","type":"int","is_config_by_user":true,"field_name":"bk_biz_id","unit":"","option":{}},{"default_value":null,"alias_name":"","tag":"dimension","description":"\u4e91\u533a\u57dfID","type":"int","is_config_by_user":true,"field_name":"bk_cloud_id","unit":"","option":{}},{"default_value":null,"alias_name":"","tag":"dimension","description":"CMDB\u5c42\u7ea7\u4fe1\u606f","type":"string","is_config_by_user":true,"field_name":"bk_cmdb_level","unit":"","option":{"influxdb_disabled":true}},{"default_value":null,"alias_name":"","tag":"dimension","description":"\u5f00\u53d1\u5546ID","type":"int","is_config_by_user":true,"field_name":"bk_supplier_id","unit":"","option":{}},{"default_value":null,"alias_name":"","tag":"metric","description":"\u9519\u8bef\u4e8b\u4ef6\u6570","type":"int","is_config_by_user":true,"field_name":"error","unit":"","option":{}},{"default_value":null,"alias_name":"","tag":"metric","description":"\u5931\u8d25\u4e8b\u4ef6\u6570","type":"int","is_config_by_user":true,"field_name":"fail","unit":"","option":{}},{"default_value":null,"alias_name":"","tag":"dimension","description":"\u91c7\u96c6\u5668IP\u5730\u5740","type":"string","is_config_by_user":true,"field_name":"ip","unit":"","option":{}},{"default_value":null,"alias_name":"","tag":"metric","description":"\u5386\u53f2\u8f7d\u5165\u4efb\u52a1\u6570","type":"int","is_config_by_user":true,"field_name":"loaded_tasks","unit":"","option":{}},{"default_value":null,"alias_name":"","tag":"dimension","description":"\u8282\u70b9 ID","type":"int","is_config_by_user":true,"field_name":"node_id","unit":"","option":{}},{"default_value":null,"alias_name":"","tag":"metric","description":"\u91cd\u8f7d\u6b21\u6570","type":"int","is_config_by_user":true,"field_name":"reload","unit":"","option":{}},{"default_value":null,"alias_name":"","tag":"metric","description":"\u91cd\u8f7d\u65f6\u95f4","type":"int","is_config_by_user":true,"field_name":"reload_timestamp","unit":"","option":{}},{"default_value":null,"alias_name":"","tag":"metric","description":"\u8fd0\u884c\u4efb\u52a1\u6570","type":"int","is_config_by_user":true,"field_name":"running_tasks","unit":"","option":{}},{"default_value":null,"alias_name":"","tag":"dimension","description":"\u72b6\u6001","type":"string","is_config_by_user":true,"field_name":"status","unit":"","option":{}},{"default_value":null,"alias_name":"","tag":"metric","description":"\u6210\u529f\u4e8b\u4ef6\u6570","type":"int","is_config_by_user":true,"field_name":"success","unit":"","option":{}},{"default_value":null,"alias_name":"time","tag":"timestamp","description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","type":"timestamp","is_config_by_user":true,"field_name":"timestamp","unit":"","option":{}},{"default_value":null,"alias_name":"","tag":"metric","description":"\u542f\u52a8\u65f6\u95f4","type":"int","is_config_by_user":true,"field_name":"uptime","unit":"ms","option":{}},{"default_value":null,"alias_name":"","tag":"dimension","description":"\u7248\u672c","type":"string","is_config_by_user":true,"field_name":"version","unit":"ms","option":{}}]}],"source_label":"bk_monitor","type_label":"time_series","data_id":1008,"mq_config":{},"etl_config":"flat","option":{}}`,
	)

	processor, err := auto.NewFlatProcessor(s.CTX, "test")
	s.NoError(err)

	s.Run(`{"bizid":0,"bk_biz_id":2,"bk_cloud_id":0,"cloudid":0,"dataid":1008,"error":0,"fail":0,"gseindex":14130,"ip":"127.0.0.1","loaded_tasks":0,"node_id":10,"reload":0,"reload_timestamp":1548731512,"running_tasks":0,"status":0,"success":0,"timestamp":1549522372,"type":"uptimecheckbeat","uptime":790860000,"version":"1.3.2"}`,
		processor,
		func(result map[string]interface{}) {
			s.EqualRecord(result, map[string]interface{}{
				"dimensions": map[string]interface{}{
					"ip":             "127.0.0.1",
					"bk_supplier_id": 0.0,
					"bk_cloud_id":    0.0,
					"bk_biz_id":      2.0,
					"bk_cmdb_level":  nil,
					"status":         "0",
					"version":        "1.3.2",
					"node_id":        10.0,
				},
				"metrics": map[string]interface{}{
					"error":            0.0,
					"fail":             0.0,
					"loaded_tasks":     0.0,
					"reload":           0.0,
					"reload_timestamp": 1548731512.0,
					"running_tasks":    0.0,
					"success":          0.0,
					"uptime":           790860000.0,
				},
				"time": 1549522372,
			})
		},
	)
}

// FlatTestLogSuite
type FlatTestLogSuite struct {
	testsuite.ETLSuite
}

// 测试日志清洗
func (s *FlatTestLogSuite) TestLogUsage() {
	s.CTX = testsuite.PipelineConfigStringInfoContext(
		s.CTX, s.PipelineConfig,
		`{"bk_data_id":1500101,"data_id":1500101,"mq_config":{"storage_config":{"topic":"0bkmonitor_15001010","partition":1},"cluster_config":{"domain_name":"kafka.service.consul","port":9092,"schema":null,"is_ssl_verify":false,"cluster_id":1,"cluster_name":"kafka_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"kafka","auth_info":{"password":"","username":""}},"etl_config":"bk_flat_batch","result_table_list":[{"result_table":"2_bklog.trace_object","shipper_list":[{"storage_config":{"index_datetime_format":"write_20060102","date_format":"%Y%m%d","slice_size":120,"slice_gap":1440,"retention":7,"warm_phase_days":0,"warm_phase_settings":{},"base_index":"2_bklog_trace_object","index_settings":{"number_of_replicas":1,"index.routing.allocation.include.temperature":"hot","number_of_shards":4},"mapping_settings":{"dynamic_templates":[{"strings_as_keywords":{"match_mapping_type":"string","mapping":{"norms":"false","type":"keyword"}}}]}},"cluster_config":{"domain_name":"127.0.0.1","port":9200,"schema":"http","is_ssl_verify":false,"cluster_id":5,"cluster_name":"\u51b7\u70ed\u6d4b\u8bd5","version":"7.6.2","custom_option":"{\"bk_biz_id\": \"2\", \"hot_warm_config\": {\"is_enabled\": true, \"hot_attr_name\": \"temperature\", \"hot_attr_value\": \"hot\", \"warm_attr_name\": \"temperature\", \"warm_attr_value\": \"warm\"}}","registered_system":"bk_log_search","creator":"admin","create_time":1606725652,"last_modify_user":"admin","is_default_cluster":false},"cluster_type":"elasticsearch","auth_info":{"password":"","username":""}}],"field_list":[{"field_name":"cloudid","type":"float","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"\u4e91\u533a\u57dfID","unit":"","alias_name":"cloudId","option":{"es_type":"integer"}},{"field_name":"reportTime","type":"timestamp","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"\u6570\u636e\u65f6\u95f4","unit":"","alias_name":"dtEventTimeStamp","option":{"es_format":"epoch_millis","es_type":"date","time_zone":8,"real_path":"bk_separator_object.reportTime","time_format":"epoch_millis","field_index":6}},{"field_name":"gseindex","type":"float","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"gse\u7d22\u5f15","unit":"","alias_name":"gseIndex","option":{"es_type":"long"}},{"field_name":"iterationindex","type":"float","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"\u8fed\u4ee3ID","unit":"","alias_name":"iterationIndex","option":{"es_type":"integer"}},{"field_name":"data","type":"string","tag":"metric","default_value":null,"is_config_by_user":true,"description":"original_text","unit":"","alias_name":"log","option":{"es_type":"text"}},{"field_name":"operationName","type":"string","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"keyword","field_index":3,"real_path":"bk_separator_object.operationName"}},{"field_name":"parentID","type":"string","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"keyword","field_index":4,"real_path":"bk_separator_object.parentID"}},{"field_name":"filename","type":"string","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"\u65e5\u5fd7\u8def\u5f84","unit":"","alias_name":"path","option":{"es_type":"keyword"}},{"field_name":"reportTime","type":"float","tag":"metric","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"long","es_doc_values":false,"field_index":6,"real_path":"bk_separator_object.reportTime"}},{"field_name":"ip","type":"string","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"ip","unit":"","alias_name":"serverIp","option":{"es_type":"keyword"}},{"field_name":"spanID","type":"string","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"keyword","field_index":2,"real_path":"bk_separator_object.spanID"}},{"field_name":"startTime","type":"float","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"long","field_index":5,"real_path":"bk_separator_object.startTime"}},{"field_name":"tDuration","type":"float","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"double","field_index":7,"real_path":"bk_separator_object.tDuration"}},{"field_name":"time","type":"timestamp","tag":"timestamp","default_value":"","is_config_by_user":true,"description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","unit":"","alias_name":"","option":{"es_format":"epoch_millis","es_type":"date","time_zone":8,"real_path":"bk_separator_object.reportTime","time_format":"epoch_millis","field_index":6}},{"field_name":"traceFlags","type":"float","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"integer","field_index":8,"real_path":"bk_separator_object.traceFlags"}},{"field_name":"traceID","type":"string","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"keyword","field_index":1,"real_path":"bk_separator_object.traceID"}},{"field_name":"traceLog","type":"string","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"keyword","field_index":9,"real_path":"bk_separator_object.traceLog"}},{"field_name":"traceTag","type":"object","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"object","field_index":10,"real_path":"bk_separator_object.traceTag"}}],"schema_type":"free","option":{"es_unique_field_list":["cloudId","serverIp","path","gseIndex","iterationIndex"],"separator_node_source":"data","retain_original_text":true,"separator":"|","separator_node_action":"delimiter","separator_field_list":["traceID","spanID","operationName","parentID","startTime","reportTime","tDuration","traceFlags","traceLog","traceTag","e"],"separator_node_name":"bk_separator_object"}}],"option":{"encoding":"UTF-8"},"type_label":"log","source_label":"bk_monitor","token":"f128d2e935b74374a8028c83600f0f09","transfer_cluster_id":"default"}`,
	)
	processor, err := auto.NewFlatProcessor(s.CTX, "test")
	s.NoError(err)

	data := `{"bizid":0,"cloudid":0,"dataid":1500101,"datetime":"2021-01-14 19:22:46","ext":"","filename":"/root/tracelog/trace_log_generator/log/new_trace.log","gseindex":6070089,"ip":"127.0.0.1","items":[{"data":"FO2KaTTsaomMZJYNDVDfBpE0vKB0Vlen|V8n5eqmtTIc7tDk2rm4O5jHIcW4U1SoZ|db|ASdT2P8VdpN2Kv1YzIAHkQpFBG0RBhSi|1610623364922|1610623364922|120000|1|[INFO][V8n5eqmtTIc7tDk2rm4O5jHIcW4U1SoZ]: Send, result_code:55419|{\"scene\": \"pve\", \"local_service\": \"auth_svrd\", \"result_code\": 3647, \"error\": false}","iterationindex":0}],"time":1610623366,"utctime":"2021-01-14 11:22:46","data":"FO2KaTTsaomMZJYNDVDfBpE0vKB0Vlen|V8n5eqmtTIc7tDk2rm4O5jHIcW4U1SoZ|db|ASdT2P8VdpN2Kv1YzIAHkQpFBG0RBhSi|1610623364922|1610623364922|120000|1|[INFO][V8n5eqmtTIc7tDk2rm4O5jHIcW4U1SoZ]: Send, result_code:55419|{\"scene\": \"pve\", \"local_service\": \"auth_svrd\", \"result_code\": 3647, \"error\": false}","iterationindex":0}`
	s.Run(
		data,
		processor,
		func(result map[string]interface{}) {
			s.EqualRecord(result, map[string]interface{}{
				"dimensions": map[string]interface{}{
					"dtEventTimeStamp": float64(1610623364922),
					"serverIp":         "127.0.0.1",
					"cloudId":          0.0,
					"gseIndex":         6070089.0,
					"iterationIndex":   0.0,
					"path":             "/root/tracelog/trace_log_generator/log/new_trace.log",
					"operationName":    "db",
					"parentID":         "ASdT2P8VdpN2Kv1YzIAHkQpFBG0RBhSi",
					"spanID":           "V8n5eqmtTIc7tDk2rm4O5jHIcW4U1SoZ",
					"startTime":        1610623364922.0,
					"tDuration":        120000.0,
					"traceFlags":       1.0,
					"traceID":          "FO2KaTTsaomMZJYNDVDfBpE0vKB0Vlen",
					"traceLog":         "[INFO][V8n5eqmtTIc7tDk2rm4O5jHIcW4U1SoZ]: Send, result_code:55419",
					"traceTag":         map[string]interface{}{"scene": "pve", "local_service": "auth_svrd", "result_code": 3647.0, "error": false},
				},
				"metrics": map[string]interface{}{
					"log":        `FO2KaTTsaomMZJYNDVDfBpE0vKB0Vlen|V8n5eqmtTIc7tDk2rm4O5jHIcW4U1SoZ|db|ASdT2P8VdpN2Kv1YzIAHkQpFBG0RBhSi|1610623364922|1610623364922|120000|1|[INFO][V8n5eqmtTIc7tDk2rm4O5jHIcW4U1SoZ]: Send, result_code:55419|{"scene": "pve", "local_service": "auth_svrd", "result_code": 3647, "error": false}`,
					"reportTime": 1610623364922.0,
				},
				"time": 1610623364,
			})
		},
	)
}

// TestFlatTestSuite :
func TestFlatTestSuite(t *testing.T) {
	suite.Run(t, new(FlatTestSuite))
	suite.Run(t, new(FlatTestLogSuite))
}
