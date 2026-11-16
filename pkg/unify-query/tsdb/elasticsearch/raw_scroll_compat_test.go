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
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestQueryRawDataLegacyScrollKeepsDecodedRows(t *testing.T) {
	mock.Init()
	const address = "http://scroll-compat.example.test"
	httpmock.RegisterResponder(http.MethodGet, address+"/scroll_logs",
		httpmock.NewStringResponder(http.StatusOK, `{"scroll_logs":{"mappings":{"properties":{"event_time":{"type":"long"}}}}}`))
	for _, tc := range []struct {
		name        string
		scroll      string
		scrollID    string
		searchAfter bool
		wantStream  bool
	}{
		{name: "scroll first page", scroll: "1m", wantStream: true},
		{name: "scroll continuation", scroll: "1m", scrollID: "original-cursor", wantStream: true},
		{name: "scroll id without scroll window", scrollID: "original-cursor", wantStream: true},
		{name: "scroll id wins over search after", scroll: "1m", scrollID: "original-cursor", searchAfter: true, wantStream: true},
		{name: "ordinary search"},
		{name: "search after", searchAfter: true},
		{name: "search after wins over scroll window", scroll: "1m", searchAfter: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			instance, err := NewInstance(ctx, &InstanceOption{
				Connect: Connect{Address: address}, MaxSize: 100, Timeout: 3 * time.Second,
			})
			require.NoError(t, err)
			searchCalls, scrollCalls := 0, 0
			brokenPage := `{"_scroll_id":"cursor-after-broken-page","_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":3,"relation":"eq"},"hits":[{"_id":"A","_source":{"event_time":1700000000100}},{"_id":"B"}]}}`
			httpmock.RegisterResponder(http.MethodPost, address+"/scroll_logs/_search", func(req *http.Request) (*http.Response, error) {
				searchCalls++
				var body map[string]json.RawMessage
				assert.NoError(t, json.NewDecoder(req.Body).Decode(&body))
				if tc.searchAfter {
					assert.NotContains(t, req.URL.Query(), "scroll")
					assert.JSONEq(t, "[1700000000000]", string(body["search_after"]))
				} else {
					assert.Equal(t, tc.scroll, req.URL.Query().Get("scroll"))
					assert.NotContains(t, body, "search_after")
				}
				return httpmock.NewStringResponse(http.StatusOK, brokenPage), nil
			})
			httpmock.RegisterResponder(http.MethodPost, address+"/_search/scroll", func(req *http.Request) (*http.Response, error) {
				scrollCalls++
				var body struct {
					Scroll   string `json:"scroll"`
					ScrollID string `json:"scroll_id"`
				}
				assert.NoError(t, json.NewDecoder(req.Body).Decode(&body))
				assert.Equal(t, tc.scroll, body.Scroll)
				// A failed decode does not update the caller's cursor.
				assert.Equal(t, tc.scrollID, body.ScrollID)
				if scrollCalls == 1 {
					return httpmock.NewStringResponse(http.StatusOK, brokenPage), nil
				}
				return httpmock.NewStringResponse(http.StatusOK,
					`{"_scroll_id":"cursor-after-C","_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":3,"relation":"eq"},"hits":[{"_id":"C","_source":{"event_time":1700000000300}}]}}`), nil
			})
			query := &metadata.Query{
				TableID: "scroll.logs", StorageID: "scroll-compat", StorageType: metadata.ElasticsearchStorageType,
				DB: "scroll_logs", Size: 2, Scroll: tc.scroll,
				TimeField:         metadata.TimeField{Name: "event_time", Type: "long", Unit: "millisecond"},
				Source:            []string{"event_time"},
				ResultTableOption: &metadata.ResultTableOption{ScrollID: tc.scrollID},
			}
			if tc.searchAfter {
				query.ResultTableOption.SearchAfter = []any{1700000000000}
			}
			beforeOption := query.ResultTableOption.Clone()
			rows := make(chan map[string]any, 3)
			_, _, _, decodeErr := instance.QueryRawData(ctx, query,
				time.UnixMilli(1700000000000), time.UnixMilli(1700000001000), rows)
			close(rows)
			require.ErrorIs(t, decodeErr, io.EOF)
			decoded := drainRawRows(rows)
			if tc.wantStream {
				if assert.Len(t, decoded, 1, "the already decoded Scroll row A remains deliverable") {
					assert.Equal(t, "A", decoded[0][metadata.KeyDocID])
				}
			} else {
				assert.Empty(t, decoded, "the actual search_after path keeps atomic route decoding")
			}
			assert.Equal(t, beforeOption, query.ResultTableOption)
			if tc.scrollID == "" {
				assert.Equal(t, 1, searchCalls)
				assert.Zero(t, scrollCalls)
				return
			}
			assert.Zero(t, searchCalls, "ScrollID takes precedence over SearchAfter and the scroll window")
			assert.Equal(t, 1, scrollCalls)

			// The controlled responder returns page C when the unchanged cursor is used again.
			// An actual ES server is not guaranteed to replay or advance that cursor this way.
			nextRows := make(chan map[string]any, 3)
			size, total, option, nextErr := instance.QueryRawData(ctx, query,
				time.UnixMilli(1700000000000), time.UnixMilli(1700000001000), nextRows)
			close(nextRows)
			require.NoError(t, nextErr)
			decodedNext := drainRawRows(nextRows)
			require.Len(t, decodedNext, 1)
			assert.Equal(t, "C", decodedNext[0][metadata.KeyDocID])
			assert.EqualValues(t, 1, size)
			assert.EqualValues(t, 3, total)
			if tc.scroll != "" {
				assert.Equal(t, "cursor-after-C", option.ScrollID)
			}
			assert.Equal(t, 2, scrollCalls)
		})
	}
}
