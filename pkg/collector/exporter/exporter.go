// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package exporter

import (
	"context"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter/converter"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter/durationmeasurer"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter/queue"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter/sizeobserver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/hook"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/wait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	gse.MarshalFunc = json.Marshal
}

type Exporter struct {
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	converter converter.Converter
	queue     queue.Queue
	cfg       *Config
	dm        *durationmeasurer.DurationMeasurer
}

var globalRecords = define.NewRecordQueue(define.PushModeGuarantee)

func PublishRecord(r *define.Record) {
	globalRecords.Push(r)
}

var globalEvents = define.NewEventQueue(define.PushModeGuarantee)

func PublishEvents(events ...define.Event) {
	globalEvents.Push(events)
}

var SentFunc = beat.Send

func New(conf *confengine.Config) (*Exporter, error) {
	c := &Config{}
	if err := conf.UnpackChild(define.ConfigFieldExporter, c); err != nil {
		return nil, err
	}
	c.Validate()
	logger.Infof("exporter config: %+v", c)

	// 注册 gse output hook 统计发送数据
	so := sizeobserver.New()
	gse.RegisterSendHook(func(dataID int32, f float64) {
		DefaultMetricMonitor.ObserveBeatSentBytes(f)
		so.ObserveSize(dataID, int(f))
	})

	ctx, cancel := context.WithCancel(context.Background())
	return &Exporter{
		ctx:       ctx,
		cancel:    cancel,
		converter: converter.NewCommonConverter(),
		queue:     queue.NewBatchQueue(c.Queue, so),
		cfg:       c,
		dm:        durationmeasurer.New(ctx, 2*time.Minute),
	}, nil
}

func (e *Exporter) Start() error {
	logger.Info("exporter start working...")

	for i := 0; i < define.Concurrency(); i++ {
		go wait.Until(e.ctx, e.consumeRecords)
		go wait.Until(e.ctx, e.consumeEvents)
		go wait.Until(e.ctx, e.sendEvents)
	}
	go wait.Until(e.ctx, e.checkIfSlowSend)
	return nil
}

// checkIfSlowSend 检查是否存在慢发送的情况 如果存在的话就执行 hook 逻辑
func (e *Exporter) checkIfSlowSend() {
	e.wg.Add(1)
	defer e.wg.Done()

	detected := time.NewTicker(time.Minute)
	defer detected.Stop()

	ch := make(chan struct{}, 1)
	var updated int64

	enabled := e.cfg.SlowSend.Enabled
	threshold := e.cfg.SlowSend.Threshold.Seconds()
	checkInterval := int64(e.cfg.SlowSend.CheckInterval.Seconds())
	var p90, p95, p99 time.Duration
	for {
		select {
		case <-detected.C:
			p90, p95, p99 = e.dm.P90(), e.dm.P95(), e.dm.P99()
			if p99.Seconds() > threshold {
				select {
				case ch <- struct{}{}: // 非堵塞
				default:
				}
			}

		case <-ch:
			now := time.Now().Unix()
			logger.Infof("detected slow send, p90=%v, p95=%v, p99=%v", p90, p95, p99)
			if now-updated > checkInterval && enabled {
				hook.OnFailureHook()
				updated = now
			}

		case <-e.ctx.Done():
			return
		}
	}
}

func (e *Exporter) consumeEvents() {
	e.wg.Add(1)
	defer e.wg.Done()

	for {
		select {
		case events := <-globalEvents.Get():
			if len(events) <= 0 {
				continue
			}
			event := events[0]
			DefaultMetricMonitor.AddHandledEventCounter(len(events), event.RecordType(), event.DataId())
			e.queue.Put(events...)

		case <-e.ctx.Done():
			return
		}
	}
}

func (e *Exporter) consumeRecords() {
	e.wg.Add(1)
	defer e.wg.Done()

	for {
		select {
		case record := <-globalRecords.Get():
			e.converter.Convert(record, PublishEvents)

		case <-e.ctx.Done():
			return
		}
	}
}

func (e *Exporter) sendEvents() {
	e.wg.Add(1)
	defer e.wg.Done()

	for {
		select {
		case event := <-e.queue.Pop():
			start := time.Now()
			SentFunc(event)
			e.dm.Measure(time.Since(start))
			DefaultMetricMonitor.ObserveSentDuration(start)
			DefaultMetricMonitor.IncSentCounter()

		case <-e.ctx.Done():
			e.queue.Close()
			return
		}
	}
}

func (e *Exporter) Stop() {
	e.cancel()
	e.wg.Wait()
}
