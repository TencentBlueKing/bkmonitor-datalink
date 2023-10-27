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
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
)

// AuthHandler
type AuthHandler struct {
	PublicPrefix []string
	Token        string
	Handler      http.Handler
}

func (h *AuthHandler) reject(writer http.ResponseWriter) {
	writer.Header().Set("WWW-Authenticate", `Basic realm="all"`)
	writer.WriteHeader(http.StatusUnauthorized)
	_, err := writer.Write([]byte(`<head><script>location.href="/";</script></head>`))
	if err != nil {
		logging.Warnf("write index response error: %v", err)
	}
}

func (h *AuthHandler) isPublicRequest(request *http.Request) bool {
	urlPath := request.URL.Path

	if urlPath == "/" {
		return true
	}

	for _, prefix := range h.PublicPrefix {
		if strings.HasPrefix(urlPath, prefix) {
			return true
		}
	}
	return false
}

func (h *AuthHandler) isAuthenticate(writer http.ResponseWriter, request *http.Request) bool {
	if h.Token == "" {
		return true
	}

	if h.isPublicRequest(request) {
		return true
	}

	prefix := "Basic "
	value := request.Header.Get("Authorization")
	if !strings.HasPrefix(value, prefix) {
		return false
	}

	suffix := value[len(prefix):]
	payload, err := base64.StdEncoding.DecodeString(suffix)
	if err != nil {
		return false
	}

	return h.Token == string(payload)
}

// ServeHTTP
func (h *AuthHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	logging.Debugf("serve http request %v", request.URL)

	// /debug 路由不需要鉴权
	if h.isAuthenticate(writer, request) || strings.HasPrefix(request.URL.String(), "/debug") {
		h.Handler.ServeHTTP(writer, request)
	} else {
		logging.Warnf("http rejected request %v", request.URL)
		h.reject(writer)
	}
}
