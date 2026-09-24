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

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestTimeGraphLoadAttributionPreservesRejectedInput(t *testing.T) {
	for _, test := range []struct {
		name, outcome string
		backendErr    error
		rejected      bool
		empty         bool
	}{
		{name: "success", outcome: "success"},
		{name: "empty", outcome: "empty", empty: true},
		{name: "matrix_budget", outcome: "rejected", rejected: true},
		{name: "backend_error", outcome: "failed", backendErr: fmt.Errorf("private backend error")},
		{name: "canceled", outcome: "canceled", backendErr: context.Canceled},
		{name: "timeout", outcome: "timeout", backendErr: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := withTimeGraphQueryStage(initTimeGraphQueryTestEnvironment(), "relation-edge")
			oldLimit, oldYolo := MaxSharedTopologyMatrixPoints, yoloMode
			t.Cleanup(func() { MaxSharedTopologyMatrixPoints, yoloMode = oldLimit, oldYolo })
			yoloMode = false
			if test.rejected {
				MaxSharedTopologyMatrixPoints = 1
			}
			matrix := contractMatrix(map[string]string{"from_id": "a", "to_id": "b"}, 1700000000000, 1700000060000)
			if test.empty {
				matrix = nil
			}
			loader := timeGraphMatrixLoader{
				model: &Model{}, graph: NewTimeGraph(), topology: true,
				start: time.Unix(1700000000, 0), end: time.Unix(1700000060, 0), step: time.Minute,
				override: func(context.Context, *structured.QueryTs) (pl.Matrix, error) { return matrix, test.backendErr },
			}
			name := "load_test_" + test.name
			query := &structured.QueryTs{QueryList: []*structured.Query{{FieldName: name}}}
			labels := map[string]string{"stage": "relation-edge", "metric_name": name, "result": test.outcome}
			beforeCalls := readTimeGraphMetric(t, "cmdb_timegraph_load_operations_total", labels, "counter")
			expected := map[string]float64{"series": 1, "points": 2, "label_bytes": 14}
			if test.empty || test.backendErr != nil {
				expected = map[string]float64{"series": 0, "points": 0, "label_bytes": 0}
			}
			before := map[string]float64{}
			for kind := range expected {
				before[kind] = readTimeGraphMetric(t, "cmdb_timegraph_load_size_total", map[string]string{"stage": "relation-edge", "metric_name": name, "kind": kind}, "counter")
			}
			got, _, err := loader.query(ctx, query)
			if test.backendErr != nil || test.rejected {
				require.Error(t, err)
				require.Nil(t, got)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, beforeCalls+1, readTimeGraphMetric(t, "cmdb_timegraph_load_operations_total", labels, "counter"))
			require.Greater(t, readTimeGraphMetric(t, "cmdb_timegraph_load_last_timestamp_seconds", map[string]string{"stage": "relation-edge", "metric_name": name}, "gauge"), float64(0))
			for kind, count := range expected {
				require.Equal(t, before[kind]+count, readTimeGraphMetric(t, "cmdb_timegraph_load_size_total", map[string]string{"stage": "relation-edge", "metric_name": name, "kind": kind}, "counter"), kind)
			}
		})
	}
}

func TestTimeGraphLoadAttributionAcrossSourceAndRelationQueries(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%t", partial), func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			responses := map[string]pl.Matrix{
				"source_info_relation": contractMatrix(map[string]string{"source_id": "a"}, 1700000000000),
				"source_middle_flow":   contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000),
				"middle_target_flow":   {},
			}
			model := sharedTopologyQueryModel(responses)
			model.timeGraphVMQueryWithPartial = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, bool, error) {
				return responses[q.QueryList[0].FieldName], partial, nil
			}
			outcome := "success"
			if partial {
				outcome = "partial"
			}
			names := map[string]string{"source_info_relation": "source-info", "source_middle_flow": "relation-edge"}
			beforePoints, beforeCalls := map[string]float64{}, map[string]float64{}
			for name, stage := range names {
				beforePoints[name] = readTimeGraphMetric(t, "cmdb_timegraph_load_size_total", map[string]string{"stage": stage, "metric_name": name, "kind": "points"}, "counter")
				beforeCalls[name] = readTimeGraphMetric(t, "cmdb_timegraph_load_operations_total", map[string]string{"stage": stage, "metric_name": name, "result": outcome}, "counter")
			}
			result, err := model.QuerySharedTopology(ctx, cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, Timestamp: 1700000000, MaxHops: 1})
			require.NoError(t, err)
			require.NotEmpty(t, result.Snapshots)
			for name, stage := range names {
				require.Equal(t, beforePoints[name]+1, readTimeGraphMetric(t, "cmdb_timegraph_load_size_total", map[string]string{"stage": stage, "metric_name": name, "kind": "points"}, "counter"))
				require.Equal(t, beforeCalls[name]+1, readTimeGraphMetric(t, "cmdb_timegraph_load_operations_total", map[string]string{"stage": stage, "metric_name": name, "result": outcome}, "counter"))
			}
		})
	}
	require.Equal(t, "__mixed__", timeGraphLoadMetricName(&structured.QueryTs{QueryList: []*structured.Query{{FieldName: "a"}, {FieldName: "b"}}}))
}
