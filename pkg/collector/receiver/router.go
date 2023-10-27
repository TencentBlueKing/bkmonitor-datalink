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
	"fmt"
	"net/http"
	"net/http/pprof"
	"runtime/debug"
	"sort"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/pprofsnapshot"
)

type Ready func()

var componentsReady = map[string]Ready{}

func RegisterReadyFunc(source string, f Ready) {
	componentsReady[source] = f
}

func init() {
	const statsSource = "stats"
	mustRegisterHttpGetRoute(statsSource, "/metrics", func(w http.ResponseWriter, r *http.Request) {
		promhttp.Handler().ServeHTTP(w, r)
	})
	mustRegisterHttpGetRoute(statsSource, "/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("pong"))
	})

	const adminSource = "admin"
	mustRegisterHttpPostRoute(adminSource, "/-/logger", func(w http.ResponseWriter, r *http.Request) {
		level := r.FormValue("level")
		logger.SetLoggerLevel(level)
		w.Write([]byte(`{"status": "success"}`))
	})
	mustRegisterHttpPostRoute(adminSource, "/-/reload", func(w http.ResponseWriter, r *http.Request) {
		beat.ReloadChan <- true
		w.Write([]byte(`{"status": "success"}`))
	})

	// debug 专用
	mustRegisterHttpPostRoute(adminSource, "/-/freemem", func(w http.ResponseWriter, r *http.Request) {
		debug.FreeOSMemory()
		w.Write([]byte(`{"status": "success"}`))
	})

	const pprofSource = "pprof"
	mustRegisterHttpGetRoute(pprofSource, "/debug/pprof/snapshot", pprofsnapshot.HandlerFuncFor())
	mustRegisterHttpGetRoute(pprofSource, "/debug/pprof/cmdline", pprof.Cmdline)
	mustRegisterHttpGetRoute(pprofSource, "/debug/pprof/profile", pprof.Profile)
	mustRegisterHttpGetRoute(pprofSource, "/debug/pprof/symbol", pprof.Symbol)
	mustRegisterHttpGetRoute(pprofSource, "/debug/pprof/trace", pprof.Trace)
	mustRegisterHttpGetRoute(pprofSource, "/debug/pprof/{other}", pprof.Index)
}

type serviceManager struct {
	httpRoutes   map[string]define.RouteInfo
	httpRouter   *mux.Router
	grpcServices []func(s *grpc.Server)
}

var serviceMgr = &serviceManager{
	httpRoutes: map[string]define.RouteInfo{},
	httpRouter: mux.NewRouter(),
}

// HttpRouter 返回全局 mux.Router
func HttpRouter() *mux.Router {
	return serviceMgr.httpRouter
}

// HttpRoutes 返回已经注册的路由表
func HttpRoutes() []define.RouteInfo {
	var routes []define.RouteInfo
	for _, v := range serviceMgr.httpRoutes {
		routes = append(routes, v)
	}

	sort.Slice(routes, func(i, j int) bool {
		return routes[i].ID() < routes[j].ID()
	})
	return routes
}

type RouteWithFunc struct {
	Method       string
	RelativePath string
	HandlerFunc  http.HandlerFunc
}

func mustRegisterHttpGetRoute(source, relativePath string, handleFunc http.HandlerFunc) {
	err := registerHttpRoute(source, http.MethodGet, relativePath, handleFunc)
	if err != nil {
		panic(err)
	}
}

func mustRegisterHttpPostRoute(source, relativePath string, handleFunc http.HandlerFunc) {
	err := registerHttpRoute(source, http.MethodPost, relativePath, handleFunc)
	if err != nil {
		panic(err)
	}
}

// registerHttpRoute 端口需要收敛 所以 server 的控制权转移至上层控制器 调用方只需要注册路由
func registerHttpRoute(source, httpMethod, relativePath string, handleFunc http.HandlerFunc) error {
	ri := define.RouteInfo{
		Source:     source,
		HttpMethod: httpMethod,
		Path:       relativePath,
	}
	if _, ok := serviceMgr.httpRoutes[ri.Key()]; ok {
		return fmt.Errorf("duplicated http route '%v'", ri)
	}

	serviceMgr.httpRoutes[ri.Key()] = ri
	serviceMgr.httpRouter.HandleFunc(relativePath, handleFunc).Methods(httpMethod)
	return nil
}

// RegisterHttpRoute 注册 Http 路由 失败直接 panic
func RegisterHttpRoute(source string, routes []RouteWithFunc) {
	for i := 0; i < len(routes); i++ {
		r := routes[i]
		if err := registerHttpRoute(source, r.Method, r.RelativePath, r.HandlerFunc); err != nil {
			panic(err)
		}
	}
}

// RegisterGrpcRoute 注册 Grpc 路由
func RegisterGrpcRoute(register func(s *grpc.Server)) {
	serviceMgr.grpcServices = append(serviceMgr.grpcServices, register)
}
