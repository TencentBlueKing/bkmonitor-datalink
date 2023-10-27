// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package formatter_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// HandlerSuite :
type EventSuite struct {
	testsuite.ETLSuite
}

func (s *EventSuite) TestNormalEventProcessor() {
	// 配置一个实际的consul配置得到的context
	s.CTX = testsuite.PipelineConfigStringInfoContext(s.CTX, s.PipelineConfig, `{"result_table_list":[{"option":{"event_dimension":{"login":["log_path","set","module"],"custom_event_name":["dimension_two","dimension_one"]}},"schema_type":"free","shipper_list":[{"cluster_config":{"creator":"system","registered_system":"_default","create_time":1584105478,"cluster_id":17,"port":9090,"is_ssl_verify":false,"domain_name":"test.domain.mq","cluster_name":"test_ES_cluster","version":null,"last_modify_user":"system","custom_option":"","schema":null},"storage_config":{"slice_size":500,"date_format":"%Y%m%d%H","index_settings":{},"slice_gap":10500,"retention":30,"base_index":"1_bkmonitor_event_1500003","mapping_settings":{"dynamic_templates":[{"discover_dimension":{"path_match":"dimensions.*","mapping":{"type":"keyword"}}}]},"index_datetime_format":"2006010215_write"},"auth_info":{"username":"","password":""},"cluster_type":"elasticsearch"}],"result_table":"1_bkmonitor_event_1500003","field_list":[{"default_value":null,"alias_name":"","tag":"dimension","description":"","type":"string","is_config_by_user":true,"field_name":"bk_target","unit":"","option":{"es_type":"keyword"}},{"default_value":null,"alias_name":"","tag":"dimension","description":"","type":"object","is_config_by_user":true,"field_name":"dimensions","unit":"","option":{"es_type":"object","es_dynamic":true}},{"default_value":null,"alias_name":"","tag":"dimension","description":"","type":"object","is_config_by_user":true,"field_name":"event","unit":"","option":{"es_type":"object","es_properties":{"event_content":{"type":"text"},"_bk_count":{"type":"integer"}}}},{"default_value":null,"alias_name":"","tag":"dimension","description":"","type":"string","is_config_by_user":true,"field_name":"event_name","unit":"","option":{"es_type":"keyword"}},{"default_value":"","alias_name":"","tag":"timestamp","description":"\\u6570\\u636e\\u4e0a\\u62a5\\u65f6\\u95f4","type":"timestamp","is_config_by_user":true,"field_name":"time","unit":"","option":{"es_format":"epoch_millis","es_type":"date_nanos"}}]}],"source_label":"bk_monitor","bk_data_id":1500003,"option":{"flat_batch_key":"data","timestamp_precision":"us"},"data_id":1500003,"etl_config":"bk_standard_event_v2","mq_config":{"cluster_config":{"creator":"system","registered_system":"_default","create_time":1584105478,"cluster_id":14,"port":9090,"is_ssl_verify":false,"domain_name":"test.domain.mq","cluster_name":"test_kafka_cluster","version":null,"last_modify_user":"system","custom_option":"","schema":null},"storage_config":{"topic":"0bkmonitor_15000030","partition":1},"auth_info":{"username":"","password":""},"cluster_type":"kafka"},"type_label":"bk_event"}`)

	// 增加一些option的配置内容
	rt := config.ResultTableConfigFromContext(s.CTX)
	rtOption := utils.NewMapHelper(rt.Option)
	rtOption.Set(config.ResultTableOptEventAllowNewEvent, false)
	rtOption.Set(config.ResultTableOptEventContentList, map[string]interface{}{"event_content": struct{}{}, "bk_count": struct{}{}})
	rtOption.Set(config.ResultTableOptEventAllowNewDimension, true)
	rtOption.Set(config.ResultTableOptEventDimensionMustHave, []string{"target"})
	rtOption.Set(config.ResultTableOptEventEventMustHave, []string{"event_content", "bk_count"})
	rtOption.Set(config.ResultTableOptEventDimensionList, make(map[string][]string))

	// 通过context得到一个新的processor
	processor, err := define.NewDataProcessor(s.CTX, "event_v2_handler")
	s.Equal(nil, err)
	s.NotEqual(nil, processor)

	// 构造一个虚假的payload测试
	// 正常的数据
	s.RunN(
		1,
		`{
			"dimensions": {
				"event_name":"corefile",
				"target":"127.0.0.1",
				"dimensions": {
					"path":"/data/corefile/file.txt"
				}
			},
			"metrics":{
				"event": {
					"bk_count":123,
					"event_content":"corefile found"
				}
			},
			"time":1558774691000000
		}`,
		processor,
		func(result map[string]interface{}) {
			expect := map[string]interface{}{
				"exemplar": nil,
				"dimensions": map[string]interface{}{
					"event_name": "corefile",
					"target":     "127.0.0.1",
					"dimensions": map[string]interface{}{
						"path": "/data/corefile/file.txt",
					},
				},
				"metrics": map[string]interface{}{
					"event": map[string]interface{}{
						"bk_count":      123.0,
						"event_content": "corefile found",
					},
				},
				"time": 1558774691000000.0,
			}
			if !cmp.Equal(result, expect) {
				diff := cmp.Diff(result, expect)
				s.FailNow("difference: %s", diff)
			}
		},
	)

	// 缺少必要的维度, target
	s.RunN(
		0,
		`{
			"dimensions": {
			  "event_name": "corefile",
			  "dimensions": {
				"path": "/data/corefile/file.txt"
			  }
			},
			"metrics": {
			  "event": {
				"bk_count": 123,
				"event_content": "corefile found"
			  }
			},
			"time": 1558774691000000
		}`,
		processor,
		func(result map[string]interface{}) {},
	)

	// 缺少必要的event内容, event_content
	s.RunN(
		0,
		`{
			"dimensions":{
				"event_name":"corefile",
				"target":"127.0.0.1",
				"dimensions": {
					"path":"/data/corefile/file.txt"
				}
			},
			"metrics":{
				"event": {
					"bk_count":123
				}
			},
			"time":1558774691000000
		}`,
		processor,
		func(result map[string]interface{}) {},
	)

	// 有多于的的event内容, event_new, 最后入库时会被删除
	s.RunN(
		1,
		`{
			"dimensions":{
				"event_name":"corefile",
				"target":"127.0.0.1",
				"dimensions": {
					"path":"/data/corefile/file.txt"
				}
			},
			"metrics":{
				"event": {
					"bk_count":123,
					"event_content": "corefile found",
					"event_new": "haha"
				}
			},
			"time":1558774691000000
		}`,
		processor,
		func(result map[string]interface{}) {
			expect := map[string]interface{}{
				"exemplar": nil,
				"dimensions": map[string]interface{}{
					"event_name": "corefile",
					"target":     "127.0.0.1",
					"dimensions": map[string]interface{}{
						"path": "/data/corefile/file.txt",
					},
				},
				"metrics": map[string]interface{}{
					"event": map[string]interface{}{
						"bk_count":      123.0,
						"event_content": "corefile found",
					},
				},
				"time": 1558774691000000.0,
			}
			if !cmp.Equal(result, expect) {
				diff := cmp.Diff(result, expect)
				s.FailNow("difference: %s", diff)
			}
		},
	)

	// 时间精度ns与配置精度us不匹配
	s.RunN(
		0,
		`{
			"dimensions":{
				"event_name":"corefile",
				"target":"127.0.0.1",
				"dimensions": {
					"path":"/data/corefile/file.txt"
				}
			},
			"metrics":{
				"event": {
					"bk_count":123,
					"event_content": "content"
				}
			},
			"time":1558774691000000000
		}`,
		processor,
		func(result map[string]interface{}) {},
	)
}

// TestProcessSuite :
func TestEventProcessSuite(t *testing.T) {
	suite.Run(t, new(EventSuite))
}
