// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package producer

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"linkd/internal/actiondelivery"
	"linkd/internal/domain"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

func TestActionLeaseBusyKeepsMixedErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		busy bool
	}{
		{"nil", nil, false},
		{"busy", scheduler.ErrLockBusy, true},
		{"wrapped busy", fmt.Errorf("lock: %w", scheduler.ErrLockBusy), true},
		{"joined busy", errors.Join(scheduler.ErrLockBusy, scheduler.ErrLockBusy), true},
		{"mixed cancellation", errors.Join(scheduler.ErrLockBusy, context.Canceled), false},
		{"wrapped mixed infrastructure", fmt.Errorf("lock: %w", errors.Join(scheduler.ErrLockBusy, errors.New("backend failed"))), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := actionLeaseBusy(tc.err); got != tc.busy {
				t.Fatal(got, tc.busy)
			}
		})
	}
}

type actionReadFunc func(context.Context, string, string) (store.StoredAlert, error)

func (f actionReadFunc) GetAlertCurrent(c context.Context, t, id string) (store.StoredAlert, error) {
	return f(c, t, id)
}

type actionFinishFunc func(context.Context, string, string) (store.StoredAlert, error)

func (f actionFinishFunc) FinishActionDelivery(c context.Context, t, id string) (store.StoredAlert, error) {
	return f(c, t, id)
}

type actionLease struct {
	busy         bool
	key          string
	released     bool
	releaseError error
	onAcquire    func()
}

func (l *actionLease) Acquire(_ context.Context, key string) (scheduler.Lease, error) {
	l.key = key
	if l.busy {
		return scheduler.Lease{}, scheduler.ErrLockBusy
	}
	if l.onAcquire != nil {
		l.onAcquire()
	}
	return scheduler.Lease{}, nil
}

func (*actionLease) Renew(context.Context, scheduler.Lease) error { return nil }

func (l *actionLease) Release(ctx context.Context, _ scheduler.Lease) error {
	l.released = true
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return l.releaseError
}

func actionProducerFixture(t *testing.T) (*Producer, *memory.Repository, *actionLease) {
	t.Helper()
	repo := memory.New()
	a := storetest.Alert("tenant", "alert", "opening", "fp", "warning")
	at := a.UpdateAt
	a.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: a.Severity, CauseType: "source_event", CauseID: a.LatestEventID}
	a.Projection.Targets = map[string]domain.ProjectionTargetState{"kac": {ActionEnabled: true, SourceVersion: 1, RequiredRevision: 1}}
	var err error
	a.ActionPending, err = domain.NewAlertActionIntent(a, "source_event", a.LatestEventID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateAlert(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	locker := &actionLease{}
	p := &Producer{alerts: actionReadFunc(repo.GetAlert), slots: make(chan struct{}, 4), locker: func(source string) (scheduler.Locker, error) {
		if source != a.EventSourceID {
			t.Error("wrong source lease")
		}
		return locker, nil
	}}
	p.finisher = actionFinishFunc(func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
		row, e := repo.GetAlert(ctx, tenant, id)
		if e != nil {
			return row, e
		}
		next := row.Alert.Clone()
		next.ActionPending = nil
		return repo.CompareAndSetAlert(ctx, tenant, id, row.Version, next)
	})
	return p, repo, locker
}

func TestProducerUsesFingerprintLeaseAndRereadsScope(t *testing.T) {
	p, repo, locker := actionProducerFixture(t)
	result, err := p.FinishActionDelivery(t.Context(), "tenant", "alert")
	if err != nil || result.Alert.ActionPending != nil || result.Alert.Revision != 1 || !locker.released || locker.key != scheduler.CorrelationKey("tenant", result.Alert.EventSourceID, "fp") {
		t.Fatal("wrong lease or enqueue confirmation", err)
	}
	if _, err = p.FinishActionDelivery(t.Context(), "other", "alert"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("cross tenant read", err)
	}
	p.alerts = actionReadFunc(func(ctx context.Context, _, _ string) (store.StoredAlert, error) {
		return repo.GetAlert(ctx, "tenant", "alert")
	})
	if _, err = p.FinishActionDelivery(t.Context(), "other", "alert"); !errors.Is(err, actiondelivery.ErrInvalid) {
		t.Fatal("foreign reader accepted", err)
	}
}

func TestProducerStopsWhenLockedIdentityChanges(t *testing.T) {
	p, repo, locker := actionProducerFixture(t)
	reads := 0
	finished := false
	p.alerts = actionReadFunc(func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
		row, e := repo.GetAlert(ctx, tenant, id)
		reads++
		if reads == 2 {
			row.Alert.Fingerprint = "other"
		}
		return row, e
	})
	p.finisher = actionFinishFunc(func(context.Context, string, string) (store.StoredAlert, error) {
		finished = true
		return store.StoredAlert{}, nil
	})
	if _, err := p.FinishActionDelivery(t.Context(), "tenant", "alert"); !errors.Is(err, actiondelivery.ErrInvalid) || finished || !locker.released {
		t.Fatal("stale lease identity used", err)
	}
}

func TestProducerBusyCancellationAndReleaseFailure(t *testing.T) {
	p, _, locker := actionProducerFixture(t)
	locker.busy = true
	if _, err := p.FinishActionDelivery(t.Context(), "tenant", "alert"); !actiondelivery.CanDefer(err) || locker.released {
		t.Fatal("busy lease not deferred", err)
	}
	locker.busy = false
	locker.releaseError = errors.New("injected release failure")
	p.finisher = actionFinishFunc(func(context.Context, string, string) (store.StoredAlert, error) {
		return store.StoredAlert{}, actiondelivery.ErrBusy
	})
	if _, err := p.FinishActionDelivery(t.Context(), "tenant", "alert"); err == nil || actiondelivery.CanDefer(err) || !locker.released {
		t.Fatal("mixed release failure swallowed", err)
	}
	locker.releaseError = nil
	locker.released = false
	entered := make(chan struct{})
	p.finisher = actionFinishFunc(func(ctx context.Context, _, _ string) (store.StoredAlert, error) {
		close(entered)
		<-ctx.Done()
		return store.StoredAlert{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := p.FinishActionDelivery(ctx, "tenant", "alert"); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("producer did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel ignored")
	}
	if !locker.released || len(p.slots) != 0 {
		t.Fatal("cancel retained lease or slot")
	}
}
