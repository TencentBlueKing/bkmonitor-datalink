// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package shellhistory

import (
	"context"
	"fmt"
	"os"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Gather struct {
	config *configs.ShellHistoryConfig
	tasks.BaseTask
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.config = taskConfig.(*configs.ShellHistoryConfig)

	gather.Init()

	logger.Info("New a ShellHistory Task Instance")
	return gather
}

func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	entities, err := parse()
	if err != nil {
		logger.Errorf("failed to parse paaswd details, err: %v", err)
		return
	}

	var items []UserHistory
	for _, entity := range entities {
		var p string
		if entity.User == "root" {
			p = "/root/.bash_history"
		} else {
			p = fmt.Sprintf("/home/%s/.bash_history", entity.User)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			logger.Warnf("failed to read file '%s', err: %v", p, err)
			continue
		}

		items = append(items, UserHistory{
			User:    entity.User,
			Path:    p,
			History: string(b),
		})
	}

	e <- &Event{dataid: g.config.DataID, data: items}
}
