// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build windows

package collector

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

var DefaultBasereportConfigWin = configs.BasereportConfig{
	Cpu: configs.CpuConfig{
		StatTimes:     4,
		InfoPeriod:    1 * time.Minute,
		InfoTimeout:   30 * time.Second,
		ReportCpuFlag: false,
	},
	ReportCrontab: false,
	ReportHosts:   false,
	ReportRoute:   false,
}

func TestGetCPUStatUsageWin(t *testing.T) {
	report := &CpuReport{}
	for i := 0; i <= 4; i++ {
		err := getCPUStatUsage(report)
		t.Log(report.TotalStat.Idle)
		t.Log(report.TotalStat.System)
		t.Log(report.TotalStat.User)
		assert.NoError(t, err)
		assert.NotNil(t, report.Stat)
		assert.NotNil(t, report.Usage)
		time.Sleep(1 * time.Second)
	}
}

func TestQueryCpuInfoWin(t *testing.T) {
	report := &CpuReport{}
	cfg := configs.FastBasereportConfig
	err := queryCpuInfo(report, cfg.Cpu.InfoPeriod, cfg.Cpu.InfoTimeout)
	assert.NoError(t, err)
}
