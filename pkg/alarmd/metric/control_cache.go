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
	// There was a Shared field here, published as result="share" and described
	// by this comment and the metric's HELP as the count of misses served from
	// a read another caller already had in flight. No code ever incremented it.
	// It read zero on every object for the life of the metric, and the only
	// test that exercised the label fed the collector an object name --
	// "snapshot" -- that the production wiring does not emit, so the test was
	// green and the mechanism did not exist.
	//
	// A result label nothing can produce is worse than a missing one. Someone
	// reading zero concludes the coalescing is wired but never hitting and goes
	// to fix that, which is a whole implementation before anything contradicts
	// them -- and for the version header the contradiction is that coalescing
	// across operations is forbidden, not merely absent: the header is how a
	// publication cutover is observed, so serving it from an earlier read
	// misses the cutover. See controlplane/control_version_scope.go.
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
	// Audit is present only for a cached object whose reads are sampled
	// against the store itself; see ControlCacheAudit.
	Audit *ControlCacheAudit
}

// ControlCacheAudit is a sampled reconciliation of a cached object against
// what the store holds. Samples and Agreed count audits; OverNamed and Missed
// count objects, in opposite directions that are never summed: an over-named
// object cost a read it did not need, a missed one served stale content.
type ControlCacheAudit struct {
	Samples   uint64
	Agreed    uint64
	OverNamed uint64
	Missed    uint64
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
	audit      *prometheus.Desc
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
				"is the whole cache dropped at once: for the key segment memos a population outgrew a bound "+
				"its own design assumes it stays inside, and for the activation delta a Worker found a header "+
				"more than one revision past the one it held, so the delta could not speak for the gap.",
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
		audit: prometheus.NewDesc(
			prometheus.BuildFQName(metricNamespace, metricSubsystem, "control_cache_audit_total"),
			"Sampled reconciliations of a cached object against the store, by result. sample counts audits "+
				"run and agreed the ones that found nothing wrong, so a zero elsewhere can be told from no "+
				"audit having run. over_named counts objects the cache dropped that had not changed (a wasted "+
				"read) and missed counts objects it kept that had changed (stale content served); the two are "+
				"opposite failures and are never added together.",
			[]string{"object", "result"}, nil,
		),
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
	ch <- c.audit
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
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.CounterValue, float64(counts.Evictions), counts.Object, "evict")
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.CounterValue, float64(counts.Clears), counts.Object, "clear")
		if audit := counts.Audit; audit != nil {
			ch <- prometheus.MustNewConstMetric(c.audit, prometheus.CounterValue, float64(audit.Samples), counts.Object, "sample")
			ch <- prometheus.MustNewConstMetric(c.audit, prometheus.CounterValue, float64(audit.Agreed), counts.Object, "agreed")
			ch <- prometheus.MustNewConstMetric(c.audit, prometheus.CounterValue, float64(audit.OverNamed), counts.Object, "over_named")
			ch <- prometheus.MustNewConstMetric(c.audit, prometheus.CounterValue, float64(audit.Missed), counts.Object, "missed")
		}
		if counts.Occupancy == nil {
			continue
		}
		ch <- prometheus.MustNewConstMetric(c.entries, prometheus.GaugeValue, counts.Occupancy.Entries, counts.Object)
		ch <- prometheus.MustNewConstMetric(c.bytes, prometheus.GaugeValue, counts.Occupancy.Bytes, counts.Object)
		ch <- prometheus.MustNewConstMetric(c.bytesLimit, prometheus.GaugeValue, counts.Occupancy.BytesLimit, counts.Object)
	}
}
