// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package auth

import (
	"encoding/base64"
	"net/http"

	"github.com/spf13/viper"
)

// BasicAuth 基本校验
type BasicAuth struct {
	inUse bool
	code  string
}

// NewBasicAuth :
func NewBasicAuth() (Auth, error) {
	ba := new(BasicAuth)
	ba.inUse = viper.GetBool("authorization.enable")
	username := viper.GetString("authorization.username")
	password := viper.GetString("authorization.password")
	auth := username + ":" + password
	ba.code = "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
	return ba, nil
}

// Check :
func (ba *BasicAuth) Check(request *http.Request) bool {
	// 如果不开启则认证默认通过
	if !ba.inUse {
		return true
	}
	header := request.Header
	authMsg := header.Get("Authorization")
	if authMsg == ba.code {
		return true
	}

	return false
}
