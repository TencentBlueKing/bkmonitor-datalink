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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
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

func findTsDBByTableID(tsdb []*query.TsDBV2, tableID string) *query.TsDBV2 {
	for _, d := range tsdb {
		if d.TableID == tableID {
			return d
		}
	}
	return nil
}

// spaceRouterSeriesByReason 读取 unify_query_space_router_total 指定 reason 维度下，按 metric label 聚合的累计值，
// 用于断言兜底埋点是否上报，以及验证 metric label 未携带高基数的用户输入字段名。
func spaceRouterSeriesByReason(reason string) map[string]float64 {
	out := make(map[string]float64)
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return out
	}
	for _, mf := range mfs {
		if mf.GetName() != "unify_query_space_router_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			var gotReason, metricLabel string
			for _, l := range m.GetLabel() {
				switch l.GetName() {
				case "reason":
					gotReason = l.GetValue()
				case "metric":
					metricLabel = l.GetValue()
				}
			}
			if gotReason == reason {
				out[metricLabel] += m.GetCounter().GetValue()
			}
		}
	}
	return out
}

func sumFloat(m map[string]float64) float64 {
	var s float64
	for _, v := range m {
		s += v
	}
	return s
}

func TestSpaceFilter_DataList_ExplicitRouteFieldFallback(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	mock.Init()
	ctx = metadata.InitHashID(ctx)
	influxdb.MockSpaceRouter(ctx)

	sf, err := NewSpaceFilter(ctx, &TsDBOption{SpaceUid: influxdb.SpaceUid})
	require.NoError(t, err)

	t.Run("full_table_id_field_missing_fallbacks_to_original_rt", func(t *testing.T) {
		tsdb, err := sf.DataList(&TsDBOption{
			SpaceUid:    influxdb.SpaceUid,
			TableID:     "system.cpu_summary",
			FieldName:   "not_exists_metric",
			IsSkipField: false,
		})
		require.NoError(t, err)
		require.Len(t, tsdb, 1)
		assert.Equal(t, "system.cpu_summary", tsdb[0].TableID)
		assert.Equal(t, []string{"not_exists_metric"}, tsdb[0].ExpandMetricNames)
	})

	t.Run("field_missing_fallback_reports_distinct_low_cardinality_metric", func(t *testing.T) {
		// 兜底命中应上报区分性 reason 指标，避免 fallback 静默掩盖元数据缺失问题；
		// 同时 metric label 必须固定为空，避免用户输入的 fieldName 造成 Prometheus 高基数。
		const probeField = "fallback_metric_for_observability"
		beforeFallback := sumFloat(spaceRouterSeriesByReason(metadata.SpaceTableIDFieldMissingFallback))
		beforeNotExist := sumFloat(spaceRouterSeriesByReason(metadata.SpaceTableIDFieldIsNotExists))

		tsdb, err := sf.DataList(&TsDBOption{
			SpaceUid:    influxdb.SpaceUid,
			TableID:     "system.cpu_summary",
			FieldName:   probeField,
			IsSkipField: false,
		})
		require.NoError(t, err)
		require.Len(t, tsdb, 1)

		afterSeries := spaceRouterSeriesByReason(metadata.SpaceTableIDFieldMissingFallback)
		// 兜底命中：区分性指标总量 +1，而"完全找不到"指标不应增加。
		assert.Equal(t, beforeFallback+1, sumFloat(afterSeries))
		assert.Equal(t, beforeNotExist, sumFloat(spaceRouterSeriesByReason(metadata.SpaceTableIDFieldIsNotExists)))
		// 高基数防护：兜底指标的 metric label 固定为空，绝不能以用户输入的 fieldName 建时序。
		assert.Contains(t, afterSeries, "")
		assert.NotContains(t, afterSeries, probeField)
	})

	t.Run("data_label_field_missing_fallbacks_to_all_related_rts", func(t *testing.T) {
		tsdb, err := sf.DataList(&TsDBOption{
			SpaceUid:    influxdb.SpaceUid,
			TableID:     "influxdb",
			FieldName:   "not_exists_metric",
			IsSkipField: false,
		})
		require.NoError(t, err)
		require.Len(t, tsdb, 2)

		influxdbTsDB := findTsDBByTableID(tsdb, influxdb.ResultTableInfluxDB)
		require.NotNil(t, influxdbTsDB)
		assert.Equal(t, []string{"not_exists_metric"}, influxdbTsDB.ExpandMetricNames)

		vmTsDB := findTsDBByTableID(tsdb, influxdb.ResultTableVM)
		require.NotNil(t, vmTsDB)
		assert.Equal(t, []string{"not_exists_metric"}, vmTsDB.ExpandMetricNames)
	})

	t.Run("full_table_id_empty_field_does_not_fallback", func(t *testing.T) {
		tsdb, err := sf.DataList(&TsDBOption{
			SpaceUid:    influxdb.SpaceUid,
			TableID:     "system.cpu_summary",
			FieldName:   "",
			IsSkipField: false,
		})
		require.NoError(t, err)
		assert.Empty(t, tsdb)
	})

	t.Run("split_measurement_field_missing_fallback_keeps_current_rt_even_when_separate_metric_rt_exists", func(t *testing.T) {
		router, err := influxdb.GetSpaceTsDbRouter()
		require.NoError(t, err)

		metricName := "not_in_fields_but_sep_rt"
		err = router.Add(ctx, ir.ResultTableDetailKey, "result_table."+metricName, &ir.ResultTableDetail{
			StorageId:       2,
			TableId:         "result_table." + metricName,
			Fields:          []string{metricName},
			DB:              "other",
			Measurement:     metricName,
			VmRt:            "2_bcs_prom_computation_result_table",
			MeasurementType: redis.BkSplitMeasurement,
			StorageType:     metadata.VictoriaMetricsStorageType,
			DataLabel:       metricName,
		})
		require.NoError(t, err)

		tsdb, err := sf.DataList(&TsDBOption{
			SpaceUid:    influxdb.SpaceUid,
			TableID:     influxdb.ResultTableInfluxDB,
			FieldName:   metricName,
			IsSkipField: false,
		})
		require.NoError(t, err)
		require.Len(t, tsdb, 1)
		assert.Equal(t, []string{metricName}, tsdb[0].ExpandMetricNames)
		assert.Equal(t, influxdb.ResultTableInfluxDB, tsdb[0].TableID)
		assert.Equal(t, "influxdb", tsdb[0].Measurement)
		assert.Equal(t, "influxdb", tsdb[0].DataLabel)
		assert.Equal(t, metadata.InfluxDBStorageType, tsdb[0].StorageType)
	})

	t.Run("dotted_data_label_field_missing_fallback_keeps_data_label_rt_boundary", func(t *testing.T) {
		router, err := influxdb.GetSpaceTsDbRouter()
		require.NoError(t, err)

		dataLabel := "influx.db"
		metricName := "dotted_label_missing_metric"
		err = router.Add(ctx, ir.DataLabelToResultTableKey, dataLabel, &ir.ResultTableList{
			influxdb.ResultTableInfluxDB,
			influxdb.ResultTableVM,
		})
		require.NoError(t, err)
		err = router.Add(ctx, ir.ResultTableDetailKey, "result_table."+metricName, &ir.ResultTableDetail{
			StorageId:       2,
			TableId:         "result_table." + metricName,
			Fields:          []string{metricName},
			DB:              "other",
			Measurement:     metricName,
			VmRt:            "2_bcs_prom_computation_result_table",
			MeasurementType: redis.BkSplitMeasurement,
			StorageType:     metadata.VictoriaMetricsStorageType,
			DataLabel:       metricName,
		})
		require.NoError(t, err)

		tsdb, err := sf.DataList(&TsDBOption{
			SpaceUid:    influxdb.SpaceUid,
			TableID:     TableID(dataLabel),
			FieldName:   metricName,
			IsSkipField: false,
		})
		require.NoError(t, err)
		require.Len(t, tsdb, 2)

		influxdbTsDB := findTsDBByTableID(tsdb, influxdb.ResultTableInfluxDB)
		require.NotNil(t, influxdbTsDB)
		assert.Equal(t, []string{metricName}, influxdbTsDB.ExpandMetricNames)
		assert.Equal(t, "influxdb", influxdbTsDB.DataLabel)

		vmTsDB := findTsDBByTableID(tsdb, influxdb.ResultTableVM)
		require.NotNil(t, vmTsDB)
		assert.Equal(t, []string{metricName}, vmTsDB.ExpandMetricNames)
		assert.Equal(t, "vm", vmTsDB.DataLabel)
	})

	t.Run("dotted_data_label_with_mapping_does_not_fallback_unrelated_same_named_table_id", func(t *testing.T) {
		// 回归 Codex 评论 3：dotted data_label 命中映射时，兼容分支合成的同名 table_id
		// （system.disk 是 space 中真实存在、但不属于该 data_label 映射的 RT，dataLabel=disk）
		// 不应越过 data_label 边界被字段缺失兜底返回。
		router, err := influxdb.GetSpaceTsDbRouter()
		require.NoError(t, err)

		// data_label "system.disk" 仅映射到 influxdb，与 space 中真实的 system.disk RT 无关。
		err = router.Add(ctx, ir.DataLabelToResultTableKey, "system.disk", &ir.ResultTableList{
			influxdb.ResultTableInfluxDB,
		})
		require.NoError(t, err)

		tsdb, err := sf.DataList(&TsDBOption{
			SpaceUid:    influxdb.SpaceUid,
			TableID:     "system.disk",
			FieldName:   "missing_field_not_in_any_rt",
			IsSkipField: false,
		})
		require.NoError(t, err)
		// 只返回 data_label 映射内的 influxdb；同名真实 RT system.disk 不应被越界兜底。
		require.Len(t, tsdb, 1)
		assert.Equal(t, influxdb.ResultTableInfluxDB, tsdb[0].TableID)
		assert.Nil(t, findTsDBByTableID(tsdb, "system.disk"))
	})

	t.Run("explicit_route_regex_match_keeps_field_expansion", func(t *testing.T) {
		tsdb, err := sf.DataList(&TsDBOption{
			SpaceUid:    influxdb.SpaceUid,
			TableID:     "influxdb",
			FieldName:   "kubelet_.+",
			IsRegexp:    true,
			IsSkipField: false,
		})
		require.NoError(t, err)

		influxdbTsDB := findTsDBByTableID(tsdb, influxdb.ResultTableInfluxDB)
		require.NotNil(t, influxdbTsDB)
		assert.Equal(t, []string{"kubelet_cluster_request_total"}, influxdbTsDB.ExpandMetricNames)

		vmTsDB := findTsDBByTableID(tsdb, influxdb.ResultTableVM)
		require.NotNil(t, vmTsDB)
		assert.Equal(t, []string{"kubelet_info"}, vmTsDB.ExpandMetricNames)
	})

	t.Run("explicit_route_regex_missing_does_not_fallback_as_literal_metric", func(t *testing.T) {
		tsdb, err := sf.DataList(&TsDBOption{
			SpaceUid:    influxdb.SpaceUid,
			TableID:     "influxdb",
			FieldName:   "not_exists_.+",
			IsRegexp:    true,
			IsSkipField: false,
		})
		require.NoError(t, err)
		assert.Empty(t, tsdb)
	})

	t.Run("full_space_field_missing_still_returns_empty", func(t *testing.T) {
		tsdb, err := sf.DataList(&TsDBOption{
			SpaceUid:    influxdb.SpaceUid,
			TableID:     "",
			FieldName:   "not_exists_metric",
			IsSkipK8s:   true,
			IsSkipField: false,
		})
		require.NoError(t, err)
		assert.Empty(t, tsdb)
	})
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

	// 非 split-measurement 的 RT（如 bklog 日志表 ResultTableEs，Labels scene=k8s）在容器默认过滤下会被误杀；
	// 显式传 table_id_conditions 时应绕过容器默认规则，按 Labels 选表，不再要求 bk_split_measurement。
	t.Run("non_split_rt_with_conditions_bypasses_k8s_filter", func(t *testing.T) {
		opt := *optBase
		opt.FieldName = "dtEventTimeStamp"
		opt.IsRegexp = false
		opt.IsSkipField = true
		opt.IsSkipK8s = false
		opt.TableIDConditions = AllConditions{
			{{DimensionName: "scene", Value: []string{"k8s"}, Operator: ConditionEqual}},
		}
		tsdb, err := sf.DataList(&opt)
		require.NoError(t, err)
		tableIDs := make([]string, 0, len(tsdb))
		for _, d := range tsdb {
			tableIDs = append(tableIDs, d.TableID)
		}
		assert.Contains(t, tableIDs, influxdb.ResultTableEs, "显式 table_id_conditions 下，非 split-measurement 的 RT 在 Labels 命中后应被选中（不叠加容器默认过滤）")
	})

	t.Run("non_split_rt_without_conditions_is_filtered_by_k8s_default", func(t *testing.T) {
		opt := *optBase
		opt.FieldName = "dtEventTimeStamp"
		opt.IsRegexp = false
		opt.IsSkipField = true
		opt.IsSkipK8s = false
		opt.TableIDConditions = nil
		tsdb, err := sf.DataList(&opt)
		require.NoError(t, err)
		tableIDs := make([]string, 0, len(tsdb))
		for _, d := range tsdb {
			tableIDs = append(tableIDs, d.TableID)
		}
		assert.NotContains(t, tableIDs, influxdb.ResultTableEs, "无 table_id_conditions 时维持容器默认规则，非 split-measurement 的 RT 应被过滤")
	})
}
