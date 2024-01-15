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
						"field":"event_id",
						"expr":"event.id"
					},
					{
						"field":"dimensions",
						"expr":"{name: event.name}"
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
						},
						{
							"type": "object",
                            "is_config_by_user": true,
							"field_name":"dedupe_keys"
						}
					]
				}
			]
		}`,
	)

	processor, err := fta.NewAlertFTAProcessor(s.CTX, "test")

	s.NoError(err)
	s.Run(`{"bk_plugin_id": "bkplugin", "bk_ingest_time": 1618210322, "__bk_event_id__": "123", "data": {"event": {"report_time": "2021-03-18 17:30:07", "tag": {}, "name": "CPU使用率", "dimensions": [{"field": "device_name", "value": "cpu0"}, {"field": "ip", "value": "127.0.0.1"}]}} }`,
		processor,
		func(result map[string]interface{}) {
			if result == nil {
				s.T().Fatal("result is nil")
			}
			delete(result, "bk_clean_time")
			s.MapEqual(map[string]interface{}{
				"tags": []interface{}{
					map[string]interface{}{
						"key":   "device",
						"value": "cpu0",
					},
					map[string]interface{}{
						"key":   "name",
						"value": "CPU使用率",
					},
				},
				"target":         "127.0.0.1",
				"alert_name":     "CPU Usage",
				"time":           1616059807.0,
				"bk_ingest_time": 1618210322.0,
				"event_id":       "123",
				"plugin_id":      "bkplugin",
				"dedupe_keys":    []interface{}{"name"},
			}, result)
		},
	)

	s.Run(`{"bk_plugin_id": "bkplugin", "bk_ingest_time": 1618210322, "__bk_event_id__": "123", "data": {"event": {"report_time": "2021-03-18 17:30:07", "tag": {}, "name": "test_event", "dimensions": [{"field": "device_name", "value": "cpu0"}, {"field": "ip", "value": "127.0.0.1"}]}}}`,
		processor,
		func(result map[string]interface{}) {
			if result == nil {
				s.T().Fatal("result is nil")
			}
			delete(result, "bk_clean_time")
			s.MapEqual(map[string]interface{}{
				"tags": []interface{}{
					map[string]interface{}{
						"key":   "device",
						"value": "cpu0",
					},
					map[string]interface{}{
						"key":   "name",
						"value": "test_event",
					},
				},
				"target":         "127.0.0.1",
				"alert_name":     "Default",
				"time":           1616059807.0,
				"bk_ingest_time": 1618210322.0,
				"event_id":       "123",
				"plugin_id":      "bkplugin",
				"dedupe_keys":    []interface{}{"name"},
			}, result)
		},
	)

	s.Run(`{"bk_plugin_id": "bkplugin", "bk_ingest_time": 1618210322, "__bk_event_id__": "123", "data": {"event": {"report_time": "2021-03-18 17:30:07", "tag": {}, "type": "aaa", "name": "test_event", "dimensions": [{"field": "device_name", "value": "cpu0"}, {"field": "ip", "value": "127.0.0.1"}]}}}`,
		processor,
		func(result map[string]interface{}) {
			if result == nil {
				s.T().Fatal("result is nil")
			}

			delete(result, "bk_clean_time")
			s.MapEqual(map[string]interface{}{
				"tags": []interface{}{
					map[string]interface{}{
						"key":   "device",
						"value": "cpu0",
					},
					map[string]interface{}{
						"key":   "name",
						"value": "CPU使用率",
					},
				},
				"target":         "127.0.0.1",
				"alert_name":     "Test",
				"time":           1616059807.0,
				"bk_ingest_time": 1618210322.0,
				"event_id":       "123",
				"plugin_id":      "bkplugin",
				"dedupe_keys":    []interface{}{"name"},
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
								"expr": "['google_cloud_alert_2', incident.incident_id] | join('.', @)"
							},
							{
								"field": "description",
								"expr": "incident.summary"
							},
							{
								"field": "metric",
								"expr": "incident.metric.type"
							},
							{
								"field": "status",
								"expr": "get_field({OPEN: 'ABNORMAL', open: 'ABNORMAL', CLOSED: 'CLOSED', closed: 'CLOSED'}, incident.state)"
							},
							{
								"field": "severity",
								"expr": "get_field({Warning: '3', Error: '2', Critical: '1'}, incident.severity)"
							},
							{
								"field": "bk_biz_id",
								"expr": "'2'"
							},
							{
								"field": "tags",
								"expr": "{  scoping_project_id: incident.scoping_project_id,  scoping_project_number: incident.scoping_project_number,  resource_id: incident.resource_id,  resource_name: incident.resource_name,  resource_type_display_name: incident.resource_type_display_name,  metric_display_name: incident.metric.displayName,  url: incident.url}"
							},
							{
								"field":"dimensions",
								"expr":"merge(incident.resource.labels, incident.metric.labels, {resource_type: incident.resource.type, metric_type: incident.metric.type})"
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

	if processor == nil {
		s.T().Fatal("processor is nil")
	}

	s.Run(`{"bk_data_id":1572956,"bk_plugin_id":"rest_api","bk_agent_id":"","ip":"127.0.0.1","hostname":"VM-68-183-centos","dataid":1572956,"bizid":0,"cloudid":0,"gseindex":36,"bk_host_id":0,"bk_ingest_time":1703843214, "data":{"__http_query_params__": {"source": "google"}, "incident": {"threshold_value": "5", "condition": {"conditionThreshold": {"filter": "resource.type = \"audited_resource\" AND metric.type = \"logging.googleapis.com/byte_count\"", "thresholdValue": 5, "trigger": {"count": 1}, "aggregations": [{"alignmentPeriod": "300s", "perSeriesAligner": "ALIGN_RATE"}], "comparison": "COMPARISON_GT", "duration": "0s"}, "displayName": "Audited Resource - Log bytes", "name": "projects/e-pulsar-410908/alertPolicies/13599821159254713273/conditions/1331777643246131723"}, "observed_value": "6.477", "resource": {"labels": {"method": "google.monitoring.v3.AlertPolicyService.UpdateAlertPolicy", "project_id": "e-pulsar-410908", "service": "monitoring.googleapis.com"}, "type": "audited_resource"}, "resource_type_display_name": "Audited Resource", "severity": "Warning", "url": "https://console.cloud.google.com/monitoring/alerting/incidents/0.n808anxyygx2?project=e-pulsar-410908", "condition_name": "Audited Resource - Log bytes", "ended_at": null, "incident_id": "0.n808anxyygx2", "resource_id": "", "state": "open", "documentation": {"content": "hjkwedfjklaswjkfhjkasdhfkshadfhjkd", "mime_type": "text/markdown", "subject": "[ALERT - Warning] Audited Resource - Log bytes on e-pulsar-410908 Audited Resource labels {project_id=e-pulsar-410908, service=monitoring.googleapis.com, method=google.monitoring.v3.AlertPolicyService.UpdateAlertPolicy}"}, "policy_name": "aaa", "scoping_project_number": 635651495397, "summary": "Log bytes for e-pulsar-410908 Audited Resource labels {project_id=e-pulsar-410908, service=monitoring.googleapis.com, method=google.monitoring.v3.AlertPolicyService.UpdateAlertPolicy} with metric labels {log=cloudaudit.googleapis.com/activity, severity=NOTICE} is above the threshold of 5.000 with b value of 6.477.", "metadata": {"system_labels": {}, "user_labels": {}}, "metric": {"displayName": "Log bytes", "labels": {"log": "cloudaudit.googleapis.com/activity", "severity": "NOTICE"}, "type": "logging.googleapis.com/byte_count"}, "resource_name": "e-pulsar-410908 Audited Resource labels {project_id=e-pulsar-410908, service=monitoring.googleapis.com, method=google.monitoring.v3.AlertPolicyService.UpdateAlertPolicy}", "scoping_project_id": "e-pulsar-410908", "started_at": 1704963878}, "version": "1.2", "__http_headers__": {"User-Agent": "curl/7.29.0", "Accept": "*/*", "Content-Type": "application/json", "Content-Length": "2662", "Expect": "100-continue"}},"__bk_event_id__":"089c54d0-6331-4e8d-99bc-0f33bda0ecbd"}`,
		processor,
		func(result map[string]interface{}) {
			if result == nil {
				s.T().Fatal("result is nil")
			}
			delete(result, "bk_clean_time")
			s.MapEqual(map[string]interface{}{
				"alert_name":     "aaa",
				"bk_ingest_time": float64(1703843214),
				"description":    "Log bytes for e-pulsar-410908 Audited Resource labels {project_id=e-pulsar-410908, service=monitoring.googleapis.com, method=google.monitoring.v3.AlertPolicyService.UpdateAlertPolicy} with metric labels {log=cloudaudit.googleapis.com/activity, severity=NOTICE} is above the threshold of 5.000 with b value of 6.477.",
				"event_id":       "google_cloud_alert_2.0.n808anxyygx2",
				"plugin_id":      "rest_api",
				"severity":       float64(3),
				"dedupe_keys":    []interface{}{"log", "method", "metric_type", "project_id", "resource_type", "service", "severity"},
				"tags":           []interface{}{map[string]interface{}{"key": "log", "value": "cloudaudit.googleapis.com/activity"}, map[string]interface{}{"key": "method", "value": "google.monitoring.v3.AlertPolicyService.UpdateAlertPolicy"}, map[string]interface{}{"key": "metric_display_name", "value": "Log bytes"}, map[string]interface{}{"key": "metric_type", "value": "logging.googleapis.com/byte_count"}, map[string]interface{}{"key": "project_id", "value": "e-pulsar-410908"}, map[string]interface{}{"key": "resource_id", "value": ""}, map[string]interface{}{"key": "resource_name", "value": "e-pulsar-410908 Audited Resource labels {project_id=e-pulsar-410908, service=monitoring.googleapis.com, method=google.monitoring.v3.AlertPolicyService.UpdateAlertPolicy}"}, map[string]interface{}{"key": "resource_type", "value": "audited_resource"}, map[string]interface{}{"key": "resource_type_display_name", "value": "Audited Resource"}, map[string]interface{}{"key": "scoping_project_id", "value": "e-pulsar-410908"}, map[string]interface{}{"key": "scoping_project_number", "value": 6.35651495397e+11}, map[string]interface{}{"key": "service", "value": "monitoring.googleapis.com"}, map[string]interface{}{"key": "severity", "value": "NOTICE"}, map[string]interface{}{"key": "url", "value": "https://console.cloud.google.com/monitoring/alerting/incidents/0.n808anxyygx2?project=e-pulsar-410908"}},
			}, result)
		},
	)
}

// TestAlertFTATest :
func TestAlertFTATest(t *testing.T) {
	suite.Run(t, new(AlertFTATest))
}
