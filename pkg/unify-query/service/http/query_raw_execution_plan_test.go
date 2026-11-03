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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestPrepareRawQueryExecutionsPagination(t *testing.T) {
	for _, tc := range []struct {
		name        string
		targetCount int
		option      *metadata.ResultTableOption
		multiFrom   bool
		wantFrom    int
		wantSize    int
	}{
		{name: "单目标保留请求分页", targetCount: 1, wantFrom: 5, wantSize: 10},
		{name: "多个执行目标读取合并前的数据", targetCount: 2, wantSize: 15},
		{name: "MultiFrom保持各目标的分页", targetCount: 2, multiFrom: true, wantFrom: 5, wantSize: 10},
		{name: "单目标使用返回的偏移游标", targetCount: 1, option: &metadata.ResultTableOption{From: rawPaginationIntPointer(20)}, wantFrom: 20, wantSize: 10},
		{name: "多个目标也保留偏移游标", targetCount: 2, option: &metadata.ResultTableOption{From: rawPaginationIntPointer(20)}, wantFrom: 20, wantSize: 10},
		{name: "零偏移游标不阻止合并分页", targetCount: 2, option: &metadata.ResultTableOption{From: rawPaginationIntPointer(0)}, wantSize: 15},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queries := make(metadata.QueryList, 0, tc.targetCount)
			for range tc.targetCount {
				queries = append(queries, &metadata.Query{From: 5, Size: 10, ResultTableOption: tc.option})
			}
			input := metadata.QueryReference{"A": {{ReferenceName: "A", MetricName: "spans", IsCount: true, QueryList: queries}}}
			prepared, skipped := prepareRawQueryExecutions(input, &structured.QueryTs{IsMultiFrom: tc.multiFrom})
			assert.Zero(t, skipped)
			require.Equal(t, tc.targetCount, prepared.Count())
			assert.Equal(t, "A", prepared["A"][0].ReferenceName)
			assert.Equal(t, "spans", prepared["A"][0].MetricName)
			assert.True(t, prepared["A"][0].IsCount)
			prepared.Range("", func(query *metadata.Query) {
				assert.Equal(t, tc.wantFrom, query.From)
				assert.Equal(t, tc.wantSize, query.Size)
			})
			for _, query := range queries {
				assert.Equal(t, 5, query.From)
				assert.Equal(t, 10, query.Size)
			}
		})
	}
}

func TestPrepareRawQueryExecutionsSearchAfter(t *testing.T) {
	for _, isBatch := range []bool{false, true} {
		for _, continuation := range []bool{false, true} {
			input := metadata.QueryReference{
				"A": {nil, {QueryList: metadata.QueryList{
					nil,
					{StorageType: metadata.ElasticsearchStorageType, TableID: "es", ResultTableOption: &metadata.ResultTableOption{SearchAfter: []any{42}}},
					{StorageType: metadata.BkSqlStorageType, TableID: "doris", ResultTableOption: &metadata.ResultTableOption{From: rawPaginationIntPointer(20)}},
					{TableID: "finished"},
					{TableID: "empty-cursor", ResultTableOption: &metadata.ResultTableOption{}},
					{TableID: "zero-offset", ResultTableOption: &metadata.ResultTableOption{From: rawPaginationIntPointer(0)}},
				}}},
			}
			request := &structured.QueryTs{IsSearchAfter: true, IsESBatch: isBatch}
			if continuation {
				request.ResultTableOptions = metadata.ResultTableOptions{"es": input["A"][1].QueryList[1].ResultTableOption}
			}
			prepared, skipped := prepareRawQueryExecutions(input, request)
			if continuation {
				require.Equal(t, 2, prepared.Count())
				assert.Equal(t, 3, skipped)
				assert.Equal(t, "es", prepared["A"][0].QueryList[0].TableID)
				assert.Equal(t, "doris", prepared["A"][0].QueryList[1].TableID)
				assert.Equal(t, 20, prepared["A"][0].QueryList[1].From)
			} else {
				assert.Equal(t, 5, prepared.Count())
				assert.Zero(t, skipped)
			}
			prepared.Range("", func(query *metadata.Query) { assert.True(t, query.IsSearchAfter) })
			input.Range("", func(query *metadata.Query) { assert.False(t, query.IsSearchAfter) })
		}
	}
}

func TestPrepareRawQueryExecutionsIsolatesPaginationState(t *testing.T) {
	option := &metadata.ResultTableOption{
		From: rawPaginationIntPointer(20), SearchAfter: []any{42}, FieldType: map[string]string{"timestamp": "date"},
	}
	query := &metadata.Query{From: 5, Size: 10, ResultTableOption: option}
	input := metadata.QueryReference{"A": {{QueryList: metadata.QueryList{query, query}}}}
	prepared, skipped := prepareRawQueryExecutions(input, &structured.QueryTs{})
	assert.Zero(t, skipped)
	require.Equal(t, 2, prepared.Count())
	first, second := prepared["A"][0].QueryList[0], prepared["A"][0].QueryList[1]
	first.From = 99
	*first.ResultTableOption.From = 99
	first.ResultTableOption.SearchAfter[0] = 99
	first.ResultTableOption.FieldType["timestamp"] = "long"
	assert.Equal(t, 20, second.From)
	assert.Equal(t, 5, query.From)
	for _, independent := range []*metadata.ResultTableOption{option, second.ResultTableOption} {
		assert.Equal(t, 20, *independent.From)
		assert.Equal(t, []any{42}, independent.SearchAfter)
		assert.Equal(t, "date", independent.FieldType["timestamp"])
	}
}

func TestPrepareRawQueryExecutionsEmptyTargets(t *testing.T) {
	prepared, skipped := prepareRawQueryExecutions(nil, &structured.QueryTs{})
	assert.Empty(t, prepared)
	assert.Zero(t, skipped)

	input := metadata.QueryReference{"A": {{QueryList: metadata.QueryList{{TableID: "finished"}}}}}
	prepared, skipped = prepareRawQueryExecutions(input, &structured.QueryTs{
		IsSearchAfter: true, ResultTableOptions: metadata.ResultTableOptions{"other": {}},
	})
	assert.Empty(t, prepared)
	assert.Equal(t, 1, skipped)
	assert.Equal(t, 1, input.Count())
}

func rawPaginationIntPointer(value int) *int { return &value }
