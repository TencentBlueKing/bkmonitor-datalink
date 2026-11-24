// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// datasetRecordIDHelpKept and datasetRecordIDHelpOutside are the two
// families' Help, kept beside METRICS.md word for word.
const (
	datasetRecordIDHelpKept = "Columnar Dataset runs (one series' consecutive points) that keep their record ids, " +
		"because at least one id is not the one derived from the series and the point's source time. " +
		"A run whose every id is the derived one keeps none, 64 bytes a point less, and a window derives them " +
		"when it reads the series. Every producer of Datasets builds its records from derived ids, so this stays " +
		"zero; a rising count is a producer that does not, whose input is held at 64 bytes a point more than the share was measured at."
	datasetRecordIDHelpOutside = "Record ids of columnar Dataset points derived outside a window: read from a run " +
		"that keeps no ids when no window has derived them, each derived for the read. Every reader of a Slot's " +
		"inputs reads them in the window that evaluates its series, from ids derived once a point, so this stays " +
		"near zero; a rising count is a reader outside a window paying a SHA-256 per read. A folded series' records " +
		"are read once a point here, and a diagnostic query derives its own and is not counted."
)

// datasetRecordIDCollector reads at scrape time how the columnar Datasets
// came by their record ids (execution.DatasetRecordIDCounts).
type datasetRecordIDCollector struct {
	kept    *prometheus.Desc
	outside *prometheus.Desc
}

func newDatasetRecordIDCollector() *datasetRecordIDCollector {
	name := func(suffix string) string {
		return prometheus.BuildFQName("", metricSubsystem, "dataset_record_ids_"+suffix)
	}
	return &datasetRecordIDCollector{
		kept:    prometheus.NewDesc(name("kept_runs_total"), datasetRecordIDHelpKept, nil, nil),
		outside: prometheus.NewDesc(name("derived_outside_window_total"), datasetRecordIDHelpOutside, nil, nil),
	}
}

func (c *datasetRecordIDCollector) Describe(out chan<- *prometheus.Desc) {
	out <- c.kept
	out <- c.outside
}

func (c *datasetRecordIDCollector) Collect(out chan<- prometheus.Metric) {
	kept, outside := execution.DatasetRecordIDCounts()
	out <- prometheus.MustNewConstMetric(c.kept, prometheus.CounterValue, float64(kept))
	out <- prometheus.MustNewConstMetric(c.outside, prometheus.CounterValue, float64(outside))
}
