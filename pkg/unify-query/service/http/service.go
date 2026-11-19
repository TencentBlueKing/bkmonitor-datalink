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
	"errors"
	gohttp "net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/endpoint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/middleware"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/trace"
)

// Service
type Service struct {
	wg         sync.WaitGroup
	ctx        context.Context
	cancelFunc context.CancelFunc

	// 全局唯一的http服务
	server *gohttp.Server
	g      *gin.Engine
}

// Type
func (s *Service) Type() string {
	return "http"
}

// Start
func (s *Service) Start(ctx context.Context) {
	s.Reload(ctx)
}

// Reload
func (s *Service) Reload(ctx context.Context) {
	var err error

	// 先关闭当前的服务
	if s.server != nil {
		tempCtx, cancelFunc := context.WithTimeout(ctx, WriteTimeout)
		defer cancelFunc()
		if err = s.server.Shutdown(tempCtx); err != nil {
			log.Errorf(ctx, "failed to shutdown http server for->[%s]", err)
		}
	}

	if s.cancelFunc != nil {
		s.cancelFunc()
	}

	log.Debugf(ctx, "waiting for http service close")
	s.Wait()

	gin.SetMode(gin.ReleaseMode)
	var public, private *gin.RouterGroup
	s.g, public, private = newEngine()

	// 注册默认路由
	publicRegisterHandler := endpoint.NewRegisterHandler(ctx, public)
	registerDefaultHandlers(publicRegisterHandler)
	api.RegisterRelation(publicRegisterHandler)
	registerProxyHandler(publicRegisterHandler)

	privateRegisterHandler := endpoint.NewRegisterHandler(ctx, private)
	registerOtherHandlers(privateRegisterHandler)
	prebuildRequestOutcomes(s.g)

	// 构造新的http服务
	s.server = &gohttp.Server{
		Addr:         strings.Join([]string{IPAddress, strconv.Itoa(Port)}, ":"),
		ReadTimeout:  ReadTimeout,
		WriteTimeout: WriteTimeout,
		Handler:      s.g,
	}

	s.wg.Add(1)
	go func(server *gohttp.Server) {
		defer s.wg.Done()
		// 两个 goroutine 各用自己的 err，不共写 Reload 的变量。
		if err := server.ListenAndServe(); err != nil && !errors.Is(gohttp.ErrServerClosed, err) {
			log.Panicf(ctx, "failed to start server for->[%s]", err)
			return
		}
	}(s.server)
	// 更新上下文控制方法
	s.ctx, s.cancelFunc = context.WithCancel(ctx)
	log.Debugf(ctx, "http service context update success.")
	// 起一个goroutine去跟踪ctx，ctx关闭时server也关闭
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		<-s.ctx.Done()
		if err := s.server.Close(); err != nil {
			log.Errorf(ctx, "failed to close http server for->[%s]", err)
		}
	}()
}

// newEngine 建出带生产中间件链的 engine，路由由调用方注册，注册完后调用 prebuildRequestOutcomes。
// 请求结果计数注册在 engine 上、位于所有中间件之外，未匹配路由的请求也经过它；public 组再加恢复、
// trace、元数据和 JWT（中间件必须在注册路由之前加，否则不生效），private 组只有结果计数。
func newEngine() (engine *gin.Engine, public, private *gin.RouterGroup) {
	engine = gin.New()
	engine.Use(requestOutcomeMiddleware())
	public = engine.Group("/")
	public.Use(
		requestRecovery(),
		otelgin.Middleware(trace.ServiceName),
		middleware.MetaData(&middleware.Params{
			SlowQueryThreshold: SlowQueryThreshold,
		}),
		middleware.JwtAuthMiddleware(JwtEnabled, JwtPublicKey, JwtBkAppCodeSpaces),
	)
	private = engine.Group("/")
	return engine, public, private
}

// Wait
func (s *Service) Wait() {
	s.wg.Wait()
}

// Close
func (s *Service) Close() {
	s.cancelFunc()
}
