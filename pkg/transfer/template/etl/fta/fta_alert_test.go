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
		s.CTX, s.PipelineConfig, `{
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
				]
			}
		}`,
	)

	processor, err := fta.NewAlertFTAProcessor(s.CTX, "test")

	s.NoError(err)
	s.Run(`{"event": {"name": "CPU 使用率告警"}, "metric_id": "system.cpu.usage", "alert_name": "xxx"}`,
		processor,
		func(result map[string]interface{}) {
			s.MapEqual(map[string]interface{}{
				"event": map[string]interface{}{
					"name": "CPU 使用率告警",
				},
				"metric_id":         "system.cpu.usage",
				"__bk_alert_name__": "CPU Usage",
				"alert_name":        "xxx",
			}, result)
		},
	)

	s.Run(`{"event": {"name": "内存使用率告警"}, "metric_id": "system.mem.usage", "alert_name": "xxx"}`,
		processor,
		func(result map[string]interface{}) {
			s.MapEqual(map[string]interface{}{
				"event": map[string]interface{}{
					"name": "内存使用率告警",
				},
				"metric_id":         "system.mem.usage",
				"__bk_alert_name__": "MEM Usage",
				"alert_name":        "xxx",
			}, result)
		},
	)

	s.Run(`{"event": {"name": "内存使用率告警"}, "metric_id": "system.mem"}`,
		processor,
		func(result map[string]interface{}) {
			s.MapEqual(map[string]interface{}{
				"event": map[string]interface{}{
					"name": "内存使用率告警",
				},
				"metric_id":         "system.mem",
				"__bk_alert_name__": "Default",
			}, result)
		},
	)
}

// TestAlertFTATest :
func TestAlertFTATest(t *testing.T) {
	suite.Run(t, new(AlertFTATest))
}
