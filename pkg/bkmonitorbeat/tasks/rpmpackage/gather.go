// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package rpmpackage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	config  *configs.RpmPackageConfig
	tasks.BaseTask
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.config = taskConfig.(*configs.RpmPackageConfig)

	gather.Init()
	return gather
}

func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	if !utils.IsLinuxOS() {
		return
	}

	if g.running.Load() {
		logger.Info("RpmPackage task has running, will skip")
		return
	}

	g.running.Store(true)
	defer g.running.Store(false)

	now := time.Now()
	var items []PackageInfo

	executable, err := os.Executable()
	if err != nil {
		logger.Errorf("failed to get executable: %v", err)
		return
	}

	args := []string{
		"-verify-rpm",
		"-cgroup-block-write-bytes",
		fmt.Sprintf("%d", g.config.BlockWriteBytes),
		"-cgroup-block-read-bytes",
		fmt.Sprintf("%d", g.config.BlockReadBytes),
		"-cgroup-block-write-iops",
		fmt.Sprintf("%d", g.config.BlockWriteIOps),
		"-cgroup-block-read-iops",
		fmt.Sprintf("%d", g.config.BlockReadIOps),
	}

	cmd := exec.CommandContext(ctx, executable, args...)
	b, err := cmd.Output()
	if err != nil {
		logger.Errorf("faield to exec verify-rpm command: %v", err)
		return
	}
	var ret []utils.RpmResult
	if err := json.Unmarshal(b, &ret); err != nil {
		logger.Errorf("failed to unmarshal rpm-result: %v", err)
		return
	}

	for _, item := range ret {
		items = append(items, PackageInfo{
			Package: item.Package,
			Verify:  item.Verify,
		})
	}

	e <- &Event{dataid: g.config.DataID, data: items, utcTime: now}
}
