// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestTimeGraphErrorResultCases(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"成功", nil, "success"},
		{"失败", fmt.Errorf("backend secret"), "failed"},
		{"包装取消", fmt.Errorf("backend: %w", context.Canceled), "canceled"},
		{"包装超时", fmt.Errorf("backend: %w", context.DeadlineExceeded), "timeout"},
	} {
		t.Run(tt.name, func(t *testing.T) { require.Equal(t, tt.want, CMDBTimeGraphErrorResult(tt.err)) })
	}
}

func TestTimeGraphMetricsRejectDynamicLabels(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name   string
		record func()
	}{
		{"请求类型", func() { CMDBTopologyObserve(ctx, "secret-scope", "instant", "success", time.Second) }},
		{"查询模式", func() { CMDBTopologyObserve(ctx, "query", "secret-mode", "success", time.Second) }},
		{"错误文本", func() { CMDBTopologyObserve(ctx, "query", "instant", "secret-error", time.Second) }},
		{"阶段名称", func() { CMDBTimeGraphStageObserve(ctx, "secret-stage", "success", time.Second) }},
		{"规模名称", func() { CMDBTimeGraphSizeObserve(ctx, "build", "secret-kind", 1) }},
		{"负耗时", func() { CMDBTopologyObserve(ctx, "query", "instant", "success", -time.Second) }},
		{"存储模式", func() { CMDBTimeGraphStorageObserve(ctx, "private-storage", "nodes", "success", 1) }},
		{"存储种类", func() { CMDBTimeGraphStorageObserve(ctx, "shared", "private-kind", "success", 1) }},
		{"构图阶段", func() { CMDBTimeGraphBuildPhaseObserve(ctx, "shared", "private-phase", "success", time.Second) }},
		{"字节阶段", func() { CMDBTopologyPayloadObserve(ctx, "private-stage", "success", 1) }},
		{"负规模", func() { CMDBTopologySizeObserve(ctx, "instant", -1, 0, 0, 0) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := timeGraphFamilies(t)
			tt.record()
			require.Equal(t, before, timeGraphFamilies(t))
		})
	}
}

func TestTopologyRejectionReasonsAreBounded(t *testing.T) {
	for _, tt := range []struct{ name, reason, label string }{
		{"点数超限", "max_shared_topology_points", "max_shared_topology_points"},
		{"构图超限", "max_graph_edges", "max_graph_edges"},
		{"未知原因", "private backend response", "other"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			counter := cmdbTopologyRejectionsTotal.WithLabelValues("instant", tt.label)
			before := &dto.Metric{}
			require.NoError(t, counter.Write(before))
			CMDBTopologyRejectInc(context.Background(), "instant", tt.reason)
			after := &dto.Metric{}
			require.NoError(t, counter.Write(after))
			require.Equal(t, before.GetCounter().GetValue()+1, after.GetCounter().GetValue())
		})
	}
}

func timeGraphFamilies(t *testing.T) []*dto.MetricFamily {
	t.Helper()
	registry := prometheus.NewRegistry()
	registry.MustRegister(cmdbTopologyOperationsTotal, cmdbTopologyOperationSeconds, cmdbTopologyInFlight, cmdbTopologySize, cmdbTopologyRejectionsTotal, cmdbTimeGraphStageSeconds, cmdbTimeGraphSize, cmdbTimeGraphStorageSize, cmdbTimeGraphBuildPhaseSeconds, cmdbTopologyAdmissionActive, cmdbTopologyPayloadBytes)
	families, err := registry.Gather()
	require.NoError(t, err)
	return families
}
