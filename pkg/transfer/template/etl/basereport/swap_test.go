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

// SysSwapTest
type SysSwapTest struct {
	testsuite.ETLSuite
}

//go:embed fixture/swap_test_data.json
var swapData string

// TestUsage :
func (s *SysSwapTest) TestUsage() {
	s.Run(
		swapData,
		basereport.NewSwapProcessor(s.CTX, "test"),
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
				},
				"metrics": map[string]interface{}{
					"free":     0.0,
					"pct_used": 0.0,
					"total":    0.0,
					"used":     0.0,
					"swap_in":  0.0,
					"swap_out": 0.0,
				},
				"time": 1551940933,
			})
		},
	)
}

func (s *SysSwapTest) TestDisabledBizIDs() {
	processor := basereport.NewSwapProcessor(s.CTX, "test")
	processor.DisabledBizIDs = map[string]struct{}{"0": {}}
	s.RunN(0,
		swapData,
		processor,
		func(result map[string]interface{}) {},
	)
}

// TestSysSwapTest :
func TestSysSwapTest(t *testing.T) {
	suite.Run(t, new(SysSwapTest))
}

// BenchmarkSwapProcessor_Process:
func BenchmarkSwapProcessor_Process(b *testing.B) {
	testsuite.ETLBenchmarkTest(b, func(ctx context.Context, name string) define.DataProcessor {
		return basereport.NewSwapProcessor(ctx, name)
	}, []byte(swapData))
}
