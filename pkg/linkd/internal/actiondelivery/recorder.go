// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package actiondelivery

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"time"

	"linkd/internal/domain"
)

// RecordingStore 只提供原动作入队、预算和排序可见性确认，不暴露扫描或发送顺序读取。
type RecordingStore interface {
	Get(context.Context, string, string) (StoredTask, error)
	Put(context.Context, Task, string) (StoredTask, error)
	CountWork(context.Context, string, int) (int, error)
	ConfirmVisible(context.Context, Task) error
}

// Recorder 可靠保存已获准动作，不持有 KAC 凭据、来源解析器、投影 Gate 或 HTTP Sender。
// 与业务意图按原版本共同使用，最多四个并发入队，每次有界排队/锁租约/持久化/排序可见性确认。
type Recorder struct {
	tasks  RecordingStore
	locker TargetLocker
	now    func() time.Time
	slots  chan struct{}
}

// NewRecorder 构造仅入队端口；不打开网络连接、不创建 goroutine，不直接执行通知或处置。
func NewRecorder(tasks RecordingStore, locker TargetLocker, now func() time.Time) (*Recorder, error) {
	if tasks == nil || locker == nil || now == nil {
		return nil, ErrInvalid
	}
	return &Recorder{tasks: tasks, locker: locker, now: now, slots: make(chan struct{}, 4)}, nil
}

// RecordAction 实现 Lifecycle 的可靠入队端口，最多 16 个目标共享十秒预算。
// 部分成功后返回错误时保留原 Alert 意图；重试复用所有已存在任务，直到全部排序可见。
func (s *Recorder) RecordAction(ctx context.Context, a domain.Alert, intent domain.AlertActionIntent) error {
	if ctx == nil || a.Validate() != nil || intent.Validate(a) != nil || !reflect.DeepEqual(a.ActionPending, &intent) {
		return ErrInvalid
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ids := make([]string, 0, len(intent.Targets))
	for id := range intent.Targets {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if _, err := s.Record(call, a, id, Cause{Type: intent.CauseType, ID: intent.CauseID}); err != nil {
			return err
		}
	}
	return nil
}

func withActionAdmission(ctx context.Context, locker TargetLocker, slots chan struct{}, tenant string, run func(context.Context) (StoredTask, error)) (result StoredTask, err error) {
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-call.Done():
		return StoredTask{}, call.Err()
	}
	release, e := locker.Acquire(call, digest("linkd:action-admission:v1", tenant))
	if e != nil {
		return StoredTask{}, e
	}
	if release == nil {
		return StoredTask{}, ErrInvalid
	}
	defer func() {
		end, stop := context.WithTimeout(context.WithoutCancel(call), 2*time.Second)
		defer stop()
		err = errors.Join(err, release(end))
	}()
	return run(call)
}

// Record 幂等保存本次原动作，失败必须由业务意图生产者重试，不能按普通 Hook 错误吞掉。
// 相同业务版本的快照/动作/来源引用变化返回冲突；满额时已存在的相同记录仍可复用。
func (s *Recorder) Record(ctx context.Context, a domain.Alert, target string, cause Cause) (StoredTask, error) {
	if ctx == nil {
		return StoredTask{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return StoredTask{}, err
	}
	t, e := NewTask(a, target, cause, s.now())
	if e != nil {
		return StoredTask{}, e
	}
	return withActionAdmission(ctx, s.locker, s.slots, a.BKTenantID, func(ctx context.Context) (StoredTask, error) {
		old, e := s.tasks.Get(ctx, a.BKTenantID, t.ID)
		if e == nil {
			return s.confirmRecorded(ctx, old, t)
		}
		if !errors.Is(e, ErrNotFound) {
			return StoredTask{}, e
		}
		n, e := s.tasks.CountWork(ctx, a.BKTenantID, MaxPendingPerTenant)
		if e != nil {
			return StoredTask{}, e
		}
		if n < 0 || n > MaxPendingPerTenant {
			return StoredTask{}, ErrInvalid
		}
		if n == MaxPendingPerTenant {
			return StoredTask{}, ErrCapacity
		}
		row, e := s.tasks.Put(ctx, t, "")
		if e == nil {
			return s.confirmRecorded(ctx, row, t)
		}
		if !errors.Is(e, ErrConflict) {
			return StoredTask{}, e
		}
		old, e = s.tasks.Get(ctx, a.BKTenantID, t.ID)
		if e != nil {
			return StoredTask{}, e
		}
		return s.confirmRecorded(ctx, old, t)
	})
}

func sameRecorded(old StoredTask, next Task) (StoredTask, error) {
	if old.Task.Validate() != nil || old.Version == "" {
		return StoredTask{}, ErrInvalid
	}
	a, b := old.Task.Clone(), next.Clone()
	a.Progress, b.Progress = Progress{}, Progress{}
	a.CreatedAt, b.CreatedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(a, b) {
		return StoredTask{}, ErrConflict
	}
	return old, nil
}

// 入队成功同时证明排序索引可发现；实时 GET 命中不足以确认 ES 的搜索可见性。
func (s *Recorder) confirmRecorded(ctx context.Context, old StoredTask, next Task) (StoredTask, error) {
	current, e := sameRecorded(old, next)
	if e != nil {
		return current, e
	}
	if current.Task.Unsettled() {
		if e := s.tasks.ConfirmVisible(ctx, current.Task); e != nil {
			return current, e
		}
	}
	return current, nil
}
