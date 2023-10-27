// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package auth_test

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/http/auth"
)

func TestAuthCheck(t *testing.T) {
	username := "test"
	password := "testpass"
	viper.Set("authorization.enable", true)
	viper.Set("authorization.username", username)
	viper.Set("authorization.password", password)
	key := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	ba, err := auth.NewBasicAuth()
	assert.Nil(t, err)
	req, err := http.NewRequest("POST", "http://127.0.0.1:8080", strings.NewReader("test"))
	assert.Nil(t, err)
	req.Header.Set("Authorization", "Basic "+key)
	assert.True(t, ba.Check(req))
}
