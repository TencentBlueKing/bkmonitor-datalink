// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

// The gateway answers a proxied query's failure with the same status the
// query's own endpoint would: a storage timeout is 504, anything else 400.
func TestTheGatewayAnswersAProxiedTimeoutWithGatewayTimeout(t *testing.T) {
	mock.Init()
	cases := map[string]struct {
		err  error
		want int
	}{
		"a storage timeout":       {err: fmt.Errorf("查询异常: %w", context.DeadlineExceeded), want: http.StatusGatewayTimeout},
		"a caller that cancelled": {err: fmt.Errorf("查询异常: %w", context.Canceled), want: http.StatusBadRequest},
		"a refused request":       {err: errors.New("1:10: parse error"), want: http.StatusBadRequest},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			path := "/test/proxy-status/" + strings.ReplaceAll(name, " ", "-")
			metadata.AddHandler(path, func(ctx *gin.Context) {
				ctx.Set(ContextKeyResponseError, c.err)
			})
			recorder := httptest.NewRecorder()
			ginCtx, _ := gin.CreateTestContext(recorder)
			ginCtx.Request = httptest.NewRequest(http.MethodPost, "/query/ts/proxy",
				strings.NewReader(`{"path":"`+path+`","data":{}}`))
			HandleProxy(ginCtx)
			require.Equal(t, c.want, recorder.Code)
			require.Contains(t, recorder.Body.String(), c.err.Error())
		})
	}
}
