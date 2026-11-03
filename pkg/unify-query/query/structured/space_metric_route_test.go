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
	internalQuery "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

func setupDataLabelMetricRoutes(t *testing.T) (context.Context, *TsDBOption) {
	t.Helper()
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)
	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err)

	const spaceUID = "bkcc__metric_route_union"
	for tableID, detail := range map[string]*ir.ResultTableDetail{
		"metric_route.all": {
			StorageId: 2, StorageType: metadata.InfluxDBStorageType,
			DB: "original_db", VmRt: "original_vm_rt", DataLabel: "metric_route_label",
			MeasurementType: redis.BkSplitMeasurement, Fields: []string{"cpu", "memory"},
		},
		"metric_route.cpu": {
			StorageId: 3, StorageType: metadata.InfluxDBStorageType,
			DB: "separate_db", VmRt: "separate_vm_rt", DataLabel: "separate_label",
			MeasurementType: redis.BkSplitMeasurement, Fields: []string{"cpu"},
		},
	} {
		detail.TableId = tableID
		require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, tableID, detail))
	}
	require.NoError(t, router.Add(ctx, ir.DataLabelToResultTableKey, "metric_route_label", &ir.ResultTableList{"metric_route.all"}))
	require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, spaceUID, &ir.Space{
		"metric_route.all": {TableId: "metric_route.all", Filters: []map[string]string{{"bk_biz_id": "2"}}},
	}))
	return ctx, &TsDBOption{SpaceUid: spaceUID, TableID: "metric_route_label", FieldName: "cpu"}
}

func TestSpaceFilter_DataLabelKeepsOriginalMetricRoute(t *testing.T) {
	for _, tc := range []struct {
		name     string
		field    string
		isRegexp bool
		metrics  []string
	}{
		{name: "exact", field: "cpu", metrics: []string{"cpu"}},
		{name: "regexp", field: "cpu|memory", isRegexp: true, metrics: []string{"cpu", "memory"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, opt := setupDataLabelMetricRoutes(t)
			opt.FieldName, opt.IsRegexp = tc.field, tc.isRegexp
			tsDBs, err := GetTsDBList(ctx, opt)
			require.NoError(t, err)
			require.Len(t, tsDBs, 2)

			routes := make(map[string]*query.TsDBV2)
			for _, route := range tsDBs {
				routes[route.DataLabel] = route
				assert.Equal(t, []query.Filter{{"bk_biz_id": "2"}}, route.Filters)
			}
			original := routes["metric_route_label"]
			require.NotNil(t, original, "独立指标 RT 不能排除 data_label 索引到的原 RT")
			assert.Equal(t, tc.metrics, original.ExpandMetricNames)
			assert.Equal(t, "original_db", original.DB)
			assert.Equal(t, "2", original.StorageID)
			separate := routes["separate_label"]
			require.NotNil(t, separate)
			assert.Equal(t, []string{"cpu"}, separate.ExpandMetricNames)
			assert.Equal(t, "separate_db", separate.DB)
			assert.Equal(t, "3", separate.StorageID)
		})
	}
}

func TestSpaceFilter_MetricRouteDoesNotDuplicateItself(t *testing.T) {
	ctx, opt := setupDataLabelMetricRoutes(t)
	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err)
	require.NoError(t, router.Add(ctx, ir.DataLabelToResultTableKey, string(opt.TableID), &ir.ResultTableList{"metric_route.cpu"}))
	require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, opt.SpaceUid, &ir.Space{
		"metric_route.cpu": {TableId: "metric_route.cpu"},
	}))
	tsDBs, err := GetTsDBList(ctx, opt)
	require.NoError(t, err)
	require.Len(t, tsDBs, 1)
	assert.Equal(t, "separate_db", tsDBs[0].DB)
	assert.Equal(t, []string{"cpu"}, tsDBs[0].ExpandMetricNames)
}

func TestSpaceFilter_MetricRoutesKeepSeparateStorageHistory(t *testing.T) {
	ctx, opt := setupDataLabelMetricRoutes(t)
	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err)
	detail := *router.GetResultTable(ctx, "metric_route.all", false)
	detail.StorageClusterRecords = []ir.Record{{StorageID: 2, EnableTime: 100}}
	require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, detail.TableId, &detail))

	tsDBs, err := GetTsDBList(ctx, opt)
	require.NoError(t, err)
	require.Len(t, tsDBs, 2)
	for _, route := range tsDBs {
		if route.DataLabel == "metric_route_label" {
			require.Len(t, route.StorageClusterRecords, 1)
			assert.Equal(t, "2", route.StorageClusterRecords[0].StorageID)
		} else {
			assert.Empty(t, route.StorageClusterRecords, "独立 RT 不能继承原 RT 的存储迁移记录")
		}
	}
}

func TestQueryToMetric_DataLabelIncludesOriginalAndSeparateRoutes(t *testing.T) {
	ctx, opt := setupDataLabelMetricRoutes(t)
	metric, err := (&Query{
		TableID: opt.TableID, FieldName: opt.FieldName, ReferenceName: "a",
	}).ToQueryMetric(ctx, opt.SpaceUid, nil)
	require.NoError(t, err)
	require.Len(t, metric.QueryList, 2)

	routes := make(map[string]string)
	for _, route := range metric.QueryList {
		routes[route.DB] = route.StorageID
		assert.Equal(t, "cpu", route.Measurement)
		assert.Equal(t, "value", route.Field)
		assert.Contains(t, route.Condition, "bk_biz_id")
	}
	assert.Equal(t, map[string]string{"original_db": "2", "separate_db": "3"}, routes)
	expand := internalQuery.ToVmExpand(ctx, metadata.QueryReference{"a": {metric}})
	require.NotNil(t, expand)
	assert.ElementsMatch(t, []string{"original_vm_rt", "separate_vm_rt"}, expand.ResultTableList)
	assert.Contains(t, expand.MetricFilterCondition["a"], `result_table_id="original_vm_rt"`)
	assert.Contains(t, expand.MetricFilterCondition["a"], `result_table_id="separate_vm_rt"`)
}
