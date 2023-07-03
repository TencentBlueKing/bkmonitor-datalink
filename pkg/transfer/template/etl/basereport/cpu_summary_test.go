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
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/basereport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// SysCPUSummaryTest
type SysCPUSummaryTest struct {
	testsuite.ETLSuite
}

//go:embed fixture/cpu_summary_test_data.json
var cpuSummaryData string

// TestUsage :
func (s *SysCPUSummaryTest) TestUsage() {
	s.Run(
		cpuSummaryData,
		basereport.NewCPUSummaryProcessor(s.CTX, "test"),
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
					"hostname":           "rbtnode1-new",
					"device_name":        "cpu-total",
					"bk_cmdb_level":      "[{\"a\":1},{\"b\":2}]",
				},
				"metrics": map[string]interface{}{
					"idle":      0.3031411786454213,
					"iowait":    0.006046627345253356,
					"system":    0.10623802299487654,
					"usage":     65.55378017024681,
					"user":      0.5816949598457978,
					"stolen":    0.0,
					"nice":      4.1687890130876494e-07,
					"interrupt": 0.000430660022013187,
					"guest":     0.0,
					"softirq":   0.002448134267736383,
				},
				"time": 1553417593,
			})
		},
	)
}

func (s *SysCPUSummaryTest) TestDisabledBizIDs() {
	processor := basereport.NewCPUSummaryProcessor(s.CTX, "test")
	processor.DisabledBizIDs = map[string]struct{}{"0": {}}
	s.RunN(0,
		cpuSummaryData,
		processor,
		func(result map[string]interface{}) {},
	)
}

// TestSysCPUSummaryTest :
func TestSysCPUSummaryTest(t *testing.T) {
	suite.Run(t, new(SysCPUSummaryTest))
}

// BenchmarkCPUSummaryProcessor_Process
func BenchmarkCPUSummaryProcessor_Process(b *testing.B) {
	testsuite.ETLBenchmarkTest(b, func(ctx context.Context, name string) define.DataProcessor {
		return basereport.NewCPUSummaryProcessor(ctx, name)
	}, []byte(cpuSummaryData))
}
