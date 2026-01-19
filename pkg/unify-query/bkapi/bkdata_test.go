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
	"sync"
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

// TestBkDataDirectAddress 测试直接使用 bk_data.address 配置
func TestBkDataDirectAddress(t *testing.T) {
	mock.Init()

	// 设置 bk_data.address 配置
	originalAddress := viper.GetString(BkDataAddressConfigPath)
	defer func() {
		// 恢复原始配置
		if originalAddress != "" {
			viper.Set(BkDataAddressConfigPath, originalAddress)
		} else {
			viper.Set(BkDataAddressConfigPath, "")
		}
		// 重置单例，以便重新读取配置
		onceBkDataAPI = sync.Once{}
		defaultBkDataAPI = nil
	}()

	// 测试直接使用 bk_data.address 的场景
	directAddress := "http://127.1.1.1:8000/v3/queryengine"
	viper.Set(BkDataAddressConfigPath, directAddress)

	// 重置单例以重新读取配置
	onceBkDataAPI = sync.Once{}
	defaultBkDataAPI = nil

	bkDataAPI := GetBkDataAPI()

	// 验证 QueryUrl 使用直接地址
	url := bkDataAPI.QueryUrl("")
	expected := "http://127.1.1.1:8000/v3/queryengine/query_sync/"
	assert.Equal(t, expected, url, "应该直接使用 bk_data.address，不再通过 bk_api.address 组装")

	// 测试使用空的场景
	viper.Set(BkDataAddressConfigPath, "")
	onceBkDataAPI = sync.Once{}
	defaultBkDataAPI = nil

	bkDataAPI = GetBkDataAPI()
	url = bkDataAPI.QueryUrl("")
	expected = "http://127.0.0.1:12001/bk_data/query_sync/"
	assert.Equal(t, expected, url, "应该使用 bk_api.address 组装")
}
