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
	stdjson "encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	uqtrace "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

func TestInstanceEsQueryMissingMappingEmptyIndexFallback(t *testing.T) {
	tests := []struct {
		name                string
		alias               string
		badIndex            string
		goodIndex           string
		expectAliasCheck    bool
		expectPhysicalCheck bool
		expectRetry         bool
	}{
		{
			name:             "alias filter and search routing are kept during empty check",
			alias:            "test_alias",
			badIndex:         "bad_index",
			goodIndex:        "good_index",
			expectAliasCheck: true,
			expectRetry:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock.Init()
			metadata.InitMetadata()
			ctx := metadata.InitHashID(context.Background())

			// alias 元数据带有 filter/search_routing，用来显式验证 fallback 的安全约束：
			// 空检查和 retry 都必须继续使用 alias target，而不是直接查询物理索引。
			// ES 文档：https://www.elastic.co/guide/en/elasticsearch/reference/7.17/aliases.html
			httpmock.RegisterResponder(
				http.MethodGet,
				mock.EsUrl+"/"+tt.alias,
				httpmock.NewStringResponder(
					http.StatusOK,
					fmt.Sprintf(`{"%s":{"aliases":{"%s":{"filter":{"term":{"tenant_id":"123"}},"search_routing":"1"}}},"%s":{"aliases":{"%s":{"filter":{"term":{"tenant_id":"123"}},"search_routing":"1"}}}}`, tt.badIndex, tt.alias, tt.goodIndex, tt.alias),
				),
			)

			var aliasEmptyCheckCalled, directPhysicalEmptyCheckCalled, retryCalled bool
			aliasSearchCalls := 0
			httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+tt.alias+"/_search", func(r *http.Request) (*http.Response, error) {
				aliasSearchCalls++
				if aliasSearchCalls == 1 {
					return httpmock.NewStringResponse(
						http.StatusOK,
						fmt.Sprintf(`{"took":1,"timed_out":false,"_shards":{"total":2,"successful":1,"skipped":0,"failed":1,"failures":[{"shard":0,"index":"%s","reason":{"type":"query_shard_exception","reason":"No mapping found for [svrname] in order to sort on"}}]},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`, tt.badIndex),
					), nil
				}
				if aliasSearchCalls == 3 {
					retryCalled = true
					bodyString := assertSearchBodySortHasUnmappedType(t, r, "svrname", "keyword")
					assert.Equal(t, 1, strings.Count(bodyString, `"value_count"`))
					return httpmock.NewStringResponse(http.StatusOK, `{"took":1,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]},"aggregations":{"_value":{"value":1}}}`), nil
				}
				aliasEmptyCheckCalled = true
				assertSearchBodyFiltersIndexes(t, r, tt.badIndex)
				return httpmock.NewStringResponse(http.StatusOK, `{"took":1,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`), nil
			})
			// 如果空检查直接查物理索引，会看到 alias 外的无关文档并错误取消 fallback。
			// 期望空检查仍请求 alias，并在 body 中通过 _index terms 收窄到 badIndex。
			httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+tt.badIndex+"/_search", func(r *http.Request) (*http.Response, error) {
				directPhysicalEmptyCheckCalled = true
				return httpmock.NewStringResponse(http.StatusOK, `{"took":1,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":3,"relation":"eq"},"hits":[]}}`), nil
			})
			inst, err := NewInstance(ctx, &InstanceOption{
				Connect: Connect{Address: mock.EsUrl},
				Timeout: time.Minute,
			})
			require.NoError(t, err)

			query := &metadata.Query{
				DB:          tt.alias,
				Field:       "svrname",
				TimeField:   metadata.TimeField{Name: "dtEventTimeStamp", Type: TimeFieldTypeTime, Unit: "millisecond"},
				StorageType: metadata.ElasticsearchStorageType,
				Orders:      metadata.Orders{{Name: "svrname", Ast: false}},
				Aggregates:  metadata.Aggregates{{Name: Count, Field: "dtEventTimeStamp"}},
			}
			fact := NewFormatFactory(ctx).
				WithQuery(query.Field, query.TimeField, time.UnixMilli(1784013830711), time.UnixMilli(1784014730711), function.Millisecond, 0).
				WithFieldMap(metadata.FieldsMap{
					"svrname":          {FieldType: "keyword"},
					"dtEventTimeStamp": {FieldType: TimeFieldTypeTime},
				}).
				WithOrders(query.Orders)

			res, err := inst.esQuery(ctx, &queryOption{
				indexes: []string{tt.alias},
				start:   time.UnixMilli(1784013830711),
				end:     time.UnixMilli(1784014730711),
				query:   query,
				conn:    inst.connect,
			}, fact)

			require.NoError(t, err)
			require.NotNil(t, res)
			assert.Equal(t, tt.expectAliasCheck, aliasEmptyCheckCalled)
			assert.Equal(t, tt.expectPhysicalCheck, directPhysicalEmptyCheckCalled)
			assert.Equal(t, tt.expectRetry, retryCalled)
			assert.NotNil(t, res.Aggregations)
		})
	}
}

func TestInstanceEsQueryMissingMappingMultiEmptyIndexFallback(t *testing.T) {
	mock.Init()
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())

	const (
		alias      = "test_alias_multi"
		badIndex1  = "bad_index_multi_1"
		badIndex2  = "bad_index_multi_2"
		goodIndex  = "good_index_multi"
		failedBody = `{"type":"query_shard_exception","reason":"No mapping found for [svrname] in order to sort on"}`
	)
	httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+alias, httpmock.NewStringResponder(http.StatusOK, fmt.Sprintf(`{"%s":{},"%s":{},"%s":{}}`, badIndex1, badIndex2, goodIndex)))

	var emptyCheckCalled, retryCalled int
	aliasSearchCalls := 0
	httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+alias+"/_search", func(r *http.Request) (*http.Response, error) {
		aliasSearchCalls++
		if aliasSearchCalls == 1 {
			return httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`{
			"took":1,
			"timed_out":false,
			"_shards":{
				"total":3,
				"successful":1,
				"skipped":0,
				"failed":2,
				"failures":[
					{"shard":0,"index":"%s","reason":%s},
					{"shard":0,"index":"%s","reason":%s}
				]
			},
			"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}
			}`, badIndex1, failedBody, badIndex2, failedBody)), nil
		}
		if aliasSearchCalls == 3 {
			retryCalled++
			assertSearchBodySortHasUnmappedType(t, r, "svrname", "keyword")
			return httpmock.NewStringResponse(http.StatusOK, `{
				"took":1,
				"timed_out":false,
				"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},
				"hits":{"total":{"value":0,"relation":"eq"},"hits":[]},
				"aggregations":{"_value":{"value":1}}
			}`), nil
		}
		// 两个失败索引用一次基于 alias 的请求完成空检查。请求 body 中用 _index
		// terms 收窄到失败索引，避免 URL 随健康索引数量增长。
		emptyCheckCalled++
		assertSearchBodyFiltersIndexes(t, r, badIndex1, badIndex2)
		return httpmock.NewStringResponse(http.StatusOK, `{
			"took":1,
			"timed_out":false,
			"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},
			"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}
		}`), nil
	})

	inst, err := NewInstance(ctx, &InstanceOption{
		Connect: Connect{Address: mock.EsUrl},
		Timeout: time.Minute,
	})
	require.NoError(t, err)

	query := &metadata.Query{
		DB:          alias,
		Field:       "svrname",
		TimeField:   metadata.TimeField{Name: "dtEventTimeStamp", Type: TimeFieldTypeTime, Unit: "millisecond"},
		StorageType: metadata.ElasticsearchStorageType,
		Orders:      metadata.Orders{{Name: "svrname", Ast: false}},
		Aggregates:  metadata.Aggregates{{Name: Count, Field: "dtEventTimeStamp"}},
	}
	fact := NewFormatFactory(ctx).
		WithQuery(query.Field, query.TimeField, time.UnixMilli(1784013830711), time.UnixMilli(1784014730711), function.Millisecond, 0).
		WithFieldMap(metadata.FieldsMap{
			"svrname":          {FieldType: "keyword"},
			"dtEventTimeStamp": {FieldType: TimeFieldTypeTime},
		}).
		WithOrders(query.Orders)

	res, err := inst.esQuery(ctx, &queryOption{
		indexes: []string{alias},
		start:   time.UnixMilli(1784013830711),
		end:     time.UnixMilli(1784014730711),
		query:   query,
		conn:    inst.connect,
	}, fact)

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 1, emptyCheckCalled)
	assert.Equal(t, 1, retryCalled)
}

func TestInstanceEsQueryMissingMappingFallbackCoversAllMissingSortFields(t *testing.T) {
	mock.Init()
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())

	const (
		alias           = "test_alias_multi_missing_fields"
		hostIndex       = "bad_index_missing_host"
		containerIndex  = "bad_index_missing_container"
		goodIndex       = "good_index_multi_missing_fields"
		hostReason      = `{"type":"query_shard_exception","reason":"No mapping found for [host] in order to sort on"}`
		containerReason = `{"type":"query_shard_exception","reason":"No mapping found for [container_name] in order to sort on"}`
	)
	httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+alias, httpmock.NewStringResponder(http.StatusOK, fmt.Sprintf(`{"%s":{},"%s":{},"%s":{}}`, hostIndex, containerIndex, goodIndex)))

	var emptyCheckCalled, retryCalled int
	aliasSearchCalls := 0
	httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+alias+"/_search", func(r *http.Request) (*http.Response, error) {
		aliasSearchCalls++
		if aliasSearchCalls == 1 {
			return httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`{
				"took":1,
				"timed_out":false,
				"_shards":{
					"total":3,
					"successful":1,
					"skipped":0,
					"failed":2,
					"failures":[
						{"shard":0,"index":"%s","reason":%s},
						{"shard":0,"index":"%s","reason":%s}
					]
				},
				"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}
			}`, hostIndex, hostReason, containerIndex, containerReason)), nil
		}
		if aliasSearchCalls == 3 {
			retryCalled++
			assertSearchBodySortHasUnmappedTypes(t, r, map[string]string{
				"host":           "keyword",
				"container_name": "keyword",
			})
			return httpmock.NewStringResponse(http.StatusOK, `{
				"took":1,
				"timed_out":false,
				"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},
				"hits":{"total":{"value":0,"relation":"eq"},"hits":[]},
				"aggregations":{"_value":{"value":1}}
			}`), nil
		}
		emptyCheckCalled++
		assertSearchBodyFiltersIndexes(t, r, hostIndex, containerIndex)
		return httpmock.NewStringResponse(http.StatusOK, `{
			"took":1,
			"timed_out":false,
			"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},
			"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}
		}`), nil
	})

	inst, err := NewInstance(ctx, &InstanceOption{
		Connect: Connect{Address: mock.EsUrl},
		Timeout: time.Minute,
	})
	require.NoError(t, err)

	query := &metadata.Query{
		DB:          alias,
		Field:       "host",
		TimeField:   metadata.TimeField{Name: "dtEventTimeStamp", Type: TimeFieldTypeTime, Unit: "millisecond"},
		StorageType: metadata.ElasticsearchStorageType,
		Orders: metadata.Orders{
			{Name: "host", Ast: false},
			{Name: "container_name", Ast: false},
		},
		Aggregates: metadata.Aggregates{{Name: Count, Field: "dtEventTimeStamp"}},
	}
	fact := NewFormatFactory(ctx).
		WithQuery(query.Field, query.TimeField, time.UnixMilli(1784013830711), time.UnixMilli(1784014730711), function.Millisecond, 0).
		WithFieldMap(metadata.FieldsMap{
			"host":             {FieldType: "keyword"},
			"container_name":   {FieldType: "keyword"},
			"dtEventTimeStamp": {FieldType: TimeFieldTypeTime},
		}).
		WithOrders(query.Orders)

	res, err := inst.esQuery(ctx, &queryOption{
		indexes: []string{alias},
		start:   time.UnixMilli(1784013830711),
		end:     time.UnixMilli(1784014730711),
		query:   query,
		conn:    inst.connect,
	}, fact)

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 1, emptyCheckCalled)
	assert.Equal(t, 1, retryCalled)
}

func TestInstanceEsQueryMissingMappingFallbackAllowsAllEmptyMissingMappingIndexes(t *testing.T) {
	mock.Init()
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())

	const (
		alias    = "test_alias_all_empty_missing_mapping"
		badIndex = "bad_index_all_empty_missing_mapping"
	)
	httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+alias, httpmock.NewStringResponder(http.StatusOK, fmt.Sprintf(`{"%s":{}}`, badIndex)))

	var emptyCheckCalled, retryCalled int
	aliasSearchCalls := 0
	httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+alias+"/_search", func(r *http.Request) (*http.Response, error) {
		aliasSearchCalls++
		if aliasSearchCalls == 1 {
			return httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`{
				"took":1,
				"timed_out":false,
				"_shards":{
					"total":1,
					"successful":0,
					"skipped":0,
					"failed":1,
					"failures":[{"shard":0,"index":"%s","reason":{"type":"query_shard_exception","reason":"No mapping found for [svrname] in order to sort on"}}]
				},
				"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}
			}`, badIndex)), nil
		}
		if aliasSearchCalls == 3 {
			retryCalled++
			assertSearchBodySortHasUnmappedType(t, r, "svrname", "keyword")
			return httpmock.NewStringResponse(http.StatusOK, `{
				"took":1,
				"timed_out":false,
				"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},
				"hits":{"total":{"value":0,"relation":"eq"},"hits":[]},
				"aggregations":{"_value":{"value":0}}
			}`), nil
		}
		emptyCheckCalled++
		assertSearchBodyFiltersIndexes(t, r, badIndex)
		return httpmock.NewStringResponse(http.StatusOK, `{
			"took":1,
			"timed_out":false,
			"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},
			"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}
		}`), nil
	})

	inst, err := NewInstance(ctx, &InstanceOption{
		Connect: Connect{Address: mock.EsUrl},
		Timeout: time.Minute,
	})
	require.NoError(t, err)

	query := &metadata.Query{
		DB:          alias,
		Field:       "svrname",
		TimeField:   metadata.TimeField{Name: "dtEventTimeStamp", Type: TimeFieldTypeTime, Unit: "millisecond"},
		StorageType: metadata.ElasticsearchStorageType,
		Orders:      metadata.Orders{{Name: "svrname", Ast: false}},
		Aggregates:  metadata.Aggregates{{Name: Count, Field: "dtEventTimeStamp"}},
	}
	fact := NewFormatFactory(ctx).
		WithQuery(query.Field, query.TimeField, time.UnixMilli(1784013830711), time.UnixMilli(1784014730711), function.Millisecond, 0).
		WithFieldMap(metadata.FieldsMap{
			"svrname":          {FieldType: "keyword"},
			"dtEventTimeStamp": {FieldType: TimeFieldTypeTime},
		}).
		WithOrders(query.Orders)

	res, err := inst.esQuery(ctx, &queryOption{
		indexes: []string{alias},
		start:   time.UnixMilli(1784013830711),
		end:     time.UnixMilli(1784014730711),
		query:   query,
		conn:    inst.connect,
	}, fact)

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 1, emptyCheckCalled)
	assert.Equal(t, 1, retryCalled)
}

func TestInstanceEsQueryMissingMappingFallbackKeepsOriginalErrorOnMixedShardFailures(t *testing.T) {
	mock.Init()
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())

	const (
		alias      = "test_alias_mixed_failures"
		badIndex   = "bad_index_mixed_missing_mapping"
		otherIndex = "bad_index_mixed_other_failure"
		goodIndex  = "good_index_mixed_failures"
	)

	var indexGetCalled, retryCalled bool
	httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+alias, func(r *http.Request) (*http.Response, error) {
		indexGetCalled = true
		return httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`{"%s":{},"%s":{},"%s":{}}`, badIndex, otherIndex, goodIndex)), nil
	})

	aliasSearchCalls := 0
	httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+alias+"/_search", func(r *http.Request) (*http.Response, error) {
		aliasSearchCalls++
		if aliasSearchCalls > 1 {
			retryCalled = true
			return httpmock.NewStringResponse(http.StatusOK, `{}`), nil
		}
		return httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`{
			"took":1,
			"timed_out":false,
			"_shards":{
				"total":3,
				"successful":1,
				"skipped":0,
				"failed":2,
				"failures":[
					{
						"shard":0,
						"index":"%s",
						"reason":{"type":"query_shard_exception","reason":"No mapping found for [svrname] in order to sort on"}
					},
					{
						"shard":1,
						"index":"%s",
						"reason":{"type":"illegal_argument_exception","reason":"Trying to create too many scroll contexts."}
					}
				]
			},
			"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}
		}`, badIndex, otherIndex)), nil
	})

	inst, err := NewInstance(ctx, &InstanceOption{
		Connect: Connect{Address: mock.EsUrl},
		Timeout: time.Minute,
	})
	require.NoError(t, err)

	query := &metadata.Query{
		DB:          alias,
		Field:       "svrname",
		TimeField:   metadata.TimeField{Name: "dtEventTimeStamp", Type: TimeFieldTypeTime, Unit: "millisecond"},
		StorageType: metadata.ElasticsearchStorageType,
		Orders:      metadata.Orders{{Name: "svrname", Ast: false}},
		Aggregates:  metadata.Aggregates{{Name: Count, Field: "dtEventTimeStamp"}},
	}
	fact := NewFormatFactory(ctx).
		WithQuery(query.Field, query.TimeField, time.UnixMilli(1784013830711), time.UnixMilli(1784014730711), function.Millisecond, 0).
		WithFieldMap(metadata.FieldsMap{
			"svrname":          {FieldType: "keyword"},
			"dtEventTimeStamp": {FieldType: TimeFieldTypeTime},
		}).
		WithOrders(query.Orders)

	res, err := inst.esQuery(ctx, &queryOption{
		indexes: []string{alias},
		start:   time.UnixMilli(1784013830711),
		end:     time.UnixMilli(1784014730711),
		query:   query,
		conn:    inst.connect,
	}, fact)

	require.Error(t, err)
	assert.Nil(t, res)
	assert.Equal(t, 1, aliasSearchCalls)
	assert.False(t, indexGetCalled)
	assert.False(t, retryCalled)
	assert.Contains(t, err.Error(), "No mapping found for [svrname] in order to sort on")
}

func TestInstanceEsQueryMissingMappingFallbackUsesGetMappingWhenIndexGetFails(t *testing.T) {
	mock.Init()
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())

	const (
		alias     = "test_alias_get_mapping_fallback"
		badIndex  = "bad_get_mapping_fallback"
		goodIndex = "good_get_mapping_fallback"
	)
	httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+alias, httpmock.NewStringResponder(http.StatusInternalServerError, `{"error":{"reason":"index get unsupported"}}`))
	httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+alias+"/_mapping/", httpmock.NewStringResponder(http.StatusOK, fmt.Sprintf(`{"%s":{},"%s":{}}`, badIndex, goodIndex)))

	var emptyCheckCalled, retryCalled bool
	aliasSearchCalls := 0
	httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+alias+"/_search", func(r *http.Request) (*http.Response, error) {
		aliasSearchCalls++
		if aliasSearchCalls == 1 {
			return httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`{
				"took":1,
				"timed_out":false,
				"_shards":{
					"total":2,
					"successful":1,
					"skipped":0,
					"failed":1,
					"failures":[{"shard":0,"index":"%s","reason":{"type":"query_shard_exception","reason":"No mapping found for [svrname] in order to sort on"}}]
				},
				"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}
			}`, badIndex)), nil
		}
		if aliasSearchCalls == 3 {
			retryCalled = true
			assertSearchBodySortHasUnmappedType(t, r, "svrname", "keyword")
			return httpmock.NewStringResponse(http.StatusOK, `{
				"took":1,
				"timed_out":false,
				"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},
				"hits":{"total":{"value":0,"relation":"eq"},"hits":[]},
				"aggregations":{"_value":{"value":1}}
			}`), nil
		}
		emptyCheckCalled = true
		assertSearchBodyFiltersIndexes(t, r, badIndex)
		return httpmock.NewStringResponse(http.StatusOK, `{
			"took":1,
			"timed_out":false,
			"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},
			"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}
		}`), nil
	})

	inst, err := NewInstance(ctx, &InstanceOption{
		Connect: Connect{Address: mock.EsUrl},
		Timeout: time.Minute,
	})
	require.NoError(t, err)

	query := &metadata.Query{
		DB:          alias,
		Field:       "svrname",
		TimeField:   metadata.TimeField{Name: "dtEventTimeStamp", Type: TimeFieldTypeTime, Unit: "millisecond"},
		StorageType: metadata.ElasticsearchStorageType,
		Orders:      metadata.Orders{{Name: "svrname", Ast: false}},
		Aggregates:  metadata.Aggregates{{Name: Count, Field: "dtEventTimeStamp"}},
	}
	fact := NewFormatFactory(ctx).
		WithQuery(query.Field, query.TimeField, time.UnixMilli(1784013830711), time.UnixMilli(1784014730711), function.Millisecond, 0).
		WithFieldMap(metadata.FieldsMap{
			"svrname":          {FieldType: "keyword"},
			"dtEventTimeStamp": {FieldType: TimeFieldTypeTime},
		}).
		WithOrders(query.Orders)

	res, err := inst.esQuery(ctx, &queryOption{
		indexes: []string{alias},
		start:   time.UnixMilli(1784013830711),
		end:     time.UnixMilli(1784014730711),
		query:   query,
		conn:    inst.connect,
	}, fact)

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, emptyCheckCalled)
	assert.True(t, retryCalled)
}

func TestInstanceEsQueryMissingMappingFallbackKeepsOriginalErrorWhenIndexHasData(t *testing.T) {
	mock.Init()
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())

	const (
		alias     = "test_alias_has_data"
		badIndex  = "bad_index_has_data"
		goodIndex = "good_index_has_data"
	)
	httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+alias, httpmock.NewStringResponder(http.StatusOK, fmt.Sprintf(`{"%s":{},"%s":{}}`, badIndex, goodIndex)))

	var retryCalled bool
	aliasSearchCalls := 0
	httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+alias+"/_search", func(r *http.Request) (*http.Response, error) {
		aliasSearchCalls++
		if aliasSearchCalls == 1 {
			return httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`{
			"took":1,
			"timed_out":false,
			"_shards":{
				"total":2,
				"successful":1,
				"skipped":0,
				"failed":1,
				"failures":[
					{
						"shard":0,
						"index":"%s",
						"reason":{"type":"query_shard_exception","reason":"No mapping found for [svrname] in order to sort on"}
					}
				]
			},
				"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}
		}`, badIndex)), nil
		}
		assertSearchBodyFiltersIndexes(t, r, badIndex)
		return httpmock.NewStringResponse(http.StatusOK, `{
		"took":1,
		"timed_out":false,
		"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},
		"hits":{"total":{"value":3,"relation":"eq"},"hits":[]}
	}`), nil
	})
	httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+alias+"%2C-"+badIndex+"/_search", func(r *http.Request) (*http.Response, error) {
		retryCalled = true
		return httpmock.NewStringResponse(http.StatusOK, `{}`), nil
	})

	inst, err := NewInstance(ctx, &InstanceOption{
		Connect: Connect{Address: mock.EsUrl},
		Timeout: time.Minute,
	})
	require.NoError(t, err)

	query := &metadata.Query{
		DB:          alias,
		Field:       "svrname",
		TimeField:   metadata.TimeField{Name: "dtEventTimeStamp", Type: TimeFieldTypeTime, Unit: "millisecond"},
		StorageType: metadata.ElasticsearchStorageType,
		Orders:      metadata.Orders{{Name: "svrname", Ast: false}},
		Aggregates:  metadata.Aggregates{{Name: Count, Field: "dtEventTimeStamp"}},
	}
	fact := NewFormatFactory(ctx).
		WithQuery(query.Field, query.TimeField, time.UnixMilli(1784013830711), time.UnixMilli(1784014730711), function.Millisecond, 0).
		WithFieldMap(metadata.FieldsMap{
			"svrname":          {FieldType: "keyword"},
			"dtEventTimeStamp": {FieldType: TimeFieldTypeTime},
		}).
		WithOrders(query.Orders)

	res, err := inst.esQuery(ctx, &queryOption{
		indexes: []string{alias},
		start:   time.UnixMilli(1784013830711),
		end:     time.UnixMilli(1784014730711),
		query:   query,
		conn:    inst.connect,
	}, fact)

	require.Error(t, err)
	assert.Nil(t, res)
	assert.False(t, retryCalled)
	assert.Contains(t, err.Error(), "No mapping found for [svrname] in order to sort on")
}

func TestInstanceEsQueryMissingMappingFallbackKeepsOriginalErrorOnGuardFailures(t *testing.T) {
	tests := []struct {
		name             string
		alias            string
		badIndex         string
		goodIndex        string
		resultTableOpt   *metadata.ResultTableOption
		emptyCheckStatus int
		emptyCheckBody   string
		registerFallback func(alias, badIndex, goodIndex string, retryCalled *bool, indexGetCalled *bool)
	}{
		{
			name:      "index get fails",
			alias:     "test_alias_index_get_fails",
			badIndex:  "bad_index_get_fails",
			goodIndex: "good_index_get_fails",
			registerFallback: func(alias, badIndex, goodIndex string, retryCalled *bool, indexGetCalled *bool) {
				httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+alias, func(r *http.Request) (*http.Response, error) {
					*indexGetCalled = true
					return httpmock.NewStringResponse(http.StatusInternalServerError, `{"error":{"reason":"index get failed"}}`), nil
				})
				httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+goodIndex+"/_search", func(r *http.Request) (*http.Response, error) {
					*retryCalled = true
					return httpmock.NewStringResponse(http.StatusOK, `{}`), nil
				})
			},
		},
		{
			name:             "empty check fails",
			alias:            "test_alias_empty_check_fails",
			badIndex:         "bad_empty_check_fails",
			goodIndex:        "good_empty_check_fails",
			emptyCheckStatus: http.StatusInternalServerError,
			emptyCheckBody:   `{"error":{"reason":"empty check failed"}}`,
			registerFallback: func(alias, badIndex, goodIndex string, retryCalled *bool, indexGetCalled *bool) {
				httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+alias, func(r *http.Request) (*http.Response, error) {
					*indexGetCalled = true
					return httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`{"%s":{},"%s":{}}`, badIndex, goodIndex)), nil
				})
				httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+alias+"%2C-"+badIndex+"/_search", func(r *http.Request) (*http.Response, error) {
					*retryCalled = true
					return httpmock.NewStringResponse(http.StatusOK, `{}`), nil
				})
			},
		},
		{
			name:             "empty check shard failure",
			alias:            "test_alias_empty_check_shard_failure",
			badIndex:         "bad_empty_check_shard_failure",
			goodIndex:        "good_empty_check_shard_failure",
			emptyCheckStatus: http.StatusOK,
			emptyCheckBody: `{
				"took":1,
				"timed_out":false,
				"_shards":{"total":2,"successful":1,"skipped":0,"failed":1},
				"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}
			}`,
			registerFallback: func(alias, badIndex, goodIndex string, retryCalled *bool, indexGetCalled *bool) {
				httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+alias, func(r *http.Request) (*http.Response, error) {
					*indexGetCalled = true
					return httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`{"%s":{},"%s":{}}`, badIndex, goodIndex)), nil
				})
				httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+alias+"%2C-"+badIndex+"/_search", func(r *http.Request) (*http.Response, error) {
					*retryCalled = true
					return httpmock.NewStringResponse(http.StatusOK, `{}`), nil
				})
			},
		},
		{
			name:           "search after skips fallback",
			alias:          "test_alias_search_after",
			badIndex:       "bad_search_after",
			goodIndex:      "good_search_after",
			resultTableOpt: &metadata.ResultTableOption{SearchAfter: []any{"next"}},
			registerFallback: func(alias, badIndex, goodIndex string, retryCalled *bool, indexGetCalled *bool) {
				httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+alias, func(r *http.Request) (*http.Response, error) {
					*indexGetCalled = true
					return httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`{"%s":{},"%s":{}}`, badIndex, goodIndex)), nil
				})
				httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+alias+"%2C-"+badIndex+"/_search", func(r *http.Request) (*http.Response, error) {
					*retryCalled = true
					return httpmock.NewStringResponse(http.StatusOK, `{}`), nil
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock.Init()
			metadata.InitMetadata()
			ctx := metadata.InitHashID(context.Background())

			aliasSearchCalls := 0
			httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+tt.alias+"/_search", func(r *http.Request) (*http.Response, error) {
				aliasSearchCalls++
				if aliasSearchCalls == 1 {
					return httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`{
					"took":1,
					"timed_out":false,
					"_shards":{
						"total":2,
						"successful":1,
						"skipped":0,
						"failed":1,
						"failures":[
							{
								"shard":0,
								"index":"%s",
								"reason":{"type":"query_shard_exception","reason":"No mapping found for [svrname] in order to sort on"}
							}
						]
					},
					"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}
				}`, tt.badIndex)), nil
				}
				assertSearchBodyFiltersIndexes(t, r, tt.badIndex)
				return httpmock.NewStringResponse(tt.emptyCheckStatus, tt.emptyCheckBody), nil
			})

			var retryCalled, indexGetCalled bool
			tt.registerFallback(tt.alias, tt.badIndex, tt.goodIndex, &retryCalled, &indexGetCalled)

			inst, err := NewInstance(ctx, &InstanceOption{
				Connect: Connect{Address: mock.EsUrl},
				Timeout: time.Minute,
			})
			require.NoError(t, err)

			query := &metadata.Query{
				DB:                tt.alias,
				Field:             "svrname",
				TimeField:         metadata.TimeField{Name: "dtEventTimeStamp", Type: TimeFieldTypeTime, Unit: "millisecond"},
				StorageType:       metadata.ElasticsearchStorageType,
				Orders:            metadata.Orders{{Name: "svrname", Ast: false}},
				Aggregates:        metadata.Aggregates{{Name: Count, Field: "dtEventTimeStamp"}},
				ResultTableOption: tt.resultTableOpt,
			}
			fact := NewFormatFactory(ctx).
				WithQuery(query.Field, query.TimeField, time.UnixMilli(1784013830711), time.UnixMilli(1784014730711), function.Millisecond, 0).
				WithFieldMap(metadata.FieldsMap{
					"svrname":          {FieldType: "keyword"},
					"dtEventTimeStamp": {FieldType: TimeFieldTypeTime},
				}).
				WithOrders(query.Orders)

			res, err := inst.esQuery(ctx, &queryOption{
				indexes: []string{tt.alias},
				start:   time.UnixMilli(1784013830711),
				end:     time.UnixMilli(1784014730711),
				query:   query,
				conn:    inst.connect,
			}, fact)

			require.Error(t, err)
			assert.Nil(t, res)
			assert.False(t, retryCalled)
			assert.Contains(t, err.Error(), "No mapping found for [svrname] in order to sort on")
			if tt.resultTableOpt != nil && len(tt.resultTableOpt.SearchAfter) > 0 {
				assert.False(t, indexGetCalled)
			}
		})
	}
}

func TestMissingMappingSortFallbackHelpers(t *testing.T) {
	t.Run("extracts missing mapping sort failure", func(t *testing.T) {
		failures := missingMappingSortFailures(nil, &elastic.SearchResult{
			Shards: &elastic.ShardsInfo{
				Failures: []*elastic.ShardOperationFailedException{
					{
						Index: "index-1",
						Reason: map[string]any{
							"type":   "query_shard_exception",
							"reason": "No mapping found for [svrname] in order to sort on",
						},
					},
				},
			},
		})
		require.Len(t, failures, 1)
		assert.Equal(t, "index-1", failures[0].Index)
		assert.Equal(t, "svrname", failures[0].Field)
	})

	t.Run("ignores non mapping sort failure", func(t *testing.T) {
		failures := missingMappingSortFailures(nil, &elastic.SearchResult{
			Shards: &elastic.ShardsInfo{
				Failures: []*elastic.ShardOperationFailedException{
					{
						Index: "index-1",
						Reason: map[string]any{
							"type":   "exception",
							"reason": "Trying to create too many scroll contexts.",
						},
					},
				},
			},
		})
		assert.Empty(t, failures)
	})

	t.Run("strict extraction rejects mixed failures", func(t *testing.T) {
		failures, ok := allMissingMappingSortFailures(nil, &elastic.SearchResult{
			Shards: &elastic.ShardsInfo{
				Failures: []*elastic.ShardOperationFailedException{
					{
						Index: "index-1",
						Reason: map[string]any{
							"type":   "query_shard_exception",
							"reason": "No mapping found for [svrname] in order to sort on",
						},
					},
					{
						Index: "index-2",
						Reason: map[string]any{
							"type":   "illegal_argument_exception",
							"reason": "Trying to create too many scroll contexts.",
						},
					},
				},
			},
		})
		assert.False(t, ok)
		require.Len(t, failures, 1)
		assert.Equal(t, "index-1", failures[0].Index)
	})

	t.Run("extracts from elastic error failed shards", func(t *testing.T) {
		failures := missingMappingSortFailures(&elastic.Error{
			Details: &elastic.ErrorDetails{
				FailedShards: []map[string]any{
					{
						"index": "index-2",
						"reason": map[string]any{
							"type":   "query_shard_exception",
							"reason": "No mapping found for [host] in order to sort on",
						},
					},
				},
			},
		}, nil)
		require.Len(t, failures, 1)
		assert.Equal(t, "index-2", failures[0].Index)
		assert.Equal(t, "host", failures[0].Field)
	})

	t.Run("extracts from search result error failed shards", func(t *testing.T) {
		failures := missingMappingSortFailures(nil, &elastic.SearchResult{
			Status: http.StatusBadRequest,
			Error: &elastic.ErrorDetails{
				FailedShards: []map[string]any{
					{
						"index": "index-3",
						"reason": map[string]any{
							"type":   "query_shard_exception",
							"reason": "No mapping found for [container_name] in order to sort on",
						},
					},
				},
			},
		})
		require.Len(t, failures, 1)
		assert.Equal(t, "index-3", failures[0].Index)
		assert.Equal(t, "container_name", failures[0].Field)
	})

	t.Run("fallback disabled for scroll and search after", func(t *testing.T) {
		assert.False(t, canFallbackMissingMappingQuery(&metadata.Query{Scroll: "5m"}))
		assert.False(t, canFallbackMissingMappingQuery(&metadata.Query{
			ResultTableOption: &metadata.ResultTableOption{ScrollID: "scroll-id"},
		}))
		assert.False(t, canFallbackMissingMappingQuery(&metadata.Query{
			ResultTableOption: &metadata.ResultTableOption{SearchAfter: []any{"next"}},
		}))
		assert.True(t, canFallbackMissingMappingQuery(&metadata.Query{}))
	})
}

func TestRecordESQueryShards(t *testing.T) {
	log.InitTestLogger()

	t.Run("records shard counters on healthy response", func(t *testing.T) {
		rec := setupESTraceRecorder(t)
		_, span := uqtrace.NewSpan(context.Background(), "test-span")

		recordESQueryShards(context.Background(), span, &queryOption{indexes: []string{"index-1"}}, &elastic.SearchResult{
			TimedOut: false,
			Shards: &elastic.ShardsInfo{
				Total:      4,
				Successful: 4,
				Failed:     0,
				Skipped:    1,
			},
		})
		var err error
		span.End(&err)

		attrs := endedSpanAttrs(t, rec)
		timedOut, ok := esSpanAttrBool(attrs, "timed_out")
		require.True(t, ok)
		assert.False(t, timedOut)
		shardsTotal, ok := esSpanAttrInt(attrs, "shards_total")
		require.True(t, ok)
		assert.Equal(t, int64(4), shardsTotal)
		shardsSkipped, ok := esSpanAttrInt(attrs, "shards_skipped")
		require.True(t, ok)
		assert.Equal(t, int64(1), shardsSkipped)
		_, ok = esSpanAttrInt(attrs, "shards_failures_count")
		assert.False(t, ok)
		_, ok = esSpanAttrString(attrs, "shards_failures_sample")
		assert.False(t, ok)
	})

	t.Run("records bounded failure sample", func(t *testing.T) {
		rec := setupESTraceRecorder(t)
		_, span := uqtrace.NewSpan(context.Background(), "test-span")
		longReason := strings.Repeat("x", esShardFailureReasonMaxLength+64)

		recordESQueryShards(context.Background(), span, &queryOption{indexes: []string{"index-*"}}, &elastic.SearchResult{
			Shards: &elastic.ShardsInfo{
				Total:      5,
				Successful: 1,
				Failed:     4,
				Failures: []*elastic.ShardOperationFailedException{
					{Shard: 1, Index: "index-1", Status: "INTERNAL_SERVER_ERROR", Reason: map[string]any{"type": "illegal_argument_exception", "reason": longReason}},
					{Shard: 2, Index: "index-2", Reason: map[string]any{"reason": "second"}},
					{Shard: 3, Index: "index-3", Reason: map[string]any{"reason": "third"}},
					{Shard: 4, Index: "index-4", Reason: map[string]any{"reason": "fourth"}},
				},
			},
		})
		var err error
		span.End(&err)

		attrs := endedSpanAttrs(t, rec)
		shardsFailed, ok := esSpanAttrInt(attrs, "shards_failed")
		require.True(t, ok)
		assert.Equal(t, int64(4), shardsFailed)
		failuresCount, ok := esSpanAttrInt(attrs, "shards_failures_count")
		require.True(t, ok)
		assert.Equal(t, int64(4), failuresCount)
		failuresSampleJson, ok := esSpanAttrString(attrs, "shards_failures_sample")
		require.True(t, ok)
		assert.NotContains(t, failuresSampleJson, "index-4")

		var failuresSample []esShardFailureSample
		require.NoError(t, stdjson.Unmarshal([]byte(failuresSampleJson), &failuresSample))
		require.Len(t, failuresSample, esShardFailureSampleLimit)
		assert.Equal(t, 1, failuresSample[0].Shard)
		assert.Equal(t, "index-1", failuresSample[0].Index)
		assert.Equal(t, "INTERNAL_SERVER_ERROR", failuresSample[0].Status)
		assert.LessOrEqual(t, len([]rune(failuresSample[0].Reason)), esShardFailureReasonMaxLength)
	})

	t.Run("records timeout without failures", func(t *testing.T) {
		rec := setupESTraceRecorder(t)
		_, span := uqtrace.NewSpan(context.Background(), "test-span")

		recordESQueryShards(context.Background(), span, &queryOption{indexes: []string{"index-1"}}, &elastic.SearchResult{
			TimedOut: true,
			Shards: &elastic.ShardsInfo{
				Total:      2,
				Successful: 2,
			},
		})
		var err error
		span.End(&err)

		attrs := endedSpanAttrs(t, rec)
		timedOut, ok := esSpanAttrBool(attrs, "timed_out")
		require.True(t, ok)
		assert.True(t, timedOut)
		failuresCount, ok := esSpanAttrInt(attrs, "shards_failures_count")
		require.True(t, ok)
		assert.Equal(t, int64(0), failuresCount)
		_, ok = esSpanAttrString(attrs, "shards_failures_sample")
		assert.False(t, ok)
	})

	t.Run("records fallback retry shard counters with prefix", func(t *testing.T) {
		rec := setupESTraceRecorder(t)
		_, span := uqtrace.NewSpan(context.Background(), "test-span")

		// 首次响应有分片失败，但 retry 随后成功。下面的断言用于确认这两个分片状态
		// 在 trace attributes 中仍然可以区分。
		recordESQueryShards(context.Background(), span, &queryOption{indexes: []string{"alias"}}, &elastic.SearchResult{
			Shards: &elastic.ShardsInfo{
				Total:      2,
				Successful: 1,
				Failed:     1,
				Failures: []*elastic.ShardOperationFailedException{
					{Shard: 0, Index: "bad-index", Reason: map[string]any{"reason": "No mapping found for [svrname] in order to sort on"}},
				},
			},
		})
		recordESQueryShardsWithPrefix(context.Background(), span, &queryOption{indexes: []string{"alias", "-bad-index"}}, &elastic.SearchResult{
			Shards: &elastic.ShardsInfo{
				Total:      1,
				Successful: 1,
				Failed:     0,
			},
		}, "fallback_retry_")
		var err error
		span.End(&err)

		attrs := endedSpanAttrs(t, rec)
		shardsFailed, ok := esSpanAttrInt(attrs, "shards_failed")
		require.True(t, ok)
		assert.Equal(t, int64(1), shardsFailed)
		failuresCount, ok := esSpanAttrInt(attrs, "shards_failures_count")
		require.True(t, ok)
		assert.Equal(t, int64(1), failuresCount)
		retryShardsFailed, ok := esSpanAttrInt(attrs, "fallback_retry_shards_failed")
		require.True(t, ok)
		assert.Equal(t, int64(0), retryShardsFailed)
		_, ok = esSpanAttrInt(attrs, "fallback_retry_shards_failures_count")
		assert.False(t, ok)
	})

	t.Run("handles nil result and nil shards", func(t *testing.T) {
		rec := setupESTraceRecorder(t)
		_, span := uqtrace.NewSpan(context.Background(), "test-span")
		assert.NotPanics(t, func() {
			recordESQueryShards(context.Background(), span, nil, nil)
		})
		var err error
		span.End(&err)
		assert.Empty(t, endedSpanAttrs(t, rec))

		rec = setupESTraceRecorder(t)
		_, span = uqtrace.NewSpan(context.Background(), "test-span")
		assert.NotPanics(t, func() {
			recordESQueryShards(context.Background(), span, nil, &elastic.SearchResult{TimedOut: true})
		})
		span.End(&err)

		attrs := endedSpanAttrs(t, rec)
		timedOut, ok := esSpanAttrBool(attrs, "timed_out")
		require.True(t, ok)
		assert.True(t, timedOut)
		failuresCount, ok := esSpanAttrInt(attrs, "shards_failures_count")
		require.True(t, ok)
		assert.Equal(t, int64(0), failuresCount)
	})
}

func setupESTraceRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prevTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
	})
	return rec
}

func endedSpanAttrs(t *testing.T, rec *tracetest.SpanRecorder) []attribute.KeyValue {
	t.Helper()
	ended := rec.Ended()
	require.Len(t, ended, 1)
	return ended[0].Attributes()
}

func esSpanAttrBool(attrs []attribute.KeyValue, key string) (bool, bool) {
	for _, kv := range attrs {
		if string(kv.Key) == key {
			return kv.Value.AsBool(), true
		}
	}
	return false, false
}

func esSpanAttrInt(attrs []attribute.KeyValue, key string) (int64, bool) {
	for _, kv := range attrs {
		if string(kv.Key) == key {
			return kv.Value.AsInt64(), true
		}
	}
	return 0, false
}

func esSpanAttrString(attrs []attribute.KeyValue, key string) (string, bool) {
	for _, kv := range attrs {
		if string(kv.Key) == key {
			return kv.Value.AsString(), true
		}
	}
	return "", false
}

func assertSearchBodyFiltersIndexes(t *testing.T, r *http.Request, indexes ...string) {
	t.Helper()
	var body any
	require.NoError(t, stdjson.NewDecoder(r.Body).Decode(&body))
	bodyBytes, err := stdjson.Marshal(body)
	require.NoError(t, err)
	bodyString := string(bodyBytes)
	assert.Contains(t, bodyString, `"terms":{"_index":[`)
	for _, index := range indexes {
		assert.Contains(t, bodyString, `"`+index+`"`)
	}
}

func assertSearchBodySortHasUnmappedType(t *testing.T, r *http.Request, field, unmappedType string) string {
	t.Helper()
	return assertSearchBodySortHasUnmappedTypes(t, r, map[string]string{field: unmappedType})
}

func assertSearchBodySortHasUnmappedTypes(t *testing.T, r *http.Request, unmappedTypes map[string]string) string {
	t.Helper()
	var body any
	require.NoError(t, stdjson.NewDecoder(r.Body).Decode(&body))
	bodyBytes, err := stdjson.Marshal(body)
	require.NoError(t, err)
	bodyString := string(bodyBytes)
	for field, unmappedType := range unmappedTypes {
		assert.Contains(t, bodyString, `"`+field+`":{"order":`)
		assert.Contains(t, bodyString, `"unmapped_type":"`+unmappedType+`"`)
	}
	assert.NotContains(t, r.URL.EscapedPath(), "%2C-")
	return bodyString
}
