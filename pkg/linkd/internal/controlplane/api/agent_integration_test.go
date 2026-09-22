// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/taskdispatch"
)

type emptySources struct{}

func (emptySources) Get(context.Context, string, string) (json.RawMessage, string, error) {
	return nil, "", eventsource.ErrNotFound
}

func (emptySources) Put(context.Context, string, string, string, json.RawMessage) error {
	return fmt.Errorf("unused")
}

func (emptySources) List(context.Context, string, string, int) ([]json.RawMessage, error) {
	return nil, nil
}

type agentSources struct {
	emptySources
	release eventsource.Release
}

func (a agentSources) Get(_ context.Context, kind, id string) (json.RawMessage, string, error) {
	if kind == "releases" {
		b, e := json.Marshal(a.release)
		return b, "1", e
	}
	return nil, "", eventsource.ErrNotFound
}

func TestAgentStopHandshakeIntegration(t *testing.T) {
	address := os.Getenv("LINKD_TEST_DISPATCH_REDIS")
	if address == "" {
		t.Skip("set LINKD_TEST_DISPATCH_REDIS for explicit integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: address})
	defer func() { _ = client.Close() }()
	rel, now := integrationRelease()
	sources := eventsource.New(agentSources{release: rel}, config.SeverityConfig{})
	c, e := newIntegrationController(ctx, client, sources, "agent-"+uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	defer client.Del(context.Background(), c.key, c.leader)
	cfg := config.DispatchConfig{APIToken: "test-admin", WorkerToken: "test-worker"}
	server := httptest.NewServer((&API{Sources: sources, Controller: c, Config: cfg}).Handler())
	defer server.Close()
	cfg.URL = server.URL
	started := make(chan struct{})
	a := taskdispatch.Agent{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Runtime: integrationWorkerRuntime(), Config: cfg, Roles: []string{"cleaner"}, RunTask: func(ctx context.Context, _ taskdispatch.Task, _ config.EventSource) error {
		close(started)
		<-ctx.Done()
		return nil
	}}
	run, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	done := make(chan error, 1)
	go func() { done <- a.Run(run) }()
	planned := false
	for !planned {
		if e := c.update(ctx, func(s *taskdispatch.State) error {
			if len(s.Workers) == 0 {
				return nil
			}
			s.Metadata[rel.ID] = taskdispatch.UpdateMetadata(taskdispatch.Metadata{}, rel.Spec, "topic-id", 1, nil, now)
			taskdispatch.Reconcile(s, []eventsource.Release{rel}, time.Now())
			planned = len(s.Tasks) > 0
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		if !planned {
			select {
			case <-ctx.Done():
				t.Fatal("worker did not register")
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("task was not authorized")
	}
	cancelRun()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("agent failed to stop")
	}
	s, e := c.Snapshot(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	for _, task := range s.Tasks {
		if task.Phase != "stopped" {
			t.Fatalf("stop report not confirmed: %+v", task)
		}
	}
}

func TestAgentDisconnectSelfStopIntegration(t *testing.T) {
	address := os.Getenv("LINKD_TEST_DISPATCH_REDIS")
	if address == "" {
		t.Skip("set LINKD_TEST_DISPATCH_REDIS for explicit integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: address})
	defer func() { _ = client.Close() }()
	rel, now := integrationRelease()
	sources := eventsource.New(agentSources{release: rel}, config.SeverityConfig{})
	c, e := newIntegrationController(ctx, client, sources, "agent-"+uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	defer client.Del(context.Background(), c.key, c.leader)
	cfg := config.DispatchConfig{APIToken: "test-admin", WorkerToken: "test-worker"}
	var offline atomic.Bool
	api := (&API{Sources: sources, Controller: c, Config: cfg}).Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		api.ServeHTTP(w, r)
	}))
	defer server.Close()
	cfg.URL = server.URL
	started := make(chan struct{})
	stopped := make(chan struct{})
	a := taskdispatch.Agent{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Runtime: integrationWorkerRuntime(), Config: cfg, Roles: []string{"cleaner"}, RunTask: func(ctx context.Context, _ taskdispatch.Task, _ config.EventSource) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil
	}}
	run, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	done := make(chan error, 1)
	go func() { done <- a.Run(run) }()
	planned := false
	for !planned {
		if e := c.update(ctx, func(s *taskdispatch.State) error {
			if len(s.Workers) == 0 {
				return nil
			}
			s.Metadata[rel.ID] = taskdispatch.UpdateMetadata(taskdispatch.Metadata{}, rel.Spec, "topic-id", 1, nil, now)
			taskdispatch.Reconcile(s, []eventsource.Release{rel}, time.Now())
			planned = len(s.Tasks) > 0
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		if !planned {
			select {
			case <-ctx.Done():
				t.Fatal("worker did not register")
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("task was not authorized")
	}
	offline.Store(true)
	select {
	case <-stopped:
	case <-time.After(20 * time.Second):
		t.Fatal("worker did not self stop after losing control connection")
	}
	offline.Store(false)
	time.Sleep(taskdispatch.HeartbeatInterval + time.Second)
	cancelRun()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("agent failed to stop")
	}
	s, e := c.Snapshot(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	for _, task := range s.Tasks {
		if task.Phase != "stopped" {
			t.Fatalf("stop report not confirmed: %+v", task)
		}
	}
}

// integrationController 仅为集成测试写入规划快照；HTTP 和 Worker 使用真实生产控制器。
// WATCH 避免测试规划覆盖并发心跳，保持原集成测试验证的握手和断联行为。
type integrationController struct {
	*taskdispatch.Controller
	client      *redis.Client
	key, leader string
}

func newIntegrationController(ctx context.Context, client *redis.Client, sources *eventsource.Service, namespace string) (*integrationController, error) {
	c, err := taskdispatch.NewController(ctx, client, sources, namespace)
	if err != nil {
		return nil, err
	}
	prefix := "linkd:dispatch:{" + namespace + "}"
	return &integrationController{c, client, prefix + ":state", prefix + ":leader"}, nil
}

func (c *integrationController) update(ctx context.Context, change func(*taskdispatch.State) error) error {
	for range 16 {
		err := c.client.Watch(ctx, func(tx *redis.Tx) error {
			raw, err := tx.Get(ctx, c.key).Bytes()
			if err != nil {
				return err
			}
			var state taskdispatch.State
			if err := json.Unmarshal(raw, &state); err != nil {
				return err
			}
			if err := change(&state); err != nil {
				return err
			}
			raw, err = json.Marshal(state)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error { return pipe.Set(ctx, c.key, raw, 0).Err() })
			return err
		}, c.key)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		return err
	}
	return fmt.Errorf("integration planning conflict")
}

func integrationRelease() (eventsource.Release, time.Time) {
	source := config.EventSource{EventSourceID: "source", Enabled: true}.WithDefaults()
	return eventsource.Release{ID: "source", Version: 1, Spec: source}, time.Now()
}

func integrationWorkerRuntime() taskdispatch.WorkerRuntime {
	cleaner := config.DefaultCleanerRuntimeConfig()
	return taskdispatch.WorkerRuntime{Cleaner: &cleaner}
}
