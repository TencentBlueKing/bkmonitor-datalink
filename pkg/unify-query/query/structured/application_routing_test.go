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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

const (
	applicationRoutingSpace  = "test__application_routing"
	applicationRoutingLabel  = "application_relation"
	applicationRoutingMetric = "apm_service_instance_with_system_relation"
)

var applicationRoutingFields = []string{
	"apm_service_with_apm_service_instance_relation",
	"apm_service_instance_with_pod_relation",
	applicationRoutingMetric,
	"apm_custom_metric",
}

func applicationCondition(operator string, values ...string) AllConditions {
	return AllConditions{{{DimensionName: ApmApplicationName, Operator: operator, Value: values}}}
}

func TestAllConditions_MayMatchLabel(t *testing.T) {
	for _, tc := range []struct {
		name       string
		conditions AllConditions
		value      string
		want       bool
		wantError  bool
	}{
		{"no_conditions", nil, "alpha", true, false},
		{"equal", applicationCondition(ConditionEqual, "alpha"), "alpha", true, false},
		{"different_application", applicationCondition(ConditionEqual, "alpha"), "beta", false, false},
		{"equal_multiple_values", applicationCondition(ConditionEqual, "alpha", "beta"), "beta", true, false},
		{"equal_literals_not_regex", applicationCondition(ConditionEqual, "alpha.one", "beta"), "alphaXone", false, false},
		{"not_equal", applicationCondition(ConditionNotEqual, "alpha"), "alpha", false, false},
		{"not_equal_multiple_values", applicationCondition(ConditionNotEqual, "alpha", "beta"), "gamma", true, false},
		{"contains", applicationCondition(ConditionContains, "alpha", "beta"), "beta", true, false},
		{"contains_is_exact", applicationCondition(ConditionContains, "alpha"), "alpha-extra", false, false},
		{"not_contains", applicationCondition(ConditionNotContains, "alpha", "beta"), "beta", false, false},
		{"regex_multiple_values_are_or", applicationCondition(ConditionRegEqual, "alpha.*", "beta.*"), "beta", true, false},
		{"regex_miss", applicationCondition(ConditionRegEqual, "alpha.*", "beta.*"), "gamma", false, false},
		{"regex_influx_substring_kept", applicationCondition(ConditionRegEqual, "alpha"), "alpha-extra", true, false},
		{"negative_regex_vm_full_match_kept", applicationCondition(ConditionNotRegEqual, "alpha"), "alpha-extra", true, false},
		{"negative_regex_full_match_excluded", applicationCondition(ConditionNotRegEqual, "alpha.*", "beta.*"), "beta", false, false},
		{"regex_vm_dot_matches_newline", applicationCondition(ConditionRegEqual, "alpha.*"), "alpha\nextra", true, false},
		{"negative_regex_influx_newline_kept", applicationCondition(ConditionNotRegEqual, "alpha.*extra"), "alpha\nextra", true, false},
		{"negative_regex_flags_per_influx_value", applicationCondition(ConditionNotRegEqual, "(?i)alpha", "beta"), "BETA", true, false},
		{"invalid_regex", applicationCondition(ConditionRegEqual, "["), "alpha", false, true},
		{"empty_values_ignored", applicationCondition(ConditionEqual), "alpha", true, false},
		{"unsupported_operator_ignored", applicationCondition(ConditionGt, "beta"), "alpha", true, false},
		{"and_conflicting_applications", AllConditions{{
			{DimensionName: ApmApplicationName, Operator: ConditionEqual, Value: []string{"alpha"}},
			{DimensionName: ApmApplicationName, Operator: ConditionNotEqual, Value: []string{"alpha"}},
		}}, "alpha", false, false},
		{"or_application_branches", AllConditions{
			{{DimensionName: ApmApplicationName, Operator: ConditionEqual, Value: []string{"alpha"}}},
			{{DimensionName: ApmApplicationName, Operator: ConditionEqual, Value: []string{"beta"}}},
		}, "beta", true, false},
		{"or_without_application_constraint", AllConditions{
			{{DimensionName: ApmApplicationName, Operator: ConditionEqual, Value: []string{"alpha"}}},
			{{DimensionName: "service_name", Operator: ConditionEqual, Value: []string{"frontend"}}},
		}, "beta", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := make(AllConditions, len(tc.conditions))
			for i, group := range tc.conditions {
				before[i] = append([]ConditionField(nil), group...)
				for j := range before[i] {
					before[i][j].Value = append([]string(nil), group[j].Value...)
				}
			}
			got, err := tc.conditions.MayMatchLabel(ApmApplicationName, tc.value)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			}
			if tc.conditions != nil {
				assert.Equal(t, before, tc.conditions, "routing must preserve sample conditions")
			}
		})
	}
}

func setupApplicationRouting(t *testing.T, legacy bool) (context.Context, *SpaceFilter) {
	t.Helper()
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)
	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err)
	tables := []struct {
		id        string
		app       string
		storageID int64
		cluster   string
	}{
		{"application.alpha_one", "alpha", 11, "test-cluster-a"},
		{"application.alpha_two", "alpha", 12, "test-cluster-b"},
		{"application.beta", "beta", 12, "test-cluster-a"},
		{"application.gamma", "gamma", 13, "test-cluster-a"},
	}
	space := ir.Space{}
	tableIDs := ir.ResultTableList{}
	for _, table := range tables {
		require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, table.id, &ir.ResultTableDetail{
			TableId: table.id, StorageId: table.storageID, StorageType: metadata.VictoriaMetricsStorageType,
			MeasurementType: redis.BkSplitMeasurement, BcsClusterID: table.cluster,
			Fields: applicationRoutingFields, Labels: map[string]string{ApmApplicationName: table.app, "scene": "apm"},
		}))
		space[table.id] = &ir.SpaceResultTable{TableId: table.id}
		tableIDs = append(tableIDs, table.id)
	}
	if legacy {
		for id, label := range map[string]map[string]string{
			"application.legacy": nil,
			"application.empty":  {ApmApplicationName: ""},
		} {
			require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, id, &ir.ResultTableDetail{
				TableId: id, StorageId: 13, StorageType: metadata.VictoriaMetricsStorageType,
				MeasurementType: redis.BkSplitMeasurement, Fields: []string{applicationRoutingMetric}, Labels: label,
			}))
			space[id] = &ir.SpaceResultTable{TableId: id}
			tableIDs = append(tableIDs, id)
		}
	}
	require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, applicationRoutingSpace, &space))
	require.NoError(t, router.Add(ctx, ir.DataLabelToResultTableKey, applicationRoutingLabel, &tableIDs))
	sf, err := NewSpaceFilter(ctx, &TsDBOption{SpaceUid: applicationRoutingSpace})
	require.NoError(t, err)
	return ctx, sf
}

func applicationTableIDs(tables []*query.TsDBV2) []string {
	ids := make([]string, 0, len(tables))
	for _, table := range tables {
		ids = append(ids, table.TableID)
	}
	return ids
}

func TestSpaceFilter_ApplicationRouting(t *testing.T) {
	_, sf := setupApplicationRouting(t, false)
	alpha := []string{"application.alpha_one", "application.alpha_two"}
	all := []string{"application.alpha_one", "application.alpha_two", "application.beta", "application.gamma"}
	for _, tc := range []struct {
		name       string
		tableID    TableID
		conditions AllConditions
		tableConds AllConditions
		want       []string
	}{
		{"no_application_filter", "", nil, nil, all},
		{"all_space_same_application_multiple_tables", "", applicationCondition(ConditionEqual, "alpha"), nil, alpha},
		{"data_label", applicationRoutingLabel, applicationCondition(ConditionEqual, "alpha"), nil, alpha},
		{"explicit_matching_table", "application.alpha_one", applicationCondition(ConditionEqual, "alpha"), nil, alpha[:1]},
		{"explicit_conflicting_table", "application.beta", applicationCondition(ConditionEqual, "alpha"), nil, nil},
		{"table_label_selector", "", applicationCondition(ConditionEqual, "alpha"), AllConditions{{
			{DimensionName: "scene", Operator: ConditionEqual, Value: []string{"apm"}},
		}}, alpha},
		{"table_selector_still_ignored_for_explicit_route", "application.alpha_one", applicationCondition(ConditionEqual, "alpha"), AllConditions{{
			{DimensionName: "scene", Operator: ConditionEqual, Value: []string{"other"}},
		}}, alpha[:1]},
		{"negative_application", "", applicationCondition(ConditionNotEqual, "alpha"), nil, []string{"application.beta", "application.gamma"}},
		{"multiple_regex_alternatives", "", applicationCondition(ConditionRegEqual, "alpha.*", "beta.*"), nil, append(append([]string{}, alpha...), "application.beta")},
		{"or_without_application_filter", "", AllConditions{
			{{DimensionName: ApmApplicationName, Operator: ConditionEqual, Value: []string{"alpha"}}},
			{{DimensionName: "service_name", Operator: ConditionEqual, Value: []string{"frontend"}}},
		}, nil, all},
		{"bcs_filter_still_applies", "", AllConditions{{
			{DimensionName: ApmApplicationName, Operator: ConditionEqual, Value: []string{"alpha"}},
			{DimensionName: ClusterID, Operator: ConditionEqual, Value: []string{"test-cluster-a"}},
		}}, nil, alpha[:1]},
		{"explicit_route_keeps_bcs_behavior", "application.alpha_two", AllConditions{{
			{DimensionName: ApmApplicationName, Operator: ConditionEqual, Value: []string{"alpha"}},
			{DimensionName: ClusterID, Operator: ConditionEqual, Value: []string{"test-cluster-a"}},
		}}, nil, alpha[1:]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tables, err := sf.DataList(&TsDBOption{
				SpaceUid: applicationRoutingSpace, FieldName: applicationRoutingMetric,
				TableID: tc.tableID, AllConditions: tc.conditions, TableIDConditions: tc.tableConds,
			})
			require.NoError(t, err)
			assert.ElementsMatch(t, tc.want, applicationTableIDs(tables))
		})
	}
}

func TestSpaceFilter_ApplicationRoutingLegacyMetadata(t *testing.T) {
	_, sf := setupApplicationRouting(t, true)
	tables, err := sf.DataList(&TsDBOption{
		SpaceUid: applicationRoutingSpace, FieldName: applicationRoutingMetric,
		AllConditions: applicationCondition(ConditionEqual, "alpha"),
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"application.alpha_one", "application.alpha_two", "application.legacy", "application.empty"}, applicationTableIDs(tables))
}

func TestSpaceFilter_ApplicationRoutingMetricScope(t *testing.T) {
	_, sf := setupApplicationRouting(t, false)
	for _, metric := range applicationRoutingFields {
		t.Run(metric, func(t *testing.T) {
			tables, err := sf.DataList(&TsDBOption{
				SpaceUid: applicationRoutingSpace, FieldName: metric,
				TableID: "application.beta", AllConditions: applicationCondition(ConditionEqual, "alpha"),
			})
			require.NoError(t, err)
			if metric == "apm_custom_metric" {
				require.Len(t, tables, 1, "custom samples may use an application label different from their owning table")
			} else {
				assert.Empty(t, tables)
			}
		})
	}
	t.Run("regex_mixing_relation_and_custom_metrics", func(t *testing.T) {
		tables, err := sf.DataList(&TsDBOption{
			SpaceUid: applicationRoutingSpace, FieldName: "apm_.*", IsRegexp: true,
			TableID: "application.beta", AllConditions: applicationCondition(ConditionEqual, "alpha"),
		})
		require.NoError(t, err)
		require.Len(t, tables, 1)
		assert.Equal(t, []string{"apm_custom_metric"}, tables[0].ExpandMetricNames)
	})
	t.Run("skip_field_check_still_filters_known_relation", func(t *testing.T) {
		tables, err := sf.DataList(&TsDBOption{
			SpaceUid: applicationRoutingSpace, FieldName: applicationRoutingMetric, IsSkipField: true,
			TableID: "application.beta", AllConditions: applicationCondition(ConditionEqual, "alpha"),
		})
		require.NoError(t, err)
		assert.Empty(t, tables)
	})
	t.Run("skip_field_check_retains_unexpanded_metric_regex", func(t *testing.T) {
		tables, err := sf.DataList(&TsDBOption{
			SpaceUid: applicationRoutingSpace, FieldName: applicationRoutingMetric, IsSkipField: true, IsRegexp: true,
			TableID: "application.beta", AllConditions: applicationCondition(ConditionEqual, "alpha"),
		})
		require.NoError(t, err)
		require.Len(t, tables, 1)
	})
}

func TestSpaceFilter_ApplicationRoutingSeparateMetric(t *testing.T) {
	ctx, sf := setupApplicationRouting(t, false)
	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err)
	separateID := "application." + applicationRoutingMetric
	t.Cleanup(func() { require.NoError(t, router.Delete(ctx, ir.ResultTableDetailKey, separateID)) })
	for _, tc := range []struct {
		name       string
		labels     map[string]string
		queryApp   string
		storageIDs []string
	}{
		{"unrelated_application_excluded", map[string]string{ApmApplicationName: "beta"}, "alpha", []string{"11"}},
		{"same_application_kept", map[string]string{ApmApplicationName: "alpha"}, "alpha", []string{"11", "14"}},
		{"unknown_application_kept", nil, "alpha", []string{"11", "14"}},
		{"parent_excluded_separate_application_kept", map[string]string{ApmApplicationName: "beta"}, "beta", []string{"14"}},
		{"parent_excluded_separate_unknown_kept", nil, "beta", []string{"14"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, separateID, &ir.ResultTableDetail{
				TableId: separateID, StorageId: 14, StorageType: metadata.VictoriaMetricsStorageType,
				MeasurementType: redis.BkSplitMeasurement, Fields: []string{applicationRoutingMetric}, Labels: tc.labels,
			}))
			tables, err := sf.DataList(&TsDBOption{
				SpaceUid: applicationRoutingSpace, FieldName: applicationRoutingMetric,
				TableID: "application.alpha_one", AllConditions: applicationCondition(ConditionEqual, tc.queryApp),
			})
			require.NoError(t, err)
			storageIDs := make([]string, 0, len(tables))
			for _, table := range tables {
				storageIDs = append(storageIDs, table.StorageID)
			}
			assert.ElementsMatch(t, tc.storageIDs, storageIDs)
		})
	}
}

func TestQueryToMetric_ApplicationRoutingKeepsSampleFilters(t *testing.T) {
	ctx, _ := setupApplicationRouting(t, false)
	q := &Query{
		FieldName: applicationRoutingMetric, ReferenceName: "a",
		Conditions: Conditions{
			FieldList: []ConditionField{
				{DimensionName: ApmApplicationName, Operator: ConditionEqual, Value: []string{"alpha"}},
				{DimensionName: "service_name", Operator: ConditionEqual, Value: []string{"frontend"}},
			},
			ConditionList: []string{ConditionAnd},
		},
	}
	result, err := q.ToQueryMetric(ctx, applicationRoutingSpace, nil)
	require.NoError(t, err)
	require.Len(t, result.QueryList, 2)
	tableIDs := make([]string, 0, len(result.QueryList))
	for _, query := range result.QueryList {
		tableIDs = append(tableIDs, query.TableID)
		assert.Equal(t, metadata.VictoriaMetricsStorageType, query.StorageType)
		assert.Contains(t, string(query.VmCondition), `apm_application_name="alpha"`)
		assert.Contains(t, string(query.VmCondition), `service_name="frontend"`)
		assert.Contains(t, query.Condition, `apm_application_name='alpha'`)
		require.Len(t, query.AllConditions, 1)
		require.Len(t, query.AllConditions[0], 2)
	}
	assert.ElementsMatch(t, []string{"application.alpha_one", "application.alpha_two"}, tableIDs)
	assert.Equal(t, []string{"alpha"}, q.Conditions.FieldList[0].Value)
}
