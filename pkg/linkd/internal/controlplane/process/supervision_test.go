// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplaneprocess

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/controlplane/taskstate"
	"linkd/internal/taskgroup"
	"linkd/internal/telemetry"
	"testing/synctest"
)

func TestSupervisionKeepsSiblingAliveAcrossErrorUnexpectedExitAndPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		registry := taskstate.New("test", []taskstate.Definition{{ID: "fault", Enabled: true}})
		var logs strings.Builder
		logger := slog.New(slog.NewTextHandler(&logs, nil))
		attempts, heartbeats := 0, 0
		fault := superviseTask(logger, registry, "fault", taskgroup.Task{Name: "fault", Run: func(ctx context.Context) error {
			attempts++
			switch attempts {
			case 1:
				return errors.New("ES read-only")
			case 2:
				return nil
			case 3:
				panic("private panic payload")
			}
			registry.Finish(ctx, "fault", "", "", 0, 0, 0, "")
			<-ctx.Done()
			return nil
		}})
		done := make(chan error, 1)
		go func() {
			done <- taskgroup.Run(ctx, []taskgroup.Task{registry.Wrap("fault", fault), {Name: "scheduler", Run: func(ctx context.Context) error {
				for waitForArchiveRetry(ctx, time.Second) {
					heartbeats++
				}
				return nil
			}}})
		}()
		time.Sleep(3 * taskRetryInterval)
		synctest.Wait()
		state := registry.Snapshot().Tasks[0]
		if attempts != 4 || heartbeats < 14 || state.Execution.Failed != 3 || state.State != "idle" {
			t.Fatalf("attempts=%d heartbeats=%d state=%+v", attempts, heartbeats, state)
		}
		if strings.Contains(logs.String(), "private panic payload") || !strings.Contains(logs.String(), "task_panic") {
			t.Fatal("panic diagnostic missing or includes payload", logs.String())
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if registry.Snapshot().Tasks[0].Active {
			t.Fatal("canceled task still active")
		}
	})
}

func TestElasticsearchFailureDoesNotBlockRuntimeAssembly(t *testing.T) {
	var calls atomic.Int64
	requested := make(chan struct{}, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case requested <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"type":"cluster_block_exception","reason":"disk usage exceeded flood-stage watermark"}}`)
	}))
	defer backend.Close()
	rt, err := telemetry.Start(t.Context(), config.TelemetryConfig{}, telemetry.RoleControlPlane, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Shutdown(context.Background()) }()
	cfg := config.Config{Storage: &config.StorageConfig{Repository: config.RepositoryTypeElasticsearch, Elasticsearch: &config.ElasticsearchConfig{Addresses: []string{backend.URL}, IndexPrefix: "test"}}}
	runtime, err := openRuntime(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), rt)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.closeAll() }()
	if calls.Load() != 0 {
		t.Fatal("runtime assembly contacted ES", calls.Load())
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.tasks[0].Run(ctx) }()
	select {
	case <-requested:
	case err := <-done:
		t.Fatal("failed task exited", err)
	case <-time.After(3 * time.Second):
		t.Fatal("background initialization did not start")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDependentTaskWaitsForPreparationAndCanBeCanceled(t *testing.T) {
	for _, readyFirst := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			ready := make(chan struct{})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			called := false
			done := make(chan error, 1)
			go func() {
				done <- afterTaskReady(ready, func(ctx context.Context) error { called = true; <-ctx.Done(); return nil })(ctx)
			}()
			synctest.Wait()
			if called {
				t.Fatal("dependent task ran before preparation")
			}
			if readyFirst {
				close(ready)
				synctest.Wait()
				if !called {
					t.Fatal("prepared task did not run")
				}
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
