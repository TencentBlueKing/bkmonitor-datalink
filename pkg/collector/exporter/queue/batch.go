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
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter/sizeobserver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	queueFullTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "exporter_queue_full_total",
			Help:      "Exporter queue full total",
		},
		[]string{"id"},
	)

	queueTickTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "exporter_queue_tick_total",
			Help:      "Exporter queue tick total",
		},
		[]string{"id"},
	)

	queuePopBatchSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "exporter_queue_pop_batch_size",
			Help:      "Exporter queue pop batch size",
			Buckets:   []float64{50, 100, 200, 500, 1000, 2000, 3000, 5000, 8000, 10000, 20000},
		},
		[]string{"record_type", "id"},
	)

	queueMaxBatch = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "exporter_queue_max_batch",
			Help:      "Exporter queue max batch",
		},
		[]string{"id"},
	)
)

func init() {
	prometheus.MustRegister(
		queueFullTotal,
		queueTickTotal,
		queuePopBatchSize,
		queueMaxBatch,
	)
}

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

func (m *metricMonitor) SetQueueMaxBatch(n int, dataId int32) {
	queueMaxBatch.WithLabelValues(strconv.Itoa(int(dataId))).Set(float64(n))
}

const maxBatchBytes = 6 * 1024 * 1024 // 6MB

type BatchQueue struct {
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	mut            sync.RWMutex
	qs             map[int32]chan []define.Event
	out            chan common.MapStr
	conf           Config
	so             *sizeobserver.SizeObserver
	resizeInterval time.Duration
}

func NewBatchQueue(conf Config, so *sizeobserver.SizeObserver) *BatchQueue {
	ctx, cancel := context.WithCancel(context.Background())
	cq := &BatchQueue{
		ctx:    ctx,
		cancel: cancel,
		qs:     make(map[int32]chan []define.Event),
		out:    make(chan common.MapStr, define.Concurrency()),
		conf:   conf,
		so:     so,
	}

	return cq
}

type DataIDChan struct {
	dataID    int32
	batchSize int
	rtype     define.RecordType
	ch        chan []define.Event
}

func (bq *BatchQueue) compact(dc DataIDChan) {
	bq.wg.Add(1)
	defer bq.wg.Done()

	flushTicker := time.NewTicker(bq.conf.FlushInterval)
	defer flushTicker.Stop()

	dymBatch := dc.batchSize

	var total int
	data := make([]common.MapStr, 0)

	sentOut := func() {
		DefaultMetricMonitor.ObserveQueuePopBatchSizeDistribution(len(data), dc.dataID, dc.rtype)
		switch dc.rtype {
		case define.RecordTraces, define.RecordLogs:
			bq.out <- NewEventsMapStr(dc.dataID, data)
		case define.RecordMetrics, define.RecordPushGateway, define.RecordRemoteWrite:
			bq.out <- NewMetricsMapStr(dc.dataID, data)

		// proxy/pingserver 数据不做聚合（没办法做聚合
		case define.RecordProxy, define.RecordPingserver:
			for _, item := range data {
				bq.out <- item
			}
		}

		// 状态置零
		total = 0
		data = make([]common.MapStr, 0)
	}

	resizeInterval := bq.resizeInterval
	if bq.resizeInterval <= 0 {
		resizeInterval = time.Minute
	}
	resizeTicker := time.NewTicker(resizeInterval)
	defer resizeTicker.Stop()

	for {
		select {
		case events := <-dc.ch:
			for _, event := range events {
				data = append(data, event.Data())
				total++
				if total >= dymBatch {
					sentOut()
					DefaultMetricMonitor.IncQueueFullCounter(dc.dataID)
				}
			}

		case <-resizeTicker.C:
			if bq.so == nil {
				continue
			}
			size := bq.so.Get(dc.dataID)
			if size <= 0 {
				continue
			}

			// 经过多轮调整 batch 最终会靠向 maxBatchBytes（有一定波动范围）
			// 从数学角度上 属于升得慢 降得快
			if int(math.Ceil(float64(size)*1.1)) < maxBatchBytes {
				dymBatch = int(math.Ceil(float64(dymBatch) * 1.1)) // 逐步调大 batch, factor 为 1.1
				logger.Debugf("next round(up) batch=%d, dataID=%d", dymBatch, dc.dataID)
			} else {
				dymBatch = int(math.Ceil(float64(dymBatch) * 0.9)) // 逐步调小 batch, factor 为 0.9
				logger.Debugf("next round(down) batch=%d, dataID=%d", dymBatch, dc.dataID)
			}
			DefaultMetricMonitor.SetQueueMaxBatch(dymBatch, dc.dataID)

		case <-flushTicker.C:
			if len(data) <= 0 {
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
	if len(events) <= 0 {
		return
	}

	dataID := events[0].DataId()
	rtype := events[0].RecordType()

	bq.mut.Lock() // read-write-lock
	_, ok := bq.qs[dataID]
	var batchSize int
	if !ok {
		switch rtype {
		case define.RecordMetrics, define.RecordPushGateway, define.RecordRemoteWrite:
			batchSize = bq.conf.MetricsBatchSize
		case define.RecordLogs:
			batchSize = bq.conf.LogsBatchSize
		case define.RecordTraces:
			batchSize = bq.conf.TracesBatchSize
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
		bq.qs[dataID] = ch
	}
	q := bq.qs[dataID]
	bq.mut.Unlock()

	select {
	case q <- events:
	case <-bq.ctx.Done():
	}
}
