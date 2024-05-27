// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package procsnapshot

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Gather struct {
	config *configs.ProcSnapshotConfig
	tasks.BaseTask
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.config = taskConfig.(*configs.ProcSnapshotConfig)

	gather.Init()

	logger.Info("New a ProcSnapshot Task Instance")
	return gather
}

func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	logger.Info("ProcSnapshot is running....")

	procs, err := allProcsMeta()
	if err != nil {
		logger.Errorf("faile to get all procs meta: %v", err)
		return
	}

	pids := make([]int32, 0, len(procs))
	for i := 0; i < len(procs); i++ {
		pids = append(pids, procs[i].Pid)
	}

	fs, err := allProcsFileSockets(pids)
	if err != nil {
		logger.Errorf("faile to get filesockets: %v", err)
		return
	}

	evt := &Event{
		DataID:  g.config.DataID,
		Process: procs,
		Network: fs,
	}
	e <- evt
}
