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
	"sort"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
)

// requestOutcomeMiddleware 注册在 engine 上、位于所有中间件之外，每个请求在 handler 返回后
// 按 requestOutcome 的顺序判一次结果，计入 unify_query_request_outcomes_total。
func requestOutcomeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 入站请求的 context 只在调用方断开（或 HTTP/2 取消）时取消；下游中间件替换的
		// c.Request 可能带着自己的 cancel，不用它判调用方断开。
		inbound := c.Request.Context()
		ctx, record := metadata.WithRequestOutcome(inbound)
		c.Request = c.Request.WithContext(ctx)
		defer func() {
			if recovered := recover(); recovered != nil {
				// 不在恢复中间件之后的路由，panic 一路传到这里：计数后照旧交给 net/http。
				metric.PanicInc(ctx, metric.PanicSiteHTTPHandler)
				metric.RequestOutcomeInc(ctx, requestHandlerLabel(c), metric.OutcomeInternal)
				panic(recovered)
			}
		}()
		c.Next()
		metric.RequestOutcomeInc(ctx, requestHandlerLabel(c), requestOutcome(inbound, c, record.Snapshot()))
	}
}

// requestRecovery 替代 gin.Recovery：恢复 panic、计数，并照旧答复 500。
func requestRecovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, _ any) {
		ctx := c.Request.Context()
		metric.PanicInc(ctx, metric.PanicSiteHTTPHandler)
		metadata.RecordRequestPanic(ctx)
		c.AbortWithStatus(http.StatusInternalServerError)
	})
}

func requestHandlerLabel(c *gin.Context) string {
	if handler := c.FullPath(); handler != "" {
		return handler
	}
	return metric.HandlerUnmatched
}

// requestOutcome 按顺序判，先命中的算：
//  1. canceled：调用方断开，即入站请求的 context 已取消；或者响应写回调用方失败。
//     不对错误链用 errors.Is：多路查询把各路错误合在一起，其中一路被本服务内部取消，不等于调用方断开。
//  2. unauthorized：本服务的 JWT 鉴权拒绝。
//  3. internal：恢复的 panic，没能编码出响应，以及不是由失败分类得出的 5xx。
//  4. timeout：失败响应所依据的错误经 FailureStatus 得 504；没有记下错误时按答复的状态码。
//  5. upstream_error：同上得 502。
//  6. client_error：同上得其余 4xx。
//  7. partial：有路由失败，返回的是其余结果。
//  8. success。
func requestOutcome(inbound context.Context, c *gin.Context, record metadata.RequestOutcomeSnapshot) string {
	// 写响应失败：代理记下的写出错误，或者 gin 的 Render 放进 c.Errors 的错误（本服务不另用 c.Error）。
	// 已经开始写出的是写回调用方失败；什么都没写出的是本服务没能编码出响应。
	responseFailed := proxy.ResponseWriteError(c) != nil || len(c.Errors) > 0
	if inbound.Err() == context.Canceled || record.ResponseWriteFailure || (responseFailed && c.Writer.Written()) {
		return metric.OutcomeCanceled
	}
	if record.Unauthorized {
		return metric.OutcomeUnauthorized
	}
	if record.Panicked || responseFailed {
		return metric.OutcomeInternal
	}
	status := c.Writer.Status()
	if record.Failure != nil {
		status = metadata.FailureStatus(record.Failure)
	}
	switch {
	case status == http.StatusGatewayTimeout:
		return metric.OutcomeTimeout
	case status == http.StatusBadGateway:
		return metric.OutcomeUpstreamError
	case status >= http.StatusInternalServerError:
		return metric.OutcomeInternal
	case status >= http.StatusBadRequest:
		return metric.OutcomeClientError
	}
	if record.Partial {
		return metric.OutcomePartial
	}
	return metric.OutcomeSuccess
}

// prebuildRequestOutcomes 在路由注册完成后，按每个路由模板建出全部结果格。
func prebuildRequestOutcomes(engine *gin.Engine) {
	seen := make(map[string]struct{})
	handlers := make([]string, 0)
	for _, route := range engine.Routes() {
		if _, ok := seen[route.Path]; ok {
			continue
		}
		seen[route.Path] = struct{}{}
		handlers = append(handlers, route.Path)
	}
	sort.Strings(handlers)
	metric.PrebuildRequestOutcomes(handlers)
}
