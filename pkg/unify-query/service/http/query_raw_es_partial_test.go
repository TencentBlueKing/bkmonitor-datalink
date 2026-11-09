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
	rawESPartialShards = `{"total":3,"successful":2,"skipped":1,"failed":1,"failures":[{"shard":2,"reason":{"type":"node_not_connected_exception","reason":"test shard unavailable"}}]}`
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits := `[{"_id":"partial-hit","_source":{"start_time":1700000000100},"sort":[1700000000100]}]`
			total := 1
			if tc.empty {
				hits, total = "[]", 0
			}
			httpmock.RegisterResponder(http.MethodPost, esURL+"/"+index+"/_search", func(req *http.Request) (*http.Response, error) {
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
	httpmock.RegisterResponder(http.MethodGet, esURL+"/_msearch?max_concurrent_searches=4",
		httpmock.NewStringResponder(http.StatusOK, `{"responses":[`+
			`{"status":200,"_shards":`+rawESHealthyShards+`,"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_id":"healthy-hit","_source":{"start_time":1700000000100}}]}},`+
			`{"status":200,"_shards":`+rawESPartialShards+`,"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_id":"partial-hit","_source":{"start_time":1700000000200}}]}},`+
			`{"status":503,"_shards":`+rawESPartialShards+`,"error":{"type":"search_phase_execution_exception"}}]}`))

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
