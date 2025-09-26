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
	gohttp "net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/api"
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
		codedWarn := errno.ErrWarningServiceDegraded().
			WithComponent("HTTP服务器").
			WithOperation("重启前关闭").
			WithContext("超时时间", WriteTimeout.String()).
			WithSolution("等待现有请求完成")
		log.WarnWithCodef(ctx, codedWarn)
		tempCtx, cancelFunc := context.WithTimeout(ctx, WriteTimeout)
		defer cancelFunc()
		if err = s.server.Shutdown(tempCtx); err != nil {
			codedErr := errno.ErrBusinessLogicError().
				WithComponent("HTTP服务器").
				WithOperation("服务器关闭").
				WithError(err).
				WithSolution("检查请求处理和网络连接")
			log.ErrorWithCodef(ctx, codedErr)
		}
		codedWarn = errno.ErrWarningServiceDegraded().
			WithComponent("HTTP服务器").
			WithOperation("服务器关闭完成").
			WithSolution("服务器正常重启")
		log.WarnWithCodef(ctx, codedWarn)
	}

	if s.cancelFunc != nil {
		s.cancelFunc()
	}

	log.Debugf(ctx, "waiting for http service close")
	s.Wait()

	gin.SetMode(gin.ReleaseMode)
	s.g = gin.New()

	public := s.g.Group("/")
	// 注册默认路由
	// 注册中间件，注意中间件必须要在其他服务之前注册，否则中间件不生效
	public.Use(
		gin.Recovery(),
		otelgin.Middleware(trace.ServiceName),
		middleware.MetaData(&middleware.Params{
			SlowQueryThreshold: SlowQueryThreshold,
		}),
		middleware.JwtAuthMiddleware(JwtPublicKey, JwtBkAppCodeSpaces),
	)
	registerDefaultHandlers(ctx, public)
	api.RegisterRelation(ctx, public)
	registerProxyHandler(ctx, public)

	private := s.g.Group("/")
	registerOtherHandlers(ctx, private)

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
		if err = server.ListenAndServe(); err != nil && err != gohttp.ErrServerClosed {
			log.Panicf(ctx, "failed to start server for->[%s]", err)
			return
		}
		codedWarn := errno.ErrWarningServiceDegraded().
			WithComponent("HTTP服务器").
			WithOperation("服务器停止").
			WithContext("说明", "服务器正常停止监听")
		log.WarnWithCodef(ctx, codedWarn)
	}(s.server)
	// 更新上下文控制方法
	s.ctx, s.cancelFunc = context.WithCancel(ctx)
	log.Debugf(ctx, "http service context update success.")
	// 起一个goroutine去跟踪ctx，ctx关闭时server也关闭
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		<-s.ctx.Done()
		err = s.server.Close()
		if err != nil {
			codedErr := errno.ErrBusinessLogicError().
				WithComponent("HTTP服务器").
				WithOperation("服务器关闭").
				WithError(err).
				WithSolution("检查服务器状态和连接")
			log.ErrorWithCodef(ctx, codedErr)
		}
	}()
	codedInfo := errno.ErrInfoServiceStart().
		WithComponent("HTTP").
		WithOperation("服务启动").
		WithContext("状态", "成功")
	log.InfoWithCodef(ctx, codedInfo)
}

// Wait
func (s *Service) Wait() {
	s.wg.Wait()
}

// Close
func (s *Service) Close() {
	s.cancelFunc()
	codedInfo := errno.ErrInfoServiceShutdown().
		WithComponent("HTTP").
		WithOperation("服务关闭")
	log.InfoWithCodef(s.ctx, codedInfo)
}
