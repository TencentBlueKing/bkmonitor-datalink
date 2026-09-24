// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// observeSharedTopologyResult 统一模型终态，区分校验拒绝、执行失败和不完整响应。
func observeSharedTopologyResult(ctx context.Context, mode string, started time.Time, validated bool, result cmdb.SharedTopologyResult, err error) {
	metric.CMDBTopologyInFlightAdd(mode, -1)
	outcome := metric.CMDBTimeGraphErrorResult(err)
	if err != nil {
		var limit interface{ TruncationReason() string }
		if errors.As(err, &limit) {
			metric.CMDBTopologyRejectInc(ctx, mode, limit.TruncationReason())
		} else if !validated && outcome == metric.CMDBRelationResultFailed {
			outcome = metric.CMDBRelationResultRejected
			metric.CMDBTopologyRejectInc(ctx, mode, "invalid_request")
		}
	} else {
		nodes, edges, partial := 0, 0, 0
		for _, snapshot := range result.Snapshots {
			nodes += len(snapshot.Nodes)
			edges += len(snapshot.Edges)
			if snapshot.Partial {
				partial++
			}
		}
		if partial > 0 {
			outcome = metric.CMDBRelationResultPartial
		} else if nodes == 0 {
			outcome = metric.CMDBRelationResultEmpty
		}
		metric.CMDBTopologySizeObserve(ctx, mode, result.PointCount, nodes, edges, partial)
	}
	metric.CMDBTopologyObserve(ctx, metric.CMDBTopologyScopeQuery, mode, outcome, time.Since(started))
}

// SetTimeGraphLimitTrace 保留拒绝时的数量与生效上限，错误文本不作为指标标签。
func SetTimeGraphLimitTrace(span *trace.Span, err error) {
	var limit *ResultLimitError
	var grid *topologyGridLimitError
	switch {
	case errors.As(err, &limit):
		span.Set("limit-reason", limit.Reason)
		span.Set("limit-count", limit.Count)
		span.Set("limit-maximum", limit.Limit)
	case errors.As(err, &grid):
		span.Set("limit-reason", grid.TruncationReason())
		span.Set("limit-count", grid.count)
		span.Set("limit-maximum", grid.limit)
	}
}

func observeTimeGraphBuild(ctx context.Context, span *trace.Span, graph *TimeGraph, loader *timeGraphMatrixLoader, started time.Time, err error) {
	storage := "time-buckets"
	relations, versions := len(graph.topologyEdges), graph.nodeInfoCount
	if graph.shared != nil {
		storage = "shared"
		relations, versions = len(graph.shared.edgeBits), 0
		for _, infos := range graph.shared.nodeInfos {
			versions += len(infos)
		}
	}
	outcome := metric.CMDBTimeGraphErrorResult(err)
	for kind, count := range map[string]int{"nodes": graph.nodeBuilder.Length(), "relations": relations, "attribute_versions": versions, "legacy_graphs": len(graph.timeGraph), "edge_time_entries": graph.edgeCount, "node_time_entries": graph.nodeInfoCount} {
		metric.CMDBTimeGraphStorageObserve(ctx, storage, kind, outcome, count)
	}
	for kind, count := range map[string]int{"nodes": graph.nodeBuilder.Length(), "edges": graph.edgeCount, "node_infos": graph.nodeInfoCount, "timepoints": graph.timepointCount()} {
		metric.CMDBTimeGraphSizeObserve(ctx, "build", kind, count)
	}
	// Matrix 调用当前串行；余下墙钟包含 query 构造、属性与目标索引，不是纯 CPU 时间。
	local := time.Since(started) - loader.queryDuration
	metric.CMDBTimeGraphBuildPhaseObserve(ctx, storage, "matrix", outcome, loader.queryDuration)
	metric.CMDBTimeGraphBuildPhaseObserve(ctx, storage, "local", outcome, local)
	span.Set("matrix-query-duration-seconds", loader.queryDuration.Seconds())
	span.Set("local-build-duration-seconds", local.Seconds())
	span.Set("graph-legacy-timepoint-count", len(graph.timeGraph))
	if graph.shared != nil {
		span.Set("graph-shared-edge-count", relations)
	}
	span.Set("graph-attribute-version-count", versions)
	span.Set("graph-node-count", graph.nodeBuilder.Length())
	span.Set("graph-edge-count", graph.edgeCount)
	span.Set("graph-node-info-count", graph.nodeInfoCount)
	span.Set("graph-timepoint-count", graph.timepointCount())
	span.Set("graph-partial-point-count", len(graph.partialTimes))
	span.Set("matrix-query-count", loader.queryCount)
	span.Set("matrix-cumulative-point-count", loader.pointCount)
	SetTimeGraphLimitTrace(span, err)
}

func finishTimeGraphStage(ctx context.Context, span *trace.Span, stage string, started time.Time, err *error) {
	SetTimeGraphLimitTrace(span, *err)
	metric.CMDBTimeGraphStageObserve(ctx, stage, metric.CMDBTimeGraphErrorResult(*err), time.Since(started))
	span.End(err)
}
