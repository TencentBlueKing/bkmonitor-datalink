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
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

func TestAgentRejectsOverBudgetAssignments(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	runtime := workerRuntime(config.Config{}, []string{"lifecycle"})
	rejected := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/releases/") {
			writeAgentTestResponse(t, w, eventsource.Release{ID: "source", Version: 1, Spec: config.EventSource{EventSourceID: "source"}})
			return
		}
		var heartbeat Heartbeat
		if err := json.NewDecoder(r.Body).Decode(&heartbeat); err != nil {
			t.Error(err)
			http.Error(w, "bad heartbeat", 400)
			return
		}
		if heartbeat.Worker.Runtime.Lifecycle == nil || *heartbeat.Worker.Runtime.Lifecycle != *runtime.Lifecycle {
			t.Error("effective worker budget not advertised")
		}
		for _, report := range heartbeat.Reports {
			if report.Phase == "stopped" && report.Error == "worker runtime budget rejected" {
				select {
				case rejected <- struct{}{}:
				default:
				}
			}
		}
		tasks := make([]Task, 2)
		for i := range tasks {
			tasks[i] = Task{ID: fmt.Sprintf("source-%d:lifecycle:0", i), Source: fmt.Sprintf("source-%d", i), Role: "lifecycle", Epoch: int64(i + 1), Version: 1, Phase: "starting", RemainingMillis: 60000, Concurrency: runtime.Lifecycle.Concurrency, InflightBytes: runtime.Lifecycle.InflightBytes}
		}
		writeAgentTestResponse(t, w, tasks)
	}))
	defer server.Close()
	var started atomic.Int64
	a := Agent{Config: config.DispatchConfig{URL: server.URL}, Runtime: runtime, Roles: []string{"lifecycle"}, Worker: config.WorkerConfig{MaxConcurrency: 32}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), RunTask: func(ctx context.Context, _ Task, _ config.EventSource) error {
		started.Add(1)
		<-ctx.Done()
		return ctx.Err()
	}}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	select {
	case <-rejected:
	case <-ctx.Done():
		t.Fatal("over-budget assignment was not rejected")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent did not stop")
	}
	if started.Load() != 1 {
		t.Fatalf("started %d tasks above max concurrency 32", started.Load())
	}
}

func TestHostAdvertisesBothEffectiveRoleBudgets(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	registered := make(chan Worker, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var h Heartbeat
		if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
			t.Error(err)
			http.Error(w, "bad heartbeat", 400)
			return
		}
		select {
		case registered <- h.Worker:
		default:
		}
		writeAgentTestResponse(t, w, []Task{})
	}))
	defer server.Close()
	dispatch := config.DispatchConfig{URL: server.URL, WorkerToken: "synthetic-worker"}
	host := &Host{Config: dispatch, Roles: []string{"cleaner", "lifecycle"}}
	ctx = WithHost(ctx, host)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	done := make(chan error, 2)
	for _, role := range host.Roles {
		cfg := config.Config{Dispatch: dispatch, Cleaner: config.CleanerRuntimeConfig{WorkerCount: 24}, Lifecycle: &config.LifecycleConfig{Concurrency: 64}}
		go func() {
			done <- Serve(ctx, cfg, role, func(context.Context, Task, config.EventSource) error { return fmt.Errorf("unexpected task") }, logger)
		}()
	}
	select {
	case w := <-registered:
		if w.Runtime.Cleaner == nil || w.Runtime.Cleaner.WorkerCount != 24 || w.Runtime.Lifecycle == nil || w.Runtime.Lifecycle.Concurrency != 64 {
			t.Fatalf("role budgets missing: %+v", w.Runtime)
		}
	case <-ctx.Done():
		t.Fatal("host did not register")
	}
	cancel()
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("role did not stop")
		}
	}
}

func writeAgentTestResponse(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}
