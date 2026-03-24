// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.package fta

package fta

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

type AlertTest struct {
	testsuite.ETLSuite
}

var cleanConfigStr = `
clean_configs:
  - rules:
    - key: __http_query_params__.source
      method: eq
      condition: and
      value:
        - "azure"
    - key: data.essentials.monitoringService
      method: eq
      value:
        - "Platform"
    normalization_config:
      - field: alert_name
        expr: data.essentials.alertRule
      - field: event_id
        expr: "['azure_cloud_alert_{{plugin_inst_biz_id}}', data.essentials.originAlertId] | join('.', @)"
      - field: description
        expr: data.essentials.description
      - field: metric
        expr: "[data.alertContext.condition.allOf[0].metricNamespace || data.alertContext.condition.allOf[0].webTestName, data.alertContext.condition.allOf[0].metricName] | join('/', @)"
      - field: status
        expr: "get_field({Fired: 'ABNORMAL', Resolved: 'RECOVERED'}, data.essentials.monitorCondition)"
      - field: severity
        expr: "get_field({Sev0: '1', Sev1: '1', Sev2: '2', Sev3: '3', Sev4: '3'}, data.essentials.severity) || '1'"
      - field: bk_biz_id
        expr: "'{{plugin_inst_biz_id}}'"
      - field: dimensions
        expr: "zip(data.alertContext.condition.allOf[0].dimensions[*].name, data.alertContext.condition.allOf[0].dimensions[*].value)"
      - field: tags
        expr: "data.alertContext.properties || {source: 'AZURE_CLOUD'}"
  - rules:
    - key: __http_query_params__.source
      method: eq
      condition: and
      value:
        - "azure"
    - key: data.essentials.monitoringService
      method: eq
      value:
        - "Log Analytics"
        - "Application Insights"
    normalization_config:
      - field: alert_name
        expr: data.essentials.alertRule
      - field: event_id
        expr: "['azure_cloud_alert_{{plugin_inst_biz_id}}', data.essentials.originAlertId] | join('.', @)"
      - field: description
        expr: data.essentials.description
      - field: metric
        expr: "data.alertContext.SearchQuery"
      - field: status
        expr: "get_field({Fired: 'ABNORMAL', Resolved: 'RECOVERED'}, data.essentials.monitorCondition)"
      - field: severity
        expr: "get_field({Sev0: '1', Sev1: '1', Sev2: '2', Sev3: '3', Sev4: '3'}, data.essentials.severity) || '1'"
      - field: bk_biz_id
        expr: "'{{plugin_inst_biz_id}}'"
      - field: dimensions
        expr: "zip(data.alertContext.Dimensions[*].name, data.alertContext.Dimensions[*].value)"
      - field: tags
        expr: "data.customProperties || {source: 'AZURE_CLOUD'}"
  - rules:
    - key: __http_query_params__.source
      method: eq
      condition: and
      value:
        - "azure"
    - key: data.essentials.monitoringService
      method: eq
      value:
        - "Log Alerts V2"
    normalization_config:
      - field: alert_name
        expr: data.essentials.alertRule
      - field: event_id
        expr: "['azure_cloud_alert_{{plugin_inst_biz_id}}', data.essentials.originAlertId] | join('.', @)"
      - field: description
        expr: data.essentials.description
      - field: metric
        expr: "data.alertContext.condition.allOf[*].SearchQuery"
      - field: status
        expr: "get_field({Fired: 'ABNORMAL', Resolved: 'RECOVERED'}, data.essentials.monitorCondition)"
      - field: severity
        expr: "get_field({Sev0: '1', Sev1: '1', Sev2: '2', Sev3: '3', Sev4: '3'}, data.essentials.severity) || '1'"
      - field: bk_biz_id
        expr: "'{{plugin_inst_biz_id}}'"
      - field: dimensions
        expr: "zip(data.alertContext.condition.allOf[0].dimensions[*].name, data.alertContext.condition.allOf[0].dimensions[*].value)"
      - field: tags
        expr: "data.customProperties || {source: 'AZURE_CLOUD'}"
  - rules:
    - key: __http_query_params__.source
      condition: and
      method: eq
      value:
        - "azure"
    - key: data.essentials.monitoringService
      method: eq
      value:
        - "Activity Log - Administrative"
    normalization_config:
      - field: alert_name
        expr: data.essentials.alertRule
      - field: event_id
        expr: "['azure_cloud_alert_{{plugin_inst_biz_id}}', data.essentials.originAlertId] | join('.', @)"
      - field: description
        expr: data.essentials.description
      - field: metric
        expr: data.essentials.monitoringService
      - field: status
        expr: "get_field({Fired: 'ABNORMAL', Resolved: 'RECOVERED'}, data.essentials.monitorCondition)"
      - field: severity
        expr: "get_field({Sev0: '1', Sev1: '1', Sev2: '2', Sev3: '3', Sev4: '3'}, data.essentials.severity) || '1'"
      - field: bk_biz_id
        expr: "'{{plugin_inst_biz_id}}'"
      - field: dimensions
        expr: "data.alertContext.authorization"
      - field: tags
        expr: "data.customProperties || {source: 'AZURE_CLOUD'}"
  - rules:
    - key: __http_query_params__.source
      method: eq
      condition: and
      value:
        - "azure"
    - key: data.essentials.monitoringService
      method: eq
      value:
        - "Activity Log - Policy"
    normalization_config:
      - field: alert_name
        expr: data.essentials.alertRule
      - field: event_id
        expr: "['azure_cloud_alert_{{plugin_inst_biz_id}}', data.essentials.originAlertId] | join('.', @)"
      - field: description
        expr: data.alertContext.properties.description
      - field: metric
        expr: data.essentials.monitoringService
      - field: status
        expr: "get_field({Fired: 'ABNORMAL', Resolved: 'RECOVERED'}, data.essentials.monitorCondition)"
      - field: severity
        expr: "get_field({Sev0: '1', Sev1: '1', Sev2: '2', Sev3: '3', Sev4: '3'}, data.essentials.severity) || '1'"
      - field: bk_biz_id
        expr: "'{{plugin_inst_biz_id}}'"
      - field: dimensions
        expr: data.alertContext.authorization
      - field: tags
        expr: data.alertContext.properties
  - rules:
    - key: __http_query_params__.source
      method: eq
      condition: and
      value:
        - "azure"
    - key: data.essentials.monitoringService
      method: eq
      value:
        - "Activity Log - Autoscale"
    normalization_config:
      - field: alert_name
        expr: data.essentials.alertRule
      - field: event_id
        expr: "['azure_cloud_alert_{{plugin_inst_biz_id}}', data.essentials.originAlertId] | join('.', @)"
      - field: description
        expr: data.alertContext.properties.description
      - field: metric
        expr: data.essentials.monitoringService
      - field: status
        expr: "get_field({Fired: 'ABNORMAL', Resolved: 'RECOVERED'}, data.essentials.monitorCondition)"
      - field: severity
        expr: "get_field({Sev0: '1', Sev1: '1', Sev2: '2', Sev3: '3', Sev4: '3'}, data.essentials.severity) || '1'"
      - field: bk_biz_id
        expr: "'{{plugin_inst_biz_id}}'"
      - field: dimensions
        expr: "{resourceName: data.alertContext.properties.resourceName}"
      - field: tags
        expr: data.alertContext.properties
  - rules:
    - key: __http_query_params__.source
      method: eq
      condition: and
      value:
        - "azure"
    - key: data.essentials.monitoringService
      method: eq
      value:
        - "Activity Log - Security"
    normalization_config:
      - field: alert_name
        expr: data.essentials.alertRule
      - field: event_id
        expr: "['azure_cloud_alert_{{plugin_inst_biz_id}}', data.essentials.originAlertId] | join('.', @)"
      - field: description
        expr: "['category:', data.alertContext.properties.category, 'threatID:', data.alertContext.properties.threatID, 'protectionType:', data.alertContext.properties.protectionType, 'severity:', data.alertContext.properties.severity, 'actionTaken:', data.alertContext.properties.actionTaken, 'protectionType:', data.alertContext.properties.protectionType, 'compromisedEntity:', data.alertContext.properties.compromisedEntity, 'attackedResourceType:', data.alertContext.properties.attackedResourceType] | join(' ', @)"
      - field: metric
        expr: data.essentials.monitoringService
      - field: status
        expr: "get_field({Fired: 'ABNORMAL', Resolved: 'RECOVERED'}, data.essentials.monitorCondition)"
      - field: severity
        expr: "get_field({Sev0: '1', Sev1: '1', Sev2: '2', Sev3: '3', Sev4: '3'}, data.essentials.severity) || '1'"
      - field: bk_biz_id
        expr: "'{{plugin_inst_biz_id}}'"
      - field: dimensions
        expr: |
          {
            category: data.alertContext.properties.category,
            threatID: data.alertContext.properties.threatID,
            protectionType: data.alertContext.properties.protectionType,
            severity: data.alertContext.properties.severity,
            actionTaken: data.alertContext.properties.actionTaken,
            protectionType: data.alertContext.properties.protectionType,
            compromisedEntity: data.alertContext.properties.compromisedEntity,
            attackedResourceType: data.alertContext.properties.attackedResourceType
          }
      - field: tags
        expr: data.alertContext.properties
  - rules:
    - key: __http_query_params__.source
      method: eq
      condition: and
      value:
        - "azure"
    - key: data.essentials.monitoringService
      method: eq
      value:
        - "ServiceHealth"
    normalization_config:
      - field: alert_name
        expr: data.essentials.alertRule
      - field: event_id
        expr: "['azure_cloud_alert_{{plugin_inst_biz_id}}', data.essentials.originAlertId] | join('.', @)"
      - field: description
        expr: data.alertContext.properties.title
      - field: metric
        expr: data.essentials.monitoringService
      - field: status
        expr: "get_field({Fired: 'ABNORMAL', Resolved: 'RECOVERED'}, data.essentials.monitorCondition)"
      - field: severity
        expr: "get_field({Sev0: '1', Sev1: '1', Sev2: '2', Sev3: '3', Sev4: '3'}, data.essentials.severity) || '1'"
      - field: bk_biz_id
        expr: "'{{plugin_inst_biz_id}}'"
      - field: dimensions
        expr: |
          {
            service: data.alertContext.properties.service,
            region: data.alertContext.properties.region,
            incidentType: data.alertContext.properties.incidentType
          }
      - field: tags
        expr: data.alertContext.properties
`

var eventDataStr = `
    "schemaId": "azureMonitorCommonAlertSchema",
    "data": {
        "essentials": {
            "alertId": "/subscriptions/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx/providers/Microsoft.AlertsManagement/alerts/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxx",
            "alertRule": "default",
            "severity": "Sev3",
            "signalType": "Metric",
            "monitorCondition": "Fired",
            "monitoringService": "Platform",
            "alertTargetIDs": [
                "/subscriptions/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx/resourcegroups/default/providers/microsoft.compute/virtualmachines/xxx"
            ],
            "configurationItems": ["lai"],
            "originAlertId": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx_default_microsoft.insights_metricAlerts_default_-xxxxxxxxxx",
            "firedDateTime": "2024-06-25T03:01:54.7647341Z",
            "description": "",
            "essentialsVersion": "1.0",
            "alertContextVersion": "1.0",
            "investigationLink": "https://portal.azure.com/#view/Microsoft_Azure_Monitoring_Alerts/Investigation.ReactView/alertId/%2fsubscriptions%2fxxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx%2fresourceGroups%2fdefault%2fproviders%2fMicrosoft.AlertsManagement%2falerts%2fxxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
        },
        "alertContext": {
            "properties": {
                "aaa": "aaa",
                "bbb": "bbb"
            },
            "conditionType": "MultipleResourceMultipleMetricCriteria",
            "condition": {
                "windowSize": "PT5M",
                "allOf": [
                    {
                        "metricName": "Available Memory Bytes",
                        "metricNamespace": "Microsoft.Compute/virtualMachines",
                        "operator": "GreaterThan",
                        "threshold": "0",
                        "timeAggregation": "Average",
                        "dimensions": [],
                        "metricValue": 480142950.4,
                        "webTestName": null
                    }
                ],
                "windowStartTime": "2024-06-25T02:54:46.281Z",
                "windowEndTime": "2024-06-25T02:59:46.281Z"
            }
        },
        "customProperties": {
            "aaa": "aaa",
            "bbb": "bbb"
        }
    },
`

func (s *AlertTest) TestCleanConfig() {
	pipelineConfig := `{
		"etl_config":"bk_fta_event",
		"option":{
			"clean_configs": %s,
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
	}`

	var cleanConfig map[string]interface{}
	cleanConfigStr = strings.Replace(cleanConfigStr, "{{plugin_inst_biz_id}}", "2", -1)
	err := yaml.Unmarshal([]byte(cleanConfigStr), &cleanConfig)
	if err != nil {
		s.T().Fatal(err)
	}

	cleanConfigs, _ := json.Marshal(cleanConfig["clean_configs"])
	pipelineConfig = fmt.Sprintf(pipelineConfig, cleanConfigs)
	s.CTX = testsuite.PipelineConfigStringInfoContext(s.CTX, s.PipelineConfig, pipelineConfig)

	processor, err := NewAlertFTAProcessor(s.CTX, "test")
	s.NoError(err)

	if processor == nil {
		s.T().Fatal("processor is nil")
	}

	eventDataShell := `
{
	"bk_data_id": 1572956,
	"bk_plugin_id": "rest_api",
	"bk_agent_id": "",
	"ip": "127.0.0.1",
	"hostname": "VM-68-183-centos",
	"dataid": 1572956,
	"bizid": 0,
	"cloudid": 0,
	"gseindex": 36,
	"bk_host_id": 0,
	"bk_ingest_time": 1703843214,
	"data": {
		"__http_query_params__": {
			"source": "azure"
		},
        %s
		"__http_headers__": {
			"User-Agent": "curl/7.29.0",
			"Accept": "*/*",
			"Content-Type": "application/json",
			"Content-Length": "2662",
			"Expect": "100-continue"
		}
	},
	"__bk_event_id__": "089c54d0-6331-4e8d-99bc-0f33bda0ecbd"
}`
	eventData := fmt.Sprintf(eventDataShell, eventDataStr)
	s.Run(
		eventData,
		processor,
		func(result map[string]interface{}) {
			if result == nil {
				s.T().Fatal("result is nil")
			}
			resultStr, err := json.Marshal(result)
			s.NoError(err)
			s.T().Logf("result: %s", resultStr)
		},
	)
}

func TestAlertTest(t *testing.T) {
	suite.Run(t, new(AlertTest))
}
