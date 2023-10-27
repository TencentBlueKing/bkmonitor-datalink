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

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// StandardPipelineSuite :
type StandardEventPipelineSuite struct {
	testsuite.FreeSchemaETLPipelineSuite
}

// SetupTest :
func (s *StandardEventPipelineSuite) SetupTest() {
	s.PipelineName = "bk_standard_v2_event"
	s.ConsulConfig = `{"result_table_list":[{"option":{"event_dimension":{"login":["log_path","set","module"],"custom_event_name":["dimension_two","dimension_one"]}},"schema_type":"free","shipper_list":[{"cluster_config":{"creator":"system","registered_system":"_default","create_time":1584105478,"cluster_id":17,"port":9090,"is_ssl_verify":false,"domain_name":"test.domain.mq","cluster_name":"test_ES_cluster","version":null,"last_modify_user":"system","custom_option":"","schema":null},"storage_config":{"slice_size":500,"date_format":"%Y%m%d%H","index_settings":{},"slice_gap":10500,"retention":30,"base_index":"1_bkmonitor_event_1500003","mapping_settings":{"dynamic_templates":[{"discover_dimension":{"path_match":"dimensions.*","mapping":{"type":"keyword"}}}]},"index_datetime_format":"2006010215_write"},"auth_info":{"username":"","password":""},"cluster_type":"elasticsearch"}],"result_table":"1_bkmonitor_event_1500003","field_list":[{"default_value":null,"alias_name":"","tag":"dimension","description":"","type":"string","is_config_by_user":true,"field_name":"bk_target","unit":"","option":{"es_type":"keyword"}},{"default_value":null,"alias_name":"","tag":"dimension","description":"","type":"object","is_config_by_user":true,"field_name":"dimensions","unit":"","option":{"es_type":"object","es_dynamic":true}},{"default_value":null,"alias_name":"","tag":"dimension","description":"","type":"object","is_config_by_user":true,"field_name":"event","unit":"","option":{"es_type":"object","es_properties":{"content":{"type":"text"},"_bk_count":{"type":"integer"}}}},{"default_value":null,"alias_name":"","tag":"dimension","description":"","type":"string","is_config_by_user":true,"field_name":"event_name","unit":"","option":{"es_type":"keyword"}},{"default_value":"","alias_name":"","tag":"timestamp","description":"\\u6570\\u636e\\u4e0a\\u62a5\\u65f6\\u95f4","type":"timestamp","is_config_by_user":true,"field_name":"time","unit":"","option":{"es_format":"epoch_millis","es_type":"date_nanos"}}]}],"source_label":"bk_monitor","bk_data_id":1500003,"option":{"flat_batch_key":"data","timestamp_precision":"us"},"data_id":1500003,"etl_config":"bk_standard_event_v2","mq_config":{"cluster_config":{"creator":"system","registered_system":"_default","create_time":1584105478,"cluster_id":14,"port":9090,"is_ssl_verify":false,"domain_name":"test.domain.mq","cluster_name":"test_kafka_cluster","version":null,"last_modify_user":"system","custom_option":"","schema":null},"storage_config":{"topic":"0bkmonitor_15000030","partition":1},"auth_info":{"username":"","password":""},"cluster_type":"kafka"},"type_label":"bk_event"}`
	s.FreeSchemaETLPipelineSuite.SetupTest()
}

// TestUsage :
func (s *StandardEventPipelineSuite) TestUsage() {
	var wg sync.WaitGroup
	s.FrontendPulled = `{"data_id":10000,"access_token":"access token for verify","version":"v2","data":[{"event_name":"端口异常啦","event":{"content":"event descrition"},"dimension":{"d1":"8080这个端口"},"timestamp":1558774691000000,"target":"127.0.0.1"},{"event_name":"端口异常啦","event":{"content":"event descrition"},"dimension":{"d1":"8080这个端口"},"timestamp":1558774691000000,"target":"127.0.0.1"}],"bk_info":{"bk_report_time":1558774691000000,"bk_report_index":1,"bk_unique_id":"4605da1e-9658-4f61-8928-6a4c96a39f43"}}`
	s.ConsulClient.EXPECT().Put(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	wg.Add(3)
	count := 0
	pipe := s.BuildPipe(func(payload define.Payload) {
		wg.Done()
		count += 1
	}, func(i map[string]interface{}) {
		wg.Done()
		count += 1
	})

	s.RunPipe(pipe, wg.Wait)
}

// TestStandardPipelineSuite :
func TestStandardEventPipelineSuite(t *testing.T) {
	suite.Run(t, new(StandardEventPipelineSuite))
}
