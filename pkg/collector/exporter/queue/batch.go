// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package queue

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	queueFullTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "exporter_queue_full_total",
			Help:      "Exporter queue full total",
		},
		[]string{"id"},
	)

	queueTickTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "exporter_queue_tick_total",
			Help:      "Exporter queue tick total",
		},
		[]string{"id"},
	)

	queuePopBatchSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "exporter_queue_pop_batch_size",
			Help:      "Exporter queue pop batch size",
			Buckets:   []float64{10, 50, 100, 200, 500, 1000, 2000, 3000, 5000},
		},
		[]string{"record_type", "id"},
	)
)

var DefaultMetricMonitor = &metricMonitor{}

type metricMonitor struct{}

func (m *metricMonitor) IncQueueTickCounter(dataId int32) {
	queueTickTotal.WithLabelValues(strconv.Itoa(int(dataId))).Inc()
}

func (m *metricMonitor) IncQueueFullCounter(dataId int32) {
	queueFullTotal.WithLabelValues(strconv.Itoa(int(dataId))).Inc()
}

func (m *metricMonitor) ObserveQueuePopBatchSizeDistribution(n int, dataId int32, rtype define.RecordType) {
	queuePopBatchSize.WithLabelValues(rtype.S(), strconv.Itoa(int(dataId))).Observe(float64(n))
}

type BatchQueue struct {
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mut     sync.RWMutex
	qs      map[string]chan []define.Event
	out     chan common.MapStr
	conf    Config
	getSize func(string) Config
}

// Config 不同类型的数据大小不同 因此要允许为每种类型单独设置队列批次
type Config struct {
	MetricsBatchSize  int           `config:"metrics_batch_size" mapstructure:"metrics_batch_size"`
	LogsBatchSize     int           `config:"logs_batch_size" mapstructure:"logs_batch_size"`
	TracesBatchSize   int           `config:"traces_batch_size" mapstructure:"traces_batch_size"`
	ProxyBatchSize    int           `config:"proxy_batch_size" mapstructure:"proxy_batch_size"`
	ProfilesBatchSize int           `config:"profiles_batch_size" mapstructure:"profiles_batch_size"`
	FlushInterval     time.Duration `config:"flush_interval" mapstructure:"flush_interval"`
}

func NewBatchQueue(conf Config, fn func(string) Config) Queue {
	ctx, cancel := context.WithCancel(context.Background())
	cq := &BatchQueue{
		ctx:     ctx,
		cancel:  cancel,
		qs:      make(map[string]chan []define.Event),
		out:     make(chan common.MapStr, define.Concurrency()),
		conf:    conf,
		getSize: fn,
	}

	return cq
}

type DataIDChan struct {
	dataID    int32
	batchSize int
	rtype     define.RecordType
	ch        chan []define.Event
}

func (bq *BatchQueue) resize(rtype define.RecordType, token string, backup int) int {
	v := bq.getSize(token)
	batchSize := backup

	switch rtype {
	case define.RecordLogs:
		if v.LogsBatchSize > 0 && batchSize != v.LogsBatchSize {
			batchSize = v.LogsBatchSize
			logger.Infof("resize logs batch, token=%s, prev.size=%d, curr.size=%d", token, backup, batchSize)
		}
	case define.RecordTraces:
		if v.TracesBatchSize > 0 && batchSize != v.TracesBatchSize {
			batchSize = v.TracesBatchSize
			logger.Infof("resize traces batch, token=%s, prev.size=%d, curr.size=%d", token, backup, batchSize)
		}
	case define.RecordProxy:
		if v.ProxyBatchSize > 0 && batchSize != v.ProxyBatchSize {
			batchSize = v.ProxyBatchSize
			logger.Infof("resize proxy batch, token=%s, prev.size=%d, curr.size=%d", token, backup, batchSize)
		}
	case define.RecordProfiles:
		if v.ProfilesBatchSize > 0 && batchSize != v.ProfilesBatchSize {
			batchSize = v.ProfilesBatchSize
			logger.Infof("resize profiles batch, token=%s, prev.size=%d, curr.size=%d", token, backup, batchSize)
		}
	default: // Metrics
		if v.MetricsBatchSize > 0 && batchSize != v.MetricsBatchSize {
			batchSize = v.MetricsBatchSize
			logger.Infof("resize metrics batch, token=%s, prev.size=%d, curr.size=%d", token, backup, batchSize)
		}
	}

	return batchSize
}

func (bq *BatchQueue) compact(dc DataIDChan) {
	bq.wg.Add(1)
	defer bq.wg.Done()

	ticker := time.NewTicker(bq.conf.FlushInterval)
	defer ticker.Stop()

	dynamicBatch := dc.batchSize

	var total int
	data := make([]common.MapStr, 0, dynamicBatch)

	sentOut := func() {
		DefaultMetricMonitor.ObserveQueuePopBatchSizeDistribution(len(data), dc.dataID, dc.rtype)
		switch dc.rtype {
		case define.RecordTraces, define.RecordLogs:
			bq.out <- NewEventsMapStr(dc.dataID, data)
		case define.RecordMetrics, define.RecordPushGateway, define.RecordRemoteWrite, define.RecordTars:
			bq.out <- NewMetricsMapStr(dc.dataID, data)
		case define.RecordProfiles:
			bq.out <- NewProfilesMapStr(dc.dataID, data)
		case define.RecordProxy:
			bq.out <- NewProxyMapStr(dc.dataID, data)

		// 数据不做聚合
		case define.RecordPingserver, define.RecordFta, define.RecordBeat:
			for _, item := range data {
				bq.out <- item
			}
		}

		// 状态置零
		total = 0
		data = make([]common.MapStr, 0, dynamicBatch)
	}

	for {
		select {
		case events := <-dc.ch:
			for _, event := range events {
				data = append(data, event.Data())

				total++
				if total >= dynamicBatch {
					// full 时判断是否需要调整 batch size
					dynamicBatch = bq.resize(dc.rtype, event.Token().Original, dynamicBatch)
					sentOut()
					DefaultMetricMonitor.IncQueueFullCounter(dc.dataID)
				}
			}

		case <-ticker.C:
			if len(data) == 0 {
				continue
			}
			sentOut()
			DefaultMetricMonitor.IncQueueTickCounter(dc.dataID)

		case <-bq.ctx.Done():
			return
		}
	}
}

func (bq *BatchQueue) Pop() <-chan common.MapStr {
	return bq.out
}

func (bq *BatchQueue) Close() {
	bq.cancel()
	bq.wg.Wait()
}

func (bq *BatchQueue) Put(events ...define.Event) {
	if len(events) == 0 {
		return
	}

	dataID := events[0].DataId()
	rtype := events[0].RecordType()
	uk := strconv.Itoa(int(dataID)) + "/" + string(rtype)

	bq.mut.Lock() // read-write-lock
	_, ok := bq.qs[uk]
	var batchSize int
	if !ok {
		switch rtype {
		case define.RecordMetrics, define.RecordPushGateway, define.RecordRemoteWrite:
			batchSize = bq.conf.MetricsBatchSize
		case define.RecordLogs:
			batchSize = bq.conf.LogsBatchSize
		case define.RecordTraces:
			batchSize = bq.conf.TracesBatchSize
		case define.RecordProxy:
			batchSize = bq.conf.ProxyBatchSize
		case define.RecordProfiles:
			batchSize = bq.conf.ProfilesBatchSize
		default:
			batchSize = 100
		}

		ch := make(chan []define.Event, define.Concurrency())
		go bq.compact(DataIDChan{
			dataID:    dataID,
			rtype:     rtype,
			batchSize: batchSize,
			ch:        ch,
		})
		bq.qs[uk] = ch
	}
	q := bq.qs[uk]
	bq.mut.Unlock()

	select {
	case q <- events:
	case <-bq.ctx.Done():
	}
}
