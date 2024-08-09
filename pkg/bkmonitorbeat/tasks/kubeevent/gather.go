// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kubeevent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/hpcloud/tail"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type recorder struct {
	ctx            context.Context
	set            map[string]*k8sEvent
	mut            sync.Mutex
	interval       time.Duration
	eventSpan      time.Duration
	started        int64
	dataID         int32
	externalLabels []map[string]string
	out            chan define.Event
}

func newRecorder(ctx context.Context, conf *configs.KubeEventConfig) *recorder {
	interval := time.Minute
	if conf.Interval.Seconds() > 0 {
		interval = conf.Interval
	}
	eventSpan := time.Hour * 2
	if conf.EventSpan.Seconds() > 0 {
		eventSpan = conf.EventSpan
	}
	r := &recorder{
		ctx:            ctx,
		interval:       interval,
		eventSpan:      eventSpan,
		dataID:         conf.DataID,
		externalLabels: conf.GetLabels(),
		set:            map[string]*k8sEvent{},
		started:        time.Now().Unix(),
		out:            make(chan define.Event, 1),
	}

	go r.loopSent()
	go r.loopCleanup()
	return r
}

func (r *recorder) loopCleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.mut.Lock()
			now := time.Now().Unix()
			// 避免内存无限增长
			for k, v := range r.set {
				if now-v.GetLastTime() >= int64(r.eventSpan.Seconds()) {
					delete(r.set, k)
				}
			}
			r.mut.Unlock()
		case <-r.ctx.Done():
			return
		}
	}
}

func (r *recorder) loopSent() {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.mut.Lock()
			for _, v := range r.set {
				// 状态重置 清算 window
				cloned := v.Clone()
				v.windowL = v.windowR
				cnt := cloned.windowR - cloned.windowL

				// 表示这段时间内没有产生事件
				if cnt <= 0 {
					if cnt < 0 {
						logger.Errorf("get negative counter, event: %+v", cloned)
					}
					continue
				}
				cloned.Count = cnt
				r.out <- newWrapEvent(r.dataID, r.externalLabels, *cloned)
			}
			r.mut.Unlock()

		case <-r.ctx.Done():
			return
		}
	}
}

// Recv 只负责接收原始数据 不负责做差值计算
func (r *recorder) Recv(event k8sEvent) {
	r.mut.Lock()
	defer r.mut.Unlock()

	// 异常时间处理
	if event.IsZeroTime() {
		return
	}

	h := event.Hash()
	if _, ok := r.set[h]; !ok {
		r.set[h] = &event
		switch {
		case event.GetFirstTime() > r.started:
			// 事件第一次发生时间在采集器启动后
			// 按原始的次数计算
			r.set[h].windowL = 0
			r.set[h].windowR = event.GetCount()

		case event.GetLastTime() > r.started:
			// 事件在采集器启动前就已发送过
			// 最近一次发生的时间在采集器启动后（可能是缓存进行了清理）
			// 次数记录为 1
			r.set[h].windowL = event.GetCount() - 1
			r.set[h].windowR = event.GetCount()

		default:
			// 事件第一次发生时间在采集器启动前
			// 事件最近一次发生时间也在采集器启动前
			// 次数为 0 且下个周期不发送
		}
		logger.Infof("receive set event first: %+v", r.set[h])
		return
	}

	r.set[h].windowR = event.GetCount()
	r.set[h].Message = event.Message // 采样最后一次 message
	r.set[h].LastTs = event.LastTs
	logger.Infof("receive set event again: %+v", r.set[h])
}

// Gather :
type Gather struct {
	tasks.BaseTask
	config *configs.KubeEventConfig
	store  *recorder
	ctx    context.Context
	cancel context.CancelFunc
}

// Run :
func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	logger.Info("kubeevent gather is running...")
	defer logger.Info("kubeevent exit")

	g.PreRun(ctx)
	defer g.PostRun(ctx)

	g.ctx, g.cancel = context.WithCancel(ctx)
	g.store = newRecorder(g.ctx, g.config)
	for _, f := range g.config.TailFiles {
		go g.watchEvents(f)
	}

	for {
		select {
		case out := <-g.store.out:
			logger.Infof("send k8s event: %+v", out.AsMapStr())
			e <- out
		case <-g.ctx.Done():
			return
		}
	}
}

func (g *Gather) watchEvents(filename string) {
	tr, err := tail.TailFile(filename, tail.Config{
		Follow: true,
		ReOpen: true,
		Poll:   true,
	})
	if err != nil {
		logger.Errorf("failed to follow file: %s, err: %v", filename, err)
		return
	}
	defer tr.Stop()

	for {
		select {
		case line := <-tr.Lines:
			var e k8sEvent
			if err := json.Unmarshal([]byte(line.Text), &e); err != nil {
				logger.Errorf("failed to parse k8s event: %v", err)
				continue
			}
			g.store.Recv(e)

		case <-g.ctx.Done():
			return
		}
	}
}

// New :
func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.config = taskConfig.(*configs.KubeEventConfig)
	gather.Init()

	logger.Infof("kubeevent config: %v", gather.config)
	return gather
}
