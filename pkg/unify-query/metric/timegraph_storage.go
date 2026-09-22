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
	cmdbTimeGraphStorageSize = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "unify_query", Name: "cmdb_timegraph_storage_size",
		Help:    "stored graph objects or logical time entries at build exit, not memory bytes",
		Buckets: []float64{0, 1, 10, 60, 100, 1000, 10000, 100000, 200000, 1000000},
	}, []string{"storage", "kind", "result"})
	cmdbTimeGraphBuildPhaseSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "unify_query", Name: "cmdb_timegraph_build_phase_seconds",
		Help:    "per-build cumulative matrix query time or remaining local wall time, not CPU time",
		Buckets: secondsBuckets,
	}, []string{"storage", "phase", "result"})
	cmdbTopologyAdmissionActive = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "unify_query", Name: "cmdb_topology_admission_active",
		Help: "occupied topology admission slots, including HTTP response writing",
	})
	cmdbTopologyAdmissionLimit = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "unify_query", Name: "cmdb_topology_admission_limit",
		Help: "effective process topology slot limit at the latest admission attempt; zero before first attempt",
	})
	cmdbTopologyPayloadBytes = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "unify_query", Name: "cmdb_topology_payload_bytes",
		Help:    "bytes read from request/backend bodies or encoded response items; not complete wire response bytes",
		Buckets: []float64{0, 1024, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864},
	}, []string{"stage", "result"})
)

func timeGraphStorage(storage string) bool { return storage == "shared" || storage == "time-buckets" }

func CMDBTimeGraphStorageObserve(ctx context.Context, storage, kind, result string, count int) {
	if !timeGraphStorage(storage) || !timeGraphResult(result) || count < 0 {
		return
	}
	switch kind {
	case "nodes", "relations", "attribute_versions", "legacy_graphs", "edge_time_entries", "node_time_entries":
		observe(ctx, cmdbTimeGraphStorageSize.WithLabelValues(storage, kind, result), float64(count))
	}
}

func CMDBTimeGraphBuildPhaseObserve(ctx context.Context, storage, phase, result string, duration time.Duration) {
	if !timeGraphStorage(storage) || !timeGraphResult(result) || duration < 0 || (phase != "matrix" && phase != "local") {
		return
	}
	observe(ctx, cmdbTimeGraphBuildPhaseSeconds.WithLabelValues(storage, phase, result), duration.Seconds())
}

// CMDBTopologyAdmissionSet 由准入锁保护更新，复用已有名额的模型调用不重复计数。
func CMDBTopologyAdmissionSet(active, limit int) {
	cmdbTopologyAdmissionActive.Set(float64(active))
	cmdbTopologyAdmissionLimit.Set(float64(limit))
}

func CMDBTopologyPayloadObserve(ctx context.Context, stage, result string, bytes int) {
	if !timeGraphResult(result) || bytes < 0 {
		return
	}
	switch stage {
	case "request-body", "backend-response", "response-item":
		observe(ctx, cmdbTopologyPayloadBytes.WithLabelValues(stage, result), float64(bytes))
	}
}
