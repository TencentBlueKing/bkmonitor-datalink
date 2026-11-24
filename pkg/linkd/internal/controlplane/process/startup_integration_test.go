// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplaneprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/controlplane/taskstate"
	"linkd/internal/internaltoken"
	"linkd/internal/telemetry"
)

// 使用专属 Redis 地址；测试只清理本次唯一 deployment 的两个调度 key，ES 全部由本地 HTTP fixture 提供。
func TestControlPlaneStorageOutageKeepsAPIAndScheduling(t *testing.T) {
	address := os.Getenv("LINKD_TEST_DISPATCH_REDIS")
	if address == "" {
		t.Skip("set LINKD_TEST_DISPATCH_REDIS for explicit isolated Redis integration")
	}
	for _, fresh := range []bool{false, true} {
		t.Run(fmt.Sprintf("fresh=%t", fresh), func(t *testing.T) { testControlPlaneStorageOutage(t, address, fresh) })
	}
}

func testControlPlaneStorageOutage(t *testing.T, address string, fresh bool) {
	var failReads atomic.Bool
	var collectionsReady atomic.Bool
	collectionsReady.Store(!fresh)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if fresh && r.Method == http.MethodPut && (strings.HasPrefix(r.URL.Path, "/linkd_event_source_") || strings.Contains(r.URL.Path, "-policy-")) {
			if strings.HasSuffix(r.URL.Path, "_records") {
				collectionsReady.Store(true)
			}
			_, _ = io.WriteString(w, `{"acknowledged":true}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/_search") && !collectionsReady.Load() {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"type":"index_not_found_exception"}}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/_search") && !failReads.Load() {
			_, _ = io.WriteString(w, `{"_shards":{"failed":0},"hits":{"hits":[]}}`)
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"type":"cluster_block_exception","reason":"disk usage exceeded flood-stage watermark"}}`)
	}))
	defer backend.Close()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	deployment := fmt.Sprintf("cp-startup-%d", time.Now().UnixNano())
	cfg := config.Config{
		Storage:  &config.StorageConfig{Repository: config.RepositoryTypeElasticsearch, Elasticsearch: &config.ElasticsearchConfig{Addresses: []string{backend.URL}, IndexPrefix: "cp-startup-test"}, Redis: &config.RedisConfig{Address: address}},
		Dispatch: config.DispatchConfig{Deployment: deployment, Listen: listen, URL: "http://" + listen, JWT: config.JWTConfig{SecretKey: "test-internal-secret"}, WorkerToken: "test-worker-token"},
	}
	rt, err := telemetry.Start(t.Context(), config.TelemetryConfig{}, telemetry.RoleControlPlane, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Shutdown(context.Background()) }()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, slog.New(slog.NewTextHandler(testWriter{t}, nil)), rt) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("control plane did not stop")
		}
		client := redis.NewClient(&redis.Options{Addr: address})
		defer func() { _ = client.Close() }()
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := client.Del(cleanup, "linkd:dispatch:{"+deployment+"}:state", "linkd:dispatch:{"+deployment+"}:leader").Err(); err != nil {
			t.Error(err)
		}
	}()
	signer, err := internaltoken.New(cfg.Dispatch.JWT.SecretKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Sign("test-caller")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second}
	wait := func(ready func(map[string]taskstate.Task) bool) map[string]taskstate.Task {
		t.Helper()
		until := time.Now().Add(8 * time.Second)
		for time.Now().Before(until) {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, cfg.Dispatch.URL+"/api/v1/control-plane/tasks", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Internal-Token", token)
			response, err := client.Do(request)
			if err == nil {
				var snapshot taskstate.Snapshot
				err = json.NewDecoder(response.Body).Decode(&snapshot)
				_ = response.Body.Close()
				if err == nil && response.StatusCode == http.StatusOK {
					tasks := map[string]taskstate.Task{}
					for _, task := range snapshot.Tasks {
						tasks[task.ID] = task
					}
					if ready(tasks) {
						return tasks
					}
				}
			}
			select {
			case err := <-done:
				t.Fatalf("control plane exited during storage fault: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
		}
		t.Fatal("API or scheduler did not reach expected state")
		return nil
	}
	initial := wait(func(tasks map[string]taskstate.Task) bool {
		configurationObserved := tasks["control-storage"].Execution.Failed > 0
		if fresh {
			configurationObserved = tasks["control-storage"].Execution.Succeeded > 0
		}
		return tasks["scheduler"].Execution.Succeeded > 0 && configurationObserved && tasks["elasticsearch-schema-and-active-reconciler"].Execution.Failed > 0
	})
	failReads.Store(true)
	wait(func(tasks map[string]taskstate.Task) bool { return tasks["scheduler"].Execution.Failed > 0 })
	failReads.Store(false)
	wait(func(tasks map[string]taskstate.Task) bool {
		return tasks["scheduler"].Execution.Succeeded > initial["scheduler"].Execution.Succeeded && tasks["scheduler"].Execution.ErrorCode == ""
	})
}
