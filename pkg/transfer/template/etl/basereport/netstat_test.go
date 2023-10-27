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

// SysNetStatTest
type SysNetStatTest struct {
	testsuite.ETLSuite
}

//go:embed fixture/netstat_test_data.json
var netstatData string

// TestUsage :
func (s *SysNetStatTest) TestUsage() {
	s.Run(
		netstatData,
		basereport.NewNetStatProcessor(s.CTX, "test"),
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
					"bk_cmdb_level":      "null",
				},
				"metrics": map[string]interface{}{
					"cur_tcp_closed":       0.0,
					"cur_tcp_closewait":    28.0,
					"cur_tcp_closing":      0.0,
					"cur_tcp_estab":        2125.0,
					"cur_tcp_finwait1":     0.0,
					"cur_tcp_finwait2":     0.0,
					"cur_tcp_lastack":      0.0,
					"cur_tcp_listen":       38.0,
					"cur_tcp_syn_recv":     0.0,
					"cur_tcp_syn_sent":     0.0,
					"cur_tcp_timewait":     684.0,
					"cur_udp_indatagrams":  1710.0,
					"cur_udp_outdatagrams": 1710.0,
				},
				"time": 1551940933,
			})
		},
	)
}

func (s *SysNetStatTest) TestDisabledBizIDs() {
	processor := basereport.NewNetStatProcessor(s.CTX, "test")
	processor.DisabledBizIDs = map[string]struct{}{"0": {}}
	s.RunN(0,
		netstatData,
		processor,
		func(result map[string]interface{}) {},
	)
}

// TestSysNetStatTest :
func TestSysNetStatTest(t *testing.T) {
	suite.Run(t, new(SysNetStatTest))
}

// BenchmarkNetStatProcessor_Process :
func BenchmarkNetStatProcessor_Process(b *testing.B) {
	testsuite.ETLBenchmarkTest(b, func(ctx context.Context, name string) define.DataProcessor {
		return basereport.NewNetStatProcessor(ctx, name)
	}, []byte(netstatData))
}
