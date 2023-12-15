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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/gse_event"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

type GseSystemEventPipelineSuite struct {
	FreeSchemaETLPipelineSuite
}

func (s *GseSystemEventPipelineSuite) SetupTest() {
	s.ConsulConfig = `{"result_table_list":[{"option":{"event_dimension":{"login":["log_path","set","module"],"custom_event_name":["dimension_two","dimension_one"]}},"schema_type":"free","shipper_list":[{"cluster_config":{"creator":"system","registered_system":"_default","create_time":1584105478,"cluster_id":17,"port":9090,"is_ssl_verify":false,"domain_name":"test.domain.mq","cluster_name":"test_ES_cluster","version":null,"last_modify_user":"system","custom_option":"","schema":null},"storage_config":{"slice_size":500,"date_format":"%Y%m%d%H","index_settings":{},"slice_gap":10500,"retention":30,"base_index":"1_bkmonitor_event_1500003","mapping_settings":{"dynamic_templates":[{"discover_dimension":{"path_match":"dimensions.*","mapping":{"type":"keyword"}}}]},"index_datetime_format":"2006010215_write"},"auth_info":{"username":"","password":""},"cluster_type":"elasticsearch"}],"result_table":"1_bkmonitor_event_1500003","field_list":[{"default_value":null,"alias_name":"","tag":"dimension","description":"","type":"string","is_config_by_user":true,"field_name":"bk_target","unit":"","option":{"es_type":"keyword"}},{"default_value":null,"alias_name":"","tag":"dimension","description":"","type":"object","is_config_by_user":true,"field_name":"dimensions","unit":"","option":{"es_type":"object","es_dynamic":true}},{"default_value":null,"alias_name":"","tag":"dimension","description":"","type":"object","is_config_by_user":true,"field_name":"event","unit":"","option":{"es_type":"object","es_properties":{"content":{"type":"text"},"_bk_count":{"type":"integer"}}}},{"default_value":null,"alias_name":"","tag":"dimension","description":"","type":"string","is_config_by_user":true,"field_name":"event_name","unit":"","option":{"es_type":"keyword"}},{"default_value":"","alias_name":"","tag":"timestamp","description":"\\u6570\\u636e\\u4e0a\\u62a5\\u65f6\\u95f4","type":"timestamp","is_config_by_user":true,"field_name":"time","unit":"","option":{"es_format":"epoch_millis","es_type":"date_nanos"}}]}],"source_label":"bk_monitor","bk_data_id":1500003,"option":{"flat_batch_key":"data","timestamp_precision":"ms"},"data_id":1500003,"etl_config":"bk_standard_event_v2","mq_config":{"cluster_config":{"creator":"system","registered_system":"_default","create_time":1584105478,"cluster_id":14,"port":9090,"is_ssl_verify":false,"domain_name":"test.domain.mq","cluster_name":"test_kafka_cluster","version":null,"last_modify_user":"system","custom_option":"","schema":null},"storage_config":{"topic":"0bkmonitor_15000030","partition":1},"auth_info":{"username":"","password":""},"cluster_type":"kafka"},"type_label":"bk_event"}`
	s.PipelineName = "bk_gse_system_event"
	s.FreeSchemaETLPipelineSuite.SetupTest()
}

// TestRun :
func (s *GseSystemEventPipelineSuite) TestRun() {
	var wg sync.WaitGroup

	hostInfo := models.CCHostInfo{
		IP:      "127.0.0.1",
		CloudID: 0,
		CCTopoBaseModelInfo: &models.CCTopoBaseModelInfo{
			BizID: []int{2},
			Topo:  []map[string]string{},
		},
	}
	s.StoreHost(&hostInfo).AnyTimes()
	s.Store.EXPECT().Get(gomock.Any()).Return(nil, define.ErrItemNotFound).AnyTimes()

	wg.Add(1)
	s.FrontendPulled = `{"server": "","time": "2019-03-02 15:29:24","timezone": 0,"utctime": "2019-03-02 15:29:24","utctime2": "2019-03-02 07:29:24","value": [{"event_desc": "","event_raw_id": 0,"event_time": "2019-03-02 07:29:24","event_source_system": "","event_title": "","event_type": "gse_basic_alarm_type","extra": {"type": 2,"count": 0,"host": [{"bizid": 0,"cloudid": 0,"ip": "127.0.0.1"}]}}]}`
	s.ConsulClient.EXPECT().Put(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	wg.Add(1)
	pipe := s.BuildPipe(func(payload define.Payload) {
		wg.Done()
	}, func(result map[string]interface{}) {
		wg.Done()
		s.MapEqual(map[string]interface{}{
			"dimensions": map[string]interface{}{
				"dimensions": map[string]interface{}{
					"bk_target_ip":       "127.0.0.1",
					"bk_target_cloud_id": "0",
					"bk_biz_id":          "2",
					"ip":                 "127.0.0.1",
					"bk_cloud_id":        "0",
				},
				"event_name": "AgentLost",
				"target":     "0:127.0.0.1",
			},
			"time": float64(1551511764000),
			"metrics": map[string]interface{}{
				"event": map[string]interface{}{
					"content": "AgentLost",
				},
			},
		}, result)
	})

	s.RunPipe(pipe, wg.Wait)
}

// TestGseSystemEventPipelineSuite: 测试GSE系统事件
func TestGseSystemEventPipelineSuite(t *testing.T) {
	suite.Run(t, new(GseSystemEventPipelineSuite))
}
