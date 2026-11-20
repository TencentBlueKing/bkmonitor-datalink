// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestQueryFlagsMiddleware(t *testing.T) {
	original := viper.GetBool(metadata.DorisDirectQueryConfigPath)
	viper.Set(metadata.DorisDirectQueryConfigPath, false)
	t.Cleanup(func() { viper.Set(metadata.DorisDirectQueryConfigPath, original) })
	for _, tc := range []struct {
		header string
		status int
		direct bool
	}{
		{"", 200, false}, {`{"doris_direct_query":true}`, 200, true},
		{`{"doris_direct_query":false}`, 200, false}, {`{"future_flag":123}`, 200, false},
		{`{"doris_direct_query":"true"}`, 400, false}, {`[]`, 400, false},
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			called := false
			g := gin.New()
			g.Use(QueryFlags())
			g.Handle(method, "/query", func(c *gin.Context) {
				called = true
				require.Equal(t, tc.direct, metadata.GetQueryFlags(c.Request.Context()).DorisDirectQuery)
				c.Status(http.StatusOK)
			})
			r := httptest.NewRequest(method, "/query", nil)
			r.Header.Set(metadata.QueryFlagsHeader, tc.header)
			w := httptest.NewRecorder()
			g.ServeHTTP(w, r)
			require.Equal(t, tc.status, w.Code)
			require.Equal(t, tc.status == 200, called)
		}
	}
}
