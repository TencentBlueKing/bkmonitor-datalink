// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package processingerror 定义消息级错误分类，普通错误不重试；取消仍交给所有权协议处理。
package processingerror

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
)

// MaxAttempts 包含第一次调用；重试不能把一条消息升级为来源故障。
const MaxAttempts = 3

// ErrPanic 表示单条处理异常，错误文本不包含 panic 的业务载荷。
var ErrPanic = errors.New("message handler panic")

// CapturePanic 在 recover 边界保留调用位置，不记录可能包含凭据或正文的 panic value。
// 仅异常路径采集有限栈帧；Unwrap 保留既有 panic 分类和重试语义。
func CapturePanic() error {
	pcs := make([]uintptr, 16)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var stack strings.Builder
	for {
		frame, more := frames.Next()
		fmt.Fprintf(&stack, "\n%s %s:%d", frame.Function, frame.File, frame.Line)
		if !more {
			break
		}
	}
	return fmt.Errorf("%w:%s", ErrPanic, stack.String())
}

// HTTPStatus 保留可判定的 HTTP 状态，不保存地址、响应体或凭据。
type HTTPStatus int

func (s HTTPStatus) Error() string { return fmt.Sprintf("upstream HTTP %d", s) }

// RetryableError 是适配器在已确认可安全重放时使用的显式标记；当前消息链路不预置白名单。
type RetryableError struct{ Err error }

func (e RetryableError) Error() string { return e.Err.Error() }

func (e RetryableError) Unwrap() error { return e.Err }

func (e RetryableError) CanRetry() bool { return true }

// Retryable 不将 JSON、逻辑错误或未经分类的 error 当成可重试错误。
func Retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, ErrPanic) {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !Retryable(cause) {
				return false
			}
		}
		return true
	}
	if classified, ok := err.(interface{ CanRetry() bool }); ok {
		return classified.CanRetry()
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return Retryable(wrapped.Unwrap())
	}
	return false
}

// Guard 捕获单条调用的 panic，不暴露 panic value，也不改变正常错误分类。
func Guard[T any](run func() (T, error)) (result T, err error) {
	defer func() {
		if recover() != nil {
			err = CapturePanic()
		}
	}()
	return run()
}
