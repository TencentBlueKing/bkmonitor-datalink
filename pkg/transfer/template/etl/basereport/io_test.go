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

// SysIOTest
type SysIOTest struct {
	testsuite.ETLSuite
}

//go:embed fixture/io_test_data.json
var ioData string

// TestUsage :
func (s *SysIOTest) TestUsage() {
	s.RunN(3,
		ioData,
		basereport.NewPerformanceIoProcessor(s.CTX, "test"),
		func(result map[string]interface{}) {
			var deviceName string
			if deviceValue, ok := result["dimensions"].(map[string]interface{})["device_name"].(string); ok {
				deviceName = deviceValue
			} else {
				panic(fmt.Errorf("convert fail, no device_name found in %v", result))
			}

			s.EqualRecord(result, map[string]interface{}{
				"vda1": map[string]interface{}{
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
						"device_name":        "vda1",
						"bk_cmdb_level":      "[{\"a\":1},{\"b\":2}]",
					},
					"metrics": map[string]interface{}{
						"avgqu_sz": 0.5578246945728673,
						"avgrq_sz": 20.530465949820787,
						"await":    23.9921146953405,
						"r_s":      0.0,
						"rkb_s":    0.0,
						"svctm":    2.670967741935484,
						"util":     0.06210089372190695,
						"w_s":      23.250334606027966,
						"wkb_s":    238.67010147549854,
					},
					"time": 1551940933,
				},
				"vda": map[string]interface{}{
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
						"device_name":        "vda",
						"bk_cmdb_level":      "[{\"a\":1},{\"b\":2}]",
					},
					"metrics": map[string]interface{}{
						"avgqu_sz": 0.5683415125917947,
						"avgrq_sz": 16.709451575262545,
						"await":    19.894982497082847,
						"r_s":      0.0,
						"rkb_s":    0.0,
						"svctm":    2.5029171528588097,
						"util":     0.07150102900348385,
						"w_s":      28.567077788338302,
						"wkb_s":    238.67010147549854,
					},
					"time": 1551940933,
				},
				"sr0": map[string]interface{}{
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
						"device_name":        "sr0",
						"bk_cmdb_level":      "[{\"a\":1},{\"b\":2}]",
					},
					"metrics": map[string]interface{}{
						"avgqu_sz": 0.0,
						"avgrq_sz": 0.0,
						"await":    0.0,
						"r_s":      0.0,
						"rkb_s":    0.0,
						"svctm":    0.0,
						"util":     0.0,
						"w_s":      0.0,
						"wkb_s":    0.0,
					},
					"time": 1551940933,
				},
			}[deviceName].(map[string]interface{}))
		},
	)
}

func (s *SysIOTest) TestDisabledBizIDs() {
	processor := basereport.NewPerformanceIoProcessor(s.CTX, "test")
	processor.DisabledBizIDs = map[string]struct{}{"0": {}}
	s.RunN(0,
		ioData,
		processor,
		func(result map[string]interface{}) {},
	)
}

// TestSysIOTest :
func TestSysIOTest(t *testing.T) {
	suite.Run(t, new(SysIOTest))
}

// BenchmarkPerformanceIoProcessor_Process
func BenchmarkPerformanceIoProcessor_Process(b *testing.B) {
	testsuite.ETLBenchmarkTest(b, func(ctx context.Context, name string) define.DataProcessor {
		return basereport.NewPerformanceIoProcessor(ctx, name)
	}, []byte(ioData))
}
