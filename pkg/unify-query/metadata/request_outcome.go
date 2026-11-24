// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"sync"
)

type requestOutcomeContextKey struct{}

// RequestOutcome 是一次 HTTP 请求在处理过程中留下的结果依据，由请求入口的中间件在 handler 返回后读取，
// 每个请求只判一次。它挂在请求的 context 上：从请求 context 派生出的所有 context（包括各输出和
// selector 的状态作用域）写的都是同一份。
type RequestOutcome struct {
	mu                   sync.Mutex
	failure              error
	unauthorized         bool
	panicked             bool
	partial              bool
	responseWriteFailure bool
}

// WithRequestOutcome 为一次请求建一份结果依据。
func WithRequestOutcome(ctx context.Context) (context.Context, *RequestOutcome) {
	outcome := &RequestOutcome{}
	return context.WithValue(ctx, requestOutcomeContextKey{}, outcome), outcome
}

func requestOutcomeOf(ctx context.Context) *RequestOutcome {
	if ctx == nil {
		return nil
	}
	outcome, _ := ctx.Value(requestOutcomeContextKey{}).(*RequestOutcome)
	return outcome
}

func (o *RequestOutcome) update(apply func(o *RequestOutcome)) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	apply(o)
}

// RecordRequestFailure 记下失败响应所依据的错误，结果按 FailureStatus 分类；只保留第一个。
func RecordRequestFailure(ctx context.Context, err error) {
	if err == nil {
		return
	}
	requestOutcomeOf(ctx).update(func(o *RequestOutcome) {
		if o.failure == nil {
			o.failure = err
		}
	})
}

// RecordRequestUnauthorized 记下本服务的鉴权拒绝了这次请求。
func RecordRequestUnauthorized(ctx context.Context) {
	requestOutcomeOf(ctx).update(func(o *RequestOutcome) { o.unauthorized = true })
}

// RecordRequestPanic 记下这次请求的处理链上恢复了一次 panic。
func RecordRequestPanic(ctx context.Context) {
	requestOutcomeOf(ctx).update(func(o *RequestOutcome) { o.panicked = true })
}

// RecordResponseWriteFailure 记下响应写回调用方失败：调用方连接已断，结果按调用方断开计。
func RecordResponseWriteFailure(ctx context.Context) {
	requestOutcomeOf(ctx).update(func(o *RequestOutcome) { o.responseWriteFailure = true })
}

// recordRequestPartial 记下有路由失败、返回的是其余结果；由 SetStatus 在写部分结果状态时调用。
func recordRequestPartial(ctx context.Context) {
	requestOutcomeOf(ctx).update(func(o *RequestOutcome) { o.partial = true })
}

// RequestOutcomeSnapshot 是某一时刻读出的结果依据。
type RequestOutcomeSnapshot struct {
	Failure              error
	Unauthorized         bool
	Panicked             bool
	Partial              bool
	ResponseWriteFailure bool
}

// Snapshot 读出当前的结果依据。
func (o *RequestOutcome) Snapshot() RequestOutcomeSnapshot {
	if o == nil {
		return RequestOutcomeSnapshot{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return RequestOutcomeSnapshot{
		Failure:              o.failure,
		Unauthorized:         o.unauthorized,
		Panicked:             o.panicked,
		Partial:              o.partial,
		ResponseWriteFailure: o.responseWriteFailure,
	}
}
