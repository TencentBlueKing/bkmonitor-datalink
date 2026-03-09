// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

/*
进程采集配置的数据源目前有两种：
1）CMDB 进程采集配置
2) 用户自定义数据采集

目前这两者分别对应的不同的`任务类型`和`采集配置同步类型`
1) CMDB：procconf（同步采集任务） -> processbeat
2）用户自定义：procsync（同步采集任务） -> proccustom

不同的同步任务执行的逻辑也不同：
1）procconf： 读取 `/var/lib/gse/host/hostid` 文件并转换成 CMDB 进程采集任务到 `/usr/local/gse/plugins/etc/bkmonitorbeat/bkmonitorbeat_processbeat.conf`
2）proccustom：读取 `/usr/local/gse/plugins/etc/bkmonitorbeat/processbeat` 文件夹内容并转换成自定义采集任务到 `/usr/local/gse/plugins/etc/bkmonitorbeat`
文件名以 `monitor_process` 作为前缀
*/

package processbeat

import (
	"context"
	"time"

	bkcommon "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/processbeat/process"
)

type Gather struct {
	config    *configs.ProcessbeatConfig
	isRunning bool
	tasks.BaseTask
	ctr *process.ProcCollector
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.config = taskConfig.(*configs.ProcessbeatConfig)

	gather.ctr = process.NewProcCollector()
	gather.ctr.UpdateConf(gather.config)

	gather.Init()
	time.Sleep(time.Second)

	logger.Info("New ProcessBeat Task Instance")
	return gather
}

type processEvent struct {
	DataID int32
	Data   interface{}
}

func (e processEvent) AsMapStr() common.MapStr {
	datetime, utctime, zone := bkcommon.GetDateTime()
	return common.MapStr{
		"type":     "processbeat",
		"dataid":   e.DataID,
		"data":     e.Data,
		"timezone": zone,
		"datetime": datetime,
		"utctime":  utctime,
	}
}

func (e processEvent) IgnoreCMDBLevel() bool { return false }

func (e processEvent) GetType() string {
	return define.ModuleProcessbeat
}

func (g *Gather) Run(_ context.Context, e chan<- define.Event) {
	logger.Info("ProcessBeat is running....")
	if g.isRunning {
		logger.Info("ProcessBeat has been started")
		return
	}
	if g.config.Disable {
		logger.Info("ProcessBeat collection is disabled")
		return
	}

	g.isRunning = true
	defer func() { g.isRunning = false }()

	all, err := g.ctr.GetAllMetaData()
	if err != nil {
		logger.Warnf("failed to get all proc perf detailed: %v", err)
		return
	}

	now := time.Now()
	exists, notExists, pcs := g.ctr.CollectProcStat(all)
	if g.config.EnablePerfCollected() {
		e <- processEvent{
			DataID: g.config.PerfDataId,
			Data:   common.MapStr{"perf": append(exists, notExists...)},
		}
	}
	if g.config.EnablePortCollected() {
		ports, err := g.ctr.CollectPortStat(pcs)
		if err != nil {
			logger.Errorf("failed to get proc port detailed: %v", err)
			return
		}

		maxNolisten := g.config.MaxNoListenPorts
		if maxNolisten <= 0 {
			maxNolisten = 100 // 默认不允许超过 100 个无监听端口
		}
		for i := 0; i < len(ports.Processes); i++ {
			if len(ports.Processes[i].NonListen) > maxNolisten {
				ports.Processes[i].NonListen = []uint16{0} // 特殊标记
			}
		}

		e <- processEvent{
			DataID: g.config.PortDataId,
			Data:   ports,
		}
	}
	logger.Infof("processbeat Collected take: %+v", time.Since(now))
}
