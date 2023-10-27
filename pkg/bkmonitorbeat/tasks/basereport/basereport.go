// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package basereport

import (
	"context"
	"regexp"
	"sync"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/basereport/collector"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/basereport/toolkit"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Gather struct {
	config *configs.BasereportConfig
	once   bool

	isRunning bool        // is collect task running
	runMutex  *sync.Mutex // lock isRunning
	tasks.BaseTask
}

// New :
func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{runMutex: new(sync.Mutex)}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.Init()
	cfg := taskConfig.(*configs.BasereportConfig)

	// 确保不能除以 0
	if cfg.Cpu.StatTimes <= 0 {
		cfg.Cpu.StatTimes = configs.DefaultBasereportConfig.Cpu.StatTimes
	}
	if cfg.Disk.StatTimes <= 0 {
		cfg.Disk.StatTimes = configs.DefaultBasereportConfig.Disk.StatTimes
	}
	if cfg.Mem.InfoTimes <= 0 {
		cfg.Mem.InfoTimes = configs.DefaultBasereportConfig.Mem.InfoTimes
	}
	if cfg.Net.StatTimes <= 0 {
		cfg.Net.StatTimes = configs.DefaultBasereportConfig.Net.StatTimes
	}

	// 计算出每次调用的时间间隔
	cfg.Cpu.StatPeriod = cfg.Period / time.Duration(cfg.Cpu.StatTimes)
	cfg.Disk.StatPeriod = cfg.Period / time.Duration(cfg.Disk.StatTimes)
	cfg.Mem.InfoPeriod = cfg.Period / time.Duration(cfg.Mem.InfoTimes)
	cfg.Net.StatPeriod = cfg.Period / time.Duration(cfg.Net.StatTimes)

	// 初始化编译各个正则pattern
	configPairList := []struct {
		pattern *[]string
		reItem  *[]*regexp.Regexp
	}{
		{
			&cfg.Disk.DiskBlackListPattern,
			&cfg.Disk.DiskBlackList,
		},
		{
			&cfg.Disk.DiskWhiteListPattern,
			&cfg.Disk.DiskWhiteList,
		},
		{
			&cfg.Disk.PartitionBlackListPattern,
			&cfg.Disk.PartitionBlackList,
		},
		{
			&cfg.Disk.PartitionWhiteListPattern,
			&cfg.Disk.PartitionWhiteList,
		},
		{
			&cfg.Disk.MountpointBlackListPattern,
			&cfg.Disk.MountpointBlackList,
		},
		{
			&cfg.Disk.MountpointWhiteListPattern,
			&cfg.Disk.MountpointWhiteList,
		},
		{
			&cfg.Disk.FSTypeBlackListPattern,
			&cfg.Disk.FSTypeBlackList,
		},
		{
			&cfg.Disk.FSTypeWhiteListPattern,
			&cfg.Disk.FSTypeWhiteList,
		},
		{
			&cfg.Net.InterfaceBlackListPattern,
			&cfg.Net.InterfaceBlackList,
		},
		{
			&cfg.Net.InterfaceWhiteListPattern,
			&cfg.Net.InterfaceWhiteList,
		},
		{
			&cfg.Net.ForceReportListPattern,
			&cfg.Net.ForceReportList,
		},
	}

	for _, configPair := range configPairList {
		for _, pattern := range *configPair.pattern {
			if compileResult, err := regexp.Compile(pattern); err != nil {
				logger.Errorf("failed to compile pattern->[%s] for err->[%s]", pattern, err)
				continue
			} else {
				*configPair.reItem = append(*configPair.reItem, compileResult)
				logger.Infof("pattern->[%s] is added to result.", pattern)
			}
		}
	}

	gather.config = cfg
	logger.Infof("basereport.New.config: %+v", cfg)
	return gather
}

// Run beater interface
func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	g.PreRun(ctx)
	defer g.PostRun(ctx)

	if !g.once {
		g.fastRunOnce()
		g.once = true
		return
	}

	g.CollectItem(e)
}

// 获取第一个采集点，提供计算差值使用
func (g *Gather) fastRunOnce() {
	// 初始化时先运行一次 并把任务标记为 running 完成后标记为 done
	g.markRunningState()
	defer g.markDoneState()

	cfg := configs.FastBasereportConfig
	// 即使是单次运行，也需要获取用户指定的CPU INFO获取间隔时间
	// 防止配置失效
	cfg.Cpu.InfoPeriod = g.config.Cpu.InfoPeriod
	cfg.Cpu.InfoTimeout = g.config.Cpu.InfoTimeout

	// 同步硬盘配置
	cfg.Disk.IOSkipPartition = g.config.Disk.IOSkipPartition
	cfg.Disk.DropDuplicateDevice = g.config.Disk.DropDuplicateDevice
	cfg.Disk.DiskWhiteList = g.config.Disk.DiskWhiteList
	cfg.Disk.DiskBlackList = g.config.Disk.DiskBlackList
	cfg.Disk.PartitionWhiteList = g.config.Disk.PartitionWhiteList
	cfg.Disk.PartitionBlackList = g.config.Disk.PartitionBlackList
	cfg.Disk.MountpointWhiteList = g.config.Disk.MountpointWhiteList
	cfg.Disk.MountpointBlackList = g.config.Disk.MountpointBlackList
	cfg.Disk.FSTypeBlackList = g.config.Disk.FSTypeBlackList
	cfg.Disk.FSTypeWhiteList = g.config.Disk.FSTypeWhiteList

	// 同步网络配置
	cfg.Net.SkipVirtualInterface = g.config.Net.SkipVirtualInterface
	cfg.Net.InterfaceWhiteList = g.config.Net.InterfaceWhiteList
	cfg.Net.InterfaceBlackList = g.config.Net.InterfaceBlackList
	cfg.Net.ForceReportList = g.config.Net.ForceReportList
	cfg.Cpu.InfoTimeout = g.config.Cpu.InfoTimeout

	cfg.Net.RevertProtectNumber = g.config.Net.RevertProtectNumber
	cfg.Mem.SpecialSource = g.config.Mem.SpecialSource

	logger.Infof("basereport.fastRunOnce.config: %+v", cfg)
	// 计算出每次调用的时间间隔
	collector.Collect(cfg, true)
	// 此处休眠的时间是fastRun的配置，默认是5秒
	time.Sleep(cfg.Period)
}

type BasereportEvent struct {
	Type   string
	DataID int32
	Data   collector.ReportData
}

func (be BasereportEvent) AsMapStr() common.MapStr {
	return common.MapStr{
		"type":   "basereport",
		"dataid": be.DataID,
		"data":   be.Data,
	}
}

func (be BasereportEvent) IgnoreCMDBLevel() bool { return false }

func (be BasereportEvent) GetType() string {
	return define.ModuleBasereport
}

func (g *Gather) CollectItem(e chan<- define.Event) {
	logger.Debug("start to exec basereport CollectItem")
	if g.IsRunning() {
		logger.Info("found collect task is already running, this round will do nothing.")
		return
	}

	// 防止改变isRunning之后，已经进行判断完毕从而影响判断结果
	if !g.markRunningState() {
		logger.Error("failed to mark state to running, nothing will do now.")
		return
	}
	logger.Debug("mark running state done, will start task now")

	defer func() {
		if result := g.markDoneState(); !result {
			logger.Error("failed to mark state to done, maybe something go wrong?")
			return
		}
		logger.Debug("state is mark to done now.")
	}()

	var (
		data collector.ReportData
		err  error
	)

	if g.config.DataID >= 0 {
		// collect port
		localTime := time.Now().Unix()
		if data, err = collector.Collect(*g.config, false); err != nil {
			logger.Errorf("collect failed, %v", err)
			return
		}

		// make event
		event := BasereportEvent{
			Type:   "basereport",
			DataID: g.TaskConfig.GetDataID(),
			Data:   data,
		}

		// 与上次启动最后上报时间进行对比，判断是否要上报
		// 在此处判断，主要是因为上报的时间和记录时间如果有差异，可能会导致上报异常，例如
		// 例如，是在00分59秒触发的动作，但是上报的时间是01分03秒，到01分59秒再触发时认为已经上报过了
		if !toolkit.IsDiffMinLastPublish(time.Now(), g.config.Period) {
			logger.Info("data already report in this min, exit in current min")
			return
		}
		logger.Debug("no data report in this min, will report now")

		// send data
		e <- event

		if err = toolkit.RecordPublishTime(localTime); err != nil {
			logger.Errorf("failed to save report time for->[%s]", err)
		}
		logger.Debugf("set last publish time :%d", localTime)
	}
}

// 判断是否需要采集上报，如果需要则会直接将running改为true
func (g *Gather) markRunningState() bool {
	g.runMutex.Lock()
	defer g.runMutex.Unlock()
	if g.isRunning {
		logger.Errorf("is already running state, state will not change any way.")
		return false
	}
	g.isRunning = true
	logger.Debug("running stats is set to true now.")
	return true
}

func (g *Gather) markDoneState() bool {
	g.runMutex.Lock()
	defer g.runMutex.Unlock()
	if !g.isRunning {
		logger.Errorf("state is already not running, it should not change to false")
		return false
	}
	g.isRunning = false
	logger.Debug("running stats is set to false now.")
	return true
}

func (g *Gather) IsRunning() bool {
	g.runMutex.Lock()
	defer g.runMutex.Unlock()
	return g.isRunning
}
