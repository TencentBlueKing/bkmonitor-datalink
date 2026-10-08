// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package merge

import (
	"context"
	"errors"
	"time"

	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/policy"
	"linkd/internal/store"
)

// RetryStepper 是正式执行器已有的有界步骤，不提供重新裁决或修改冻结内容的端口。
type RetryStepper interface {
	StepDecision(context.Context, string, string, time.Time) error
	CheckRelation(context.Context, string, string, time.Time) error
}

// RetryWindowRunner 必须与自动裁决/关系任务共用窗口租约和并发预算。
// fn 执行和租约释放错误都须返回；租约至少 30 秒，fn 的总时间最多 10 秒。
type RetryWindowRunner func(context.Context, string, string, func(context.Context) error) error

// RetryController 协调持久命令与正式合并运行器；请求 API 不直接执行合并。
type RetryController struct {
	journal *Journal
	locker  scheduler.Locker
	slots   chan struct{}
}

// NewRetryController 不启动后台任务；locker 使用部署隔离、至少 30 秒的请求租约。
func NewRetryController(j *Journal, locker scheduler.Locker) (*RetryController, error) {
	if j == nil || locker == nil {
		return nil, policy.ErrInvalid
	}
	if _, ok := j.docs.(RetryDocuments); !ok {
		return nil, policy.ErrUnavailable
	}
	return &RetryController{journal: j, locker: locker, slots: make(chan struct{}, 4)}, nil
}

func (c *RetryController) withLease(ctx context.Context, key string, fn func(context.Context) error) (err error) {
	lease, err := c.locker.Acquire(ctx, key)
	if err != nil {
		return err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		err = errors.Join(err, c.locker.Release(release, lease))
	}()
	return fn(ctx)
}

// Request 在租户准入锁中保存完整命令，重复请求不重新读取或更换原观察版本。
func (c *RetryController) Request(ctx context.Context, command RetryCommand) (RetryRequest, error) {
	if command.Validate() != nil {
		return RetryRequest{}, policy.ErrInvalid
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return RetryRequest{}, policy.ErrPreviewCapacity
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var result RetryRequest
	err := c.withLease(call, hash("merge-request-admission", command.TenantID), func(ctx context.Context) error {
		saved, e := c.journal.EnqueueRetry(ctx, command, time.Now())
		result = saved.Request
		return e
	})
	return result, err
}

// Execute 先取得同请求租约，再交给共享窗口运行器；自动任务已推进时只记录 superseded。
// 开始记录先于副作用；取消或结果持久化失败保留 pending，同一身份接续不回退原业务进度。
func (c *RetryController) Execute(ctx context.Context, request RetryRequest, runWindow RetryWindowRunner, stepper RetryStepper) error {
	if request.Validate() != nil || runWindow == nil || stepper == nil {
		return policy.ErrInvalid
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return policy.ErrPreviewCapacity
	}
	call, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return c.withLease(call, hash("merge-request-execute", request.Command.TenantID, request.ID), func(ctx context.Context) error {
		cmd := request.Command
		current, e := c.journal.GetRetry(ctx, cmd.TenantID, cmd.Kind, cmd.TargetID, request.ID)
		if e != nil {
			return e
		}
		if current.Request.Command != request.Command || current.Request.WindowID != request.WindowID {
			return policy.ErrConflict
		}
		if current.Request.State != "pending" {
			return nil
		}
		current, e = c.journal.StartRetry(ctx, current, time.Now())
		if e != nil {
			return e
		}
		r := current.Request
		result := RetryResult{}
		entered := false
		runErr := runWindow(ctx, r.Command.TenantID, r.WindowID, func(ctx context.Context) error {
			entered = true
			before, e := c.journal.ReadControlPoint(ctx, r.Command.TenantID, r.Command.Kind, r.Command.TargetID)
			if e != nil {
				return e
			}
			if before.WindowID != r.WindowID {
				return policy.ErrAccess
			}
			result.Before = &before
			if before.Token != r.Command.ExpectedToken {
				result.Outcome, result.Reason = "superseded", "target_changed"
				return nil
			}
			if before.Complete {
				after := before
				result.After = &after
				result.Outcome, result.Reason = "unchanged", "already_complete"
				return nil
			}
			result.StepAttempted = true
			at := time.Now().UTC()
			// 时钟短暂回拨不得把用户的接续请求变成一次倒退进度写入。
			if at.Before(before.UpdatedAt) {
				at = before.UpdatedAt
			}
			if r.Command.Kind == "decisions" {
				e = stepper.StepDecision(ctx, r.Command.TenantID, r.Command.TargetID, at)
			} else {
				e = stepper.CheckRelation(ctx, r.Command.TenantID, r.Command.TargetID, at)
			}
			if ctx.Err() != nil {
				return errors.Join(e, ctx.Err())
			}
			after, readErr := c.journal.ReadControlPoint(ctx, r.Command.TenantID, r.Command.Kind, r.Command.TargetID)
			if readErr == nil {
				if after.WindowID != r.WindowID {
					return errors.Join(e, policy.ErrAccess)
				}
				result.After = &after
			}
			if e != nil || readErr != nil {
				return errors.Join(e, readErr)
			}
			result.Outcome, result.Reason = "unchanged", "no_progress"
			if before.Token != after.Token {
				result.Outcome, result.Reason = "advanced", "progressed"
			}
			return nil
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if RetryCanDefer(runErr) {
			return runErr
		}
		if runErr != nil {
			reason := retryErrorCode(runErr)
			if !entered || result.Outcome != "" {
				reason = "execution_unavailable"
			}
			result.Outcome, result.Reason = "failed", reason
		}
		result.CheckedAt = time.Now().UTC()
		if result.Validate(r.Command, r.WindowID) != nil {
			return errors.Join(runErr, policy.ErrInvalid)
		}
		_, finishErr := c.journal.FinishRetry(ctx, current, result)
		return errors.Join(runErr, finishErr)
	})
}

func retryErrorCode(err error) string {
	switch {
	case errors.Is(err, policy.ErrAccess):
		return "scope_mismatch"
	case errors.Is(err, policy.ErrInvalid), errors.Is(err, store.ErrInvalidArgument), errors.Is(err, store.ErrInvalidTransition):
		return "invalid_state"
	case errors.Is(err, context.DeadlineExceeded):
		return "step_timeout"
	case errors.Is(err, policy.ErrNotFound), errors.Is(err, store.ErrNotFound):
		return "target_missing"
	default:
		return "dependency_failed"
	}
}

// RetryCanDefer 只允许纯锁忙/容量错误延后；混合释放、读取或取消异常不能被吞掉。
func RetryCanDefer(err error) bool {
	if err == nil {
		return false
	}
	if x, ok := err.(interface{ Unwrap() []error }); ok {
		es := x.Unwrap()
		if len(es) == 0 {
			return false
		}
		for _, e := range es {
			if !RetryCanDefer(e) {
				return false
			}
		}
		return true
	}
	if x, ok := err.(interface{ Unwrap() error }); ok {
		return RetryCanDefer(x.Unwrap())
	}
	return errors.Is(err, scheduler.ErrLockBusy) || errors.Is(err, policy.ErrPreviewCapacity)
}
