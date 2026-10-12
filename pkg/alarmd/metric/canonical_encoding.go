// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// canonicalEncodingCollector reads the shared canonical encoder at scrape
// time: how many encodings the single-pass form answered and how many it
// handed back to the established path, and how the series delivery digests
// assembled from their shared parts compared with the generic digest on the
// one in 4096 that is checked.
type canonicalEncodingCollector struct {
	calls   *prometheus.Desc
	records *prometheus.Desc
}

func newCanonicalEncodingCollector() *canonicalEncodingCollector {
	name := func(suffix string) string {
		return prometheus.BuildFQName(metricNamespace, metricSubsystem, "canonical_encoding_"+suffix)
	}
	return &canonicalEncodingCollector{
		calls: prometheus.NewDesc(name("calls_total"),
			"Canonical encodings by what the single-pass form did with them: served, or declined back to the established path.",
			[]string{"outcome"}, nil),
		records: prometheus.NewDesc(name("records_shadow_total"),
			"Series delivery digests assembled from a series' shared parts that were checked against the "+
				"generic canonical digest, by result: agreed, or differed. A difference returns the generic "+
				"digest, so it is a finding and not a wrong answer; one in 4096 assemblies is checked.",
			[]string{"outcome"}, nil),
	}
}

func (c *canonicalEncodingCollector) Describe(out chan<- *prometheus.Desc) {
	out <- c.calls
	out <- c.records
}

func (c *canonicalEncodingCollector) Collect(out chan<- prometheus.Metric) {
	served, declined := contract.CanonicalStreamCounts()
	out <- prometheus.MustNewConstMetric(c.calls, prometheus.CounterValue, float64(served), "served")
	out <- prometheus.MustNewConstMetric(c.calls, prometheus.CounterValue, float64(declined), "declined")
	compared, differed := contract.ReadRecordsDigestShadowCounts()
	out <- prometheus.MustNewConstMetric(c.records, prometheus.CounterValue, float64(compared-differed), "agreed")
	out <- prometheus.MustNewConstMetric(c.records, prometheus.CounterValue, float64(differed), "differed")
}
