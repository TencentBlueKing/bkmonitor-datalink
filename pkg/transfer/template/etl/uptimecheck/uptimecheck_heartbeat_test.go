// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uptimecheck_test

import (
	_ "embed"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/uptimecheck"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

type UptimecheckHeartbeatTest struct {
	testsuite.ETLSuite
}

//go:embed fixture/uptimecheck_heartbeat_test_data.json
var uptimecheckHeatbeatTestData string

//go:embed fixture/uptimecheck_heartbeat_test_consul_data.json
var uptimecheckHeatbeatTestConsulData string

// TestUsage :
func (s *UptimecheckHeartbeatTest) TestUsage() {
	s.CTX = testsuite.PipelineConfigStringInfoContext(s.CTX, s.PipelineConfig, uptimecheckHeatbeatTestConsulData)
	processor, err := uptimecheck.NewHeartbeatProcessor(s.CTX, "test")
	s.NoError(err)
	s.Run(
		uptimecheckHeatbeatTestData,
		processor,
		func(result map[string]interface{}) {
			s.EqualRecord(result, map[string]interface{}{
				"dimensions": map[string]interface{}{
					"ip":             "127.0.0.1",
					"bk_supplier_id": "0",
					"bk_cloud_id":    "2",
					"bk_agent_id":    "010000525400c48bdc1670385834306k",
					"bk_host_id":     "30145",
					"bk_biz_id":      2.0,
					"status":         "0",
					"node_id":        5.0,
					"version":        "1.4.7",
				},
				"metrics": map[string]interface{}{
					"reload":           0.0,
					"running_tasks":    3.0,
					"loaded_tasks":     3.0,
					"success":          0.0,
					"uptime":           4440018.0,
					"reload_timestamp": 1554090323.0,
					"fail":             0.0,
					"error":            0.0,
				},
				"time": 1554094763,
			})
		},
	)
}

// TestServletTest :
func TestUptimecheckHeartbeatTest(t *testing.T) {
	suite.Run(t, new(UptimecheckHeartbeatTest))
}
