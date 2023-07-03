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

// SysCPUDetailTest
type SysCPUDetailTest struct {
	testsuite.ETLSuite
}

//go:embed fixture/cpu_detail_test_data.json
var cpuDetailData string

// TestUsage :
func (s *SysCPUDetailTest) TestUsage() {
	s.RunN(8,
		cpuDetailData,
		basereport.NewPerformanceCPUDetailProcessor(s.CTX, "test"),
		func(result map[string]interface{}) {
			var deviceName string
			if deviceValue, ok := result["dimensions"].(map[string]interface{})["device_name"].(string); ok {
				deviceName = deviceValue
			} else {
				panic(fmt.Errorf("convert fail, no device_name found in %v", result))
			}

			s.EqualRecord(result, map[string]interface{}{
				"cpu0": map[string]interface{}{
					"dimensions": map[string]interface{}{
						"ip":                 "127.0.0.1",
						"bk_target_ip":       "127.0.0.1",
						"bk_supplier_id":     "0",
						"bk_cloud_id":        "0",
						"bk_target_cloud_id": "0",
						"bk_agent_id":        "010000525400c48bdc1670385834306k",
						"bk_biz_id":          "2",
						"bk_host_id":         "30145",
						"bk_target_host_id":  "30145",
						"hostname":           "rbtnode1-new",
						"device_name":        "cpu0",
						"bk_cmdb_level":      "[{\"a\":1},{\"b\":2}]",
					},
					"metrics": map[string]interface{}{
						"idle":      0.30072991450387343,
						"iowait":    0.012733993507811724,
						"system":    0.10659980548664424,
						"usage":     62.17127444281228,
						"user":      0.5770452836715144,
						"stolen":    0.0,
						"interrupt": 0.00034466183951676206,
						"guest":     0.0,
						"nice":      7.582560469368765e-07,
						"softirq":   0.00254558273459253,
					},
					"time": 1553417593,
				},
				"cpu7": map[string]interface{}{
					"dimensions": map[string]interface{}{
						"ip":                 "127.0.0.1",
						"bk_target_ip":       "127.0.0.1",
						"bk_supplier_id":     "0",
						"bk_cloud_id":        "0",
						"bk_target_cloud_id": "0",
						"bk_agent_id":        "010000525400c48bdc1670385834306k",
						"bk_biz_id":          "2",
						"bk_host_id":         "30145",
						"bk_target_host_id":  "30145",
						"hostname":           "rbtnode1-new",
						"device_name":        "cpu7",
						"bk_cmdb_level":      "[{\"a\":1},{\"b\":2}]",
					},
					"metrics": map[string]interface{}{
						"idle":      0.31108339509285143,
						"iowait":    0.0035461899061101925,
						"system":    0.10613581868146613,
						"usage":     59.06945380961094,
						"user":      0.5767746086709588,
						"stolen":    0.0,
						"interrupt": 0.0,
						"guest":     0.0,
						"nice":      2.895583351202357e-07,
						"softirq":   0.0024596980902783254,
					},
					"time": 1553417593,
				},
				"cpu6": map[string]interface{}{
					"dimensions": map[string]interface{}{
						"ip":                 "127.0.0.1",
						"bk_target_ip":       "127.0.0.1",
						"bk_supplier_id":     "0",
						"bk_cloud_id":        "0",
						"bk_agent_id":        "010000525400c48bdc1670385834306k",
						"bk_biz_id":          "2",
						"bk_host_id":         "30145",
						"bk_target_host_id":  "30145",
						"bk_target_cloud_id": "0",
						"hostname":           "rbtnode1-new",
						"device_name":        "cpu6",
						"bk_cmdb_level":      "[{\"a\":1},{\"b\":2}]",
					},
					"metrics": map[string]interface{}{
						"idle":      0.30552519181953863,
						"iowait":    0.0037474266206700443,
						"system":    0.1062760005944224,
						"usage":     68.07795698900756,
						"user":      0.5820524817289869,
						"stolen":    0.0,
						"interrupt": 0.0,
						"guest":     0.0,
						"nice":      3.136302558046257e-07,
						"softirq":   0.0023985856061262095,
					},
					"time": 1553417593,
				},
				"cpu5": map[string]interface{}{
					"dimensions": map[string]interface{}{
						"ip":                 "127.0.0.1",
						"bk_target_ip":       "127.0.0.1",
						"bk_supplier_id":     "0",
						"bk_cloud_id":        "0",
						"bk_agent_id":        "010000525400c48bdc1670385834306k",
						"bk_biz_id":          "2",
						"bk_host_id":         "30145",
						"bk_target_host_id":  "30145",
						"bk_target_cloud_id": "0",
						"hostname":           "rbtnode1-new",
						"device_name":        "cpu5",
						"bk_cmdb_level":      "[{\"a\":1},{\"b\":2}]",
					},
					"metrics": map[string]interface{}{
						"idle":      0.3060685295203505,
						"iowait":    0.004138514713460164,
						"system":    0.10627398971186623,
						"usage":     68.90982503418932,
						"user":      0.5811025450001504,
						"stolen":    0.0,
						"interrupt": 0.0,
						"guest":     0.0,
						"nice":      4.2049949949874737e-07,
						"softirq":   0.002416000554673307,
					},
					"time": 1553417593,
				},
				"cpu4": map[string]interface{}{
					"dimensions": map[string]interface{}{
						"ip":                 "127.0.0.1",
						"bk_target_ip":       "127.0.0.1",
						"bk_supplier_id":     "0",
						"bk_cloud_id":        "0",
						"bk_target_cloud_id": "0",
						"bk_agent_id":        "010000525400c48bdc1670385834306k",
						"bk_biz_id":          "2",
						"bk_host_id":         "30145",
						"bk_target_host_id":  "30145",
						"hostname":           "rbtnode1-new",
						"device_name":        "cpu4",
						"bk_cmdb_level":      "[{\"a\":1},{\"b\":2}]",
					},
					"metrics": map[string]interface{}{
						"idle":      0.3027599819240099,
						"iowait":    0.004546414524356778,
						"system":    0.10630656471397247,
						"usage":     64.67828418154629,
						"user":      0.583965526443566,
						"stolen":    0.0,
						"interrupt": 0.0,
						"guest":     0.0,
						"nice":      2.895070027297959e-07,
						"softirq":   0.002421222887091903,
					},
					"time": 1553417593,
				},
				"cpu3": map[string]interface{}{
					"dimensions": map[string]interface{}{
						"ip":                 "127.0.0.1",
						"bk_target_ip":       "127.0.0.1",
						"bk_supplier_id":     "0",
						"bk_cloud_id":        "0",
						"bk_target_cloud_id": "0",
						"bk_agent_id":        "010000525400c48bdc1670385834306k",
						"bk_biz_id":          "2",
						"bk_host_id":         "30145",
						"bk_target_host_id":  "30145",
						"hostname":           "rbtnode1-new",
						"device_name":        "cpu3",
						"bk_cmdb_level":      "[{\"a\":1},{\"b\":2}]",
					},
					"metrics": map[string]interface{}{
						"idle":      0.3013574594946799,
						"iowait":    0.005227073482178070505,
						"system":    0.10623172407634046,
						"usage":     65.90450571652302,
						"user":      0.5847470817957443,
						"stolen":    0.0,
						"interrupt": 0.0,
						"guest":     0.0,
						"nice":      3.825641171773625e-07,
						"softirq":   0.002436278586939946,
					},
					"time": 1553417593,
				},
				"cpu2": map[string]interface{}{
					"dimensions": map[string]interface{}{
						"ip":                 "127.0.0.1",
						"bk_target_ip":       "127.0.0.1",
						"bk_supplier_id":     "0",
						"bk_cloud_id":        "0",
						"bk_target_cloud_id": "0",
						"bk_agent_id":        "010000525400c48bdc1670385834306k",
						"bk_biz_id":          "2",
						"bk_host_id":         "30145",
						"bk_target_host_id":  "30145",
						"hostname":           "rbtnode1-new",
						"device_name":        "cpu2",
						"bk_cmdb_level":      "[{\"a\":1},{\"b\":2}]",
					},
					"metrics": map[string]interface{}{
						"idle":      0.2997479024575093,
						"iowait":    0.0062638645299422525,
						"system":    0.10622886994724591,
						"usage":     70.37533512099486,
						"user":      0.5853170831466825,
						"stolen":    0.0,
						"interrupt": 0.0,
						"guest":     0.0,
						"nice":      2.44695670770784e-07,
						"softirq":   0.002442035222949239,
					},
					"time": 1553417593,
				},
				"cpu1": map[string]interface{}{
					"dimensions": map[string]interface{}{
						"ip":                 "127.0.0.1",
						"bk_target_ip":       "127.0.0.1",
						"bk_supplier_id":     "0",
						"bk_cloud_id":        "0",
						"bk_target_cloud_id": "0",
						"bk_agent_id":        "010000525400c48bdc1670385834306k",
						"bk_biz_id":          "2",
						"bk_host_id":         "30145",
						"bk_target_host_id":  "30145",
						"hostname":           "rbtnode1-new",
						"device_name":        "cpu1",
						"bk_cmdb_level":      "[{\"a\":1},{\"b\":2}]",
					},
					"metrics": map[string]interface{}{
						"idle":      0.29879891343992293,
						"iowait":    0.008187953366306405,
						"system":    0.10618094046957909,
						"usage":     65.29372045791891,
						"user":      0.5843583007522759,
						"stolen":    0.0,
						"interrupt": 0.0,
						"guest":     0.0,
						"nice":      6.307150690737258e-07,
						"softirq":   0.0024732612568465974,
					},
					"time": 1553417593,
				},
			}[deviceName].(map[string]interface{}))
		},
	)
}

func (s *SysCPUDetailTest) TestDisabledBizIDs() {
	processor := basereport.NewPerformanceCPUDetailProcessor(s.CTX, "test")
	processor.DisabledBizIDs = map[string]struct{}{"0": {}}
	s.RunN(0,
		cpuDetailData,
		processor,
		func(result map[string]interface{}) {},
	)
}

// TestSysCPUDetailTest :
func TestSysCPUDetailTest(t *testing.T) {
	suite.Run(t, new(SysCPUDetailTest))
}

// BenchmarkPerformanceCPUDetailProcessor_Process
func BenchmarkPerformanceCPUDetailProcessor_Process(b *testing.B) {
	testsuite.ETLBenchmarkTest(b, func(ctx context.Context, name string) define.DataProcessor {
		return basereport.NewPerformanceCPUDetailProcessor(ctx, name)
	}, []byte(cpuDetailData))
}
