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
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/eventsource"
)

func TestAgentReportsIdleTasks(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	runtime := workerRuntime(config.Config{}, []string{"cleaner", "lifecycle"})
	reports := make(chan Heartbeat, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/releases/") {
			writeAgentTestResponse(t, w, eventsource.Release{ID: "source", Version: 1, Spec: config.EventSource{EventSourceID: "source"}})
			return
		}
		var h Heartbeat
		if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
			t.Error(err)
			http.Error(w, "bad heartbeat", http.StatusBadRequest)
			return
		}
		select {
		case reports <- h:
		default:
		}
		var tasks []Task
		for i, role := range []string{"cleaner", "lifecycle"} {
			budget, err := runtime.budgetFor(role, config.EventSource{})
			if err != nil {
				t.Error(err)
				return
			}
			tasks = append(tasks, Task{ID: taskID("source", role, 0), Source: "source", Role: role, Epoch: int64(i + 1), Version: 1, Phase: "starting", RemainingMillis: LeaseTTL.Milliseconds(), Concurrency: budget.Concurrency, InflightBytes: budget.InflightBytes})
		}
		writeAgentTestResponse(t, w, tasks)
	}))
	defer server.Close()
	a := Agent{Config: config.DispatchConfig{URL: server.URL}, Runtime: runtime, Roles: []string{"cleaner", "lifecycle"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), RunTask: func(ctx context.Context, _ Task, _ config.EventSource) error {
		// 没有事件输入，任务仅等待取消，仍必须持续出现在调度心跳中。
		<-ctx.Done()
		return ctx.Err()
	}}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("idle agent did not stop")
		}
	})
	counts := map[string]int{}
	for counts["cleaner"] < 2 || counts["lifecycle"] < 2 {
		select {
		case h := <-reports:
			for _, r := range h.Reports {
				if r.Phase == "running" {
					counts[strings.Split(r.ID, ":")[1]]++
				}
			}
		case <-ctx.Done():
			t.Fatalf("idle tasks did not report repeated heartbeats: %v", counts)
		}
	}
}

func TestTaskHeartbeatIntegration(t *testing.T) {
	address := os.Getenv("LINKD_TEST_DISPATCH_REDIS")
	if address == "" {
		t.Skip("set LINKD_TEST_DISPATCH_REDIS for explicit real Redis integration")
	}
	ctx := t.Context()
	client := redis.NewClient(&redis.Options{Addr: address})
	defer func() { _ = client.Close() }()
	c, err := NewController(ctx, client, eventsource.New(emptySources{}, config.SeverityConfig{}), "test-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Del(context.Background(), c.key, c.leader)
	for _, role := range []string{"cleaner", "lifecycle"} {
		for _, tc := range []struct {
			name, phase, reportPhase string
			epoch                    int64
			foreign, omitted, replay bool
			updated                  bool
		}{
			{name: "idle running", phase: "running", reportPhase: "running", epoch: 7, updated: true},
			{name: "preparing", phase: "preparing", reportPhase: "prepared", epoch: 7, updated: true},
			{name: "draining", phase: "stopping", reportPhase: "stopping", epoch: 7, updated: true},
			{name: "missing report", phase: "running", omitted: true},
			{name: "old epoch", phase: "running", reportPhase: "running", epoch: 6},
			{name: "other worker", phase: "running", reportPhase: "running", epoch: 7, foreign: true},
			{name: "replayed sequence", phase: "running", reportPhase: "running", epoch: 7, replay: true},
			{name: "stop report", phase: "running", reportPhase: "stopped", epoch: 7},
			{name: "stopped task", phase: "stopped", reportPhase: "running", epoch: 7},
		} {
			t.Run(role+"/"+tc.name, func(t *testing.T) {
				old := time.Now().Add(-time.Minute).UTC()
				worker := Worker{ID: "worker", Roles: []string{role}, Runtime: workerRuntime(config.Config{}, []string{role}), Seq: 1, Seen: time.Now()}
				task := Task{ID: taskID("source", role, 0), Source: "source", Role: role, Worker: worker.ID, Epoch: 7, Phase: tc.phase, HeartbeatAt: old, Expires: time.Now().Add(LeaseTTL)}
				if err := c.update(ctx, func(s *State) error {
					*s = newState()
					s.Workers[worker.ID], s.Tasks[task.ID] = worker, task
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if !tc.replay {
					worker.Seq++
				}
				if tc.foreign {
					worker.ID = "other-worker"
				}
				h := Heartbeat{Worker: worker}
				if !tc.omitted {
					h.Reports = []Report{{ID: task.ID, Epoch: tc.epoch, Phase: tc.reportPhase}}
				}
				_, err := c.Beat(ctx, h)
				if (err != nil) != tc.replay {
					t.Fatalf("heartbeat error = %v", err)
				}
				state, err := c.Snapshot(ctx)
				if err != nil {
					t.Fatal(err)
				}
				got := state.Tasks[task.ID].HeartbeatAt
				if tc.updated && !got.After(old) || !tc.updated && !got.Equal(old) {
					t.Fatalf("heartbeat_at = %v, previous = %v, should update = %v", got, old, tc.updated)
				}
			})
		}
	}
}

func TestNewAssignmentStartsWithoutHeartbeat(t *testing.T) {
	state, release, now := fixture()
	Reconcile(&state, []eventsource.Release{release}, now)
	for id, task := range state.Tasks {
		task.Phase = "stopped"
		task.HeartbeatAt = now
		state.Tasks[id] = task
	}
	Reconcile(&state, []eventsource.Release{release}, now)
	for _, task := range state.Tasks {
		if task.Phase != "preparing" || !task.HeartbeatAt.IsZero() {
			t.Fatalf("new assignment inherited a heartbeat: %+v", task)
		}
		encoded, err := json.Marshal(task)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "heartbeat_at") {
			t.Fatal("assignment fabricated a heartbeat before its first report")
		}
	}
}
