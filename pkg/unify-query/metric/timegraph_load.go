// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	cmdbTimeGraphLoadOperations = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "unify_query", Name: "cmdb_timegraph_load_operations_total",
		Help: "physical Matrix fetches by stage and outcome; not user HTTP requests",
	}, []string{"stage", "result"})
	cmdbTimeGraphLoadSeconds = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "unify_query", Name: "cmdb_timegraph_load_seconds_total",
		Help: "cumulative Matrix fetch wall time including validation, not CPU time",
	}, []string{"stage", "result"})
	cmdbTimeGraphLoadSize = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "unify_query", Name: "cmdb_timegraph_load_size_total",
		Help: "returned Matrix series, points and label string bytes, including matrices rejected by validation; not resident heap or backend wire bytes",
	}, []string{"stage", "kind"})
)

// TimeGraphLoadSize describes the Matrix actually returned by the backend.
// LabelBytes counts label names and values once per returned series. It excludes
// container/map overhead and must not be interpreted as allocated memory.
type TimeGraphLoadSize struct {
	Returned   bool
	Series     int
	Points     int
	LabelBytes int
}

func CMDBTimeGraphLoadObserve(ctx context.Context, stage, result string, duration time.Duration, size TimeGraphLoadSize) {
	if (stage != "source-info" && stage != "relation-edge" && stage != "target-info") || !timeGraphResult(result) || duration < 0 {
		return
	}
	if size.Series < 0 || size.Points < 0 || size.LabelBytes < 0 {
		return
	}
	counterInc(ctx, cmdbTimeGraphLoadOperations.WithLabelValues(stage, result))
	counterAdd(ctx, cmdbTimeGraphLoadSeconds.WithLabelValues(stage, result), duration.Seconds())
	if !size.Returned {
		return
	}
	for kind, count := range map[string]int{"series": size.Series, "points": size.Points, "label_bytes": size.LabelBytes} {
		counterAdd(ctx, cmdbTimeGraphLoadSize.WithLabelValues(stage, kind), float64(count))
	}
}
