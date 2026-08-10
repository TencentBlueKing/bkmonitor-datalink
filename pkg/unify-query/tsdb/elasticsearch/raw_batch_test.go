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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jarcoal/httpmock"
	elastic "github.com/olivere/elastic/v7"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
)

func TestRawBatchFinalBodyFingerprint(t *testing.T) {
	first := rawBatchPrepared(t, Connect{}, []string{"index-a"}, `{"query":{"match_all":{}}}`, 0)
	same := rawBatchPrepared(t, Connect{}, []string{"index-b"}, `{"query":{"match_all":{}}}`, 0)
	different := rawBatchPrepared(t, Connect{}, []string{"index-a"}, `{"query":{"term":{"level":"error"}}}`, 0)

	firstFingerprint, err := PreparedRawQueryFingerprint(first)
	require.NoError(t, err)
	sameFingerprint, err := PreparedRawQueryFingerprint(same)
	require.NoError(t, err)
	differentFingerprint, err := PreparedRawQueryFingerprint(different)
	require.NoError(t, err)

	assert.Equal(t, firstFingerprint, sameFingerprint)
	assert.NotEqual(t, firstFingerprint, differentFingerprint)
}

func TestRawBatchErrorsKeepSensitiveCausesAndTypesPrivate(t *testing.T) {
	cause := fmt.Errorf("endpoint-and-reason-sentinel")
	transportErr := newRawBatchTransportError("request", 0, cause)
	assert.Nil(t, errors.Unwrap(transportErr))
	assert.NotContains(t, transportErr.Error(), "endpoint-and-reason-sentinel")

	assert.Equal(t, "index_not_found_exception", sanitizeRawBatchErrorType("index_not_found_exception"))
	assert.Equal(t, "unknown", sanitizeRawBatchErrorType("secret_type_sentinel"))
}

func TestRawBatchChildErrorRejectsFailedShardWithoutFailureDetails(t *testing.T) {
	err := rawBatchChildError(&elastic.SearchResult{
		Status: http.StatusOK,
		Shards: &elastic.ShardsInfo{
			Total:      2,
			Successful: 1,
			Failed:     1,
		},
	})

	require.Error(t, err)
	assert.EqualError(t, err, "elasticsearch raw batch child failed with status 200 (type=shard_failure)")
}

func TestRawBatchEncodeStableNDJSONAndTrailingNewline(t *testing.T) {
	prepared := rawBatchPrepared(
		t,
		Connect{},
		[]string{"logs-2026", "logs-2025"},
		`{"size":10,"query":{"match_all":{}}}`,
		0,
	)
	member := RawBatchMember{Ordinal: 7, Prepared: prepared}

	first, err := encodeRawBatchMember(member)
	require.NoError(t, err)
	second, err := encodeRawBatchMember(member)
	require.NoError(t, err)

	assert.Equal(t, first, second)
	assert.Equal(
		t,
		"{\"index\":[\"logs-2026\",\"logs-2025\"]}\n{\"size\":10,\"query\":{\"match_all\":{}}}\n",
		first,
	)
	assert.True(t, strings.HasSuffix(first, "\n"))
	assert.Equal(t, 2, strings.Count(first, "\n"))
}

func TestRawBatchPackMemberBoundary(t *testing.T) {
	members := make([]RawBatchMember, 17)
	for index := range members {
		members[index] = RawBatchMember{
			Ordinal:  index,
			Prepared: rawBatchPrepared(t, Connect{}, []string{fmt.Sprintf("index-%02d", index)}, `{}`, 0),
		}
	}

	batches, oversized, err := PackRawBatchMembers(members[:16], 16, 1<<20)
	require.NoError(t, err)
	require.Empty(t, oversized)
	require.Len(t, batches, 1)
	assert.Equal(t, 16, batches[0].MemberCount())

	batches, oversized, err = PackRawBatchMembers(members, 16, 1<<20)
	require.NoError(t, err)
	require.Empty(t, oversized)
	require.Len(t, batches, 2)
	assert.Equal(t, 16, batches[0].MemberCount())
	assert.Equal(t, 1, batches[1].MemberCount())
	assert.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}, rawBatchOrdinals(batches[0]))
	assert.Equal(t, []int{16}, rawBatchOrdinals(batches[1]))
}

func TestRawBatchPackBodyBudgetBoundary(t *testing.T) {
	members := []RawBatchMember{
		{Ordinal: 0, Prepared: rawBatchPrepared(t, Connect{}, []string{"index-a"}, `{"size":1}`, 0)},
		{Ordinal: 1, Prepared: rawBatchPrepared(t, Connect{}, []string{"index-b"}, `{"size":1}`, 0)},
	}
	first, err := encodeRawBatchMember(members[0])
	require.NoError(t, err)
	second, err := encodeRawBatchMember(members[1])
	require.NoError(t, err)
	exactBudget := len([]byte(first + second))

	batches, oversized, err := PackRawBatchMembers(members, 16, exactBudget)
	require.NoError(t, err)
	require.Empty(t, oversized)
	require.Len(t, batches, 1)
	assert.Equal(t, exactBudget, batches[0].BodyBytes())

	batches, oversized, err = PackRawBatchMembers(members, 16, exactBudget-1)
	require.NoError(t, err)
	require.Empty(t, oversized)
	require.Len(t, batches, 2)
	assert.Equal(t, []int{0}, rawBatchOrdinals(batches[0]))
	assert.Equal(t, []int{1}, rawBatchOrdinals(batches[1]))
}

func TestRawBatchPackMarksSingleOversizedMember(t *testing.T) {
	member := RawBatchMember{
		Ordinal:  9,
		Prepared: rawBatchPrepared(t, Connect{}, []string{"index-a"}, `{"query":{"match_all":{}}}`, 0),
	}
	encoded, err := encodeRawBatchMember(member)
	require.NoError(t, err)

	batches, oversized, err := PackRawBatchMembers([]RawBatchMember{member}, 16, len([]byte(encoded))-1)
	require.NoError(t, err)
	require.Empty(t, batches)
	require.Len(t, oversized, 1)
	assert.Equal(t, 9, oversized[0].Member.Ordinal)
	assert.Equal(t, len([]byte(encoded)), oversized[0].BodyBytes)
	assert.Same(t, member.Prepared, oversized[0].Member.Prepared)
}

func TestMSearchURIAndRawNDJSONWire(t *testing.T) {
	longIndex := "logs-" + strings.Repeat("x", 5000)
	var capturedURI string
	var capturedBody string
	var capturedContentType string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		capturedURI = request.RequestURI
		capturedContentType = request.Header.Get("Content-Type")
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		capturedBody = string(body)
		assert.Equal(t, http.MethodGet, request.Method)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"responses":[{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}]}`)
	}))
	defer server.Close()

	connect := Connect{Address: server.URL}
	prepared := rawBatchPrepared(t, connect, []string{longIndex}, `{"query":{"match_all":{}}}`, 0)
	batches, oversized, err := PackRawBatchMembers(
		[]RawBatchMember{{Ordinal: 3, Prepared: prepared}},
		16,
		1<<20,
	)
	require.NoError(t, err)
	require.Empty(t, oversized)
	require.Len(t, batches, 1)
	instance := rawBatchInstance(t, connect)

	results, err := instance.ExecuteRawBatch(context.Background(), batches[0], 7)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.NoError(t, results[0].Err)

	parsedURI, err := url.ParseRequestURI(capturedURI)
	require.NoError(t, err)
	assert.Equal(t, "/_msearch", parsedURI.Path)
	assert.NotContains(t, capturedURI, longIndex)
	assert.Equal(t, "7", parsedURI.Query().Get("max_concurrent_searches"))
	assert.Equal(t, "application/x-ndjson", capturedContentType)
	assert.Equal(t, batches[0].ndjson, capturedBody)
	assert.True(t, strings.HasPrefix(capturedBody, `{"index":`))
	assert.Contains(t, capturedBody, longIndex)
	assert.NotContains(t, capturedBody, "base64")
	assert.True(t, strings.HasSuffix(capturedBody, "\n"))
}

func TestMSearchOmitsInvalidMaxConcurrentSearches(t *testing.T) {
	for _, maxConcurrentSearches := range []int{0, -1} {
		t.Run(fmt.Sprintf("%d", maxConcurrentSearches), func(t *testing.T) {
			var query url.Values
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				query = request.URL.Query()
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, `{"responses":[{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}]}`)
			}))
			defer server.Close()

			connect := Connect{Address: server.URL}
			batch := rawSingleBatch(t, rawBatchPrepared(t, connect, []string{"index-a"}, `{}`, 0))
			_, err := rawBatchInstance(t, connect).ExecuteRawBatch(context.Background(), batch, maxConcurrentSearches)
			require.NoError(t, err)
			assert.NotContains(t, query, "max_concurrent_searches")
		})
	}
}

func TestRawBatchExecuteTwoChildrenIndependently(t *testing.T) {
	wireSuccessBefore := rawBatchMetricEventValue(t, metric.QueryRawESBatchEventWireSuccess)
	memberHistogramBefore := rawBatchMetricHistogramCount(
		t,
		"unify_query_query_raw_es_batch_member_count",
	)
	bodyHistogramBefore := rawBatchMetricHistogramCount(
		t,
		"unify_query_query_raw_es_batch_body_bytes",
	)
	durationHistogramBefore := rawBatchMetricHistogramCount(
		t,
		"unify_query_query_raw_es_batch_duration_seconds",
	)
	response := `{
		"responses": [
			{"hits":{"total":{"value":2,"relation":"eq"},"hits":[
				{"_index":"shared-alias","_id":"one","sort":[100,"one"],"_source":{"time":100,"value":"first"}}
			]}},
			{"hits":{"total":{"value":7,"relation":"eq"},"hits":[
				{"_index":"shared-alias","_id":"two","sort":[200,"two"],"_source":{"time":200,"value":"second"}}
			]}}
		]
	}`
	connect, instance, server := rawBatchTestServer(t, http.StatusOK, response)
	defer server.Close()

	first := rawBatchPrepared(t, connect, []string{"shared-alias"}, `{"search_after":[50,"zero"]}`, 10)
	second := rawBatchPrepared(t, connect, []string{"shared-alias"}, `{"search_after":[150,"one"]}`, 20)
	batch := rawBatchFromMembers(t, []RawBatchMember{
		{Ordinal: 41, Prepared: first},
		{Ordinal: 99, Prepared: second},
	})

	results, err := instance.ExecuteRawBatch(context.Background(), batch, 0)
	require.NoError(t, err)
	require.Len(t, results, 2)

	assert.Equal(t, 41, results[0].Ordinal)
	assert.EqualValues(t, 1, results[0].Size)
	assert.EqualValues(t, 2, results[0].Total)
	require.Len(t, results[0].Rows, 1)
	assert.Equal(t, "first", results[0].Rows[0]["value"])
	assert.Equal(t, "one", results[0].Rows[0][metadata.KeyDocID])
	assert.Equal(t, []any{float64(100), "one"}, results[0].Option.SearchAfter)
	require.NotNil(t, results[0].Option.From)
	assert.Equal(t, 10, *results[0].Option.From)

	assert.Equal(t, 99, results[1].Ordinal)
	assert.EqualValues(t, 1, results[1].Size)
	assert.EqualValues(t, 7, results[1].Total)
	require.Len(t, results[1].Rows, 1)
	assert.Equal(t, "second", results[1].Rows[0]["value"])
	assert.Equal(t, "two", results[1].Rows[0][metadata.KeyDocID])
	assert.Equal(t, []any{float64(200), "two"}, results[1].Option.SearchAfter)
	require.NotNil(t, results[1].Option.From)
	assert.Equal(t, 20, *results[1].Option.From)
	assert.Equal(
		t,
		wireSuccessBefore+1,
		rawBatchMetricEventValue(t, metric.QueryRawESBatchEventWireSuccess),
	)
	assert.Equal(
		t,
		memberHistogramBefore+1,
		rawBatchMetricHistogramCount(t, "unify_query_query_raw_es_batch_member_count"),
	)
	assert.Equal(
		t,
		bodyHistogramBefore+1,
		rawBatchMetricHistogramCount(t, "unify_query_query_raw_es_batch_body_bytes"),
	)
	assert.Equal(
		t,
		durationHistogramBefore+1,
		rawBatchMetricHistogramCount(t, "unify_query_query_raw_es_batch_duration_seconds"),
	)
}

func TestRawBatchExecuteChildErrorIsPartialAndSanitized(t *testing.T) {
	wirePartialBefore := rawBatchMetricEventValue(t, metric.QueryRawESBatchEventWirePartial)
	childFailureBefore := rawBatchMetricEventValue(t, metric.QueryRawESBatchEventWireChildFailure)
	response := `{
		"responses": [
			{"error":{"type":"index_not_found_exception","reason":"secret-index-sentinel"},"status":404},
			{"hits":{"total":{"value":1,"relation":"eq"},"hits":[
				{"_index":"shared-alias","_id":"ok","_source":{"time":200,"value":"success"}}
			]}}
		]
	}`
	connect, instance, server := rawBatchTestServer(t, http.StatusOK, response)
	defer server.Close()

	batch := rawBatchFromMembers(t, []RawBatchMember{
		{Ordinal: 0, Prepared: rawBatchPrepared(t, connect, []string{"missing-index-sentinel"}, `{}`, 0)},
		{Ordinal: 1, Prepared: rawBatchPrepared(t, connect, []string{"shared-alias"}, `{}`, 0)},
	})
	results, err := instance.ExecuteRawBatch(context.Background(), batch, 0)
	require.NoError(t, err)
	require.Len(t, results, 2)

	require.Error(t, results[0].Err)
	assert.Contains(t, results[0].Err.Error(), "index_not_found_exception")
	assert.NotContains(t, results[0].Err.Error(), "secret-index-sentinel")
	assert.NotContains(t, results[0].Err.Error(), "missing-index-sentinel")
	assert.Zero(t, results[0].Size)
	assert.Zero(t, results[0].Total)
	assert.Empty(t, results[0].Rows)
	assert.Nil(t, results[0].Option)

	require.NoError(t, results[1].Err)
	assert.EqualValues(t, 1, results[1].Size)
	assert.EqualValues(t, 1, results[1].Total)
	require.Len(t, results[1].Rows, 1)
	assert.Equal(t, "success", results[1].Rows[0]["value"])
	require.NotNil(t, results[1].Option)
	assert.Equal(
		t,
		wirePartialBefore+1,
		rawBatchMetricEventValue(t, metric.QueryRawESBatchEventWirePartial),
	)
	assert.Equal(
		t,
		childFailureBefore+1,
		rawBatchMetricEventValue(t, metric.QueryRawESBatchEventWireChildFailure),
	)
}

func TestRawBatchExecuteChildDecodePanicIsIndependent(t *testing.T) {
	connect, instance, server := rawBatchTestServer(
		t,
		http.StatusOK,
		`{"responses":[`+
			`{"status":200,"hits":{"total":{"value":1,"relation":"eq"},"hits":[null]}},`+
			`{"status":200,"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"second","_id":"second","_source":{"time":2,"value":"ok"}}]}}`+
			`]}`,
	)
	defer server.Close()

	batch := rawBatchFromMembers(t, []RawBatchMember{
		{Ordinal: 0, Prepared: rawBatchPrepared(t, connect, []string{"first"}, `{}`, 0)},
		{Ordinal: 1, Prepared: rawBatchPrepared(t, connect, []string{"second"}, `{}`, 0)},
	})
	results, err := instance.ExecuteRawBatch(context.Background(), batch, 0)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Error(t, results[0].Err)
	assert.Contains(t, results[0].Err.Error(), "decode")
	require.NoError(t, results[1].Err)
	require.Len(t, results[1].Rows, 1)
	assert.Equal(t, "ok", results[1].Rows[0]["value"])
}

func TestRawBatchExecuteNilChildDoesNotShiftLaterMember(t *testing.T) {
	connect, instance, server := rawBatchTestServer(
		t,
		http.StatusOK,
		`{"responses":[null,{"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"second","_id":"ok","_source":{"time":2,"value":"second"}}]}}]}`,
	)
	defer server.Close()

	batch := rawBatchFromMembers(t, []RawBatchMember{
		{Ordinal: 10, Prepared: rawBatchPrepared(t, connect, []string{"first"}, `{}`, 0)},
		{Ordinal: 20, Prepared: rawBatchPrepared(t, connect, []string{"second"}, `{}`, 0)},
	})
	results, err := instance.ExecuteRawBatch(context.Background(), batch, 0)
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, 10, results[0].Ordinal)
	require.Error(t, results[0].Err)
	assert.Equal(t, 20, results[1].Ordinal)
	require.NoError(t, results[1].Err)
	require.Len(t, results[1].Rows, 1)
	assert.Equal(t, "second", results[1].Rows[0]["value"])
}

func TestRawBatchExecuteResponseCountMismatchIsTransportError(t *testing.T) {
	connect, instance, server := rawBatchTestServer(
		t,
		http.StatusOK,
		`{"responses":[{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}]}`,
	)
	defer server.Close()
	batch := rawBatchFromMembers(t, []RawBatchMember{
		{Ordinal: 0, Prepared: rawBatchPrepared(t, connect, []string{"first"}, `{}`, 0)},
		{Ordinal: 1, Prepared: rawBatchPrepared(t, connect, []string{"second"}, `{}`, 0)},
	})

	results, err := instance.ExecuteRawBatch(context.Background(), batch, 0)
	require.Error(t, err)
	assert.Empty(t, results)
	var transportErr *RawBatchTransportError
	require.ErrorAs(t, err, &transportErr)
	assert.Equal(t, "response_count_mismatch", transportErr.Kind)
}

func TestRawBatchExecuteTransportStatusDoesNotFanOut(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			transportFailureBefore := rawBatchMetricEventValue(
				t,
				metric.QueryRawESBatchEventWireTransportFailure,
			)
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				assert.Equal(t, "/_msearch", request.URL.Path)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(status)
				_, _ = io.WriteString(writer, fmt.Sprintf(
					`{"status":%d,"error":{"type":"transport_sentinel","reason":"must-not-leak"}}`,
					status,
				))
			}))
			defer server.Close()

			connect := Connect{Address: server.URL}
			batch := rawBatchFromMembers(t, []RawBatchMember{
				{Ordinal: 0, Prepared: rawBatchPrepared(t, connect, []string{"first"}, `{}`, 0)},
				{Ordinal: 1, Prepared: rawBatchPrepared(t, connect, []string{"second"}, `{}`, 0)},
			})
			results, err := rawBatchInstance(t, connect).ExecuteRawBatch(context.Background(), batch, 0)
			require.Error(t, err)
			assert.Empty(t, results)
			assert.NotContains(t, err.Error(), "must-not-leak")
			assert.NotContains(t, err.Error(), server.URL)
			assert.EqualValues(t, 1, requests.Load())

			var transportErr *RawBatchTransportError
			require.ErrorAs(t, err, &transportErr)
			assert.Equal(t, status, transportErr.Status)
			assert.Equal(
				t,
				transportFailureBefore+1,
				rawBatchMetricEventValue(t, metric.QueryRawESBatchEventWireTransportFailure),
			)
		})
	}
}

func TestRawBatchSpanRecordsMemberDiagnosticsWithoutConnectionDetails(t *testing.T) {
	recorder := setupESTraceRecorder(t)
	const (
		firstIndexSentinel  = "sensitive-index-first"
		secondIndexSentinel = "sensitive-index-second"
		querySentinel       = "trace-query-sentinel"
		firstTableID        = "trace.app.first"
		secondTableID       = "trace.app.second"
		userSentinel        = "sensitive-user-sentinel"
		passSentinel        = "sensitive-password-sentinel"
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(
			writer,
			`{"responses":[`+
				`{"took":932,"timed_out":true,"status":200,"_shards":{"total":8,"successful":7,"failed":1},`+
				`"hits":{"total":{"value":23,"relation":"eq"},"hits":[]}},`+
				`{"took":41,"timed_out":false,"status":404,"_shards":{"total":4,"successful":4,"failed":0},`+
				`"error":{"type":"index_not_found_exception","reason":"must-not-leak"}}]}`,
		)
	}))
	defer server.Close()

	connect := Connect{
		Address:  server.URL,
		UserName: userSentinel,
		Password: passSentinel,
	}
	queryBody := `{"query":{"term":{"message":"` + querySentinel + `"}}}`
	first := rawBatchPrepared(
		t,
		connect,
		[]string{firstIndexSentinel},
		queryBody,
		0,
	)
	first.query.TableID = firstTableID
	second := rawBatchPrepared(t, connect, []string{secondIndexSentinel}, queryBody, 0)
	second.query.TableID = secondTableID
	batch := rawBatchFromMembers(t, []RawBatchMember{
		{Ordinal: 7, Prepared: first},
		{Ordinal: 9, Prepared: second},
	})
	results, err := rawBatchInstance(t, connect).ExecuteRawBatch(context.Background(), batch, 4)
	require.NoError(t, err)
	require.Len(t, results, 2)

	attributes := endedSpanAttrs(t, recorder)
	renderedAttributes := fmt.Sprint(attributes)
	for _, sentinel := range []string{
		server.URL,
		firstIndexSentinel,
		secondIndexSentinel,
		userSentinel,
		passSentinel,
		"must-not-leak",
	} {
		assert.NotContains(t, renderedAttributes, sentinel)
	}
	queryBodyAttribute, ok := esSpanAttrString(attributes, "query-body")
	require.True(t, ok)
	assert.Equal(t, queryBody, queryBodyAttribute)
	queryBodySize, ok := esSpanAttrInt(attributes, "query-body-size")
	require.True(t, ok)
	assert.EqualValues(t, len([]byte(queryBody)), queryBodySize)
	queryBodyTruncated, ok := esSpanAttrBool(attributes, "query-body-truncated")
	require.True(t, ok)
	assert.False(t, queryBodyTruncated)
	sharedQueryBody, ok := esSpanAttrBool(attributes, "es_batch_shared_query_body")
	require.True(t, ok)
	assert.True(t, sharedQueryBody)

	memberOrdinals, ok := esSpanAttrIntSlice(attributes, "es_batch_member_ordinals")
	require.True(t, ok)
	assert.Equal(t, []int64{7, 9}, memberOrdinals)
	memberTableIDs, ok := esSpanAttrStringSlice(attributes, "es_batch_member_table_ids")
	require.True(t, ok)
	assert.Equal(t, []string{firstTableID, secondTableID}, memberTableIDs)
	memberIndexCounts, ok := esSpanAttrIntSlice(attributes, "es_batch_member_index_counts")
	require.True(t, ok)
	assert.Equal(t, []int64{1, 1}, memberIndexCounts)

	childTookMillis, ok := esSpanAttrIntSlice(attributes, "es_batch_child_took_millis")
	require.True(t, ok)
	assert.Equal(t, []int64{932, 41}, childTookMillis)
	childTimedOutOrdinals, ok := esSpanAttrIntSlice(attributes, "es_batch_child_timed_out_ordinals")
	require.True(t, ok)
	assert.Equal(t, []int64{7}, childTimedOutOrdinals)
	childStatuses, ok := esSpanAttrIntSlice(attributes, "es_batch_child_statuses")
	require.True(t, ok)
	assert.Equal(t, []int64{200, 404}, childStatuses)
	childTotalHits, ok := esSpanAttrIntSlice(attributes, "es_batch_child_total_hits")
	require.True(t, ok)
	assert.Equal(t, []int64{23, 0}, childTotalHits)
	childShardsTotal, ok := esSpanAttrIntSlice(attributes, "es_batch_child_shards_total")
	require.True(t, ok)
	assert.Equal(t, []int64{8, 4}, childShardsTotal)
	childShardsSuccessful, ok := esSpanAttrIntSlice(attributes, "es_batch_child_shards_successful")
	require.True(t, ok)
	assert.Equal(t, []int64{7, 4}, childShardsSuccessful)
	childShardsFailed, ok := esSpanAttrIntSlice(attributes, "es_batch_child_shards_failed")
	require.True(t, ok)
	assert.Equal(t, []int64{1, 0}, childShardsFailed)
	childErrorTypes, ok := esSpanAttrStringSlice(attributes, "es_batch_child_error_types")
	require.True(t, ok)
	assert.Equal(t, []string{"shard_failure", "index_not_found_exception"}, childErrorTypes)

	memberCount, ok := esSpanAttrInt(attributes, "es_batch_member_count")
	require.True(t, ok)
	assert.EqualValues(t, 2, memberCount)
	bodyBytes, ok := esSpanAttrInt(attributes, "es_batch_body_bytes")
	require.True(t, ok)
	assert.Positive(t, bodyBytes)
}

func TestRawBatchSpanTruncatesOversizedSharedQueryBody(t *testing.T) {
	recorder := setupESTraceRecorder(t)
	const maxQueryBodyLength = 16 * 1024
	queryBody := strings.Repeat("x", maxQueryBodyLength+1)
	connect, instance, server := rawBatchTestServer(
		t,
		http.StatusOK,
		`{"responses":[{"status":200,"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}]}`,
	)
	defer server.Close()

	batch := rawSingleBatch(t, rawBatchPrepared(t, connect, []string{"index-a"}, queryBody, 0))
	_, err := instance.ExecuteRawBatch(context.Background(), batch, 0)
	require.NoError(t, err)

	attributes := endedSpanAttrs(t, recorder)
	queryBodyAttribute, ok := esSpanAttrString(attributes, "query-body")
	require.True(t, ok)
	assert.Equal(t, queryBody[:maxQueryBodyLength], queryBodyAttribute)
	queryBodySize, ok := esSpanAttrInt(attributes, "query-body-size")
	require.True(t, ok)
	assert.EqualValues(t, len([]byte(queryBody)), queryBodySize)
	queryBodyTruncated, ok := esSpanAttrBool(attributes, "query-body-truncated")
	require.True(t, ok)
	assert.True(t, queryBodyTruncated)
}

func TestRawBatchSpanTruncatesSharedQueryBodyOnUTF8Boundary(t *testing.T) {
	recorder := setupESTraceRecorder(t)
	const maxQueryBodyBytes = 16 * 1024
	queryBody := strings.Repeat("查", maxQueryBodyBytes/3+1)
	connect, instance, server := rawBatchTestServer(
		t,
		http.StatusOK,
		`{"responses":[{"status":200,"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}]}`,
	)
	defer server.Close()

	batch := rawSingleBatch(t, rawBatchPrepared(t, connect, []string{"index-a"}, queryBody, 0))
	_, err := instance.ExecuteRawBatch(context.Background(), batch, 0)
	require.NoError(t, err)

	attributes := endedSpanAttrs(t, recorder)
	queryBodyAttribute, ok := esSpanAttrString(attributes, "query-body")
	require.True(t, ok)
	assert.LessOrEqual(t, len([]byte(queryBodyAttribute)), maxQueryBodyBytes)
	assert.True(t, utf8.ValidString(queryBodyAttribute))
	assert.True(t, strings.HasPrefix(queryBody, queryBodyAttribute))
	queryBodyTruncated, ok := esSpanAttrBool(attributes, "query-body-truncated")
	require.True(t, ok)
	assert.True(t, queryBodyTruncated)
}

func TestRawBatchSpanOmitsQueryBodyWhenMembersDiffer(t *testing.T) {
	recorder := setupESTraceRecorder(t)
	connect, instance, server := rawBatchTestServer(
		t,
		http.StatusOK,
		`{"responses":[`+
			`{"status":200,"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}},`+
			`{"status":200,"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}]}`,
	)
	defer server.Close()

	batch := rawBatchFromMembers(t, []RawBatchMember{
		{Ordinal: 0, Prepared: rawBatchPrepared(t, connect, []string{"index-a"}, `{"query":{"term":{"level":"info"}}}`, 0)},
		{Ordinal: 1, Prepared: rawBatchPrepared(t, connect, []string{"index-b"}, `{"query":{"term":{"level":"error"}}}`, 0)},
	})
	_, err := instance.ExecuteRawBatch(context.Background(), batch, 0)
	require.NoError(t, err)

	attributes := endedSpanAttrs(t, recorder)
	sharedQueryBody, ok := esSpanAttrBool(attributes, "es_batch_shared_query_body")
	require.True(t, ok)
	assert.False(t, sharedQueryBody)
	_, ok = esSpanAttrString(attributes, "query-body")
	assert.False(t, ok)
	_, ok = esSpanAttrInt(attributes, "query-body-size")
	assert.False(t, ok)
	_, ok = esSpanAttrBool(attributes, "query-body-truncated")
	assert.False(t, ok)
}

func esSpanAttrIntSlice(attrs []attribute.KeyValue, key string) ([]int64, bool) {
	for _, kv := range attrs {
		if string(kv.Key) == key {
			return kv.Value.AsInt64Slice(), true
		}
	}
	return nil, false
}

func esSpanAttrStringSlice(attrs []attribute.KeyValue, key string) ([]string, bool) {
	for _, kv := range attrs {
		if string(kv.Key) == key {
			return kv.Value.AsStringSlice(), true
		}
	}
	return nil, false
}

func TestRawBatchExecuteClaimsPreparedMembersOnce(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"responses":[{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}]}`)
	}))
	defer server.Close()

	connect := Connect{Address: server.URL}
	batch := rawSingleBatch(t, rawBatchPrepared(t, connect, []string{"index-a"}, `{}`, 0))
	instance := rawBatchInstance(t, connect)
	_, err := instance.ExecuteRawBatch(context.Background(), batch, 0)
	require.NoError(t, err)

	_, err = instance.ExecuteRawBatch(context.Background(), batch, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already consumed")
	assert.EqualValues(t, 1, requests.Load())
}

func TestRawBatchExecuteClaimConflictRollsBackUnsentMembers(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"responses":[{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}]}`)
	}))
	defer server.Close()

	connect := Connect{Address: server.URL}
	first := rawBatchPrepared(t, connect, []string{"first"}, `{}`, 0)
	alreadyConsumed := rawBatchPrepared(t, connect, []string{"second"}, `{}`, 0)
	alreadyConsumed.claimed.Store(true)
	conflictingBatch := rawBatchFromMembers(t, []RawBatchMember{
		{Ordinal: 0, Prepared: first},
		{Ordinal: 1, Prepared: alreadyConsumed},
	})
	instance := rawBatchInstance(t, connect)

	_, err := instance.ExecuteRawBatch(context.Background(), conflictingBatch, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already consumed")
	assert.False(t, first.claimed.Load(), "unsent preceding member must be released")
	assert.True(t, alreadyConsumed.claimed.Load(), "pre-existing claim must remain owned")
	assert.Zero(t, requests.Load())

	retryBatch := rawSingleBatch(t, first)
	results, err := instance.ExecuteRawBatch(context.Background(), retryBatch, 0)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.NoError(t, results[0].Err)
	assert.EqualValues(t, 1, requests.Load())
}

func rawBatchPrepared(
	t *testing.T,
	connect Connect,
	indexes []string,
	body string,
	from int,
) *PreparedRawQuery {
	t.Helper()
	query := &metadata.Query{
		DB:          "index",
		TableID:     "result.table",
		StorageType: metadata.ElasticsearchStorageType,
		From:        from,
		Size:        10,
		Source:      []string{"time", "value"},
		TimeField: metadata.TimeField{
			Name: "time",
			Type: TimeFieldTypeTime,
			Unit: "millisecond",
		},
	}
	queryOption := &queryOption{
		indexes: append([]string(nil), indexes...),
		query:   query,
		conn:    connect,
	}
	fact := newRawFormatFactory(metadata.InitHashID(context.Background()), query, queryOption, nil)
	instance := rawBatchInstance(t, connect)
	return &PreparedRawQuery{
		query:         query,
		queryOption:   queryOption,
		fact:          fact,
		source:        elastic.NewSearchSource(),
		body:          body,
		connectionKey: instance.RawBatchConnectionKey(metadata.InitHashID(context.Background())),
	}
}

func rawBatchInstance(t *testing.T, connect Connect) *Instance {
	t.Helper()
	// Several legacy Elasticsearch tests leave the package-global httpmock
	// transport active. These tests use a real httptest server to validate the
	// exact request URI and wire bytes.
	if http.DefaultTransport == httpmock.DefaultTransport {
		httpmock.Deactivate()
		t.Cleanup(httpmock.Activate)
	}
	instance, err := NewInstance(context.Background(), &InstanceOption{
		Connect:     connect,
		Timeout:     2 * time.Second,
		HealthCheck: false,
	})
	require.NoError(t, err)
	return instance
}

func rawBatchTestServer(
	t *testing.T,
	status int,
	response string,
) (Connect, *Instance, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = io.WriteString(writer, response)
	}))
	connect := Connect{Address: server.URL}
	return connect, rawBatchInstance(t, connect), server
}

func rawBatchFromMembers(t *testing.T, members []RawBatchMember) *RawBatch {
	t.Helper()
	batches, oversized, err := PackRawBatchMembers(members, len(members), 1<<20)
	require.NoError(t, err)
	require.Empty(t, oversized)
	require.Len(t, batches, 1)
	return batches[0]
}

func rawSingleBatch(t *testing.T, prepared *PreparedRawQuery) *RawBatch {
	t.Helper()
	return rawBatchFromMembers(t, []RawBatchMember{{Ordinal: 0, Prepared: prepared}})
}

func rawBatchOrdinals(batch *RawBatch) []int {
	members := batch.Members()
	ordinals := make([]int, len(members))
	for index, member := range members {
		ordinals[index] = member.Ordinal
	}
	return ordinals
}

func rawBatchMetricEventValue(t *testing.T, event string) float64 {
	t.Helper()

	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "unify_query_query_raw_es_batch_events_total" {
			continue
		}
		for _, familyMetric := range family.GetMetric() {
			for _, label := range familyMetric.GetLabel() {
				if label.GetName() == "event" && label.GetValue() == event {
					return familyMetric.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

func rawBatchMetricHistogramCount(t *testing.T, metricName string) uint64 {
	t.Helper()

	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != metricName {
			continue
		}
		var count uint64
		for _, familyMetric := range family.GetMetric() {
			count += familyMetric.GetHistogram().GetSampleCount()
		}
		return count
	}
	return 0
}
