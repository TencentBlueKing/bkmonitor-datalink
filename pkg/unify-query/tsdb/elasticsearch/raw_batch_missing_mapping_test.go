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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
)

func TestRawBatchMissingMappingFallbackRetriesOnlyFailedChild(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	fallbackAttemptedBefore := rawBatchMetricEventValue(
		t,
		metric.QueryRawESBatchEventFallbackAttempted,
	)
	fallbackRecoveredBefore := rawBatchMetricEventValue(
		t,
		metric.QueryRawESBatchEventFallbackRecovered,
	)
	var multiSearchCalls, firstAliasSearches, secondAliasSearches atomic.Int32

	const (
		firstAlias  = "batch_fallback_first"
		secondAlias = "batch_fallback_second"
		badIndex    = "batch_fallback_bad"
		goodIndex   = "batch_fallback_good"
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/"+firstAlias:
			_, _ = io.WriteString(writer, fmt.Sprintf(
				`{"%s":{"aliases":{"%s":{}},"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"}}}},"%s":{"aliases":{"%s":{}},"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"},"svrname":{"type":"keyword"}}}}}`,
				badIndex,
				firstAlias,
				goodIndex,
				firstAlias,
			))
		case request.Method == http.MethodGet && request.URL.Path == "/"+secondAlias:
			_, _ = io.WriteString(
				writer,
				fmt.Sprintf(
					`{"%s":{"aliases":{"%s":{}},"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"},"svrname":{"type":"keyword"}}}}}`,
					secondAlias,
					secondAlias,
				),
			)
		case request.Method == http.MethodGet && request.URL.Path == "/_msearch":
			multiSearchCalls.Add(1)
			_, _ = io.WriteString(writer, fmt.Sprintf(
				`{"responses":[{"status":200,"_shards":{"total":2,"successful":1,"failed":1,"failures":[{"shard":0,"index":"%s","reason":{"type":"query_shard_exception","reason":"No mapping found for [svrname] in order to sort on"}}]},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}},{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"%s","_id":"second","_source":{"dtEventTimeStamp":2,"svrname":"second"}}]}}]}`,
				badIndex,
				secondAlias,
			))
		case request.Method == http.MethodPost && request.URL.Path == "/"+firstAlias+"/_search":
			call := firstAliasSearches.Add(1)
			if call == 1 {
				assertSearchBodyFiltersIndexes(t, request, badIndex)
				_, _ = io.WriteString(
					writer,
					`{"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
				)
				return
			}
			assertSearchBodySortHasUnmappedType(t, request, "svrname", "keyword")
			_, _ = io.WriteString(
				writer,
				fmt.Sprintf(
					`{"_shards":{"total":2,"successful":2,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"%s","_id":"first","_source":{"dtEventTimeStamp":1,"svrname":"first"}}]}}`,
					goodIndex,
				),
			)
		case request.Method == http.MethodPost && request.URL.Path == "/"+secondAlias+"/_search":
			secondAliasSearches.Add(1)
			http.Error(writer, "unexpected single search", http.StatusInternalServerError)
		default:
			http.Error(writer, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	instance := rawBatchInstance(t, Connect{Address: server.URL})
	prepare := func(alias, tableID string) *PreparedRawQuery {
		prepared, err := instance.PrepareRawQuery(
			ctx,
			&metadata.Query{
				DB:          alias,
				Field:       "svrname",
				TableID:     tableID,
				StorageID:   "3",
				StorageType: metadata.ElasticsearchStorageType,
				Size:        1,
				TimeField: metadata.TimeField{
					Name: "dtEventTimeStamp",
					Type: TimeFieldTypeTime,
					Unit: "millisecond",
				},
				Orders: metadata.Orders{{Name: "svrname", Ast: false}},
			},
			time.UnixMilli(1),
			time.UnixMilli(2),
			nil,
		)
		require.NoError(t, err)
		return prepared
	}
	first := prepare(firstAlias, "result.table.first")
	second := prepare(secondAlias, "result.table.second")
	firstFingerprint, err := PreparedRawQueryFingerprint(first)
	require.NoError(t, err)
	secondFingerprint, err := PreparedRawQueryFingerprint(second)
	require.NoError(t, err)
	require.Equal(t, firstFingerprint, secondFingerprint)

	batches, oversized, err := PackRawBatchMembers([]RawBatchMember{
		{Ordinal: 0, Prepared: first},
		{Ordinal: 1, Prepared: second},
	}, 16, 1<<20)
	require.NoError(t, err)
	require.Empty(t, oversized)
	require.Len(t, batches, 1)

	results, err := instance.ExecuteRawBatch(ctx, batches[0], 4)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.NoError(t, results[0].Err)
	require.NoError(t, results[1].Err)
	assert.True(t, results[0].FallbackAttempted)
	assert.True(t, results[0].FallbackSucceeded)
	assert.False(t, results[1].FallbackAttempted)
	assert.False(t, results[1].FallbackSucceeded)
	assert.Equal(t, "first", results[0].Rows[0]["svrname"])
	assert.Equal(t, "second", results[1].Rows[0]["svrname"])
	assert.EqualValues(t, 1, multiSearchCalls.Load())
	assert.EqualValues(t, 2, firstAliasSearches.Load())
	assert.Zero(t, secondAliasSearches.Load())
	assert.Equal(
		t,
		fallbackAttemptedBefore+1,
		rawBatchMetricEventValue(t, metric.QueryRawESBatchEventFallbackAttempted),
	)
	assert.Equal(
		t,
		fallbackRecoveredBefore+1,
		rawBatchMetricEventValue(t, metric.QueryRawESBatchEventFallbackRecovered),
	)
}

func TestRawBatchMissingMappingFallbackRunsFailedChildrenSequentially(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	var (
		multiSearchCalls     atomic.Int32
		firstAliasSearches   atomic.Int32
		secondAliasSearches  atomic.Int32
		successAliasSearches atomic.Int32
		activeRequests       atomic.Int32
		maxInflight          atomic.Int32
		searchOrderMu        sync.Mutex
		searchOrder          []string
	)

	const (
		firstAlias   = "batch_fallback_serial_first"
		firstBad     = "batch_fallback_serial_first_bad"
		firstGood    = "batch_fallback_serial_first_good"
		secondAlias  = "batch_fallback_serial_second"
		secondBad    = "batch_fallback_serial_second_bad"
		secondGood   = "batch_fallback_serial_second_good"
		successAlias = "batch_fallback_serial_success"
	)
	recordRequest := func() func() {
		active := activeRequests.Add(1)
		for {
			previous := maxInflight.Load()
			if active <= previous || maxInflight.CompareAndSwap(previous, active) {
				break
			}
		}
		// Make concurrent child fallbacks overlap if the implementation ever
		// starts them in parallel, so maxInflight is a meaningful assertion.
		time.Sleep(10 * time.Millisecond)
		return func() {
			activeRequests.Add(-1)
		}
	}
	recordSearch := func(alias, phase string) {
		searchOrderMu.Lock()
		defer searchOrderMu.Unlock()
		searchOrder = append(searchOrder, alias+":"+phase)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer recordRequest()()
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/"+firstAlias:
			_, _ = io.WriteString(writer, fmt.Sprintf(
				`{"%s":{"aliases":{"%s":{}},"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"}}}},"%s":{"aliases":{"%s":{}},"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"},"svrname":{"type":"keyword"}}}}}`,
				firstBad,
				firstAlias,
				firstGood,
				firstAlias,
			))
		case request.Method == http.MethodGet && request.URL.Path == "/"+secondAlias:
			_, _ = io.WriteString(writer, fmt.Sprintf(
				`{"%s":{"aliases":{"%s":{}},"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"}}}},"%s":{"aliases":{"%s":{}},"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"},"svrname":{"type":"keyword"}}}}}`,
				secondBad,
				secondAlias,
				secondGood,
				secondAlias,
			))
		case request.Method == http.MethodGet && request.URL.Path == "/"+successAlias:
			_, _ = io.WriteString(writer, fmt.Sprintf(
				`{"%s":{"aliases":{"%s":{}},"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"},"svrname":{"type":"keyword"}}}}}`,
				successAlias,
				successAlias,
			))
		case request.Method == http.MethodGet && request.URL.Path == "/_msearch":
			multiSearchCalls.Add(1)
			_, _ = io.WriteString(writer, fmt.Sprintf(
				`{"responses":[{"status":200,"_shards":{"total":2,"successful":1,"failed":1,"failures":[{"shard":0,"index":"%s","reason":{"type":"query_shard_exception","reason":"No mapping found for [svrname] in order to sort on"}}]},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}},{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"%s","_id":"success","_source":{"dtEventTimeStamp":2,"svrname":"success"}}]}},{"status":200,"_shards":{"total":2,"successful":1,"failed":1,"failures":[{"shard":0,"index":"%s","reason":{"type":"query_shard_exception","reason":"No mapping found for [svrname] in order to sort on"}}]},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}]}`,
				firstBad,
				successAlias,
				secondBad,
			))
		case request.Method == http.MethodPost && request.URL.Path == "/"+firstAlias+"/_search":
			call := firstAliasSearches.Add(1)
			if call == 1 {
				recordSearch(firstAlias, "empty-check")
				assertSearchBodyFiltersIndexes(t, request, firstBad)
				_, _ = io.WriteString(
					writer,
					`{"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
				)
				return
			}
			recordSearch(firstAlias, "retry")
			assertSearchBodySortHasUnmappedType(t, request, "svrname", "keyword")
			_, _ = io.WriteString(writer, fmt.Sprintf(
				`{"_shards":{"total":2,"successful":2,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"%s","_id":"first","_source":{"dtEventTimeStamp":1,"svrname":"first"}}]}}`,
				firstGood,
			))
		case request.Method == http.MethodPost && request.URL.Path == "/"+secondAlias+"/_search":
			call := secondAliasSearches.Add(1)
			if call == 1 {
				recordSearch(secondAlias, "empty-check")
				assertSearchBodyFiltersIndexes(t, request, secondBad)
				_, _ = io.WriteString(
					writer,
					`{"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
				)
				return
			}
			recordSearch(secondAlias, "retry")
			assertSearchBodySortHasUnmappedType(t, request, "svrname", "keyword")
			_, _ = io.WriteString(writer, fmt.Sprintf(
				`{"_shards":{"total":2,"successful":2,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"%s","_id":"second","_source":{"dtEventTimeStamp":3,"svrname":"second"}}]}}`,
				secondGood,
			))
		case request.Method == http.MethodPost && request.URL.Path == "/"+successAlias+"/_search":
			successAliasSearches.Add(1)
			http.Error(writer, "unexpected successful sibling replay", http.StatusInternalServerError)
		default:
			http.Error(writer, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	instance := rawBatchInstance(t, Connect{Address: server.URL})
	prepare := func(alias, tableID string) *PreparedRawQuery {
		prepared, err := instance.PrepareRawQuery(
			ctx,
			&metadata.Query{
				DB:          alias,
				Field:       "svrname",
				TableID:     tableID,
				StorageID:   "3",
				StorageType: metadata.ElasticsearchStorageType,
				Size:        1,
				TimeField: metadata.TimeField{
					Name: "dtEventTimeStamp",
					Type: TimeFieldTypeTime,
					Unit: "millisecond",
				},
				Orders: metadata.Orders{{Name: "svrname", Ast: false}},
			},
			time.UnixMilli(1),
			time.UnixMilli(2),
			nil,
		)
		require.NoError(t, err)
		return prepared
	}
	first := prepare(firstAlias, "result.table.first")
	success := prepare(successAlias, "result.table.success")
	second := prepare(secondAlias, "result.table.second")
	require.Zero(t, activeRequests.Load())
	maxInflight.Store(0)

	firstFingerprint, err := PreparedRawQueryFingerprint(first)
	require.NoError(t, err)
	successFingerprint, err := PreparedRawQueryFingerprint(success)
	require.NoError(t, err)
	secondFingerprint, err := PreparedRawQueryFingerprint(second)
	require.NoError(t, err)
	require.Equal(t, firstFingerprint, successFingerprint)
	require.Equal(t, firstFingerprint, secondFingerprint)

	batches, oversized, err := PackRawBatchMembers([]RawBatchMember{
		{Ordinal: 0, Prepared: first},
		{Ordinal: 1, Prepared: success},
		{Ordinal: 2, Prepared: second},
	}, 16, 1<<20)
	require.NoError(t, err)
	require.Empty(t, oversized)
	require.Len(t, batches, 1)

	results, err := instance.ExecuteRawBatch(ctx, batches[0], 4)
	require.NoError(t, err)
	require.Len(t, results, 3)
	for _, result := range results {
		require.NoError(t, result.Err)
	}
	assert.True(t, results[0].FallbackAttempted)
	assert.True(t, results[0].FallbackSucceeded)
	assert.False(t, results[1].FallbackAttempted)
	assert.False(t, results[1].FallbackSucceeded)
	assert.True(t, results[2].FallbackAttempted)
	assert.True(t, results[2].FallbackSucceeded)
	assert.Equal(t, "first", results[0].Rows[0]["svrname"])
	assert.Equal(t, "success", results[1].Rows[0]["svrname"])
	assert.Equal(t, "second", results[2].Rows[0]["svrname"])
	assert.EqualValues(t, 1, multiSearchCalls.Load())
	assert.EqualValues(t, 2, firstAliasSearches.Load())
	assert.EqualValues(t, 2, secondAliasSearches.Load())
	assert.Zero(t, successAliasSearches.Load())
	assert.Equal(t, []string{
		firstAlias + ":empty-check",
		firstAlias + ":retry",
		secondAlias + ":empty-check",
		secondAlias + ":retry",
	}, searchOrder)
	assert.EqualValues(t, 1, maxInflight.Load())
	assert.Zero(t, activeRequests.Load())
}

func TestRawBatchMissingMappingFallbackRetryShardFailureIsNotRecovered(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	fallbackAttemptedBefore := rawBatchMetricEventValue(
		t,
		metric.QueryRawESBatchEventFallbackAttempted,
	)
	fallbackFailedBefore := rawBatchMetricEventValue(
		t,
		metric.QueryRawESBatchEventFallbackFailed,
	)
	var aliasSearches atomic.Int32

	const (
		alias          = "batch_fallback_retry_failure"
		badIndex       = "batch_fallback_retry_failure_bad"
		goodIndex      = "batch_fallback_retry_failure_good"
		querySentinel  = "raw_batch_query_secret_8d15"
		reasonSentinel = "retry_reason_secret_6a21"
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/"+alias:
			_, _ = io.WriteString(writer, fmt.Sprintf(
				`{"%s":{"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"}}}},"%s":{"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"},"svrname":{"type":"keyword"}}}}}`,
				badIndex,
				goodIndex,
			))
		case request.Method == http.MethodGet && request.URL.Path == "/_msearch":
			_, _ = io.WriteString(writer, fmt.Sprintf(
				`{"responses":[{"status":200,"_shards":{"total":2,"successful":1,"failed":1,"failures":[{"shard":0,"index":"%s","reason":{"type":"query_shard_exception","reason":"No mapping found for [svrname] in order to sort on"}}]},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}]}`,
				badIndex,
			))
		case request.Method == http.MethodPost && request.URL.Path == "/"+alias+"/_search":
			if aliasSearches.Add(1) == 1 {
				_, _ = io.WriteString(
					writer,
					`{"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
				)
				return
			}
			_, _ = io.WriteString(writer, fmt.Sprintf(
				`{"_shards":{"total":2,"successful":1,"failed":1,"failures":[{"shard":0,"index":"%s","reason":{"type":"query_shard_exception","reason":"%s"}}]},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
				goodIndex,
				reasonSentinel,
			))
		default:
			http.Error(writer, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	instance := rawBatchInstance(t, Connect{Address: server.URL})
	prepared, err := instance.PrepareRawQuery(
		ctx,
		&metadata.Query{
			DB:          alias,
			Field:       "svrname",
			TableID:     "result.table",
			StorageID:   "3",
			StorageType: metadata.ElasticsearchStorageType,
			Size:        1,
			QueryString: `svrname:"` + querySentinel + `"`,
			TimeField: metadata.TimeField{
				Name: "dtEventTimeStamp",
				Type: TimeFieldTypeTime,
				Unit: "millisecond",
			},
			Orders: metadata.Orders{{Name: "svrname", Ast: false}},
		},
		time.UnixMilli(1),
		time.UnixMilli(2),
		nil,
	)
	require.NoError(t, err)
	batch := rawSingleBatch(t, prepared)

	results, err := instance.ExecuteRawBatch(ctx, batch, 4)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Error(t, results[0].Err)
	assert.True(t, results[0].FallbackAttempted)
	assert.False(t, results[0].FallbackSucceeded)
	assert.EqualValues(t, 2, aliasSearches.Load())
	assert.Equal(
		t,
		fallbackAttemptedBefore+1,
		rawBatchMetricEventValue(t, metric.QueryRawESBatchEventFallbackAttempted),
	)
	assert.Equal(
		t,
		fallbackFailedBefore+1,
		rawBatchMetricEventValue(t, metric.QueryRawESBatchEventFallbackFailed),
	)
	publicError := results[0].Err.Error()
	for _, sentinel := range []string{
		server.URL,
		alias,
		badIndex,
		goodIndex,
		querySentinel,
		reasonSentinel,
	} {
		assert.NotContains(t, publicError, sentinel)
	}
}
