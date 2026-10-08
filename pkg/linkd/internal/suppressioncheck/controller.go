// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package suppressioncheck

import (
	"context"
	"errors"
	"time"

	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/policy"
)

// Checker 必须在正式 owner lease 内检查，返回安全诊断和可观测的底层执行错误。
type Checker interface {
	Check(context.Context, Command) (Check, error)
}

// Controller 限制显式请求容量与执行，持久最终结果可在窗口消失后读取。
type Controller struct {
	journal *Journal
	locker  scheduler.Locker
	checker Checker
	slots   chan struct{}
}

// NewController 不启动工作循环，调用方注入按部署隔离的控制请求锁。
func NewController(j *Journal, l scheduler.Locker, c Checker) (*Controller, error) {
	if j == nil || l == nil || c == nil {
		return nil, policy.ErrInvalid
	}
	return &Controller{j, l, c, make(chan struct{}, 4)}, nil
}

func withLease(ctx context.Context, l scheduler.Locker, key string, run func() error) (err error) {
	lease, err := l.Acquire(ctx, key)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		err = errors.Join(err, l.Release(cleanup, lease))
	}()
	return run()
}

// Request 在租户准入锁内保存完整命令；接受不代表已检查或窗口已清理。
func (c *Controller) Request(ctx context.Context, command Command) (Request, error) {
	if command.Validate() != nil {
		return Request{}, policy.ErrInvalid
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return Request{}, policy.ErrPreviewCapacity
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var result Stored
	err := withLease(ctx, c.locker, digest("suppression-request-admission", command.TenantID), func() error { var err error; result, err = c.journal.Enqueue(ctx, command, time.Now()); return err })
	return result.Request, err
}

// Execute 先取得同请求锁并重读；已完成命令不会再次操作窗口。锁忙和取消保留 pending。
func (c *Controller) Execute(ctx context.Context, request Request) error {
	if request.Validate() != nil {
		return policy.ErrInvalid
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return policy.ErrPreviewCapacity
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return withLease(ctx, c.locker, digest("suppression-request-execute", request.Command.TenantID, request.ID), func() error {
		q := request.Command
		current, err := c.journal.Get(ctx, q.TenantID, q.Kind, q.WindowID, request.ID)
		if err != nil {
			return err
		}
		if current.Request.State != "pending" {
			return nil
		}
		current, err = c.journal.Start(ctx, current, time.Now())
		if err != nil {
			return err
		}
		result, checkErr := c.checker.Check(ctx, current.Request.Command)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if onlyBusy(checkErr) {
			return checkErr
		}
		if result.Validate(current.Request.Command) != nil {
			return errors.Join(checkErr, policy.ErrInvalid)
		}
		_, finishErr := c.journal.Finish(ctx, current, result)
		return errors.Join(checkErr, finishErr)
	})
}

func onlyBusy(err error) bool {
	if err == nil {
		return false
	}
	if x, ok := err.(interface{ Unwrap() []error }); ok {
		children := x.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, e := range children {
			if !onlyBusy(e) {
				return false
			}
		}
		return true
	}
	if x, ok := err.(interface{ Unwrap() error }); ok {
		return onlyBusy(x.Unwrap())
	}
	return errors.Is(err, scheduler.ErrLockBusy)
}

// CanDefer 仅当整条错误链都是正常锁忙/容量限制时允许延后，不能掩盖租约释放等错误。
func CanDefer(err error) bool {
	if err == nil {
		return false
	}
	if x, ok := err.(interface{ Unwrap() []error }); ok {
		children := x.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, e := range children {
			if !CanDefer(e) {
				return false
			}
		}
		return true
	}
	if x, ok := err.(interface{ Unwrap() error }); ok {
		return CanDefer(x.Unwrap())
	}
	return errors.Is(err, policy.ErrPreviewCapacity) || errors.Is(err, scheduler.ErrLockBusy)
}
