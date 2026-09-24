// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const maxTimeGraphLoadMetricNames = 1024

var (
	cmdbTimeGraphLoadOperations = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "unify_query", Name: "cmdb_timegraph_load_operations_total",
		Help: "Matrix calls by configured metric name and outcome; not user HTTP requests",
	}, []string{"stage", "metric_name", "result"})
	cmdbTimeGraphLoadSeconds = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "unify_query", Name: "cmdb_timegraph_load_seconds_total",
		Help: "cumulative Matrix call wall time including validation, not CPU time",
	}, []string{"stage", "metric_name", "result"})
	cmdbTimeGraphLoadSize = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "unify_query", Name: "cmdb_timegraph_load_size_total",
		Help: "returned Matrix series, points and label string bytes, including matrices rejected by validation; not resident heap or backend wire bytes",
	}, []string{"stage", "metric_name", "kind"})
	cmdbTimeGraphLoadLastTimestamp = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "unify_query", Name: "cmdb_timegraph_load_last_timestamp_seconds",
		Help: "Unix timestamp of the last completed Matrix call, including failed calls",
	}, []string{"stage", "metric_name"})
	timeGraphLoadNames = timeGraphMetricNames{names: make(map[string]struct{})}
)

type timeGraphMetricNames struct {
	sync.Mutex
	names map[string]struct{}
}

func (n *timeGraphMetricNames) label(name string) string {
	if name == "" {
		return "__unknown__"
	}
	if len(name) > 256 {
		return "__overflow__"
	}
	n.Lock()
	defer n.Unlock()
	if _, exists := n.names[name]; exists {
		return name
	}
	// Custom schema churn must not grow the process metric registry forever.
	// Overflow only aggregates telemetry; it never rejects a user query.
	if len(n.names) >= maxTimeGraphLoadMetricNames {
		return "__overflow__"
	}
	n.names[name] = struct{}{}
	return name
}

// TimeGraphLoadSize describes the Matrix actually returned by the backend.
// LabelBytes counts label names and values once per returned series. It excludes
// container/map overhead and must not be interpreted as allocated memory.
type TimeGraphLoadSize struct {
	Returned   bool
	Series     int
	Points     int
	LabelBytes int
}

func CMDBTimeGraphLoadObserve(ctx context.Context, stage, metricName, result string, duration time.Duration, size TimeGraphLoadSize) {
	if (stage != "source-info" && stage != "relation-edge" && stage != "target-info") || !timeGraphResult(result) || duration < 0 {
		return
	}
	if size.Series < 0 || size.Points < 0 || size.LabelBytes < 0 {
		return
	}
	name := timeGraphLoadNames.label(metricName)
	gaugeSet(ctx, cmdbTimeGraphLoadLastTimestamp.WithLabelValues(stage, name), float64(time.Now().Unix()))
	counterInc(ctx, cmdbTimeGraphLoadOperations.WithLabelValues(stage, name, result))
	counterAdd(ctx, cmdbTimeGraphLoadSeconds.WithLabelValues(stage, name, result), duration.Seconds())
	if !size.Returned {
		return
	}
	for kind, count := range map[string]int{"series": size.Series, "points": size.Points, "label_bytes": size.LabelBytes} {
		counterAdd(ctx, cmdbTimeGraphLoadSize.WithLabelValues(stage, name, kind), float64(count))
	}
}
