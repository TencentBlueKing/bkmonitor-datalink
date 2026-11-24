// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type hintCheckFunc func(context.Context, string, string) error

func (f hintCheckFunc) CheckHint(ctx context.Context, tenant, id string) error {
	return f(ctx, tenant, id)
}

type hintReadFunc func(context.Context, string, string, string, int) (store.ShieldAlertPage, error)

func (f hintReadFunc) ListShieldDependents(ctx context.Context, tenant, main, after string, limit int) (store.ShieldAlertPage, error) {
	return f(ctx, tenant, main, after, limit)
}

type hintListenFunc func(context.Context, func(redisstate.ShieldHint), func(string)) error

func (f hintListenFunc) ListenShieldHints(ctx context.Context, receive func(redisstate.ShieldHint), observe func(string)) error {
	return f(ctx, receive, observe)
}

func TestShieldHintQueueBoundCoalescingAndFairPagination(t *testing.T) {
	q := newShieldHintQueue()
	h := redisstate.ShieldHint{TenantID: "tenant", MainAlertID: "first"}
	if q.add(h) != "queued" {
		t.Fatal("enqueue failed")
	}
	item, ok := q.pop()
	if !ok || q.add(h) != "coalesced" {
		t.Fatal("inflight identity lost")
	}
	for i := range 63 {
		if q.add(redisstate.ShieldHint{TenantID: "tenant", MainAlertID: fmt.Sprint(i)}) != "queued" {
			t.Fatal(i)
		}
	}
	if q.add(redisstate.ShieldHint{TenantID: "other", MainAlertID: "first"}) != "dropped" {
		t.Fatal("capacity exceeded")
	}
	q.finish(item, "next-child")
	for range 63 {
		next, ok := q.pop()
		if !ok || next.hint == h {
			t.Fatal("full page starved queued mains")
		}
		q.finish(next, "")
	}
	next, ok := q.pop()
	if !ok || next.hint != h || next.after != "next-child" {
		t.Fatal(next)
	}
	q.finish(next, "")
	var wg sync.WaitGroup
	var queued atomic.Int64
	for range 32 {
		wg.Go(func() {
			if q.add(h) == "queued" {
				queued.Add(1)
			}
		})
	}
	wg.Wait()
	if queued.Load() != 1 {
		t.Fatal("duplicates not coalesced", queued.Load())
	}
	if q.add(redisstate.ShieldHint{}) != "invalid" {
		t.Fatal("invalid identity entered queue")
	}
}

func hintRepository(t *testing.T) *memory.Repository {
	t.Helper()
	repo := memory.New()
	for i := range 17 {
		a := storetest.Alert("tenant", fmt.Sprintf("child-%02d", i), "opening", fmt.Sprintf("fp-%d", i), "warning")
		b := storetest.ShieldBinding(a.CreateAt)
		b.Type = "rely_shield"
		b.Mode = "custom_shield"
		b.ActivationID = ""
		b.MainAlertID = "main"
		next := time.Now().Add(time.Hour)
		a.Shield = domain.AlertShield{Active: true, Bindings: []domain.ShieldBinding{b}, NextCheckAt: &next}
		if _, err := repo.CreateAlert(t.Context(), a); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func TestShieldHintChecksFutureBindingsInBoundedPagesAndRejectsForeignFacts(t *testing.T) {
	repo := hintRepository(t)
	item := shieldHintCursor{hint: redisstate.ShieldHint{TenantID: "tenant", MainAlertID: "main"}}
	var calls, active, peak atomic.Int64
	checker := hintCheckFunc(func(ctx context.Context, tenant, id string) error {
		if tenant != "tenant" {
			t.Error("foreign execution")
		}
		n := active.Add(1)
		defer active.Add(-1)
		calls.Add(1)
		for {
			old := peak.Load()
			if old >= n || peak.CompareAndSwap(old, n) {
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
		return nil
	})
	next, n, failed, err := checkShieldHintPage(t.Context(), repo, checker, item)
	if err != nil || n != 16 || failed != 0 || next != "child-15" || peak.Load() > 4 {
		t.Fatal(next, n, failed, err)
	}
	item.after = next
	next, n, failed, err = checkShieldHintPage(t.Context(), repo, checker, item)
	if err != nil || next != "" || n != 1 || failed != 0 || calls.Load() != 17 {
		t.Fatal("missing last child", next, n, failed, err)
	}
	page, _ := repo.ListShieldDependents(t.Context(), "tenant", "main", "", 16)
	page.Alerts[0].Alert.BKTenantID = "foreign"
	before := calls.Load()
	item.after = ""
	_, _, _, err = checkShieldHintPage(t.Context(), hintReadFunc(func(context.Context, string, string, string, int) (store.ShieldAlertPage, error) { return page, nil }), checker, item)
	if err == nil || calls.Load() != before {
		t.Fatal("untrusted page executed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, _, err = checkShieldHintPage(ctx, repo, checker, item); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored", err)
	}
}

func TestShieldHintRunnerRecoversQueueAndCancelsSubscription(t *testing.T) {
	repo := hintRepository(t)
	var calls atomic.Int64
	entered := make(chan struct{}, 17)
	published := make(chan struct{})
	listen := hintListenFunc(func(ctx context.Context, accept func(redisstate.ShieldHint), observe func(string)) error {
		observe("subscribed")
		accept(redisstate.ShieldHint{TenantID: "tenant", MainAlertID: "main"})
		accept(redisstate.ShieldHint{TenantID: "tenant", MainAlertID: "main"})
		close(published)
		<-ctx.Done()
		return ctx.Err()
	})
	checker := hintCheckFunc(func(context.Context, string, string) error {
		<-published
		calls.Add(1)
		entered <- struct{}{}
		return nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runShieldHints(ctx, listen, repo, checker, nil, taskCatalog(config.Config{}, 0), nil) }()
	for range 17 {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("pending hint page not resumed")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hint runner leaked")
	}
	if calls.Load() != 17 {
		t.Fatal("duplicate hint rechecked children", calls.Load())
	}
}

func TestShieldHintSubscriptionFailureDoesNotStopCheckerAndBackoffCancels(t *testing.T) {
	registry := taskCatalog(config.Config{Lifecycle: &config.LifecycleConfig{}}, 0)
	observed := make(chan struct{}, 1)
	listen := hintListenFunc(func(context.Context, func(redisstate.ShieldHint), func(string)) error {
		observed <- struct{}{}
		return errors.New("private connection detail")
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runShieldHints(ctx, listen, memory.New(), hintCheckFunc(func(context.Context, string, string) error { return nil }), nil, registry, nil)
	}()
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("subscription did not start")
	}
	select {
	case err := <-done:
		t.Fatal("optional subscription stopped control task", err)
	case <-time.After(10 * time.Millisecond):
	}
	failed := false
	for _, task := range registry.Snapshot().Tasks {
		if task.ID == "shield-hints" {
			for _, step := range task.Steps {
				failed = failed || step.ErrorCode == "hint_subscription_failed"
			}
		}
	}
	if !failed {
		t.Fatal("subscription failure not visible")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("reconnect backoff ignored cancellation")
	}
}
