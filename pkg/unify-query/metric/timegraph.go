// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	CMDBTopologyScopeRequest   = "request"
	CMDBTopologyScopeQuery     = "query"
	CMDBRelationResultRejected = "rejected"
	CMDBRelationResultCanceled = "canceled"
	CMDBRelationResultTimeout  = "timeout"
)

var (
	cmdbTopologyOperationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "unify_query", Name: "cmdb_topology_operations_total",
		Help: "completed topology HTTP requests or individual queries by outcome",
	}, []string{"scope", "query_mode", "result"})
	cmdbTopologyOperationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "unify_query", Name: "cmdb_topology_operation_seconds",
		Help: "topology HTTP request or individual query duration", Buckets: secondsBuckets,
	}, []string{"scope", "query_mode"})
	cmdbTopologyInFlight = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "unify_query", Name: "cmdb_topology_inflight",
		Help: "topology model queries currently executing",
	}, []string{"query_mode"})
	cmdbTopologySize = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "unify_query", Name: "cmdb_topology_size",
		Help:    "successful topology response size; nodes and edges are summed across snapshots",
		Buckets: []float64{0, 1, 2, 6, 30, 60, 64, 100, 1000, 10000, 100000, 1000000},
	}, []string{"query_mode", "kind"})
	cmdbTopologyRejectionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "unify_query", Name: "cmdb_topology_rejections_total",
		Help: "topology query rejections by bounded validation or capacity reason",
	}, []string{"query_mode", "reason"})
	cmdbTimeGraphStageSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "unify_query", Name: "cmdb_timegraph_stage_seconds",
		Help:    "TimeGraph build, matrix query and topology traversal duration and outcome",
		Buckets: secondsBuckets,
	}, []string{"stage", "result"})
	cmdbTimeGraphSize = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "unify_query", Name: "cmdb_timegraph_size",
		Help:    "TimeGraph build size or successful matrix query size by bounded stage and kind",
		Buckets: []float64{0, 1, 10, 100, 1000, 10000, 100000, 200000, 1000000},
	}, []string{"stage", "kind"})
)

// CMDBTimeGraphErrorResult 使用错误类型分类，错误原文和请求字段不进入指标标签。
func CMDBTimeGraphErrorResult(err error) string {
	switch {
	case err == nil:
		return CMDBRelationResultSuccess
	case errors.Is(err, context.Canceled):
		return CMDBRelationResultCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return CMDBRelationResultTimeout
	}
	var limit interface{ TruncationReason() string }
	if errors.As(err, &limit) {
		return CMDBRelationResultRejected
	}
	return CMDBRelationResultFailed
}

// CMDBTopologyObserve 每个请求或子查询只记录一次终态，避免 started 混入完成率分母。
func CMDBTopologyObserve(ctx context.Context, scope, mode, result string, duration time.Duration) {
	if (scope != CMDBTopologyScopeRequest && scope != CMDBTopologyScopeQuery) || !timeGraphQueryMode(mode) || !timeGraphResult(result) || duration < 0 {
		return
	}
	counterInc(ctx, cmdbTopologyOperationsTotal.WithLabelValues(scope, mode, result))
	observe(ctx, cmdbTopologyOperationSeconds.WithLabelValues(scope, mode), duration.Seconds())
}

// CMDBTopologyInFlightAdd 只统计已经进入模型的查询，结束时包括失败和取消都必须减一。
func CMDBTopologyInFlightAdd(mode string, delta int) {
	if timeGraphQueryMode(mode) && (delta == 1 || delta == -1) {
		cmdbTopologyInFlight.WithLabelValues(mode).Add(float64(delta))
	}
}

// CMDBTopologySizeObserve 仅对成功返回的响应记录规模；partial 响应也保留实际数量。
func CMDBTopologySizeObserve(ctx context.Context, mode string, points, nodes, edges, partialSnapshots int) {
	if !timeGraphQueryMode(mode) || points < 0 || nodes < 0 || edges < 0 || partialSnapshots < 0 {
		return
	}
	for kind, count := range map[string]int{"points": points, "nodes": nodes, "edges": edges, "partial_snapshots": partialSnapshots} {
		observe(ctx, cmdbTopologySize.WithLabelValues(mode, kind), float64(count))
	}
}

// CMDBTopologyRejectInc 将未知的容量原因归入 other，保证标签基数有界。
func CMDBTopologyRejectInc(ctx context.Context, mode, reason string) {
	if !timeGraphQueryMode(mode) {
		return
	}
	switch reason {
	case "invalid_request", "max_shared_topology_points", "max_graph_nodes", "max_graph_edges", "max_graph_node_infos", "max_graph_results", "max_targets",
		"max_response_bytes", "max_topology_matrix_points", "max_topology_matrix_series",
		"max_topology_output_elements", "max_topology_output_bytes", "max_topology_queries", "max_topology_request_bytes":
	default:
		reason = "other"
	}
	counterInc(ctx, cmdbTopologyRejectionsTotal.WithLabelValues(mode, reason))
}

// CMDBTimeGraphStageObserve 阶段级聚合同时覆盖路径查询和共享拓扑，不逐节点或逐边打点。
func CMDBTimeGraphStageObserve(ctx context.Context, stage, result string, duration time.Duration) {
	if !timeGraphStage(stage) || !timeGraphResult(result) || duration < 0 {
		return
	}
	observe(ctx, cmdbTimeGraphStageSeconds.WithLabelValues(stage, result), duration.Seconds())
}

// CMDBTimeGraphSizeObserve 构图规模在失败时仍保留；Matrix 规模仅记录成功取回的样本。
func CMDBTimeGraphSizeObserve(ctx context.Context, stage, kind string, count int) {
	if !timeGraphStage(stage) || count < 0 {
		return
	}
	switch kind {
	case "nodes", "edges", "node_infos", "timepoints", "series", "points":
	default:
		return
	}
	observe(ctx, cmdbTimeGraphSize.WithLabelValues(stage, kind), float64(count))
}

func timeGraphQueryMode(mode string) bool {
	return mode == CMDBRelationQueryModeInstant || mode == CMDBRelationQueryModeRange
}

func timeGraphResult(result string) bool {
	switch result {
	case CMDBRelationResultSuccess, CMDBRelationResultEmpty, CMDBRelationResultPartial, CMDBRelationResultFailed,
		CMDBRelationResultRejected, CMDBRelationResultCanceled, CMDBRelationResultTimeout:
		return true
	}
	return false
}

func timeGraphStage(stage string) bool {
	switch stage {
	case "build", "source-info", "relation-edge", "target-info", "topology-traversal", "topology-state", "topology-materialize", "topology-convert", "topology-encode", "apply-relation", "apply-source-info", "apply-target-info", "matrix-validation", "topology-propagation", "cleanup", "admission", "admission-release":
		return true
	}
	return false
}
