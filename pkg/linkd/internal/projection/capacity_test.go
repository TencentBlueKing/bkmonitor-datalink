// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package projection

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
)

func fillProjectionTasks(t *testing.T, m *taskMemory, a domain.Alert, count int) {
	t.Helper()
	for i := range count {
		next := a.Clone()
		next.AlertID = fmt.Sprintf("budget-%04d", i)
		task, err := NewTask(next, "kac", time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Put(t.Context(), task, ""); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBacklogDoesNotBlockProductionOrExplicitRecovery(t *testing.T) {
	s, tasks, _, _, sender, _, a := projectionFixture(t)
	fillProjectionTasks(t, tasks, a, 2048)
	first, err := s.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac")
	if err != nil {
		t.Fatal(err)
	}
	sender.err = Failure{"remote_unauthorized", false}
	failed, err := s.Deliver(t.Context(), a.BKTenantID, first.Task.ID)
	if err != nil || failed.Task.Progress.State != "failed" {
		t.Fatal(failed, err)
	}
	command := RetryCommand{TenantID: a.BKTenantID, TaskID: first.Task.ID, ExpectedVersion: failed.Version, OperationID: "restore", OperatorID: "tester", Reason: "receiver restored"}
	restored, err := s.Retry(t.Context(), command)
	if err != nil || restored.Task.Progress.Generation != 2 {
		t.Fatal(restored, err)
	}
	again, err := s.Retry(t.Context(), command)
	if err != nil || again.Version != restored.Version {
		t.Fatal("idempotent retry", err)
	}
}

func TestConcurrentProducerInstancesCanPassFormerTenantLimit(t *testing.T) {
	first, tasks, repo, resolver, sender, now, a := projectionFixture(t)
	fillProjectionTasks(t, tasks, a, WorkCountSampleLimit-1)
	for i := range 2 {
		next := a.Clone()
		next.AlertID = fmt.Sprintf("concurrent-%d", i)
		next.Fingerprint = next.AlertID
		if _, err := repo.CreateAlert(t.Context(), next); err != nil {
			t.Fatal(err)
		}
	}
	second, err := New(tasks, repo, resolver, sender, first.locker, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for i, s := range []*Service{first, second} {
		wg.Go(func() {
			<-start
			_, err := s.Produce(t.Context(), a.BKTenantID, fmt.Sprintf("concurrent-%d", i), "kac")
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrBusy) && !errors.Is(err, ErrCapacity) {
			t.Fatal(err)
		}
	}
	n, err := tasks.CountWork(t.Context(), a.BKTenantID, WorkCountSampleLimit+1)
	if err != nil || success != 2 || n != WorkCountSampleLimit+1 {
		t.Fatal("budget raced", success, n, err)
	}
}

type failingCount struct {
	Store
	value int
	err   error
}

func (f failingCount) CountWork(context.Context, string, int) (int, error) { return f.value, f.err }

func TestProducerDoesNotDependOnTenantCount(t *testing.T) {
	for _, f := range []failingCount{{value: -1}, {value: WorkCountSampleLimit + 1}, {err: errors.New("count unavailable")}} {
		s, tasks, _, _, _, _, a := projectionFixture(t)
		f.Store = tasks
		s.tasks = f
		if _, err := s.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac"); err != nil {
			t.Fatal("count should not gate persistence", err)
		}
		if len(tasks.rows) != 1 {
			t.Fatal("count failure created task")
		}
	}
}
