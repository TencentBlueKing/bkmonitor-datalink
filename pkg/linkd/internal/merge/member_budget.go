// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package merge

import (
	"context"
	"errors"
	"time"
)

// memberWorkContext 给现有关系/裁决进度 CAS 留一秒，不延长调用方窗口租约。
// 无截止时间时沿用调用方；预算只够收尾时不再启动成员操作。
func memberWorkContext(ctx context.Context) (context.Context, context.CancelFunc, bool) {
	deadline, bounded := ctx.Deadline()
	if !bounded {
		return ctx, func() {}, true
	}
	deadline = deadline.Add(-time.Second)
	if !time.Now().Before(deadline) {
		return ctx, func() {}, false
	}
	work, cancel := context.WithDeadline(ctx, deadline)
	return work, cancel, true
}

// memberWorkResult 在操作返回时区分主动让出和真实失败，避免稍后预算到期吞掉较早的依赖超时。
// 未完成项即使是主动让出也不能计入成功前缀；父取消和混合的租约释放失败始终保留。
func memberWorkResult(parent, work context.Context, err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if parent.Err() == nil && errors.Is(work.Err(), context.DeadlineExceeded) && memberDeadlineOnly(err) {
		return false, nil
	}
	return false, err
}

func memberDeadlineOnly(err error) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var cause error
		for _, next := range joined.Unwrap() {
			if next == nil {
				continue
			}
			// 两个deadline也可能分别来自操作和归还租约，不能将释放不确定当作主动让出。
			if cause != nil {
				return false
			}
			cause = next
		}
		return cause != nil && memberDeadlineOnly(cause)
	}
	if cause := errors.Unwrap(err); cause != nil {
		return memberDeadlineOnly(cause)
	}
	return errors.Is(err, context.DeadlineExceeded)
}
