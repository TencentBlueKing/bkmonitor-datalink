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

// QueryPermitOccupancy is how much of the query permit budget is in use, as the
// scheduler reports it. The metric package restates the shape rather than
// importing the scheduler so the dependency keeps pointing one way.
type QueryPermitOccupancy struct {
	Inflight       map[string]int
	Waiting        map[string]int
	HeldSeconds    map[string]float64
	Budget         int
	RecoveryBudget int
}

// QueryPermitOccupancySource reads occupancy at scrape time.
type QueryPermitOccupancySource func() QueryPermitOccupancy

// queryPermitCollector answers "how full is the query permit budget".
//
// It replaces a gauge that was set from whatever permit event happened last.
// That gauge was wrong in a specific way rather than merely imprecise: permit
// events are the moments the count changes, so sampling only at those moments
// reports the boundary rather than the interval, and in production it never
// read above one while thousands of permits were granted each minute.
type queryPermitCollector struct {
	source      QueryPermitOccupancySource
	inflight    *prometheus.Desc
	waiting     *prometheus.Desc
	heldSeconds *prometheus.Desc
	budget      *prometheus.Desc
}

func newQueryPermitCollector(source QueryPermitOccupancySource) *queryPermitCollector {
	descriptor := func(name, help string, labels []string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, name), help, labels, nil)
	}
	return &queryPermitCollector{
		source: source,
		inflight: descriptor("worker_query_permits_held",
			"Query permits held at the moment of the scrape, by operation. This is an instant, "+
				"not an average: a scrape that lands between two queries reports what is running then, "+
				"which is why it must NOT be used to judge whether the budget is full. Use the rate of "+
				"worker_query_permit_seconds_total for that.",
			[]string{"kind"}),
		waiting: descriptor("worker_query_permits_waiting",
			"Callers queued for a permit at the moment of the scrape, by queue. Non-zero means the "+
				"budget is the constraint right now; zero does NOT mean it has spare room, because a "+
				"caller that is admitted immediately never appears here.",
			[]string{"queue"}),
		heldSeconds: descriptor("worker_query_permit_seconds_total",
			"Cumulative permit-hold time by operation, including permits still held. Its rate over a "+
				"window is the mean number of permits occupied in that window, so rate divided by "+
				"worker_query_permit_budget is the fraction of the budget in use. Permits that are never "+
				"released keep raising it, which is the intended behaviour: a stuck query should look "+
				"like permanent occupancy rather than disappear.",
			[]string{"kind"}),
		budget: descriptor("worker_query_permit_budget",
			"The configured permit ceiling, exported beside the occupancy so a reader does not need "+
				"the deployment's configuration to know what the occupancy is out of.",
			[]string{"queue"}),
	}
}

func (c *queryPermitCollector) Describe(descriptions chan<- *prometheus.Desc) {
	descriptions <- c.inflight
	descriptions <- c.waiting
	descriptions <- c.heldSeconds
	descriptions <- c.budget
}

func (c *queryPermitCollector) Collect(metrics chan<- prometheus.Metric) {
	occupancy := c.source()
	// Every operation is published even at zero. An absent series and a zero
	// series read the same on a dashboard but not in an alert: "no retries are
	// running" and "retries stopped being reported" need to be distinguishable.
	for _, kind := range phaseTwoQueryInflightKinds {
		metrics <- prometheus.MustNewConstMetric(
			c.inflight, prometheus.GaugeValue, float64(occupancy.Inflight[kind]), kind)
		metrics <- prometheus.MustNewConstMetric(
			c.heldSeconds, prometheus.CounterValue, occupancy.HeldSeconds[kind], kind)
	}
	for _, queue := range []string{"normal", "recovery"} {
		metrics <- prometheus.MustNewConstMetric(
			c.waiting, prometheus.GaugeValue, float64(occupancy.Waiting[queue]), queue)
	}
	metrics <- prometheus.MustNewConstMetric(
		c.budget, prometheus.GaugeValue, float64(occupancy.Budget), "normal")
	metrics <- prometheus.MustNewConstMetric(
		c.budget, prometheus.GaugeValue, float64(occupancy.RecoveryBudget), "recovery")
}

// BindQueryPermits registers the permit occupancy collector. Bound once: two
// sources would be two answers to "is the budget full", and the whole point of
// this family is that there is one.
func (r *Recorder) BindQueryPermits(source QueryPermitOccupancySource) error {
	if r == nil || r.registry == nil {
		return errors.New("metric: initialized recorder is required")
	}
	if source == nil {
		return errors.New("metric: query permit occupancy source is required")
	}
	r.queryPermitMu.Lock()
	defer r.queryPermitMu.Unlock()
	if r.queryPermitBound {
		return errors.New("metric: query permit occupancy source is already bound")
	}
	if err := r.registry.Register(newQueryPermitCollector(source)); err != nil {
		return err
	}
	r.queryPermitBound = true
	return nil
}
