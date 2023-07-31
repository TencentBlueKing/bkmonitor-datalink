// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package static

import (
	"context"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const VERSION = "v1.0"

// Gather :
type Gather struct {
	tasks.BaseTask
	ctx    context.Context
	cancel context.CancelFunc
	status *Status

	checkLock *sync.RWMutex
	preData   *Report
}

// Run :
func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	conf := g.GetConfig()
	dataid := conf.GetDataID()
	g.ctx, g.cancel = context.WithTimeout(ctx, conf.GetTimeout())

	// 判断是否达到上报周期,周期上报优先于周期检查
	if g.status.ShouldReport() {
		data, err := g.getData()
		if err != nil {
			logger.Errorf("get static data failed, error:%s", err)
			return
		}
		timestamp := time.Now().Unix()
		g.Report(dataid, timestamp, e, data)
		return
	}

	logger.Debug("not reached report time")
	// 判断是否达到检查周期
	if g.status.ShouldCheck() {
		logger.Debug("start check data")
		// 采集最新数据
		data, err := g.getData()
		if err != nil {
			logger.Errorf("get static data failed,error:%s", err)
			return
		}
		timestamp := time.Now().Unix()
		if g.shouldReportData(data) {
			logger.Debug("data changed,should report new data")
			g.Report(dataid, timestamp, e, data)
			g.status.UpdateCheckTime(timestamp)
			return
		}
		logger.Debug("data not changed,and will not report")
		g.status.UpdateCheckTime(timestamp)
		return
	}
	logger.Debug("not reached check time")

}

// 获取主机静态数据
func (g *Gather) getData() (*Report, error) {
	logger.Debugf("start collect report data")

	cfg, ok := g.TaskConfig.(*configs.StaticTaskConfig)
	if ok {
		return GetData(g.ctx, cfg)
	}
	return GetData(g.ctx, nil)
}

// updateReportData 成功上报数据后，存储更新最后一次上报的数据
func (g *Gather) updateReportData(nowData *Report) {
	g.checkLock.Lock()
	defer g.checkLock.Unlock()
	g.preData = nowData
}

// 对比新旧数据，判断是否需要立刻上报
func (g *Gather) shouldReportData(nowData *Report) bool {
	g.checkLock.RLock()
	defer g.checkLock.RUnlock()
	if g.preData == nil {
		return true
	}
	// hash相同则不需要上报
	if utils.HashIt(g.preData) == utils.HashIt(nowData) {
		logger.Debug("hash data not changed")
		return false
	}
	return true
}

// Report 将report转换为event并上报
func (g *Gather) Report(dataid int32, timestamp int64, e chan<- define.Event, data *Report) {
	logger.Debug("start report data")

	var (
		operType string
	)

	if g.status.FirstReport {
		operType = NewOperType
	} else {
		operType = UpdateOperType
	}

	event := NewStaticEvent(dataid, timestamp, VERSION, data, operType)
	e <- event
	logger.Debug("report data success")
	g.status.UpdateReportTime(timestamp)
	g.updateReportData(data)
}

// New :
func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	cfg := taskConfig.(*configs.StaticTaskConfig)
	gather := &Gather{
		status:    NewStatus(cfg.CheckPeriod, cfg.ReportPeriod),
		checkLock: new(sync.RWMutex),
	}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig

	gather.Init()

	return gather
}
