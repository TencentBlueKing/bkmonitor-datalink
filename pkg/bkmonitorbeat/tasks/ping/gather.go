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
	"strconv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Gather :
type Gather struct {
	tasks.BaseTask
}

// Run :
func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	taskConf := g.TaskConfig.(*configs.PingTaskConfig)
	// 预处理
	g.PreRun(ctx)
	defer g.PostRun(ctx)

	// 配置超时context
	timeout := taskConf.Timeout
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 生成初始化参数
	maxRTT, err := time.ParseDuration(taskConf.MaxRTT)
	if err != nil {
		logger.Errorf("parse max rtt failed, error:%s", err)
		tasks.SendFailEvent(taskConf.GetDataID(), e)
		return
	}

	// 发送间隔
	if taskConf.SendInterval == "" {
		taskConf.SendInterval = "500us"
	}
	sendInterval, err := time.ParseDuration(taskConf.SendInterval)
	if err != nil {
		logger.Errorf("parse send interval failed, error:%s", err)
		tasks.SendFailEvent(taskConf.GetDataID(), e)
		return
	}

	if len(taskConf.Targets) == 0 {
		// 目标为空则直接返回空
		logger.Debugf("icmp targetList is empty")
		return
	}

	// ping目标准备
	var targets []*PingerTarget
	for _, target := range taskConf.Targets {
		targets = append(targets, &PingerTarget{
			Target:     target.GetTarget(),
			TargetType: target.GetTargetType(),
			Labels:     target.Labels,

			DnsCheckMode: taskConf.DNSCheckMode,
			DomainIpType: taskConf.TargetIPType,

			MaxRtt: maxRTT,
			Times:  taskConf.TotalNum,
			Size:   taskConf.PingSize,
		})
	}

	// 启动ping任务
	pinger := NewPinger(sendInterval, !taskConf.NotPrivileged)
	err = pinger.Ping(subCtx, targets)
	if err != nil {
		logger.Errorf("ping failed, error:%v", err)
		tasks.SendFailEvent(taskConf.GetDataID(), e)
		return
	}

	// 数据处理
	resultCount := 0
	config := g.GetConfig().(*configs.PingTaskConfig)

	pingEvents := make([]*tasks.PingEvent, 0)
	for _, target := range targets {
		for ip, rttList := range target.GetResult() {
			// 计数
			resultCount++

			// 丢包率及时延统计
			lossCount, rttTotal, maxRtt, minRtt := 0, 0.0, 0.0, 0.0
			for _, rtt := range rttList {
				rtt := rtt.Seconds() * 1000

				if rtt <= 0 {
					lossCount++
				} else {
					rttTotal += rtt
					if rtt > maxRtt {
						maxRtt = rtt
					}
					if rtt < minRtt || minRtt == 0 {
						minRtt = rtt
					}
				}
			}

			// 丢包率及可用率
			lossPercent := float64(lossCount) / float64(len(rttList))
			available := 1 - lossPercent

			// 计算平均时延
			var avgRtt float64
			if rttTotal > 0 {
				avgRtt = rttTotal / float64(len(rttList)-lossCount)
			}

			// 解析域名时，resolved_ip为解析后的ip
			resolvedIP := ""
			if target.TargetType == "domain" {
				resolvedIP = ip
			}

			// 生成事件
			event := tasks.NewPingEvent(config)
			event.Time = time.Now()
			event.DataID = taskConf.GetDataID()
			event.Dimensions = map[string]string{
				"target":      target.Target,
				"target_type": target.TargetType,
				"error_code":  "0",
				"bk_biz_id":   strconv.Itoa(int(taskConf.GetBizID())),
				"resolved_ip": resolvedIP,
			}

			// 将target的labels合并到event的dimensions中
			for k, v := range target.Labels {
				// 如果event中没有该key，则添加
				if _, ok := event.Dimensions[k]; !ok {
					event.Dimensions[k] = v
				}
			}

			event.Metrics = map[string]interface{}{
				"available":     available,
				"loss_percent":  lossPercent,
				"max_rtt":       maxRtt,
				"min_rtt":       minRtt,
				"avg_rtt":       avgRtt,
				"task_duration": avgRtt,
			}

			// 如果需要使用自定义上报，则将事件转换为自定义事件
			if config.CustomReport {
				pingEvents = append(pingEvents, event)

				// 为了避免上报数据条数过多，将数据分批上报
				if len(pingEvents) >= 512 {
					e <- tasks.NewCustomEventByPingEvent(pingEvents...)

					// 清空数据
					pingEvents = make([]*tasks.PingEvent, 0)
				}
			} else {
				e <- event
			}
		}
	}

	// 如果有剩余数据，则上报
	if len(pingEvents) > 0 {
		e <- tasks.NewCustomEventByPingEvent(pingEvents...)
	}

	// 任务结束
	logger.Infof("ping task(%d) get %v result", taskConf.TaskID, resultCount)
}

// New :
func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.Init()

	return gather
}
