// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package backend

import (
	"encoding/base64"
	"net/http"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

// Auth 认证填充
type Auth interface {
	SetAuth(request *http.Request) error
}

// basicAuth 基础认证实现
type basicAuth struct {
	code string
}

// NewBasicAuth :
func NewBasicAuth(username string, password string) Auth {
	ba := new(basicAuth)
	// 如果用户名未传入，则不会有认证头
	if username == "" {
		ba.code = ""
	}
	path := username + ":" + password
	ba.code = base64.StdEncoding.EncodeToString([]byte(path))
	return ba
}

// SetAuth 将认证写入头部
func (ba *basicAuth) SetAuth(request *http.Request) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	if ba.code == "" {
		flowLog.Info("get empty authentication infomation,skip pushing authorization into header")
		return nil
	}
	request.Header.Set("Authorization", "Basic "+ba.code)
	return nil
}
