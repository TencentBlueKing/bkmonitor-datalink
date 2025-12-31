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
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

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
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			VmExpand := ToVmExpand(ctx, c.queryRef)

			//
			for k, v := range VmExpand.MetricFilterCondition {
				or := " or "
				arr := strings.Split(v, or)
				sort.Strings(arr)
				VmExpand.MetricFilterCondition[k] = strings.Join(arr, or)
			}

			assert.Equal(t, c.VmExpand, VmExpand)
		})
	}
}

func TestConflict(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	mock.Init()
	influxdb.MockSpaceRouter(ctx)
	influxdbRouter := influxdb.GetInfluxDBRouter() // 获取InfluxDB Router实例，
	if influxdbRouter == nil {
		t.Errorf("influxdb router is nil")
	}
	// 调用ReloadRouter初始化router字段
	err := influxdbRouter.ReloadRouter(ctx, "bkmonitorv3:influxdb", nil)
	if err != nil {
		t.Errorf("reload router failed, error:%s", err)
	}
	for name, c := range map[string]struct {
		queryRef   metadata.QueryReference
		isConflict bool
	}{
		//gzl：测试用例1 匹配黑名单规则 isConflict为false
		"default-1": {
			queryRef: metadata.QueryReference{
				"a": {
					{
						QueryList: []*metadata.Query{
							{
								TableID: "result_table.vm",
								VmRt:    "vmrt_1",
								Field:   "container_cpu_usage_seconds",
							},
							{
								TableID: "result_table.vm_1",
								VmRt:    "vmrt_3",
								Field:   "container_cpu_usage_seconds",
							},
							{
								TableID: "result_table.vm_3",
								VmRt:    "vmrt_5",
								Field:   "container_cpu_usage_seconds",
							},
						},
					},
				},
				"b": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm_1",
								VmRt:        "vmrt_1",
								MetricNames: []string{"kube_pod_container_resource_requests"},
							},
							{
								TableID:     "result_table.vm_3",
								VmRt:        "vmrt_3",
								MetricNames: []string{"kube_pod_container_resource_requests"},
							},
						},
					},
				},
			},
			isConflict: false,
		},
		//gzl：测试用例2 不匹配黑名单规则 isConflict为true
		"default-2": {
			queryRef: metadata.QueryReference{
				"a": {
					{
						QueryList: []*metadata.Query{
							{
								TableID: "result_table.vm_1",
								VmRt:    "vmrt_1",
								Field:   "container_cpu_usage_seconds",
							},
							{
								TableID: "result_table.vm_2",
								VmRt:    "vmrt_2",
								Field:   "container_cpu_usage_seconds",
							},
							{
								TableID: "result_table.vm_3",
								VmRt:    "vmrt_3",
								Field:   "container_cpu_usage_seconds",
							},
						},
					},
				},
				"b": {
					{
						QueryList: []*metadata.Query{
							{
								TableID:     "result_table.vm_1",
								VmRt:        "vmrt_1",
								MetricNames: []string{"kube_pod_container_resource_requests"},
							},
							{
								TableID:     "result_table.vm_2",
								VmRt:        "vmrt_2",
								MetricNames: []string{"kube_pod_container_resource_requests"},
							},
						},
					},
				},
			},
			isConflict: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			VmExpand := ToVmExpand(ctx, c.queryRef)
			isConflict, err := influxdbRouter.CheckVMRT(VmExpand.ResultTableList) // vm rt 黑名单规则检查
			if err != nil {
				t.Errorf("check vm rt failed, error:%s", err)
			}
			assert.Equal(t, c.isConflict, isConflict)

		})
	}
}
