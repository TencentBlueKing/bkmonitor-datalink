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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter/queue"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/wait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/metricmeta"
)

func init() {
	gse.MarshalFunc = json.Marshal
}

type Exporter struct {
	metricMetadata  *metricmeta.Exporter
	lifecycleMu     sync.Mutex
	started, closed bool
	stopOnce        sync.Once
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	converter       converter.Converter
	queue           queue.Queue
	cfg             *Config
	batches         map[string]queue.Config // 无并发读写 无需锁保护
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
	c := &Config{MetricMetadata: MetricMetadataConfig{Enabled: true, Config: metricmeta.DefaultConfig(), InfoRetention: 2 * time.Minute}}
	if err := conf.UnpackChild(define.ConfigFieldExporter, c); err != nil {
		return nil, err
	}
	c.Validate()
	logger.Infof("exporter config: %+v", c)

	// 注册 gse output hook
	gse.RegisterSendHook(func(dataID int32, n float64) bool {
		DefaultMetricMonitor.ObserveBeatSentBytes(dataID, n)
		return n < float64(c.MaxMessageBytes)
	})

	ctx, cancel := context.WithCancel(context.Background())
	var metadata *metricmeta.Exporter
	var observer metricmeta.Observer
	if c.MetricMetadata.Enabled {
		var err error
		metadata, err = newMetricMetadataExporter(c.MetricMetadata)
		if err != nil {
			cancel()
			return nil, err
		}
		observer = metadata.Store()
		metadataStats.Store(metadata.Store())
	}
	if metadata == nil {
		metadataStats.Store(nil)
	}
	if !c.MetricMetadata.Enabled || c.MetricMetadata.Endpoint != "" {
		metadataInfo.Store(nil)
	}
	exp := &Exporter{
		ctx:            ctx,
		cancel:         cancel,
		converter:      converter.NewCommonConverterWithMetadata(&c.Converter, observer),
		metricMetadata: metadata,
		cfg:            c,
		batches:        LoadConfigFrom(conf),
	}
	exp.queue = queue.NewBatchQueue(c.Queue, func(s string) queue.Config {
		return exp.batches[s]
	})
	return exp, nil
}

func (e *Exporter) Start() error {
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	if e.closed {
		return metricmeta.ErrClosed
	}
	if e.started {
		return nil
	}
	if e.metricMetadata != nil {
		if err := e.metricMetadata.Start(); err != nil {
			return err
		}
	}
	e.started = true
	logger.Info("exporter start working...")

	for i := 0; i < define.Concurrency(); i++ {
		for _, consume := range []func(){e.consumeRecords, e.consumeEvents, e.sendEvents} {
			e.wg.Add(1)
			go func(consume func()) {
				defer e.wg.Done()
				wait.Until(e.ctx, consume)
			}(consume)
		}
	}
	return nil
}

func (e *Exporter) Reload(conf *confengine.Config) {
	e.batches = LoadConfigFrom(conf)
}

func (e *Exporter) consumeEvents() {
	for {
		select {
		case events := <-globalEvents.Get():
			if len(events) == 0 {
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
	for {
		select {
		case event := <-e.queue.Pop():
			start := time.Now()
			SentFunc(event)
			DefaultMetricMonitor.ObserveSentDuration(start)
			DefaultMetricMonitor.IncSentCounter()

		case <-e.ctx.Done():
			e.queue.Close()
			return
		}
	}
}

func (e *Exporter) Stop() {
	e.stopOnce.Do(func() {
		e.lifecycleMu.Lock()
		e.closed = true
		e.lifecycleMu.Unlock()
		e.converter.Clean()
		e.cancel()
		e.wg.Wait()
		if e.metricMetadata != nil {
			if err := e.metricMetadata.Stop(); err != nil {
				logger.Warnf("stop metric metadata exporter: %v", err)
			}
		}
	})
}
