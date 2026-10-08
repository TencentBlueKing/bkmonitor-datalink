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
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"linkd/internal/controlplane/taskstate"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
	"linkd/internal/taskgroup"
	"linkd/internal/telemetry"
)

type shieldHintListener interface {
	ListenShieldHints(context.Context, func(redisstate.ShieldHint), func(string)) error
}

type shieldHintChecker interface {
	CheckHint(context.Context, string, string) error
}

type shieldHintCursor struct {
	hint  redisstate.ShieldHint
	after string
}

// 所有未结束主提示最多六十四个，含执行中项；重复合并但不重置游标，满页回队尾避免饿死其他主。
// 队列只加速，不承诺可靠排队；错误/退出/容量满均由独立持久化定时扫描补偿。
type shieldHintQueue struct {
	mu      sync.Mutex
	pending map[redisstate.ShieldHint]bool
	items   []shieldHintCursor
	wake    chan struct{}
}

func newShieldHintQueue() *shieldHintQueue {
	return &shieldHintQueue{pending: map[redisstate.ShieldHint]bool{}, wake: make(chan struct{}, 1)}
}

func (q *shieldHintQueue) add(h redisstate.ShieldHint) string {
	if h.Validate() != nil {
		return "invalid"
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending[h] {
		return "coalesced"
	}
	if len(q.pending) >= 64 {
		return "dropped"
	}
	q.pending[h] = true
	q.items = append(q.items, shieldHintCursor{hint: h})
	q.notify()
	return "queued"
}

func (q *shieldHintQueue) notify() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *shieldHintQueue) pop() (shieldHintCursor, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return shieldHintCursor{}, false
	}
	item := q.items[0]
	q.items = slices.Delete(q.items, 0, 1)
	return item, true
}

func (q *shieldHintQueue) finish(item shieldHintCursor, next string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if next != "" {
		item.after = next
		q.items = append(q.items, item)
		q.notify()
	} else {
		delete(q.pending, item.hint)
	}
}

func runShieldHints(ctx context.Context, listener shieldHintListener, reader store.ShieldDependencyReader, checker shieldHintChecker, logger *slog.Logger, registry *taskstate.Registry, metrics *telemetry.Runtime) error {
	queue := newShieldHintQueue()
	return taskgroup.Run(ctx, []taskgroup.Task{{Name: "subscription", Run: func(ctx context.Context) error {
		for ctx.Err() == nil {
			registry.Begin("shield-hints", "subscription", "事件提示订阅")
			started := time.Now()
			err := listener.ListenShieldHints(ctx, func(h redisstate.ShieldHint) { metrics.ObserveShieldHint(ctx, queue.add(h)) }, func(outcome string) {
				metrics.ObserveShieldHint(ctx, outcome)
				if outcome == "subscribed" {
					registry.Finish(ctx, "shield-hints", "subscription", "事件提示订阅", time.Since(started), 0, 0, "")
				}
			})
			if ctx.Err() != nil {
				return nil
			}
			registry.Finish(ctx, "shield-hints", "subscription", "事件提示订阅", time.Since(started), 0, 1, "hint_subscription_failed")
			metrics.ObserveShieldHint(ctx, "subscription_failed")
			if logger != nil {
				logger.WarnContext(ctx, "shield hint subscription unavailable", "reason_code", "hint_subscription_failed")
			}
			// 返回 nil 也不能把意外退出当成健康；重连有五秒退避，绝不拖停定时检查。
			_ = err
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
		return nil
	}}, {Name: "checks", Run: func(ctx context.Context) error {
		for ctx.Err() == nil {
			item, ok := queue.pop()
			if !ok {
				select {
				case <-ctx.Done():
					return nil
				case <-queue.wake:
					continue
				}
			}
			started := time.Now()
			registry.Begin("shield-hints", "", "")
			call, cancel := context.WithTimeout(ctx, 45*time.Second)
			next, n, failed, err := checkShieldHintPage(call, reader, checker, item)
			cancel()
			code, outcome := "", "page_succeeded"
			if err != nil {
				code = "hint_check_failed"
				outcome = "page_failed"
				next = ""
			}
			queue.finish(item, next)
			registry.Finish(ctx, "shield-hints", "", "", time.Since(started), n, failed, code)
			if ctx.Err() == nil {
				metrics.ObserveShieldHint(ctx, outcome)
				metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskShieldHints).RunFinished(ctx, time.Since(started), err == nil)
				if err != nil && logger != nil {
					logger.WarnContext(ctx, "shield hinted page failed", "bk_tenant_id", item.hint.TenantID, "main_alert_id", item.hint.MainAlertID, "reason_code", code, "failed", failed)
				}
			}
		}
		return nil
	}}})
}

func checkShieldHintPage(ctx context.Context, reader store.ShieldDependencyReader, checker shieldHintChecker, item shieldHintCursor) (string, int, int, error) {
	if store.ValidateShieldDependentsQuery(item.hint.TenantID, item.hint.MainAlertID, item.after, 16) != nil {
		return "", 0, 1, store.ErrInvalidArgument
	}
	query, cancel := context.WithTimeout(ctx, 5*time.Second)
	page, err := reader.ListShieldDependents(query, item.hint.TenantID, item.hint.MainAlertID, item.after, 16)
	cancel()
	if err != nil {
		return "", 0, 1, err
	}
	if len(page.Alerts) > 16 {
		return "", 0, 1, fmt.Errorf("shield hint page exceeds budget")
	}
	last := item.after
	for _, row := range page.Alerts {
		a := row.Alert
		if row.Version.IsZero() || a.Validate() != nil || a.BKTenantID != item.hint.TenantID || a.AlertID <= last || !slices.Contains(store.ShieldMainAlertIDs(a), item.hint.MainAlertID) {
			return "", 0, 1, fmt.Errorf("shield hint page scope or order mismatch")
		}
		last = a.AlertID
	}
	if page.Next != "" && (len(page.Alerts) != 16 || page.Next != last || last <= item.after) {
		return "", 0, 1, fmt.Errorf("shield hint cursor mismatch")
	}
	jobs := make(chan string, len(page.Alerts))
	for _, row := range page.Alerts {
		jobs <- row.Alert.AlertID
	}
	close(jobs)
	var failed atomic.Int64
	var wg sync.WaitGroup
	for range min(4, len(page.Alerts)) {
		wg.Go(func() {
			for id := range jobs {
				if ctx.Err() != nil {
					failed.Add(1)
					continue
				}
				if checker.CheckHint(ctx, item.hint.TenantID, id) != nil {
					failed.Add(1)
				}
			}
		})
	}
	wg.Wait()
	n := int(failed.Load())
	if n > 0 {
		err = errors.New("shield hinted checks incomplete")
	}
	return page.Next, len(page.Alerts), n, err
}
