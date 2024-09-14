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
	"fmt"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestGetDataAuth(t *testing.T) {
	mock.Path = `../dist/local/unify-query.yaml`
	mock.Init()

	headers := GetBkDataAPI().Headers(nil)
	fmt.Println(headers)
}

func TestGetDataUrl(t *testing.T) {
	mock.Path = `../dist/local/unify-query.yaml`
	mock.Init()

	testCase := map[string]struct {
		spaceUid string
		url      string
	}{
		"test-1": {
			spaceUid: "default",
			url:      `http://127.0.0.1/api/bk-base/prod/v3/queryengine/query_sync`,
		},
		"test-2": {
			spaceUid: "bkcc__test",
			url:      `http://127.0.0.1/api/bk-base/prod/v3/queryengine/test/query_sync`,
		},
		"test-3": {
			spaceUid: "bkci__test",
			url:      `http://127.0.0.1/api/bk-base/prod/v3/queryengine/test/query_sync`,
		},
	}

	viper.Set(BkAPIAddressConfigPath, "http://127.0.0.1")

	for name, c := range testCase {
		t.Run(name, func(t *testing.T) {
			url := GetBkDataAPI().QueryUrl(c.spaceUid)
			assert.Equal(t, c.url, url)
		})
	}

}
