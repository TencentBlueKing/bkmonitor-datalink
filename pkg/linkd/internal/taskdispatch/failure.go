// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskdispatch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"

	mysql "github.com/go-sql-driver/mysql"
	redis "github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kerr"
	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/processingerror"
	"linkd/internal/store"
)

type taskError struct {
	stage string
	err   error
}

type repairRequired struct{ err error }

func (e *repairRequired) Error() string { return "task requires repair: " + e.err.Error() }

func (e *repairRequired) Unwrap() error { return e.err }

// RequireTaskRepair 保留失败并禁止调度器自动重启；修复后须显式恢复原来源版本。
// 该标记只用于确定性失败，暂时性依赖错误继续使用既有退避。
func RequireTaskRepair(err error) error {
	if err == nil {
		return nil
	}
	return &repairRequired{err: err}
}

func requiresTaskRepair(err error) bool {
	var failure *repairRequired
	return errors.As(err, &failure)
}

func (e *taskError) Error() string { return e.stage + ": " + e.err.Error() }

func (e *taskError) Unwrap() error { return e.err }

// WithTaskStage 标记任务失败阶段并保留错误链；stage 必须是代码中固定的操作名，不能包含输入值。
func WithTaskStage(stage string, err error) error {
	if err == nil {
		return nil
	}
	return &taskError{stage: stage, err: err}
}

func expectedTaskCancellation(ctx context.Context, err error) bool {
	return ctx.Err() != nil && cancellationOnly(err)
}

// 取消过程中加入的清理错误仍需诊断，不能因 errors.Join 同时含有
// context.Canceled 就把整个失败当作正常退出。
func cancellationOnly(err error) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !cancellationOnly(child) {
				return false
			}
		}
		return true
	}
	if child := errors.Unwrap(err); child != nil {
		return cancellationOnly(child)
	}
	return errors.Is(err, context.Canceled)
}

// logTaskFailure 记录来源、代次、失败阶段、错误链及依赖错误码。
// 统一日志 Handler 脱敏常见凭据，调用方不得在错误中包含业务正文。
func (a *Agent) logTaskFailure(ctx context.Context, task Task, err error) {
	if err == nil || expectedTaskCancellation(ctx, err) {
		return
	}
	stage := "task_run"
	var failure *taskError
	if errors.As(err, &failure) {
		stage = failure.stage
	}
	cause := err
	for range 16 {
		next := errors.Unwrap(cause)
		if next == nil {
			break
		}
		cause = next
	}
	attrs := []any{"module", "taskdispatch", "operation", stage, "error", err, "event_source_id", task.Source, "role", task.Role, "task_id", task.ID, "assignment_epoch", task.Epoch, "event_source_version", task.Version, "stage", stage, "error_type", fmt.Sprintf("%T", cause)}
	reason := "task_error"
	switch {
	case errors.Is(err, consume.ErrDeferredCapacity):
		reason = "deferred_capacity_exhausted"
	case errors.Is(err, consume.ErrStopIncomplete):
		reason = "stop_incomplete"
	case errors.Is(err, context.DeadlineExceeded):
		reason = "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		reason = "canceled"
	case errors.Is(err, store.ErrVersionConflict):
		reason = "store_version_conflict"
	case errors.Is(err, store.ErrIdentityConflict):
		reason = "store_identity_conflict"
	case errors.Is(err, store.ErrInvalidEventProcessing):
		reason = "invalid_event_processing"
	case errors.Is(err, store.ErrInvalidArgument):
		reason = "invalid_argument"
	case errors.Is(err, store.ErrNotFound):
		reason = "store_not_found"
	case errors.Is(err, consume.ErrReceiveLimitExceeded):
		reason = "receive_limit_exceeded"
	case errors.Is(err, consume.ErrInvalidDelivery):
		reason = "invalid_delivery"
	}
	var sqlErr *mysql.MySQLError
	if errors.As(err, &sqlErr) {
		reason = "mysql_error"
		attrs = append(attrs, "mysql_error_code", sqlErr.Number)
	}
	var kafkaErr *kerr.Error
	if errors.As(err, &kafkaErr) {
		reason = "kafka_error"
		attrs = append(attrs, "kafka_error_code", kafkaErr.Code)
	}
	var redisErr redis.Error
	if errors.As(err, &redisErr) {
		reason = "redis_error"
		// 只保留 Redis 协议的固定错误分类，不输出错误携带的命令和参数。
		code, _, _ := strings.Cut(redisErr.Error(), " ")
		switch code {
		case "WRONGPASS", "NOAUTH", "NOPERM", "BUSYGROUP", "NOGROUP", "READONLY", "OOM", "LOADING", "CLUSTERDOWN", "MASTERDOWN":
			attrs = append(attrs, "redis_error_code", code)
		}
	}
	var networkErr *net.OpError
	if errors.As(err, &networkErr) {
		reason = "network_error"
		if networkErr.Timeout() {
			reason = "network_timeout"
		}
		switch networkErr.Op {
		case "dial", "read", "write", "accept":
			attrs = append(attrs, "network_operation", networkErr.Op)
		}
	}
	var systemErr syscall.Errno
	if errors.As(err, &systemErr) {
		attrs = append(attrs, "system_error_code", int(systemErr))
	}
	if requiresTaskRepair(err) {
		reason = "task_requires_repair"
	}
	attrs = append(attrs, "reason_code", reason)
	attrs = append(attrs, "error_types", taskErrorTypes(err))
	a.Logger.ErrorContext(context.WithoutCancel(ctx), "source task failed", attrs...)
}

// taskErrorTypes 有界遍历单链及 Join 分支；只输出类型，不携带任意错误正文。
func taskErrorTypes(err error) []string {
	result := make([]string, 0, 8)
	var visit func(error, int)
	visit = func(e error, depth int) {
		if e == nil || depth >= 16 || len(result) >= 8 {
			return
		}
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			for _, child := range joined.Unwrap() {
				visit(child, depth+1)
			}
			return
		}
		if child := errors.Unwrap(e); child != nil {
			visit(child, depth+1)
			return
		}
		result = append(result, fmt.Sprintf("%T", e))
	}
	visit(err, 0)
	return result
}

func (a *Agent) runTask(ctx context.Context, task Task, source config.EventSource) error {
	_, err := processingerror.Guard(func() (struct{}, error) { return struct{}{}, a.RunTask(ctx, task, source) })
	if err == nil && ctx.Err() == nil {
		err = WithTaskStage("task_return", fmt.Errorf("task exited unexpectedly"))
	}
	a.logTaskFailure(ctx, task, err)
	return err
}
