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
	"net/http/pprof"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/endpoint"
)

// registerProfile
func registerProfile(registerHandler *endpoint.RegisterHandler) {
	path := viper.GetString(ProfilePathConfigPath)

	registerHandler.RegisterWithOutHandlerMap(http.MethodGet, path, gin.WrapF(pprof.Index))
	registerHandler.RegisterWithOutHandlerMap(http.MethodGet, path+"cmdline", gin.WrapF(pprof.Cmdline))
	registerHandler.RegisterWithOutHandlerMap(http.MethodGet, path+"profile", gin.WrapF(pprof.Profile))
	registerHandler.RegisterWithOutHandlerMap(http.MethodGet, path+"symbol", gin.WrapF(pprof.Symbol))
	registerHandler.RegisterWithOutHandlerMap(http.MethodGet, path+"trace", gin.WrapF(pprof.Trace))
	registerHandler.RegisterWithOutHandlerMap(http.MethodGet, path+"goroutine", gin.WrapF(func(writer http.ResponseWriter, request *http.Request) {
		pprof.Handler("goroutine").ServeHTTP(writer, request)
	}))
	registerHandler.RegisterWithOutHandlerMap(http.MethodGet, path+"heap", gin.WrapF(func(writer http.ResponseWriter, request *http.Request) {
		pprof.Handler("heap").ServeHTTP(writer, request)
	}))
}
