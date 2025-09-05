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
	"sort"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	"google.golang.org/grpc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

type Ready func(config ComponentConfig)

var componentsReady = map[string]Ready{}

func RegisterReadyFunc(source string, f Ready) {
	componentsReady[source] = f
}

type serviceManager struct {
	httpRoutes   map[string]define.RouteInfo
	httpRouter   *mux.Router
	grpcServices []func(s *grpc.Server)
	tarsServants map[string]*TarsServant
}

var serviceMgr = &serviceManager{
	httpRoutes:   map[string]define.RouteInfo{},
	httpRouter:   mux.NewRouter(),
	tarsServants: map[string]*TarsServant{},
}

// RecvHttpRouter 返回 Receiver mux.Router
func RecvHttpRouter() *mux.Router {
	return serviceMgr.httpRouter
}

// RecvHttpRoutes 返回 Receiver 注册的路由表
func RecvHttpRoutes() []define.RouteInfo {
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

// registerHttpRoute 端口需要收敛 所以 server 的控制权转移至上层控制器 调用方只需要注册路由
func registerHttpRoute(source, httpMethod, relativePath string, handleFunc http.HandlerFunc, mgr *serviceManager) error {
	ri := define.RouteInfo{
		Source:     source,
		HttpMethod: httpMethod,
		Path:       relativePath,
	}
	if _, ok := mgr.httpRoutes[ri.Key()]; ok {
		return errors.Errorf("duplicated http route '%v'", ri)
	}

	mgr.httpRoutes[ri.Key()] = ri
	mgr.httpRouter.HandleFunc(relativePath, handleFunc).Methods(httpMethod)
	return nil
}

// RegisterRecvHttpRoute 注册 Http 路由 失败直接 panic
func RegisterRecvHttpRoute(source string, routes []RouteWithFunc) {
	for i := 0; i < len(routes); i++ {
		r := routes[i]
		if err := registerHttpRoute(source, r.Method, r.RelativePath, r.HandlerFunc, serviceMgr); err != nil {
			panic(err)
		}
	}
}

// RegisterRecvGrpcRoute 注册 Grpc 路由
func RegisterRecvGrpcRoute(register func(s *grpc.Server)) {
	serviceMgr.grpcServices = append(serviceMgr.grpcServices, register)
}

// registerRecvTarsRoute 注册 Tars Servant
func registerRecvTarsRoute(o string, server string, impl any, dispatch TarsDispatch, mgr *serviceManager) error {
	s := NewTarsServant(o, server, impl, dispatch)
	if _, ok := mgr.tarsServants[s.Obj]; ok {
		return errors.Errorf("duplicated tars servant '%v'", s.Obj)
	}
	serviceMgr.tarsServants[s.Obj] = s
	return nil
}

// RegisterRecvTarsRoute 注册 Tars Servant
func RegisterRecvTarsRoute(o string, server string, impl any, dispatch TarsDispatch) {
	if err := registerRecvTarsRoute(o, server, impl, dispatch, serviceMgr); err != nil {
		panic(err)
	}
}
