// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskgroup

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunGracefullyStopsAllTasks(t *testing.T) {
	t.Parallel()

	started := make(chan string, 2)
	stopped := make(chan string, 2)
	task := func(name string) Task {
		return Task{Name: name, Run: func(ctx context.Context) error {
			started <- name
			<-ctx.Done()
			stopped <- name
			return ctx.Err()
		}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, []Task{task("cleaner"), task("lifecycle")})
	}()
	waitNames(t, started, 2)
	cancel()
	waitNames(t, stopped, 2)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not wait for task cleanup")
	}
}

func TestRunFailsFastAndWaitsForSibling(t *testing.T) {
	t.Parallel()

	siblingStarted := make(chan struct{})
	siblingStopped := make(chan struct{})
	want := errors.New("manager failed")
	tasks := []Task{
		{Name: "api", Run: func(ctx context.Context) error {
			close(siblingStarted)
			<-ctx.Done()
			close(siblingStopped)
			return ctx.Err()
		}},
		{Name: "manager", Run: func(context.Context) error {
			<-siblingStarted
			return want
		}},
	}
	err := Run(context.Background(), tasks)
	if !errors.Is(err, want) || !strings.Contains(err.Error(), `task "manager"`) {
		t.Fatalf("Run() error = %v", err)
	}
	select {
	case <-siblingStopped:
	default:
		t.Fatal("Run() returned before sibling cleanup")
	}
}

func TestRunTreatsUnexpectedStopAndPanicAsFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		runner    func(context.Context) error
		wantError string
	}{
		{name: "unexpected stop", runner: func(context.Context) error { return nil }, wantError: "stopped unexpectedly"},
		{name: "panic", runner: func(context.Context) error { panic("boom") }, wantError: "task panic: boom"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := Run(context.Background(), []Task{{Name: "task-a", Run: test.runner}})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Run() error = %v", err)
			}
		})
	}
}

func TestRunRejectsInvalidTaskDefinitions(t *testing.T) {
	t.Parallel()

	for _, tasks := range [][]Task{nil, {{Name: "missing-runner"}}, {{Run: func(context.Context) error { return nil }}}} {
		if err := Run(context.Background(), tasks); err == nil {
			t.Fatalf("Run(%#v) error = nil", tasks)
		}
	}
}

func waitNames(t *testing.T, values <-chan string, count int) map[string]bool {
	t.Helper()
	result := make(map[string]bool, count)
	for range count {
		select {
		case value := <-values:
			result[value] = true
		case <-time.After(time.Second):
			t.Fatalf("received %d of %d task notifications", len(result), count)
		}
	}
	return result
}
