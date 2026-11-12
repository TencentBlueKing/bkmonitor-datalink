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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

func TestHandleRelationSchemaPrintShowsSources(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := v1beta3.GetUnderlyingProvider(v1beta3.GetSchemaProvider())
	t.Cleanup(func() { v1beta3.InitSchemaProvider(previous) })
	v1beta3.InitSchemaProvider(relation.NewDefaultStaticSchemaProvider())

	request := func(target string) string {
		t.Helper()
		writer := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(writer)
		ctx.Request = httptest.NewRequest(http.MethodGet, target, nil)
		HandleRelationSchemaPrint(ctx)
		require.Equal(t, http.StatusOK, writer.Code)
		return writer.Body.String()
	}
	body := request("/relation_schema_print?namespace=bkcc__2")
	require.Contains(t, body, "provider: static\nnamespace: bkcc__2\nlookup: builtin\n")
	require.True(t, strings.Contains(body, "\npod\tbuiltin\tbuiltin=same\n"), body)
	require.True(t, strings.Contains(body, "\nnode_with_pod\tbuiltin\tbuiltin=same\n"), body)
	require.NotContains(t, body, `"bcs_cluster_id"`)

	require.Contains(t, body, "\noverlay: unavailable last_success=- ")
	require.Contains(t, request("/relation_schema_print"), "namespace: __all__\n")
	require.Contains(t, request("/relation_schema_print?content=true"), `"bcs_cluster_id"`)

	var decoded struct {
		Provider  string `json:"provider"`
		Namespace string `json:"namespace"`
		Overlay   struct {
			State    string           `json:"state"`
			Failures map[string]int64 `json:"failures"`
		} `json:"overlay"`
		Resources []struct {
			Name           string          `json:"name"`
			Source         string          `json:"source"`
			MatchesBuiltin bool            `json:"matches_builtin"`
			Definition     json.RawMessage `json:"definition"`
		} `json:"resources"`
	}
	require.NoError(t, json.Unmarshal([]byte(request("/relation_schema_print?namespace=bkcc__2&format=json")), &decoded))
	require.Equal(t, "static", decoded.Provider)
	require.Equal(t, "bkcc__2", decoded.Namespace)
	require.Equal(t, "unavailable", decoded.Overlay.State)
	require.Contains(t, decoded.Overlay.Failures, "read_failed")
	require.Len(t, decoded.Resources, len(relation.DefaultResourceDefinitions()))
	for _, item := range decoded.Resources {
		require.Equal(t, "builtin", item.Source)
		require.True(t, item.MatchesBuiltin)
		require.Empty(t, item.Definition, "content=false 不带定义内容")
	}
	require.Contains(t, request("/relation_schema_print?format=json&content=true"), `"definition":{`)

	writer := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(writer)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/relation_schema_print?format=yaml", nil)
	HandleRelationSchemaPrint(ctx)
	require.Equal(t, http.StatusBadRequest, writer.Code)
}
