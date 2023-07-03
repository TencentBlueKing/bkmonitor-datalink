// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fta_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/fta"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// MapFTATest
type MapFTATest struct {
	testsuite.ETLSuite
}

// TestEvent
func (s *MapFTATest) TestEvent() {
	s.CTX = testsuite.PipelineConfigStringInfoContext(
		s.CTX, s.PipelineConfig,
		`{
			"etl_config":"bk_fta_event",
			"option":{
				"multiple_events":false,
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
						"field":"time",
						"expr":"event.report_time"
					},
					{
						"field":"alert_name",
						"expr":"event.name"
					},
					{
						"field":"event_id",
						"expr":"event.id"
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
							"field_name": "alert_name"
						},
						{
							"type": "string",
							"is_config_by_user": true,
							"field_name": "event_id"
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
						},
						{
							"type": "timestamp",
							"is_config_by_user": true,
							"field_name": "time",
							"option": {
								"time_format": "datetime",
								"time_zone": 8
							}
						}
					]
				}
			]
		}`,
	)

	processor, err := fta.NewMapFTAProcessor(s.CTX, "test")

	s.NoError(err)
	s.Run(`{"bk_plugin_id": "bkplugin", "bk_ingest_time": 1618210322, "__bk_alert_name__": "default alert name", "__bk_event_id__": "123", "event": {"report_time": "2021-03-18 17:30:07", "tag": {}, "name": "test_event", "dimensions": [{"field": "device_name", "value": "cpu0"}, {"field": "ip", "value": "127.0.0.1"}]}}`,
		processor,
		func(result map[string]interface{}) {
			delete(result, "bk_clean_time")
			s.MapEqual(map[string]interface{}{
				"tags": []interface{}{
					map[string]interface{}{
						"key":   "device",
						"value": "cpu0",
					},
				},
				"target":     "127.0.0.1",
				"alert_name": "default alert name",
				//"bk_local_time": 1.0,
				"time":           1616059807.0,
				"bk_ingest_time": 1618210322.0,
				"event_id":       "123",
				"plugin_id":      "bkplugin",
			}, result)
		},
	)

	s.Run(`{"bk_plugin_id": "bkplugin", "bk_ingest_time": 1618210322, "__bk_alert_name__": "", "__bk_event_id__": "123", "event": {"report_time": "2021-03-18 17:30:07", "tag": {}, "name": "test_event", "dimensions": [{"field": "device_name", "value": "cpu0"}, {"field": "ip", "value": "127.0.0.1"}]}}`,
		processor,
		func(result map[string]interface{}) {
			delete(result, "bk_clean_time")
			s.MapEqual(map[string]interface{}{
				"tags": []interface{}{
					map[string]interface{}{
						"key":   "device",
						"value": "cpu0",
					},
				},
				"target":     "127.0.0.1",
				"alert_name": "test_event",
				//"bk_local_time": 1.0,
				"time":           1616059807.0,
				"bk_ingest_time": 1618210322.0,
				"event_id":       "123",
				"plugin_id":      "bkplugin",
			}, result)
		},
	)

	s.Run(`{"bk_plugin_id": "bkplugin", "bk_ingest_time": 1618210322, "__bk_alert_name__": null, "__bk_event_id__": "123", "event": {"report_time": "2021-03-18 17:30:07", "tag": {}, "name": "test_event", "dimensions": [{"field": "device_name", "value": "cpu0"}, {"field": "ip", "value": "127.0.0.1"}]}}`,
		processor,
		func(result map[string]interface{}) {
			delete(result, "bk_clean_time")
			s.MapEqual(map[string]interface{}{
				"tags": []interface{}{
					map[string]interface{}{
						"key":   "device",
						"value": "cpu0",
					},
				},
				"target":     "127.0.0.1",
				"alert_name": "test_event",
				//"bk_local_time": 1.0,
				"time":           1616059807.0,
				"bk_ingest_time": 1618210322.0,
				"event_id":       "123",
				"plugin_id":      "bkplugin",
			}, result)
		},
	)
}

// TestMapFTATest :
func TestMapFTATest(t *testing.T) {
	suite.Run(t, new(MapFTATest))
}
