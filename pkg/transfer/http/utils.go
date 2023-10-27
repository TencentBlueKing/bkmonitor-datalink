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
	"net/http"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
)

// GetBasicAuthInfo
func GetBasicAuthInfo(conf define.Configuration) (user string, password string) {
	auth := conf.GetString(ConfAuthToken)
	parts := strings.SplitN(auth, ":", 2)
	user = parts[0]
	password = parts[1]
	return
}

// WriteJSONResponse write json as http response
func WriteJSONResponse(status int, writer http.ResponseWriter, data interface{}) {
	payload, err := json.Marshal(data)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}

	header := writer.Header()
	header.Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, err = writer.Write(payload)
	if err != nil {
		logging.Warnf("write json payload error: %v", err)
	}
}
