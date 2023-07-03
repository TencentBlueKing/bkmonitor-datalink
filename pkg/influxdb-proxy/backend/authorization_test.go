// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package backend_test

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
)

// 测试认证头的封装能力是否正常
func TestSetBasicAuth(t *testing.T) {
	username := "user1"
	password := "pass1"
	// 认证对象
	auth := backend.NewBasicAuth(username, password)
	req, err := http.NewRequest("POST", "http://127.0.0.1:8080/ttt", strings.NewReader("ttt"))
	assert.Nil(t, err)
	// 封装认证信息到request头部
	err = auth.SetAuth(req)
	assert.Nil(t, err)
	str := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	code := "Basic " + str
	// 判断封装结果是否符合预期
	assert.Equal(t, code, req.Header.Get("Authorization"))
}
