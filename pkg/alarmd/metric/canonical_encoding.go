// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The canonical encoder is being replaced by a single-pass form, and the
// replacement is proven online rather than only offline: the pinned branch
// table and the fuzz corpus cover what somebody thought of, and the rejection
// boundary is where an unthought-of input lands.
//
// These series exist so that "no divergence" is a reading rather than an
// absence of readings. Three separate questions have to be answerable from
// them and none can be inferred from another:
//
//	which position is this process in    canonical_encoding_mode
//	how much was actually compared       canonical_encoding_shadow_total{outcome}
//	how much did the new form answer     canonical_encoding_calls_total{outcome}
//
// Declined is an outcome of its own on both, never folded into agreement. An
// input the new form hands back is not evidence that the two agree on it, and
// a switch that had quietly stopped accepting anything would otherwise still
// show a clean sheet at full coverage.
type canonicalEncodingCollector struct {
	mode     *prometheus.Desc
	stride   *prometheus.Desc
	calls    *prometheus.Desc
	shadow   *prometheus.Desc
	findings *prometheus.Desc
	coverage *prometheus.Desc
}

func newCanonicalEncodingCollector() *canonicalEncodingCollector {
	name := func(suffix string) string {
		return prometheus.BuildFQName(metricNamespace, metricSubsystem, "canonical_encoding_"+suffix)
	}
	return &canonicalEncodingCollector{
		mode: prometheus.NewDesc(name("mode"),
			"Rollout position of the shared canonical encoder, as 1 on the active mode and 0 on the others: "+
				"established, shadow, stream_shadow or stream.",
			[]string{"mode"}, nil),
		stride: prometheus.NewDesc(name("shadow_sample_stride"),
			"One call in this many is compared against the other form. Zero means no comparison is running, "+
				"which is the reading that separates 'nothing diverged' from 'nothing was checked'.",
			nil, nil),
		calls: prometheus.NewDesc(name("calls_total"),
			"Canonical encodings by what the single-pass form did with them: served, or declined back to the established path.",
			[]string{"outcome"}, nil),
		shadow: prometheus.NewDesc(name("shadow_total"),
			"Shadow comparisons by result: agreed, declined by the shadow, or one of the three divergence classes.",
			[]string{"outcome"}, nil),
		findings: prometheus.NewDesc(name("distinct_findings"),
			"Distinct divergence fingerprints held in memory, deduplicated by class, direction, Go type, "+
				"container shape and offset kind, and capped. At the cap the count stops rising while the "+
				"totals keep climbing, so the two together say whether new shapes are still appearing.",
			nil, nil),
		coverage: prometheus.NewDesc(name("covered_call_sites"),
			"Distinct Go types that have actually been compared. A comparison total says how much was "+
				"checked; only this says how widely. A million comparisons from one caller prove one caller.",
			nil, nil),
	}
}

func (c *canonicalEncodingCollector) Describe(out chan<- *prometheus.Desc) {
	out <- c.mode
	out <- c.stride
	out <- c.calls
	out <- c.shadow
	out <- c.findings
	out <- c.coverage
}

func (c *canonicalEncodingCollector) Collect(out chan<- prometheus.Metric) {
	counts := contract.ReadCanonicalShadowCounts()
	// Every mode is emitted, not only the active one. A series that vanishes
	// when its mode is not selected cannot be graphed across a cutover, which
	// is the one moment anybody looks at it.
	for _, mode := range contract.CanonicalModeNames() {
		active := 0.0
		if mode == counts.Mode {
			active = 1
		}
		out <- prometheus.MustNewConstMetric(c.mode, prometheus.GaugeValue, active, mode)
	}
	out <- prometheus.MustNewConstMetric(c.stride, prometheus.GaugeValue, float64(counts.Stride))
	out <- prometheus.MustNewConstMetric(c.calls, prometheus.CounterValue, float64(counts.StreamServed), "served")
	out <- prometheus.MustNewConstMetric(c.calls, prometheus.CounterValue, float64(counts.StreamDeclined), "declined")
	out <- prometheus.MustNewConstMetric(c.shadow, prometheus.CounterValue, float64(counts.Agreed), "agreed")
	out <- prometheus.MustNewConstMetric(c.shadow, prometheus.CounterValue, float64(counts.Declined), "declined")
	out <- prometheus.MustNewConstMetric(c.shadow, prometheus.CounterValue, float64(counts.BytesDiffer), "bytes_differ")
	out <- prometheus.MustNewConstMetric(c.shadow, prometheus.CounterValue, float64(counts.VerdictDiffer), "verdict_differ")
	out <- prometheus.MustNewConstMetric(c.shadow, prometheus.CounterValue, float64(counts.PanicDiffer), "panic_differ")
	out <- prometheus.MustNewConstMetric(c.findings, prometheus.GaugeValue,
		float64(len(contract.ReadCanonicalShadowSamples())))
	out <- prometheus.MustNewConstMetric(c.coverage, prometheus.GaugeValue, float64(counts.CoveredCallSites))
}
