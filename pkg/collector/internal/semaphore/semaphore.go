// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package semaphore

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var (
	semaphoreAcquired = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "semaphore_acquired_num",
			Help:      "Semaphore acquired number",
		},
		[]string{"name"},
	)

	semaphoreAcquiredSuccess = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "semaphore_acquired_success",
			Help:      "Semaphore acquired success",
		},
		[]string{"name"},
	)

	semaphoreAcquiredFailed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "semaphore_acquired_failed",
			Help:      "Semaphore acquired failed",
		},
		[]string{"name"},
	)

	semaphoreAcquiredDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "semaphore_acquired_duration_seconds",
			Help:      "Semaphore acquired duration seconds",
			Buckets:   define.DefObserveDuration,
		},
		[]string{"name"},
	)
)

var DefaultMetricMonitor = &metricMonitor{}

type metricMonitor struct{}

func (m *metricMonitor) SetSemaphoreAcquired(name string, n int) {
	semaphoreAcquired.WithLabelValues(name).Set(float64(n))
}

func (m *metricMonitor) IncAcquiredSuccessCounter(name string) {
	semaphoreAcquiredSuccess.WithLabelValues(name).Inc()
}

func (m *metricMonitor) IncAcquiredFailedCounter(name string) {
	semaphoreAcquiredFailed.WithLabelValues(name).Inc()
}

func (m *metricMonitor) ObserveAcquiredDuration(t time.Time, name string) {
	semaphoreAcquiredDuration.WithLabelValues(name).Observe(time.Since(t).Seconds())
}

type Semaphore struct {
	name string
	ch   chan struct{}
	done chan struct{}
}

func New(name string, capacity int) *Semaphore {
	sem := &Semaphore{
		name: name,
		ch:   make(chan struct{}, capacity),
		done: make(chan struct{}, 1),
	}
	go sem.record()
	return sem
}

func (s *Semaphore) record() {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			DefaultMetricMonitor.SetSemaphoreAcquired(s.name, len(s.ch))
		}
	}
}

func (s *Semaphore) AcquireWithTimeout(t time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), t)
	defer cancel()

	start := time.Now()
	select {
	case <-ctx.Done():
		DefaultMetricMonitor.ObserveAcquiredDuration(start, s.name)
		DefaultMetricMonitor.IncAcquiredFailedCounter(s.name)
		return false

	case s.ch <- struct{}{}:
		DefaultMetricMonitor.ObserveAcquiredDuration(start, s.name)
		DefaultMetricMonitor.IncAcquiredSuccessCounter(s.name)
		return true
	}
}

func (s *Semaphore) String() string {
	return s.name
}

func (s *Semaphore) Close() {
	close(s.done)
}

func (s *Semaphore) Acquire() {
	s.ch <- struct{}{}
}

func (s *Semaphore) Release() {
	<-s.ch
}

func (s *Semaphore) Count() int {
	return len(s.ch)
}
