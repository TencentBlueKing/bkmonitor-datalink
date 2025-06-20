// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/basereport/toolkit"
)

type EnvReport struct {
	Contab         []toolkit.Crontab `json:"crontab"`
	Host           string            `json:"host"`
	Route          string            `json:"route"`
	MaxFiles       int               `json:"maxfiles"`
	AllocatedFiles int               `json:"allocated_files"`
	Uname          string            `json:"uname"`
	LoginUser      int               `json:"login_user"`
	RunningProc    int               `json:"proc_running_current"`
	BlockedProc    int               `json:"procs_blocked_current"`
	Totalproc      int               `json:"procs_processes_total"`
	Ctxt           int               `json:"procs_ctxt_total"`
}

func GetEnvInfo(config configs.BasereportConfig) (*EnvReport, error) {
	report := EnvReport{
		Contab: make([]toolkit.Crontab, 0),
	}
	var err, lastErr error

	if config.ReportCrontab {
		report.Contab, err = toolkit.ListCrontab()
		if err != nil {
			lastErr = err
		}
	}

	if config.ReportHosts {
		report.Host, err = toolkit.ListHosts()
		if err != nil {
			lastErr = err
		}
	}

	if config.ReportRoute {
		report.Route, err = toolkit.ListRouteTable()
		if err != nil {
			lastErr = err
		}
	}

	report.MaxFiles, err = GetMaxFiles()
	if err != nil {
		lastErr = err
	}

	report.AllocatedFiles, err = GetAllocatedFiles()
	if err != nil {
		lastErr = err
	}

	report.Uname, err = GetUname()
	if err != nil {
		lastErr = err
	}

	report.LoginUser, err = GetLoginUsers()
	if err != nil {
		lastErr = err
	}

	report.RunningProc, report.BlockedProc, report.Totalproc, report.Ctxt, err = GetProcEnv()
	if err != nil {
		lastErr = err
	}

	return &report, lastErr
}
