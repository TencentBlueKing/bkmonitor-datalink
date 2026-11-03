// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package onemodel

import (
	"context"
	"errors"
	"net"
)

// DiagnosticError 为原错误附加可持久化的原因码，不改变 errors.Is/As 和查询结果。
// 原错误仅供运行日志使用；原因码不能包含查询值、响应正文或凭据。
type DiagnosticError struct {
	reason string
	cause  error
}

// Error 保留原错误描述，避免诊断分类替代底层错误。
func (e *DiagnosticError) Error() string { return e.cause.Error() }

// Unwrap 保留原错误链，尤其是禁止身份异常进入未命中兜底所依赖的 sentinel。
func (e *DiagnosticError) Unwrap() error { return e.cause }

// DiagnosticReason 返回不包含业务数据的稳定原因码。
func (e *DiagnosticError) DiagnosticReason() string { return e.reason }

func diagnosticError(reason string, cause error) error {
	return &DiagnosticError{reason: reason, cause: cause}
}

// ErrorReason 提取产生错误时记录的原因；未分类的旧 Reader 错误保留保守分类。
// 调用方仍须按 context 状态传播取消，不能把本函数当成重试或兜底决策。
func ErrorReason(err error) string {
	if err == nil {
		return ""
	}
	var classified *DiagnosticError
	if errors.As(err, &classified) {
		return classified.reason
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return "query_timeout"
	}
	if errors.Is(err, ErrInvalidDataSourceResponse) {
		return "response_invalid"
	}
	return "query_error"
}
