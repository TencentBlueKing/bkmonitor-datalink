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
	"context"
	"net/http"
	"net/http/pprof"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// registerProfile
func registerProfile(ctx context.Context, g *gin.RouterGroup) {
	path := viper.GetString(ProfilePathConfigPath)
	g.GET(path, gin.WrapF(pprof.Index))
	g.GET(path+"cmdline", gin.WrapF(pprof.Cmdline))
	g.GET(path+"profile", gin.WrapF(pprof.Profile))
	g.GET(path+"symbol", gin.WrapF(pprof.Symbol))
	g.GET(path+"trace", gin.WrapF(pprof.Trace))
	g.GET(path+"goroutine", gin.WrapF(func(writer http.ResponseWriter, request *http.Request) {
		pprof.Handler("goroutine").ServeHTTP(writer, request)
	}))

	g.GET(path+"heap", gin.WrapF(func(writer http.ResponseWriter, request *http.Request) {
		pprof.Handler("heap").ServeHTTP(writer, request)
	}))

	codedInfo := errno.ErrInfoServiceStart().
		WithComponent("Profile服务").
		WithOperation("启动服务器").
		WithContext("状态", "成功")
	log.InfoWithCodef(ctx, codedInfo)
}
