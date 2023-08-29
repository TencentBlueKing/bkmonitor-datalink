// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ping

import (
	"context"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Gather :
type Gather struct {
	tasks.BaseTask
}

// InitTargets 初始化targets
var InitTargets = func(targets []*configs.Target) []Target {
	targetList := make([]Target, len(targets))
	for index, target := range targets {
		targetList[index] = target
	}
	return targetList
}

// analyzeResult 处理结果发送
func (g *Gather) analyzeResult(resMap map[string]map[string]*Info, dataID int32, outChan chan<- define.Event) {
	// 遍历结果，组装event并发送
	recvCountMap := make(map[string]int)
	for _, vMap := range resMap {
		for ipStr, v := range vMap {
			// 内部逻辑通过 ipStr 为空，将异常信息透传出来
			if ipStr == "" {
				errUpEvent := tasks.NewGatherUpEventWithDims(g, define.BeatErrorCode(v.Code), common.MapStr{"bk_target_ip": v.Name})
				// 目前将拨测状态异常的事件通过日志的方式记录下来，后续考虑上报独立的DataID
				logger.Errorf("Fail to gather ping result, up = %v+", errUpEvent.AsMapStr())
				continue
			}
			logger.Debugf("resMap item:%v", v)
			event := tasks.NewPingEvent(g.GetConfig())
			now := time.Now()
			// 计算丢包率
			lossPercent := float64(v.TotalCount-v.RecvCount) / float64(v.TotalCount)
			event.Time = now
			event.DataID = dataID
			var resolvedIP string
			if v.Type == "domain" {
				resolvedIP = ipStr
			}
			dimensions := map[string]string{
				"target":      v.Name, //实际ping的目标地址
				"target_type": v.Type, // 目标类型
				"error_code":  "0",
				"bk_biz_id":   string(g.TaskConfig.GetBizID()),
				"resolved_ip": resolvedIP,
			}

			// available 和 task_duration是兼容其他拨测的格式
			metrics := map[string]interface{}{
				"available":    1 - lossPercent,
				"loss_percent": lossPercent,
				"max_rtt":      v.MaxRTT, //最大时延
				"min_rtt":      v.MinRTT,
			}
			if v.RecvCount != 0 {
				avgRtt := v.TotalRTT / float64(v.RecvCount)
				metrics["avg_rtt"] = avgRtt
				metrics["task_duration"] = avgRtt
				recvCountMap[v.Type] += v.RecvCount
			} else {
				metrics["avg_rtt"] = 0
				metrics["task_duration"] = 0
			}
			event.Dimensions = dimensions
			event.Metrics = metrics
			outChan <- event
		}
	}
}

// Run :
func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	var (
		taskConf = g.TaskConfig.(*configs.PingTaskConfig)
	)
	// 预处理
	g.PreRun(ctx)
	defer g.PostRun(ctx)

	// 配置超时context
	timeout := taskConf.Timeout
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 生成初始化参数
	totalNum := taskConf.TotalNum
	maxRTT := taskConf.MaxRTT
	batchSize := taskConf.BatchSize
	targetList := InitTargets(taskConf.Targets)
	pingSize := taskConf.PingSize
	ipType := taskConf.TargetIPType
	dnsCheckMode := taskConf.DNSCheckMode

	if len(targetList) == 0 {
		//目标为空则直接返回空
		logger.Debugf("icmp targetList is empty")
		return
	}

	// 获取ping工具
	tool, err := NewBatchPingTool(subCtx, targetList, totalNum, maxRTT, pingSize, batchSize, ipType, dnsCheckMode, g.GetSemaphore())
	if err != nil {
		logger.Errorf("new ping tool failed, error:%s", err)
		tasks.SendFailEvent(taskConf.GetDataID(), e)
		return
	}

	// 记录结果数
	resultCount := 0

	// doFunc提供一个回调函数给pingTool，提供处理ping结果的能力
	var doFunc = func(resMap map[string]map[string]*Info, wg *sync.WaitGroup) {
		defer func() {
			wg.Done()
			g.GetSemaphore().Release(1)
		}()
		// 结果计数叠加
		resultCount = resultCount + len(resMap)
		g.analyzeResult(resMap, taskConf.DataID, e)
	}

	// 启动ping操作
	err = tool.Ping(doFunc)
	if err != nil {
		logger.Errorf("ping failed, error:%v", err)
		tasks.SendFailEvent(taskConf.GetDataID(), e)
	}

	// 任务结束
	logger.Infof("ping task get %v result", resultCount)
}

// New :
func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.Init()

	return gather
}
