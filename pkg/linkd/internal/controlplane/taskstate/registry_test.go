// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskstate

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"linkd/internal/taskgroup"
)

func testRegistry() *Registry {
	return New("owner", []Definition{{ID: "periodic", Name: "周期", Kind: "periodic", Enabled: true, IntervalSeconds: 1, DeadlineSeconds: 2, Settings: map[string]any{"limit": 16}}, {ID: "archive", Kind: "continuous", Enabled: true, IntervalSeconds: 5}, {ID: "off", Enabled: false}, {ID: "api", Kind: "service", Enabled: true}})
}

func TestActualOwnerAndCancellation(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			r := testRegistry()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started := make(chan struct{})
			done := make(chan error, 1)
			want := errors.New("failed")
			task := r.Wrap("periodic", taskgroup.Task{Name: "periodic", Run: func(ctx context.Context) error {
				r.Begin("periodic", "", "")
				close(started)
				<-ctx.Done()
				r.Finish(ctx, "periodic", "", "", time.Second, 1, 0, "failed")
				if failure {
					return want
				}
				return nil
			}})
			go func() { done <- task.Run(ctx) }()
			<-started
			if !r.Snapshot().Tasks[0].Active {
				t.Fatal("missing actual owner")
			}
			cancel()
			err := <-done
			if failure && !errors.Is(err, want) {
				t.Fatal(err)
			}
			v := r.Snapshot().Tasks[0]
			if v.Active || v.Execution.Failed != 0 || v.Execution.Canceled != 1 {
				t.Fatalf("bad cancel %+v", v)
			}
		})
	}
}

func TestStateSemanticsAndIsolation(t *testing.T) {
	r := testRegistry()
	r.active("periodic", 1)
	r.active("archive", 1)
	r.active("api", 1)
	if r.Snapshot().Services[0].State != "healthy" {
		t.Fatal("service requires periodic success")
	}
	r.Finish(t.Context(), "periodic", "", "", 0, 0, 0, "")
	if r.Snapshot().Tasks[0].State != "idle" {
		t.Fatal("empty work not idle")
	}
	r.Finish(t.Context(), "periodic", "target", "target", 0, 3, 1, "partial_failure")
	if r.Snapshot().Tasks[0].State != "failed" {
		t.Fatal("partial failure hidden")
	}
	r.RetainSteps("periodic", nil)
	if r.Snapshot().Tasks[0].State != "idle" {
		t.Fatal("removed target poisoned health")
	}
	r.Finish(t.Context(), "archive", "", "", time.Hour, 0, 0, "")
	old := time.Now().Add(-time.Hour)
	r.entries["archive"].task.Execution.FinishedAt = &old
	if r.Snapshot().Tasks[1].State != "idle" {
		t.Fatal("continuous task incorrectly overdue")
	}
	r.entries["periodic"].task.Execution.FinishedAt = &old
	if r.Snapshot().Tasks[0].State != "overdue" {
		t.Fatal("missing overdue")
	}
	r.Begin("periodic", "", "")
	r.entries["periodic"].task.Execution.StartedAt = &old
	if r.Snapshot().Tasks[0].State != "overdue" {
		t.Fatal("hung round not overdue")
	}
	v := r.Snapshot()
	v.Tasks[0].Settings["limit"] = 99
	*v.Tasks[0].Execution.StartedAt = time.Now()
	if r.Snapshot().Tasks[0].Settings["limit"] != 16 || r.Snapshot().Tasks[0].State != "overdue" {
		t.Fatal("snapshot aliases mutable state")
	}
	if r.Snapshot().Tasks[2].State != "disabled" {
		t.Fatal("disabled confused with missing observations")
	}
}

func TestBoundedConcurrentSnapshotsAndRestart(t *testing.T) {
	r := testRegistry()
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() { r.Finish(t.Context(), "periodic", fmt.Sprint(i), "target", 0, 1, 0, ""); r.Snapshot() })
	}
	wg.Wait()
	s := r.Snapshot().Tasks[0]
	if len(s.Steps) != 64 || !s.DetailsTruncated {
		t.Fatal("unbounded detail list")
	}
	r.Finish(t.Context(), "unknown", "", "", 0, 0, 0, "")
	if len(r.Snapshot().Tasks) != 3 {
		t.Fatal("unknown task created")
	}
	r.Finish(t.Context(), "periodic", "", "", 0, 1, 0, "")
	if testRegistry().Snapshot().Tasks[0].Execution.Succeeded != 0 {
		t.Fatal("new process inherits prior counts")
	}
}

func TestOwnerReleasedOnPanic(t *testing.T) {
	r := testRegistry()
	task := r.Wrap("periodic", taskgroup.Task{Name: "panic", Run: func(context.Context) error { panic("test") }})
	if err := taskgroup.Run(t.Context(), []taskgroup.Task{task}); err == nil {
		t.Fatal("panic hidden")
	}
	if r.Snapshot().Tasks[0].Active {
		t.Fatal("owner leaked")
	}
}

func TestUnregisteredOrDisabledTaskCannotRun(t *testing.T) {
	for _, id := range []string{"unknown", "off"} {
		t.Run(id, func(t *testing.T) {
			ran := false
			task := testRegistry().Wrap(id, taskgroup.Task{Name: id, Run: func(context.Context) error { ran = true; return nil }})
			if task.Run(t.Context()) == nil || ran {
				t.Fatal("unregistered work ran without observation")
			}
		})
	}
}
