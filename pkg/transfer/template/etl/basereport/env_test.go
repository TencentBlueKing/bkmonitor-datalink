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

// SysEnvTest
type SysEnvTest struct {
	testsuite.ETLSuite
}

//go:embed fixture/env_test_data.json
var envData string

// TestUsage :
func (s *SysEnvTest) TestUsage() {
	s.Run(
		envData,
		basereport.NewEnvProcessor(s.CTX, "test"),
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
					"city":               "",
					"timezone":           "8",
					"bk_cmdb_level":      "[{\"a\":1},{\"b\":2}]",
				},
				"metrics": map[string]interface{}{
					"procs":                 375.0,
					"uptime":                1442845.0,
					"login_user":            8.0,
					"maxfiles":              3248324.0,
					"proc_running_current":  2.0,
					"procs_blocked_current": 0.0,
					"procs_ctxt_total":      403319496256.0,
					"procs_processes_total": 4702930008.0,
					"uname":                 "Linux version 3.10.0-693.el7.x86_64 (builder@kbuilder.dev.centos.org) (gcc version 4.8.5 20150623 (Red Hat 4.8.5-16) (GCC) ) #1 SMP Tue Aug 22 21:09:27 UTC 2017\n",
				},
				"time": 1551940933,
			})
		},
	)
}

func (s *SysEnvTest) TestDisabledBizIDs() {
	processor := basereport.NewEnvProcessor(s.CTX, "test")
	processor.DisabledBizIDs = map[string]struct{}{"0": {}}
	s.RunN(0,
		envData,
		processor,
		func(result map[string]interface{}) {},
	)
}

// TestSysEnvTest :
func TestSysEnvTest(t *testing.T) {
	suite.Run(t, new(SysEnvTest))
}

// BenchmarkEnvProcessor_Process
func BenchmarkEnvProcessor_Process(b *testing.B) {
	testsuite.ETLBenchmarkTest(b, func(ctx context.Context, name string) define.DataProcessor {
		return basereport.NewEnvProcessor(ctx, name)
	}, []byte(envData))
}
