// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package aegisv2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWhitelistResponse_TimeFieldsAreNumbers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, routeAegisV2Whitelist, nil)
	resp := httptest.NewRecorder()

	httpSvc.Whitelist(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)

	var body struct {
		Code            int                `json:"code"`
		Msg             string             `json:"msg"`
		IsInWhiteList   int                `json:"is_in_white_list"`
		SampleMap       whitelistSampleMap `json:"sample_map"`
		ServerTime      int64              `json:"server_time"`
		StartServerTime int64              `json:"start_server_time"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))

	assert.Equal(t, 0, body.Code)
	assert.Equal(t, "success", body.Msg)
	assert.Equal(t, 0, body.IsInWhiteList)
	assert.Equal(t, whitelistSample, body.SampleMap)
	assert.Greater(t, body.ServerTime, int64(0))
	assert.GreaterOrEqual(t, body.ServerTime, body.StartServerTime)
	assert.Equal(t, body.ServerTime, body.StartServerTime)
}

func TestWhitelistResponse_StartServerTimeIgnoresUIDTimestamp(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodGet,
		routeAegisV2Whitelist+"?uid=user_1781749136169_b838746b&topic=SDK-daffdasfdasfdsafdas",
		nil,
	)
	resp := httptest.NewRecorder()

	httpSvc.Whitelist(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)

	var body struct {
		ServerTime      int64 `json:"server_time"`
		StartServerTime int64 `json:"start_server_time"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))

	assert.Equal(t, body.ServerTime, body.StartServerTime)
	assert.GreaterOrEqual(t, body.ServerTime, body.StartServerTime)
}
