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

// AlertFTATest
type AlertFTATest struct {
	testsuite.ETLSuite
}

// TestEvent
func (s *AlertFTATest) TestEvent() {
	s.CTX = testsuite.PipelineConfigStringInfoContext(
		s.CTX, s.PipelineConfig,
		`{
			"etl_config":"bk_fta_event",
			"option":{
				"clean_configs": [
					{
						"alert_config": [
							{
								"name": "CPU",
								"rules": [{"key": "event.name","value": ["^CPU"],"method": "reg","condition": ""}]
							},
							{
								"name": "Test"
							}
						],
						"rules": [{"key": "event.type","value": ["aaa"],"method": "eq","condition": ""}]
					}
				],
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
						"name": "MEM Usage",
						"rules": [
							{
								"key": "event.name",
								"value": ["^MEM"],
								"method": "reg",
								"condition": ""
							},
							{
								"key": "metric_id",
								"value": ["system.mem.usage"],
								"method": "eq",
								"condition": "or"
							}
						]
					},
					{
						"name": "Default"
					}
				],
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

	processor, err := fta.NewAlertFTAProcessor(s.CTX, "test")

	s.NoError(err)
	s.Run(`{"bk_plugin_id": "bkplugin", "bk_ingest_time": 1618210322, "__bk_event_id__": "123", "event": {"report_time": "2021-03-18 17:30:07", "tag": {}, "name": "CPU使用率", "dimensions": [{"field": "device_name", "value": "cpu0"}, {"field": "ip", "value": "127.0.0.1"}]}}`,
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
				"target":         "127.0.0.1",
				"alert_name":     "CPU Usage",
				"time":           1616059807.0,
				"bk_ingest_time": 1618210322.0,
				"event_id":       "123",
				"plugin_id":      "bkplugin",
			}, result)
		},
	)

	s.Run(`{"bk_plugin_id": "bkplugin", "bk_ingest_time": 1618210322, "__bk_event_id__": "123", "event": {"report_time": "2021-03-18 17:30:07", "tag": {}, "name": "test_event", "dimensions": [{"field": "device_name", "value": "cpu0"}, {"field": "ip", "value": "127.0.0.1"}]}}`,
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
				"target":         "127.0.0.1",
				"alert_name":     "Default",
				"time":           1616059807.0,
				"bk_ingest_time": 1618210322.0,
				"event_id":       "123",
				"plugin_id":      "bkplugin",
			}, result)
		},
	)

	s.Run(`{"bk_plugin_id": "bkplugin", "bk_ingest_time": 1618210322, "__bk_event_id__": "123", "event": {"report_time": "2021-03-18 17:30:07", "tag": {}, "type": "aaa", "name": "test_event", "dimensions": [{"field": "device_name", "value": "cpu0"}, {"field": "ip", "value": "127.0.0.1"}]}}`,
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
				"target":         "127.0.0.1",
				"alert_name":     "Test",
				"time":           1616059807.0,
				"bk_ingest_time": 1618210322.0,
				"event_id":       "123",
				"plugin_id":      "bkplugin",
			}, result)
		},
	)
}

func (s *AlertFTATest) TestCleanConfig() {
	s.CTX = testsuite.PipelineConfigStringInfoContext(
		s.CTX, s.PipelineConfig,
		`{
			"etl_config":"bk_fta_event",
			"option":{
				"clean_configs": [
					{
						"rules": [
							{
								"key": "headers.\"user-agent\"",
								"value": [
									"Google-Alerts"
								],
								"method": "eq",
								"condition": "or"
							},
							{
								"key": "__http_query_params__.source",
								"value": [
									"google"
								],
								"method": "eq",
								"condition": "or"
							}
						],
						"normalization_config": [
							{
								"field": "alert_name",
								"expr": "incident.policy_name"
							},
							{
								"field": "event_id",
								"expr": "incident.incident_id"
							},
							{
								"field": "description",
								"expr": "incident.summary"
							},
							{
								"field": "metric",
								"expr": "incident.metric.displayName"
							},
							{
								"field": "category",
								"expr": "category"
							},
							{
								"field": "assignee",
								"expr": "assignee"
							},
							{
								"field": "status",
								"expr": "get_field({OPEN: 'ABNORMAL', open: 'ABNORMAL', CLOSED: 'CLOSED', closed: 'CLOSED'}, incident.state)"
							},
							{
								"field": "target",
								"expr": "target"
							},
							{
								"field": "target_type",
								"expr": "target_type"
							},
							{
								"field": "severity",
								"expr": "1"
							},
							{
								"field": "bk_biz_id",
								"expr": "bk_biz_id || '{{plugin_inst_biz_id}}'"
							},
							{
								"field": "tags",
								"expr": "{scoping_project_id: incident.scoping_project_id, scoping_project_number: incident.scoping_project_number, observed_value: condition.observed_value, resource: to_string(incident.resource), resource_id: incident.resource_id, resource_display_name: incident.resource_display_name, metric: to_string(incident.metric), metadata: to_string(incident.metadata), policy_user_labels: to_string(incident.policy_user_labels), condition: to_string(incident.condition)}"
							},
							{
								"field": "time",
								"expr": "incident.started_at"
							},
							{
								"field": "anomaly_time",
								"expr": "incident.started_at"
							}
						]
					},
					{
						"rules": [
							{
								"key": "__http_query_params__.source",
								"value": [
									"tencent"
								],
								"method": "eq"
							}
						],
						"normalization_config": [
							{
								"field": "alert_name",
								"expr": "alarmPolicyInfo.policyName"
							},
							{
								"field": "event_id",
								"expr": "event_id"
							},
							{
								"field": "description",
								"expr": "alarmPolicyInfo.conditions.metricShowName && alarmPolicyInfo.conditions.calcType && alarmPolicyInfo.conditions.calcValue && alarmPolicyInfo.conditions.calcUnit && join(' ', [alarmPolicyInfo.conditions.metricShowName, alarmPolicyInfo.conditions.calcType, alarmPolicyInfo.conditions.calcValue, alarmPolicyInfo.conditions.calcUnit]) || alarmPolicyInfo.conditions.productShowName && alarmPolicyInfo.conditions.eventShowName && join(' ', [alarmPolicyInfo.conditions.productShowName, alarmPolicyInfo.conditions.eventShowName]) || alarmObjInfo.content"
							},
							{
								"field": "metric",
								"expr": "alarmPolicyInfo.conditions.metricName"
							},
							{
								"field": "category",
								"expr": "category"
							},
							{
								"field": "assignee",
								"expr": "assignee"
							},
							{
								"field": "status",
								"expr": "get_field({1: 'ABNORMAL', 0: 'RECOVERED'}, alarmStatus)"
							},
							{
								"field": "target",
								"expr": "target"
							},
							{
								"field": "target_type",
								"expr": "target_type"
							},
							{
								"field": "severity",
								"expr": "1"
							},
							{
								"field": "bk_biz_id",
								"expr": "bk_biz_id || '{{plugin_inst_biz_id}}'"
							},
							{
								"field": "tags",
								"expr": "{alarmObjInfo: to_string(alarmObjInfo), alarm_type: alarmType, policyId: alarmPolicyInfo.policyId}"
							},
							{
								"field": "time",
								"expr": "firstOccurTime"
							},
							{
								"field": "source_time",
								"expr": "firstOccurTime"
							},
							{
								"field": "anomaly_time",
								"expr": "firstOccurTime"
							}
						]
					}
				],
				"alert_config": [],
				"multiple_events":false,
				"normalization_config":[]
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

	processor, err := fta.NewAlertFTAProcessor(s.CTX, "test")

	s.NoError(err)
	s.Run(`{"bk_data_id":1572956,"bk_plugin_id":"rest_api","bk_agent_id":"","ip":"127.0.0.1","hostname":"VM-68-183-centos","dataid":1572956,"bizid":0,"cloudid":0,"gseindex":36,"bk_host_id":0,"bk_ingest_time":1703843214,"bk_biz_id":2,"alarmType":"metric","alarmPolicyInfo":{"policyId":"policy-n4exeh88","policyType":"cvm_device","policyName":"test","policyTypeCName":"云服务器-基础监控","conditions":{"alarmNotifyType":"continuousAlarm","calcType":">","currentValue":"100","unit":"%","period":"60","historyValue":"5","periodNum":"1","alarmNotifyPeriod":300,"metricName":"cpu_usage","metricShowName":"CPU 利用率","calcValue":"90","calcUnit":"%"}},"durationTime":500,"recoverTime":"2017-03-09 07:50:00","__http_headers__":{"User-Agent":"curl/7.29.0","Accept":"*/*","Content-Type":"application/json","Content-Length":"1039","Expect":"100-continue"},"__http_query_params__":{"source":"tencent"},"sessionId":"xxxxxxxx","alarmStatus":"1","alarmObjInfo":{"region":"gz","namespace":"qce/cvm","appId":"xxxxxxxxxxxx","uin":"xxxxxxxxxxxx","dimensions":{"unInstanceId":"ins-o9p3rg3m","objId":"xxxxxxxxxxxx"}},"firstOccurTime":"2017-03-09 07:00:00","__bk_event_id__":"089c54d0-6331-4e8d-99bc-0f33bda0ecbd"}`,
		processor,
		func(result map[string]interface{}) {
			delete(result, "bk_clean_time")
			s.MapEqual(map[string]interface{}{
				"alert_name":     "test",
				"bk_ingest_time": float64(1703843214),
				"description":    "CPU 利用率 > 90 %",
				"event_id":       "089c54d0-6331-4e8d-99bc-0f33bda0ecbd",
				"plugin_id":      "rest_api",
				"target":         nil,
				"tags": []interface{}{
					map[string]interface{}{
						"key":   "alarmObjInfo",
						"value": `{"appId":"xxxxxxxxxxxx","dimensions":{"objId":"xxxxxxxxxxxx","unInstanceId":"ins-o9p3rg3m"},"namespace":"qce/cvm","region":"gz","uin":"xxxxxxxxxxxx"}`,
					},
					map[string]interface{}{
						"key":   "alarm_type",
						"value": "metric",
					},
					map[string]interface{}{
						"key":   "policyId",
						"value": "policy-n4exeh88",
					},
				},
			}, result)
		},
	)
}

// TestAlertFTATest :
func TestAlertFTATest(t *testing.T) {
	suite.Run(t, new(AlertFTATest))
}
