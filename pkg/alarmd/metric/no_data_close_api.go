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

	"github.com/prometheus/client_golang/prometheus"
)

// outcomeCounterCollector reports a source's counts by outcome, every
// outcome the source names as a cell from the start.
type outcomeCounterCollector struct {
	mu       sync.Mutex
	words    []string
	outcomes func() map[string]uint64
	desc     *prometheus.Desc
}

func newOutcomeCounterCollector(name, help string) *outcomeCounterCollector {
	return &outcomeCounterCollector{desc: prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, name), help, []string{"outcome"}, nil)}
}

func (c *outcomeCounterCollector) set(words []string, outcomes func() map[string]uint64) {
	c.mu.Lock()
	c.words, c.outcomes = append([]string(nil), words...), outcomes
	c.mu.Unlock()
}

func (c *outcomeCounterCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *outcomeCounterCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	words, outcomes := c.words, c.outcomes
	c.mu.Unlock()
	if outcomes == nil {
		return
	}
	counts := outcomes()
	for _, word := range words {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.CounterValue, float64(counts[word]), word)
	}
}

func newNoDataCloseAPICollector() *outcomeCounterCollector {
	return newOutcomeCounterCollector("no_data_close_requests_total",
		"The alert link's calls of the no-data close route (/internal/v1/no-data-closures), by outcome: recorded; "+
			"full_skipped, answered 200 and not recorded because the strategy's close hash is at its bound (a refusal "+
			"would stop the user's close at the link; skipped, alarmd may raise the absence again); store_unavailable "+
			"(503, the link retries); the refusals method_not_allowed (405), body_unreadable, body_not_object and "+
			"identity_invalid (400). internal_listen_off is 1 when the process serves the route with no "+
			"http.internal_listen, so the route is served nowhere. Every cell exists from the start so a zero is a "+
			"reading and not an absence.")
}

// SetNoDataCloseAPISource binds the route's counts. Safe before or after
// registration; a nil recorder is a no-op.
func (r *Recorder) SetNoDataCloseAPISource(words []string, outcomes func() map[string]uint64) {
	if r == nil || r.phaseTwo.noDataCloseAPI == nil {
		return
	}
	r.phaseTwo.noDataCloseAPI.set(words, outcomes)
}
