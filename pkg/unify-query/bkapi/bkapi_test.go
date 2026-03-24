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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestGetBkAPI(t *testing.T) {
	mock.Init()

	code := GetBkAPI().GetCode()
	assert.Equal(t, "bk_code", code)

	url := GetBkAPI().Url("query")
	assert.Equal(t, "http://127.0.0.1:12001/query", url)

	headers := GetBkAPI().Headers(map[string]string{
		"Content-Type": "application/json",
	})

	actual := make(map[string]any)
	for k, v := range headers {
		var nv map[string]string
		if err := json.Unmarshal([]byte(v), &nv); err != nil {
			actual[k] = v
		} else {
			actual[k] = nv
		}
	}

	expected := map[string]any{
		"Content-Type": "application/json",
		"X-Bkapi-Authorization": map[string]string{
			"bk_app_code":   "bk_code",
			"bk_app_secret": "bk_secret",
			"bk_username":   "admin",
		},
	}

	assert.Equal(t, expected, actual)
}
