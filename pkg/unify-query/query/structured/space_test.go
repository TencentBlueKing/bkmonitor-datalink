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
	"github.com/stretchr/testify/require"

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

// TestSpaceFilter_DataList_WithTableIDConditions 表标签条件过滤：nil 时行为不变；有 expr 且 RT 无匹配 Labels 时被过滤
func TestSpaceFilter_DataList_WithTableIDConditions(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	mock.Init()

	t.Run("nil_expr_same_as_before", func(t *testing.T) {
		ctx = metadata.InitHashID(ctx)
		influxdb.MockSpaceRouter(ctx)
		sf, err := NewSpaceFilter(ctx, &TsDBOption{SpaceUid: influxdb.SpaceUid})
		require.NoError(t, err)
		opt := &TsDBOption{
			SpaceUid:    influxdb.SpaceUid,
			FieldName:   "kube_node_info",
			TableID:     "",
			IsRegexp:    false,
			IsSkipK8s:   true,
			IsSkipField: false,
		}
		tsdb, err := sf.DataList(opt)
		require.NoError(t, err)
		// 无 TableIDConditions 时与原有逻辑一致，应拿到多个 TsDB
		assert.Greater(t, len(tsdb), 0)
	})

	t.Run("expr_no_match_filtered", func(t *testing.T) {
		ctx = metadata.InitHashID(ctx)
		influxdb.MockSpaceRouter(ctx)
		sf, err := NewSpaceFilter(ctx, &TsDBOption{SpaceUid: influxdb.SpaceUid})
		require.NoError(t, err)
		// mock 中 influxdb 为 scene=log、vm 为 scene=k8s；scene=other 无匹配，应 0 个 TsDB
		opt := &TsDBOption{
			SpaceUid:          influxdb.SpaceUid,
			FieldName:         "kube_node_info",
			TableID:           "",
			IsRegexp:          false,
			IsSkipK8s:         true,
			IsSkipField:       false,
			TableIDConditions: AllConditions{{{DimensionName: "scene", Value: []string{"other"}, Operator: ConditionEqual}}},
		}
		tsdb, err := sf.DataList(opt)
		require.NoError(t, err)
		assert.Equal(t, 0, len(tsdb))
	})

	t.Run("invalid_regex_in_table_id_conditions_returns_error", func(t *testing.T) {
		ctx = metadata.InitHashID(ctx)
		influxdb.MockSpaceRouter(ctx)
		sf, err := NewSpaceFilter(ctx, &TsDBOption{SpaceUid: influxdb.SpaceUid})
		require.NoError(t, err)
		opt := &TsDBOption{
			SpaceUid:    influxdb.SpaceUid,
			FieldName:   "kube_node_info",
			TableID:     "",
			IsRegexp:    false,
			IsSkipK8s:   true,
			IsSkipField: false,
			TableIDConditions: AllConditions{{
				{DimensionName: "scene", Value: []string{"("}, Operator: ConditionRegEqual},
			}},
		}
		tsdb, err := sf.DataList(opt)
		require.Error(t, err)
		assert.Empty(t, tsdb)
	})
}

// TestE2E_DataList_FilterResultTableByLabel 端到端：按表标签过滤 result table；mock 中 influxdb 为 scene=log、vm 为 scene=k8s
func TestE2E_DataList_FilterResultTableByLabel(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	mock.Init()
	ctx = metadata.InitHashID(ctx)
	influxdb.MockSpaceRouter(ctx)

	sf, err := NewSpaceFilter(ctx, &TsDBOption{SpaceUid: influxdb.SpaceUid})
	require.NoError(t, err)

	// 使用 kubelet_.+ 与 IsRegexp 使无过滤时返回 influxdb + vm（与 test_2_regex 一致）
	optBase := &TsDBOption{
		SpaceUid:    influxdb.SpaceUid,
		FieldName:   "kubelet_.+",
		TableID:     "",
		IsRegexp:    true,
		IsSkipK8s:   true,
		IsSkipField: false,
	}

	t.Run("no_expr_returns_both", func(t *testing.T) {
		tsdb, err := sf.DataList(optBase)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(tsdb), 2, "无表标签条件时应返回至少 2 个 TsDB（influxdb + vm）")
		tableIDs := make([]string, 0, len(tsdb))
		for _, d := range tsdb {
			tableIDs = append(tableIDs, d.TableID)
		}
		assert.Contains(t, tableIDs, influxdb.ResultTableInfluxDB)
		assert.Contains(t, tableIDs, influxdb.ResultTableVM)
	})

	t.Run("scene_eq_log_returns_only_influxdb", func(t *testing.T) {
		opt := *optBase
		opt.TableIDConditions = AllConditions{{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}}}
		tsdb, err := sf.DataList(&opt)
		require.NoError(t, err)
		require.Len(t, tsdb, 1, "scene=log 应只命中 result_table.influxdb")
		assert.Equal(t, influxdb.ResultTableInfluxDB, tsdb[0].TableID)
	})

	t.Run("scene_eq_k8s_returns_only_vm", func(t *testing.T) {
		opt := *optBase
		opt.TableIDConditions = AllConditions{{{DimensionName: "scene", Value: []string{"k8s"}, Operator: ConditionEqual}}}
		tsdb, err := sf.DataList(&opt)
		require.NoError(t, err)
		require.Len(t, tsdb, 1, "scene=k8s 应只命中 result_table.vm，若为 0 请确认 mock 中 ResultTableVM 已设置 Labels scene=k8s")
		assert.Equal(t, influxdb.ResultTableVM, tsdb[0].TableID)
	})

	t.Run("or_scene_log_or_k8s_returns_both", func(t *testing.T) {
		opt := *optBase
		opt.TableIDConditions = AllConditions{
			{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}},
			{{DimensionName: "scene", Value: []string{"k8s"}, Operator: ConditionEqual}},
		}
		tsdb, err := sf.DataList(&opt)
		require.NoError(t, err)
		require.Len(t, tsdb, 2, "scene=log or scene=k8s 应命中 influxdb 与 vm")
		tableIDs := make([]string, 0, len(tsdb))
		for _, d := range tsdb {
			tableIDs = append(tableIDs, d.TableID)
		}
		assert.Contains(t, tableIDs, influxdb.ResultTableInfluxDB)
		assert.Contains(t, tableIDs, influxdb.ResultTableVM)
	})

	// AND 多标签：mock 中 influxdb=scene=log,cluster_id=1；vm=scene=k8s,cluster_id=2
	t.Run("and_scene_log_cluster_id_1_returns_only_influxdb", func(t *testing.T) {
		opt := *optBase
		opt.TableIDConditions = AllConditions{{
			{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual},
			{DimensionName: "cluster_id", Value: []string{"1"}, Operator: ConditionEqual},
		}}
		tsdb, err := sf.DataList(&opt)
		require.NoError(t, err)
		require.Len(t, tsdb, 1, "scene=log AND cluster_id=1 应只命中 result_table.influxdb")
		assert.Equal(t, influxdb.ResultTableInfluxDB, tsdb[0].TableID)
	})

	t.Run("and_scene_log_cluster_id_2_returns_none", func(t *testing.T) {
		opt := *optBase
		opt.TableIDConditions = AllConditions{{
			{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual},
			{DimensionName: "cluster_id", Value: []string{"2"}, Operator: ConditionEqual},
		}}
		tsdb, err := sf.DataList(&opt)
		require.NoError(t, err)
		assert.Len(t, tsdb, 0, "scene=log AND cluster_id=2 无匹配（influxdb 为 cluster_id=1）")
	})

	// neq：scene!=k8s 命中 influxdb；scene!=log 命中 vm
	t.Run("scene_neq_k8s_returns_only_influxdb", func(t *testing.T) {
		opt := *optBase
		opt.TableIDConditions = AllConditions{
			{{DimensionName: "scene", Value: []string{"k8s"}, Operator: ConditionNotEqual}},
		}
		tsdb, err := sf.DataList(&opt)
		require.NoError(t, err)
		require.Len(t, tsdb, 1, "scene!=k8s 应只命中 result_table.influxdb（scene=log）")
		assert.Equal(t, influxdb.ResultTableInfluxDB, tsdb[0].TableID)
	})

	t.Run("scene_neq_log_returns_only_vm", func(t *testing.T) {
		opt := *optBase
		opt.TableIDConditions = AllConditions{
			{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionNotEqual}},
		}
		tsdb, err := sf.DataList(&opt)
		require.NoError(t, err)
		require.Len(t, tsdb, 1, "scene!=log 应只命中 result_table.vm（scene=k8s）")
		assert.Equal(t, influxdb.ResultTableVM, tsdb[0].TableID)
	})

	// 正则：scene=~"log.*" 命中 influxdb；scene=~"k8s" 命中 vm；scene!~"metric.*" 两个都命中
	t.Run("scene_regex_log_star_returns_only_influxdb", func(t *testing.T) {
		opt := *optBase
		opt.TableIDConditions = AllConditions{
			{{DimensionName: "scene", Value: []string{"log.*"}, Operator: ConditionRegEqual}},
		}
		tsdb, err := sf.DataList(&opt)
		require.NoError(t, err)
		require.Len(t, tsdb, 1, "scene=~\"log.*\" 应只命中 result_table.influxdb")
		assert.Equal(t, influxdb.ResultTableInfluxDB, tsdb[0].TableID)
	})

	t.Run("scene_regex_k8s_returns_only_vm", func(t *testing.T) {
		opt := *optBase
		opt.TableIDConditions = AllConditions{
			{{DimensionName: "scene", Value: []string{"k8s"}, Operator: ConditionRegEqual}},
		}
		tsdb, err := sf.DataList(&opt)
		require.NoError(t, err)
		require.Len(t, tsdb, 1, "scene=~\"k8s\" 应只命中 result_table.vm")
		assert.Equal(t, influxdb.ResultTableVM, tsdb[0].TableID)
	})

	t.Run("scene_nregex_metric_star_returns_both", func(t *testing.T) {
		opt := *optBase
		opt.TableIDConditions = AllConditions{
			{{DimensionName: "scene", Value: []string{"metric.*"}, Operator: ConditionNotRegEqual}},
		}
		tsdb, err := sf.DataList(&opt)
		require.NoError(t, err)
		require.Len(t, tsdb, 2, "scene!~\"metric.*\" 应命中 log 与 k8s（两者均不匹配 metric.*）")
		tableIDs := make([]string, 0, len(tsdb))
		for _, d := range tsdb {
			tableIDs = append(tableIDs, d.TableID)
		}
		assert.Contains(t, tableIDs, influxdb.ResultTableInfluxDB)
		assert.Contains(t, tableIDs, influxdb.ResultTableVM)
	})

	// 已指定 table_id / data_label（Split 后 db 非空）时，不再套用 table_id_conditions，避免与显式选表互斥。
	t.Run("with_data_label_ignores_table_id_conditions", func(t *testing.T) {
		opt := *optBase
		opt.TableID = "influxdb"
		opt.TableIDConditions = AllConditions{
			{{DimensionName: "scene", Value: []string{"other"}, Operator: ConditionEqual}},
		}
		tsdb, err := sf.DataList(&opt)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(tsdb), 2, "有 data_label 时 table_id_conditions 应被忽略，仍应命中 influxdb+vm（mock 中二者同属 influxdb data_label）")
		tableIDs := make([]string, 0, len(tsdb))
		for _, d := range tsdb {
			tableIDs = append(tableIDs, d.TableID)
		}
		assert.Contains(t, tableIDs, influxdb.ResultTableInfluxDB)
		assert.Contains(t, tableIDs, influxdb.ResultTableVM)
	})
}
