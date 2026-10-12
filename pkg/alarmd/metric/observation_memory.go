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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/memoryline"
)

// ObservationMemorySource reads the observation memory line at scrape time.
type ObservationMemorySource func() memoryline.Reading

// observationMemoryCollector is the line observation memory grows under:
// how far the process is from it, and, by consumer, what was admitted and
// how often a consumer was refused.
type observationMemoryCollector struct {
	source     ObservationMemorySource
	headroom   *prometheus.Desc
	refused    *prometheus.Desc
	admitted   *prometheus.Desc
	held       *prometheus.Desc
	budgetSize *prometheus.Desc
	budgetHeld *prometheus.Desc
}

func newObservationMemoryCollector(source ObservationMemorySource) *observationMemoryCollector {
	return &observationMemoryCollector{
		source: source,
		headroom: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "observation_memory_headroom_bytes"),
			"The Go soft memory limit less the live heap after the last collection and what the detection budgets "+
				"(the Slots' retained bytes, the control caches) may still take: the room observation may grow into. "+
				"Negative is past the line, where every observation consumer takes no more. Admission also takes out "+
				"what observation was granted since that collection.", nil, nil),
		refused: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "observation_memory_refused_total"),
			"Asks for more memory the line refused, by consumer: cost_summary (groups past the refusal are not "+
				"tracked), cost_projection (the refresh reads no projections), series_sampler (a new sample window "+
				"gets no buffer), lookback (the read stops its per-series sums and the sample is unclassified), "+
				"fleet_rounds (an object whose rounds are full keeps them at what they hold and lets its oldest go; "+
				"asked again each round while full, so this counts objects times rounds), "+
				"fleet_restore (the record and every one after it are left for the next publish, spending no attempt), "+
				"diagnosis_progress (the page's objects from that record on are PROGRESS_DEFERRED), "+
				"fleet_view (the view is not read and is the gap SNAPSHOTS_DEFERRED). "+
				"Zero in normal running.", []string{"consumer"}, nil),
		admitted: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "observation_memory_admitted_bytes_total"),
			"Bytes the line admitted, by consumer: what each consumer's growth asked for and got. A grant to state "+
				"that stays is not given back - what it took is in the live heap from the next collection on; a hold, "+
				"memory taken for one piece of work (a page's snapshots and view), is given back when the work is done "+
				"(observation_memory_held_bytes).", []string{"consumer"}, nil),
		held: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "observation_memory_held_bytes"),
			"What observation holds now for work in progress, admitted by the line and counted against it until "+
				"the work is done and lets it go. Zero between pages; one that stays above zero is a hold never "+
				"released.", nil, nil),
		budgetSize: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "observation_memory_budget_size_bytes"),
			"A detection budget's size as the line reads it, by budget: retained (the Slots' retained-bytes "+
				"ceiling), timeline_cache and object_cache (each cache's working set - what it holds and an entry "+
				"at its largest charge for each one a reader is about to store, up to its ceiling - the object "+
				"cache charged decoded). Its size less its held bytes is what it holds observation off with; a "+
				"cache whose size stays well above what it holds between reads is a reader that did not settle.",
			[]string{"budget"}, nil),
		budgetHeld: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "observation_memory_budget_held_bytes"),
			"What a detection budget holds now, by budget, in the charge its size is in.", []string{"budget"}, nil),
	}
}

func (c *observationMemoryCollector) Describe(descriptions chan<- *prometheus.Desc) {
	descriptions <- c.headroom
	descriptions <- c.refused
	descriptions <- c.admitted
	descriptions <- c.held
	descriptions <- c.budgetSize
	descriptions <- c.budgetHeld
}

func (c *observationMemoryCollector) Collect(metrics chan<- prometheus.Metric) {
	reading := c.source()
	metrics <- prometheus.MustNewConstMetric(c.headroom, prometheus.GaugeValue, float64(reading.HeadroomBytes))
	metrics <- prometheus.MustNewConstMetric(c.held, prometheus.GaugeValue, float64(reading.HeldBytes))
	for _, consumer := range memoryline.Consumers {
		metrics <- prometheus.MustNewConstMetric(c.refused, prometheus.CounterValue, float64(reading.RefusedTotal[consumer]), string(consumer))
		metrics <- prometheus.MustNewConstMetric(c.admitted, prometheus.CounterValue, float64(reading.AdmittedBytes[consumer]), string(consumer))
	}
	for _, budget := range reading.Budgets {
		metrics <- prometheus.MustNewConstMetric(c.budgetSize, prometheus.GaugeValue, float64(budget.SizeBytes), budget.Name)
		metrics <- prometheus.MustNewConstMetric(c.budgetHeld, prometheus.GaugeValue, float64(budget.HeldBytes), budget.Name)
	}
}

// BindObservationMemory registers the observation memory line's reading.
// Bound once, to the line the bundle's consumers ask.
func (r *Recorder) BindObservationMemory(source ObservationMemorySource) error {
	if r == nil || r.registry == nil {
		return errors.New("metric: initialized recorder is required")
	}
	if source == nil {
		return errors.New("metric: observation memory source is required")
	}
	return r.registry.Register(newObservationMemoryCollector(source))
}
