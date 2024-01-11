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
	"github.com/shirou/gopsutil/v3/host"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
)

type SystemInfo struct {
	*host.InfoStat
	SystemType string `json:"systemtype"`
}

type SystemReport struct {
	Info SystemInfo `json:"info"`
}

// osSystemType 运行时不会发生变更 可以缓存
var osSystemType string

func GetSystemInfo() (*SystemReport, error) {
	var report SystemReport
	var err error

	report.Info.InfoStat, err = host.Info()
	if err != nil {
		return nil, err
	}

	if osSystemType == "" {
		osSystemType = tasks.GetSystemType()
	}

	report.Info.SystemType = osSystemType
	return &report, nil
}
