// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package receiver

import (
	"net/http"
	"net/http/pprof"
	"sort"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/pprofsnapshot"
)

func init() {
	const statsSource = "stats"
	registerAdminHttpGetRoute(statsSource, "/metrics", func(w http.ResponseWriter, r *http.Request) {
		promhttp.Handler().ServeHTTP(w, r)
	})

	const adminSource = "admin"
	registerAdminHttpPostRoute(adminSource, "/-/logger", func(w http.ResponseWriter, r *http.Request) {
		level := r.FormValue("level")
		logger.SetLoggerLevel(level)
		w.Write([]byte(`{"status": "success"}`))
	})
	registerAdminHttpPostRoute(adminSource, "/-/reload", func(w http.ResponseWriter, r *http.Request) {
		beat.ReloadChan <- true
		w.Write([]byte(`{"status": "success"}`))
	})
	registerAdminHttpGetRoute(adminSource, "/-/routes", func(w http.ResponseWriter, r *http.Request) {
		var routes []define.RouteInfo
		routes = append(routes, RecvHttpRoutes()...)
		routes = append(routes, AdminHttpRoutes()...)
		b, _ := json.Marshal(routes)
		w.Write(b)
	})
	registerAdminHttpGetRoute(adminSource, "/-/healthz", func(w http.ResponseWriter, r *http.Request) {
		if confengine.LoadedPlatformConfig() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable) // 未加载到平台配置表示服务未就绪
	})

	const pprofSource = "pprof"
	registerAdminHttpGetRoute(pprofSource, "/debug/pprof/snapshot", pprofsnapshot.HandlerFuncFor())
	registerAdminHttpGetRoute(pprofSource, "/debug/pprof/cmdline", pprof.Cmdline)
	registerAdminHttpGetRoute(pprofSource, "/debug/pprof/profile", pprof.Profile)
	registerAdminHttpGetRoute(pprofSource, "/debug/pprof/symbol", pprof.Symbol)
	registerAdminHttpGetRoute(pprofSource, "/debug/pprof/trace", pprof.Trace)
	registerAdminHttpGetRoute(pprofSource, "/debug/pprof/{other}", pprof.Index)
}

// AdminHttpRouter 返回 Admin mux.Router
func AdminHttpRouter() *mux.Router {
	return adminMgr.httpRouter
}

// AdminHttpRoutes 返回 Admin 注册的路由表
func AdminHttpRoutes() []define.RouteInfo {
	var routes []define.RouteInfo
	for _, v := range adminMgr.httpRoutes {
		routes = append(routes, v)
	}

	sort.Slice(routes, func(i, j int) bool {
		return routes[i].ID() < routes[j].ID()
	})
	return routes
}

var adminMgr = &serviceManager{
	httpRoutes: map[string]define.RouteInfo{},
	httpRouter: mux.NewRouter(),
}

func registerAdminHttpGetRoute(source, relativePath string, handleFunc http.HandlerFunc) {
	err := registerHttpRoute(source, http.MethodGet, relativePath, handleFunc, adminMgr)
	if err != nil {
		panic(err)
	}
}

func registerAdminHttpPostRoute(source, relativePath string, handleFunc http.HandlerFunc) {
	err := registerHttpRoute(source, http.MethodPost, relativePath, handleFunc, adminMgr)
	if err != nil {
		panic(err)
	}
}
