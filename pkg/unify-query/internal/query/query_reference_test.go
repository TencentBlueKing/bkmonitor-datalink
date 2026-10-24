// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package query

import (
	"context"
	"testing"

	"github.com/prometheus/prometheus/promql/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestVmExpandUsesOnlyExpressionSelectors(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	qr := metadata.QueryReference{}
	for _, ref := range []string{"a", "b"} {
		qr[ref] = []*metadata.QueryMetric{{QueryList: []*metadata.Query{{
			TableID: "table-" + ref, VmRt: "rt-" + ref, StorageName: "storage-" + ref,
			VmCondition: metadata.VmCondition("filter-" + ref),
		}}}}
	}
	for _, tc := range []struct {
		expr string
		refs []string
	}{
		{"a", []string{"a"}},
		{`count_over_time({__name__=~"a"}[5m])`, []string{"a"}},
		{"count_over_time(a[5m]) or b", []string{"a", "b"}},
		{`label_replace(a, "label", "b", "source", ".*")`, []string{"a"}},
		{"sum_over_time((a offset 1m)[5m:1m])", []string{"a"}},
		{"vector(1)", nil},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			expr, err := parser.ParseExpr(tc.expr)
			require.NoError(t, err)
			expand := ToVmExpandForExpression(ctx, qr, expr)
			require.NotNil(t, expand)
			if tc.refs == nil {
				require.Equal(t, []string{"rt-a", "rt-b"}, expand.ResultTableList)
				require.Empty(t, expand.RtDetailList, "constants have no storage coverage requirement")
			} else {
				require.Len(t, expand.ResultTableList, len(tc.refs))
				require.Len(t, expand.RtDetailList, len(tc.refs))
				require.Len(t, expand.MetricFilterCondition, len(tc.refs))
				for _, ref := range tc.refs {
					require.Contains(t, expand.ResultTableList, "rt-"+ref)
					require.Equal(t, metadata.RtDetail{TableID: "table-" + ref, StorageName: "storage-" + ref}, expand.RtDetailList["rt-"+ref])
					require.Equal(t, "filter-"+ref, expand.MetricFilterCondition[ref])
				}
				if len(tc.refs) == 1 {
					require.Equal(t, "storage-"+tc.refs[0], expand.ClusterName)
				} else {
					require.Empty(t, expand.ClusterName)
				}
			}
			// Building or mutating one envelope must not change another output.
			delete(expand.RtDetailList, "rt-a")
			delete(expand.MetricFilterCondition, "a")
			full := ToVmExpand(ctx, qr)
			require.Len(t, full.RtDetailList, 2)
			require.Equal(t, "filter-a", full.MetricFilterCondition["a"])
		})
	}
}

func TestVmExpand(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	for name, c := range map[string]struct {
		queryRef metadata.QueryReference
		VmExpand *metadata.VmExpand
	}{
		"default-1": {
			queryRef: metadata.QueryReference{
				"a": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm",
								VmRt:        "vm_result_table",
								Field:       "container_cpu_usage_seconds",
								VmCondition: `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table"`,
							},
							{
								TableID:     "result_table.vm_1",
								VmRt:        "vm_result_table_1",
								Field:       "container_cpu_usage_seconds",
								VmCondition: `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table_1"`,
							},
						},
					},
				},
				"b": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm",
								VmRt:        "vm_result_table",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								VmCondition: `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table"`,
							},
							{
								TableID:     "result_table.vm_1",
								VmRt:        "vm_result_table_1",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								VmCondition: `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table_1"`,
							},
						},
					},
				},
			},
			VmExpand: &metadata.VmExpand{
				MetricFilterCondition: map[string]string{
					"a": `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table" or __name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table_1"`,
					"b": `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table" or __name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table_1"`,
				},
				ResultTableList: []string{
					"vm_result_table",
					"vm_result_table_1",
				},
				RtDetailList: map[string]metadata.RtDetail{
					"vm_result_table":   {TableID: "result_table.vm"},
					"vm_result_table_1": {TableID: "result_table.vm_1"},
				},
			},
		},
		"default-2": {
			queryRef: metadata.QueryReference{
				"a": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm",
								VmRt:        "vm_result_table",
								MetricNames: []string{"container_cpu_usage_seconds"},
								VmCondition: `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table"`,
							},
						},
					},
				},
				"b": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm",
								VmRt:        "vm_result_table",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								VmCondition: `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table"`,
							},
							{
								TableID:     "result_table.vm_1",
								VmRt:        "",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								VmCondition: `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table_1"`,
							},
						},
					},
				},
			},
			VmExpand: &metadata.VmExpand{
				MetricFilterCondition: map[string]string{
					"a": `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table"`,
					"b": `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table"`,
				},
				ResultTableList: []string{
					"vm_result_table",
				},
				RtDetailList: map[string]metadata.RtDetail{
					"vm_result_table": {TableID: "result_table.vm"},
				},
			},
		},
		"default-3": {
			queryRef: metadata.QueryReference{
				"a": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm",
								VmRt:        "vm_result_table",
								MetricNames: []string{"container_cpu_usage_seconds"},
								VmCondition: `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table"`,
							},
						},
					},
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm",
								VmRt:        "vm_result_table_2",
								MetricNames: []string{"container_cpu_usage_seconds"},
								VmCondition: `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table_2"`,
							},
						},
					},
				},
				"b": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm",
								VmRt:        "vm_result_table",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								VmCondition: `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table"`,
							},
							{
								TableID:     "result_table.vm_1",
								VmRt:        "vm_result_table_1",
								MetricNames: []string{"kube_pod_container_resource_requests"},
								VmCondition: `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table_1"`,
							},
						},
					},
				},
			},
			VmExpand: &metadata.VmExpand{
				MetricFilterCondition: map[string]string{
					"a": `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table"`,
					"b": `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table" or __name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table_1"`,
				},
				ResultTableList: []string{
					"vm_result_table",
					"vm_result_table_1",
				},
				RtDetailList: map[string]metadata.RtDetail{
					"vm_result_table":   {TableID: "result_table.vm"},
					"vm_result_table_1": {TableID: "result_table.vm_1"},
				},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			for i := 0; i < 64; i++ {
				vmExpand := ToVmExpand(ctx, c.queryRef)
				assert.Equal(t, c.VmExpand, vmExpand, "VM condition order changed at iteration %d", i)
			}
		})
	}
}
