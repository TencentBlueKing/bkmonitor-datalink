// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricbeat

import (
	"context"
	"net/url"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Gather :
type Gather struct {
	tasks.BaseTask
	ctx    context.Context
	cancel context.CancelFunc
	config *configs.MetricBeatConfig

	tool *Tool
}

// Run :
func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	// 预处理
	g.PreRun(ctx)
	defer g.PostRun(ctx)
	g.ctx, g.cancel = context.WithCancel(ctx)
	logger.Info("metricbeat is starting")

	if gc, ok := g.GlobalConfig.(*configs.Config); ok {
		g.config.Workers = gc.MetricbeatWorkers
	}

	if g.tool == nil {
		g.tool = new(Tool)
		err := g.tool.Init(g.config, g.GetGlobalConfig())
		if err != nil {
			logger.Errorf("metricbeat init failed, err:%v", err)
			g.tool = nil
			return
		}
	}

	valCtx := context.WithValue(g.ctx, "gConfig", g.GlobalConfig)
	err := g.tool.Run(valCtx, e)
	if err != nil {
		logger.Errorf("metricbeat run failed, err: %v", err)
		return
	}
}

// New :
func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.config = taskConfig.(*configs.MetricBeatConfig)

	hosts := struct {
		Hosts []string `config:"hosts"`
	}{}
	if err := gather.config.Module.Unpack(&hosts); err != nil {
		logger.Errorf("failed to unpack hosts: %v", err)
		return &Gather{}
	}
	logger.Infof("metricbeat hosts: %v", hosts)

	for _, h := range hosts.Hosts {
		// 如果不是 http 协议的话那就不处理了
		// snmp 的采集可能没有协议头
		if !strings.HasPrefix(h, "http") {
			continue
		}
		if _, err := url.Parse(h); err != nil {
			logger.Errorf("failed to parse host: %s, %v", h, err)
			return &Gather{}
		}
	}

	gather.Init()
	return gather
}
