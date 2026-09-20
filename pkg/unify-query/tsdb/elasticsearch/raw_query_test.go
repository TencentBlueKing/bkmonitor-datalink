// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestPrepareRawQuery_FinalBodyIncludesSearchAfterAndDoesNotMutateInput(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	instance, err := NewInstance(ctx, &InstanceOption{
		Connect: Connect{Address: mock.EsUrl},
		MaxSize: 100,
		Timeout: 3 * time.Second,
	})
	require.NoError(t, err)

	from := 7
	query := &metadata.Query{
		DB:          "es_index",
		Field:       "a",
		TableID:     "result_table.es",
		StorageID:   "3",
		StorageType: metadata.ElasticsearchStorageType,
		From:        3,
		Size:        10,
		Source:      []string{"a"},
		TimeField: metadata.TimeField{
			Name: "dtEventTimeStamp",
			Type: TimeFieldTypeTime,
			Unit: "millisecond",
		},
		Orders: metadata.Orders{
			{Name: "dtEventTimeStamp", Ast: false},
		},
		ResultTableOption: &metadata.ResultTableOption{
			From:        &from,
			SearchAfter: []any{1723679900000, "doc-7"},
		},
	}
	originalOption := query.ResultTableOption.Clone()

	prepared, err := instance.PrepareRawQuery(
		ctx,
		query,
		time.UnixMilli(1723593608000),
		time.UnixMilli(1723679962000),
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, prepared)

	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(prepared.body), &body))
	assert.Equal(t, []any{float64(1723679900000), "doc-7"}, body["search_after"])

	assert.Equal(t, 3, query.From)
	assert.Equal(t, 10, query.Size)
	assert.Equal(t, originalOption, query.ResultTableOption)
	assert.NotSame(t, query.ResultTableOption, prepared.query.ResultTableOption)
	assert.NotSame(t, query.ResultTableOption.From, prepared.query.ResultTableOption.From)
}

func TestQueryPreparedRawDataMatchesQueryRawData(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	httpmock.RegisterResponder(
		http.MethodPost,
		mock.EsUrl+"/es_index/_search",
		httpmock.NewStringResponder(http.StatusOK, `{
			"hits": {
				"total": {"value": 2, "relation": "eq"},
				"hits": [
					{"_index":"es_index","_id":"one","sort":[1723679900000],"_source":{"dtEventTimeStamp":1723679900000,"level":"info"}},
					{"_index":"es_index","_id":"two","sort":[1723679800000],"_source":{"dtEventTimeStamp":1723679800000,"level":"warn"}}
				]
			}
		}`),
	)

	instance, err := NewInstance(ctx, &InstanceOption{
		Connect: Connect{Address: mock.EsUrl},
		MaxSize: 100,
		Timeout: 3 * time.Second,
	})
	require.NoError(t, err)

	query := &metadata.Query{
		DB:          "es_index",
		Field:       "a",
		TableID:     "result_table.es",
		StorageID:   "3",
		StorageType: metadata.ElasticsearchStorageType,
		From:        0,
		Size:        2,
		Source:      []string{"dtEventTimeStamp", "level"},
		TimeField: metadata.TimeField{
			Name: "dtEventTimeStamp",
			Type: TimeFieldTypeTime,
			Unit: "millisecond",
		},
		Orders: metadata.Orders{
			{Name: "dtEventTimeStamp", Ast: false},
		},
	}
	start := time.UnixMilli(1723593608000)
	end := time.UnixMilli(1723679962000)

	baselineRows := make(chan map[string]any, 16)
	baselineSize, baselineTotal, baselineOption, err := instance.QueryRawData(ctx, query, start, end, baselineRows)
	require.NoError(t, err)
	close(baselineRows)

	prepared, err := instance.PrepareRawQuery(ctx, query, start, end, nil)
	require.NoError(t, err)
	preparedRows := make(chan map[string]any, 16)
	preparedSize, preparedTotal, preparedOption, err := instance.QueryPreparedRawData(ctx, prepared, preparedRows)
	require.NoError(t, err)
	close(preparedRows)

	assert.Equal(t, baselineSize, preparedSize)
	assert.Equal(t, baselineTotal, preparedTotal)
	assert.Equal(t, baselineOption, preparedOption)
	assert.Equal(t, drainRawRows(baselineRows), drainRawRows(preparedRows))
}

func TestPreparedFieldMetadataFieldsMapReturnsDeepCopy(t *testing.T) {
	prepared := &PreparedFieldMetadata{
		fieldMap: metadata.FieldsMap{
			"log": {
				FieldName:       "log",
				FieldType:       "text",
				TokenizeOnChars: []string{"whitespace"},
			},
		},
		complete: true,
	}

	first := prepared.FieldsMap()
	firstOption := first["log"]
	firstOption.TokenizeOnChars[0] = "letter"
	first["log"] = firstOption
	delete(first, "missing")

	second := prepared.FieldsMap()
	require.Contains(t, second, "log")
	assert.Equal(t, []string{"whitespace"}, second["log"].TokenizeOnChars)
}

func TestCloneRawMetadataQueryDeepCopiesNestedAggregates(t *testing.T) {
	source := &metadata.Query{
		Aggregates: metadata.Aggregates{{
			Name:       "sum",
			Dimensions: []string{"service"},
			Args:       []any{"argument"},
		}},
	}

	cloned := cloneRawMetadataQuery(source)
	source.Aggregates[0].Dimensions[0] = "mutated"
	source.Aggregates[0].Args[0] = "mutated"

	assert.Equal(t, []string{"service"}, cloned.Aggregates[0].Dimensions)
	assert.Equal(t, []any{"argument"}, cloned.Aggregates[0].Args)
}

func TestPrepareRawQueryPrefetchCompletenessControlsMetadataLookup(t *testing.T) {
	for _, testCase := range []struct {
		name              string
		complete          bool
		expectedIndexGets int32
	}{
		{name: "complete empty metadata is reusable", complete: true, expectedIndexGets: 0},
		{name: "incomplete metadata is refreshed", complete: false, expectedIndexGets: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mock.Init()
			ctx := metadata.InitHashID(context.Background())

			const index = "prepared_metadata_completeness"
			var indexGets int32
			httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+index, func(*http.Request) (*http.Response, error) {
				indexGets++
				return httpmock.NewStringResponse(http.StatusOK, `{}`), nil
			})

			instance, err := NewInstance(ctx, &InstanceOption{
				Connect: Connect{Address: mock.EsUrl},
				MaxSize: 100,
				Timeout: 3 * time.Second,
			})
			require.NoError(t, err)

			query := &metadata.Query{
				DB:          index,
				Field:       "a",
				TableID:     "result_table.es",
				StorageID:   "3",
				StorageType: metadata.ElasticsearchStorageType,
				Size:        1,
				TimeField: metadata.TimeField{
					Name: "dtEventTimeStamp",
					Type: TimeFieldTypeTime,
					Unit: "millisecond",
				},
			}
			reuseIdentity, err := rawFieldMetadataReuseIdentity([]string{index}, query.FieldAlias)
			require.NoError(t, err)
			prefetched := &PreparedFieldMetadata{
				indexes:       []string{index},
				fieldMap:      metadata.FieldsMap{},
				connectionKey: instance.RawBatchConnectionKey(ctx),
				reuseIdentity: reuseIdentity,
				complete:      testCase.complete,
			}

			prepared, err := instance.PrepareRawQuery(
				ctx,
				query,
				time.UnixMilli(1723593608000),
				time.UnixMilli(1723679962000),
				prefetched,
			)
			require.NoError(t, err)
			require.NotNil(t, prepared)
			assert.Equal(t, testCase.expectedIndexGets, indexGets)
		})
	}
}

func TestQueryPreparedRawDataCanOnlyBeConsumedOnce(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	var searches int
	httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/es_index/_search", func(*http.Request) (*http.Response, error) {
		searches++
		return httpmock.NewStringResponse(http.StatusOK, `{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`), nil
	})

	instance, err := NewInstance(ctx, &InstanceOption{
		Connect: Connect{Address: mock.EsUrl},
		MaxSize: 100,
		Timeout: 3 * time.Second,
	})
	require.NoError(t, err)
	query := &metadata.Query{
		DB:          "es_index",
		Field:       "a",
		TableID:     "result_table.es",
		StorageID:   "3",
		StorageType: metadata.ElasticsearchStorageType,
		Size:        1,
		TimeField: metadata.TimeField{
			Name: "dtEventTimeStamp",
			Type: TimeFieldTypeTime,
			Unit: "millisecond",
		},
	}
	prepared, err := instance.PrepareRawQuery(
		ctx,
		query,
		time.UnixMilli(1723593608000),
		time.UnixMilli(1723679962000),
		nil,
	)
	require.NoError(t, err)

	rows := make(chan map[string]any, 1)
	_, _, _, err = instance.QueryPreparedRawData(ctx, prepared, rows)
	require.NoError(t, err)
	_, _, _, err = instance.QueryPreparedRawData(ctx, prepared, rows)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already consumed")
	assert.Equal(t, 1, searches)
}

func TestQueryPreparedRawDataConcurrentConsumersOnlyExecuteOnce(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	var searches atomic.Int32
	httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/es_index/_search", func(*http.Request) (*http.Response, error) {
		searches.Add(1)
		return httpmock.NewStringResponse(http.StatusOK, `{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`), nil
	})

	instance, err := NewInstance(ctx, &InstanceOption{
		Connect: Connect{Address: mock.EsUrl},
		MaxSize: 100,
		Timeout: 3 * time.Second,
	})
	require.NoError(t, err)
	prepared, err := instance.PrepareRawQuery(
		ctx,
		&metadata.Query{
			DB:          "es_index",
			Field:       "a",
			TableID:     "result_table.es",
			StorageID:   "3",
			StorageType: metadata.ElasticsearchStorageType,
			Size:        1,
			TimeField: metadata.TimeField{
				Name: "dtEventTimeStamp",
				Type: TimeFieldTypeTime,
				Unit: "millisecond",
			},
		},
		time.UnixMilli(1723593608000),
		time.UnixMilli(1723679962000),
		nil,
	)
	require.NoError(t, err)

	start := make(chan struct{})
	results := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for range 2 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			_, _, _, queryErr := instance.QueryPreparedRawData(
				ctx,
				prepared,
				make(chan map[string]any, 1),
			)
			results <- queryErr
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)

	var successCount, consumedCount int
	for queryErr := range results {
		if queryErr == nil {
			successCount++
			continue
		}
		if strings.Contains(queryErr.Error(), "already consumed") {
			consumedCount++
		}
	}
	assert.Equal(t, 1, successCount)
	assert.Equal(t, 1, consumedCount)
	assert.Equal(t, int32(1), searches.Load())
}

func TestQueryRawDataPreparationErrorContract(t *testing.T) {
	t.Run("alias construction error is not relabeled as field mapping", func(t *testing.T) {
		mock.Init()
		ctx := metadata.InitHashID(context.Background())
		instance, err := NewInstance(ctx, &InstanceOption{
			Connect: Connect{Address: mock.EsUrl},
			Timeout: 3 * time.Second,
		})
		require.NoError(t, err)

		query := &metadata.Query{
			DB:          "unused",
			DBs:         []string{""},
			TableID:     "result_table.es",
			StorageType: metadata.ElasticsearchStorageType,
		}
		_, _, _, err = instance.QueryRawData(ctx, query, time.Time{}, time.Time{}, make(chan map[string]any, 1))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "构建索引异常")
		assert.NotContains(t, err.Error(), "字段查询异常")
	})

	t.Run("mapping error retains the alias context", func(t *testing.T) {
		mock.Init()
		ctx := metadata.InitHashID(context.Background())
		const index = "prepared_mapping_error"
		httpmock.RegisterResponder(
			http.MethodGet,
			mock.EsUrl+"/"+index,
			httpmock.NewStringResponder(http.StatusInternalServerError, `{"error":"index get failed"}`),
		)
		httpmock.RegisterResponder(
			http.MethodGet,
			mock.EsUrl+"/"+index+"/_mapping/",
			httpmock.NewStringResponder(http.StatusInternalServerError, `{"error":"mapping failed"}`),
		)
		instance, err := NewInstance(ctx, &InstanceOption{
			Connect: Connect{Address: mock.EsUrl},
			Timeout: 3 * time.Second,
		})
		require.NoError(t, err)

		query := &metadata.Query{
			DB:          index,
			TableID:     "result_table.es",
			StorageType: metadata.ElasticsearchStorageType,
		}
		_, _, _, err = instance.QueryRawData(ctx, query, time.Time{}, time.Time{}, make(chan map[string]any, 1))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "字段查询异常: ["+index+"]")
	})
}

func TestQueryRawDataPreparedPathRetainsMissingMappingFallback(t *testing.T) {
	mock.Init()
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())

	const (
		alias     = "prepared_fallback_alias"
		badIndex  = "prepared_fallback_bad"
		goodIndex = "prepared_fallback_good"
	)
	httpmock.RegisterResponder(
		http.MethodGet,
		mock.EsUrl+"/"+alias,
		httpmock.NewStringResponder(
			http.StatusOK,
			fmt.Sprintf(
				`{"%s":{"aliases":{"%s":{}},"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"}}}},"%s":{"aliases":{"%s":{}},"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"},"svrname":{"type":"keyword"}}}}}`,
				badIndex,
				alias,
				goodIndex,
				alias,
			),
		),
	)

	searchCalls := 0
	httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+alias+"/_search", func(request *http.Request) (*http.Response, error) {
		searchCalls++
		switch searchCalls {
		case 1:
			return httpmock.NewStringResponse(
				http.StatusOK,
				fmt.Sprintf(
					`{"took":1,"timed_out":false,"_shards":{"total":2,"successful":1,"skipped":0,"failed":1,"failures":[{"shard":0,"index":"%s","reason":{"type":"query_shard_exception","reason":"No mapping found for [svrname] in order to sort on"}}]},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
					badIndex,
				),
			), nil
		case 2:
			assertSearchBodyFiltersIndexes(t, request, badIndex)
			return httpmock.NewStringResponse(
				http.StatusOK,
				`{"took":1,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
			), nil
		case 3:
			body := assertSearchBodySortHasUnmappedType(t, request, "svrname", "keyword")
			assert.Equal(t, 1, strings.Count(body, `"unmapped_type":"keyword"`))
			return httpmock.NewStringResponse(
				http.StatusOK,
				`{"took":1,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
			), nil
		default:
			return nil, fmt.Errorf("unexpected search call %d", searchCalls)
		}
	})

	instance, err := NewInstance(ctx, &InstanceOption{
		Connect: Connect{Address: mock.EsUrl},
		Timeout: time.Minute,
	})
	require.NoError(t, err)
	query := &metadata.Query{
		DB:          alias,
		Field:       "svrname",
		TableID:     "result_table.es",
		StorageID:   "3",
		StorageType: metadata.ElasticsearchStorageType,
		TimeField: metadata.TimeField{
			Name: "dtEventTimeStamp",
			Type: TimeFieldTypeTime,
			Unit: "millisecond",
		},
		Orders: metadata.Orders{{Name: "svrname", Ast: false}},
	}

	_, _, _, err = instance.QueryRawData(
		ctx,
		query,
		time.UnixMilli(1784013830711),
		time.UnixMilli(1784014730711),
		make(chan map[string]any, 1),
	)
	require.NoError(t, err)
	assert.Equal(t, 3, searchCalls)
}

func drainRawRows(rows <-chan map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(rows))
	for row := range rows {
		result = append(result, row)
	}
	return result
}
