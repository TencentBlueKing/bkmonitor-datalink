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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	uqQuery "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
)

func directTables(name string) map[string]metadata.PhysicalTable {
	return map[string]metadata.PhysicalTable{"main": {Database: "logs", Table: name, AnalyzedFields: []string{"log"}}}
}

func TestBKBasePaginationCompatibility(t *testing.T) {
	for _, mode := range []string{"merged", "merged_enable", "merged_disable", "legacy_merged_enable", "legacy_search_after", "legacy_offset"} {
		t.Run(mode, func(t *testing.T) {
			ctx, req, table := rawPlanFixture()
			enabled := mode == "merged_disable"
			ctx = metadata.WithQueryFlags(ctx, metadata.QueryFlags{DorisDirectQuery: &enabled})
			table.StorageType = metadata.BkSqlStorageType
			table.Measurement = metadata.DorisStorageType
			table.TableID, table.DB = "a", "1001_a"
			table.PhysicalTables = directTables("a")
			table.StorageClusterRecords = nil
			req.IsSearchAfter = true
			option := &metadata.ResultTableOption{SearchAfter: []any{42}}
			if mode == "merged" || mode == "merged_enable" || mode == "merged_disable" || mode == "legacy_merged_enable" {
				second := *table
				second.TableID, second.DB = "b", "1001_b"
				second.PhysicalTables = directTables("b")
				req.TsDBMap["A"] = TsDBs{&second, table}
				req.IsMergeDB = true
				if mode != "legacy_merged_enable" {
					option.QueryBackend = metadata.BkSqlStorageType
				}
			} else if mode == "legacy_offset" {
				from := 20
				option = &metadata.ResultTableOption{From: &from}
			}
			plan, err := req.ToRawQueryPlan(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, plan.Queries.Count())
			req.ResultTableOptions = metadata.ResultTableOptions{}
			plan.Queries.Range("", func(q *metadata.Query) {
				if mode == "merged_disable" {
					option.QueryBackend = metadata.DorisStorageType
					option.DirectTables = q.DirectTables
				}
				req.ResultTableOptions[q.TableUUID()] = option
			})
			enabled = mode != "merged" && mode != "merged_disable"
			ctx = metadata.WithQueryFlags(ctx, metadata.QueryFlags{DorisDirectQuery: &enabled})
			plan, err = req.ToRawQueryPlan(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, plan.Queries.Count())
			plan.Routes.Range("", func(q *metadata.Query) {
				require.Equal(t, option, q.ResultTableOption)
			})
			plan.Queries.Range("", func(q *metadata.Query) {
				if mode == "merged_disable" {
					require.Equal(t, metadata.DorisStorageType, q.QueryBackend)
					require.Len(t, q.DirectTables, 2)
				} else {
					require.Equal(t, metadata.BkSqlStorageType, q.QueryBackend)
					require.Empty(t, q.DirectTables)
				}
				require.Equal(t, option, q.ResultTableOption)
				if mode == "merged" || mode == "merged_enable" || mode == "merged_disable" || mode == "legacy_merged_enable" {
					require.Equal(t, []string{"1001_a", "1001_b"}, q.DBs)
				}
			})
		})
	}
}

func TestDorisPhysicalRouteSelection(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		ctx, req, table := rawPlanFixture()
		ctx = metadata.WithQueryFlags(ctx, metadata.QueryFlags{DorisDirectQuery: &enabled})
		table.StorageType = metadata.BkSqlStorageType
		table.Measurement = metadata.DorisStorageType
		table.DB = "1001_logical"
		table.PhysicalTables = directTables("current")
		table.StorageClusterRecords = nil
		plan, err := req.ToRawQueryPlan(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, plan.Queries.Count())
		plan.Queries.Range("", func(q *metadata.Query) {
			require.Equal(t, "1001_logical", q.DB)
			if enabled {
				require.Equal(t, "doris", q.QueryBackend)
				require.Equal(t, "current", q.DirectTables[0].Table)
			} else {
				require.Empty(t, q.QueryBackend)
			}
		})
	}
}

func TestDorisHistoricalPhysicalTargetsRemainDistinct(t *testing.T) {
	ctx, req, table := rawPlanFixture()
	enabled := true
	ctx = metadata.WithQueryFlags(ctx, metadata.QueryFlags{DorisDirectQuery: &enabled})
	table.StorageType = metadata.BkSqlStorageType
	table.Measurement = metadata.DorisStorageType
	table.DB = "1001_logical"
	table.PhysicalTables = directTables("must_not_inherit")
	table.StorageClusterRecords = rawPlanRecords(table, "16", "16")
	table.StorageClusterRecords[0].PhysicalTables = directTables("old")
	table.StorageClusterRecords[1].PhysicalTables = directTables("new")
	plan, err := req.ToRawQueryPlan(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, plan.Queries.Count())
	found := map[string]bool{}
	plan.Queries.Range("", func(q *metadata.Query) { found[q.DirectTables[0].Table] = true })
	require.Equal(t, map[string]bool{"old": true, "new": true}, found)
	req.IsMergeDB = true
	plan, err = req.ToRawQueryPlan(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, plan.Queries.Count())
	plan.Queries.Range("", func(q *metadata.Query) { require.Len(t, q.DirectTables, 2) })
	// 历史记录没有物理配置时只查其 BKBase ID，不能继承当前表。
	req.IsMergeDB = false
	table.StorageClusterRecords[0].PhysicalTables = nil
	plan, err = req.ToRawQueryPlan(ctx)
	require.NoError(t, err)
	backends := map[string]int{}
	plan.Queries.Range("", func(q *metadata.Query) { backends[q.QueryBackend]++ })
	require.Equal(t, map[string]int{"": 1, "doris": 1}, backends)
}

func TestDorisPhysicalOnlyHistoricalRoute(t *testing.T) {
	ctx, req, table := rawPlanFixture()
	enabled := false
	ctx = metadata.WithQueryFlags(ctx, metadata.QueryFlags{DorisDirectQuery: &enabled})
	table.StorageType = metadata.BkSqlStorageType
	table.Measurement = metadata.DorisStorageType
	table.StorageClusterRecords = uqQuery.StorageClusterRecords{{StorageID: "16", StorageType: "bk_sql", ClusterName: "doris", Measurement: "doris", PhysicalTables: directTables("physical_only"), EnableTime: rawPlanStart - 1}}
	plan, err := req.ToRawQueryPlan(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, plan.Queries.Count())
	plan.Queries.Range("", func(q *metadata.Query) { require.Empty(t, q.DB); require.Equal(t, "doris", q.QueryBackend) })
}

func TestDorisPhysicalOnlyUnionPlan(t *testing.T) {
	ctx, req, table := rawPlanFixture()
	enabled := true
	ctx = metadata.WithQueryFlags(ctx, metadata.QueryFlags{DorisDirectQuery: &enabled})
	table.StorageType = "bk_sql"
	table.Measurement = "doris"
	table.DB = ""
	table.StorageClusterRecords = rawPlanRecords(table, "16", "16")
	table.StorageClusterRecords[0].PhysicalTables = directTables("old")
	table.StorageClusterRecords[1].PhysicalTables = directTables("new")
	req.IsMergeDB = true
	plan, err := req.ToRawQueryPlan(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, plan.Queries.Count())
	plan.Queries.Range("", func(q *metadata.Query) { require.Len(t, q.DirectTables, 2); require.Equal(t, "doris", q.QueryBackend) })
}

func TestDorisPaginationKeepsSelectedBackend(t *testing.T) {
	ctx, req, table := rawPlanFixture()
	enabled := true
	ctx = metadata.WithQueryFlags(ctx, metadata.QueryFlags{DorisDirectQuery: &enabled})
	table.StorageType = metadata.BkSqlStorageType
	table.Measurement = "doris"
	table.DB = "1001_rt"
	table.PhysicalTables = directTables("main")
	table.StorageClusterRecords = nil
	plan, err := req.ToRawQueryPlan(ctx)
	require.NoError(t, err)
	options := metadata.ResultTableOptions{}
	plan.Queries.Range("", func(q *metadata.Query) {
		options[q.TableUUID()] = &metadata.ResultTableOption{QueryBackend: "doris", SearchAfter: []any{1}}
	})
	req.ResultTableOptions = options
	enabled = false
	ctx = metadata.WithQueryFlags(ctx, metadata.QueryFlags{DorisDirectQuery: &enabled})
	plan, err = req.ToRawQueryPlan(ctx)
	require.NoError(t, err)
	plan.Queries.Range("", func(q *metadata.Query) {
		require.Equal(t, "doris", q.QueryBackend)
		require.Equal(t, []any{1}, q.ResultTableOption.SearchAfter)
	})
}

func TestMergedDorisPaginationKeepsAllPhysicalTargets(t *testing.T) {
	ctx, req, table := rawPlanFixture()
	enabled := true
	ctx = metadata.WithQueryFlags(ctx, metadata.QueryFlags{DorisDirectQuery: &enabled})
	table.StorageType = "bk_sql"
	table.Measurement = "doris"
	table.DB = "1001_rt"
	table.StorageClusterRecords = rawPlanRecords(table, "16", "16")
	table.StorageClusterRecords[0].PhysicalTables = directTables("old")
	table.StorageClusterRecords[1].PhysicalTables = directTables("new")
	req.IsMergeDB = true
	plan, err := req.ToRawQueryPlan(ctx)
	require.NoError(t, err)
	options := metadata.ResultTableOptions{}
	plan.Queries.Range("", func(q *metadata.Query) {
		options[q.TableUUID()] = &metadata.ResultTableOption{QueryBackend: "doris", DirectTables: q.DirectTables, SearchAfter: []any{1}}
	})
	req.ResultTableOptions = options
	enabled = false
	ctx = metadata.WithQueryFlags(ctx, metadata.QueryFlags{DorisDirectQuery: &enabled})
	plan, err = req.ToRawQueryPlan(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, plan.Queries.Count())
	plan.Queries.Range("", func(q *metadata.Query) {
		require.Equal(t, "doris", q.QueryBackend)
		require.Len(t, q.DirectTables, 2)
		require.Equal(t, []any{1}, q.ResultTableOption.SearchAfter)
	})
}

func TestMergedPaginationDoesNotPinOtherClusters(t *testing.T) {
	for _, firstDirect := range []bool{false, true} {
		ctx, req, table := rawPlanFixture()
		ctx = metadata.WithQueryFlags(ctx, metadata.QueryFlags{DorisDirectQuery: &firstDirect})
		table.StorageType, table.Measurement = metadata.BkSqlStorageType, metadata.DorisStorageType
		table.DB = "1001_a"
		table.PhysicalTables = directTables("a")
		table.StorageClusterRecords = nil
		req.IsMergeDB, req.IsSearchAfter = true, true
		plan, err := req.ToRawQueryPlan(ctx)
		require.NoError(t, err)
		req.ResultTableOptions = metadata.ResultTableOptions{}
		plan.Queries.Range("", func(q *metadata.Query) {
			backend := metadata.BkSqlStorageType
			if firstDirect {
				backend = metadata.DorisStorageType
			}
			req.ResultTableOptions[q.TableUUID()] = &metadata.ResultTableOption{QueryBackend: backend, DirectTables: q.DirectTables, SearchAfter: []any{42}}
		})
		other := *table
		other.TableID, other.StorageID = "other", "99"
		req.TsDBMap["A"] = append(req.TsDBMap["A"], &other)
		nextDirect := !firstDirect
		ctx = metadata.WithQueryFlags(ctx, metadata.QueryFlags{DorisDirectQuery: &nextDirect})
		plan, err = req.ToRawQueryPlan(ctx)
		require.NoError(t, err)
		require.Equal(t, 2, plan.Queries.Count())
		plan.Queries.Range("", func(q *metadata.Query) {
			if q.StorageID == "99" {
				require.Nil(t, q.ResultTableOption)
				require.Equal(t, nextDirect, q.QueryBackend == metadata.DorisStorageType)
			}
		})
	}
}

func TestMixedBackendPaginationIndependentOfRTOrder(t *testing.T) {
	for _, order := range []string{"abc", "acb", "bac", "bca", "cab", "cba"} {
		for _, nextDirect := range []bool{true, false} {
			t.Run(order+map[bool]string{true: "_enabled", false: "_disabled"}[nextDirect], func(t *testing.T) {
				ctx, req, table := rawPlanFixture()
				enabled := true
				ctx = metadata.WithQueryFlags(ctx, metadata.QueryFlags{DorisDirectQuery: &enabled})
				table.StorageType, table.Measurement = metadata.BkSqlStorageType, metadata.DorisStorageType
				table.StorageClusterRecords = nil
				tables := make(map[rune]*uqQuery.TsDBV2)
				for _, name := range "abc" {
					member := *table
					member.TableID, member.DB = string(name), "1001_"+string(name)
					if name != 'c' {
						member.PhysicalTables = directTables(string(name))
					}
					tables[name] = &member
				}
				req.TsDBMap["A"] = TsDBs{tables['a'], tables['b'], tables['c']}
				req.IsMergeDB, req.IsSearchAfter = true, true
				plan, err := req.ToRawQueryPlan(ctx)
				require.NoError(t, err)
				require.Equal(t, 2, plan.Queries.Count())
				req.ResultTableOptions = metadata.ResultTableOptions{}
				plan.Queries.Range("", func(q *metadata.Query) {
					// C 未配置物理表，沿用执行器返回的无后端标记旧格式游标。
					option := &metadata.ResultTableOption{SearchAfter: []any{300}}
					if q.QueryBackend == metadata.DorisStorageType {
						require.Equal(t, []string{"1001_a", "1001_b"}, q.DBs)
						option.QueryBackend = metadata.DorisStorageType
						option.DirectTables = q.DirectTables
						option.SearchAfter = []any{100}
					} else {
						require.Equal(t, []string{"1001_c"}, q.DBs)
					}
					req.ResultTableOptions[q.TableUUID()] = option
				})
				req.TsDBMap["A"] = nil
				for _, name := range order {
					req.TsDBMap["A"] = append(req.TsDBMap["A"], tables[name])
				}
				ctx = metadata.WithQueryFlags(ctx, metadata.QueryFlags{DorisDirectQuery: &nextDirect})
				plan, err = req.ToRawQueryPlan(ctx)
				require.NoError(t, err)
				require.Equal(t, 2, plan.Queries.Count())
				plan.Queries.Range("", func(q *metadata.Query) {
					require.NotNil(t, q.ResultTableOption)
					if q.QueryBackend == metadata.DorisStorageType {
						require.Equal(t, []string{"1001_a", "1001_b"}, q.DBs)
						require.ElementsMatch(t, []metadata.PhysicalTable{tables['a'].PhysicalTables["main"], tables['b'].PhysicalTables["main"]}, q.DirectTables)
						require.Equal(t, []any{100}, q.ResultTableOption.SearchAfter)
					} else {
						require.Equal(t, []string{"1001_c"}, q.DBs)
						require.Empty(t, q.DirectTables)
						require.Equal(t, []any{300}, q.ResultTableOption.SearchAfter)
					}
				})
			})
		}
	}
}
