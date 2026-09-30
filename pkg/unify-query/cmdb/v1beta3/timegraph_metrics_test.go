// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestSharedTopologyMetricsCases(t *testing.T) {
	for _, tt := range []struct {
		name, outcome, reject                                           string
		empty, partial, cancel, expired, gridLimit, graphLimit, invalid bool
		backendErr                                                      error
	}{
		{name: "非空结果", outcome: "success"},
		{name: "空结果", outcome: "empty", empty: true},
		{name: "不完整结果", outcome: "partial", partial: true},
		{name: "空但不完整", outcome: "partial", empty: true, partial: true},
		{name: "取数失败", outcome: "failed", backendErr: fmt.Errorf("backend failure")},
		{name: "构图容量拒绝", outcome: "rejected", graphLimit: true, reject: "max_graph_edges"},
		{name: "时间点超限", outcome: "rejected", gridLimit: true, reject: "max_shared_topology_points"},
		{name: "参数无效", outcome: "rejected", invalid: true, reject: "invalid_request"},
		{name: "提前取消", outcome: "canceled", cancel: true},
		{name: "提前超时", outcome: "timeout", expired: true},
		{name: "取数取消", outcome: "canceled", backendErr: fmt.Errorf("query: %w", context.Canceled)},
		{name: "取数超时", outcome: "timeout", backendErr: fmt.Errorf("query: %w", context.DeadlineExceeded)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			if tt.graphLimit {
				oldLimit := MaxGraphEdges
				MaxGraphEdges = 1
				t.Cleanup(func() { MaxGraphEdges = oldLimit })
			}
			if tt.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if tt.expired {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			}
			responses := map[string]pl.Matrix{
				"source_info_relation": contractMatrix(map[string]string{"source_id": "a"}, 1700000000000, 1700000100000),
				"source_middle_flow":   contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000, 1700000100000),
				"middle_target_flow":   {},
			}
			model := sharedTopologyQueryModel(responses)
			inflight := readTimeGraphMetric(t, "cmdb_topology_inflight", map[string]string{"query_mode": "range"}, "gauge")
			model.timeGraphVMQueryWithPartial = func(ctx context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, bool, error) {
				require.Equal(t, inflight+1, readTimeGraphMetric(t, "cmdb_topology_inflight", map[string]string{"query_mode": "range"}, "gauge"))
				if tt.empty || tt.backendErr != nil {
					return nil, tt.partial, tt.backendErr
				}
				return responses[q.QueryList[0].FieldName], tt.partial, nil
			}
			query := cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, StartTime: 1700000000, EndTime: 1700000100, Step: "100s", MaxHops: 1}
			if tt.gridLimit {
				query.EndTime = query.StartTime + 6000
			}
			if tt.invalid {
				query.SourceType = ""
			}
			labels := map[string]string{"scope": "query", "query_mode": "range", "result": tt.outcome}
			before := readTimeGraphMetric(t, "cmdb_topology_operations_total", labels, "counter")
			durations := readTimeGraphMetric(t, "cmdb_topology_operation_seconds", map[string]string{"scope": "query", "query_mode": "range"}, "count")
			sizes := readTimeGraphMetric(t, "cmdb_topology_size", map[string]string{"query_mode": "range", "kind": "points"}, "count")
			rejections := readTimeGraphMetric(t, "cmdb_topology_rejections_total", map[string]string{"query_mode": "range", "reason": tt.reject}, "counter")
			builds := readTimeGraphMetric(t, "cmdb_timegraph_stage_seconds", map[string]string{"stage": "build", "result": tt.outcome}, "count")
			matrixOutcome := tt.outcome
			if tt.graphLimit {
				matrixOutcome = "success"
			}
			matrixCalls := readTimeGraphMetric(t, "cmdb_timegraph_stage_seconds", map[string]string{"stage": "source-info", "result": matrixOutcome}, "count")
			countsBefore := map[string]float64{}
			for _, kind := range []string{"points", "nodes", "edges", "partial_snapshots"} {
				countsBefore[kind] = readTimeGraphMetric(t, "cmdb_topology_size", map[string]string{"query_mode": "range", "kind": kind}, "sum")
			}
			_, err := model.QuerySharedTopology(ctx, query)
			if tt.outcome == "success" || tt.outcome == "empty" || tt.outcome == "partial" {
				require.NoError(t, err)
				require.Equal(t, sizes+1, readTimeGraphMetric(t, "cmdb_topology_size", map[string]string{"query_mode": "range", "kind": "points"}, "count"))
				wantCounts := map[string]float64{"points": 2, "nodes": 4, "edges": 2, "partial_snapshots": 0}
				if tt.empty {
					wantCounts["nodes"], wantCounts["edges"] = 0, 0
				}
				if tt.partial {
					wantCounts["partial_snapshots"] = 2
				}
				for kind, count := range wantCounts {
					require.Equal(t, countsBefore[kind]+count, readTimeGraphMetric(t, "cmdb_topology_size", map[string]string{"query_mode": "range", "kind": kind}, "sum"), kind)
				}
			} else {
				require.Error(t, err)
				require.Equal(t, sizes, readTimeGraphMetric(t, "cmdb_topology_size", map[string]string{"query_mode": "range", "kind": "points"}, "count"))
			}
			require.Equal(t, before+1, readTimeGraphMetric(t, "cmdb_topology_operations_total", labels, "counter"))
			require.Equal(t, durations+1, readTimeGraphMetric(t, "cmdb_topology_operation_seconds", map[string]string{"scope": "query", "query_mode": "range"}, "count"))
			require.Equal(t, inflight, readTimeGraphMetric(t, "cmdb_topology_inflight", map[string]string{"query_mode": "range"}, "gauge"))
			if tt.reject != "" {
				require.Equal(t, rejections+1, readTimeGraphMetric(t, "cmdb_topology_rejections_total", map[string]string{"query_mode": "range", "reason": tt.reject}, "counter"))
			}
			if tt.backendErr != nil || tt.graphLimit {
				require.Equal(t, builds+1, readTimeGraphMetric(t, "cmdb_timegraph_stage_seconds", map[string]string{"stage": "build", "result": tt.outcome}, "count"))
			}
			if !tt.invalid && !tt.gridLimit && !tt.cancel && !tt.expired {
				require.Equal(t, matrixCalls+1, readTimeGraphMetric(t, "cmdb_timegraph_stage_seconds", map[string]string{"stage": "source-info", "result": matrixOutcome}, "count"))
			}
		})
	}
}

func readTimeGraphMetric(t *testing.T, name string, labels map[string]string, value string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "unify_query_"+name {
			continue
		}
		for _, sample := range family.GetMetric() {
			matched := len(sample.GetLabel()) == len(labels)
			for _, label := range sample.GetLabel() {
				matched = matched && labels[label.GetName()] == label.GetValue()
			}
			if !matched {
				continue
			}
			switch value {
			case "counter":
				return sample.GetCounter().GetValue()
			case "gauge":
				return sample.GetGauge().GetValue()
			case "count":
				return float64(sample.GetHistogram().GetSampleCount())
			case "sum":
				return sample.GetHistogram().GetSampleSum()
			}
		}
	}
	return 0
}
