// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// The control read cache already counts hits, misses and refreshes per cached
// object, and its own type comment names the counter it was meant to feed, but
// nothing ever consumed the snapshot. That left the one gate whose occupancy
// decides how much a decoded-timeline cache would save unobservable. A hit is
// not free today: the cached bytes are still unmarshalled and validated on
// every hit, and that path is the largest single CPU consumer in the process,
// so the hit rate is exactly what sizes the fix.
type ControlCacheCounts struct {
	Object    string
	Hits      uint64
	Misses    uint64
	Refreshes uint64
	// Shared is a miss or refresh that took the body from a complete read
	// another caller of this process already had in flight. It is what says
	// the coalescing works: bodies actually read are misses plus refreshes
	// minus shared, and a follower whose workers all miss a new revision at
	// once should show shared climbing with misses while reads stay at one.
	Shared    uint64
	Evictions uint64
	// Clears is an all-or-nothing drop, which is a different fact from an
	// eviction: an evicting cache is working inside its budget, while a clearing
	// one has been told its population would stay inside a bound and found that
	// it did not.
	Clears uint64
	// Occupancy is set only for a cached object whose size is bounded by a
	// budget derived from the container. Absent for the rest, because a
	// ceiling reported as zero would read as a cache that can hold nothing.
	Occupancy *ControlCacheOccupancy
}

// ControlCacheOccupancy is what one cached object holds against what it is
// allowed to hold. The budget is derived from the container's memory limit, so
// it is a formula, and a formula that cannot be read against the workload it
// was sized for cannot be corrected: the constant these replaced spent months
// evicting the working set with nothing to show it.
type ControlCacheOccupancy struct {
	Entries    float64
	Bytes      float64
	BytesLimit float64
}

type controlCacheCollector struct {
	mu         sync.Mutex
	source     func() []ControlCacheCounts
	desc       *prometheus.Desc
	entries    *prometheus.Desc
	bytes      *prometheus.Desc
	bytesLimit *prometheus.Desc
}

func newControlCacheCollector() *controlCacheCollector {
	descriptor := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(
			prometheus.BuildFQName(metricNamespace, metricSubsystem, name), help, []string{"object"}, nil)
	}
	return &controlCacheCollector{
		desc: prometheus.NewDesc(
			prometheus.BuildFQName(metricNamespace, metricSubsystem, "control_cache_total"),
			"Control plane read cache outcomes by cached object and result. An evict is an entry "+
				"dropped to stay inside the byte budget; evictions rising while refreshes stay at zero "+
				"means the budget cannot hold the working set, not that the control plane changed. A clear "+
				"is the whole cache dropped at once, which only the key segment memos do: any clear at all "+
				"means a population outgrew a bound its own design assumes it stays inside. A share is a "+
				"miss or refresh served from a complete read another caller of this process already had "+
				"in flight, so bodies actually read are miss plus refresh minus share.",
			[]string{"object", "result"}, nil,
		),
		entries: descriptor("control_cache_entries",
			"Objects currently cached. For timelines, read it against worker_owned_query_groups: the "+
				"cache is meant to hold one per Query Group this Worker owns."),
		bytes: descriptor("control_cache_bytes",
			"Heap charged to the cached objects. Timelines are charged the decoded object they hold, "+
				"which measures at 9/8 of the payload it was decoded from."),
		bytesLimit: descriptor("control_cache_bytes_limit",
			"The derived ceiling for the same, a share of the container's memory limit. It is a "+
				"ceiling and not a target: occupancy well below it is the cache holding a working set "+
				"smaller than the container allows for."),
	}
}

// SetControlCacheSource binds the collector to the repository snapshot. It is
// safe to call before or after registration and a nil recorder is a no-op, so
// wiring never has to be ordered against metric construction.
func (r *Recorder) SetControlCacheSource(source func() []ControlCacheCounts) {
	if r == nil || r.phaseTwo.controlCache == nil {
		return
	}
	r.phaseTwo.controlCache.mu.Lock()
	r.phaseTwo.controlCache.source = source
	r.phaseTwo.controlCache.mu.Unlock()
}

func (c *controlCacheCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
	ch <- c.entries
	ch <- c.bytes
	ch <- c.bytesLimit
}

func (c *controlCacheCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		return
	}
	for _, counts := range source() {
		if counts.Object == "" {
			continue
		}
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.CounterValue, float64(counts.Hits), counts.Object, "hit")
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.CounterValue, float64(counts.Misses), counts.Object, "miss")
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.CounterValue, float64(counts.Refreshes), counts.Object, "refresh")
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.CounterValue, float64(counts.Shared), counts.Object, "share")
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.CounterValue, float64(counts.Evictions), counts.Object, "evict")
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.CounterValue, float64(counts.Clears), counts.Object, "clear")
		if counts.Occupancy == nil {
			continue
		}
		ch <- prometheus.MustNewConstMetric(c.entries, prometheus.GaugeValue, counts.Occupancy.Entries, counts.Object)
		ch <- prometheus.MustNewConstMetric(c.bytes, prometheus.GaugeValue, counts.Occupancy.Bytes, counts.Object)
		ch <- prometheus.MustNewConstMetric(c.bytesLimit, prometheus.GaugeValue, counts.Occupancy.BytesLimit, counts.Object)
	}
}
