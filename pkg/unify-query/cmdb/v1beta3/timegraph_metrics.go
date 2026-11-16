// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// observeSharedTopologyResult 统一模型终态，区分校验拒绝、执行失败和不完整响应。
// counts 是输出（target_types 过滤后）的节点、边出现次数与 partial 帧数。
func observeSharedTopologyResult(ctx context.Context, mode string, started time.Time, validated bool, points int, counts topologySnapshotCounts, err error) {
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
		if counts.partial > 0 {
			outcome = metric.CMDBRelationResultPartial
		} else if counts.nodes == 0 {
			outcome = metric.CMDBRelationResultEmpty
		}
		metric.CMDBTopologySizeObserve(ctx, mode, points, counts.nodes, counts.edges, counts.partial)
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
	relations, versions := 0, graph.nodeInfoCount
	if graph.shared != nil {
		storage = "shared"
		relations, versions = graph.shared.edgeCount(), 0
		for _, infos := range graph.shared.nodeInfos {
			versions += len(infos)
		}
	}
	outcome := metric.CMDBTimeGraphErrorResult(err)
	if graph.shared != nil {
		metric.CMDBTimeGraphStorageObserve(ctx, storage, "relations", outcome, relations)
	}
	for kind, count := range map[string]int{"nodes": graph.nodeBuilder.Length(), "attribute_versions": versions, "legacy_graphs": len(graph.timeGraph), "edge_time_entries": graph.edgeCount, "node_time_entries": graph.nodeInfoCount} {
		metric.CMDBTimeGraphStorageObserve(ctx, storage, kind, outcome, count)
	}
	// Matrix 查询按顺序执行，累计耗时直接计入构图墙钟。
	matrixWall := loader.queryDuration
	local := max(time.Duration(0), time.Since(started)-matrixWall)
	metric.CMDBTimeGraphBuildPhaseObserve(ctx, storage, "matrix", outcome, matrixWall)
	metric.CMDBTimeGraphBuildPhaseObserve(ctx, storage, "local", outcome, local)
	metric.CMDBTimeGraphMatrixQueriesObserve(ctx, storage, outcome, loader.queryCount, loader.physicalQueryCount, loader.reusedQueryCount)
	span.Set("matrix-query-duration-seconds", matrixWall.Seconds())
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
	span.Set("matrix-physical-query-count", loader.physicalQueryCount)
	span.Set("matrix-reused-query-count", loader.reusedQueryCount)
	span.Set("matrix-cumulative-point-count", loader.pointCount)
	span.Set("matrix-prepare-count", loader.prepareCount)
	span.Set("matrix-prepare-duration-seconds", loader.prepareDuration.Seconds())
	// 逐次读取的 span 可能不入库，后端耗时由这些汇总值拆出。
	reads, readBytes, firstByte := loader.readStats.Snapshot()
	span.Set("backend-read-count", reads)
	span.Set("backend-read-bytes", readBytes)
	span.Set("backend-first-byte-seconds", firstByte.Seconds())
	batchMax := 0
	for _, state := range loader.frontierStates {
		batchMax = max(batchMax, state.batch)
	}
	span.Set("frontier-probe-count", loader.frontierProbeCount)
	span.Set("frontier-wide-skip-count", loader.frontierWideSkips)
	span.Set("frontier-wide-overflow-count", loader.frontierWideOverflows)
	span.Set("frontier-batch-max-identities", batchMax)
	SetTimeGraphLimitTrace(span, err)
}

func finishTimeGraphStage(ctx context.Context, span *trace.Span, stage string, started time.Time, err *error) {
	SetTimeGraphLimitTrace(span, *err)
	metric.CMDBTimeGraphStageObserve(ctx, stage, metric.CMDBTimeGraphErrorResult(*err), time.Since(started))
	span.End(err)
}
