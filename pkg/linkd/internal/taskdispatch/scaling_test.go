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

func TestOneWorkerSchedulesThousandSourcesWithoutStaticReservation(t *testing.T) {
	s, base, now := fixture()
	worker := s.Workers["0"]
	worker.MaxTasks = 0
	s.Workers = map[string]Worker{"0": worker}
	releases := make([]eventsource.Release, 1000)
	for i := range releases {
		r := base
		r.ID = fmt.Sprintf("source-%04d", i)
		r.Spec = base.Spec.WithDefaults()
		r.Spec.EventSourceID = r.ID
		r.Spec.Storage.Kafka.Topic = fmt.Sprintf("topic-%04d", i)
		s.Metadata[r.ID] = UpdateMetadata(Metadata{}, r.Spec, r.Spec.Storage.Kafka.Topic, 1, nil, now)
		releases[i] = r
	}
	Reconcile(&s, releases, now)
	if len(s.Tasks) != 2000 || count(s, "cleaner") != 1000 || count(s, "lifecycle") != 1000 {
		t.Fatal("unexpected static limit", len(s.Tasks))
	}
}

func TestRetiredHistoryDoesNotPreventRestart(t *testing.T) {
	s, rel, now := fixture()
	Reconcile(&s, []eventsource.Release{rel}, now)
	var id string
	for k, task := range s.Tasks {
		if task.Role == "lifecycle" {
			id = k
			break
		}
	}
	task := s.Tasks[id]
	oldEpoch := task.Epoch
	task.Phase = "stopped"
	task.Retired = make([]string, 1200)
	for i := range task.Retired {
		task.Retired[i] = fmt.Sprintf("old-%d", i)
	}
	s.Tasks[id] = task
	Reconcile(&s, []eventsource.Release{rel}, now)
	if s.Tasks[id].Epoch <= oldEpoch || len(s.Tasks[id].Retired) != 1200 {
		t.Fatal("restart lost recovery identities", s.Tasks[id].Epoch)
	}
}

func TestSlowReleaseLoadingDoesNotBlockHeartbeat(t *testing.T) {
	runtime := workerRuntime(config.Config{}, []string{"lifecycle"})
	var beats atomic.Int32
	second := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/releases/") {
			<-r.Context().Done()
			return
		}
		if r.URL.Path != "/internal/heartbeat" {
			http.NotFound(w, r)
			return
		}
		var h Heartbeat
		if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
			t.Error(err)
			return
		}
		if beats.Add(1) == 2 {
			second <- struct{}{}
		}
		tasks := make([]Task, 40)
		for i := range tasks {
			tasks[i] = Task{ID: fmt.Sprint("s", i), Source: fmt.Sprint("s", i), Role: "lifecycle", Version: 1, Epoch: int64(i + 1), Phase: "preparing", RemainingMillis: 60000, Concurrency: runtime.Lifecycle.Concurrency, InflightBytes: runtime.Lifecycle.InflightBytes}
		}
		writeAgentTestResponse(t, w, tasks)
	}))
	defer server.Close()
	agent := Agent{Runtime: runtime, Roles: []string{"lifecycle"}, Config: config.DispatchConfig{URL: server.URL}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), RunTask: func(context.Context, Task, config.EventSource) error { return nil }}
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx) }()
	select {
	case <-second:
	case <-time.After(HeartbeatInterval + 2*time.Second):
		cancel()
		t.Fatal("configuration loads blocked heartbeat")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("preparation did not cancel")
	}
}
