// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkapi

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestGetDataAuth(t *testing.T) {
	mock.Init()

	headers := GetBkDataAPI().Headers(map[string]string{
		"Content-Type": "application/json",
	})

	assert.Equal(t, "application/json", headers["Content-Type"])
	assert.JSONEq(t, `{"bk_app_code":"bk_code","bk_app_secret":"bk_secret","bk_username":"admin"}`, headers["X-Bkapi-Authorization"])
	assert.JSONEq(t, `{"bk_username":"admin","bkdata_data_token":"123456","bkdata_authentication_method":"token","bk_app_code":"bk_code"}`, headers["X-Bkbase-Authorization"])
}

func TestGetDataUrl(t *testing.T) {
	mock.Init()

	testCase := map[string]struct {
		spaceUid string
		url      string
	}{
		"test-1": {
			spaceUid: "default",
			url:      `http://127.0.0.1:12001/bk_data/query_sync/`,
		},
		"test-2": {
			spaceUid: "bkcc__test",
			url:      `http://127.0.0.1:12001/bk_data/test/query_sync/`,
		},
		"test-3": {
			spaceUid: "bkci__test",
			url:      `http://127.0.0.1:12001/bk_data/query_sync/`,
		},
	}

	for name, c := range testCase {
		t.Run(name, func(t *testing.T) {
			url := GetBkDataAPI().QueryUrl(c.spaceUid)

			clusterSpaceUid := viper.GetStringMapStringSlice(BkDataClusterSpaceUidConfigPath)
			assert.Equal(t, []string{"bkcc__test"}, clusterSpaceUid["test"])

			assert.Equal(t, c.url, url)
		})
	}
}

// TestAddressAssembly 测试地址组装逻辑
func TestAddressAssembly(t *testing.T) {
	mock.Init()

	t.Run("BkAPI基础地址组装", func(t *testing.T) {
		bkAPI := GetBkAPI()

		// 测试无路径
		url := bkAPI.Url("")
		assert.Equal(t, "http://127.0.0.1:12001", url)

		// 测试有路径
		url = bkAPI.Url("api/v1")
		assert.Equal(t, "http://127.0.0.1:12001/api/v1", url)

		// 测试单路径
		url = bkAPI.Url("query")
		assert.Equal(t, "http://127.0.0.1:12001/query", url)
	})

	t.Run("BkDataAPI地址组装-bk_api.address + bk_data.uri_path", func(t *testing.T) {
		bkDataAPI := GetBkDataAPI()

		// 测试 QueryUrl - 验证 bk_api.address + bk_data.uri_path 的组合
		// 配置: bk_api.address = http://127.0.0.1:12001, bk_data.uri_path = bk_data
		// 期望: http://127.0.0.1:12001/bk_data/query_sync/
		url := bkDataAPI.QueryUrl("")
		assert.Equal(t, "http://127.0.0.1:12001/bk_data/query_sync/", url)

		// 测试 QueryUrlForES - 在 QueryUrl 基础上追加 /es
		// 注意: QueryUrl 返回的URL末尾有斜杠，所以会变成 query_sync//es
		url = bkDataAPI.QueryUrlForES("")
		assert.Equal(t, "http://127.0.0.1:12001/bk_data/query_sync//es", url)
	})

	t.Run("完整URL组装流程", func(t *testing.T) {
		bkDataAPI := GetBkDataAPI()

		testCases := []struct {
			name     string
			spaceUid string
			expected string
		}{
			{
				name:     "无spaceUid",
				spaceUid: "",
				expected: "http://127.0.0.1:12001/bk_data/query_sync/",
			},
			{
				name:     "有spaceUid但不在clusterMap中",
				spaceUid: "unknown_space",
				expected: "http://127.0.0.1:12001/bk_data/query_sync/",
			},
			{
				name:     "有spaceUid且在clusterMap中",
				spaceUid: "bkcc__test",
				expected: "http://127.0.0.1:12001/bk_data/test/query_sync/",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				url := bkDataAPI.QueryUrl(tc.spaceUid)
				assert.Equal(t, tc.expected, url, "spaceUid: %s", tc.spaceUid)
			})
		}
	})

	t.Run("地址路径拼接规则", func(t *testing.T) {
		bkAPI := GetBkAPI()

		// 测试基础地址
		assert.Equal(t, "http://127.0.0.1:12001", bkAPI.Url(""))

		// 测试单级路径
		assert.Equal(t, "http://127.0.0.1:12001/api", bkAPI.Url("api"))

		// 测试多级路径
		assert.Equal(t, "http://127.0.0.1:12001/api/v1/query", bkAPI.Url("api/v1/query"))

		// 测试与bk_data.uri_path的模拟组合
		// 实际组合: bk_api.address + bk_data.uri_path + query_sync
		// http://127.0.0.1:12001 + /bk_data + /query_sync/
		expectedBase := "http://127.0.0.1:12001/bk_data/query_sync"
		simulatedUrl := bkAPI.Url("bk_data/query_sync")
		assert.Equal(t, expectedBase, simulatedUrl)
	})

	t.Run("地址组装边界情况", func(t *testing.T) {
		bkAPI := GetBkAPI()

		// 测试空字符串路径
		url := bkAPI.Url("")
		assert.Equal(t, "http://127.0.0.1:12001", url)

		// 测试带斜杠的路径 - BkAPI.Url 会直接拼接，导致双斜杠
		url = bkAPI.Url("/api/v1")
		assert.Equal(t, "http://127.0.0.1:12001//api/v1", url)

		// 测试多级路径
		url = bkAPI.Url("level1/level2/level3")
		assert.Equal(t, "http://127.0.0.1:12001/level1/level2/level3", url)
	})

	t.Run("验证配置值正确读取", func(t *testing.T) {
		// 验证配置值
		bkAPIAddress := viper.GetString(BkAPIAddressConfigPath)
		bkDataUriPath := viper.GetString(BkDataUriPathConfigPath)

		assert.Equal(t, "http://127.0.0.1:12001", bkAPIAddress)
		assert.Equal(t, "bk_data", bkDataUriPath)

		// 验证组装后的URL包含这两个配置值
		bkDataAPI := GetBkDataAPI()
		url := bkDataAPI.QueryUrl("")
		assert.Contains(t, url, bkAPIAddress)
		assert.Contains(t, url, bkDataUriPath)
	})
}

// TestGetBkDataAPIWithAddress 测试使用直接地址的功能
func TestGetBkDataAPIWithAddress(t *testing.T) {
	mock.Init()

	t.Run("使用直接地址-新地址", func(t *testing.T) {
		// 新地址：http://127.0.0.1:8000/v3/queryengine
		directAddress := "http://127.0.0.1:8000/v3/queryengine"
		bkDataAPI := GetBkDataAPIWithAddress(directAddress)

		// 测试 QueryUrl - 应该直接使用新地址，不再通过 bk_api.address + bk_data.uri_path 组装
		url := bkDataAPI.QueryUrl("")
		expected := "http://127.0.0.1:8000/v3/queryengine/query_sync/"
		assert.Equal(t, expected, url, "应该直接使用 directAddress，不再通过 bk_api.address 组装")

		// 测试 QueryUrlForES
		url = bkDataAPI.QueryUrlForES("")
		expected = "http://127.0.0.1:8000/v3/queryengine/query_sync//es"
		assert.Equal(t, expected, url)

		// 测试有 spaceUid 的情况
		url = bkDataAPI.QueryUrl("bkcc__test")
		expected = "http://127.0.0.1:8000/v3/queryengine/test/query_sync/"
		assert.Equal(t, expected, url)
	})

	t.Run("空地址时使用默认实例", func(t *testing.T) {
		// 空地址应该返回默认实例
		defaultAPI := GetBkDataAPI()
		customAPI := GetBkDataAPIWithAddress("")

		// 应该返回相同的默认实例
		url1 := defaultAPI.QueryUrl("")
		url2 := customAPI.QueryUrl("")
		assert.Equal(t, url1, url2)
	})

	t.Run("验证直接地址不影响认证配置", func(t *testing.T) {
		directAddress := "http://127.0.0.1:8000/v3/queryengine"
		bkDataAPI := GetBkDataAPIWithAddress(directAddress)

		// 验证认证配置仍然正确
		auth := bkDataAPI.GetDataAuth()
		assert.NotEmpty(t, auth)
		assert.Equal(t, "admin", auth[BkUserNameKey])
		assert.Equal(t, "bk_code", auth[BkAppCodeKey])

		// 验证 Headers 仍然正确
		headers := bkDataAPI.Headers(nil)
		assert.Contains(t, headers, "X-Bkapi-Authorization")
		assert.Contains(t, headers, "X-Bkbase-Authorization")
	})

	t.Run("对比原始组装方式和直接地址方式", func(t *testing.T) {
		// 原始方式：通过 bk_api.address + bk_data.uri_path 组装
		defaultAPI := GetBkDataAPI()
		originalUrl := defaultAPI.QueryUrl("")
		// 期望：http://127.0.0.1:12001/bk_data/query_sync/
		assert.Contains(t, originalUrl, "http://127.0.0.1:12001")
		assert.Contains(t, originalUrl, "bk_data")

		// 直接地址方式
		directAddress := "http://127.0.0.1:8000/v3/queryengine"
		directAPI := GetBkDataAPIWithAddress(directAddress)
		directUrl := directAPI.QueryUrl("")
		// 期望：http://127.0.0.1:8000/v3/queryengine/query_sync/
		assert.Equal(t, "http://127.0.0.1:8000/v3/queryengine/query_sync/", directUrl)
		assert.NotContains(t, directUrl, "bk_data")
		assert.NotContains(t, directUrl, "http://127.0.0.1:12001")
	})
}
