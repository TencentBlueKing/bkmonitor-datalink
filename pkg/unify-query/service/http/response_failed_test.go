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
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jarcoal/httpmock"
	promPromql "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

// slowBackendURL 是一个不应答的存储后端：请求一直挂着，直到请求自己的 context 结束。
const slowBackendURL = "http://slow-backend.example.test/query_sync/"

// slowBackendError 是存储查询经 curl 请求这个后端得到的错误，按 VictoriaMetrics 实例的包法再包一层，
// 与线上 "查询异常: 查询 <地址> 超时: Post <地址>: context deadline exceeded" 同一条链路。
// 本包的用例经 mock.Init 统一走 httpmock，这里同样注册在 httpmock 上。
func slowBackendError(t *testing.T, prepare func(ctx context.Context) (context.Context, context.CancelFunc), timeout time.Duration) error {
	t.Helper()
	mock.Init()
	httpmock.RegisterResponder(http.MethodPost, slowBackendURL, func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})

	ctx, cancel := prepare(context.Background())
	defer cancel()
	var res map[string]any
	_, err := (&curl.HttpCurl{}).Request(ctx, curl.Post, curl.Options{UrlPath: slowBackendURL, Body: []byte(`{}`), Timeout: timeout}, &res)
	require.Error(t, err)
	return metadata.NewMessage(metadata.MsgQueryVictoriaMetrics, "查询异常").Error(ctx, err)
}

func failedStatusOf(t *testing.T, err error) (int, ErrResponse) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/query/ts", nil)
	(&response{c: c}).failed(context.Background(), err)
	var body ErrResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return recorder.Code, body
}

func TestFailedAnswersBackendTimeoutAsGatewayTimeout(t *testing.T) {
	deadline := func(ctx context.Context) (context.Context, context.CancelFunc) {
		return context.WithTimeout(ctx, 50*time.Millisecond)
	}
	noDeadline := func(ctx context.Context) (context.Context, context.CancelFunc) {
		return context.WithCancel(ctx)
	}
	canceled := func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(ctx)
		time.AfterFunc(50*time.Millisecond, cancel)
		return ctx, cancel
	}

	cases := map[string]struct {
		err  error
		want int
	}{
		"storage request passes its context deadline": {err: slowBackendError(t, deadline, 0), want: http.StatusGatewayTimeout},
		"storage request passes its client timeout":   {err: slowBackendError(t, noDeadline, 50*time.Millisecond), want: http.StatusGatewayTimeout},
		"query engine times out": {
			err:  metadata.NewMessage(metadata.MsgQueryTs, "查询异常").Error(context.Background(), promPromql.ErrQueryTimeout("query execution")),
			want: http.StatusGatewayTimeout,
		},
		"caller cancels the request": {err: slowBackendError(t, canceled, 0), want: http.StatusBadRequest},
		"request is refused as written": {
			err:  metadata.NewMessage(metadata.MsgQueryTs, "查询异常").Error(context.Background(), errors.New("1:10: parse error: unexpected character")),
			want: http.StatusBadRequest,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			code, body := failedStatusOf(t, c.err)
			require.Equal(t, c.want, code)
			require.Equal(t, c.err.Error(), body.Err)
		})
	}
}
