// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package basereport_test

import (
	"context"
	_ "embed"
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/basereport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// SysNetTest
type SysNetTest struct {
	testsuite.ETLSuite
}

//go:embed fixture/net_test_data.json
var netData string

// TestUsage :
func (s *SysNetTest) TestUsage() {
	s.RunN(2,
		netData,
		basereport.NewPerformanceNetProcessor(s.CTX, "test"),
		func(result map[string]interface{}) {
			var deviceName string
			if deviceValue, ok := result["dimensions"].(map[string]interface{})["device_name"].(string); ok {
				deviceName = deviceValue
			} else {
				panic(fmt.Errorf("convert fail, no device_name found in %v", result))
			}
			s.EqualRecord(result, map[string]interface{}{
				"eth0": map[string]interface{}{
					"dimensions": map[string]interface{}{
						"ip":                 "127.0.0.1",
						"bk_target_ip":       "127.0.0.1",
						"bk_supplier_id":     "0",
						"bk_target_cloud_id": "0",
						"bk_cloud_id":        "0",
						"bk_agent_id":        "010000525400c48bdc1670385834306k",
						"bk_biz_id":          "2",
						"bk_host_id":         "30145",
						"bk_target_host_id":  "30145",
						"hostname":           "rbtnode1-new",
						"device_name":        "eth0",
						"bk_cmdb_level":      "null",
					},
					"metrics": map[string]interface{}{
						"speed_packets_recv": 1691.0,
						"speed_packets_sent": 1090.0,
						"speed_recv":         279069.0,
						"speed_sent":         318053.0,
						"speed_recv_bit":     279069.0 * 8.0,
						"speed_sent_bit":     318053.0 * 8.0,
						"packets_recv":       1906182739.0,
						"packets_sent":       1177723736.0,
						"bytes_recv":         220277678108.0,
						"bytes_sent":         322593864158.0,
						"errors":             0.0,
						"dropped":            0.0,
						"overruns":           0.0,
						"carrier":            0.0,
						"collisions":         0.0,
					},
					"time": 1551940933,
				},
				"lo": map[string]interface{}{
					"dimensions": map[string]interface{}{
						"ip":                 "127.0.0.1",
						"bk_target_ip":       "127.0.0.1",
						"bk_supplier_id":     "0",
						"bk_target_cloud_id": "0",
						"bk_cloud_id":        "0",
						"bk_agent_id":        "010000525400c48bdc1670385834306k",
						"bk_biz_id":          "2",
						"bk_host_id":         "30145",
						"bk_target_host_id":  "30145",
						"hostname":           "rbtnode1-new",
						"device_name":        "lo",
						"bk_cmdb_level":      "null",
					},
					"metrics": map[string]interface{}{
						"speed_sent":         177720.0,
						"speed_packets_recv": 786.0,
						"speed_packets_sent": 786.0,
						"packets_recv":       727703240.0,
						"packets_sent":       727703240.0,
						"speed_recv_bit":     177720.0 * 8.0,
						"speed_sent_bit":     177720.0 * 8.0,
						"bytes_recv":         91844333420.0,
						"bytes_sent":         91844333420.0,
						"speed_recv":         177720.0,
						"errors":             0.0,
						"dropped":            0.0,
						"overruns":           0.0,
						"carrier":            0.0,
						"collisions":         0.0,
					},
					"time": 1551940933,
				},
			}[deviceName].(map[string]interface{}))
		},
	)
}

func (s *SysNetTest) TestDisabledBizIDs() {
	processor := basereport.NewPerformanceNetProcessor(s.CTX, "test")
	processor.DisabledBizIDs = map[string]struct{}{"0": {}}
	s.RunN(0,
		netData,
		processor,
		func(result map[string]interface{}) {},
	)
}

// TestSysNetTest :
func TestSysNetTest(t *testing.T) {
	suite.Run(t, new(SysNetTest))
}

// BenchmarkPerformanceNetProcessor_Process
func BenchmarkPerformanceNetProcessor_Process(b *testing.B) {
	testsuite.ETLBenchmarkTest(b, func(ctx context.Context, name string) define.DataProcessor {
		return basereport.NewPerformanceNetProcessor(ctx, name)
	}, []byte(netData))
}
