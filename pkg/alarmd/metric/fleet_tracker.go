// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// fleetTrackerCollector exports what the replica's fleet object table lost
// because it was full, read from the tracker at scrape.
type fleetTrackerCollector struct {
	mu      sync.Mutex
	source  func() fleet.TrackerDropped
	dropped *prometheus.Desc
}

func newFleetTrackerCollector() *fleetTrackerCollector {
	return &fleetTrackerCollector{dropped: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "fleet_tracker_dropped_total"),
		"Facts the replica's fleet object table dropped because it was full, by what: observation (a new object's "+
			"round never recorded), truncation, range. The table holds four times the objects the replica owns, at "+
			"least 16384; anything here is an object the page and fleet.get cannot speak for, and fleet.get names "+
			"the latest of them. Every kind exists from the start.", []string{"what"}, nil)}
}

// SetFleetTrackerSource binds the collector to the tracker. Safe before or
// after registration; a nil recorder is a no-op.
func (r *Recorder) SetFleetTrackerSource(source func() fleet.TrackerDropped) {
	if r == nil || r.phaseTwo.fleetTracker == nil {
		return
	}
	r.phaseTwo.fleetTracker.mu.Lock()
	r.phaseTwo.fleetTracker.source = source
	r.phaseTwo.fleetTracker.mu.Unlock()
}

func (c *fleetTrackerCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.dropped }

func (c *fleetTrackerCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	var dropped fleet.TrackerDropped
	if source != nil {
		dropped = source()
	}
	for _, kind := range fleet.TrackerDroppedKinds {
		ch <- prometheus.MustNewConstMetric(c.dropped, prometheus.CounterValue, float64(dropped.Counts[kind]), kind)
	}
}
