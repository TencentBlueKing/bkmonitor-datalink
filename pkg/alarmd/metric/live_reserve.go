// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// liveReserveCollector reads the runtime's measured post-query tail at
// scrape time: the reserve every read's deadline is cut at once it is
// published, and every short-of-time judgement with it. Pooled, without a
// Query Group label.
type liveReserveCollector struct {
	mu      sync.Mutex
	source  func() time.Duration
	reserve *prometheus.Desc
}

func newLiveReserveCollector() *liveReserveCollector {
	return &liveReserveCollector{
		reserve: prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, "live_query_reserve_seconds"),
			"This process's measured post-query tail, pooled across its Query Groups: a read's deadline is its "+
				"consumer deadline less this (or a group's own longer tail) instead of the frozen reserve, and a round "+
				"is judged short of time against that deadline. Zero before enough Slots are measured, when the frozen "+
				"reserve applies.", nil, nil),
	}
}

// SetLiveReserveSource binds the live reserve gauge to the runtime's
// measured post-query tail.
func (r *Recorder) SetLiveReserveSource(source func() time.Duration) {
	if r == nil || r.phaseTwo.liveReserve == nil {
		return
	}
	r.phaseTwo.liveReserve.mu.Lock()
	r.phaseTwo.liveReserve.source = source
	r.phaseTwo.liveReserve.mu.Unlock()
}

func (c *liveReserveCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.reserve }

func (c *liveReserveCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	reserve := time.Duration(0)
	if source != nil {
		reserve = source()
	}
	ch <- prometheus.MustNewConstMetric(c.reserve, prometheus.GaugeValue, reserve.Seconds())
}
