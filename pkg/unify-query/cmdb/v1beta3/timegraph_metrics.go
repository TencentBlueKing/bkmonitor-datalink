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
