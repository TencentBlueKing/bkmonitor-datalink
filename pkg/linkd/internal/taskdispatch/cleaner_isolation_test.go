// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskdispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/eventsource"
)

// 模拟坏来源取消后仍卡在关闭，跨过 watchdog 期限验证健康来源继续心跳，
// 且旧来源未停止时即使中心给出新代次也不能重复启动。
func TestCleanerStopTimeoutDoesNotExitHealthyWorker(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runtime := workerRuntime(config.Config{}, []string{"cleaner"})
	budget, err := runtime.budgetFor("cleaner", config.EventSource{})
	if err != nil {
		t.Fatal(err)
	}
	var revoke atomic.Bool
	var badStarts atomic.Int64
	var healthyReports atomic.Int64
	badStopping := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	forced := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/releases/") {
			writeAgentTestResponse(t, w, eventsource.Release{Version: 1, Spec: config.EventSource{}})
			return
		}
		var h Heartbeat
		if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
			t.Error(err)
			return
		}
		for _, report := range h.Reports {
			if report.ID == "healthy:cleaner:0" && report.Phase == "running" {
				healthyReports.Add(1)
			}
		}
		tasks := []Task{}
		for _, source := range []string{"bad", "healthy"} {
			epoch := int64(1)
			if source == "bad" && revoke.Load() {
				epoch = 2
			}
			tasks = append(tasks, Task{ID: source + ":cleaner:0", Source: source, Role: "cleaner", Epoch: epoch, Version: 1, Phase: "starting", RemainingMillis: 60000, Concurrency: budget.Concurrency, InflightBytes: budget.InflightBytes})
		}
		writeAgentTestResponse(t, w, tasks)
	}))
	defer server.Close()
	a := Agent{Config: config.DispatchConfig{URL: server.URL}, Runtime: runtime, Roles: []string{"cleaner"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), OnForcedExit: func() { forced <- struct{}{} }, RunTask: func(ctx context.Context, task Task, _ config.EventSource) error {
		if task.Source == "bad" {
			badStarts.Add(1)
			revoke.Store(true)
			<-ctx.Done()
			close(badStopping)
			<-release
			return ctx.Err()
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	defer func() {
		cancel()
		unblock()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("agent did not finish after cleanup")
		}
	}()
	select {
	case <-badStopping:
	case <-time.After(15 * time.Second):
		t.Fatal("bad source was not canceled")
	}
	before := healthyReports.Load()
	select {
	case <-forced:
		t.Fatal("source stop timeout exited the whole worker")
	case <-time.After(DrainTimeout + 6*time.Second):
	}
	if healthyReports.Load() <= before {
		t.Fatal("healthy source stopped reporting while bad source was closing")
	}
	if badStarts.Load() != 1 {
		t.Fatal("started a replacement before old source stopped")
	}
	// 进程级退出仍有界；模拟入口收到强退请求后释放测试中的阻塞资源。
	cancel()
	select {
	case <-forced:
	case <-time.After(3 * time.Second):
		t.Fatal("process shutdown did not retain forced exit")
	}
}

func TestCleanerRecoversAfterConsumerCloseWithoutWorkerRestart(t *testing.T) {
	t.Parallel()
	for _, delayed := range []bool{false, true} {
		t.Run(fmt.Sprintf("delayed_close=%v", delayed), func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			runtime := workerRuntime(config.Config{}, []string{"cleaner"})
			budget, err := runtime.budgetFor("cleaner", config.EventSource{})
			if err != nil {
				t.Fatal(err)
			}
			closed := make(chan struct{})
			var closeOnce sync.Once
			finishClose := func() { closeOnce.Do(func() { close(closed) }) }
			defer finishClose()
			if !delayed {
				finishClose()
			}
			failed := make(chan struct{})
			recovered := make(chan struct{})
			forced := make(chan struct{}, 1)
			var badStarts, healthyStarts, healthyMessages atomic.Int64
			var epoch atomic.Int64
			epoch.Store(1)
			var stoppingReported atomic.Bool
			// 服务器只在收到旧代次 stopped 后分配下一代，验证完整心跳恢复链路。
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/internal/releases/") {
					writeAgentTestResponse(t, w, eventsource.Release{Version: 1, Spec: config.EventSource{}})
					return
				}
				var h Heartbeat
				if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
					t.Error(err)
					return
				}
				for _, report := range h.Reports {
					if report.ID == "bad:cleaner:0" && report.Epoch == 1 {
						if report.Phase == "stopping" {
							stoppingReported.Store(true)
						}
						if report.Phase == "stopped" {
							select {
							case <-closed:
							default:
								t.Error("reported stopped before client closed")
							}
							epoch.Store(2)
						}
					}
				}
				tasks := []Task{}
				for _, source := range []string{"bad", "healthy"} {
					e := int64(1)
					if source == "bad" {
						e = epoch.Load()
					}
					tasks = append(tasks, Task{ID: source + ":cleaner:0", Source: source, Role: "cleaner", Epoch: e, Version: 1, Phase: "starting", RemainingMillis: 60000, Concurrency: budget.Concurrency, InflightBytes: budget.InflightBytes})
				}
				writeAgentTestResponse(t, w, tasks)
			}))
			defer server.Close()
			a := Agent{Config: config.DispatchConfig{URL: server.URL}, Runtime: runtime, Roles: []string{"cleaner"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), OnForcedExit: func() { forced <- struct{}{} }, RunTask: func(ctx context.Context, task Task, _ config.EventSource) error {
				if task.Source == "bad" {
					if badStarts.Add(1) == 1 {
						close(failed)
						// 真实 Runtime 会增加 consume/close 上下文，完成信号必须穿过错误链。
						return fmt.Errorf("consume: close cleaner session: %w", &consume.StopIncompleteError{Cause: context.DeadlineExceeded, Done: closed})
					}
					close(recovered)
					<-ctx.Done()
					return ctx.Err()
				}
				healthyStarts.Add(1)
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-ticker.C:
						healthyMessages.Add(1)
					}
				}
			}}
			done := make(chan error, 1)
			go func() { done <- a.Run(ctx) }()
			defer func() {
				finishClose()
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(5 * time.Second):
					t.Error("agent did not stop")
				}
			}()
			select {
			case <-failed:
			case <-time.After(12 * time.Second):
				t.Fatal("source did not fail")
			}
			if delayed {
				before := healthyMessages.Load()
				select {
				case <-forced:
					t.Fatal("source failure terminated worker")
				case <-recovered:
					t.Fatal("source restarted before cleanup")
				case <-time.After(DrainTimeout + 6*time.Second):
				}
				if healthyMessages.Load() <= before || !stoppingReported.Load() {
					t.Fatal("healthy source stopped or pending cleanup was not reported")
				}
				if badStarts.Load() != 1 {
					t.Fatal("duplicate consumer started during cleanup")
				}
				finishClose()
			}
			select {
			case <-recovered:
			case <-forced:
				t.Fatal("worker exited instead of recovering source")
			case <-time.After(12 * time.Second):
				t.Fatal("source did not recover after cleanup")
			}
			if badStarts.Load() != 2 || healthyStarts.Load() != 1 {
				t.Fatalf("bad starts=%d healthy starts=%d", badStarts.Load(), healthyStarts.Load())
			}
		})
	}
}
