// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BK) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for
// the specific language governing permissions and limitations under the License.

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

const (
	handlerSearchAfterBKSQLMatcher = "handler-search-after-es-doris"
	handlerSearchAfterESMatcher    = "handler-search-after-es"
)

const handlerSearchAfterDorisSchema = `{"result":true,"message":"success","code":"00","data":{"list":[{"Field":"thedate","Type":"INT"},{"Field":"dtEventTimeStamp","Type":"BIGINT"},{"Field":"dtEventTime","Type":"VARCHAR(32)"},{"Field":"__unique_key__","Type":"VARCHAR(512)"},{"Field":"message","Type":"TEXT"}]}}`

type handlerSearchAfterResponse struct {
	List               []map[string]any `json:"list"`
	ResultTableOptions json.RawMessage  `json:"result_table_options"`
}

func TestHandlerQueryRawSearchAfterAcrossESAndDoris(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: influxdb.SpaceUid})
	influxdb.MockSpaceRouter(ctx)

	const (
		firstDorisCursor  = "9007199254740993"
		secondDorisCursor = "9007199254740994"
	)

	var (
		esCalls    int
		dorisCalls int
		callsLock  sync.Mutex
	)

	esMatcher := httpmock.BodyContainsString(`"message"`).WithName(handlerSearchAfterESMatcher)
	httpmock.RegisterMatcherResponder(http.MethodPost, mock.EsUrl+"/es_index/_search", esMatcher, func(req *http.Request) (*http.Response, error) {
		var body struct {
			SearchAfter []json.RawMessage `json:"search_after"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}

		callsLock.Lock()
		defer callsLock.Unlock()
		esCalls++

		switch esCalls {
		case 1:
			if len(body.SearchAfter) != 0 {
				return nil, fmt.Errorf("first ES page unexpectedly contains search_after: %s", body.SearchAfter)
			}
			return httpmock.NewStringResponse(http.StatusOK, `{"hits":{"total":{"value":2,"relation":"eq"},"hits":[{"_index":"es_index","_id":"es-1","_source":{"dtEventTimeStamp":"1744662180000","message":"es-1"},"sort":[1744662180000]}]}}`), nil
		case 2:
			if len(body.SearchAfter) != 1 || string(body.SearchAfter[0]) != "1744662180000" {
				return nil, fmt.Errorf("second ES page has unexpected search_after: %s", body.SearchAfter)
			}
			return httpmock.NewStringResponse(http.StatusOK, `{"hits":{"total":{"value":2,"relation":"eq"},"hits":[{"_index":"es_index","_id":"es-2","_source":{"dtEventTimeStamp":"1744662181000","message":"es-2"},"sort":[1744662181000]}]}}`), nil
		case 3:
			if len(body.SearchAfter) != 1 || string(body.SearchAfter[0]) != "1744662181000" {
				return nil, fmt.Errorf("final ES page has unexpected search_after: %s", body.SearchAfter)
			}
			return httpmock.NewStringResponse(http.StatusOK, `{"hits":{"total":{"value":2,"relation":"eq"},"hits":[]}}`), nil
		default:
			return nil, fmt.Errorf("ES query should stop after its empty page, got call %d", esCalls)
		}
	})

	matcher := httpmock.BodyContainsString("2_bklog_bkunify_query_doris").WithName(handlerSearchAfterBKSQLMatcher)
	httpmock.RegisterMatcherResponder(http.MethodPost, mock.BkBaseUrl, matcher, func(req *http.Request) (*http.Response, error) {
		var body struct {
			SQL string `json:"sql"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}

		if strings.HasPrefix(body.SQL, "SHOW CREATE TABLE") {
			return httpmock.NewStringResponse(http.StatusOK, handlerSearchAfterDorisSchema), nil
		}

		callsLock.Lock()
		defer callsLock.Unlock()
		dorisCalls++

		switch dorisCalls {
		case 1:
			if strings.Contains(body.SQL, firstDorisCursor) {
				return nil, fmt.Errorf("first Doris page unexpectedly contains cursor %s", firstDorisCursor)
			}
			return httpmock.NewStringResponse(http.StatusOK, dorisSearchAfterResponse("doris-1", firstDorisCursor)), nil
		case 2:
			if !strings.Contains(body.SQL, firstDorisCursor) {
				return nil, fmt.Errorf("second Doris page lost exact bigint cursor %s in SQL: %s", firstDorisCursor, body.SQL)
			}
			return httpmock.NewStringResponse(http.StatusOK, dorisSearchAfterResponse("doris-2", secondDorisCursor)), nil
		case 3:
			if !strings.Contains(body.SQL, secondDorisCursor) {
				return nil, fmt.Errorf("final Doris page has unexpected cursor SQL: %s", body.SQL)
			}
			return httpmock.NewStringResponse(http.StatusOK, `{"result":true,"message":"success","code":"00","data":{"totalRecords":2,"list":[]}}`), nil
		default:
			return nil, fmt.Errorf("Doris query should stop after its empty page, got call %d", dorisCalls)
		}
	})
	t.Cleanup(func() {
		httpmock.RegisterMatcherResponder(
			http.MethodPost,
			mock.EsUrl+"/es_index/_search",
			httpmock.NewMatcher(handlerSearchAfterESMatcher, nil),
			nil,
		)
		httpmock.RegisterMatcherResponder(
			http.MethodPost,
			mock.BkBaseUrl,
			httpmock.NewMatcher(handlerSearchAfterBKSQLMatcher, nil),
			nil,
		)
	})

	baseBody := `{"space_uid":"bkcc__2","query_list":[{"data_source":"bklog","table_id":"es_and_doris","field_name":"dtEventTimeStamp","keep_columns":["message"],"reference_name":"a","conditions":{}}],"metric_merge":"a","order_by":["dtEventTimeStamp"],"start_time":"1744662180000","end_time":"1744662280000","limit":1,"is_search_after":true}`

	page1 := handlerSearchAfterQuery(t, ctx, baseBody)
	require.ElementsMatch(t, []string{"es-1", "doris-1"}, handlerSearchAfterMessages(page1))
	firstOptions := handlerSearchAfterOptions(t, page1)
	require.Contains(t, firstOptions, "result_table.es|3")
	require.Contains(t, firstOptions, "result_table.doris|4")
	require.JSONEq(t, fmt.Sprintf(`[%s,"doris-1"]`, firstDorisCursor), string(firstOptions["result_table.doris|4"].SearchAfter))

	page2 := handlerSearchAfterQuery(t, ctx, handlerSearchAfterBody(baseBody, page1.ResultTableOptions))
	require.ElementsMatch(t, []string{"es-2", "doris-2"}, handlerSearchAfterMessages(page2))
	secondOptions := handlerSearchAfterOptions(t, page2)
	require.JSONEq(t, fmt.Sprintf(`[%s,"doris-2"]`, secondDorisCursor), string(secondOptions["result_table.doris|4"].SearchAfter))

	page3 := handlerSearchAfterQuery(t, ctx, handlerSearchAfterBody(baseBody, page2.ResultTableOptions))
	assert.Empty(t, page3.List)
	thirdOptions := handlerSearchAfterOptions(t, page3)
	assert.Empty(t, thirdOptions["result_table.es|3"].SearchAfter)
	assert.Empty(t, thirdOptions["result_table.doris|4"].SearchAfter)

	page4 := handlerSearchAfterQuery(t, ctx, handlerSearchAfterBody(baseBody, page3.ResultTableOptions))
	assert.Empty(t, page4.List)
	assert.Equal(t, 3, esCalls)
	assert.Equal(t, 3, dorisCalls)
}

func dorisSearchAfterResponse(message, cursor string) string {
	return fmt.Sprintf(
		`{"result":true,"message":"success","code":"00","data":{"totalRecords":2,"list":[{"message":%q,"_value_":%s,"_timestamp_":%s,"__search_after_0":%s,"__search_after_1":%q}],"result_schema":[{"field_alias":"message"},{"field_alias":"_value_"},{"field_alias":"_timestamp_"},{"field_alias":"__search_after_0"},{"field_alias":"__search_after_1"}]}}}`,
		message,
		cursor,
		cursor,
		cursor,
		message,
	)
}

func handlerSearchAfterQuery(t *testing.T, ctx context.Context, body string) handlerSearchAfterResponse {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/query/raw", bytes.NewBufferString(body))
	require.NoError(t, err)
	w := &Writer{}
	HandlerQueryRaw(&gin.Context{Request: req, Writer: w})

	var response handlerSearchAfterResponse
	require.NoError(t, json.Unmarshal(w.b.Bytes(), &response), w.body())
	return response
}

func handlerSearchAfterOptions(t *testing.T, response handlerSearchAfterResponse) map[string]struct {
	SearchAfter json.RawMessage `json:"search_after"`
} {
	t.Helper()

	options := make(map[string]struct {
		SearchAfter json.RawMessage `json:"search_after"`
	})
	require.NoError(t, json.Unmarshal(response.ResultTableOptions, &options))
	return options
}

func handlerSearchAfterMessages(response handlerSearchAfterResponse) []string {
	messages := make([]string, 0, len(response.List))
	for _, item := range response.List {
		if message, ok := item["message"].(string); ok {
			messages = append(messages, message)
		}
	}
	return messages
}

func handlerSearchAfterBody(base string, options json.RawMessage) string {
	return strings.TrimSuffix(base, "}") + `,"result_table_options":` + string(options) + "}"
}
