// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

func newDispatchHTTPServer(address string, handler http.Handler, receiveTimeout time.Duration) *http.Server {
	// 接收处理包含插件计算和发布，写期限必须留出处理结束后的响应时间。
	// 其他管理接口仍由 API middleware 限定自身处理预算。
	return &http.Server{
		Addr: address, Handler: handler,
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: max(25*time.Second, receiveTimeout+5*time.Second),
		IdleTimeout:  60 * time.Second,
	}
}

func serveDispatchHTTP(ctx context.Context, server *http.Server, listener net.Listener) error {
	// 使用 taskgroup 的取消上下文：调度中心失去领导权时，正在执行的接收请求
	// 必须同时取消，不能仅停止 accept 后继续执行旧请求。已发送的 I/O 仍可能生效。
	server.BaseContext = func(net.Listener) context.Context { return ctx }
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if err := server.Shutdown(shutdown); err != nil {
			// 排空失败后关闭连接，避免任务退出后仍留下网络资源。
			return errors.Join(err, server.Close())
		}
		return nil
	}
}
