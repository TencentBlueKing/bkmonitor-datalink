// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// LocalViewCounts is the size of the control content this Worker holds for
// the Query Groups it owns, as the Worker itself measured it from the bytes
// it read. decision-016 sizes the view stream, the reconnect burst and the
// per-Worker send buffer by this number; before it was on the page it could
// only be computed off Redis by a script run by hand, and the design carried
// a 256 KiB guess for a year that the first reading put at 6.5 MB.
type LocalViewCounts struct {
	// QueryGroups is how many owned Query Groups have an object in the view.
	QueryGroups int
	// ObjectBytes and OutputContextBytes are the stored bytes of the
	// execution objects and of the output contexts they name.
	ObjectBytes, OutputContextBytes int
}

type localViewCollector struct {
	mu          sync.Mutex
	source      func() LocalViewCounts
	bytes       *prometheus.Desc
	queryGroups *prometheus.Desc
}

func newLocalViewCollector() *localViewCollector {
	return &localViewCollector{
		bytes: prometheus.NewDesc(
			prometheus.BuildFQName(metricNamespace, metricSubsystem, "local_view_object_bytes"),
			"Stored bytes of the control content this Worker holds for the Query Groups it owns, by kind: "+
				"query_group is the execution objects, output_context the output contexts their Plans name. "+
				"Each Query Group counts the object and contexts its last Slot was frozen from, so the sum is "+
				"the view a Worker rebuilds on a cold connection and the delta stream's per-Worker buffer "+
				"bound. It is not the object cache's residency: a Query Group leaves the sum the moment this "+
				"Worker releases it, and a superseded object is replaced in it the moment a Slot reads the "+
				"new one.",
			[]string{"kind"}, nil,
		),
		queryGroups: prometheus.NewDesc(
			prometheus.BuildFQName(metricNamespace, metricSubsystem, "local_view_query_groups"),
			"Owned Query Groups whose last Slot was read by content and so have an object in the view. "+
				"Read it against worker_owned_query_groups: the difference is Query Groups this Worker owns "+
				"but has not frozen a Slot for yet, or whose last Slot was served from the Snapshot instead "+
				"(object_read_total{kind=\"segment\"} says which). A difference that does not close within "+
				"one evaluation period is content the Worker cannot read.",
			nil, nil,
		),
	}
}

// SetLocalViewSource binds the collector to the Worker's view over its owned
// set. Safe before or after registration, and a nil recorder is a no-op.
func (r *Recorder) SetLocalViewSource(source func() LocalViewCounts) {
	if r == nil || r.phaseTwo.localView == nil {
		return
	}
	r.phaseTwo.localView.mu.Lock()
	r.phaseTwo.localView.source = source
	r.phaseTwo.localView.mu.Unlock()
}

func (c *localViewCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.bytes
	ch <- c.queryGroups
}

func (c *localViewCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		return
	}
	// A Worker with a source and nothing owned reports zeros: an empty view
	// is a fact about an idle Worker, unlike the absent series of a process
	// that has no Worker role at all.
	counts := source()
	ch <- prometheus.MustNewConstMetric(c.bytes, prometheus.GaugeValue, float64(counts.ObjectBytes), "query_group")
	ch <- prometheus.MustNewConstMetric(c.bytes, prometheus.GaugeValue, float64(counts.OutputContextBytes), "output_context")
	ch <- prometheus.MustNewConstMetric(c.queryGroups, prometheus.GaugeValue, float64(counts.QueryGroups))
}
