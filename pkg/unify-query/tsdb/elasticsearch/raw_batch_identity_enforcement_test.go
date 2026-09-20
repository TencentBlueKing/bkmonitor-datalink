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
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestRawBatchRejectsPreparedMembersFromDifferentEffectiveHeaders(t *testing.T) {
	var multiSearchCalls atomic.Int32
	server := rawBatchIdentityServer(t, &multiSearchCalls)
	defer server.Close()

	ctx := metadata.InitHashID(context.Background())
	firstInstance := rawBatchIdentityInstance(t, server.URL, "first-token")
	secondInstance := rawBatchIdentityInstance(t, server.URL, "second-token")
	first := rawBatchIdentityPrepared(t, ctx, firstInstance, "identity-first", "result.first")
	second := rawBatchIdentityPrepared(t, ctx, secondInstance, "identity-second", "result.second")
	batch := rawBatchFromMembers(t, []RawBatchMember{
		{Ordinal: 0, Prepared: first},
		{Ordinal: 1, Prepared: second},
	})

	results, err := firstInstance.ExecuteRawBatch(ctx, batch, 0)
	require.Error(t, err)
	assert.Empty(t, results)
	assert.Contains(t, err.Error(), "mixed connections")
	assert.Zero(t, multiSearchCalls.Load())
}

func TestPreparedRawQueryRejectsDifferentEffectiveReceiver(t *testing.T) {
	var multiSearchCalls atomic.Int32
	server := rawBatchIdentityServer(t, &multiSearchCalls)
	defer server.Close()

	ctx := metadata.InitHashID(context.Background())
	preparingInstance := rawBatchIdentityInstance(t, server.URL, "prepare-token")
	wrongReceiver := rawBatchIdentityInstance(t, server.URL, "execute-token")
	prepared := rawBatchIdentityPrepared(t, ctx, preparingInstance, "identity-single", "result.single")

	dataCh := make(chan map[string]any, 1)
	_, _, _, err := wrongReceiver.QueryPreparedRawData(ctx, prepared, dataCh)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection mismatch")
	assert.False(t, prepared.claimed.Load())
	assert.Zero(t, multiSearchCalls.Load())
}

func TestPrepareRawQueryRejectsPrefetchedMetadataFromAnotherMember(t *testing.T) {
	var firstMappingCalls, secondMappingCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/prefetch-first":
			firstMappingCalls.Add(1)
			_, _ = io.WriteString(
				writer,
				`{"prefetch-first":{"settings":{},"mappings":{"properties":{"time":{"type":"date"},"first_only":{"type":"keyword"}}}}}`,
			)
		case "/prefetch-second":
			secondMappingCalls.Add(1)
			_, _ = io.WriteString(
				writer,
				`{"prefetch-second":{"settings":{},"mappings":{"properties":{"time":{"type":"date"},"second_only":{"type":"long"}}}}}`,
			)
		default:
			http.Error(writer, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	ctx := metadata.InitHashID(context.Background())
	instance := rawBatchIdentityInstance(t, server.URL, "same-token")
	firstQuery := rawBatchIdentityQuery("prefetch-first", "result.first")
	secondQuery := rawBatchIdentityQuery("prefetch-second", "result.second")
	prefetched, err := instance.PrepareRawFieldMetadata(
		ctx,
		firstQuery,
		time.UnixMilli(1),
		time.UnixMilli(2),
	)
	require.NoError(t, err)

	prepared, err := instance.PrepareRawQuery(
		ctx,
		secondQuery,
		time.UnixMilli(1),
		time.UnixMilli(2),
		prefetched,
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"prefetch-second"}, prepared.queryOption.indexes)
	assert.Contains(t, prepared.FieldsMap(), "second_only")
	assert.NotContains(t, prepared.FieldsMap(), "first_only")
	assert.EqualValues(t, 1, firstMappingCalls.Load())
	assert.EqualValues(t, 1, secondMappingCalls.Load())
}

func rawBatchIdentityServer(t *testing.T, multiSearchCalls *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/_msearch":
			multiSearchCalls.Add(1)
			_, _ = io.WriteString(
				writer,
				`{"responses":[{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}},{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}]}`,
			)
		case request.Method == http.MethodPost && request.URL.Path == "/identity-single/_search":
			multiSearchCalls.Add(1)
			_, _ = io.WriteString(
				writer,
				`{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
			)
		case request.Method == http.MethodGet:
			index := request.URL.Path[1:]
			_, _ = io.WriteString(
				writer,
				fmt.Sprintf(
					`{"%s":{"settings":{},"mappings":{"properties":{"time":{"type":"date"},"value":{"type":"keyword"}}}}}`,
					index,
				),
			)
		default:
			http.Error(writer, "unexpected request", http.StatusNotFound)
		}
	}))
}

func rawBatchIdentityInstance(t *testing.T, address, token string) *Instance {
	t.Helper()
	if http.DefaultTransport == httpmock.DefaultTransport {
		httpmock.Deactivate()
		t.Cleanup(httpmock.Activate)
	}
	instance, err := NewInstance(context.Background(), &InstanceOption{
		Connect: Connect{Address: address},
		Headers: map[string]string{
			"Authorization": token,
		},
		Timeout:     2 * time.Second,
		HealthCheck: false,
	})
	require.NoError(t, err)
	return instance
}

func rawBatchIdentityPrepared(
	t *testing.T,
	ctx context.Context,
	instance *Instance,
	index string,
	tableID string,
) *PreparedRawQuery {
	t.Helper()
	prepared, err := instance.PrepareRawQuery(
		ctx,
		rawBatchIdentityQuery(index, tableID),
		time.UnixMilli(1),
		time.UnixMilli(2),
		nil,
	)
	require.NoError(t, err)
	return prepared
}

func rawBatchIdentityQuery(index, tableID string) *metadata.Query {
	return &metadata.Query{
		DB:          index,
		Field:       "value",
		TableID:     tableID,
		StorageID:   "3",
		StorageType: metadata.ElasticsearchStorageType,
		Size:        1,
		TimeField: metadata.TimeField{
			Name: "time",
			Type: TimeFieldTypeTime,
			Unit: "millisecond",
		},
	}
}
