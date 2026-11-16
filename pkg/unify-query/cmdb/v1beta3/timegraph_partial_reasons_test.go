// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// 同一次 source info 读取既带 source_info_no_route 状态又带 VM 的 partial 位：两个原因都记入，
// 后端不完整标记保留（它决定共享拓扑的取数路径）；只有状态时只记 source_info_no_route。
func TestSourceInfoNoRouteKeepsBackendPartial(t *testing.T) {
	for _, tc := range []struct {
		name       string
		vmPartial  bool
		wantReason string
	}{
		{name: "vm_partial", vmPartial: true, wantReason: "backend_partial;source_info_no_route"},
		{name: "no_route_only", wantReason: "source_info_no_route"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			model := &Model{schemaProvider: publicSourceExpandProvider()}
			model.timeGraphQueryReference = func(ctx context.Context, q *structured.QueryTs) (metadata.QueryReference, error) {
				if q.QueryList[0].FieldName != "node_info_relation" {
					return timeGraphTestQueryReference(ctx, q)
				}
				if err := q.ToTime(ctx); err != nil {
					return nil, err
				}
				// 没有可查询存储的引用：source info 读取按 source_info_no_route 标记。
				ref := metadata.QueryReference{"a": {&metadata.QueryMetric{ReferenceName: "a", MetricName: "node_info_relation"}}}
				metadata.SetQueryReference(ctx, ref)
				return ref, nil
			}
			model.timeGraphVMQueryWithPartial = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, bool, error) {
				return nil, tc.vmPartial && q.QueryList[0].FieldName == "node_info_relation", nil
			}
			point := time.Unix(1700000040, 0)
			graph, err := model.buildTimeGraph(ctx, "space", point, point, time.Minute, "node", cmdb.Matcher{"node_id": "n1"}, cmdb.Matcher{"zone": "east"}, nil, nil, "10m", nil, nil)
			require.NoError(t, err)
			defer graph.Clean(ctx)
			require.Equal(t, tc.wantReason, graph.partialTimesCopy()[point.UnixMilli()])
			require.Equal(t, tc.vmPartial, graph.backendPartial())
		})
	}
}
