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
	"reflect"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/controlplane/taskstate"
	"linkd/internal/eventsource"
	"linkd/internal/telemetry"
)

func TestSourceStreamTaskReportsOwnerForEntireLifetime(t *testing.T) {
	for _, tc := range []struct {
		name     string
		runError error
		cancel   bool
	}{
		{name: "normal return"}, {name: "failed", runError: errors.New("failed")}, {name: "canceled", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observer := &recordingTaskObserver{}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			task := newSourceStreamTask(observer, func(call context.Context) error {
				if !reflect.DeepEqual(observer.active, []bool{true}) {
					t.Fatalf("owner was not active before loop: %v", observer.active)
				}
				if tc.cancel {
					cancel()
					<-call.Done()
				}
				return tc.runError
			})
			if err := task.Run(ctx); !errors.Is(err, tc.runError) {
				t.Fatalf("error=%v want=%v", err, tc.runError)
			}
			if !reflect.DeepEqual(observer.active, []bool{true, false}) {
				t.Fatalf("owner lifecycle=%v", observer.active)
			}
			if len(observer.succeeded) != 0 {
				t.Fatal("wrapper duplicated per-stream execution metrics")
			}
		})
	}
}

func TestSourceStreamTaskOwnerSurvivesIdleWaitAndExitsOnCancel(t *testing.T) {
	observer := &recordingTaskObserver{}
	started := make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	task := newSourceStreamTask(observer, func(call context.Context) error { close(started); <-call.Done(); return nil })
	go func() { done <- task.Run(ctx) }()
	<-started
	observer.mu.Lock()
	active := append([]bool(nil), observer.active...)
	observer.mu.Unlock()
	if !reflect.DeepEqual(active, []bool{true}) {
		t.Fatalf("idle owner=%v", active)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if !reflect.DeepEqual(observer.active, []bool{true, false}) {
		t.Fatalf("exit owner=%v", observer.active)
	}
}

func TestSourceStreamTaskWorksWithoutMetrics(t *testing.T) {
	var observer *telemetry.ControlPlaneTaskObserver
	called := false
	task := newSourceStreamTask(observer, func(context.Context) error { called = true; return nil })
	if err := task.Run(t.Context()); err != nil || !called {
		t.Fatalf("called=%v err=%v", called, err)
	}
}

type sourceListResult struct {
	records []eventsource.Record
	err     error
}

func (s sourceListResult) List(context.Context, string, int) ([]eventsource.Record, error) {
	return s.records, s.err
}

func TestSourceScanReportsPartialFailureAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		listErr bool
		cancel  bool
	}{{"partial", false, false}, {"list failure", true, false}, {"canceled", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			r := taskCatalog(config.Config{}, 0)
			s := sourceListResult{records: []eventsource.Record{{ID: "a"}, {ID: "b"}}}
			if tc.listErr {
				s.err = errors.New("private connection string")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			after := reconcileSourceStreams(ctx, time.Second, s, "", r, func(context.Context, eventsource.Record) error {
				calls++
				if tc.cancel {
					cancel()
					return ctx.Err()
				}
				if calls == 1 {
					return errors.New("failure")
				}
				return nil
			})
			if (tc.cancel && after != "a") || (!tc.cancel && after != "") {
				t.Fatal(after)
			}
			var task taskstate.Task
			for _, v := range r.Snapshot().Tasks {
				if v.ID == "redis-stream-manager" {
					task = v
				}
			}
			if tc.cancel {
				if task.Execution.Canceled != 1 || task.Execution.Failed != 0 {
					t.Fatal(task)
				}
			} else if task.Execution.Failed != 1 {
				t.Fatal("failure hidden", task)
			}
			if tc.listErr {
				if calls != 0 || task.Execution.ErrorCode != "source_list_failed" {
					t.Fatal(task)
				}
			} else if (tc.cancel && (len(task.Steps) != 1 || calls != 1)) || (!tc.cancel && len(task.Steps) != 2) {
				t.Fatal("missing source outcomes")
			}
		})
	}
}

func TestSourceStreamTimeoutDoesNotConsumeLaterSourceBudget(t *testing.T) {
	registry := taskCatalog(config.Config{}, 0)
	sources := sourceListResult{records: []eventsource.Record{{ID: "slow"}, {ID: "healthy"}}}
	calls := 0
	timeout := 20 * time.Millisecond
	after := reconcileSourceStreams(t.Context(), timeout, sources, "", registry, func(ctx context.Context, source eventsource.Record) error {
		calls++
		if source.ID == "slow" {
			<-ctx.Done()
			return ctx.Err()
		}
		if ctx.Err() != nil {
			t.Fatal("previous source exhausted next source's budget")
		}
		deadline, ok := ctx.Deadline()
		if !ok || !deadline.After(time.Now()) {
			t.Fatal("missing independent deadline")
		}
		return nil
	})
	if after != "" || calls != 2 {
		t.Fatalf("after=%q calls=%d", after, calls)
	}
	for _, task := range registry.Snapshot().Tasks {
		if task.ID != "redis-stream-manager" {
			continue
		}
		if task.Execution.Failed != 1 || task.Execution.Failures != 1 || task.Execution.Work != 2 {
			t.Fatal(task.Execution)
		}
		for _, step := range task.Steps {
			if step.ID == "slow" && step.ErrorCode != "stream_timeout" {
				t.Fatal(step)
			}
			if step.ID == "healthy" && step.Outcome != "succeeded" {
				t.Fatal(step)
			}
		}
	}
}

type sourceListFunc func(context.Context, string, int) ([]eventsource.Record, error)

func (f sourceListFunc) List(ctx context.Context, after string, limit int) ([]eventsource.Record, error) {
	return f(ctx, after, limit)
}

func TestSourceDirectoryTimeoutKeepsCursorAndDoesNotStartStreams(t *testing.T) {
	registry := taskCatalog(config.Config{}, 0)
	sources := sourceListFunc(func(ctx context.Context, after string, limit int) ([]eventsource.Record, error) {
		if after != "previous" || limit != sourceStreamPageSize {
			t.Fatal(after, limit)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	after := reconcileSourceStreams(t.Context(), 10*time.Millisecond, sources, "previous", registry, func(context.Context, eventsource.Record) error { t.Fatal("unexpected source"); return nil })
	if after != "previous" {
		t.Fatal(after)
	}
	for _, task := range registry.Snapshot().Tasks {
		if task.ID == "redis-stream-manager" && task.Execution.ErrorCode != "source_list_timeout" {
			t.Fatal(task.Execution)
		}
	}
}

func TestSourceStreamCatalogDeadlineCoversDirectoryAndEverySource(t *testing.T) {
	registry := taskCatalog(config.Config{Storage: &config.StorageConfig{Redis: &config.RedisConfig{}}, Lifecycle: &config.LifecycleConfig{}}, 0)
	for _, task := range registry.Snapshot().Tasks {
		if task.ID == "redis-stream-manager" && task.DeadlineSeconds != 3*float64(sourceStreamPageSize+1) {
			t.Fatal(task)
		}
	}
}
