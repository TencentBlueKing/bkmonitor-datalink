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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Gather struct {
	config *configs.RpmPackageConfig
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
	var items []PackageInfo
	pkgs, err := RpmList(ctx)
	if err != nil {
		logger.Errorf("failed to list rpm packages: %v", err)
		return
	}

	for _, pkg := range pkgs {
		if pkg == "" {
			continue
		}
		time.Sleep(time.Millisecond * 20) // 打散 CPU
		verify, err := RpmVerify(ctx, pkg)
		if err != nil {
			logger.Errorf("failed to verfiy rpm package '%s', err: %v", pkg, err)
			continue
		}
		items = append(items, PackageInfo{
			Package: pkg,
			Verify:  verify,
		})
	}

	e <- &Event{dataid: g.config.DataID, data: items}
}
