// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kaccompat

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

type readerFunc func(context.Context, string, []string) (store.AlertBatch, error)

func (f readerFunc) GetAlertsCurrent(ctx context.Context, t string, ids []string) (store.AlertBatch, error) {
	return f(ctx, t, ids)
}

type senderFunc func(context.Context, ActionRequest) error

func (f senderFunc) Send(ctx context.Context, q ActionRequest) error { return f(ctx, q) }

type writerStub struct {
	write   func(context.Context, []domain.Alert, bool) []WriteResult
	refresh func(context.Context, []string) error
}

func (w writerStub) WriteBatch(ctx context.Context, a []domain.Alert, refresh bool) []WriteResult {
	return w.write(ctx, a, refresh)
}

func (w writerStub) Refresh(ctx context.Context, indices []string) error {
	if w.refresh != nil {
		return w.refresh(ctx, indices)
	}
	return nil
}

func testReader(_ context.Context, tenant string, ids []string) (store.AlertBatch, error) {
	out := store.AlertBatch{}
	for _, id := range ids {
		out.Alerts = append(out.Alerts, store.StoredAlert{Alert: storetest.Alert(tenant, id, "opening", id, "warning")})
	}
	return out, nil
}

func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out")
	}
}

func TestProjectionShutdownFinishesStartedBatchAndLeavesQueued(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	writer := writerStub{write: func(ctx context.Context, alerts []domain.Alert, _ bool) []WriteResult {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			t.Error("shutdown canceled started projection")
		}
		return []WriteResult{{Index: "alarm-000001"}}
	}}
	runtime := NewRuntime(config.KACDeliveryConfig{ProjectionConcurrency: 1, BatchSize: 1}, nil, "stream", readerFunc(testReader), writer, nil, nil, nil)
	stop, cancel := context.WithCancel(t.Context())
	defer cancel()
	lane := &runtime.lanes[0]
	go func() { runtime.runLane(stop, lane); close(done) }()
	lane.items <- queuedChange{"1-0", domain.AlertChange{BKTenantID: "tenant", AlertID: "a"}}
	waitSignal(t, entered)
	lane.items <- queuedChange{"2-0", domain.AlertChange{BKTenantID: "tenant", AlertID: "b"}}
	cancel()
	select {
	case <-done:
		t.Fatal("exited before current projection finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	waitSignal(t, done)
	if len(lane.items) != 1 || len(runtime.completed) != 1 || <-runtime.completed != "1-0" {
		t.Fatal("unstarted message acknowledged")
	}
}

func TestActionShutdownWaitsForStartedHTTP(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	runtime := NewRuntime(config.KACDeliveryConfig{}, nil, "stream", nil, nil, senderFunc(func(ctx context.Context, _ ActionRequest) error {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			t.Error("shutdown canceled started action")
		}
		return nil
	}), nil, nil)
	stop, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { runtime.runAction(stop); close(done) }()
	runtime.ready <- actionJob{entry: queuedChange{id: "1-0"}}
	waitSignal(t, entered)
	runtime.ready <- actionJob{entry: queuedChange{id: "2-0"}}
	cancel()
	select {
	case <-done:
		t.Fatal("exited before HTTP finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	waitSignal(t, done)
	if len(runtime.ready) != 1 || len(runtime.completed) != 1 || <-runtime.completed != "1-0" {
		t.Fatal("unstarted action acknowledged")
	}
}

func TestProjectionCoalescesReadsAndKeepsIndependentActions(t *testing.T) {
	reads, writes := 0, 0
	runtime := NewRuntime(config.KACDeliveryConfig{}, nil, "stream", readerFunc(func(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
		reads += len(ids)
		return testReader(ctx, tenant, ids)
	}), writerStub{write: func(_ context.Context, a []domain.Alert, _ bool) []WriteResult {
		writes += len(a)
		return []WriteResult{{Index: "alarm-1"}}
	}}, nil, nil, nil)
	entries := []queuedChange{{"1-0", domain.AlertChange{BKTenantID: "tenant", AlertID: "a", Action: &domain.AlertAction{ID: "action-1"}}}, {"2-0", domain.AlertChange{BKTenantID: "tenant", AlertID: "a", Action: &domain.AlertAction{ID: "action-2"}}}}
	runtime.process(t.Context(), entries)
	if reads != 1 || writes != 1 || len(runtime.actions) != 2 || len(runtime.completed) != 0 {
		t.Fatal("projection duplicated or action lost", reads, writes)
	}
}

func TestProjectionFailureAndFullActionBufferEndMessages(t *testing.T) {
	runtime := NewRuntime(config.KACDeliveryConfig{ActionBuffer: 1}, nil, "stream", readerFunc(testReader), writerStub{write: func(_ context.Context, a []domain.Alert, _ bool) []WriteResult {
		return []WriteResult{{Err: errors.New("write failed")}, {Index: "alarm-1"}, {Index: "alarm-1"}}
	}}, nil, nil, nil)
	entries := []queuedChange{}
	for _, id := range []string{"a", "b", "c"} {
		entries = append(entries, queuedChange{id, domain.AlertChange{BKTenantID: "tenant", AlertID: id, Action: &domain.AlertAction{ID: id}}})
	}
	runtime.process(t.Context(), entries)
	if len(runtime.completed) != 2 || len(runtime.actions) != 1 {
		t.Fatal("failed item retained or buffer blocked")
	}
}

func TestProjectionReadAndWritePanicDoesNotStopFollowingMessages(t *testing.T) {
	for _, readPanic := range []bool{true, false} {
		t.Run(fmt.Sprint(readPanic), func(t *testing.T) {
			failed := true
			runtime := NewRuntime(config.KACDeliveryConfig{}, nil, "stream", readerFunc(func(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
				if failed && readPanic {
					panic("private alert payload")
				}
				return testReader(ctx, tenant, ids)
			}), writerStub{write: func(context.Context, []domain.Alert, bool) []WriteResult {
				if failed {
					panic("private projection payload")
				}
				return []WriteResult{{Index: "alarm"}}
			}}, nil, nil, nil)
			runtime.process(t.Context(), []queuedChange{{"1-0", domain.AlertChange{BKTenantID: "tenant", AlertID: "a"}}})
			failed = false
			runtime.process(t.Context(), []queuedChange{{"2-0", domain.AlertChange{BKTenantID: "tenant", AlertID: "b"}}})
			if len(runtime.completed) != 2 {
				t.Fatal("projection panic blocked later messages")
			}
		})
	}
}

func TestActionPanicDoesNotStopFollowingActions(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	runtime := NewRuntime(config.KACDeliveryConfig{}, nil, "stream", nil, nil, senderFunc(func(context.Context, ActionRequest) error {
		calls++
		if calls == 1 {
			panic("private action payload")
		}
		cancel()
		return nil
	}), nil, nil)
	done := make(chan struct{})
	go func() { defer close(done); runtime.runAction(ctx) }()
	runtime.ready <- actionJob{entry: queuedChange{id: "1-0"}}
	runtime.ready <- actionJob{entry: queuedChange{id: "2-0"}}
	waitSignal(t, done)
	if calls != 2 || len(runtime.completed) != 2 {
		t.Fatal("action panic stopped worker", calls, len(runtime.completed))
	}
}

func TestRefreshPanicDoesNotStopFollowingBatches(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	runtime := NewRuntime(config.KACDeliveryConfig{BatchSize: 1}, nil, "stream", nil, writerStub{refresh: func(context.Context, []string) error {
		calls++
		if calls == 1 {
			panic("private refresh payload")
		}
		return nil
	}}, nil, nil, nil)
	done := make(chan struct{})
	go func() { defer close(done); runtime.runRefresh(ctx) }()
	runtime.actions <- actionJob{entry: queuedChange{id: "1-0"}}
	select {
	case <-runtime.completed:
	case <-time.After(3 * time.Second):
		t.Fatal("failed refresh did not settle")
	}
	runtime.actions <- actionJob{entry: queuedChange{id: "2-0"}}
	select {
	case <-runtime.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh panic blocked next batch")
	}
	cancel()
	waitSignal(t, done)
}

func TestSynchronousCloseUsesSameLaneAndLatestState(t *testing.T) {
	var mu sync.Mutex
	state := domain.AlertStatusActive
	calls := []bool{}
	entered, release, closed, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1), make(chan struct{})
	runtime := NewRuntime(config.KACDeliveryConfig{ProjectionConcurrency: 1, BatchSize: 1}, nil, "stream", readerFunc(func(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
		batch, err := testReader(ctx, tenant, ids)
		mu.Lock()
		defer mu.Unlock()
		for i := range batch.Alerts {
			batch.Alerts[i].Alert.Status = state
		}
		return batch, err
	}), writerStub{write: func(ctx context.Context, alerts []domain.Alert, refresh bool) []WriteResult {
		mu.Lock()
		calls = append(calls, refresh)
		n := len(calls)
		mu.Unlock()
		if n == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		if n > 1 && alerts[0].Status != domain.AlertStatusClosed {
			t.Error("stale active state after close")
		}
		return []WriteResult{{Index: "alarm-1"}}
	}}, nil, nil, nil)
	stop, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { runtime.runLane(stop, &runtime.lanes[0]); close(finished) }()
	runtime.lanes[0].items <- queuedChange{"1-0", domain.AlertChange{BKTenantID: "tenant", AlertID: "a"}}
	waitSignal(t, entered)
	mu.Lock()
	state = domain.AlertStatusClosed
	mu.Unlock()
	go func() { closed <- runtime.ProjectNow(t.Context(), "tenant", "a") }()
	deadline := time.After(time.Second)
	for runtime.lanes[0].urgent.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("close did not enter lane")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(release)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	cancel()
	waitSignal(t, finished)
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || calls[0] || !calls[1] {
		t.Fatal("close did not synchronously refresh", calls)
	}
}
