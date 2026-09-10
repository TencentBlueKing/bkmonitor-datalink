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

// CapacityLoad is what the process was given and what it is using.
//
// alarmd derives its entire capacity profile from the container it was handed,
// and until now exported none of it. A budget rejection could be seen without
// its ceiling, and the ceiling was only printed at startup -- so reading it off
// a running deployment meant getting into the Pod.
type CapacityLoad struct {
	// Budgets are the derived per-Slot ceilings, keyed by the same names
	// capacity_transition_total uses for its budget label. They must stay the
	// same names: joining a rejection to the limit it hit is the entire point.
	Budgets map[string]float64
	// MemoryLimitBytes is the denominator every budget above was derived from.
	MemoryLimitBytes uint64
	// MemorySource says where the limit came from, because a limit read outside
	// a Pod is a fallback and must not be presented as the container's.
	MemorySource string
	CPUSource    string
	CPUCores     int

	MemoryUsedBytes  uint64
	MemoryUsedKnown  bool
	ThrottledSeconds float64
	ThrottledKnown   bool
}

// CapacityLoadSource reads the profile and current usage at scrape time.
type CapacityLoadSource func() CapacityLoad

type capacityLoadCollector struct {
	source      CapacityLoadSource
	budget      *prometheus.Desc
	memoryLimit *prometheus.Desc
	memoryUsed  *prometheus.Desc
	cpuCores    *prometheus.Desc
	throttled   *prometheus.Desc
}

func newCapacityLoadCollector(source CapacityLoadSource) *capacityLoadCollector {
	descriptor := func(name, help string, labels []string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, name), help, labels, nil)
	}
	return &capacityLoadCollector{
		source: source,
		budget: descriptor("capacity_budget",
			"The derived ceiling for each per-Slot budget, labelled the same way capacity_transition_total "+
				"labels its rejections, so a rejection can be read against the limit it hit. These are "+
				"per-Slot caps, not a pool: a Slot above its cap completes UNAVAILABLE, and the right "+
				"reading is 'how close did the largest Slot get', never a utilization ratio.",
			[]string{"budget"}),
		memoryLimit: descriptor("container_memory_limit_bytes",
			"The memory limit every budget above was derived from, labelled with where it was read. A "+
				"source of fallback_default means no container limit was found and the budgets describe a "+
				"guess rather than this deployment.",
			[]string{"source"}),
		memoryUsed: descriptor("container_memory_used_bytes",
			"Memory charged to the container's cgroup. This is not the Go heap: the two differ by "+
				"everything the heap does not account for, and it is this number that gets the process "+
				"killed. Absent when it cannot be read, which is not the same as zero.",
			nil),
		cpuCores: descriptor("container_cpu_cores",
			"CPU budget the process is running against, labelled with where it was resolved from.",
			[]string{"source"}),
		throttled: descriptor("container_cpu_throttled_seconds_total",
			"Cumulative CFS throttling. It separates 'busy' from 'not allowed to run', which no busy-time "+
				"metric can: a process sitting at its quota looks identical to an idle one being held back. "+
				"Absent when it cannot be read.",
			nil),
	}
}

func (c *capacityLoadCollector) Describe(descriptions chan<- *prometheus.Desc) {
	descriptions <- c.budget
	descriptions <- c.memoryLimit
	descriptions <- c.memoryUsed
	descriptions <- c.cpuCores
	descriptions <- c.throttled
}

func (c *capacityLoadCollector) Collect(metrics chan<- prometheus.Metric) {
	load := c.source()
	// Only the budgets capacity_transition_total can report are published. A
	// ceiling with no matching rejection label would be a number nothing can be
	// joined to, and the label set stays closed either way.
	for _, budget := range phaseTwoBudgets {
		if budget == "other" {
			continue
		}
		metrics <- prometheus.MustNewConstMetric(
			c.budget, prometheus.GaugeValue, load.Budgets[budget], budget)
	}
	metrics <- prometheus.MustNewConstMetric(
		c.memoryLimit, prometheus.GaugeValue, float64(load.MemoryLimitBytes), boundedSource(load.MemorySource))
	metrics <- prometheus.MustNewConstMetric(
		c.cpuCores, prometheus.GaugeValue, float64(load.CPUCores), boundedSource(load.CPUSource))
	// Unreadable is published as absent rather than as zero. Zero memory in use
	// and "this is not a container" must not look the same.
	if load.MemoryUsedKnown {
		metrics <- prometheus.MustNewConstMetric(c.memoryUsed, prometheus.GaugeValue, float64(load.MemoryUsedBytes))
	}
	if load.ThrottledKnown {
		metrics <- prometheus.MustNewConstMetric(c.throttled, prometheus.CounterValue, load.ThrottledSeconds)
	}
}

// capacitySources is closed for the same reason every other label set here is:
// a value invented upstream would silently widen the family.
var capacitySources = []string{
	"pod_limit", "cgroup_v2", "cgroup_v1", "fallback_default", "product_reference",
	"environment_override", "runtime",
}

func boundedSource(source string) string {
	for _, known := range capacitySources {
		if source == known {
			return source
		}
	}
	return "other"
}

// BindCapacityLoad registers the capacity and container load collector.
func (r *Recorder) BindCapacityLoad(source CapacityLoadSource) error {
	if r == nil || r.registry == nil {
		return errors.New("metric: initialized recorder is required")
	}
	if source == nil {
		return errors.New("metric: capacity load source is required")
	}
	r.capacityLoadMu.Lock()
	defer r.capacityLoadMu.Unlock()
	if r.capacityLoadBound {
		return errors.New("metric: capacity load source is already bound")
	}
	if err := r.registry.Register(newCapacityLoadCollector(source)); err != nil {
		return err
	}
	r.capacityLoadBound = true
	return nil
}
