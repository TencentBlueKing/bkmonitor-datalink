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
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/fta"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// FTAPipelineSuite :
type FTAPipelineSuite struct {
	ETLPipelineSuite
}

// SetupTest :
func (s *FTAPipelineSuite) SetupTest() {
	s.ConsulConfig = `{
		"etl_config": "bk_fta_event", 
		"option": {
			"alert_config": [
				{
					"name": "CPU Usage",
					"rules": [
						{
							"key": "event.name",
							"value": ["^CPU"],
							"method": "reg",
							"condition": ""
						}
					]
				},
				{
					"name": "Default"
				}
			],
			"normalization_config":[
				{
					"field":"target",
					"expr":"event.dimensions[?field=='ip'].value | [0]"
				},
				{
					"field":"tags",
					"expr":"merge(event.tag, event.dimensions[?field=='device_name'].{device: value} | [0])"
				},
				{
					"field":"event_id",
					"expr":"id"
				}
			]
		},
		"result_table_list":[
			{
				"schema_type":"free",
				"shipper_list":[
					{
						"cluster_config":{
							"domain_name":"kafka.service.consul",
							"port":9092
						},
						"storage_config":{
							"topic":"test_topic"
						},
						"cluster_type":"kafka"
					}
				],
				"result_table":"base",
				"field_list":[
					{
						"type": "string",
						"is_config_by_user": true,
						"field_name": "event_id"
					},
					{
						"type": "string",
						"is_config_by_user": true,
						"field_name": "alert_name"
					},
					{
						"type": "string",
						"is_config_by_user": true,
						"field_name": "target"
					},
					{
						"type":"object",
						"is_config_by_user": true,
						"field_name":"tags"
					},
					{
						"type":"string",
						"is_config_by_user": true,
						"field_name":"description"
					},
					{
						"type":"int",
						"is_config_by_user": true,
						"field_name":"severity"
					}
				]
			}
		]
	}`
	s.PipelineName = "bk_fta_event"
	s.ETLPipelineSuite.SetupTest()
}

// TestRun :
func (s *FTAPipelineSuite) TestRun() {
	var wg sync.WaitGroup

	wg.Add(1)
	s.FrontendPulled = `{"bk_plugin_id": "bkplugin", "bk_ingest_time": 1618210322, "data": [{"__bk_event_id__": "123", "id": "abcd", "event": {"tag": {"my": "test"}, "name": "CPU使用率", "dimensions": [{"field": "device_name", "value": "cpu0"}, {"field": "ip", "value": "127.0.0.1"}]}}]}`
	wg.Add(1)
	pipe := s.BuildPipe(func(payload define.Payload) {
		wg.Done()
	}, func(result map[string]interface{}) {
		wg.Done()
		delete(result, "time")
		delete(result, "bk_clean_time")

		// 排序
		tags := result["tags"].([]interface{})
		sort.SliceStable(tags, func(i, j int) bool {
			return tags[i].(map[string]string)["key"] < tags[j].(map[string]string)["key"]
		})
		result["tags"] = tags

		s.MapEqual(map[string]interface{}{
			"alert_name": "CPU Usage",
			"target":     "127.0.0.1",
			"tags": []interface{}{
				map[string]string{
					"key":   "my",
					"value": "test",
				},
				map[string]string{
					"key":   "device",
					"value": "cpu0",
				},
			},
			"bk_ingest_time": 1618210322.0,
			"event_id":       "abcd",
			"plugin_id":      "bkplugin",
		}, result)
	})

	s.RunPipe(pipe, wg.Wait)
}

// TestFTAPipelineSuite :
func TestFTAPipelineSuite(t *testing.T) {
	suite.Run(t, new(FTAPipelineSuite))
}
