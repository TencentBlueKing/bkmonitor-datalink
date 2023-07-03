// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"context"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
)

// TaskManager :
type TaskManager struct {
	lock  sync.RWMutex
	tasks []Task
}

// ForEach :
func (t *TaskManager) ForEach(fn func(index int, task Task) error) error {
	t.lock.RLock()
	defer t.lock.RUnlock()
	for index, task := range t.tasks {
		err := fn(index, task)
		if err != nil {
			return err
		}
	}
	return nil
}

// Clear :
func (t *TaskManager) Clear() {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.tasks = t.tasks[:0]
}

// Add :
func (t *TaskManager) Add(task Task) {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.tasks = append(t.tasks, task)
}

// Start :
func (t *TaskManager) Start() error {
	return t.ForEach(func(index int, task Task) error {
		return errors.WithMessagef(task.Start(), "start task %v failed", task)
	})
}

// Stop :
func (t *TaskManager) Stop() error {
	return t.ForEach(func(index int, task Task) error {
		return errors.WithMessagef(task.Stop(), "stop task %v failed", task)
	})
}

// Wait :
func (t *TaskManager) Wait() error {
	return t.ForEach(func(index int, task Task) error {
		return errors.WithMessagef(task.Wait(), "wait task %v failed", task)
	})
}

// NewTaskManager :
func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasks: make([]Task, 0),
	}
}

// BaseTask :
type BaseTask struct {
	ctx       context.Context
	cancelFn  context.CancelFunc
	waitGroup sync.WaitGroup
}

// Activate :
func (t *BaseTask) Activate(fn func(ctx context.Context)) error {
	t.waitGroup.Add(1)
	go func() {
		defer t.waitGroup.Done()
		fn(t.ctx)
	}()
	return nil
}

// Start :
func (t *BaseTask) Start() error {
	return nil
}

// Stop :
func (t *BaseTask) Stop() error {
	t.cancelFn()
	return nil
}

// Wait :
func (t *BaseTask) Wait() error {
	t.waitGroup.Wait()
	return nil
}

// NewBaseTask :
func NewBaseTask(ctx context.Context) *BaseTask {
	ctx, cancelFn := context.WithCancel(ctx)
	return &BaseTask{
		ctx:      ctx,
		cancelFn: cancelFn,
	}
}

// ContextTask :
type ContextTask struct {
	*BaseTask
	fn func(ctx context.Context)
}

// Start :
func (t *ContextTask) Start() error {
	err := t.BaseTask.Start()
	if err != nil {
		return err
	}
	return t.Activate(t.fn)
}

// NewContextTask :
func NewContextTask(ctx context.Context, fn func(ctx context.Context)) *ContextTask {
	return &ContextTask{
		BaseTask: NewBaseTask(ctx),
		fn:       fn,
	}
}

// PeriodTask :
type PeriodTask struct {
	*ContextTask
	callback    func(ctx context.Context) bool
	immediately bool
}

// Start :
func (t *PeriodTask) Start() error {
	if t.immediately && !t.callback(t.ctx) {
		return errors.Errorf("task done")
	}

	return t.ContextTask.Start()
}

// NewPeriodTask : 周期任务 周期调用callback的任务
func NewPeriodTask(ctx context.Context, period time.Duration, immediately bool, callback func(ctx context.Context) bool) *PeriodTask {
	return &PeriodTask{
		immediately: immediately,
		callback:    callback,
		ContextTask: NewContextTask(ctx, func(subCtx context.Context) {
			ticker := time.NewTicker(period)
		loop:
			for {
				select {
				case <-subCtx.Done():
					break loop
				case <-ticker.C:
					if !callback(subCtx) {
						break loop
					}
				}
			}
			ticker.Stop()
		}),
	}
}

// NewPeriodTaskWithEventBus
func NewPeriodTaskWithEventBus(ctx context.Context, period time.Duration, immediately bool, topic string, bus eventbus.Bus) *PeriodTask {
	return NewPeriodTask(ctx, period, immediately, func(ctx context.Context) bool {
		bus.Publish(topic, ctx)
		return true
	})
}
