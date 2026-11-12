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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

func TestQueryReferenceESPartialShardResults(t *testing.T) {
	mock.Init()
	promql.MockEngine()
	const (
		esURL     = "http://127.0.0.1:93053"
		storageID = "reference-es-partial-shards"
		index     = "reference_es_partial_shards"
	)
	tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: esURL})
	registerRawESBatchMappingResponder(t, esURL, index)
	for _, tc := range []struct {
		name        string
		enabled     bool
		shards      string
		instant     bool
		empty       bool
		timedOut    bool
		wantError   bool
		wantPartial bool
	}{
		{name: "omitted", shards: rawESPartialShards, wantError: true},
		{name: "range partial", enabled: true, shards: rawESPartialShards, wantPartial: true},
		{name: "instant partial", enabled: true, shards: rawESPartialShards, instant: true, wantPartial: true},
		{name: "range zero buckets", enabled: true, shards: rawESPartialShards, empty: true, wantPartial: true},
		{name: "instant zero buckets", enabled: true, shards: rawESPartialShards, instant: true, empty: true, wantPartial: true},
		{name: "healthy", enabled: true, shards: rawESHealthyShards},
		{name: "timed out without failed shards", enabled: true, shards: rawESHealthyShards, timedOut: true, wantError: true},
		{name: "timed out with failed shards", enabled: true, shards: rawESPartialShards, timedOut: true, wantError: true},
		{name: "all shards failed", enabled: true, shards: `{"total":8,"successful":0,"failed":8}`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buckets := `[{"key":1700000000000,"doc_count":7,"_value":{"value":7}}]`
			if tc.empty {
				buckets = "[]"
			}
			httpmock.RegisterResponder(http.MethodPost, esURL+"/"+index+"/_search", func(req *http.Request) (*http.Response, error) {
				assertPartialESNativeParameter(t, req, tc.enabled)
				response := fmt.Sprintf(`{"timed_out":%t,"_shards":%s,"aggregations":{"start_time":{"buckets":%s}}}`,
					tc.timedOut, tc.shards, buckets)
				return httpmock.NewStringResponse(http.StatusOK, response), nil
			})
			query := rawESBatchIntegrationQuery(storageID, "synthetic-trace", rawESBatchIntegrationRoute{
				referenceName: "a", tableID: "trace.reference", index: index,
			})
			query.AllowPartialESResults = tc.enabled
			query.MetricMerge = "a"
			query.Step = "1s"
			query.Instant = tc.instant
			query.QueryList[0].FieldName = "trace_id"
			query.QueryList[0].AggregateMethodList = structured.AggregateMethodList{
				{Method: "count"},
				{Method: "date_histogram", Window: "1s"},
			}
			ctx := metadata.InitHashID(context.Background())
			metadata.SetUser(ctx, &metadata.User{SpaceUID: query.SpaceUid})
			body, err := json.Marshal(query)
			require.NoError(t, err)
			request := httptest.NewRequest(http.MethodPost, "/query/ts/reference", bytes.NewReader(body)).WithContext(ctx)
			recorder := runCheckHandler(t, request, HandlerQueryReference)
			if tc.wantError {
				assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
				return
			}
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var response struct {
				IsPartial bool             `json:"is_partial"`
				Status    *metadata.Status `json:"status"`
				Series    []struct {
					Values [][]any `json:"values"`
				} `json:"series"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, tc.wantPartial, response.IsPartial)
			if tc.wantPartial {
				require.NotNil(t, response.Status)
				assert.Equal(t, metadata.QueryTsPartial, response.Status.Code)
			} else {
				assert.Nil(t, response.Status)
			}
			if tc.empty {
				assert.Empty(t, response.Series)
			} else {
				require.NotEmpty(t, response.Series)
				require.NotEmpty(t, response.Series[0].Values)
				assert.Contains(t, response.Series[0].Values[0], float64(7))
			}
		})
	}
}

func TestQueryReferenceESPartialShardResultsPreservesRoutingStatus(t *testing.T) {
	mock.Init()
	promql.MockEngine()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})
	metadata.SetStatus(ctx, metadata.SpaceTableIDFieldMissingFallback, "existing routing warning")
	tsdb.SetStorage("reference-es-partial-shards", &tsdb.Storage{
		Type: metadata.ElasticsearchStorageType, Address: "http://127.0.0.1:93053",
	})
	registerRawESBatchMappingResponder(t, "http://127.0.0.1:93053", "reference_es_partial_shards")
	query := rawESBatchIntegrationQuery("reference-es-partial-shards", "synthetic-trace", rawESBatchIntegrationRoute{
		referenceName: "a", tableID: "trace.reference", index: "reference_es_partial_shards",
	})
	query.AllowPartialESResults = true
	query.MetricMerge = "a"
	query.Step = "1s"
	query.Instant = true
	query.QueryList[0].FieldName = "trace_id"
	query.QueryList[0].AggregateMethodList = structured.AggregateMethodList{{Method: "count"}}
	httpmock.RegisterResponder(http.MethodPost, "http://127.0.0.1:93053/reference_es_partial_shards/_search",
		httpmock.NewStringResponder(http.StatusOK, `{"_shards":`+rawESPartialShards+`,"aggregations":{"_value":{"value":7}}}`))
	data, err := queryReferenceWithPromEngine(ctx, query)
	require.NoError(t, err)
	require.NotNil(t, data.Status)
	assert.Equal(t, metadata.QueryTsPartial, data.Status.Code)
	assert.Contains(t, data.Status.Message, "existing routing warning")
	assert.True(t, data.IsPartial)
}

func TestQueryReferenceESPartialShardResultsWithTimedOutSibling(t *testing.T) {
	mock.Init()
	promql.MockEngine()
	const (
		esURL        = "http://127.0.0.1:93054"
		storageID    = "reference-es-timeout-sibling"
		healthyIndex = "reference_es_healthy"
		timeoutIndex = "reference_es_timeout"
	)
	tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: esURL})
	registerRawESBatchMappingResponder(t, esURL, healthyIndex)
	registerRawESBatchMappingResponder(t, esURL, timeoutIndex)
	httpmock.RegisterResponder(http.MethodPost, esURL+"/"+healthyIndex+"/_search",
		httpmock.NewStringResponder(http.StatusOK, `{"_shards":`+rawESHealthyShards+`,"aggregations":{"_value":{"value":7}}}`))
	httpmock.RegisterResponder(http.MethodPost, esURL+"/"+timeoutIndex+"/_search",
		httpmock.NewStringResponder(http.StatusOK, `{"timed_out":true,"_shards":`+rawESHealthyShards+`,"aggregations":{"_value":{"value":1}}}`))
	query := rawESBatchDataLabelTraceQuery(storageID, healthyIndex, timeoutIndex, "synthetic-trace")
	query.AllowPartialESResults = true
	query.MetricMerge = "a"
	query.Step = "1s"
	query.Instant = true
	query.QueryList[0].FieldName = "trace_id"
	query.QueryList[0].AggregateMethodList = structured.AggregateMethodList{{Method: "count"}}
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: query.SpaceUid})
	body, err := json.Marshal(query)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/query/ts/reference", bytes.NewReader(body)).WithContext(ctx)
	recorder := runCheckHandler(t, request, HandlerQueryReference)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response struct {
		IsPartial bool             `json:"is_partial"`
		Status    *metadata.Status `json:"status"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.IsPartial)
	require.NotNil(t, response.Status)
	assert.Equal(t, metadata.QueryTsPartial, response.Status.Code)
	assert.Contains(t, response.Status.Message, "timed out")
	assert.NotContains(t, response.Status.Message, "部分分片结果")
}
