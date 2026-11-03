// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestPlanRawQueryExecutionsStorageRollback(t *testing.T) {
	location := time.FixedZone("UTC+08:00", 8*60*60)
	// 时间顺序：集群 16 生效、查询开始、切至集群 40、切回集群 16、查询结束。
	firstStorage16EnabledAt := time.Date(2026, 10, 1, 0, 0, 0, 0, location)
	queryStartsAt := time.Date(2026, 10, 19, 17, 26, 25, 0, location)
	secondStorage40EnabledAt := time.Date(2026, 10, 28, 0, 0, 0, 0, location)
	thirdStorage16EnabledAt := time.Date(2026, 10, 29, 0, 0, 0, 0, location)
	queryEndsAt := time.Date(2026, 11, 2, 17, 26, 30, 0, location)
	type physicalRequest struct {
		storageID   string
		tableID     string
		db          string
		start       int64
		end         int64
		fingerprint [sha256.Size]byte
	}
	type routePeriod struct {
		storageID string
		start     time.Time
		end       time.Time
	}

	for _, tc := range []struct {
		name               string
		currentStorageID   string
		records            []query.Record
		wantRoutePeriods   []routePeriod
		wantExecutionCount int
		wantStorageCounts  map[string]int
	}{
		{
			name:             "one storage switch",
			currentStorageID: "40",
			records: []query.Record{
				{StorageID: "16", EnableTime: firstStorage16EnabledAt.Unix()},
				{StorageID: "40", EnableTime: secondStorage40EnabledAt.Unix()},
			},
			wantRoutePeriods: []routePeriod{
				{storageID: "40", start: secondStorage40EnabledAt, end: queryEndsAt},
				{storageID: "16", start: queryStartsAt, end: secondStorage40EnabledAt},
			},
			wantExecutionCount: 2,
			wantStorageCounts:  map[string]int{"16": 1, "40": 1},
		},
		{
			name:             "storage switches from 16 to 40 and back to 16",
			currentStorageID: "16",
			records: []query.Record{
				{StorageID: "16", EnableTime: firstStorage16EnabledAt.Unix()},
				{StorageID: "40", EnableTime: secondStorage40EnabledAt.Unix()},
				{StorageID: "16", EnableTime: thirdStorage16EnabledAt.Unix()},
			},
			wantRoutePeriods: []routePeriod{
				{storageID: "16", start: thirdStorage16EnabledAt, end: queryEndsAt},
				{storageID: "40", start: secondStorage40EnabledAt, end: thirdStorage16EnabledAt},
				{storageID: "16", start: queryStartsAt, end: secondStorage40EnabledAt},
			},
			wantExecutionCount: 2,
			wantStorageCounts:  map[string]int{"16": 1, "40": 1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := &query.TsDBV2{StorageID: tc.currentStorageID, StorageClusterRecords: tc.records}
			routes := router.GetStorageIDRangesWithDirectionalOverlap(queryStartsAt, queryEndsAt, 0, 0)
			require.Len(t, routes, len(tc.wantRoutePeriods))
			for index, route := range routes {
				period := tc.wantRoutePeriods[index]
				assert.Equal(t, period.storageID, route.StorageID)
				assert.True(t, period.start.Equal(route.Start))
				assert.True(t, period.end.Equal(route.End))
			}

			// 将真实路由函数生成的各段时间写入 Raw 查询参数。
			queries := make(metadata.QueryList, 0, len(routes))
			for _, route := range routes {
				queries = append(queries, &metadata.Query{
					StorageType:     metadata.ElasticsearchStorageType,
					StorageID:       route.StorageID,
					TableID:         "trace-result-table",
					DB:              "trace-index",
					TimeField:       metadata.TimeField{Name: "end_time", Type: "long", Unit: "microsecond"},
					AllConditions:   metadata.AllConditions{{{DimensionName: "span_id", Operator: "eq", Value: []string{"target-span"}}}},
					Size:            100,
					RouteStart:      route.Start,
					RouteEnd:        route.End,
					RouteQueryStart: route.QueryStart,
					RouteQueryEnd:   route.QueryEnd,
				})
			}
			queryRef := metadata.QueryReference{
				"A": {&metadata.QueryMetric{QueryList: queries}},
			}
			plans, skipped := planRawQueryExecutions(queryRef, &structured.QueryTs{}, queryStartsAt, queryEndsAt)
			assert.Zero(t, skipped)
			assert.Equal(t, len(tc.wantRoutePeriods), queryRef.Count())
			// 预期保留全部历史分段，每个等价的物理请求只执行一次。
			assert.Equal(t, tc.wantExecutionCount, len(plans), "Raw 查询执行次数")

			// 用存储目标、全局时间和查询语义识别等价的执行请求。
			storageCounts := make(map[string]int)
			requests := make(map[physicalRequest]int)
			for _, plan := range plans {
				assert.True(t, queryStartsAt.Equal(plan.start))
				assert.True(t, queryEndsAt.Equal(plan.end))
				storageCounts[plan.query.StorageID]++
				fingerprint, err := rawESBatchSemanticFingerprint(&plan.query)
				require.NoError(t, err)
				requests[physicalRequest{
					storageID:   plan.query.StorageID,
					tableID:     plan.query.TableID,
					db:          plan.query.DB,
					start:       plan.start.UnixNano(),
					end:         plan.end.UnixNano(),
					fingerprint: fingerprint,
				}]++
			}
			repeatedRequests := 0
			for _, count := range requests {
				repeatedRequests += count - 1
			}
			assert.Equal(t, 2, len(requests), "不同的物理请求数")
			assert.Zero(t, repeatedRequests, "相同的物理请求被重复规划")
			assert.Equal(t, tc.wantStorageCounts, storageCounts)

			// Batch 路径使用相同的历史路由，并在准备 ES 请求前合并等价执行。
			batchPlans := planRawESBatchQueryExecutions(queryRef, &structured.QueryTs{}, queryStartsAt, queryEndsAt)
			assert.Equal(t, tc.wantExecutionCount, len(batchPlans), "Batch 查询执行次数")
			batchStorageCounts := make(map[string]int)
			for _, plan := range batchPlans {
				batchStorageCounts[plan.query.StorageID]++
			}
			assert.Equal(t, tc.wantStorageCounts, batchStorageCounts)
			assert.Len(t, queryRef.CollectRouteInfo(), len(tc.wantRoutePeriods))
		})
	}
}

func TestRawQueryExecutionTrackerRequiresEquivalentESRequest(t *testing.T) {
	start := time.Date(2026, 10, 19, 17, 26, 25, 0, time.UTC)
	end := start.Add(14 * 24 * time.Hour)

	for _, tc := range []struct {
		name            string
		changeQueries   func(first, second *metadata.Query)
		changeTimeRange func(start, end time.Time) (time.Time, time.Time)
		wantDuplicate   bool
	}{
		{name: "same request across two route periods", wantDuplicate: true},
		{
			name: "different storage",
			changeQueries: func(_, second *metadata.Query) {
				second.StorageID = "40"
			},
		},
		{
			name: "different index",
			changeQueries: func(_, second *metadata.Query) {
				second.DB = "other-trace-index"
			},
		},
		{
			name: "different filter",
			changeQueries: func(_, second *metadata.Query) {
				second.AllConditions = metadata.AllConditions{{{DimensionName: "span_id", Operator: "eq", Value: []string{"other-span"}}}}
			},
		},
		{
			name: "different projection",
			changeQueries: func(_, second *metadata.Query) {
				second.Source = []string{"span_id"}
			},
		},
		{
			name: "different pagination cursor",
			changeQueries: func(_, second *metadata.Query) {
				second.ResultTableOption = &metadata.ResultTableOption{SearchAfter: []any{42}}
			},
		},
		{
			name: "different offset",
			changeQueries: func(_, second *metadata.Query) {
				offset := 10
				second.ResultTableOption = &metadata.ResultTableOption{From: &offset}
			},
		},
		{
			name: "different exact total setting",
			changeQueries: func(_, second *metadata.Query) {
				second.ExactTotal = true
			},
		},
		{
			name: "different range end boundary",
			changeQueries: func(_, second *metadata.Query) {
				second.RangeEndExclusive = true
			},
		},
		{
			name: "different query end",
			changeTimeRange: func(start, end time.Time) (time.Time, time.Time) {
				return start, end.Add(time.Second)
			},
		},
		{
			name: "scroll requests remain independent",
			changeQueries: func(first, second *metadata.Query) {
				first.Scroll = "1m"
				second.Scroll = "1m"
			},
		},
		{
			name: "scroll cursor requests remain independent",
			changeQueries: func(first, second *metadata.Query) {
				first.ResultTableOption = &metadata.ResultTableOption{ScrollID: "scroll-id"}
				second.ResultTableOption = &metadata.ResultTableOption{ScrollID: "scroll-id"}
			},
		},
		{
			name: "sliced requests remain independent",
			changeQueries: func(first, second *metadata.Query) {
				first.ResultTableOption = &metadata.ResultTableOption{SliceMax: 2}
				second.ResultTableOption = &metadata.ResultTableOption{SliceMax: 2}
			},
		},
		{
			name: "slice index requests remain independent",
			changeQueries: func(first, second *metadata.Query) {
				first.ResultTableOption = &metadata.ResultTableOption{SliceIndex: 1}
				second.ResultTableOption = &metadata.ResultTableOption{SliceIndex: 1}
			},
		},
		{
			name: "other storage types remain independent",
			changeQueries: func(first, second *metadata.Query) {
				first.StorageType = metadata.BkSqlStorageType
				second.StorageType = metadata.BkSqlStorageType
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := metadata.Query{
				StorageType:   metadata.ElasticsearchStorageType,
				StorageID:     "16",
				TableID:       "trace-result-table",
				DB:            "trace-index",
				AllConditions: metadata.AllConditions{{{DimensionName: "span_id", Operator: "eq", Value: []string{"target-span"}}}},
				RouteStart:    start,
				RouteEnd:      start.Add(24 * time.Hour),
			}
			second := first
			second.RouteStart = start.Add(48 * time.Hour)
			second.RouteEnd = end
			if tc.changeQueries != nil {
				tc.changeQueries(&first, &second)
			}
			secondStart, secondEnd := start, end
			if tc.changeTimeRange != nil {
				secondStart, secondEnd = tc.changeTimeRange(start, end)
			}

			var tracker rawQueryExecutionTracker
			assert.False(t, tracker.isDuplicate(&first, start, end))
			assert.Equal(t, tc.wantDuplicate, tracker.isDuplicate(&second, secondStart, secondEnd))
		})
	}
}

func TestRawExecutionPlansKeepSearchAfterCursorAcrossStorageSwitch(t *testing.T) {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	end := start.Add(14 * 24 * time.Hour)
	router := &query.TsDBV2{
		StorageID: "16",
		StorageClusterRecords: []query.Record{
			{StorageID: "16", EnableTime: start.Add(-24 * time.Hour).Unix()},
			{StorageID: "40", EnableTime: start.Add(9 * 24 * time.Hour).Unix()},
			{StorageID: "16", EnableTime: start.Add(10 * 24 * time.Hour).Unix()},
		},
	}
	routes := router.GetStorageIDRangesWithDirectionalOverlap(start, end, 0, 0)
	require.Len(t, routes, 3)

	queries := make(metadata.QueryList, 0, len(routes))
	for _, route := range routes {
		var option *metadata.ResultTableOption
		if route.StorageID == "16" {
			option = &metadata.ResultTableOption{SearchAfter: []any{42}}
		}
		queries = append(queries, &metadata.Query{
			StorageType:       metadata.ElasticsearchStorageType,
			StorageID:         route.StorageID,
			TableID:           "trace-result-table",
			DB:                "trace-index",
			Size:              100,
			ResultTableOption: option,
			RouteStart:        route.Start,
			RouteEnd:          route.End,
			RouteQueryStart:   route.QueryStart,
			RouteQueryEnd:     route.QueryEnd,
		})
	}
	queryRef := metadata.QueryReference{"A": {&metadata.QueryMetric{QueryList: queries}}}
	queryTs := &structured.QueryTs{
		IsSearchAfter: true,
		ResultTableOptions: metadata.ResultTableOptions{
			"trace-result-table|16": {SearchAfter: []any{42}},
		},
	}

	ordinaryPlans, skipped := planRawQueryExecutions(queryRef, queryTs, start, end)
	require.Len(t, ordinaryPlans, 1)
	assert.Equal(t, 1, skipped)
	assert.Equal(t, "16", ordinaryPlans[0].query.StorageID)
	assert.Equal(t, []any{42}, ordinaryPlans[0].query.ResultTableOption.SearchAfter)

	batchPlans := planRawESBatchQueryExecutions(queryRef, queryTs, start, end)
	require.Len(t, batchPlans, 1)
	assert.Equal(t, "16", batchPlans[0].query.StorageID)
	assert.Equal(t, []any{42}, batchPlans[0].query.ResultTableOption.SearchAfter)
	assert.Len(t, queryRef.CollectRouteInfo(), 3)
}
