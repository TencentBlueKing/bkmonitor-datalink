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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jarcoal/httpmock"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	queryPkg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

func TestQueryRawESBatchRequestOptIn(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})

	oldSettings := queryRawESBatchSettingsSnapshot.Load()
	queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{
		maxMembers:            16,
		maxBodyBytes:          1 << 20,
		maxConcurrentSearches: 4,
	})
	t.Cleanup(func() {
		queryRawESBatchSettingsSnapshot.Store(oldSettings)
	})

	const (
		esURL       = "http://127.0.0.1:93001"
		storageID   = "query-raw-es-batch-request-opt-in"
		firstIndex  = "trace_opt_in_first"
		secondIndex = "trace_opt_in_second"
		traceID     = "00000000000000000000000000000042"
	)
	tsdb.SetStorage(storageID, &tsdb.Storage{
		Type:    metadata.ElasticsearchStorageType,
		Address: esURL,
	})
	registerRawESBatchMappingResponder(t, esURL, firstIndex)
	registerRawESBatchMappingResponder(t, esURL, secondIndex)

	var multiSearchCalls, singleSearchCalls atomic.Int32
	httpmock.RegisterResponder(
		http.MethodGet,
		esURL+"/_msearch?max_concurrent_searches=4",
		func(*http.Request) (*http.Response, error) {
			multiSearchCalls.Add(1)
			return httpmock.NewStringResponse(
				http.StatusOK,
				`{"responses":[`+
					`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}},`+
					`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`+
					`]}`,
			), nil
		},
	)
	for _, index := range []string{firstIndex, secondIndex} {
		httpmock.RegisterResponder(
			http.MethodPost,
			esURL+"/"+index+"/_search",
			func(*http.Request) (*http.Response, error) {
				singleSearchCalls.Add(1)
				return httpmock.NewStringResponse(
					http.StatusOK,
					`{"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
				), nil
			},
		)
	}

	baseQuery := rawESBatchExplicitTraceQuery(storageID, firstIndex, secondIndex, traceID)
	content, err := json.Marshal(baseQuery)
	require.NoError(t, err)
	var requestBody map[string]any
	require.NoError(t, json.Unmarshal(content, &requestBody))
	requestBody["is_es_batch"] = true
	content, err = json.Marshal(requestBody)
	require.NoError(t, err)
	var queryTs structured.QueryTs
	require.NoError(t, json.Unmarshal(content, &queryTs))

	_, _, _, _, err = queryRawWithInstance(ctx, &queryTs)
	require.NoError(t, err)
	assert.EqualValues(t, 1, multiSearchCalls.Load())
	assert.Zero(t, singleSearchCalls.Load())
}

func TestQueryRawESBatchExplicitQueryListSameTraceCondition(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})
	metricBefore := queryRawESBatchIntegrationMetricSnapshot(t)

	oldSettings := queryRawESBatchSettingsSnapshot.Load()
	queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{
		maxMembers:            16,
		maxBodyBytes:          1 << 20,
		maxConcurrentSearches: 4,
	})
	t.Cleanup(func() {
		queryRawESBatchSettingsSnapshot.Store(oldSettings)
	})

	const (
		esURL       = "http://127.0.0.1:93002"
		storageID   = "query-raw-es-batch-integration"
		firstIndex  = "trace_explicit_first"
		secondIndex = "trace_explicit_second"
		traceID     = "00000000000000000000000000000042"
	)
	tsdb.SetStorage(storageID, &tsdb.Storage{
		Type:    metadata.ElasticsearchStorageType,
		Address: esURL,
	})
	registerRawESBatchMappingResponder(t, esURL, firstIndex)
	registerRawESBatchMappingResponder(t, esURL, secondIndex)

	var multiSearchCalls, singleSearchCalls atomic.Int32
	httpmock.RegisterResponder(
		http.MethodGet,
		esURL+"/_msearch?max_concurrent_searches=4",
		func(request *http.Request) (*http.Response, error) {
			multiSearchCalls.Add(1)
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			wireBody := string(body)
			assert.Contains(t, wireBody, `{"index":["`+firstIndex+`"]}`)
			assert.Contains(t, wireBody, `{"index":["`+secondIndex+`"]}`)
			assert.Equal(t, 2, strings.Count(wireBody, traceID))

			return httpmock.NewStringResponse(
				http.StatusOK,
				`{"responses":[`+
					`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"trace_explicit_first","_id":"first","_source":{"start_time":2,"trace_id":"`+traceID+`"}}]}},`+
					`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":2,"relation":"eq"},"hits":[{"_index":"trace_explicit_second","_id":"second","_source":{"start_time":1,"trace_id":"`+traceID+`"}}]}}`+
					`]}`,
			), nil
		},
	)
	for _, index := range []string{firstIndex, secondIndex} {
		httpmock.RegisterResponder(
			http.MethodPost,
			esURL+"/"+index+"/_search",
			func(request *http.Request) (*http.Response, error) {
				singleSearchCalls.Add(1)
				return httpmock.NewStringResponse(
					http.StatusInternalServerError,
					`{"error":{"type":"unexpected_single_search"}}`,
				), nil
			},
		)
	}

	queryTs := rawESBatchExplicitTraceQuery(storageID, firstIndex, secondIndex, traceID)
	queryTs.IsESBatch = true
	total, list, options, routeInfo, err := queryRawWithInstance(ctx, queryTs)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	require.Len(t, list, 2)
	assert.ElementsMatch(
		t,
		[]any{"trace.first", "trace.second"},
		[]any{list[0][metadata.KeyTableID], list[1][metadata.KeyTableID]},
	)
	assert.Len(t, options, 2)
	assert.Len(t, routeInfo, 2)
	assert.EqualValues(t, 1, multiSearchCalls.Load())
	assert.Zero(t, singleSearchCalls.Load())
	metricAfter := queryRawESBatchIntegrationMetricSnapshot(t)
	assert.Equal(
		t,
		metricBefore[metric.QueryRawESBatchEventPlannerEnabled]+1,
		metricAfter[metric.QueryRawESBatchEventPlannerEnabled],
	)
	assert.Equal(
		t,
		metricBefore[metric.QueryRawESBatchEventPlannerCandidate]+2,
		metricAfter[metric.QueryRawESBatchEventPlannerCandidate],
	)
	assert.Equal(
		t,
		metricBefore[metric.QueryRawESBatchEventPlannerPreGroup]+1,
		metricAfter[metric.QueryRawESBatchEventPlannerPreGroup],
	)
	assert.Equal(
		t,
		metricBefore[metric.QueryRawESBatchEventPlannerFinalGroup]+1,
		metricAfter[metric.QueryRawESBatchEventPlannerFinalGroup],
	)
	assert.Equal(
		t,
		metricBefore[metric.QueryRawESBatchEventPlannerBatch]+1,
		metricAfter[metric.QueryRawESBatchEventPlannerBatch],
	)
	assert.Equal(
		t,
		metricBefore[metric.QueryRawESBatchEventPlannerSingle],
		metricAfter[metric.QueryRawESBatchEventPlannerSingle],
	)
}

func TestQueryRawESBatchRequestOptInMatchesDefaultResponse(t *testing.T) {
	mock.Init()

	oldSettings := queryRawESBatchSettingsSnapshot.Load()
	t.Cleanup(func() {
		queryRawESBatchSettingsSnapshot.Store(oldSettings)
	})

	const (
		esURL       = "http://127.0.0.1:93009"
		storageID   = "query-raw-es-batch-equivalence"
		firstIndex  = "trace_equivalence_first"
		secondIndex = "trace_equivalence_second"
		traceID     = "00000000000000000000000000000042"
	)
	tsdb.SetStorage(storageID, &tsdb.Storage{
		Type:    metadata.ElasticsearchStorageType,
		Address: esURL,
	})
	for _, index := range []string{firstIndex, secondIndex} {
		registerRawESBatchMappingResponderWithProperties(
			t,
			esURL,
			index,
			`"start_time":{"type":"date"},"trace_id":{"type":"keyword"},"service_name":{"type":"keyword"}`,
			nil,
		)
	}

	firstResponse := `"status":200,"_shards":{"total":1,"successful":1,"failed":0},` +
		`"hits":{"total":4,"hits":[` +
		`{"_index":"` + firstIndex + `","_id":"first-high","_source":{"start_time":600,"trace_id":"` + traceID + `","service_name":"checkout"}},` +
		`{"_index":"` + firstIndex + `","_id":"first-mid","_source":{"start_time":300,"trace_id":"` + traceID + `","service_name":"checkout"}},` +
		`{"_index":"` + firstIndex + `","_id":"first-low","_source":{"start_time":100,"trace_id":"` + traceID + `","service_name":"checkout"}}]}`
	secondResponse := `"status":200,"_shards":{"total":1,"successful":1,"failed":0},` +
		`"hits":{"total":{"value":3,"relation":"eq"},"hits":[` +
		`{"_index":"` + secondIndex + `","_id":"second-high","_source":{"start_time":500,"trace_id":"` + traceID + `","service_name":"payment"}},` +
		`{"_index":"` + secondIndex + `","_id":"second-mid","_source":{"start_time":400,"trace_id":"` + traceID + `","service_name":"payment"}},` +
		`{"_index":"` + secondIndex + `","_id":"second-low","_source":{"start_time":200,"trace_id":"` + traceID + `","service_name":"payment"}}]}`

	var multiSearchCalls, singleSearchCalls atomic.Int32
	httpmock.RegisterResponder(
		http.MethodGet,
		esURL+"/_msearch?max_concurrent_searches=4",
		func(request *http.Request) (*http.Response, error) {
			multiSearchCalls.Add(1)
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			wireBody := string(body)
			assert.Equal(t, 2, strings.Count(wireBody, `"from":0`))
			assert.Equal(t, 2, strings.Count(wireBody, `"size":4`))
			return httpmock.NewStringResponse(
				http.StatusOK,
				`{"responses":[{`+firstResponse+`},{`+secondResponse+`}]}`,
			), nil
		},
	)
	for _, index := range []string{firstIndex, secondIndex} {
		index := index
		httpmock.RegisterResponder(
			http.MethodPost,
			esURL+"/"+index+"/_search",
			func(request *http.Request) (*http.Response, error) {
				singleSearchCalls.Add(1)
				body, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				assert.Contains(t, string(body), `"from":0`)
				assert.Contains(t, string(body), `"size":4`)
				response := secondResponse
				if index == firstIndex {
					response = firstResponse
				}
				return httpmock.NewStringResponse(http.StatusOK, `{`+response+`}`), nil
			},
		)
	}

	type rawResponse struct {
		total         int64
		list          []map[string]any
		options       metadata.ResultTableOptions
		resultTableID []string
		status        *metadata.Status
	}
	run := func(enabled bool) rawResponse {
		queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{
			maxMembers:            16,
			maxBodyBytes:          1 << 20,
			maxConcurrentSearches: 4,
		})
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})
		queryTs := rawESBatchExplicitTraceQuery(storageID, firstIndex, secondIndex, traceID)
		queryTs.IsESBatch = enabled
		queryTs.From = 1
		queryTs.Limit = 3
		queryTs.OrderBy = structured.OrderBy{
			"-start_time",
			metadata.KeyTableID,
			metadata.KeyDocID,
		}
		for _, queryItem := range queryTs.QueryList {
			queryItem.KeepColumns = []string{"start_time", "trace_id", "service_name"}
		}

		total, list, options, routeInfo, err := queryRawWithInstance(ctx, queryTs)
		require.NoError(t, err)
		return rawResponse{
			total:         total,
			list:          list,
			options:       options,
			resultTableID: resultTableIDFromRouteInfo(routeInfo),
			status:        metadata.GetStatus(ctx),
		}
	}

	disabled := run(false)
	wireChildSuccessBefore := queryRawESBatchIntegrationMetricSnapshot(t)[metric.QueryRawESBatchEventWireChildSuccess]
	enabled := run(true)
	wireChildSuccessAfter := queryRawESBatchIntegrationMetricSnapshot(t)[metric.QueryRawESBatchEventWireChildSuccess]

	assert.Equal(t, disabled.total, enabled.total)
	assert.Equal(t, disabled.list, enabled.list)
	assert.Equal(t, disabled.options, enabled.options)
	assert.Equal(t, disabled.resultTableID, enabled.resultTableID)
	assert.Equal(t, disabled.status, enabled.status)

	assert.EqualValues(t, 7, enabled.total)
	require.Len(t, enabled.list, 3)
	assert.Equal(
		t,
		[]any{"second-high", "second-mid", "first-mid"},
		[]any{
			enabled.list[0][metadata.KeyDocID],
			enabled.list[1][metadata.KeyDocID],
			enabled.list[2][metadata.KeyDocID],
		},
	)
	assert.Equal(
		t,
		[]any{"trace.second", "trace.second", "trace.first"},
		[]any{
			enabled.list[0][metadata.KeyTableID],
			enabled.list[1][metadata.KeyTableID],
			enabled.list[2][metadata.KeyTableID],
		},
	)
	assert.Equal(
		t,
		[]any{"payment", "payment", "checkout"},
		[]any{
			enabled.list[0]["service_name"],
			enabled.list[1]["service_name"],
			enabled.list[2]["service_name"],
		},
	)
	assert.Equal(
		t,
		[]any{secondIndex, secondIndex, firstIndex},
		[]any{
			enabled.list[0][metadata.KeyIndex],
			enabled.list[1][metadata.KeyIndex],
			enabled.list[2][metadata.KeyIndex],
		},
	)
	assert.Equal(
		t,
		[]any{traceID, traceID, traceID},
		[]any{
			enabled.list[0]["trace_id"],
			enabled.list[1]["trace_id"],
			enabled.list[2]["trace_id"],
		},
	)
	assert.EqualValues(t, 500, enabled.list[0]["start_time"])
	assert.EqualValues(t, 400, enabled.list[1]["start_time"])
	assert.EqualValues(t, 300, enabled.list[2]["start_time"])
	require.Len(t, enabled.options, 2)
	for _, tableUUID := range []string{"trace.first|" + storageID, "trace.second|" + storageID} {
		option := enabled.options.GetOption(tableUUID)
		require.NotNil(t, option)
		require.NotNil(t, option.From)
		assert.Zero(t, *option.From)
		assert.Empty(t, option.SearchAfter)
	}
	assert.Equal(
		t,
		[]string{"trace.first", "trace.second"},
		enabled.resultTableID,
	)
	assert.Nil(t, enabled.status)
	assert.EqualValues(t, 2, singleSearchCalls.Load())
	assert.EqualValues(t, 1, multiSearchCalls.Load())
	assert.Equal(t, wireChildSuccessBefore+2, wireChildSuccessAfter)
}

func TestQueryRawESBatchSingleDataLabelExpandsToSameBatch(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})

	oldSettings := queryRawESBatchSettingsSnapshot.Load()
	t.Cleanup(func() {
		queryRawESBatchSettingsSnapshot.Store(oldSettings)
	})

	const (
		esURL       = "http://127.0.0.1:93002"
		storageID   = "query-raw-es-batch-data-label"
		firstIndex  = "trace_data_label_first"
		secondIndex = "trace_data_label_second"
		traceID     = "00000000000000000000000000000042"
	)
	tsdb.SetStorage(storageID, &tsdb.Storage{
		Type:    metadata.ElasticsearchStorageType,
		Address: esURL,
	})
	queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{
		maxMembers:            16,
		maxBodyBytes:          1 << 20,
		maxConcurrentSearches: 4,
	})
	registerRawESBatchMappingResponder(t, esURL, firstIndex)
	registerRawESBatchMappingResponder(t, esURL, secondIndex)

	var multiSearchCalls, singleSearchCalls atomic.Int32
	httpmock.RegisterResponder(
		http.MethodGet,
		esURL+"/_msearch?max_concurrent_searches=4",
		func(request *http.Request) (*http.Response, error) {
			multiSearchCalls.Add(1)
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			assert.Equal(t, 2, strings.Count(string(body), traceID))
			return httpmock.NewStringResponse(
				http.StatusOK,
				`{"responses":[`+
					`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"`+firstIndex+`","_id":"first","_source":{"start_time":2,"trace_id":"`+traceID+`"}}]}},`+
					`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":2,"relation":"eq"},"hits":[{"_index":"`+secondIndex+`","_id":"second","_source":{"start_time":1,"trace_id":"`+traceID+`"}}]}}`+
					`]}`,
			), nil
		},
	)
	for _, index := range []string{firstIndex, secondIndex} {
		httpmock.RegisterResponder(
			http.MethodPost,
			esURL+"/"+index+"/_search",
			func(*http.Request) (*http.Response, error) {
				singleSearchCalls.Add(1)
				return httpmock.NewStringResponse(
					http.StatusInternalServerError,
					`{"error":{"type":"unexpected_single_search"}}`,
				), nil
			},
		)
	}

	queryTs := rawESBatchDataLabelTraceQuery(storageID, firstIndex, secondIndex, traceID)
	queryTs.IsESBatch = true
	require.Len(t, queryTs.QueryList, 1)
	total, list, options, routeInfo, err := queryRawWithInstance(ctx, queryTs)

	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	require.Len(t, list, 2)
	assert.ElementsMatch(
		t,
		[]any{"trace.data_label.first", "trace.data_label.second"},
		[]any{list[0][metadata.KeyTableID], list[1][metadata.KeyTableID]},
	)
	assert.Len(t, options, 2)
	assert.Len(t, routeInfo, 2)
	assert.EqualValues(t, 1, multiSearchCalls.Load())
	assert.Zero(t, singleSearchCalls.Load())
}

func TestQueryRawESBatchExplicitQueryListDifferentConditionsStaySeparate(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})
	metricBefore := queryRawESBatchIntegrationMetricSnapshot(t)

	oldSettings := queryRawESBatchSettingsSnapshot.Load()
	t.Cleanup(func() {
		queryRawESBatchSettingsSnapshot.Store(oldSettings)
	})

	const (
		esURL       = "http://127.0.0.1:93002"
		storageID   = "query-raw-es-batch-different-conditions"
		firstIndex  = "trace_condition_first"
		secondIndex = "trace_condition_second"
		traceID     = "00000000000000000000000000000042"
	)
	tsdb.SetStorage(storageID, &tsdb.Storage{
		Type:    metadata.ElasticsearchStorageType,
		Address: esURL,
	})
	queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{
		maxMembers:            16,
		maxBodyBytes:          1 << 20,
		maxConcurrentSearches: 4,
	})
	registerRawESBatchMappingResponder(t, esURL, firstIndex)
	registerRawESBatchMappingResponder(t, esURL, secondIndex)

	var multiSearchCalls, singleSearchCalls atomic.Int32
	httpmock.RegisterResponder(
		http.MethodGet,
		esURL+"/_msearch?max_concurrent_searches=4",
		func(request *http.Request) (*http.Response, error) {
			multiSearchCalls.Add(1)
			return httpmock.NewStringResponse(
				http.StatusInternalServerError,
				`{"error":{"type":"unexpected_multi_search"}}`,
			), nil
		},
	)
	for _, index := range []string{firstIndex, secondIndex} {
		index := index
		httpmock.RegisterResponder(
			http.MethodPost,
			esURL+"/"+index+"/_search",
			func(request *http.Request) (*http.Response, error) {
				singleSearchCalls.Add(1)
				return httpmock.NewStringResponse(
					http.StatusOK,
					`{"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"`+index+`","_id":"`+index+`","_source":{"start_time":1,"trace_id":"`+traceID+`"}}]}}`,
				), nil
			},
		)
	}

	queryTs := rawESBatchExplicitTraceQuery(storageID, firstIndex, secondIndex, traceID)
	queryTs.IsESBatch = true
	queryTs.QueryList[1].Conditions.FieldList[0].Value = []string{"another-trace-id"}
	total, list, options, routeInfo, err := queryRawWithInstance(ctx, queryTs)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, list, 2)
	assert.Len(t, options, 2)
	assert.Len(t, routeInfo, 2)
	assert.Zero(t, multiSearchCalls.Load())
	assert.EqualValues(t, 2, singleSearchCalls.Load())
	metricAfter := queryRawESBatchIntegrationMetricSnapshot(t)
	assert.Equal(
		t,
		metricBefore[metric.QueryRawESBatchEventPlannerEnabled]+1,
		metricAfter[metric.QueryRawESBatchEventPlannerEnabled],
	)
	assert.Equal(
		t,
		metricBefore[metric.QueryRawESBatchEventPlannerCandidate]+2,
		metricAfter[metric.QueryRawESBatchEventPlannerCandidate],
	)
	assert.Equal(
		t,
		metricBefore[metric.QueryRawESBatchEventPlannerPreGroup]+2,
		metricAfter[metric.QueryRawESBatchEventPlannerPreGroup],
	)
	assert.Equal(
		t,
		metricBefore[metric.QueryRawESBatchEventPlannerFinalGroup],
		metricAfter[metric.QueryRawESBatchEventPlannerFinalGroup],
	)
	assert.Equal(
		t,
		metricBefore[metric.QueryRawESBatchEventPlannerBatch],
		metricAfter[metric.QueryRawESBatchEventPlannerBatch],
	)
	assert.Equal(
		t,
		metricBefore[metric.QueryRawESBatchEventPlannerSingle]+2,
		metricAfter[metric.QueryRawESBatchEventPlannerSingle],
	)
}

func TestQueryRawESBatchChildFailurePreservesSuccessfulSibling(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})

	oldSettings := queryRawESBatchSettingsSnapshot.Load()
	t.Cleanup(func() {
		queryRawESBatchSettingsSnapshot.Store(oldSettings)
	})

	const (
		esURL       = "http://127.0.0.1:93003"
		storageID   = "query-raw-es-batch-child-partial"
		firstIndex  = "trace_partial_success"
		secondIndex = "trace_partial_failure"
		traceID     = "00000000000000000000000000000042"
	)
	tsdb.SetStorage(storageID, &tsdb.Storage{
		Type:    metadata.ElasticsearchStorageType,
		Address: esURL,
	})
	queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{
		maxMembers:            16,
		maxBodyBytes:          1 << 20,
		maxConcurrentSearches: 4,
	})
	registerRawESBatchMappingResponder(t, esURL, firstIndex)
	registerRawESBatchMappingResponder(t, esURL, secondIndex)

	var multiSearchCalls, singleSearchCalls atomic.Int32
	httpmock.RegisterResponder(
		http.MethodGet,
		esURL+"/_msearch?max_concurrent_searches=4",
		func(*http.Request) (*http.Response, error) {
			multiSearchCalls.Add(1)
			return httpmock.NewStringResponse(
				http.StatusOK,
				`{"responses":[`+
					`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":7,"relation":"eq"},"hits":[{"_index":"`+firstIndex+`","_id":"success","_source":{"start_time":2,"trace_id":"`+traceID+`"}}]}},`+
					`{"status":400,"error":{"type":"query_shard_exception","reason":"must-not-leak-`+secondIndex+`-`+traceID+`"}}`+
					`]}`,
			), nil
		},
	)
	for _, index := range []string{firstIndex, secondIndex} {
		httpmock.RegisterResponder(
			http.MethodPost,
			esURL+"/"+index+"/_search",
			func(*http.Request) (*http.Response, error) {
				singleSearchCalls.Add(1)
				return httpmock.NewStringResponse(
					http.StatusInternalServerError,
					`{"error":{"type":"unexpected_single_search"}}`,
				), nil
			},
		)
	}

	queryTs := rawESBatchExplicitTraceQuery(storageID, firstIndex, secondIndex, traceID)
	queryTs.IsESBatch = true
	total, list, options, routeInfo, err := queryRawWithInstance(ctx, queryTs)

	require.NoError(t, err)
	assert.EqualValues(t, 7, total)
	require.Len(t, list, 1)
	assert.Equal(t, "trace.first", list[0][metadata.KeyTableID])
	assert.Contains(t, options, "trace.first|"+storageID)
	assert.NotContains(t, options, "trace.second|"+storageID)
	assert.ElementsMatch(t, []string{"trace.first", "trace.second"}, resultTableIDFromRouteInfo(routeInfo))
	assert.EqualValues(t, 1, multiSearchCalls.Load())
	assert.Zero(t, singleSearchCalls.Load(), "successful child and failed child must not be replayed as single searches")

	status := metadata.GetStatus(ctx)
	require.NotNil(t, status)
	assert.Equal(t, metadata.QueryRawPartial, status.Code)
	assert.Contains(t, status.Message, "query_shard_exception")
	assert.NotContains(t, status.Message, secondIndex)
	assert.NotContains(t, status.Message, traceID)
	assert.Equal(t, 1, strings.Count(status.Message, "elasticsearch raw batch child failed"))
}

func TestQueryRawESBatchTransportFailureConvergesOnceWithoutFanout(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})

	oldSettings := queryRawESBatchSettingsSnapshot.Load()
	t.Cleanup(func() {
		queryRawESBatchSettingsSnapshot.Store(oldSettings)
	})

	const (
		esURL       = "http://127.0.0.1:93004"
		storageID   = "query-raw-es-batch-transport-failure"
		firstIndex  = "trace_transport_first"
		secondIndex = "trace_transport_second"
		traceID     = "00000000000000000000000000000042"
	)
	tsdb.SetStorage(storageID, &tsdb.Storage{
		Type:    metadata.ElasticsearchStorageType,
		Address: esURL,
	})
	queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{
		maxMembers:            16,
		maxBodyBytes:          1 << 20,
		maxConcurrentSearches: 4,
	})
	registerRawESBatchMappingResponder(t, esURL, firstIndex)
	registerRawESBatchMappingResponder(t, esURL, secondIndex)

	var multiSearchCalls, singleSearchCalls atomic.Int32
	httpmock.RegisterResponder(
		http.MethodGet,
		esURL+"/_msearch?max_concurrent_searches=4",
		func(*http.Request) (*http.Response, error) {
			multiSearchCalls.Add(1)
			return httpmock.NewStringResponse(
				http.StatusServiceUnavailable,
				`{"error":{"type":"unavailable","reason":"must-not-leak-`+traceID+`"}}`,
			), nil
		},
	)
	for _, index := range []string{firstIndex, secondIndex} {
		httpmock.RegisterResponder(
			http.MethodPost,
			esURL+"/"+index+"/_search",
			func(*http.Request) (*http.Response, error) {
				singleSearchCalls.Add(1)
				return httpmock.NewStringResponse(
					http.StatusInternalServerError,
					`{"error":{"type":"unexpected_single_search"}}`,
				), nil
			},
		)
	}

	queryTs := rawESBatchExplicitTraceQuery(storageID, firstIndex, secondIndex, traceID)
	queryTs.IsESBatch = true
	total, list, options, routeInfo, err := queryRawWithInstance(ctx, queryTs)

	require.Error(t, err)
	assert.Zero(t, total)
	assert.Empty(t, list)
	assert.Empty(t, options)
	assert.ElementsMatch(t, []string{"trace.first", "trace.second"}, resultTableIDFromRouteInfo(routeInfo))
	assert.EqualValues(t, 1, multiSearchCalls.Load())
	assert.Zero(t, singleSearchCalls.Load())
	assert.Equal(t, 1, strings.Count(err.Error(), "elasticsearch raw batch transport failed"))
	assert.NotContains(t, err.Error(), traceID)
	assert.NotContains(t, err.Error(), firstIndex)
	assert.NotContains(t, err.Error(), secondIndex)
}

func TestQueryRawESBatchFinalBodyDifferenceUsesPreparedSingles(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})

	oldSettings := queryRawESBatchSettingsSnapshot.Load()
	t.Cleanup(func() {
		queryRawESBatchSettingsSnapshot.Store(oldSettings)
	})

	const (
		esURL         = "http://127.0.0.1:93005"
		storageID     = "query-raw-es-batch-final-body"
		firstIndex    = "trace_final_keyword"
		secondIndex   = "trace_final_date"
		conditionTime = "2025-03-08T12:34:56Z"
	)
	tsdb.SetStorage(storageID, &tsdb.Storage{
		Type:    metadata.ElasticsearchStorageType,
		Address: esURL,
	})
	queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{
		maxMembers:            16,
		maxBodyBytes:          1 << 20,
		maxConcurrentSearches: 4,
	})
	registerRawESBatchMappingResponderWithProperties(
		t,
		esURL,
		firstIndex,
		`"start_time":{"type":"date"},"event_time":{"type":"keyword"}`,
		nil,
	)
	registerRawESBatchMappingResponderWithProperties(
		t,
		esURL,
		secondIndex,
		`"start_time":{"type":"date"},"event_time":{"type":"date"}`,
		nil,
	)

	var multiSearchCalls, singleSearchCalls atomic.Int32
	httpmock.RegisterResponder(
		http.MethodGet,
		esURL+"/_msearch?max_concurrent_searches=4",
		func(*http.Request) (*http.Response, error) {
			multiSearchCalls.Add(1)
			return httpmock.NewStringResponse(
				http.StatusInternalServerError,
				`{"error":{"type":"unexpected_multi_search"}}`,
			), nil
		},
	)

	var (
		bodyLock   sync.Mutex
		searchBody = make(map[string]string, 2)
	)
	for _, index := range []string{firstIndex, secondIndex} {
		index := index
		httpmock.RegisterResponder(
			http.MethodPost,
			esURL+"/"+index+"/_search",
			func(request *http.Request) (*http.Response, error) {
				singleSearchCalls.Add(1)
				body, readErr := io.ReadAll(request.Body)
				assert.NoError(t, readErr)
				bodyLock.Lock()
				searchBody[index] = string(body)
				bodyLock.Unlock()
				return httpmock.NewStringResponse(
					http.StatusOK,
					`{"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"`+index+`","_id":"`+index+`","_source":{"start_time":1,"event_time":"`+conditionTime+`"}}]}}`,
				), nil
			},
		)
	}

	queryTs := rawESBatchExplicitTraceQuery(storageID, firstIndex, secondIndex, conditionTime)
	queryTs.IsESBatch = true
	for _, queryItem := range queryTs.QueryList {
		queryItem.KeepColumns = []string{"start_time", "event_time"}
		queryItem.Conditions.FieldList[0].DimensionName = "event_time"
	}
	total, list, options, routeInfo, err := queryRawWithInstance(ctx, queryTs)

	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, list, 2)
	assert.Len(t, options, 2)
	assert.Len(t, routeInfo, 2)
	assert.Zero(t, multiSearchCalls.Load())
	assert.EqualValues(t, 2, singleSearchCalls.Load())
	bodyLock.Lock()
	firstBody := searchBody[firstIndex]
	secondBody := searchBody[secondIndex]
	bodyLock.Unlock()
	require.NotEmpty(t, firstBody)
	require.NotEmpty(t, secondBody)
	assert.NotEqual(t, firstBody, secondBody, "mapping-derived final query bodies must be split")
	assert.Contains(t, firstBody, conditionTime)
	assert.NotContains(t, secondBody, conditionTime)
}

func TestQueryRawESBatchPackingFallsBackToPreparedSingles(t *testing.T) {
	t.Run("max_members_tail_singleton", func(t *testing.T) {
		mock.Init()
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})

		oldSettings := queryRawESBatchSettingsSnapshot.Load()
		t.Cleanup(func() {
			queryRawESBatchSettingsSnapshot.Store(oldSettings)
		})

		const (
			esURL     = "http://127.0.0.1:93006"
			storageID = "query-raw-es-batch-max-members-tail"
			traceID   = "00000000000000000000000000000042"
		)
		routes := []rawESBatchIntegrationRoute{
			{referenceName: "a", tableID: "trace.first", index: "trace_pack_first"},
			{referenceName: "b", tableID: "trace.second", index: "trace_pack_second"},
			{referenceName: "c", tableID: "trace.third", index: "trace_pack_third"},
		}
		tsdb.SetStorage(storageID, &tsdb.Storage{
			Type:    metadata.ElasticsearchStorageType,
			Address: esURL,
		})
		queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{
			maxMembers:            2,
			maxBodyBytes:          1 << 20,
			maxConcurrentSearches: 4,
		})

		mappingCalls := make([]atomic.Int32, len(routes))
		for index, route := range routes {
			registerRawESBatchMappingResponderWithProperties(
				t,
				esURL,
				route.index,
				`"start_time":{"type":"date"},"trace_id":{"type":"keyword"}`,
				&mappingCalls[index],
			)
		}

		var multiSearchCalls, singleSearchCalls atomic.Int32
		httpmock.RegisterResponder(
			http.MethodGet,
			esURL+"/_msearch?max_concurrent_searches=4",
			func(*http.Request) (*http.Response, error) {
				multiSearchCalls.Add(1)
				return httpmock.NewStringResponse(
					http.StatusOK,
					`{"responses":[`+
						`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"trace_pack_first","_id":"first","_source":{"start_time":3,"trace_id":"`+traceID+`"}}]}},`+
						`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"trace_pack_second","_id":"second","_source":{"start_time":2,"trace_id":"`+traceID+`"}}]}}`+
						`]}`,
				), nil
			},
		)
		for _, route := range routes {
			route := route
			httpmock.RegisterResponder(
				http.MethodPost,
				esURL+"/"+route.index+"/_search",
				func(*http.Request) (*http.Response, error) {
					singleSearchCalls.Add(1)
					if route.tableID != "trace.third" {
						return httpmock.NewStringResponse(
							http.StatusInternalServerError,
							`{"error":{"type":"unexpected_single_search"}}`,
						), nil
					}
					return httpmock.NewStringResponse(
						http.StatusOK,
						`{"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"`+route.index+`","_id":"third","_source":{"start_time":1,"trace_id":"`+traceID+`"}}]}}`,
					), nil
				},
			)
		}

		queryTs := rawESBatchIntegrationQuery(storageID, traceID, routes...)
		queryTs.IsESBatch = true
		total, list, options, routeInfo, err := queryRawWithInstance(ctx, queryTs)

		require.NoError(t, err)
		assert.EqualValues(t, 3, total)
		assert.Len(t, list, 3)
		assert.Len(t, options, 3)
		assert.Len(t, routeInfo, 3)
		assert.EqualValues(t, 1, multiSearchCalls.Load())
		assert.EqualValues(t, 1, singleSearchCalls.Load())
		for index := range mappingCalls {
			assert.EqualValues(t, 1, mappingCalls[index].Load(), "prepared singleton must not repeat mapping preparation")
		}
	})

	t.Run("max_body_oversized_members", func(t *testing.T) {
		mock.Init()
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})

		oldSettings := queryRawESBatchSettingsSnapshot.Load()
		t.Cleanup(func() {
			queryRawESBatchSettingsSnapshot.Store(oldSettings)
		})

		const (
			esURL     = "http://127.0.0.1:93007"
			storageID = "query-raw-es-batch-oversized"
			traceID   = "00000000000000000000000000000042"
		)
		routes := []rawESBatchIntegrationRoute{
			{referenceName: "a", tableID: "trace.first", index: "trace_oversized_first"},
			{referenceName: "b", tableID: "trace.second", index: "trace_oversized_second"},
		}
		tsdb.SetStorage(storageID, &tsdb.Storage{
			Type:    metadata.ElasticsearchStorageType,
			Address: esURL,
		})
		queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{
			maxMembers:            16,
			maxBodyBytes:          1,
			maxConcurrentSearches: 4,
		})

		mappingCalls := make([]atomic.Int32, len(routes))
		for index, route := range routes {
			registerRawESBatchMappingResponderWithProperties(
				t,
				esURL,
				route.index,
				`"start_time":{"type":"date"},"trace_id":{"type":"keyword"}`,
				&mappingCalls[index],
			)
		}

		var multiSearchCalls, singleSearchCalls atomic.Int32
		httpmock.RegisterResponder(
			http.MethodGet,
			esURL+"/_msearch?max_concurrent_searches=4",
			func(*http.Request) (*http.Response, error) {
				multiSearchCalls.Add(1)
				return httpmock.NewStringResponse(
					http.StatusInternalServerError,
					`{"error":{"type":"unexpected_multi_search"}}`,
				), nil
			},
		)
		for _, route := range routes {
			route := route
			httpmock.RegisterResponder(
				http.MethodPost,
				esURL+"/"+route.index+"/_search",
				func(*http.Request) (*http.Response, error) {
					singleSearchCalls.Add(1)
					return httpmock.NewStringResponse(
						http.StatusOK,
						`{"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"`+route.index+`","_id":"`+route.referenceName+`","_source":{"start_time":1,"trace_id":"`+traceID+`"}}]}}`,
					), nil
				},
			)
		}

		queryTs := rawESBatchIntegrationQuery(storageID, traceID, routes...)
		queryTs.IsESBatch = true
		total, list, options, routeInfo, err := queryRawWithInstance(ctx, queryTs)

		require.NoError(t, err)
		assert.EqualValues(t, 2, total)
		assert.Len(t, list, 2)
		assert.Len(t, options, 2)
		assert.Len(t, routeInfo, 2)
		assert.Zero(t, multiSearchCalls.Load())
		assert.EqualValues(t, 2, singleSearchCalls.Load())
		for index := range mappingCalls {
			assert.EqualValues(t, 1, mappingCalls[index].Load(), "oversized prepared member must not repeat mapping preparation")
		}
	})
}

func TestQueryRawESBatchHighlightReusesPreparedFieldMappings(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})

	oldSettings := queryRawESBatchSettingsSnapshot.Load()
	t.Cleanup(func() {
		queryRawESBatchSettingsSnapshot.Store(oldSettings)
	})

	const (
		esURL       = "http://127.0.0.1:93008"
		storageID   = "query-raw-es-batch-highlight"
		firstIndex  = "trace_highlight_first"
		secondIndex = "trace_highlight_second"
		keyword     = "needle"
	)
	tsdb.SetStorage(storageID, &tsdb.Storage{
		Type:    metadata.ElasticsearchStorageType,
		Address: esURL,
	})
	queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{
		maxMembers:            16,
		maxBodyBytes:          1 << 20,
		maxConcurrentSearches: 4,
	})

	var firstMappingCalls, secondMappingCalls atomic.Int32
	registerRawESBatchMappingResponderWithProperties(
		t,
		esURL,
		firstIndex,
		`"start_time":{"type":"date"},"log":{"type":"text"}`,
		&firstMappingCalls,
	)
	registerRawESBatchMappingResponderWithProperties(
		t,
		esURL,
		secondIndex,
		`"start_time":{"type":"date"},"log":{"type":"text"}`,
		&secondMappingCalls,
	)

	var multiSearchCalls, singleSearchCalls atomic.Int32
	httpmock.RegisterResponder(
		http.MethodGet,
		esURL+"/_msearch?max_concurrent_searches=4",
		func(*http.Request) (*http.Response, error) {
			multiSearchCalls.Add(1)
			return httpmock.NewStringResponse(
				http.StatusOK,
				`{"responses":[`+
					`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"`+firstIndex+`","_id":"first","_source":{"start_time":2,"log":"Needle first"}}]}},`+
					`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"`+secondIndex+`","_id":"second","_source":{"start_time":1,"log":"second needle"}}]}}`+
					`]}`,
			), nil
		},
	)
	for _, index := range []string{firstIndex, secondIndex} {
		httpmock.RegisterResponder(
			http.MethodPost,
			esURL+"/"+index+"/_search",
			func(*http.Request) (*http.Response, error) {
				singleSearchCalls.Add(1)
				return httpmock.NewStringResponse(
					http.StatusInternalServerError,
					`{"error":{"type":"unexpected_single_search"}}`,
				), nil
			},
		)
	}

	queryTs := rawESBatchExplicitTraceQuery(storageID, firstIndex, secondIndex, keyword)
	queryTs.IsESBatch = true
	queryTs.HighLight = &metadata.HighLight{Enable: true}
	for _, queryItem := range queryTs.QueryList {
		queryItem.KeepColumns = []string{"start_time", "log"}
		queryItem.Conditions.FieldList[0].DimensionName = "log"
	}
	total, list, options, routeInfo, err := queryRawWithInstance(ctx, queryTs)

	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, list, 2)
	assert.Len(t, options, 2)
	assert.Len(t, routeInfo, 2)
	assert.EqualValues(t, 1, multiSearchCalls.Load())
	assert.Zero(t, singleSearchCalls.Load())
	assert.EqualValues(t, 1, firstMappingCalls.Load())
	assert.EqualValues(t, 1, secondMappingCalls.Load())

	highlightByTable := make(map[string]any, len(list))
	for _, item := range list {
		highlightByTable[item[metadata.KeyTableID].(string)] = item[function.KeyHighLight]
	}
	assert.Equal(
		t,
		map[string]any{"log": []string{"<mark>Needle</mark> first"}},
		highlightByTable["trace.first"],
	)
	assert.Equal(
		t,
		map[string]any{"log": []string{"second <mark>needle</mark>"}},
		highlightByTable["trace.second"],
	)
}

func registerRawESBatchMappingResponder(t *testing.T, esURL, index string) {
	t.Helper()
	registerRawESBatchMappingResponderWithProperties(
		t,
		esURL,
		index,
		`"start_time":{"type":"date"},"trace_id":{"type":"keyword"}`,
		nil,
	)
}

func registerRawESBatchMappingResponderWithProperties(
	t *testing.T,
	esURL, index, properties string,
	calls *atomic.Int32,
) {
	t.Helper()
	httpmock.RegisterResponder(
		http.MethodGet,
		esURL+"/"+index,
		func(*http.Request) (*http.Response, error) {
			if calls != nil {
				calls.Add(1)
			}
			return httpmock.NewStringResponse(
				http.StatusOK,
				`{"`+index+`":{"settings":{},"mappings":{"properties":{`+properties+`}}}}`,
			), nil
		},
	)
}

func rawESBatchExplicitTraceQuery(storageID, firstIndex, secondIndex, traceID string) *structured.QueryTs {
	return rawESBatchIntegrationQuery(
		storageID,
		traceID,
		rawESBatchIntegrationRoute{
			referenceName: "a",
			tableID:       "trace.first",
			index:         firstIndex,
		},
		rawESBatchIntegrationRoute{
			referenceName: "b",
			tableID:       "trace.second",
			index:         secondIndex,
		},
	)
}

func rawESBatchDataLabelTraceQuery(
	storageID, firstIndex, secondIndex, traceID string,
) *structured.QueryTs {
	timeField := metadata.TimeField{
		Name: "start_time",
		Type: "date",
		Unit: "millisecond",
	}
	return &structured.QueryTs{
		SpaceUid: "bkcc__2",
		QueryList: []*structured.Query{
			{
				ReferenceName: "a",
				TableID:       "trace_data_label",
				KeepColumns:   []string{"start_time", "trace_id"},
				Conditions: structured.Conditions{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "trace_id",
							Value:         []string{traceID},
							Operator:      structured.ConditionEqual,
						},
					},
				},
			},
		},
		TsDBMap: map[string]structured.TsDBs{
			"a": {
				&queryPkg.TsDBV2{
					TableID:     "trace.data_label.first",
					DataLabel:   "trace_data_label",
					DB:          firstIndex,
					StorageID:   storageID,
					StorageType: metadata.ElasticsearchStorageType,
					TimeField:   timeField,
				},
				&queryPkg.TsDBV2{
					TableID:     "trace.data_label.second",
					DataLabel:   "trace_data_label",
					DB:          secondIndex,
					StorageID:   storageID,
					StorageType: metadata.ElasticsearchStorageType,
					TimeField:   timeField,
				},
			},
		},
		Start: "1700000000000",
		End:   "1700000001000",
		Limit: 10,
		OrderBy: structured.OrderBy{
			"-start_time",
		},
	}
}

type rawESBatchIntegrationRoute struct {
	referenceName string
	tableID       string
	index         string
}

func rawESBatchIntegrationQuery(
	storageID, traceID string,
	routes ...rawESBatchIntegrationRoute,
) *structured.QueryTs {
	traceCondition := func() structured.Conditions {
		return structured.Conditions{
			FieldList: []structured.ConditionField{
				{
					DimensionName: "trace_id",
					Value:         []string{traceID},
					Operator:      structured.ConditionEqual,
				},
			},
		}
	}
	timeField := metadata.TimeField{
		Name: "start_time",
		Type: "date",
		Unit: "millisecond",
	}
	queryTs := &structured.QueryTs{
		SpaceUid:  "bkcc__2",
		QueryList: make([]*structured.Query, 0, len(routes)),
		TsDBMap:   make(map[string]structured.TsDBs, len(routes)),
		Start:     "1700000000000",
		End:       "1700000001000",
		Limit:     10,
		OrderBy: structured.OrderBy{
			"-start_time",
		},
	}
	for _, route := range routes {
		queryTs.QueryList = append(queryTs.QueryList, &structured.Query{
			ReferenceName: route.referenceName,
			TableID:       structured.TableID(route.tableID),
			KeepColumns:   []string{"start_time", "trace_id"},
			Conditions:    traceCondition(),
		})
		queryTs.TsDBMap[route.referenceName] = structured.TsDBs{
			&queryPkg.TsDBV2{
				TableID:     route.tableID,
				DataLabel:   strings.ReplaceAll(route.tableID, ".", "_"),
				DB:          route.index,
				StorageID:   storageID,
				StorageType: metadata.ElasticsearchStorageType,
				TimeField:   timeField,
			},
		}
	}
	return queryTs
}

func queryRawESBatchIntegrationMetricSnapshot(t *testing.T) map[string]float64 {
	t.Helper()

	values := make(map[string]float64)
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "unify_query_query_raw_es_batch_events_total" {
			continue
		}
		for _, familyMetric := range family.GetMetric() {
			event := ""
			for _, label := range familyMetric.GetLabel() {
				if label.GetName() == "event" {
					event = label.GetValue()
					break
				}
			}
			if event != "" {
				values[event] += familyMetric.GetCounter().GetValue()
			}
		}
	}
	return values
}
