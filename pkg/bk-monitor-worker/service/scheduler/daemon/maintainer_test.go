// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
)

type daemonTestAttempt struct {
	ctx     context.Context
	errors  chan<- error
	payload string
}

type daemonTestOperator chan daemonTestAttempt

func (op daemonTestOperator) Start(ctx context.Context, errs chan<- error, payload []byte) {
	op <- daemonTestAttempt{ctx, errs, string(payload)}
}

func (op daemonTestOperator) GetTaskDimension([]byte) string { return "test" }

func (op daemonTestOperator) next(t *testing.T, timeout time.Duration) daemonTestAttempt {
	t.Helper()
	select {
	case attempt := <-op:
		return attempt
	case <-time.After(timeout):
		t.Fatal("timed out waiting for task to start")
		return daemonTestAttempt{}
	}
}

func newDaemonTestBinding(t *testing.T) (*RunMaintainer, TaskBinding, daemonTestOperator) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	op := make(daemonTestOperator, 1)
	r := &RunMaintainer{ctx: ctx, runningInstance: &sync.Map{}, methodOperatorMapping: map[string]Operator{"test": op}}
	binding := TaskBinding{UniId: "test-id", SerializerTask: task.SerializerTask{Kind: "test", Payload: []byte("original")}}
	r.handleAddTaskBinding(binding)
	return r, binding, op
}

func TestDaemonUnbindWithLateTaskError(t *testing.T) {
	r, binding, op := newDaemonTestBinding(t)
	old := op.next(t, time.Second)
	r.handleDeleteTaskBinding(binding.UniId)
	if old.ctx.Err() == nil {
		t.Fatal("removed task is still running")
	}
	if _, exists := r.runningInstance.Load(binding.UniId); exists {
		t.Fatal("removed binding is still registered")
	}

	r.handleAddTaskBinding(binding)
	current := op.next(t, time.Second)
	old.errors <- errors.New("late task error")
	if current.ctx.Err() != nil {
		t.Fatal("late error canceled the replacement task")
	}
	r.handleDeleteTaskBinding(binding.UniId)
}

func TestDaemonUnbindDuringReloadAndTaskError(t *testing.T) {
	r, binding, op := newDaemonTestBinding(t)
	attempt := op.next(t, time.Second)
	value, _ := r.runningInstance.Load(binding.UniId)
	instance := value.(*runningBinding)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, action := range []func(){
		func() { r.handleReloadBinding(binding) },
		func() { attempt.errors <- errors.New("task failed") },
		func() { r.handleDeleteTaskBinding(binding.UniId) },
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			action()
		}()
	}
	close(start)
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("unbind, reload or task error is blocked")
	}
	select {
	case <-instance.done:
	default:
		t.Fatal("listener did not exit on unbind")
	}
	if _, exists := r.runningInstance.Load(binding.UniId); exists {
		t.Fatal("listener restored removed binding")
	}
}

func TestDaemonReloadRetriesWithUpdatedPayload(t *testing.T) {
	r, binding, op := newDaemonTestBinding(t)
	attempt := op.next(t, time.Second)
	binding.Payload = []byte("updated")
	r.handleReloadBinding(binding)
	retry := op.next(t, 15*time.Second)
	if attempt.ctx.Err() == nil || retry.ctx.Err() != nil || retry.payload != "updated" {
		t.Fatal("reload did not replace the running task with the updated payload")
	}
	r.handleDeleteTaskBinding(binding.UniId)
}
