// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package loginlog

import (
	"bytes"
	"context"
	"os"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Gather struct {
	config *configs.LoginLogConfig
	tasks.BaseTask
}

var logPaths = []string{
	"/var/log/wtmp",
	"/var/run/utmp",
	"/var/log/btmp",
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.config = taskConfig.(*configs.LoginLogConfig)
	gather.Init()
	return gather
}

func (g *Gather) Run(_ context.Context, e chan<- define.Event) {
	if !utils.IsLinuxOS() {
		return
	}

	var records []Record
	now := time.Now()
	for _, file := range logPaths {
		b, err := os.ReadFile(file)
		if err != nil {
			logger.Errorf("failed to read login logs, file=%s, err: %v", file, err)
			continue
		}

		logs, err := Unpack(bytes.NewBuffer(b))
		if err != nil {
			logger.Errorf("failed to unpack login logs, file=%s, err: %v", file, err)
			continue
		}
		records = append(records, Record{
			Source: file,
			Logs:   logs,
		})
	}

	e <- &Event{
		dataid:  g.config.DataID,
		records: records,
		utcTime: now,
	}
}
