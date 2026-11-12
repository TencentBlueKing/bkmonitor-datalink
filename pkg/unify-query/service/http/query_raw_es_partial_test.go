// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and limitations under the License.

package http

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

const (
	rawESHealthyShards = `{"total":1,"successful":1,"failed":0}`
	rawESPartialShards = `{"total":98,"successful":90,"skipped":0,"failed":8,"failures":[{"shard":2,"reason":{"type":"node_not_connected_exception","reason":"test shard unavailable"}}]}`
)

func runRawESPartialRequest(t *testing.T, query *structured.QueryTs, allowPartial *bool) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(struct {
		*structured.QueryTs
		AllowPartial *bool `json:"allow_partial_es_results,omitempty"`
	}{QueryTs: query, AllowPartial: allowPartial})
	require.NoError(t, err)
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: query.SpaceUid})
	request := httptest.NewRequest(http.MethodPost, "/query/ts/raw", bytes.NewReader(body)).WithContext(ctx)
	return runCheckHandler(t, request, HandlerQueryRaw)
}

func TestHandlerQueryRawWithScrollRejectsPartialESResults(t *testing.T) {
	mock.Init()
	originalWindowTimeout := ScrollWindowTimeout
	// Session setup parses this value before accessing Redis.
	ScrollWindowTimeout = "invalid-session-window"
	t.Cleanup(func() { ScrollWindowTimeout = originalWindowTimeout })

	for _, tc := range []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "enabled without scroll",
			body:    `{"allow_partial_es_results":true}`,
			wantErr: "allow_partial_es_results is not supported by query_raw_with_scroll",
		},
		{
			name:    "enabled with scroll",
			body:    `{"allow_partial_es_results":true,"scroll":"1m"}`,
			wantErr: "allow_partial_es_results is not supported by query_raw_with_scroll",
		},
		{
			name:    "disabled keeps session setup",
			body:    `{"allow_partial_es_results":false}`,
			wantErr: "invalid-session-window",
		},
		{
			name:    "omitted keeps session setup",
			body:    `{}`,
			wantErr: "invalid-session-window",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			request := httptest.NewRequest(http.MethodPost, "/query/ts/raw_with_scroll", bytes.NewBufferString(tc.body)).WithContext(ctx)
			response := runCheckHandler(t, request, HandlerQueryRawWithScroll)
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Contains(t, response.Body.String(), tc.wantErr)
		})
	}
}

func TestQueryRawESPartialShardResults(t *testing.T) {
	mock.Init()
	const (
		esURL     = "http://127.0.0.1:93051"
		storageID = "raw-es-partial-shards"
		index     = "raw_es_partial_shards"
	)
	tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: esURL})
	registerRawESBatchMappingResponder(t, esURL, index)

	enabled, disabled := true, false
	for _, tc := range []struct {
		name        string
		allow       *bool
		shards      string
		searchAfter bool
		timedOut    bool
		empty       bool
		wantPartial bool
		wantError   bool
		decodeError bool
	}{
		{name: "omitted", shards: rawESPartialShards, wantError: true},
		{name: "disabled", allow: &disabled, shards: rawESPartialShards, wantError: true},
		{name: "enabled", allow: &enabled, shards: rawESPartialShards, wantPartial: true},
		{name: "failed count without details", allow: &enabled, shards: `{"total":2,"successful":1,"failed":1}`, wantPartial: true},
		{name: "search after", allow: &enabled, shards: rawESPartialShards, searchAfter: true, wantPartial: true},
		{name: "no hits", allow: &enabled, shards: rawESPartialShards, empty: true, wantPartial: true},
		{name: "healthy", allow: &enabled, shards: rawESHealthyShards},
		{name: "all shards failed", allow: &enabled, shards: `{"total":1,"successful":0,"failed":1}`, wantError: true},
		{name: "only skipped shards succeeded", allow: &enabled, shards: `{"total":2,"successful":1,"skipped":1,"failed":1}`, wantError: true},
		{name: "timed out with failed shards", allow: &enabled, shards: rawESPartialShards, timedOut: true, wantError: true},
		{name: "timed out without failed shards", allow: &enabled, shards: rawESHealthyShards, timedOut: true, wantError: true},
		{name: "legacy timeout", shards: rawESHealthyShards, timedOut: true},
		{name: "malformed partial hit", allow: &enabled, shards: rawESPartialShards, decodeError: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits := `[{"_id":"partial-hit","_source":{"start_time":1700000000100},"sort":[1700000000100]}]`
			total := 1
			if tc.empty {
				hits, total = "[]", 0
			}
			if tc.decodeError {
				hits = `[{"_id":"partial-hit","_source":"invalid log object"}]`
			}
			httpmock.RegisterResponder(http.MethodPost, esURL+"/"+index+"/_search", func(req *http.Request) (*http.Response, error) {
				assertPartialESNativeParameter(t, req, tc.allow != nil && *tc.allow)
				var body map[string]json.RawMessage
				assert.NoError(t, json.NewDecoder(req.Body).Decode(&body))
				if tc.searchAfter {
					assert.JSONEq(t, "[1700000000200]", string(body["search_after"]))
				} else {
					assert.NotContains(t, body, "search_after")
				}
				response := fmt.Sprintf(`{"timed_out":%t,"_shards":%s,"hits":{"total":{"value":%d,"relation":"eq"},"hits":%s}}`,
					tc.timedOut, tc.shards, total, hits)
				return httpmock.NewStringResponse(http.StatusOK, response), nil
			})
			query := rawESBatchIntegrationQuery(storageID, "synthetic-trace", rawESBatchIntegrationRoute{
				referenceName: "a", tableID: "trace.partial", index: index,
			})
			tableUUID := "trace.partial|" + storageID
			if tc.searchAfter {
				query.IsSearchAfter = true
				query.ResultTableOptions = metadata.ResultTableOptions{
					tableUUID: {SearchAfter: []any{1700000000200}},
				}
			}

			recorder := runRawESPartialRequest(t, query, tc.allow)
			if tc.wantError {
				assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
				assert.NotContains(t, recorder.Body.String(), "partial-hit")
				return
			}
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var response ListData
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.EqualValues(t, total, response.Total)
			require.Len(t, response.List, total)
			assert.False(t, response.TotalExact)
			if total != 0 {
				assert.Equal(t, "partial-hit", response.List[0][metadata.KeyDocID])
			}
			if tc.searchAfter {
				require.NotNil(t, response.ResultTableOptions[tableUUID])
				assert.Equal(t, []any{float64(1700000000100)}, response.ResultTableOptions[tableUUID].SearchAfter)
			}
			if tc.wantPartial {
				require.NotNil(t, response.Status)
				assert.Equal(t, metadata.QueryRawPartial, response.Status.Code)
				assert.Contains(t, response.Status.Message, "部分分片结果")
			} else {
				assert.Nil(t, response.Status)
			}
		})
	}
}

func TestQueryRawESPartialShardDecodeFailureDiscardsRoute(t *testing.T) {
	mock.Init()
	const (
		esURL        = "http://127.0.0.1:93056"
		storageID    = "raw-es-partial-decode-failure"
		failedIndex  = "raw_es_partial_decode_failed"
		healthyIndex = "raw_es_partial_decode_healthy"
	)
	tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: esURL})
	registerRawESBatchMappingResponder(t, esURL, failedIndex)
	registerRawESBatchMappingResponder(t, esURL, healthyIndex)

	enabled, disabled := true, false
	for _, tc := range []struct {
		name        string
		allow       *bool
		searchAfter bool
		shards      string
	}{
		{name: "omitted partial shards", shards: rawESPartialShards},
		{name: "disabled partial shards", allow: &disabled, shards: rawESPartialShards},
		{name: "omitted malformed hit", shards: rawESHealthyShards},
		{name: "disabled malformed hit", allow: &disabled, shards: rawESHealthyShards},
		{name: "enabled", allow: &enabled, shards: rawESPartialShards},
		{name: "search after", allow: &enabled, searchAfter: true, shards: rawESPartialShards},
	} {
		t.Run(tc.name, func(t *testing.T) {
			httpmock.RegisterResponder(http.MethodPost, esURL+"/"+failedIndex+"/_search", func(req *http.Request) (*http.Response, error) {
				assertPartialESNativeParameter(t, req, tc.allow != nil && *tc.allow)
				return httpmock.NewStringResponse(http.StatusOK, `{"_shards":`+tc.shards+`,"hits":{"total":{"value":2,"relation":"eq"},"hits":[{"_id":"discarded-hit","_source":{"start_time":1700000000200},"sort":[1700000000200]},{"_id":"missing-source"}]}}`), nil
			})
			httpmock.RegisterResponder(http.MethodPost, esURL+"/"+healthyIndex+"/_search", func(req *http.Request) (*http.Response, error) {
				assertPartialESNativeParameter(t, req, tc.allow != nil && *tc.allow)
				return httpmock.NewStringResponse(http.StatusOK, `{"_shards":`+rawESHealthyShards+`,"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_id":"healthy-hit","_source":{"start_time":1700000000100},"sort":[1700000000100]}]}}`), nil
			})
			query := rawESBatchExplicitTraceQuery(storageID, failedIndex, healthyIndex, "synthetic-trace")
			failedTable := "trace.first|" + storageID
			healthyTable := "trace.second|" + storageID
			if tc.searchAfter {
				query.IsSearchAfter = true
				query.ResultTableOptions = metadata.ResultTableOptions{
					failedTable:  {SearchAfter: []any{1700000000300}},
					healthyTable: {SearchAfter: []any{1700000000300}},
				}
			}

			recorder := runRawESPartialRequest(t, query, tc.allow)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var response ListData
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.EqualValues(t, 1, response.Total)
			require.Len(t, response.List, 1)
			assert.Equal(t, "healthy-hit", response.List[0][metadata.KeyDocID])
			require.NotNil(t, response.Status)
			assert.Equal(t, metadata.QueryRawPartial, response.Status.Code)
			if tc.shards == rawESHealthyShards || tc.allow != nil && *tc.allow {
				assert.Contains(t, response.Status.Message, "EOF")
			}
			assert.NotContains(t, response.ResultTableOptions, failedTable)
			if tc.searchAfter {
				require.NotNil(t, response.ResultTableOptions[healthyTable])
				assert.Equal(t, []any{float64(1700000000100)}, response.ResultTableOptions[healthyTable].SearchAfter)
			}
		})
	}
}

func TestQueryRawESBatchPartialShardResults(t *testing.T) {
	mock.Init()
	const (
		esURL     = "http://127.0.0.1:93052"
		storageID = "raw-es-batch-partial-shards"
	)
	tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: esURL})
	routes := []rawESBatchIntegrationRoute{
		{referenceName: "a", tableID: "trace.healthy", index: "trace_healthy"},
		{referenceName: "b", tableID: "trace.partial", index: "trace_partial"},
		{referenceName: "c", tableID: "trace.failed", index: "trace_failed"},
	}
	for _, route := range routes {
		registerRawESBatchMappingResponder(t, esURL, route.index)
	}
	oldSettings := queryRawESBatchSettingsSnapshot.Swap(&queryRawESBatchSettings{
		maxMembers: 16, maxBodyBytes: 1 << 20, maxConcurrentSearches: 4,
	})
	t.Cleanup(func() { queryRawESBatchSettingsSnapshot.Store(oldSettings) })
	batchResponse := `{"responses":[` +
		`{"status":200,"_shards":` + rawESHealthyShards + `,"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_id":"healthy-hit","_source":{"start_time":1700000000100}}]}},` +
		`{"status":200,"_shards":` + rawESPartialShards + `,"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_id":"partial-hit","_source":{"start_time":1700000000200}}]}},` +
		`{"status":503,"_shards":` + rawESPartialShards + `,"error":{"type":"search_phase_execution_exception"}}]}`

	query := rawESBatchIntegrationQuery(storageID, "synthetic-trace", routes...)
	query.IsESBatch = true
	enabled := true
	for _, tc := range []struct {
		name     string
		allow    *bool
		wantHits []string
	}{
		{name: "omitted", wantHits: []string{"healthy-hit"}},
		{name: "enabled", allow: &enabled, wantHits: []string{"healthy-hit", "partial-hit"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			httpmock.RegisterResponder(http.MethodGet, esURL+"/_msearch?max_concurrent_searches=4",
				func(req *http.Request) (*http.Response, error) {
					assertPartialESBatchNativeParameter(t, req, tc.allow != nil && *tc.allow)
					return httpmock.NewStringResponse(http.StatusOK, batchResponse), nil
				})
			recorder := runRawESPartialRequest(t, query, tc.allow)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var response ListData
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.EqualValues(t, len(tc.wantHits), response.Total)
			hits := make([]string, 0, len(response.List))
			for _, hit := range response.List {
				hits = append(hits, hit[metadata.KeyDocID].(string))
			}
			assert.ElementsMatch(t, tc.wantHits, hits)
			require.NotNil(t, response.Status)
			assert.Equal(t, metadata.QueryRawPartial, response.Status.Code)
			assert.Contains(t, response.Status.Message, "search_phase_execution_exception")
			if tc.allow != nil {
				assert.Contains(t, response.Status.Message, "部分分片结果")
			}
		})
	}
}

func assertPartialESNativeParameter(t *testing.T, req *http.Request, enabled bool) {
	t.Helper()
	if enabled {
		assert.Equal(t, "true", req.URL.Query().Get("allow_partial_search_results"))
	} else {
		assert.NotContains(t, req.URL.Query(), "allow_partial_search_results")
	}
}

func assertPartialESBatchNativeParameter(t *testing.T, req *http.Request, enabled bool) {
	t.Helper()
	scanner := bufio.NewScanner(req.Body)
	line := 0
	for scanner.Scan() {
		if line%2 == 0 {
			var header map[string]any
			assert.NoError(t, json.Unmarshal(scanner.Bytes(), &header))
			if enabled {
				assert.Equal(t, true, header["allow_partial_search_results"])
			} else {
				assert.NotContains(t, header, "allow_partial_search_results")
			}
		}
		line++
	}
	assert.NoError(t, scanner.Err())
	assert.Greater(t, line, 0)
}

func TestQueryRawESPartialShardResultsRejectConflicts(t *testing.T) {
	mock.Init()
	enabled := true
	for _, tc := range []struct {
		name  string
		query *structured.QueryTs
	}{
		{name: "exact_total", query: &structured.QueryTs{QueryList: []*structured.Query{{ExactTotal: true}}}},
		{name: "scroll", query: &structured.QueryTs{Scroll: "1m"}},
		{name: "scroll cursor", query: &structured.QueryTs{ResultTableOptions: metadata.ResultTableOptions{"table": {ScrollID: "test-cursor"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := runRawESPartialRequest(t, tc.query, &enabled)
			assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			assert.Contains(t, recorder.Body.String(), "allow_partial_es_results cannot be combined with")
		})
	}
}

func TestQueryRawESBatchPartialShardResultsTimeoutAndDecodeErrors(t *testing.T) {
	mock.Init()
	const (
		esURL       = "http://127.0.0.1:93055"
		storageID   = "raw-es-partial-timeout"
		firstIndex  = "raw_es_timeout_first"
		secondIndex = "raw_es_timeout_second"
	)
	tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: esURL})
	registerRawESBatchMappingResponder(t, esURL, firstIndex)
	registerRawESBatchMappingResponder(t, esURL, secondIndex)
	oldSettings := queryRawESBatchSettingsSnapshot.Swap(&queryRawESBatchSettings{
		maxMembers: 16, maxBodyBytes: 1 << 20, maxConcurrentSearches: 4,
	})
	t.Cleanup(func() { queryRawESBatchSettingsSnapshot.Store(oldSettings) })
	healthy := `{"status":200,"_shards":` + rawESHealthyShards + `,"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_id":"healthy-hit","_source":{"start_time":1700000000100}}]}}`
	timeout := `{"status":200,"timed_out":true,"_shards":` + rawESHealthyShards + `,"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_id":"timeout-hit","_source":{"start_time":1700000000200}}]}}`
	malformed := `{"status":200,"_shards":` + rawESPartialShards + `,"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_id":"malformed-hit","_source":"invalid log object"}]}}`
	enabled := true
	for _, tc := range []struct {
		name          string
		allow         *bool
		first, second string
		wantHits      int
		wantError     bool
		wantMessage   string
	}{
		{name: "legacy timeout", first: healthy, second: timeout, wantHits: 2},
		{name: "timed out sibling", allow: &enabled, first: healthy, second: timeout, wantHits: 1, wantMessage: "timeout"},
		{name: "all timed out", allow: &enabled, first: timeout, second: timeout, wantError: true},
		{name: "malformed partial sibling", allow: &enabled, first: healthy, second: malformed, wantHits: 1, wantMessage: "decode_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			httpmock.RegisterResponder(http.MethodGet, esURL+"/_msearch?max_concurrent_searches=4",
				func(req *http.Request) (*http.Response, error) {
					assertPartialESBatchNativeParameter(t, req, tc.allow != nil && *tc.allow)
					return httpmock.NewStringResponse(http.StatusOK, `{"responses":[`+tc.first+","+tc.second+"]}"), nil
				})
			query := rawESBatchExplicitTraceQuery(storageID, firstIndex, secondIndex, "synthetic-trace")
			query.IsESBatch = true
			recorder := runRawESPartialRequest(t, query, tc.allow)
			if tc.wantError {
				assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
				return
			}
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var response ListData
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Len(t, response.List, tc.wantHits)
			assert.EqualValues(t, tc.wantHits, response.Total)
			if tc.wantMessage != "" {
				require.NotNil(t, response.Status)
				assert.Equal(t, metadata.QueryRawPartial, response.Status.Code)
				assert.Contains(t, response.Status.Message, tc.wantMessage)
				assert.NotContains(t, response.Status.Message, "部分分片结果")
			} else {
				assert.Nil(t, response.Status)
			}
		})
	}
}
