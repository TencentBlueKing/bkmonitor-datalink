// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

// RetainedReservationSource reads, at scrape time, the retained bytes the
// Slots running on this replica hold reserved against the shared pool.
type RetainedReservationSource func() uint64

// retainedReservationCollector answers "how full is the retained-byte pool
// right now": the reservation outstanding at the scrape, under the budget
// label capacity_budget carries the pool's ceiling with, so usage over limit
// is one division. The sum of per-object peaks is an upper bound on a
// simultaneous peak, and the completion rows report a Slot's share after it
// has finished; neither says what the pool holds while Slots are admitting.
type retainedReservationCollector struct {
	source   RetainedReservationSource
	reserved *prometheus.Desc
}

func newRetainedReservationCollector(source RetainedReservationSource) *retainedReservationCollector {
	return &retainedReservationCollector{
		source: source,
		reserved: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "capacity_reserved"),
			"What the shared pool has handed out at the moment of the scrape, labelled the way "+
				"capacity_budget labels its ceiling, so capacity_reserved / capacity_budget is the pool's "+
				"usage. Only retained_bytes is reported. An instant, not an average: a scrape between two "+
				"rounds reads what is held then, and a pool can refuse a Slot between two scrapes that "+
				"both read low. Read refusals from capacity_transition_total, not from this.",
			[]string{"budget"}, nil),
	}
}

func (c *retainedReservationCollector) Describe(descriptions chan<- *prometheus.Desc) {
	descriptions <- c.reserved
}

func (c *retainedReservationCollector) Collect(metrics chan<- prometheus.Metric) {
	metrics <- prometheus.MustNewConstMetric(c.reserved, prometheus.GaugeValue, float64(c.source()), "retained_bytes")
}

// BindRetainedReservation registers the pool's reservation reading. Bound
// once, to the coordinator that owns the pool.
func (r *Recorder) BindRetainedReservation(source RetainedReservationSource) error {
	if r == nil || r.registry == nil {
		return errors.New("metric: initialized recorder is required")
	}
	if source == nil {
		return errors.New("metric: retained reservation source is required")
	}
	r.retainedMu.Lock()
	defer r.retainedMu.Unlock()
	if r.retainedBound {
		return errors.New("metric: retained reservation source is already bound")
	}
	if err := r.registry.Register(newRetainedReservationCollector(source)); err != nil {
		return err
	}
	r.retainedBound = true
	return nil
}
