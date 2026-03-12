// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
)

func TestSpaceFilter_NewTsDBs(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	mock.Init()

	testCases := map[string]struct {
		tableID      TableID
		fieldName    string
		isRegexp     bool
		allCondition AllConditions

		isSkipSpace bool
		isSkipField bool
		isSkipK8s   bool

		expected string
	}{
		"test_1": {
			fieldName: "kube_node_info",
			tableID:   "",
			expected:  `[{"table_id":"result_table.influxdb","field":["kube_pod_info","kube_node_info","kube_node_status_condition","kubelet_cluster_request_total","merltrics_rest_request_status_200_count","merltrics_rest_request_status_500_count"],"measurement_type":"bk_split_measurement","data_label":"influxdb","storage_id":"2","cluster_name":"default","db":"result_table","measurement":"influxdb","metric_name":"kube_node_info","expand_metric_names":["kube_node_info"],"time_field":{},"need_add_time":false,"storage_type":"influxdb"}]`,
		},
		"test_2_regex": {
			fieldName: "kubelet_.+",
			isRegexp:  true,
			expected:  `[{"table_id":"result_table.influxdb","field":["kube_pod_info","kube_node_info","kube_node_status_condition","kubelet_cluster_request_total","merltrics_rest_request_status_200_count","merltrics_rest_request_status_500_count"],"measurement_type":"bk_split_measurement","data_label":"influxdb","storage_id":"2","cluster_name":"default","db":"result_table","measurement":"influxdb","metric_name":"kubelet_.+","expand_metric_names":["kubelet_cluster_request_total"],"time_field":{},"need_add_time":false,"storage_type":"influxdb"},{"table_id":"result_table.vm","field":["container_cpu_usage_seconds_total","kube_pod_info","node_with_pod_relation","node_with_system_relation","deployment_with_replicaset_relation","pod_with_replicaset_relation","apm_service_instance_with_pod_relation","apm_service_instance_with_system_relation","container_info_relation","host_info_relation","kubelet_info"],"measurement_type":"bk_split_measurement","data_label":"kubelet_info","storage_id":"2","db":"other","measurement":"kubelet_info","vm_rt":"2_bcs_prom_computation_result_table","metric_name":"kubelet_.+","expand_metric_names":["kubelet_info"],"time_field":{},"need_add_time":false,"storage_type":"victoria_metrics"}]`,
		},
		"test_3_regex": {
			fieldName: "container_.+",
			isRegexp:  true,
			expected:  `[{"table_id":"result_table.vm","field":["container_cpu_usage_seconds_total","kube_pod_info","node_with_pod_relation","node_with_system_relation","deployment_with_replicaset_relation","pod_with_replicaset_relation","apm_service_instance_with_pod_relation","apm_service_instance_with_system_relation","container_info_relation","host_info_relation","kubelet_info"],"measurement_type":"bk_split_measurement","data_label":"vm","storage_id":"2","vm_rt":"2_bcs_prom_computation_result_table","metric_name":"container_.+","expand_metric_names":["container_cpu_usage_seconds_total","container_info_relation"],"time_field":{},"need_add_time":false,"storage_type":"victoria_metrics"}]`,
		},
		"test_4_incomplete_tableid_from_datalabel": {
			fieldName: "kube_pod_info",
			tableID:   "influxdb",
			expected:  `[{"table_id":"result_table.influxdb","field":["kube_pod_info","kube_node_info","kube_node_status_condition","kubelet_cluster_request_total","merltrics_rest_request_status_200_count","merltrics_rest_request_status_500_count"],"measurement_type":"bk_split_measurement","data_label":"influxdb","storage_id":"2","cluster_name":"default","db":"result_table","measurement":"influxdb","metric_name":"kube_pod_info","expand_metric_names":["kube_pod_info"],"time_field":{},"need_add_time":false,"storage_type":"influxdb"},{"table_id":"result_table.vm","field":["container_cpu_usage_seconds_total","kube_pod_info","node_with_pod_relation","node_with_system_relation","deployment_with_replicaset_relation","pod_with_replicaset_relation","apm_service_instance_with_pod_relation","apm_service_instance_with_system_relation","container_info_relation","host_info_relation","kubelet_info"],"measurement_type":"bk_split_measurement","data_label":"vm","storage_id":"2","vm_rt":"2_bcs_prom_computation_result_table","metric_name":"kube_pod_info","expand_metric_names":["kube_pod_info"],"time_field":{},"need_add_time":false,"storage_type":"victoria_metrics"}]`,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			influxdb.MockSpaceRouter(ctx)

			sf, err := NewSpaceFilter(ctx, &TsDBOption{
				SpaceUid: influxdb.SpaceUid,
			})
			assert.NoError(t, err)

			tsdb, err := sf.DataList(&TsDBOption{
				IsSkipSpace:   c.isSkipSpace,
				IsSkipField:   c.isSkipField,
				IsSkipK8s:     c.isSkipK8s,
				TableID:       c.tableID,
				FieldName:     c.fieldName,
				IsRegexp:      c.isRegexp,
				AllConditions: c.allCondition,
			})

			actual := toJson(tsdb)
			assert.Equal(t, c.expected, actual)
		})
	}
}

func toJson(q []*query.TsDBV2) string {
	sort.SliceStable(q, func(i, j int) bool {
		return q[i].TableID < q[j].TableID
	})

	s, _ := json.Marshal(q)
	return string(s)
}

func TestMatchLabels(t *testing.T) {
	testCases := map[string]struct {
		labels     map[string]string
		conditions map[string]string
		expected   bool
	}{
		"exact_match": {
			labels:     map[string]string{"scene": "log", "cluster_id": "BCS-K8S-00001"},
			conditions: map[string]string{"scene": "log", "cluster_id": "BCS-K8S-00001"},
			expected:   true,
		},
		"subset_match": {
			labels:     map[string]string{"scene": "log", "cluster_id": "BCS-K8S-00001"},
			conditions: map[string]string{"scene": "log"},
			expected:   true,
		},
		"mismatch_value": {
			labels:     map[string]string{"scene": "log"},
			conditions: map[string]string{"scene": "k8s"},
			expected:   false,
		},
		"missing_key": {
			labels:     map[string]string{"scene": "log"},
			conditions: map[string]string{"cluster_id": "BCS-K8S-00001"},
			expected:   false,
		},
		"empty_labels": {
			labels:     map[string]string{},
			conditions: map[string]string{"scene": "log"},
			expected:   false,
		},
		"nil_labels": {
			labels:     nil,
			conditions: map[string]string{"scene": "log"},
			expected:   false,
		},
		"empty_conditions": {
			labels:     map[string]string{"scene": "log"},
			conditions: map[string]string{},
			expected:   true,
		},
		"both_empty": {
			labels:     map[string]string{},
			conditions: map[string]string{},
			expected:   false,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			result := matchLabels(c.labels, c.conditions)
			assert.Equal(t, c.expected, result)
		})
	}
}

func TestSpaceFilter_DataList_WithTableIDConditions(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	mock.Init()

	testCases := map[string]struct {
		tableID           TableID
		tableIDConditions map[string]string
		fieldName         string
		isSkipField       bool
		expectTableIDs    []string
		expectErr         bool
	}{
		"match_by_scene_log": {
			tableIDConditions: map[string]string{"scene": "log"},
			isSkipField:       true,
			expectTableIDs:    []string{influxdb.ResultTableEs},
		},
		"match_by_scene_and_cluster": {
			tableIDConditions: map[string]string{"scene": "log", "cluster_id": "BCS-K8S-00001"},
			isSkipField:       true,
			expectTableIDs:    []string{influxdb.ResultTableEs},
		},
		"no_match": {
			tableIDConditions: map[string]string{"scene": "metric"},
			isSkipField:       true,
			expectTableIDs:    nil,
		},
		// 仅传 TableIDConditions，不传 TableID 和 FieldName，应该不报错（输入校验放宽）
		"only_conditions_no_field_no_table": {
			tableIDConditions: map[string]string{"scene": "log"},
			fieldName:         "",
			isSkipField:       true,
			expectTableIDs:    []string{influxdb.ResultTableEs},
		},
		// TableID + FieldName + Conditions 都为空 → 应报错
		"all_empty_returns_error": {
			tableID:           "",
			tableIDConditions: nil,
			fieldName:         "",
			expectErr:         true,
		},
		// 空 map 等价于不传，走原逻辑（无 TableID + 无 FieldName → 报错）
		"empty_map_conditions_falls_through": {
			tableIDConditions: map[string]string{},
			fieldName:         "",
			expectErr:         true,
		},
		// conditions 中有一个 key 不匹配就整体不通过
		"partial_key_mismatch": {
			tableIDConditions: map[string]string{"scene": "log", "env": "prod"},
			isSkipField:       true,
			expectTableIDs:    nil,
		},
		// TableID + TableIDConditions 同时存在时，两者都生效：
		// TableID 决定候选集，TableIDConditions 在 NewTsDBs 中做二次过滤
		// system.cpu_summary 没有 Labels，所以被过滤掉
		"tableid_with_conditions_both_applied": {
			tableID:           "system.cpu_summary",
			tableIDConditions: map[string]string{"scene": "log"},
			fieldName:         "usage",
			expectTableIDs:    nil,
		},
		// TableID 存在且无 conditions 时走原始逻辑，不受 labels 影响
		"tableid_without_conditions_works": {
			tableID:        "system.cpu_summary",
			fieldName:      "usage",
			expectTableIDs: []string{"system.cpu_summary"},
		},
		// TableIDConditions + FieldName 组合：先 labels 过滤再字段过滤
		// ResultTableEs 没有 Fields，isSkipField=false 时字段匹配走空返回
		"conditions_with_field_filter_no_fields": {
			tableIDConditions: map[string]string{"scene": "log"},
			fieldName:         "some_metric",
			isSkipField:       false,
			expectTableIDs:    nil,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			influxdb.MockSpaceRouter(ctx)

			sf, err := NewSpaceFilter(ctx, &TsDBOption{
				SpaceUid: influxdb.SpaceUid,
			})
			assert.NoError(t, err)

			tsdb, err := sf.DataList(&TsDBOption{
				SpaceUid:          influxdb.SpaceUid,
				TableID:           c.tableID,
				TableIDConditions: c.tableIDConditions,
				FieldName:         c.fieldName,
				IsSkipField:       c.isSkipField,
			})

			if c.expectErr {
				assert.Error(t, err)
				return
			}

			if c.expectTableIDs == nil {
				assert.Nil(t, tsdb)
			} else {
				assert.NotNil(t, tsdb)
				actual := make([]string, 0, len(tsdb))
				for _, db := range tsdb {
					actual = append(actual, db.TableID)
				}
				sort.Strings(actual)
				sort.Strings(c.expectTableIDs)
				assert.Equal(t, c.expectTableIDs, actual)
			}
		})
	}
}

// TestHasTableIDConditions 单独测试 hasTableIDConditions 方法
func TestHasTableIDConditions(t *testing.T) {
	testCases := map[string]struct {
		opt      *TsDBOption
		expected bool
	}{
		// 仅 expr 非空
		"only_expr": {
			opt: &TsDBOption{
				TableIDConditionExpr: &TableIDConditionExpr{
					OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpEq, Value: "log"}}},
				},
			},
			expected: true,
		},
		// 仅 map 非空
		"only_map": {
			opt: &TsDBOption{
				TableIDConditions: map[string]string{"scene": "log"},
			},
			expected: true,
		},
		// 两者都非空
		"both_expr_and_map": {
			opt: &TsDBOption{
				TableIDConditionExpr: &TableIDConditionExpr{
					OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpEq, Value: "log"}}},
				},
				TableIDConditions: map[string]string{"scene": "log"},
			},
			expected: true,
		},
		// 两者都空
		"both_empty": {
			opt:      &TsDBOption{},
			expected: false,
		},
		// expr 为 nil，map 为空
		"nil_expr_empty_map": {
			opt: &TsDBOption{
				TableIDConditionExpr: nil,
				TableIDConditions:    map[string]string{},
			},
			expected: false,
		},
		// expr 非 nil 但 OrGroups 为空
		"expr_empty_conditions": {
			opt: &TsDBOption{
				TableIDConditionExpr: &TableIDConditionExpr{OrGroups: nil},
			},
			expected: false,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.expected, c.opt.hasTableIDConditions())
		})
	}
}

// TestSpaceFilter_DataList_WithTableIDConditionExpr 测试仅使用 TableIDConditionExpr（不使用 map）的 DataList 集成路径
func TestSpaceFilter_DataList_WithTableIDConditionExpr(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	mock.Init()

	testCases := map[string]struct {
		tableIDConditionExpr *TableIDConditionExpr
		fieldName            string
		isSkipField          bool
		expectTableIDs       []string
		expectErr            bool
	}{
		// eq 匹配 —— ResultTableEs 的 Labels 包含 scene=log
		"expr_eq_match_scene_log": {
			tableIDConditionExpr: &TableIDConditionExpr{
				OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpEq, Value: "log"}}},
			},
			isSkipField:    true,
			expectTableIDs: []string{influxdb.ResultTableEs},
		},
		// neq 匹配 —— scene != "metric"，ResultTableEs 的 scene=log 满足
		"expr_neq_match": {
			tableIDConditionExpr: &TableIDConditionExpr{
				OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpNeq, Value: "metric"}}},
			},
			isSkipField:    true,
			expectTableIDs: []string{influxdb.ResultTableEs},
		},
		// neq 不匹配 —— scene != "log"，ResultTableEs 的 scene=log 不满足
		"expr_neq_no_match": {
			tableIDConditionExpr: &TableIDConditionExpr{
				OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpNeq, Value: "log"}}},
			},
			isSkipField:    true,
			expectTableIDs: nil,
		},
		// reg 匹配 —— scene 正则匹配 "lo."
		"expr_reg_match": {
			tableIDConditionExpr: &TableIDConditionExpr{
				OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpReg, Value: "lo."}}},
			},
			isSkipField:    true,
			expectTableIDs: []string{influxdb.ResultTableEs},
		},
		// nreg 匹配 —— scene 不正则匹配 "^k8s"，ResultTableEs 的 scene=log 满足
		"expr_nreg_match": {
			tableIDConditionExpr: &TableIDConditionExpr{
				OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpNreg, Value: "^k8s"}}},
			},
			isSkipField:    true,
			expectTableIDs: []string{influxdb.ResultTableEs},
		},
		// 多条件 AND —— scene=log AND cluster_id=BCS-K8S-00001
		"expr_multi_conditions_match": {
			tableIDConditionExpr: &TableIDConditionExpr{
				OrGroups: [][]LabelCondition{{
					{Key: "scene", Op: LabelOpEq, Value: "log"},
					{Key: "cluster_id", Op: LabelOpEq, Value: "BCS-K8S-00001"},
				}},
			},
			isSkipField:    true,
			expectTableIDs: []string{influxdb.ResultTableEs},
		},
		// 多条件 AND 一个不满足
		"expr_multi_conditions_one_fail": {
			tableIDConditionExpr: &TableIDConditionExpr{
				OrGroups: [][]LabelCondition{{
					{Key: "scene", Op: LabelOpEq, Value: "log"},
					{Key: "cluster_id", Op: LabelOpEq, Value: "OTHER"},
				}},
			},
			isSkipField:    true,
			expectTableIDs: nil,
		},
		// expr 为空（Empty），回退到无条件路径 → 无 TableID 和 FieldName 时报错
		"expr_empty_no_field_no_table_error": {
			tableIDConditionExpr: &TableIDConditionExpr{OrGroups: nil},
			fieldName:            "",
			expectErr:            true,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			influxdb.MockSpaceRouter(ctx)

			sf, err := NewSpaceFilter(ctx, &TsDBOption{
				SpaceUid: influxdb.SpaceUid,
			})
			assert.NoError(t, err)

			tsdb, err := sf.DataList(&TsDBOption{
				SpaceUid:             influxdb.SpaceUid,
				TableIDConditionExpr: c.tableIDConditionExpr,
				FieldName:            c.fieldName,
				IsSkipField:          c.isSkipField,
			})

			if c.expectErr {
				assert.Error(t, err)
				return
			}

			if c.expectTableIDs == nil {
				assert.Nil(t, tsdb)
			} else {
				assert.NotNil(t, tsdb)
				actual := make([]string, 0, len(tsdb))
				for _, db := range tsdb {
					actual = append(actual, db.TableID)
				}
				sort.Strings(actual)
				sort.Strings(c.expectTableIDs)
				assert.Equal(t, c.expectTableIDs, actual)
			}
		})
	}
}
