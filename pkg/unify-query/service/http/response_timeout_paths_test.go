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
	stdjson "encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	tsDBService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/tsdb"
)

const slowDorisMatcherName = "slow-doris-route"

// slowDorisRoute makes the Doris table's data queries hang until the request
// context ends, under a short bksql timeout: a storage that does not answer
// before its own deadline. Its schema query still answers, so the query
// reaches the data read.
func slowDorisRoute(t *testing.T) context.Context {
	t.Helper()
	mock.Init()
	promql.MockEngine()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)
	metadata.SetUser(ctx, &metadata.User{Key: "username:test", SpaceUID: influxdb.SpaceUid, SkipSpace: "true"})

	previous := tsDBService.BkSqlTimeout
	tsDBService.BkSqlTimeout = 100 * time.Millisecond
	matcher := httpmock.BodyContainsString("2_bklog_bkunify_query_doris").WithName(slowDorisMatcherName)
	httpmock.RegisterMatcherResponder(http.MethodPost, mock.BkBaseUrl, matcher, func(req *http.Request) (*http.Response, error) {
		var body struct {
			SQL string `json:"sql"`
		}
		if err := stdjson.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		if strings.HasPrefix(body.SQL, "SHOW CREATE TABLE") {
			if schema, ok := mock.BkSQL.Get(body.SQL); ok {
				return httpmock.NewStringResponse(http.StatusOK, schema.(string)), nil
			}
		}
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	t.Cleanup(func() {
		tsDBService.BkSqlTimeout = previous
		httpmock.RegisterMatcherResponder(http.MethodPost, mock.BkBaseUrl, httpmock.NewMatcher(slowDorisMatcherName, nil), nil)
	})
	return ctx
}

func slowDorisWindow() (string, string) {
	return strconv.FormatInt(time.UnixMilli(1744662513046).Unix(), 10), strconv.FormatInt(time.UnixMilli(1744684113046).Unix(), 10)
}

// A query the PromQL engine reads through the route querier keeps the
// route's timeout in its error, so it answers 504, with the same words.
func TestAnEngineRoutedStorageTimeoutAnswersGatewayTimeout(t *testing.T) {
	ctx := slowDorisRoute(t)
	start, end := slowDorisWindow()
	_, err := queryTsWithPromEngine(ctx, &structured.QueryTs{
		QueryList: []*structured.Query{{
			DataSource:      structured.BkLog,
			TableID:         structured.TableID(influxdb.ResultTableDoris),
			FieldName:       "gseIndex",
			ReferenceName:   "a",
			TimeAggregation: structured.TimeAggregation{Function: "count_over_time", Window: "30s"},
			AggregateMethodList: structured.AggregateMethodList{
				{Method: "sum", Dimensions: []string{"__ext.container_name"}},
			},
		}},
		MetricMerge: "a",
		Start:       start,
		End:         end,
		Step:        "30s",
		SpaceUid:    influxdb.SpaceUid,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	code, body := failedStatusOf(t, err)
	require.Equal(t, http.StatusGatewayTimeout, code)
	require.Equal(t, err.Error(), body.Err)
}

// A raw query merges its routes' errors the same way.
func TestARawQueryStorageTimeoutAnswersGatewayTimeout(t *testing.T) {
	ctx := slowDorisRoute(t)
	start, end := slowDorisWindow()
	_, _, _, _, err := queryRawWithInstance(ctx, &structured.QueryTs{
		QueryList: []*structured.Query{{
			DataSource:    structured.BkLog,
			TableID:       structured.TableID(influxdb.ResultTableDoris),
			FieldName:     "gseIndex",
			ReferenceName: "a",
			Limit:         1,
		}},
		MetricMerge: "a",
		Start:       start,
		End:         end,
		SpaceUid:    influxdb.SpaceUid,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	code, _ := failedStatusOf(t, err)
	require.Equal(t, http.StatusGatewayTimeout, code)
}
