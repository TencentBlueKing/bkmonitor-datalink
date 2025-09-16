// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package serieslimiter

import (
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var (
	seriesExceededTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "series_limiter_exceeded_total",
			Help:      "Series limiter exceeded total",
		},
		[]string{"record_type", "id"},
	)

	seriesTotal = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "series_limiter_count",
			Help:      "Series limiter count",
		},
		[]string{"record_type", "id"},
	)

	addedSeriesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "series_limiter_added_total",
			Help:      "Series limiter added total",
		},
		[]string{"record_type", "id"},
	)
)

var DefaultMetricMonitor = &metricMonitor{}

type metricMonitor struct{}

func (m *metricMonitor) IncSeriesExceededCounter(dataId int32) {
	seriesExceededTotal.WithLabelValues(define.RecordMetrics.S(), strconv.Itoa(int(dataId))).Inc()
}

func (m *metricMonitor) SetSeriesCount(dataId int32, n int) {
	seriesTotal.WithLabelValues(define.RecordMetrics.S(), strconv.Itoa(int(dataId))).Set(float64(n))
}

func (m *metricMonitor) IncAddedSeriesCounter(dataId int32) {
	addedSeriesTotal.WithLabelValues(define.RecordMetrics.S(), strconv.Itoa(int(dataId))).Inc()
}

type Limiter struct {
	mut        sync.RWMutex
	recorders  map[int32]*recorder
	maxSeries  int
	gcInterval time.Duration
}

func New(maxSeries int, gcInterval time.Duration) *Limiter {
	return &Limiter{
		recorders:  map[int32]*recorder{},
		maxSeries:  maxSeries,
		gcInterval: gcInterval,
	}
}

func (l *Limiter) Stop() {
	l.mut.Lock()
	defer l.mut.Unlock()

	for _, r := range l.recorders {
		r.Stop()
	}
}

func (l *Limiter) Set(dataID int32, hash uint64) bool {
	var r *recorder

	// 先尝试使用读锁获取 速度更快
	l.mut.RLock()
	if _, ok := l.recorders[dataID]; ok {
		r = l.recorders[dataID]
	}
	l.mut.RUnlock()
	if r != nil {
		return r.Set(hash)
	}

	// 写锁保护 确保执行流一致性
	l.mut.Lock()
	if v, ok := l.recorders[dataID]; ok {
		r = v
	} else {
		r = newRecorder(dataID, l.maxSeries, l.gcInterval)
		l.recorders[dataID] = r
	}
	l.mut.Unlock()

	return r.Set(hash)
}
