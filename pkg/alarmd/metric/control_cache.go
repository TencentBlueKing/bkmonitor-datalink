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
}

type controlCacheCollector struct {
	mu     sync.Mutex
	source func() []ControlCacheCounts
	desc   *prometheus.Desc
}

func newControlCacheCollector() *controlCacheCollector {
	return &controlCacheCollector{
		desc: prometheus.NewDesc(
			prometheus.BuildFQName(metricNamespace, metricSubsystem, "control_cache_total"),
			"Control plane read cache outcomes by cached object and result.",
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
	}
}
