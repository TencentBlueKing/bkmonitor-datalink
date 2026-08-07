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
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

type queryRawCharacterizationRoute struct {
	tableID   string
	index     string
	dataLabel string
}

func registerQueryRawCharacterizationRoutes(
	t *testing.T,
	ctx context.Context,
	virtualTable string,
	routes ...queryRawCharacterizationRoute,
) {
	t.Helper()

	influxdb.MockSpaceRouter(ctx)
	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err)

	resultTables := make(ir.ResultTableList, 0, len(routes))
	space := router.GetSpace(ctx, "bkcc__2")
	if space == nil {
		space = make(ir.Space)
	}

	for _, route := range routes {
		resultTables = append(resultTables, route.tableID)
		require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, route.tableID, &ir.ResultTableDetail{
			StorageId:   3,
			TableId:     route.tableID,
			DB:          route.index,
			StorageType: metadata.ElasticsearchStorageType,
			DataLabel:   route.dataLabel,
		}))
		space[route.tableID] = &ir.SpaceResultTable{TableId: route.tableID}
	}

	require.NoError(t, router.Add(ctx, ir.DataLabelToResultTableKey, virtualTable, &resultTables))
	space[virtualTable] = &ir.SpaceResultTable{TableId: virtualTable}
	require.NoError(t, router.Add(ctx, ir.SpaceToResultTableKey, "bkcc__2", &space))
}

func registerQueryRawCharacterizationMapping(index string) {
	body := fmt.Sprintf(
		`{"%s":{"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"},"log":{"type":"text"}}}}}`,
		index,
	)
	httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+index, httpmock.NewStringResponder(http.StatusOK, body))
	httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+index+"/_mapping/", httpmock.NewStringResponder(http.StatusOK, body))
}

func queryRawCharacterizationQuery(virtualTable string) *structured.QueryTs {
	return &structured.QueryTs{
		SpaceUid: "bkcc__2",
		QueryList: []*structured.Query{
			{
				TableID:   structured.TableID(virtualTable),
				FieldList: []string{"dtEventTimeStamp", "log"},
			},
		},
		Start: "1752141400000",
		End:   "1752141900000",
		Limit: 10,
	}
}

func TestQueryRawAllRoutesFailed(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})

	const (
		virtualTable = "characterization_all_failed"
		firstRT      = "characterization_all_failed_1"
		secondRT     = "characterization_all_failed_2"
		firstIndex   = "characterization_all_failed_index_1"
		secondIndex  = "characterization_all_failed_index_2"
	)
	registerQueryRawCharacterizationRoutes(t, ctx, virtualTable,
		queryRawCharacterizationRoute{tableID: firstRT, index: firstIndex, dataLabel: "first"},
		queryRawCharacterizationRoute{tableID: secondRT, index: secondIndex, dataLabel: "second"},
	)
	registerQueryRawCharacterizationMapping(firstIndex)
	registerQueryRawCharacterizationMapping(secondIndex)

	var searches atomic.Int32
	failedSearch := func(*http.Request) (*http.Response, error) {
		searches.Add(1)
		return httpmock.NewStringResponse(http.StatusInternalServerError, `{"error":"characterization failure"}`), nil
	}
	httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+firstIndex+"/_search", failedSearch)
	httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+secondIndex+"/_search", failedSearch)

	total, list, options, routeInfo, err := queryRawWithInstance(ctx, queryRawCharacterizationQuery(virtualTable))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "查询原始数据报错")
	assert.Equal(t, int64(0), total)
	assert.Empty(t, list)
	assert.Empty(t, options)
	assert.Equal(t, int32(2), searches.Load())
	assert.ElementsMatch(t, []string{firstRT, secondRT}, resultTableIDFromRouteInfo(routeInfo))
}

func TestQueryRawDuplicateIndexAliasKeepsLogicalRoutesIndependent(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})

	const (
		firstRT     = "characterization.duplicate_alias_1"
		secondRT    = "characterization.duplicate_alias_2"
		sharedIndex = "characterization_duplicate_alias_index"
	)
	registerQueryRawCharacterizationRoutes(t, ctx, "characterization_duplicate_alias_virtual",
		queryRawCharacterizationRoute{tableID: firstRT, index: sharedIndex, dataLabel: "first"},
		queryRawCharacterizationRoute{tableID: secondRT, index: sharedIndex, dataLabel: "second"},
	)
	registerQueryRawCharacterizationMapping(sharedIndex)

	var searches atomic.Int32
	httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+sharedIndex+"/_search", func(r *http.Request) (*http.Response, error) {
		searches.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		request := struct {
			From int `json:"from"`
		}{}
		if err = json.Unmarshal(body, &request); err != nil {
			return nil, err
		}

		switch request.From {
		case 1:
			return httpmock.NewStringResponse(http.StatusOK,
				`{"hits":{"total":{"value":11,"relation":"eq"},"hits":[{"_index":"characterization_duplicate_alias_index","_id":"first","_source":{"dtEventTimeStamp":"1752141800000","log":"first"}}]}}`,
			), nil
		case 2:
			return httpmock.NewStringResponse(http.StatusOK,
				`{"hits":{"total":{"value":22,"relation":"eq"},"hits":[{"_index":"characterization_duplicate_alias_index","_id":"second","_source":{"dtEventTimeStamp":"1752141700000","log":"second"}}]}}`,
			), nil
		default:
			return nil, fmt.Errorf("unexpected request body: %s", body)
		}
	})

	firstFrom, secondFrom := 1, 2
	qts := &structured.QueryTs{
		SpaceUid: "bkcc__2",
		QueryList: []*structured.Query{
			{
				DataSource:    structured.BkLog,
				TableID:       structured.TableID(firstRT),
				ReferenceName: "first",
				FieldList:     []string{"dtEventTimeStamp", "log"},
			},
			{
				DataSource:    structured.BkLog,
				TableID:       structured.TableID(secondRT),
				ReferenceName: "second",
				FieldList:     []string{"dtEventTimeStamp", "log"},
			},
		},
		ResultTableOptions: metadata.ResultTableOptions{
			firstRT + "|3":  {From: &firstFrom},
			secondRT + "|3": {From: &secondFrom},
		},
		Start: "1752141400000",
		End:   "1752141900000",
		Limit: 10,
	}

	total, list, options, routeInfo, err := queryRawWithInstance(ctx, qts)

	require.NoError(t, err)
	assert.Equal(t, int64(33), total)
	assert.Equal(t, int32(2), searches.Load())
	require.Len(t, list, 2)

	logByResultTable := make(map[string]any, len(list))
	for _, item := range list {
		logByResultTable[item[metadata.KeyTableID].(string)] = item["log"]
	}
	assert.Equal(t, "first", logByResultTable[firstRT])
	assert.Equal(t, "second", logByResultTable[secondRT])

	require.Contains(t, options, firstRT+"|3")
	require.Contains(t, options, secondRT+"|3")
	require.NotNil(t, options[firstRT+"|3"].From)
	require.NotNil(t, options[secondRT+"|3"].From)
	assert.Equal(t, 1, *options[firstRT+"|3"].From)
	assert.Equal(t, 2, *options[secondRT+"|3"].From)
	assert.ElementsMatch(t, []string{firstRT, secondRT}, resultTableIDFromRouteInfo(routeInfo))
}

func TestQueryRawRouteInfoDoesNotDependOnReturnedRows(t *testing.T) {
	tests := []struct {
		name       string
		from       int
		searchBody string
		total      int64
	}{
		{
			name:       "empty results",
			searchBody: `{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
		},
		{
			name:       "all rows removed by global crop",
			from:       5,
			searchBody: `{"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"characterization_crop","_id":"one","_source":{"dtEventTimeStamp":"1752141800000","log":"one"}}]}}`,
			total:      2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock.Init()
			ctx := metadata.InitHashID(context.Background())
			metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})

			suffix := strings.ReplaceAll(tc.name, " ", "_")
			virtualTable := "characterization_route_info_" + suffix
			firstRT := virtualTable + "_1"
			secondRT := virtualTable + "_2"
			firstIndex := virtualTable + "_index_1"
			secondIndex := virtualTable + "_index_2"
			registerQueryRawCharacterizationRoutes(t, ctx, virtualTable,
				queryRawCharacterizationRoute{tableID: firstRT, index: firstIndex, dataLabel: "first"},
				queryRawCharacterizationRoute{tableID: secondRT, index: secondIndex, dataLabel: "second"},
			)
			registerQueryRawCharacterizationMapping(firstIndex)
			registerQueryRawCharacterizationMapping(secondIndex)
			httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+firstIndex+"/_search", httpmock.NewStringResponder(http.StatusOK, tc.searchBody))
			httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+secondIndex+"/_search", httpmock.NewStringResponder(http.StatusOK, tc.searchBody))

			qts := queryRawCharacterizationQuery(virtualTable)
			qts.From = tc.from
			qts.Limit = 1

			total, list, _, routeInfo, err := queryRawWithInstance(ctx, qts)

			require.NoError(t, err)
			assert.Equal(t, tc.total, total)
			assert.Empty(t, list)
			assert.ElementsMatch(t, []string{firstRT, secondRT}, resultTableIDFromRouteInfo(routeInfo))
		})
	}
}

func TestQueryRawHighlightPrefetchFailureDoesNotPoisonSearchMapping(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})

	const (
		virtualTable = "characterization_highlight_prefetch"
		tableID      = "characterization_highlight_prefetch_rt"
		index        = "characterization_highlight_prefetch_index"
	)
	registerQueryRawCharacterizationRoutes(t, ctx, virtualTable,
		queryRawCharacterizationRoute{tableID: tableID, index: index, dataLabel: "highlight"},
	)

	mappingBody := fmt.Sprintf(
		`{"%s":{"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"},"log":{"type":"text"}}}}}`,
		index,
	)
	var indexGets atomic.Int32
	httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+index, func(*http.Request) (*http.Response, error) {
		if indexGets.Add(1) == 1 {
			return httpmock.NewStringResponse(http.StatusInternalServerError, `{"error":"prefetch index failure"}`), nil
		}
		return httpmock.NewStringResponse(http.StatusOK, mappingBody), nil
	})
	var mappingFallbacks atomic.Int32
	httpmock.RegisterResponder(http.MethodGet, mock.EsUrl+"/"+index+"/_mapping/", func(*http.Request) (*http.Response, error) {
		mappingFallbacks.Add(1)
		return httpmock.NewStringResponse(http.StatusInternalServerError, `{"error":"prefetch mapping failure"}`), nil
	})
	var searches atomic.Int32
	httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/"+index+"/_search", func(*http.Request) (*http.Response, error) {
		searches.Add(1)
		return httpmock.NewStringResponse(http.StatusOK,
			`{"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"characterization_highlight_prefetch_index","_id":"one","_source":{"dtEventTimeStamp":"1752141800000","log":"needle"}}]}}`,
		), nil
	})

	qts := queryRawCharacterizationQuery(virtualTable)
	qts.HighLight = &metadata.HighLight{Enable: true}
	qts.QueryList[0].Conditions = structured.Conditions{
		FieldList: []structured.ConditionField{
			{
				DimensionName: "log",
				Value:         []string{"needle"},
				Operator:      structured.ConditionEqual,
			},
		},
	}

	total, list, options, routeInfo, err := queryRawWithInstance(ctx, qts)

	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, list, 1)
	assert.Equal(t, "needle", list[0]["log"])
	assert.Contains(t, options, tableID+"|3")
	assert.ElementsMatch(t, []string{tableID}, resultTableIDFromRouteInfo(routeInfo))
	assert.Equal(t, int32(2), indexGets.Load())
	assert.Equal(t, int32(1), mappingFallbacks.Load())
	assert.Equal(t, int32(1), searches.Load())
}
