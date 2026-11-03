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
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

// exactTotalTwoRoutes routes one logical table to two ES result tables; the second one's search fails.
func exactTotalTwoRoutes(t *testing.T) context.Context {
	t.Helper()
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})
	influxdb.MockSpaceRouter(ctx)
	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err)

	const mergeTable, okRT, failRT = "et_multi_merge", "et_ok_rt", "et_fail_rt"
	require.NoError(t, router.Add(ctx, ir.DataLabelToResultTableKey, mergeTable, &ir.ResultTableList{okRT, failRT}))
	require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, okRT, &ir.ResultTableDetail{
		StorageId: 3, TableId: okRT, DB: "et_ok_idx", StorageType: "elasticsearch", DataLabel: "et_ok"}))
	require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, failRT, &ir.ResultTableDetail{
		StorageId: 3, TableId: failRT, DB: "et_fail_idx", StorageType: "elasticsearch", DataLabel: "et_fail"}))
	space := router.GetSpace(ctx, "bkcc__2")
	if space == nil {
		space = make(ir.Space)
	}
	space[okRT] = &ir.SpaceResultTable{TableId: okRT}
	space[failRT] = &ir.SpaceResultTable{TableId: failRT}
	space[mergeTable] = &ir.SpaceResultTable{TableId: mergeTable}
	require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, "bkcc__2", &space))

	const esURL = "http://127.0.0.1:93002"
	for _, idx := range []string{"et_ok_idx", "et_fail_idx"} {
		body := `{"` + idx + `":{"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"},"log":{"type":"text"}}}}}`
		httpmock.RegisterResponder(http.MethodGet, esURL+"/"+idx, httpmock.NewStringResponder(http.StatusOK, body))
		httpmock.RegisterResponder(http.MethodGet, esURL+"/"+idx+"/_mapping/", httpmock.NewStringResponder(http.StatusOK, body))
	}
	okSearch := `{"took":1,"timed_out":false,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"et_ok_idx","_id":"x1","_source":{"dtEventTimeStamp":"1752141800000","log":"ok"}}]}}`
	httpmock.RegisterResponder(http.MethodPost, esURL+"/et_ok_idx/_search", httpmock.NewStringResponder(http.StatusOK, okSearch))
	httpmock.RegisterResponder(http.MethodPost, esURL+"/et_fail_idx/_search", httpmock.NewStringResponder(http.StatusInternalServerError, `{"error":"mock failure"}`))
	return ctx
}

func exactTotalTwoRouteQuery(exact bool) *structured.QueryTs {
	return &structured.QueryTs{
		SpaceUid: "bkcc__2",
		QueryList: []*structured.Query{{
			TableID: "et_multi_merge", FieldList: []string{"dtEventTimeStamp", "log"}, ExactTotal: exact,
		}},
		Start: "1752141400000", End: "1752141900000", Limit: 50,
	}
}

// One logical query over two ES routes where one route fails: without exact_total the merge stays a partial
// success, as before; with exact_total the whole request fails, because the summed total would miss that route.
func TestQueryRawExactTotalFailsWhenAnyRouteFails(t *testing.T) {
	t.Run("without exact_total the merge is a partial success", func(t *testing.T) {
		ctx := exactTotalTwoRoutes(t)
		total, list, _, routes, err := queryRawWithInstance(ctx, exactTotalTwoRouteQuery(false))
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.NotEmpty(t, list)
		assert.Len(t, routes, 2)
		assert.Equal(t, metadata.QueryRawPartial, metadata.GetStatus(ctx).Code)
	})

	t.Run("with exact_total any failed route fails the request", func(t *testing.T) {
		ctx := exactTotalTwoRoutes(t)
		_, _, _, _, err := queryRawWithInstance(ctx, exactTotalTwoRouteQuery(true))
		require.Error(t, err)
	})
}

// A route that search_after skipped on this page is missing from the summed total: with exact_total the page fails,
// without it the page is answered as before.
func TestQueryRawExactTotalFailsWhenSearchAfterSkipsARoute(t *testing.T) {
	for _, exact := range []bool{false, true} {
		ctx := exactTotalTwoRoutes(t)
		queryTs := exactTotalTwoRouteQuery(exact)
		queryTs.IsSearchAfter = true
		queryTs.OrderBy = structured.OrderBy{"dtEventTimeStamp"}
		// only the first route still has a cursor; the second finished on an earlier page
		queryTs.ResultTableOptions = metadata.ResultTableOptions{"et_ok_rt|3": {SearchAfter: []any{1752141800000}}}
		_, _, _, _, err := queryRawWithInstance(ctx, queryTs)
		if exact {
			require.ErrorContains(t, err, "not queried on this page")
		} else {
			require.NoError(t, err)
		}
	}
}

// The scroll endpoint sums its total page by page, so it refuses exact_total instead of returning an unmarked total.
func TestHandlerQueryRawWithScrollRejectsExactTotal(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})
	body := `{"space_uid":"bkcc__2","query_list":[{"table_id":"et_multi_merge","exact_total":true}],"start_time":"1752141400000","end_time":"1752141900000"}`
	req := httptest.NewRequest(http.MethodPost, "/query/ts/raw_with_scroll", strings.NewReader(body)).WithContext(ctx)
	w := runCheckHandler(t, req, HandlerQueryRawWithScroll)
	assert.Contains(t, w.Body.String(), "exact_total is not supported by query_raw_with_scroll")
}
