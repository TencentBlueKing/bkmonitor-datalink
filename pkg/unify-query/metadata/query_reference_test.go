// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
)

func TestVmExpand(t *testing.T) {
	ctx := InitHashID(context.Background())

	for name, c := range map[string]struct {
		queryRef QueryReference
		vmExpand *VmExpand
	}{
		"default-1": {
			queryRef: QueryReference{
				"a": {
					{
						QueryList: []*Query{
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
						QueryList: []*Query{
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
			vmExpand: &VmExpand{
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
			queryRef: QueryReference{
				"a": {
					{
						QueryList: []*Query{
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
						QueryList: []*Query{
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
			vmExpand: &VmExpand{
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
			queryRef: QueryReference{
				"a": {
					{
						QueryList: []*Query{
							{
								TableID:     "result_table.vm",
								VmRt:        "vm_result_table",
								MetricNames: []string{"container_cpu_usage_seconds"},
								VmCondition: `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table"`,
							},
						},
					},
					{
						QueryList: []*Query{
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
						QueryList: []*Query{
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
			vmExpand: &VmExpand{
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
			ctx = InitHashID(ctx)
			vmExpand := c.queryRef.ToVmExpand(ctx)

			//
			for k, v := range vmExpand.MetricFilterCondition {
				or := " or "
				arr := strings.Split(v, or)
				sort.Strings(arr)
				vmExpand.MetricFilterCondition[k] = strings.Join(arr, or)
			}

			assert.Equal(t, c.vmExpand, vmExpand)
		})
	}
}

func TestQuery_ConfigureAlias(t *testing.T) {
	o := "__ext.container_name"
	n := "container_name"

	query := Query{
		FieldAlias: FieldAlias{
			n: o,
		},
		Field: n,
		AllConditions: AllConditions{
			{
				{
					DimensionName: n,
					Operator:      ConditionNotEqual,
					Value:         []string{""},
				},
			},
		},
		Aggregates: Aggregates{
			{
				Dimensions: []string{n},
				Field:      n,
				Name:       function.Count,
				Window:     time.Hour,
			},
		},
		Source: []string{n},
		Orders: Orders{
			{
				Name: n,
			},
		},
		Collapse: &Collapse{
			Field: n,
		},
	}

	var queryStr []byte
	queryStr, _ = json.Marshal(query)
	assert.Equal(t, string(queryStr), `{"field":"container_name","time_field":{},"field_alias":{"container_name":"__ext.container_name"},"aggregates":[{"name":"count","field":"container_name","dimensions":["container_name"],"window":3600000000000}],"offset_info":{"OffSet":0,"Limit":0,"SOffSet":0,"SLimit":0},"all_conditions":[[{"DimensionName":"container_name","Value":[""],"Operator":"ne","IsWildcard":false,"IsPrefix":false,"IsSuffix":false,"IsForceEq":false}]],"source":["container_name"],"orders":[{"Name":"container_name","Ast":false}],"collapse":{"field":"container_name"}}`)

	query.ConfigureAlias(context.TODO())

	queryStr, _ = json.Marshal(query)
	assert.Equal(t, string(queryStr), `{"field":"__ext.container_name","time_field":{},"field_alias":{"container_name":"__ext.container_name"},"aggregates":[{"name":"count","field":"__ext.container_name","dimensions":["__ext.container_name"],"window":3600000000000}],"offset_info":{"OffSet":0,"Limit":0,"SOffSet":0,"SLimit":0},"all_conditions":[[{"DimensionName":"__ext.container_name","Value":[""],"Operator":"ne","IsWildcard":false,"IsPrefix":false,"IsSuffix":false,"IsForceEq":false}]],"source":["__ext.container_name"],"orders":[{"Name":"__ext.container_name","Ast":false}],"collapse":{"field":"__ext.container_name"}}`)

}
