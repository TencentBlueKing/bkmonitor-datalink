// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/domain"
	"linkd/internal/store"
)

type wakeTestObserver struct{ calls atomic.Int64 }

func (o *wakeTestObserver) DeliveryReceived(context.Context, consume.DeliveryObservation) {}

func (o *wakeTestObserver) HandlerStarted(context.Context, consume.Message) {}

func (o *wakeTestObserver) HandlerFinished(context.Context, consume.OutcomeKind, time.Duration) {}

func (o *wakeTestObserver) RetryScheduled(context.Context) {}

func (o *wakeTestObserver) StepFinished(context.Context, consume.StepObservation) {}

func (o *wakeTestObserver) SettlementFinished(context.Context, consume.SettlementObservation) {}

func (o *wakeTestObserver) FlowTransition(context.Context, string) {}

func (o *wakeTestObserver) OwnershipChanged(context.Context, consume.OwnershipObservation) {}

func (o *wakeTestObserver) ShutdownFinished(context.Context, bool, time.Duration, int) {}

func (o *wakeTestObserver) Snapshot(context.Context, consume.RuntimeSnapshot) { o.calls.Add(1) }

type wakeTestWriter struct {
	started chan struct{}
	block   bool
}

func (w *wakeTestWriter) CreateEvents(ctx context.Context, events []domain.Event) ([]store.CreateEventItemResult, error) {
	if w.started != nil {
		close(w.started)
	}
	if w.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	select {
	case <-time.After(200 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return (&runtimeTestWriter{}).CreateEvents(ctx, events)
}

func TestRuntimeSlowBatchesDoNotSpin(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(map[int]string{1: "executing", 2: "waiting_for_slot"}[count], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			values := []struct{ lane, id string }{{"p", "a"}, {"q", "b"}}
			s := newRuntimeTestSession(cancel, values[:count]...)
			s.cancelAfter = count
			o := &wakeTestObserver{}
			r, err := NewRuntime(config.CleanerRuntimeConfig{WorkerCount: 2, MaxBatchMessages: count, MaxConcurrentBatches: 1}, s, runtimeTestProcessor{}, &wakeTestWriter{}, &runtimeTestMailbox{byLane: map[string][]string{}}, allowReceiveGate{}, slog.New(slog.NewTextHandler(io.Discard, nil)), o)
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Run(ctx); err != nil {
				t.Fatal(err)
			}
			if len(s.confirmed) != count {
				t.Fatalf("confirmed %d, want %d", len(s.confirmed), count)
			}
			t.Logf("events=%d snapshots=%d", count, o.calls.Load())
			if o.calls.Load() > 100 {
				t.Fatalf("busy loop: %d snapshots for %d events", o.calls.Load(), count)
			}
		})
	}
}

func TestRuntimeNextWakeOnlySchedulesActionableFutureDeadlines(t *testing.T) {
	r := &Runtime{config: (config.CleanerRuntimeConfig{BatchWaitMilliseconds: 20}).WithDefaults()}
	now := time.Now()
	future := now.Add(10 * time.Millisecond)
	for _, tc := range []struct {
		name            string
		lane            *cleanerLane
		force, canBatch bool
		want            time.Time
	}{
		{"future batch", &cleanerLane{entries: []*cleanerEntry{{readyAt: now.Add(-10 * time.Millisecond)}}}, false, true, future},
		{"expired batch", &cleanerLane{entries: []*cleanerEntry{{readyAt: now.Add(-time.Second)}}}, false, true, time.Time{}},
		{"executing batch", &cleanerLane{batching: true, entries: []*cleanerEntry{{readyAt: now}}}, false, true, time.Time{}},
		{"no slot", &cleanerLane{entries: []*cleanerEntry{{readyAt: now}}}, false, false, time.Time{}},
		{"batch retry", &cleanerLane{retryAt: future}, false, true, future},
		{"expired batch retry", &cleanerLane{retryAt: now.Add(-time.Second)}, false, true, time.Time{}},
		{"retry without slot", &cleanerLane{retryAt: future}, false, false, time.Time{}},
		{"processing retry independent of batch slots", &cleanerLane{batching: true, entries: []*cleanerEntry{{processRetryAt: future}}}, false, false, future},
		{"force flush", &cleanerLane{entries: []*cleanerEntry{{readyAt: now}}}, true, true, time.Time{}},
		{"revoke flush", &cleanerLane{revoking: true, entries: []*cleanerEntry{{readyAt: now}}}, false, true, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.nextWake(map[string]*cleanerLane{"p": tc.lane}, now, tc.force, tc.canBatch); !got.Equal(tc.want) {
				t.Fatalf("wake=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestRuntimeReleasesBatchSlotBeforeCompletionNotification(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := &Runtime{config: (config.CleanerRuntimeConfig{MaxBatchMessages: 1}).WithDefaults(), events: &runtimeTestWriter{}, mailboxes: &runtimeTestMailbox{byLane: map[string][]string{}}}
	// 确认函数需要真实 receipt；复用测试 session，但不触发取消。
	s := newRuntimeTestSession(cancel, struct{ lane, id string }{"p", "a"})
	r.session = s
	s.cancelAfter = 2
	lane := &cleanerLane{name: "p", entries: []*cleanerEntry{{readyAt: time.Now(), delivery: s.deliveries[0], event: domain.Event{EventID: "a"}}}}
	slots := make(chan struct{}, 1)
	results := make(chan laneBatchResult)
	var wg sync.WaitGroup
	r.maybeStartBatch(ctx, lane, time.Now(), true, slots, results, &wg)
	defer func() { cancel(); wg.Wait() }()
	deadline := time.Now().Add(time.Second)
	for len(slots) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(slots) != 0 {
		t.Fatal("slot held while completion notification is blocked")
	}
	select {
	case result := <-results:
		if result.err != nil {
			t.Fatal(result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("no completion")
	}
}

func TestRuntimeShutdownDeadlineWakesBlockedBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	s := newRuntimeTestSession(cancel, struct{ lane, id string }{"p", "a"})
	r, err := NewRuntime(config.CleanerRuntimeConfig{MaxBatchMessages: 1, ShutdownDrainTimeoutSeconds: 1}, s, runtimeTestProcessor{}, &wakeTestWriter{started: started, block: true}, &runtimeTestMailbox{byLane: map[string][]string{}}, allowReceiveGate{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("batch did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown error=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown deadline did not wake runtime")
	}
}

type wakeOwnershipSession struct {
	*runtimeTestSession
	changes chan consume.OwnershipEvent
}

func (s *wakeOwnershipSession) OwnershipEvents() <-chan consume.OwnershipEvent { return s.changes }

func (*wakeOwnershipSession) AllowOwnershipChanges() {}

func TestRuntimeRevokeDeadlineWakesBlockedBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	s := &wakeOwnershipSession{runtimeTestSession: newRuntimeTestSession(cancel, struct{ lane, id string }{"p", "a"}), changes: make(chan consume.OwnershipEvent, 1)}
	r, err := NewRuntime(config.CleanerRuntimeConfig{MaxBatchMessages: 1, ShutdownDrainTimeoutSeconds: 1}, s, runtimeTestProcessor{}, &wakeTestWriter{started: started, block: true}, &runtimeTestMailbox{byLane: map[string][]string{}}, allowReceiveGate{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("batch did not start")
	}
	completed := make(chan struct{})
	s.changes <- consume.OwnershipEvent{Kind: consume.OwnershipRevoked, Lanes: []string{"p"}, Complete: func() { close(completed) }}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("revoke error=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("revoke deadline did not wake runtime")
	}
	select {
	case <-completed:
	default:
		t.Fatal("revoke completion was not released")
	}
}
