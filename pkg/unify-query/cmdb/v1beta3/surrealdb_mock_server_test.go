// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

type surrealQL string

const (
	tableMockBindingName      = "mock_graph_binding"
	tableMockBizID            = "mock_biz"
	tableMockSpaceUID         = "bkcc__" + tableMockBizID
	tableMockDatabase         = "mock_graph_result_table"
	tableMockNamespace        = "mock_ns"
	tableMockClusterName      = "mock_surrealdb_cluster"
	tableMockBindingCacheKey  = ":" + tableMockBizID
	tableMockUseNSDBStatement = "USE NS " + tableMockNamespace + " DB `" + tableMockDatabase + "`;"
)

func tableMockBindingInfo() *BindingInfo {
	return &BindingInfo{
		Name:        tableMockBindingName,
		BkBizID:     tableMockBizID,
		Database:    tableMockDatabase,
		Namespace:   tableMockNamespace,
		ClusterName: tableMockClusterName,
		Phase:       "Ok",
	}
}

type tableQuerySyncRequestRecord struct {
	Body       map[string]any
	SQLPayload BKBaseSQLPayload
}

type surrealDBMockServer struct {
	*httptest.Server
	mu                   sync.Mutex
	requests             []tableQuerySyncRequestRecord
	responsesBySurrealQL map[surrealQL]string
}

func newSurrealDBMockServer(t *testing.T, responsesBySurrealQL map[surrealQL]string) *surrealDBMockServer {
	t.Helper()

	mock := &surrealDBMockServer{
		responsesBySurrealQL: responsesBySurrealQL,
	}
	mock.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		sqlText, ok := body["sql"].(string)
		require.True(t, ok, "missing sql string")

		var payload BKBaseSQLPayload
		require.NoError(t, json.Unmarshal([]byte(sqlText), &payload))

		mock.mu.Lock()
		mock.requests = append(mock.requests, tableQuerySyncRequestRecord{
			Body:       body,
			SQLPayload: payload,
		})
		mock.mu.Unlock()

		responseJSON, ok := mock.responsesBySurrealQL[surrealQL(payload.DSL)]
		require.True(t, ok, "missing mock response for SurrealQL:\n%s", payload.DSL)

		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(responseJSON))
		require.NoError(t, err)
	}))
	return mock
}

func (s *surrealDBMockServer) Requests() []tableQuerySyncRequestRecord {
	s.mu.Lock()
	defer s.mu.Unlock()

	requests := make([]tableQuerySyncRequestRecord, 0, len(s.requests))
	for _, request := range s.requests {
		copiedBody := make(map[string]any, len(request.Body))
		for k, v := range request.Body {
			copiedBody[k] = v
		}
		requests = append(requests, tableQuerySyncRequestRecord{
			Body:       copiedBody,
			SQLPayload: request.SQLPayload,
		})
	}
	return requests
}

type tablePathSplitQueryResponse struct {
	QueryMode         string                             `json:"query_mode"`
	Path              []string                           `json:"path,omitempty"`
	Matchers          cmdb.Matchers                      `json:"matchers,omitempty"`
	RangeResult       []tableMatchersWithTimestampJSON   `json:"range_result,omitempty"`
	QuerySyncRequests []tableQuerySyncRequestSummaryJSON `json:"query_sync_requests,omitempty"`
}

type tableMatchersWithTimestampJSON struct {
	Timestamp int64         `json:"timestamp"`
	Matchers  cmdb.Matchers `json:"matchers"`
}

type tableQuerySyncRequestSummaryJSON struct {
	Path                 []string       `json:"path"`
	PreferStorage        string         `json:"prefer_storage"`
	Properties           map[string]any `json:"properties,omitempty"`
	ResultTableID        string         `json:"result_table_id"`
	ContainsRelations    []string       `json:"contains_relations"`
	NotContainsRelations []string       `json:"not_contains_relations"`
}

func tableRangeResultFromMatchersWithTimestamp(result []cmdb.MatchersWithTimestamp) []tableMatchersWithTimestampJSON {
	if len(result) == 0 {
		return nil
	}
	converted := make([]tableMatchersWithTimestampJSON, 0, len(result))
	for _, item := range result {
		converted = append(converted, tableMatchersWithTimestampJSON{
			Timestamp: item.Timestamp,
			Matchers:  item.Matchers,
		})
	}
	return converted
}

func encodeTablePathSplitQueryResponseJSON(t *testing.T, response tablePathSplitQueryResponse) string {
	t.Helper()

	data, err := json.Marshal(response)
	require.NoError(t, err)
	return string(data)
}

func tablePathSplitQuerySyncRequestSummaries(t *testing.T, requests []tableQuerySyncRequestRecord) []tableQuerySyncRequestSummaryJSON {
	t.Helper()

	summaries := make([]tableQuerySyncRequestSummaryJSON, 0, len(requests))
	for _, request := range requests {
		dsl := request.SQLPayload.DSL
		summary := tableQuerySyncRequestSummaryJSON{
			PreferStorage: stringValue(request.Body["prefer_storage"]),
			Properties:    mapValue(request.Body["properties"]),
			ResultTableID: request.SQLPayload.ResultTableID,
		}
		switch {
		case strings.Contains(dsl, string(RelationNodeWithPod)):
			summary.Path = []string{"node", "pod"}
			summary.ContainsRelations = []string{string(RelationNodeWithPod)}
			summary.NotContainsRelations = []string{string(RelationNodeWithSystem), string(RelationSystemToPod)}
		case strings.Contains(dsl, string(RelationNodeWithSystem)):
			summary.Path = []string{"node", "system", "pod"}
			summary.ContainsRelations = []string{string(RelationNodeWithSystem), string(RelationSystemToPod)}
			summary.NotContainsRelations = []string{string(RelationNodeWithPod)}
		default:
			t.Fatalf("unexpected split DSL without known path relation: %s", dsl)
		}
		for _, relation := range summary.ContainsRelations {
			assert.Contains(t, dsl, relation)
		}
		for _, relation := range summary.NotContainsRelations {
			assert.NotContains(t, dsl, relation)
		}
		assert.Contains(t, dsl, tableMockUseNSDBStatement)
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(i, j int) bool {
		return strings.Join(summaries[i].Path, "/") < strings.Join(summaries[j].Path, "/")
	})
	return summaries
}

func tablePathSplitBKBaseResponsesBySurrealQL(
	t *testing.T,
	req QueryRequest,
	provider SchemaProvider,
	responseOverridesByPath map[string]string,
) map[surrealQL]string {
	t.Helper()

	responsesByPath := tablePathSplitBKBaseResponsesByPath()
	for path, responseJSON := range responseOverridesByPath {
		responsesByPath[path] = responseJSON
	}

	req.Normalize()
	adjustMaxHopsForUnconstrainedPath(&req, provider)
	pf := NewPathFinder(
		WithAllowedCategories(req.AllowedRelationTypes...),
		WithDynamicDirection(req.DynamicRelationDirection),
		WithMaxHops(req.MaxHops),
		WithSchemaProvider(provider),
		WithNamespace(req.SchemaNamespace()),
	)
	paths, err := pf.FindAllPaths(req.SourceType, req.TargetType, req.PathResource)
	require.NoError(t, err)

	result := make(map[surrealQL]string, len(paths))
	for _, path := range sortPathsForQuery(paths) {
		pathKey := strings.Join(resourceTypesToPath(resourcePathTypes(path)), "/")
		responseJSON, ok := responsesByPath[pathKey]
		require.True(t, ok, "missing mock response for path %s", pathKey)

		dsl := NewSurrealQueryBuilderForPath(&req, provider, path).Build()
		finalDSL := tableMockUseNSDBStatement + dsl
		result[surrealQL(finalDSL)] = responseJSON
	}
	return result
}

func decodeTableQueryRequestJSON(t *testing.T, raw string) QueryRequest {
	t.Helper()

	var req QueryRequest
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	require.NoError(t, decoder.Decode(&req))

	var flags map[string]any
	decoder = json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	require.NoError(t, decoder.Decode(&flags))
	if _, ok := flags["target_type"]; ok {
		req.TargetTypeExplicit = true
	}
	if _, ok := flags["look_back_delta"]; ok {
		req.LookBackDeltaSet = true
	}
	return req
}

func setTableBKBaseQueryURLForTest(url string) func() {
	previous := BKBaseSurrealDBQueryURL
	BKBaseSurrealDBQueryURL = url
	return func() {
		BKBaseSurrealDBQueryURL = previous
	}
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func mapValue(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return nil
}

func tablePathSplitBKBaseResponsesByPath() map[string]string {
	return map[string]string{
		"node/pod":        tablePathSplitDirectPodBKBaseResponseJSON,
		"node/system/pod": tablePathSplitIndirectPodBKBaseResponseJSON,
	}
}

const tableEmptyBKBaseResponseJSON = `{"result":true,"code":"00","data":{"list":[]}}`

const tablePathSplitDirectPodBKBaseResponseJSON = `{"result":true,"code":"00","data":{"list":[{"result":{"root":{"entity_type":"node","entity_id":"node:node-1","entity_data":{"node":"node-1"},"liveness":[{"period_start":0,"period_end":600}]},"hop1":{"node_with_pod":[{"relation_type":"node_with_pod","relation_category":"static","relation_id":"node_with_pod:node-1||pod-1","relation_liveness":[{"period_start":0,"period_end":600000}],"target":{"entity_type":"pod","entity_id":"pod:pod-1","entity_data":{"pod":"pod-1"},"liveness":[{"period_start":0,"period_end":600}]}}]}}}]}}`

const tablePathSplitIndirectPodBKBaseResponseJSON = `{"result":true,"code":"00","data":{"list":[{"result":{"root":{"entity_type":"node","entity_id":"node:node-1","entity_data":{"node":"node-1"},"liveness":[{"period_start":0,"period_end":600}]},"hop1":{"node_with_system":[{"relation_type":"node_with_system","relation_category":"static","relation_id":"node_with_system:node-1||system-1","relation_liveness":[{"period_start":0,"period_end":600000}],"target":{"entity_type":"system","entity_id":"system:system-1","entity_data":{"system":"system-1"},"liveness":[{"period_start":0,"period_end":600}],"hop2":{"system_to_pod":[{"relation_type":"system_to_pod","relation_category":"static","relation_id":"system_to_pod:system-1||pod-via-system","relation_liveness":[{"period_start":0,"period_end":600000}],"target":{"entity_type":"pod","entity_id":"pod:pod-via-system","entity_data":{"pod":"pod-via-system"},"liveness":[{"period_start":0,"period_end":600}]}}]}}}]}}}]}}`
