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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

const routeCandidatesOtherSpace = "test__application_routing_other"

func routeCandidatesFilter(t *testing.T, ctx context.Context, space string) *SpaceFilter {
	t.Helper()
	sf, err := NewSpaceFilter(ctx, &TsDBOption{SpaceUid: space})
	require.NoError(t, err)
	return sf
}

func routeCandidatesCounts(t *testing.T, reuse *RouteCandidates, scans, hits int) {
	t.Helper()
	gotScans, gotHits := reuse.Counts()
	assert.Equal(t, scans, gotScans, "scans")
	assert.Equal(t, hits, gotHits, "hits")
}

// The whole-space cases of TestSpaceFilter_ApplicationRouting, resolved in one request: the field match is read
// once and every query's application and cluster conditions still decide its tables, as each did alone.
func TestRouteCandidatesKeepEachQuerysConditions(t *testing.T) {
	ctx, alone := setupApplicationRouting(t, false)
	ctx, reuse := WithRouteCandidates(ctx)
	sf := routeCandidatesFilter(t, ctx, applicationRoutingSpace)
	alpha := []string{"application.alpha_one", "application.alpha_two"}
	all := []string{"application.alpha_one", "application.alpha_two", "application.beta", "application.gamma"}
	cases := []struct {
		name       string
		conditions AllConditions
		want       []string
	}{
		{"no_application_filter", nil, all},
		{"all_space_same_application_multiple_tables", applicationCondition(ConditionEqual, "alpha"), alpha},
		{"negative_application", applicationCondition(ConditionNotEqual, "alpha"), []string{"application.beta", "application.gamma"}},
		{"multiple_regex_alternatives", applicationCondition(ConditionRegEqual, "alpha.*", "beta.*"), append(append([]string{}, alpha...), "application.beta")},
		{"or_without_application_filter", AllConditions{
			{{DimensionName: ApmApplicationName, Operator: ConditionEqual, Value: []string{"alpha"}}},
			{{DimensionName: "service_name", Operator: ConditionEqual, Value: []string{"frontend"}}},
		}, all},
		{"bcs_filter_still_applies", AllConditions{{
			{DimensionName: ApmApplicationName, Operator: ConditionEqual, Value: []string{"alpha"}},
			{DimensionName: ClusterID, Operator: ConditionEqual, Value: []string{"test-cluster-a"}},
		}}, alpha[:1]},
	}
	for i, tc := range cases {
		option := func() *TsDBOption {
			return &TsDBOption{SpaceUid: applicationRoutingSpace, FieldName: applicationRoutingMetric, AllConditions: tc.conditions}
		}
		tables, err := sf.DataList(option())
		require.NoError(t, err, tc.name)
		assert.ElementsMatch(t, tc.want, applicationTableIDs(tables), tc.name)
		// The same query without reuse returns the same tables, all fields included.
		baseline, err := alone.DataList(option())
		require.NoError(t, err, tc.name)
		assert.ElementsMatch(t, baseline, tables, tc.name)
		routeCandidatesCounts(t, reuse, 1, i)
	}
}

// Only the whole-space scan by field is reused: an explicit table or data label, a skipped field (every table is a
// candidate) and table label conditions (their failure message reads the whole space) take the path they always took.
func TestRouteCandidatesLeaveOtherRoutesAlone(t *testing.T) {
	ctx, alone := setupApplicationRouting(t, false)
	ctx, reuse := WithRouteCandidates(ctx)
	sf := routeCandidatesFilter(t, ctx, applicationRoutingSpace)
	alpha := applicationCondition(ConditionEqual, "alpha")
	for _, option := range []TsDBOption{
		{FieldName: applicationRoutingMetric, TableID: applicationRoutingLabel, AllConditions: alpha},
		{FieldName: applicationRoutingMetric, TableID: "application.alpha_one", AllConditions: alpha},
		{FieldName: applicationRoutingMetric, AllConditions: alpha, IsSkipField: true},
		{FieldName: applicationRoutingMetric, AllConditions: alpha, TableIDConditions: AllConditions{{
			{DimensionName: "scene", Operator: ConditionEqual, Value: []string{"apm"}},
		}}},
		{FieldName: applicationRoutingMetric, AllConditions: alpha, TableIDConditions: AllConditions{{
			{DimensionName: "scene", Operator: ConditionEqual, Value: []string{"other"}},
		}}},
	} {
		option.SpaceUid = applicationRoutingSpace
		first := option
		tables, err := sf.DataList(&first)
		require.NoError(t, err)
		second := option
		baseline, err := alone.DataList(&second)
		require.NoError(t, err)
		assert.ElementsMatch(t, baseline, tables)
	}
	routeCandidatesCounts(t, reuse, 0, 0)
}

// The key is the space, the field and the switches that change which tables hold it (regexp, skipped space): each
// differs from the others, and a repeat of each finds its own candidates.
func TestRouteCandidatesKeyedBySpaceFieldAndSwitches(t *testing.T) {
	ctx, alone := setupApplicationRouting(t, false)
	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err)
	// The other space shares application.beta and holds a table of its own, with a field no other table has.
	const ownTable = "application.other_only"
	require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, ownTable, &ir.ResultTableDetail{
		TableId: ownTable, StorageId: 15, StorageType: metadata.VictoriaMetricsStorageType,
		MeasurementType: redis.BkSplitMeasurement, BcsClusterID: "test-cluster-a",
		Fields: []string{applicationRoutingMetric, "apm_other_only_metric"}, Labels: map[string]string{ApmApplicationName: "other", "scene": "apm"},
	}))
	other := ir.Space{
		"application.beta": &ir.SpaceResultTable{TableId: "application.beta"},
		ownTable:           &ir.SpaceResultTable{TableId: ownTable},
	}
	require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, routeCandidatesOtherSpace, &other))
	t.Cleanup(func() {
		require.NoError(t, router.Delete(ctx, ir.SpaceToResultTableKey, routeCandidatesOtherSpace))
		require.NoError(t, router.Delete(ctx, ir.ResultTableDetailKey, ownTable))
	})
	ctx, reuse := WithRouteCandidates(ctx)
	all := []string{"application.alpha_one", "application.alpha_two", "application.beta", "application.gamma"}
	cases := []struct {
		name   string
		space  string
		option TsDBOption
		want   []string
	}{
		{"field", applicationRoutingSpace, TsDBOption{FieldName: applicationRoutingMetric}, all},
		{"another_field", applicationRoutingSpace, TsDBOption{FieldName: "apm_custom_metric"}, all},
		{"regexp", applicationRoutingSpace, TsDBOption{FieldName: "apm_service_instance_with_.+", IsRegexp: true}, all},
		// The same string read literally names no field.
		{"literal_of_the_regexp", applicationRoutingSpace, TsDBOption{FieldName: "apm_service_instance_with_.+"}, nil},
		{"skip_space", applicationRoutingSpace, TsDBOption{FieldName: applicationRoutingMetric, IsSkipSpace: true}, all},
		{"another_space", routeCandidatesOtherSpace, TsDBOption{FieldName: applicationRoutingMetric}, []string{"application.beta", ownTable}},
		{"field_of_one_table", routeCandidatesOtherSpace, TsDBOption{FieldName: "apm_other_only_metric"}, []string{ownTable}},
	}
	for round := 0; round < 2; round++ {
		for i, tc := range cases {
			option := tc.option
			option.SpaceUid = tc.space
			tables, err := routeCandidatesFilter(t, ctx, tc.space).DataList(&option)
			require.NoError(t, err, tc.name)
			assert.ElementsMatch(t, tc.want, applicationTableIDs(tables), tc.name)
			baselineOption := tc.option
			baselineOption.SpaceUid = tc.space
			sf := alone
			if tc.space != applicationRoutingSpace {
				sf = routeCandidatesFilter(t, metadata.InitHashID(context.Background()), tc.space)
			}
			baseline, err := sf.DataList(&baselineOption)
			require.NoError(t, err, tc.name)
			assert.ElementsMatch(t, baseline, tables, tc.name)
			if round == 0 {
				routeCandidatesCounts(t, reuse, i+1, 0)
			} else {
				routeCandidatesCounts(t, reuse, len(cases), i+1)
			}
		}
	}
}

// Reuse lasts for the request that installed it: a table added to the space mid-request is seen from the next
// request on, and an installed reuse is kept by a nested install.
func TestRouteCandidatesLastForTheirRequest(t *testing.T) {
	ctx, _ := setupApplicationRouting(t, false)
	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err)
	request, reuse := WithRouteCandidates(ctx)
	nested, again := WithRouteCandidates(request)
	require.Same(t, reuse, again)
	option := func() *TsDBOption {
		return &TsDBOption{SpaceUid: applicationRoutingSpace, FieldName: applicationRoutingMetric}
	}
	before, err := routeCandidatesFilter(t, nested, applicationRoutingSpace).DataList(option())
	require.NoError(t, err)
	require.Len(t, before, 4)

	const added = "application.delta"
	require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, added, &ir.ResultTableDetail{
		TableId: added, StorageId: 14, StorageType: metadata.VictoriaMetricsStorageType,
		MeasurementType: redis.BkSplitMeasurement, BcsClusterID: "test-cluster-a",
		Fields: applicationRoutingFields, Labels: map[string]string{ApmApplicationName: "delta", "scene": "apm"},
	}))
	space := router.GetSpace(ctx, applicationRoutingSpace)
	grown := ir.Space{added: &ir.SpaceResultTable{TableId: added}}
	for id, table := range space {
		grown[id] = table
	}
	require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, applicationRoutingSpace, &grown))
	t.Cleanup(func() {
		require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, applicationRoutingSpace, &space))
		require.NoError(t, router.Delete(ctx, ir.ResultTableDetailKey, added))
	})

	same, err := routeCandidatesFilter(t, request, applicationRoutingSpace).DataList(option())
	require.NoError(t, err)
	assert.ElementsMatch(t, applicationTableIDs(before), applicationTableIDs(same))
	next, _ := WithRouteCandidates(ctx)
	after, err := routeCandidatesFilter(t, next, applicationRoutingSpace).DataList(option())
	require.NoError(t, err)
	assert.Contains(t, applicationTableIDs(after), added)
	assert.Len(t, after, 5)
}
