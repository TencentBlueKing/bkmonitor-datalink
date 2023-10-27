// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
)

const defaultProcStatusReportPeriod = time.Hour * 24

type ProcStatusConfig struct {
	BaseTaskParam `config:"_,inline"`
	ReportPeriod  time.Duration `config:"report_period" validate:"min=1s"` // 上报周期
}

func (p *ProcStatusConfig) InitIdent() error { return p.initIdent(p) }

func (p *ProcStatusConfig) GetType() string { return define.ModuleProcStatus }

func (p *ProcStatusConfig) GetTaskConfigList() []define.TaskConfig {
	if p.DataID == 0 {
		return []define.TaskConfig{}
	}

	return []define.TaskConfig{p}
}

func (p *ProcStatusConfig) Clean() error {
	err := utils.CleanCompositeParamList(&p.BaseTaskParam)
	if err != nil {
		return err
	}
	if p.ReportPeriod <= 0 {
		p.ReportPeriod = defaultProcStatusReportPeriod
	}
	// 上报周期应比执行周期大
	if p.ReportPeriod < p.Period {
		p.ReportPeriod = p.Period
	}
	return nil
}

func NewProcStatusConfig(root *Config) *ProcStatusConfig {
	config := &ProcStatusConfig{
		BaseTaskParam: NewBaseTaskParam(),
	}
	root.TaskTypeMapping[define.ModuleProcStatus] = config
	return config
}
