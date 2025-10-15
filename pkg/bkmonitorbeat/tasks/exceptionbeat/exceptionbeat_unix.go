// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package exceptionbeat

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/exceptionbeat/collector"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/exceptionbeat/collector/corefile"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/exceptionbeat/collector/diskro"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/exceptionbeat/collector/diskspace"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/exceptionbeat/collector/outofmem"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	DiskReadOnlyCollection   = "C_DISKRO"
	DiskSpaceCollection      = "C_DISK_SPACE"
	CoreFileDetectCollection = "C_CORE"
	OutOfMemCollection       = "C_OOM"
)

var methods []collector.Collector

type Gather struct {
	config *configs.ExceptionBeatConfig

	isRunning bool        // is collect task running
	runMutex  *sync.Mutex // lock isRunning

	ctx    context.Context
	cancel context.CancelFunc

	tasks.BaseTask
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{
		runMutex: new(sync.Mutex),
	}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.config = taskConfig.(*configs.ExceptionBeatConfig)

	bits, err := parseCheckBit(*gather.config)
	if err != nil {
		logger.Errorf("failed to parse exceptionbeat config: %v, err: %v", gather.config, err)
		return gather
	}

	gather.ctx, gather.cancel = context.WithCancel(context.Background())
	gather.config.CheckBit = bits
	gather.Init()

	logger.Info("NewOomParser a ExceptionBeat Task Instance")
	return gather
}

func (g *Gather) Stop() {
	g.cancel()
	g.isRunning = false
	logger.Info("ExceptionBeat has already Stopped")
}

func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	logger.Info("ExceptionBeat is running....")
	if g.isRunning {
		return
	}

	g.ctx, g.cancel = context.WithCancel(ctx)
	g.PreRun(g.ctx)
	defer g.PostRun(g.ctx)

	methods = collector.GetMethods()
	logger.Debugf("Total number of exception event collecting methods that have been inited: %d", len(methods))
	for _, v := range methods {
		v.Start(g.ctx, e, g.config)
	}

	g.isRunning = true
}

func parseCheckBit(conf configs.ExceptionBeatConfig) (int, error) {
	if len(conf.CheckMethod) == 0 {
		conf.CheckMethod = "C_DISK_SPACE|C_DISK_RO|C_CORE|C_OOM"
	}

	methods := strings.Split(conf.CheckMethod, "|")
	logger.Debugf("length of methods: %d, (%v)", len(methods), methods)
	bits := 0
	for _, method := range methods {
		switch method {
		case DiskReadOnlyCollection:
			bits |= configs.DiskRO
		case DiskSpaceCollection:
			bits |= configs.DiskSpace
		case CoreFileDetectCollection:
			bits |= configs.Core
		case OutOfMemCollection:
			bits |= configs.OOM
		}
	}
	if bits == 0 {
		return 0, fmt.Errorf("[check_bit] option has invalid value")
	}
	return bits, nil
}
