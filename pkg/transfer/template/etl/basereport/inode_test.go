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

// SysInodeTest :
type SysInodeTest struct {
	testsuite.ETLSuite
}

//go:embed fixture/inode_test_data_linux.json
var inodeLinuxData string

// TestForLinux :
func (s *SysInodeTest) TestForLinux() {
	s.Run(
		inodeLinuxData,
		basereport.NewPerformanceInodeProcessor(s.CTX, "test"),
		func(result map[string]interface{}) {
			var deviceName string
			if deviceValue, ok := result["dimensions"].(map[string]interface{})["device_name"].(string); ok {
				deviceName = deviceValue
			} else {
				panic(fmt.Errorf("convert fail, no device_name found in %v", result))
			}
			s.EqualRecord(result, map[string]interface{}{
				"/dev/vda1": map[string]interface{}{
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
						"device_name":        "/dev/vda1",
						"bk_cmdb_level":      "[{\"a\":1},{\"b\":2}]",
					},
					"metrics": map[string]interface{}{
						"total":  3276800.0,
						"free":   3124742.0,
						"used":   152058.0,
						"in_use": 4.64044189453125,
					},
					"time": 1551940933,
				},
			}[deviceName].(map[string]interface{}))
		},
	)
}

func (s *SysInodeTest) TestDisabledBizIDs() {
	processor := basereport.NewPerformanceInodeProcessor(s.CTX, "test")
	processor.DisabledBizIDs = map[string]struct{}{"0": {}}
	s.RunN(0,
		inodeLinuxData,
		processor,
		func(result map[string]interface{}) {},
	)
}

//go:embed fixture/inode_test_data_windows.json
var inodeWindowsData string

// TestForWindows :
func (s *SysInodeTest) TestForWindows() {
	s.Run(
		inodeWindowsData,
		basereport.NewPerformanceInodeProcessor(s.CTX, "test"),
		func(result map[string]interface{}) {
			s.EqualRecord(result, map[string]interface{}{
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
					"hostname":           "10_0_1_9",
					"device_name":        "C:",
					"bk_cmdb_level":      "null",
				},
				"metrics": map[string]interface{}{
					"total":  0.0,
					"free":   0.0,
					"used":   0.0,
					"in_use": nil,
				},
				"time": 1555397366,
			})
		},
	)
}

// TestSysInodeTest :
func TestSysInodeTest(t *testing.T) {
	suite.Run(t, new(SysInodeTest))
}

// BenchmarkPerformanceInodeProcessor_Process :
func BenchmarkPerformanceInodeProcessor_Process(b *testing.B) {
	testsuite.ETLBenchmarkTest(b, func(ctx context.Context, name string) define.DataProcessor {
		return basereport.NewPerformanceInodeProcessor(ctx, name)
	}, []byte(inodeLinuxData))
}
