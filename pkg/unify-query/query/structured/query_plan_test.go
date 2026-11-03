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
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	uqQuery "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
)

const rawPlanStart int64 = 1790000000

func rawPlanFixture() (context.Context, *QueryTs, *uqQuery.TsDBV2) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	table := &uqQuery.TsDBV2{
		TableID:     "trace.result_table",
		DataLabel:   "trace-label",
		StorageID:   "16",
		StorageType: metadata.ElasticsearchStorageType,
		StorageName: "trace-storage",
		ClusterName: "trace-cluster",
		DB:          "trace-index",
		Measurement: "__default__",
		SourceType:  BkLog,
		TimeField:   metadata.TimeField{Name: "end_time", Type: "long", Unit: "microsecond"},
	}
	table.StorageClusterRecords = rawPlanRecords(table, "16", "40", "16")
	query := &QueryTs{
		SpaceUid:     "bkcc__2",
		Start:        strconv.FormatInt(rawPlanStart, 10),
		End:          strconv.FormatInt(rawPlanStart+7*24*60*60, 10),
		Step:         "1m",
		Timezone:     "UTC",
		NotTimeAlign: true,
		QueryList: []*Query{{
			DataSource:    BkLog,
			TableID:       TableID(table.TableID),
			ReferenceName: "A",
			Limit:         20,
		}},
		TsDBMap: map[string]TsDBs{"A": {table}},
	}
	return ctx, query, table
}

func rawPlanRecords(table *uqQuery.TsDBV2, storageIDs ...string) uqQuery.StorageClusterRecords {
	records := make(uqQuery.StorageClusterRecords, 0, len(storageIDs))
	for index, storageID := range storageIDs {
		enabledAt := rawPlanStart + int64(index)*24*60*60
		if index == 0 {
			enabledAt -= 24 * 60 * 60
		}
		records = append(records, uqQuery.Record{
			StorageID:   storageID,
			StorageType: table.StorageType,
			StorageName: table.StorageName,
			ClusterName: table.ClusterName,
			DB:          table.DB,
			Measurement: table.Measurement,
			SourceType:  table.SourceType,
			EnableTime:  enabledAt,
		})
	}
	return records
}

func TestRawQueryPlanRepeatedHistoricalTargets(t *testing.T) {
	for _, tc := range []struct {
		name         string
		storageIDs   []string
		wantStorages map[string]int
		wantRoutes   int
	}{
		{name: "without history", wantStorages: map[string]int{"16": 1}, wantRoutes: 1},
		{name: "one switch", storageIDs: []string{"16", "40"}, wantStorages: map[string]int{"16": 1, "40": 1}, wantRoutes: 2},
		{name: "rollback", storageIDs: []string{"16", "40", "16"}, wantStorages: map[string]int{"16": 1, "40": 1}, wantRoutes: 3},
		{name: "multiple revisits", storageIDs: []string{"16", "40", "16", "99", "16", "40"}, wantStorages: map[string]int{"16": 1, "40": 1, "99": 1}, wantRoutes: 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, query, table := rawPlanFixture()
			table.StorageClusterRecords = rawPlanRecords(table, tc.storageIDs...)
			plan, err := query.ToRawQueryPlan(ctx)
			require.NoError(t, err)
			assert.Equal(t, tc.wantRoutes, plan.Routes.Count())
			storages := make(map[string]int)
			plan.Queries.Range("", func(executable *metadata.Query) {
				storages[executable.StorageID]++
				assert.True(t, executable.RouteStart.IsZero())
				assert.True(t, executable.RouteEnd.IsZero())
				assert.False(t, executable.RouteEndInclusive)
				assert.True(t, executable.RouteQueryStart.IsZero())
				assert.True(t, executable.RouteQueryEnd.IsZero())
			})
			assert.Equal(t, tc.wantStorages, storages)
			assert.Equal(t, plan.Queries, metadata.GetQueryReference(ctx))
		})
	}
}

func TestRawQueryPlanPreservesRouteWindowsAndTimeSeries(t *testing.T) {
	ctx, query, _ := rawPlanFixture()
	plan, err := query.ToRawQueryPlan(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, plan.Queries.Count())
	routes := plan.Routes["A"][0].QueryList
	require.Len(t, routes, 3)
	start := time.Unix(rawPlanStart, 0)
	end := start.Add(7 * 24 * time.Hour)
	assert.Equal(t, []string{"16", "40", "16"}, []string{routes[0].StorageID, routes[1].StorageID, routes[2].StorageID})
	assert.True(t, routes[0].RouteStart.Equal(start.Add(2*24*time.Hour)))
	assert.True(t, routes[0].RouteEnd.Equal(end))
	assert.True(t, routes[0].RouteEndInclusive)
	assert.True(t, routes[0].RouteQueryEnd.Equal(end.Add(time.Hour)))
	assert.True(t, routes[1].RouteStart.Equal(start.Add(24*time.Hour)))
	assert.True(t, routes[1].RouteEnd.Equal(start.Add(2*24*time.Hour)))
	assert.False(t, routes[1].RouteEndInclusive)
	assert.True(t, routes[2].RouteStart.Equal(start))
	assert.True(t, routes[2].RouteEnd.Equal(start.Add(24*time.Hour)))
	assert.True(t, routes[2].RouteQueryStart.Equal(start.Add(-time.Hour)))
	assert.Len(t, plan.Routes.CollectRouteInfo(), 3)

	// 时序查询仍逐段执行，不能复用 Raw 的全局时间目标。
	seriesReference, err := query.ToQueryReference(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, seriesReference.Count())
	assert.Equal(t, plan.Routes, seriesReference)
}

func TestRawQueryPlanDistinguishesEffectiveStorageTargets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*uqQuery.Record)
	}{
		{name: "storage ID", change: func(record *uqQuery.Record) { record.StorageID = "99" }},
		{name: "storage type", change: func(record *uqQuery.Record) { record.StorageType = metadata.BkSqlStorageType }},
		{name: "storage name", change: func(record *uqQuery.Record) { record.StorageName = "other-storage" }},
		{name: "cluster name", change: func(record *uqQuery.Record) { record.ClusterName = "other-cluster" }},
		{name: "database", change: func(record *uqQuery.Record) { record.DB = "other-index" }},
		{name: "measurement", change: func(record *uqQuery.Record) { record.Measurement = "other-measurement" }},
		{name: "source type", change: func(record *uqQuery.Record) { record.SourceType = BkData }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, query, table := rawPlanFixture()
			tc.change(&table.StorageClusterRecords[2])
			plan, err := query.ToRawQueryPlan(ctx)
			require.NoError(t, err)
			assert.Equal(t, 3, plan.Queries.Count())
			assert.Equal(t, 3, plan.Routes.Count())
		})
	}
}

func TestRawQueryPlanUsesResolvedTargetDefaults(t *testing.T) {
	ctx, query, table := rawPlanFixture()
	// 旧格式从 RT 补齐定位字段，应与显式填写相同字段的新格式合并。
	first := table.StorageClusterRecords[0]
	table.StorageClusterRecords[0] = uqQuery.Record{StorageID: first.StorageID, EnableTime: first.EnableTime}
	plan, err := query.ToRawQueryPlan(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, plan.Queries.Count())
	assert.Equal(t, 3, plan.Routes.Count())
}

func TestRawQueryPlanKeepsLogicalQueryIdentity(t *testing.T) {
	ctx, query, _ := rawPlanFixture()
	first := query.QueryList[0]
	first.Conditions = Conditions{FieldList: []ConditionField{{DimensionName: "span_id", Operator: ConditionEqual, Value: []string{"first-span"}}}}
	second := *first
	second.Conditions = Conditions{FieldList: []ConditionField{{DimensionName: "span_id", Operator: ConditionEqual, Value: []string{"second-span"}}}}
	second.KeepColumns = []string{"span_id"}
	second.ExactTotal = true
	query.QueryList = append(query.QueryList, &second)
	plan, err := query.ToRawQueryPlan(ctx)
	require.NoError(t, err)
	require.Len(t, plan.Queries["A"], 2)
	assert.Equal(t, 4, plan.Queries.Count())
	assert.Equal(t, 6, plan.Routes.Count())
	for _, executable := range plan.Queries["A"][0].QueryList {
		assert.Equal(t, []string{"first-span"}, executable.AllConditions[0][0].Value)
		assert.False(t, executable.ExactTotal)
	}
	for _, executable := range plan.Queries["A"][1].QueryList {
		assert.Equal(t, []string{"second-span"}, executable.AllConditions[0][0].Value)
		assert.Equal(t, []string{"span_id"}, executable.Source)
		assert.True(t, executable.ExactTotal)
	}
}

func TestRawQueryPlanKeepsResultTableIdentityAndFilters(t *testing.T) {
	ctx, query, first := rawPlanFixture()
	first.Filters = []uqQuery.Filter{{"tenant": "first"}}
	second := *first
	second.TableID = "trace.other_result_table"
	second.DataLabel = "other-label"
	second.Filters = []uqQuery.Filter{{"tenant": "second"}}
	query.TsDBMap["A"] = append(query.TsDBMap["A"], &second)
	plan, err := query.ToRawQueryPlan(ctx)
	require.NoError(t, err)
	assert.Equal(t, 4, plan.Queries.Count())
	assert.Equal(t, 6, plan.Routes.Count())
	tables := make(map[string]int)
	plan.Queries.Range("", func(executable *metadata.Query) {
		tables[executable.TableID]++
		if executable.TableID == first.TableID {
			assert.Equal(t, first.DataLabel, executable.DataLabel)
			assert.Equal(t, []string{"first"}, executable.AllConditions[0][0].Value)
		} else {
			assert.Equal(t, second.DataLabel, executable.DataLabel)
			assert.Equal(t, []string{"second"}, executable.AllConditions[0][0].Value)
		}
	})
	assert.Equal(t, map[string]int{first.TableID: 2, second.TableID: 2}, tables)
}

func TestRawQueryPlanKeepsPaginationAndScrollTarget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		option metadata.ResultTableOption
		scroll string
	}{
		{name: "search after", option: metadata.ResultTableOption{SearchAfter: []any{42, "span-id"}}},
		{name: "offset fallback", option: metadata.ResultTableOption{From: rawPlanOffset(40)}},
		{name: "scroll", option: metadata.ResultTableOption{ScrollID: "scroll-id"}, scroll: "1m"},
		{name: "slice", option: metadata.ResultTableOption{SliceIndex: 1, SliceMax: 4}, scroll: "1m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, query, table := rawPlanFixture()
			table.StorageClusterRecords = rawPlanRecords(table, "16", "16", "16")
			query.IsSearchAfter = tc.scroll == ""
			query.Scroll = tc.scroll
			query.ResultTableOptions = metadata.ResultTableOptions{table.TableID + "|16": &tc.option}
			plan, err := query.ToRawQueryPlan(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, plan.Queries.Count())
			assert.Equal(t, 3, plan.Routes.Count())
			executable := plan.Queries["A"][0].QueryList[0]
			assert.Equal(t, &tc.option, executable.ResultTableOption)
			assert.Equal(t, tc.scroll, executable.Scroll)
			assert.Equal(t, table.TableID+"|16", executable.TableUUID())
		})
	}
}

func rawPlanOffset(offset int) *int {
	return &offset
}

func TestRawQueryPlanStableWithMergedDatabaseMap(t *testing.T) {
	for _, tc := range []struct {
		name    string
		filters []uqQuery.Filter
	}{
		{name: "without filters"},
		{name: "multiple filter keys", filters: []uqQuery.Filter{{"tenant": "one", "service": "checkout", "namespace": "production"}}},
		{name: "compressed filters", filters: []uqQuery.Filter{{"tenant": "two", "service": "checkout"}, {"tenant": "one", "service": "storage"}, {"tenant": "one", "service": "checkout"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, query, table := rawPlanFixture()
			query.IsMergeDB = true
			table.StorageType = metadata.BkSqlStorageType
			table.Measurement = metadata.DorisStorageType
			table.Filters = tc.filters
			table.StorageClusterRecords = rawPlanRecords(table, "16", "40", "99", "16", "40", "99")
			want, err := query.ToRawQueryPlan(ctx)
			require.NoError(t, err)
			require.Equal(t, 3, want.Queries.Count())
			require.Equal(t, 6, want.Routes.Count())
			for range 50 {
				got, err := query.ToRawQueryPlan(ctx)
				require.NoError(t, err)
				require.Equal(t, want, got)
			}
		})
	}
}

func TestRawQueryPlanKeepsDorisClusterWhenMergingDatabases(t *testing.T) {
	ctx, query, table := rawPlanFixture()
	query.IsMergeDB = true
	table.StorageType = metadata.BkSqlStorageType
	table.Measurement = metadata.DorisStorageType
	table.StorageClusterRecords = rawPlanRecords(table, "16", "16", "16")
	table.StorageClusterRecords[1].ClusterName = "other-cluster"
	plan, err := query.ToRawQueryPlan(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, plan.Routes.Count())
	require.Equal(t, 2, plan.Queries.Count())
	clusters := make(map[string]int)
	plan.Queries.Range("", func(executable *metadata.Query) {
		clusters[executable.ClusterName]++
		assert.Equal(t, []string{table.DB}, executable.DBs)
	})
	assert.Equal(t, map[string]int{table.ClusterName: 1, "other-cluster": 1}, clusters)
}

func TestRawQueryPlanMergesDistinctDorisDatabasesOnce(t *testing.T) {
	ctx, query, table := rawPlanFixture()
	query.IsMergeDB = true
	table.StorageType = metadata.BkSqlStorageType
	table.Measurement = metadata.DorisStorageType
	table.StorageClusterRecords = rawPlanRecords(table, "16", "16", "16")
	table.StorageClusterRecords[1].DB = "other-database"
	plan, err := query.ToRawQueryPlan(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, plan.Routes.Count())
	require.Equal(t, 1, plan.Queries.Count())
	assert.Equal(t, []string{"other-database", table.DB}, plan.Queries["A"][0].QueryList[0].DBs)
}

func TestRawQueryPlanKeepsReferenceIdentity(t *testing.T) {
	ctx, query, table := rawPlanFixture()
	second := *query.QueryList[0]
	second.ReferenceName = "B"
	query.QueryList = append(query.QueryList, &second)
	query.TsDBMap["B"] = TsDBs{table}
	for range 20 {
		plan, err := query.ToRawQueryPlan(ctx)
		require.NoError(t, err)
		assert.Equal(t, 4, plan.Queries.Count())
		assert.Equal(t, 6, plan.Routes.Count())
		for _, referenceName := range []string{"A", "B"} {
			require.Len(t, plan.Queries[referenceName], 1)
			assert.Equal(t, referenceName, plan.Queries[referenceName][0].ReferenceName)
			assert.Len(t, plan.Queries[referenceName][0].QueryList, 2)
		}
	}
}
