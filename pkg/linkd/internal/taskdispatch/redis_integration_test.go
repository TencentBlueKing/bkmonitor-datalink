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
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/eventsource"
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

func TestRedisCoordinatorIntegration(t *testing.T) {
	address := os.Getenv("LINKD_TEST_DISPATCH_REDIS")
	if address == "" {
		t.Skip("set LINKD_TEST_DISPATCH_REDIS for explicit real Redis integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: address})
	defer func() { _ = client.Close() }()
	observer := &transitionRecorder{}
	c, e := NewController(ctx, client, eventsource.New(emptySources{}, config.SeverityConfig{}), "test-"+uuid.NewString(), observer)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Del(context.Background(), c.key, c.leader)
	s, rel, now := fixture()
	if e = c.update(ctx, func(state *State) error { *state = s; Reconcile(state, []eventsource.Release{rel}, now); return nil }); e != nil {
		t.Fatal(e)
	}
	snapshot, e := c.Snapshot(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var task Task
	for _, x := range snapshot.Tasks {
		if x.Role == "cleaner" {
			task = x
			break
		}
	}
	worker := s.Workers[task.Worker]
	worker.Seq = 1
	tasks, e := c.Beat(ctx, Heartbeat{Worker: worker, Reports: []Report{{ID: task.ID, Epoch: task.Epoch, Phase: "prepared"}}})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, x := range tasks {
		if x.ID == task.ID {
			found = x.Phase == "starting"
		}
	}
	if !found {
		t.Fatal("start authorization missing")
	}
	changed := worker
	changed.Seq++
	changed.MaxConcurrency++
	if _, err := c.Beat(ctx, Heartbeat{Worker: changed}); err == nil {
		t.Fatal("changed session budget accepted")
	}
	changed = worker
	changed.Seq++
	changed.Runtime = WorkerRuntime{}
	if _, err := c.Beat(ctx, Heartbeat{Worker: changed}); err == nil {
		t.Fatal("missing session budget accepted")
	}
	observed := observer.count.Load()
	if _, e = c.Beat(ctx, Heartbeat{Worker: worker}); e == nil {
		t.Fatal("replayed heartbeat renewed lease")
	}
	if observer.count.Load() != observed {
		t.Fatal("failed heartbeat counted a transition")
	}
	worker.Seq++
	_, e = c.Beat(ctx, Heartbeat{Worker: worker, Reports: []Report{{ID: task.ID, Epoch: task.Epoch, Phase: "stopped"}}})
	if e != nil {
		t.Fatal(e)
	}
	snapshot, _ = c.Snapshot(ctx)
	if snapshot.Tasks[task.ID].Phase != "stopped" {
		t.Fatal("stop not committed")
	}
	observed = observer.count.Load()
	worker.Seq++
	_, e = c.Beat(ctx, Heartbeat{Worker: worker, Reports: []Report{{ID: task.ID, Epoch: task.Epoch, Phase: "running"}}})
	if e != nil {
		t.Fatal(e)
	}
	snapshot, _ = c.Snapshot(ctx)
	if snapshot.Tasks[task.ID].Phase != "stopped" {
		t.Fatal("late running revived task")
	}
	if observer.count.Load() != observed {
		t.Fatal("late report counted a duplicate transition")
	}
	if e = client.Del(ctx, c.key).Err(); e != nil {
		t.Fatal(e)
	}
	if e = c.update(ctx, func(*State) error { return nil }); e == nil {
		t.Fatal("lost coordination recreated automatically")
	}
}

// transitionRecorder 继承 no-op 端口，仅统计已提交转换。
type transitionRecorder struct {
	noopObserver
	count   atomic.Int64
	expired atomic.Int64
}

func (r *transitionRecorder) Transition(_ context.Context, _, _, _, _, reason string, _ time.Duration) {
	r.count.Add(1)
	if reason == "authorization_expired" {
		r.expired.Add(1)
	}
}

func TestRedisObserverTimeoutReassignmentIntegration(t *testing.T) {
	address := os.Getenv("LINKD_TEST_DISPATCH_REDIS")
	if address == "" {
		t.Skip("set LINKD_TEST_DISPATCH_REDIS for explicit Redis integration")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: address})
	defer func() { _ = client.Close() }()
	observer := &transitionRecorder{}
	c, err := NewController(ctx, client, eventsource.New(emptySources{}, config.SeverityConfig{}), "observe-"+uuid.NewString(), observer)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Del(context.Background(), c.key, c.leader)
	state, release, now := fixture()
	Reconcile(&state, []eventsource.Release{release}, now)
	var old Task
	for _, task := range state.Tasks {
		if task.Role == "cleaner" {
			old = task
			break
		}
	}
	old.Phase = "stopping"
	old.Expires = now.Add(-SafetyMargin - time.Second)
	state.Tasks[old.ID] = old
	worker := state.Workers[old.Worker]
	worker.Seen = now.Add(-time.Minute)
	state.Workers[old.Worker] = worker
	if err := c.update(ctx, func(s *State) error { *s = state; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := c.update(ctx, func(s *State) error {
		Reconcile(s, []eventsource.Release{release}, time.Now())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Tasks[old.ID].Epoch <= old.Epoch || observer.expired.Load() != 1 {
		t.Fatalf("timeout transition lost: task=%+v expired=%d", snapshot.Tasks[old.ID], observer.expired.Load())
	}
	if err := c.update(ctx, func(*State) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if observer.expired.Load() != 1 {
		t.Fatal("unchanged snapshot counted timeout twice")
	}
}
