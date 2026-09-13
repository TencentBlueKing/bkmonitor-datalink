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

// Closed vocabulary of snapshot_body_read_total's reader label: the readers
// of the whole snapshot body that remain after the Leader's activation moved
// to the catalog index. Every reader publishes from the start.
const (
	SnapshotBodyReaderActivationContent = "activation_content"
	SnapshotBodyReaderIndexAudit        = "index_audit"
	SnapshotBodyReaderQueryGroup        = "query_group"
	SnapshotBodyReaderPlan              = "plan"
	SnapshotBodyReaderLegacyCleanup     = "legacy_cleanup"
)

// SnapshotBodyReaders lists the readers in the order they are published.
var SnapshotBodyReaders = []string{SnapshotBodyReaderActivationContent, SnapshotBodyReaderIndexAudit,
	SnapshotBodyReaderQueryGroup, SnapshotBodyReaderPlan, SnapshotBodyReaderLegacyCleanup}

// SnapshotBodyReadCounts is one reader's count of whole snapshot body reads.
type SnapshotBodyReadCounts struct {
	Reader string
	Reads  uint64
}

type controlCacheCollector struct {
	mu         sync.Mutex
	source     func() []ControlCacheCounts
	bodySource func() []SnapshotBodyReadCounts
	desc       *prometheus.Desc
	entries    *prometheus.Desc
	bytes      *prometheus.Desc
	bytesLimit *prometheus.Desc
	audit      *prometheus.Desc
	bodyReads  *prometheus.Desc
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
		audit: prometheus.NewDesc(
			prometheus.BuildFQName(metricNamespace, metricSubsystem, "control_cache_audit_total"),
			"Sampled reconciliations of a cached object against the store, by result. sample counts audits "+
				"run and agreed the ones that found nothing wrong, so a zero elsewhere can be told from no "+
				"audit having run. over_named counts objects the cache dropped that had not changed (a wasted "+
				"read) and missed counts objects it kept that had changed (stale content served); the two are "+
				"opposite failures and are never added together.",
			[]string{"object", "result"}, nil,
		),
		bodyReads: prometheus.NewDesc(
			prometheus.BuildFQName(metricNamespace, metricSubsystem, "snapshot_body_read_total"),
			"Reads of the whole snapshot body by the reader that made them, counted at the attempt. Since the "+
				"Leader's activation moved to the catalog index these are the readers that remain: activation_content "+
				"when a previous publication has no manifest, query_group when a Segment names no content or its "+
				"objects are gone, plan, legacy_cleanup for the one-time tool, and index_audit, which reads one "+
				"activation in sixteen by design and is the one reader expected to move. The body can stop being "+
				"written only once every other reader has stayed at zero for a whole cycle; index_audit moving is "+
				"what tells that zero from a counter that is not wired.",
			[]string{"reader"}, nil,
		),
	}
}

// SetSnapshotBodyReadSource binds the collector to the repository's count of
// whole snapshot body reads. Every reader of the closed vocabulary is
// published whether or not a source is bound, so a reader that never read
// shows a zero and not an absence.
func (r *Recorder) SetSnapshotBodyReadSource(source func() []SnapshotBodyReadCounts) {
	if r == nil || r.phaseTwo.controlCache == nil {
		return
	}
	r.phaseTwo.controlCache.mu.Lock()
	r.phaseTwo.controlCache.bodySource = source
	r.phaseTwo.controlCache.mu.Unlock()
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
	ch <- c.bodyReads
}

func (c *controlCacheCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source, bodySource := c.source, c.bodySource
	c.mu.Unlock()
	reads := make(map[string]uint64, len(SnapshotBodyReaders))
	if bodySource != nil {
		for _, counts := range bodySource() {
			reads[counts.Reader] = counts.Reads
		}
	}
	for _, reader := range SnapshotBodyReaders {
		ch <- prometheus.MustNewConstMetric(c.bodyReads, prometheus.CounterValue, float64(reads[reader]), reader)
	}
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
