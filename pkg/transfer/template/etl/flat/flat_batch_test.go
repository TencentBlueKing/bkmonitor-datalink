// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package flat_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/flat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// FlatBatchTest
type FlatBatchTest struct {
	testsuite.ETLSuite
}

func (f *FlatBatchTest) SetupTest() {
	f.ETLSuite.SetupTest()
	f.PipelineConfig.ETLConfig = "bk_flat_batch"
}

// TestUsage
func (s *FlatBatchTest) TestUsage() {
	processor, err := flat.NewBatchProcessor(s.CTX, "test")
	s.NoError(err)
	s.RunN(2, `{"bizid":0,"bk_biz_id":2,"bk_cloud_id":0,"cloudid":0,"ip":"127.0.0.1","testM":10086,"testD":"testD","timestamp":1554094763,"items":[{"index":1,"data":"hello"},{"index":2,"data":"world"}],"group_info":[{"tag":"aaa","tag1":"aaa1"},{"tag":"bbb","tag1":"bbb1"}]}`,
		processor,
		func(result map[string]interface{}) {
			index := result["index"]
			data := result["data"]
			s.MapEqual(map[string]interface{}{
				"data":           data,
				"index":          index,
				"bizid":          0.0,
				"cloudid":        0.0,
				"ip":             "127.0.0.1",
				"bk_supplier_id": 0.0,
				"bk_cloud_id":    0.0,
				"bk_biz_id":      2.0,
				"testD":          "testD",
				"testM":          10086.0,
				"group_info": []interface{}{
					map[string]interface{}{"tag": "aaa", "tag1": "aaa1"},
					map[string]interface{}{"tag": "bbb", "tag1": "bbb1"},
				},
				"items":     map[string]interface{}{"data": data, "index": index},
				"timestamp": 1554094763.0,
			}, result)
		},
	)
}

// TestNotHaveItems
func (s *FlatBatchTest) TestNotHaveItems() {
	processor, err := flat.NewBatchProcessor(s.CTX, "test")
	s.NoError(err)
	s.RunN(1, `{"bizid":0,"bk_biz_id":2,"bk_cloud_id":0,"cloudid":0,"ip":"127.0.0.1","testM":10086,"testD":"testD","timestamp":1554094763,"group_info":[{"tag":"aaa","tag1":"aaa1"},{"tag":"bbb","tag1":"bbb1"}]}`,
		processor,
		func(result map[string]interface{}) {
			index := result["index"]
			data := result["data"]
			s.MapEqual(map[string]interface{}{
				"data":           data,
				"index":          index,
				"bizid":          0.0,
				"cloudid":        0.0,
				"ip":             "127.0.0.1",
				"bk_supplier_id": 0.0,
				"bk_cloud_id":    0.0,
				"bk_biz_id":      2.0,
				"testD":          "testD",
				"testM":          10086.0,
				"group_info": []interface{}{
					map[string]interface{}{"tag": "aaa", "tag1": "aaa1"},
					map[string]interface{}{"tag": "bbb", "tag1": "bbb1"},
				},
				"timestamp": 1554094763.0,
			}, result)
		},
	)
}

// TestChangeItemsNameUsage
func (s *FlatBatchTest) TestChangeItemsNameUsage() {
	pipelineConfig := config.NewPipelineConfig()
	consulConfig := `{"result_table_list":[{"shipper_list":[{"cluster_config":{"port":10004,"is_ssl_verify":false,"domain_name":"es.service.consul","version":"5.4"},"storage_config":{"index_datetime_format":"20060102_write"},"auth_info":{"username":"","password":""},"cluster_type":"elasticsearch"}],"result_table":"2_bklog.abcde"}],"option":{"encoding":"UTF-8","flat_batch_key":"data"},"etl_config":"bk_standard_event","mq_config":{"cluster_config":{"creator":"system","registered_system":"_default","create_time":1574157128,"cluster_id":1,"port":9092,"is_ssl_verify":false,"domain_name":"kafka.service.consul","cluster_name":"kafka_cluster1","version":null,"last_modify_user":"system","custom_option":"","schema":null},"storage_config":{"topic":"0bk_bkmonitorv3_15000030","partition":1},"auth_info":{"username":"","password":""},"cluster_type":"kafka"}}`

	s.NoError(json.Unmarshal([]byte(consulConfig), pipelineConfig))

	s.CTX = config.PipelineConfigIntoContext(
		s.CTX, pipelineConfig,
	)

	processor, err := flat.NewBatchProcessor(s.CTX, "test")

	s.NoError(err)
	s.RunN(2, `{"data_id":10000,"version":"v2","data":[{"event_name":"port_error","event":{"event_content":"eventdescrition"},"dimension":{"module":"module"},"timestamp":1558774691000000,"target":"127.0.0.1"},{"event_name":"corefile","event":{"event_content":"eventdescrition"},"dimension":{"set":"set"},"timestamp":1558774691000000,"target":"127.0.0.1"}],"bk_info":{}}`,
		processor,
		func(result map[string]interface{}) {
			eventName := result["event_name"]
			event := result["event"]
			dimension := result["dimension"]

			s.MapEqual(map[string]interface{}{
				"data_id":    10000.0,
				"version":    "v2",
				"event_name": eventName,
				"event":      event,
				"dimension":  dimension,
				"data": map[string]interface{}{
					"event_name": eventName,
					"event":      event,
					"dimension":  dimension,
					"timestamp":  1558774691000000.0,
					"target":     "127.0.0.1",
				},
				"target":    "127.0.0.1",
				"timestamp": 1558774691000000.0,
				"bk_info":   map[string]interface{}{},
			}, result)
		},
	)
}

// TestServletTest :
func TestFlatBatchTest(t *testing.T) {
	suite.Run(t, new(FlatBatchTest))
}
