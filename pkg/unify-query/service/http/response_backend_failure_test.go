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
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

const failingDorisMatcherName = "failing-doris-route"

// failingDorisRoute answers the Doris table's data queries with fail, its
// schema query as usual.
func failingDorisRoute(t *testing.T, fail httpmock.Responder) context.Context {
	t.Helper()
	mock.Init()
	promql.MockEngine()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)
	metadata.SetUser(ctx, &metadata.User{Key: "username:test", SpaceUID: influxdb.SpaceUid, SkipSpace: "true"})

	matcher := httpmock.BodyContainsString("2_bklog_bkunify_query_doris").WithName(failingDorisMatcherName)
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
		return fail(req)
	})
	t.Cleanup(func() {
		httpmock.RegisterMatcherResponder(http.MethodPost, mock.BkBaseUrl, httpmock.NewMatcher(failingDorisMatcherName, nil), nil)
	})
	return ctx
}

func engineRoutedDorisQuery() *structured.QueryTs {
	start, end := slowDorisWindow()
	return &structured.QueryTs{
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
	}
}

// A storage that fails on its side -- it answers 503, refuses this service's
// credentials, or cannot be reached -- answers 502 through the engine and the
// route querier; one that refuses the request (400) still answers 400.
func TestAnEngineRoutedBackendFailureAnswersBadGateway(t *testing.T) {
	refused := func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	}
	cases := map[string]struct {
		fail httpmock.Responder
		want int
	}{
		"the storage answers 503":                        {fail: httpmock.NewStringResponder(http.StatusServiceUnavailable, ""), want: http.StatusBadGateway},
		"the storage refuses this service's credentials": {fail: httpmock.NewStringResponder(http.StatusUnauthorized, ""), want: http.StatusBadGateway},
		"the storage cannot be reached":                  {fail: refused, want: http.StatusBadGateway},
		"the storage refuses the request":                {fail: httpmock.NewStringResponder(http.StatusBadRequest, ""), want: http.StatusBadRequest},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := failingDorisRoute(t, c.fail)
			_, err := queryTsWithPromEngine(ctx, engineRoutedDorisQuery())
			require.Error(t, err)
			code, body := failedStatusOf(t, err)
			require.Equal(t, c.want, code, err.Error())
			require.Equal(t, err.Error(), body.Err)
		})
	}
}
