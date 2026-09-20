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
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

func TestQueryRawESBatchMissingMappingFallbackRespectsSingleRoutingSlot(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})

	const (
		esURL       = "http://127.0.0.1:93010"
		storageID   = "query-raw-es-batch-fallback-single-slot"
		firstAlias  = "trace_fallback_first"
		firstBad    = "trace_fallback_first_bad"
		firstGood   = "trace_fallback_first_good"
		secondAlias = "trace_fallback_second"
		traceID     = "00000000000000000000000000000042"
	)

	oldLimit := QueryMaxRouting
	oldSettings := queryRawESBatchSettingsSnapshot.Load()
	QueryMaxRouting = 1
	queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{
		maxMembers:            16,
		maxBodyBytes:          1 << 20,
		maxConcurrentSearches: 4,
	})
	t.Cleanup(func() {
		QueryMaxRouting = oldLimit
		queryRawESBatchSettingsSnapshot.Store(oldSettings)
	})
	tsdb.SetStorage(storageID, &tsdb.Storage{
		Type:    metadata.ElasticsearchStorageType,
		Address: esURL,
	})

	var (
		active            atomic.Int32
		maxActive         atomic.Int32
		mappingCalls      atomic.Int32
		multiSearchCalls  atomic.Int32
		firstSearchCalls  atomic.Int32
		secondSearchCalls atomic.Int32
	)
	trackActive := func() func() {
		current := active.Add(1)
		for {
			maximum := maxActive.Load()
			if current <= maximum || maxActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		return func() {
			active.Add(-1)
		}
	}

	httpmock.RegisterResponder(http.MethodGet, esURL+"/"+firstAlias, func(*http.Request) (*http.Response, error) {
		defer trackActive()()
		mappingCalls.Add(1)
		return httpmock.NewStringResponse(
			http.StatusOK,
			`{"`+firstBad+`":{"aliases":{"`+firstAlias+`":{}},"mappings":{"properties":{"trace_id":{"type":"keyword"}}}},"`+
				firstGood+`":{"aliases":{"`+firstAlias+`":{}},"mappings":{"properties":{"start_time":{"type":"date"},"trace_id":{"type":"keyword"}}}}}`,
		), nil
	})
	httpmock.RegisterResponder(http.MethodGet, esURL+"/"+secondAlias, func(*http.Request) (*http.Response, error) {
		defer trackActive()()
		mappingCalls.Add(1)
		return httpmock.NewStringResponse(
			http.StatusOK,
			`{"`+secondAlias+`":{"mappings":{"properties":{"start_time":{"type":"date"},"trace_id":{"type":"keyword"}}}}}`,
		), nil
	})
	httpmock.RegisterResponder(
		http.MethodGet,
		esURL+"/_msearch?max_concurrent_searches=4",
		func(*http.Request) (*http.Response, error) {
			defer trackActive()()
			multiSearchCalls.Add(1)
			return httpmock.NewStringResponse(
				http.StatusOK,
				`{"responses":[`+
					`{"status":200,"_shards":{"total":2,"successful":1,"failed":1,"failures":[{"shard":0,"index":"`+firstBad+`","reason":{"type":"query_shard_exception","reason":"No mapping found for [start_time] in order to sort on"}}]},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}},`+
					`{"status":200,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"`+secondAlias+`","_id":"second","_source":{"start_time":2,"trace_id":"`+traceID+`"}}]}}`+
					`]}`,
			), nil
		},
	)
	httpmock.RegisterResponder(
		http.MethodPost,
		esURL+"/"+firstAlias+"/_search",
		func(*http.Request) (*http.Response, error) {
			defer trackActive()()
			if firstSearchCalls.Add(1) == 1 {
				return httpmock.NewStringResponse(
					http.StatusOK,
					`{"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
				), nil
			}
			return httpmock.NewStringResponse(
				http.StatusOK,
				`{"_shards":{"total":2,"successful":2,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"`+firstGood+`","_id":"first","_source":{"start_time":1,"trace_id":"`+traceID+`"}}]}}`,
			), nil
		},
	)
	httpmock.RegisterResponder(
		http.MethodPost,
		esURL+"/"+secondAlias+"/_search",
		func(*http.Request) (*http.Response, error) {
			defer trackActive()()
			secondSearchCalls.Add(1)
			return httpmock.NewStringResponse(
				http.StatusInternalServerError,
				`{"error":{"type":"unexpected_successful_sibling_replay"}}`,
			), nil
		},
	)

	queryTs := rawESBatchExplicitTraceQuery(storageID, firstAlias, secondAlias, traceID)
	queryTs.IsESBatch = true
	total, list, options, _, err := queryRawWithInstance(ctx, queryTs)

	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, list, 2)
	assert.Len(t, options, 2)
	assert.EqualValues(t, 2, mappingCalls.Load())
	assert.EqualValues(t, 1, multiSearchCalls.Load())
	assert.EqualValues(t, 2, firstSearchCalls.Load())
	assert.Zero(t, secondSearchCalls.Load())
	assert.Zero(t, active.Load())
	assert.EqualValues(t, 1, maxActive.Load())
}

func TestQueryRawESBatchContextCancellationReleasesBlockedRequest(t *testing.T) {
	mock.Init()
	baseCtx := metadata.InitHashID(context.Background())
	metadata.SetUser(baseCtx, &metadata.User{SpaceUID: "bkcc__2"})
	ctx, cancel := context.WithCancel(baseCtx)
	t.Cleanup(cancel)

	const (
		esURL       = "http://127.0.0.1:93011"
		storageID   = "query-raw-es-batch-cancel"
		firstIndex  = "trace_cancel_first"
		secondIndex = "trace_cancel_second"
		traceID     = "00000000000000000000000000000042"
	)

	oldSettings := queryRawESBatchSettingsSnapshot.Load()
	queryRawESBatchSettingsSnapshot.Store(&queryRawESBatchSettings{
		maxMembers:            16,
		maxBodyBytes:          1 << 20,
		maxConcurrentSearches: 4,
	})
	t.Cleanup(func() {
		queryRawESBatchSettingsSnapshot.Store(oldSettings)
	})
	tsdb.SetStorage(storageID, &tsdb.Storage{
		Type:    metadata.ElasticsearchStorageType,
		Address: esURL,
	})
	registerRawESBatchMappingResponder(t, esURL, firstIndex)
	registerRawESBatchMappingResponder(t, esURL, secondIndex)

	var active, multiSearchCalls, singleSearchCalls atomic.Int32
	started := make(chan struct{})
	httpmock.RegisterResponder(
		http.MethodGet,
		esURL+"/_msearch?max_concurrent_searches=4",
		func(request *http.Request) (*http.Response, error) {
			active.Add(1)
			defer active.Add(-1)
			multiSearchCalls.Add(1)
			close(started)
			<-request.Context().Done()
			return nil, request.Context().Err()
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

	resultCh := make(chan error, 1)
	go func() {
		queryTs := rawESBatchExplicitTraceQuery(storageID, firstIndex, secondIndex, traceID)
		queryTs.IsESBatch = true
		_, _, _, _, err := queryRawWithInstance(ctx, queryTs)
		resultCh <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("batch request did not start")
	}
	cancel()

	select {
	case err := <-resultCh:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("query did not return after context cancellation")
	}
	assert.Zero(t, active.Load())
	assert.EqualValues(t, 1, multiSearchCalls.Load())
	assert.Zero(t, singleSearchCalls.Load())
}
