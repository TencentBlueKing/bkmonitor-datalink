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
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Gather struct {
	running atomic.Bool
	config  *configs.ShellHistoryConfig
	tasks.BaseTask
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.config = taskConfig.(*configs.ShellHistoryConfig)

	gather.Init()
	return gather
}

func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	if !utils.IsLinuxOS() {
		return
	}

	if g.running.Load() {
		logger.Info("ShellHistory task has running, will skip")
		return
	}

	g.running.Store(true)
	defer g.running.Store(false)

	now := time.Now()
	entities, err := parse()
	if err != nil {
		logger.Errorf("failed to parse paaswd details, err: %v", err)
		return
	}

	var items []UserHistory
	for _, entity := range entities {
		for _, hf := range g.config.HistoryFiles {
			time.Sleep(time.Millisecond * 100)
			p := filepath.Join(entity.Home, hf)
			b, err := utils.ReadFileTail(p, g.config.LastBytes)
			if err != nil && entity.User != "root" {
				logger.Warnf("failed to read file '%s', err: %v", p, err)
				continue
			}

			items = append(items, UserHistory{
				User:    entity.User,
				Path:    p,
				History: string(b),
			})
		}
	}

	e <- &Event{dataid: g.config.DataID, data: items, utcTime: now}
}
