// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategycache"
)

// effectiveTimeRefreshCollector exports the effective-time cache's reads by
// result, read from the cache at scrape.
type effectiveTimeRefreshCollector struct {
	mu        sync.Mutex
	source    func() map[string]uint64
	refreshes *prometheus.Desc
}

func newEffectiveTimeRefreshCollector() *effectiveTimeRefreshCollector {
	return &effectiveTimeRefreshCollector{refreshes: prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, "effective_time_refresh_total"),
		"Reads of the business timezones and calendars that legacy effective times are resolved from, by result: ok, "+
			"timeout (the read ran out of the time its caller could spare), error. Only reads that were due count. A "+
			"dependency that keeps failing past the two-minute trust window resolves EFFECTIVE_TIME_UNKNOWN for every "+
			"Plan naming it; fleet.get lists those as effective_time_unread. Every result exists from the start.",
		[]string{"result"}, nil)}
}

// SetEffectiveTimeRefreshSource binds the collector to the cache. Safe
// before or after registration; a nil recorder is a no-op.
func (r *Recorder) SetEffectiveTimeRefreshSource(source func() map[string]uint64) {
	if r == nil || r.phaseTwo.effectiveTimeRefresh == nil {
		return
	}
	r.phaseTwo.effectiveTimeRefresh.mu.Lock()
	r.phaseTwo.effectiveTimeRefresh.source = source
	r.phaseTwo.effectiveTimeRefresh.mu.Unlock()
}

func (c *effectiveTimeRefreshCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.refreshes }

func (c *effectiveTimeRefreshCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	var counts map[string]uint64
	if source != nil {
		counts = source()
	}
	for _, result := range strategycache.EffectiveTimeRefreshResults {
		ch <- prometheus.MustNewConstMetric(c.refreshes, prometheus.CounterValue, float64(counts[result]), result)
	}
}
