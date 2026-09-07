// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package ownership

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestSessionRenewsAndRejectsWorkAfterLeaseLoss(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	store := &fakeLeaseStore{lease: Lease{
		Fence:    execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"},
		Deadline: now.Add(time.Minute),
	}}
	session, err := OpenSession(context.Background(), store, "query-group-1", "worker-1", now, time.Minute)
	if err != nil {
		t.Fatalf("OpenSession() error = %v", err)
	}
	if _, err := session.ValidateCurrent(context.Background(), now.Add(time.Second)); err != nil {
		t.Fatalf("ValidateCurrent() error = %v", err)
	}
	store.renewed = Lease{Fence: store.lease.Fence, Deadline: now.Add(90 * time.Second)}
	if err := session.Renew(context.Background(), now.Add(30*time.Second), time.Minute); err != nil {
		t.Fatalf("Renew() error = %v", err)
	}
	if got := session.Deadline(); !got.Equal(store.renewed.Deadline) {
		t.Fatalf("Deadline() = %s, want %s", got, store.renewed.Deadline)
	}

	store.renewErr = ErrStaleFence
	if err := session.Renew(context.Background(), now.Add(time.Minute), time.Minute); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("Renew(stale) error = %v, want ErrStaleFence", err)
	}
	if _, err := session.ValidateCurrent(context.Background(), now.Add(time.Minute)); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("ValidateCurrent(lost) error = %v, want ErrStaleFence", err)
	}
}

func TestSessionMaintainRunsRenewalIndependentlyAndStopsOnFailure(t *testing.T) {
	now := time.Now()
	store := &fakeLeaseStore{
		lease: Lease{
			Fence:    execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"},
			Deadline: now.Add(time.Minute),
		},
		renewErr: ErrStaleFence,
	}
	session, err := OpenSession(context.Background(), store, "query-group-1", "worker-1", now, time.Minute)
	if err != nil {
		t.Fatalf("OpenSession() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.Maintain(ctx, time.Millisecond, time.Minute); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("Maintain() error = %v, want ErrStaleFence", err)
	}
	if store.renewCalls == 0 {
		t.Fatal("Maintain() did not renew the lease")
	}
}

func TestSessionConditionallyReleasesAfterStoppingAdmission(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	store := &fakeLeaseStore{lease: Lease{
		Fence:    execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"},
		Deadline: now.Add(time.Minute),
	}}
	session, err := OpenSession(context.Background(), store, "query-group-1", "worker-1", now, time.Minute)
	if err != nil {
		t.Fatalf("OpenSession() error = %v", err)
	}
	session.stopAccepting()
	if err := session.Release(context.Background()); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if store.releaseCalls != 1 {
		t.Fatalf("Release() calls = %d, want 1", store.releaseCalls)
	}
	if err := session.Release(context.Background()); err != nil {
		t.Fatalf("Release(second) error = %v", err)
	}
	if store.releaseCalls != 1 {
		t.Fatalf("Release(second) calls = %d, want 1", store.releaseCalls)
	}
}

func TestSessionKeepsAcceptingAcrossTransientRenewFailureInsideTTL(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
	store := &fakeLeaseStore{lease: Lease{Fence: fence, Deadline: now.Add(time.Minute)}}
	session, err := OpenSession(context.Background(), store, "query-group-1", "worker-1", now, time.Minute)
	if err != nil {
		t.Fatalf("OpenSession() error = %v", err)
	}
	transient := errors.New("lease store unreachable")
	store.renewErr = transient
	if err := session.Renew(context.Background(), now.Add(10*time.Second), time.Minute); !errors.Is(err, transient) {
		t.Fatalf("Renew(transient) error = %v, want the store error", err)
	}
	if got, err := session.ValidateCurrent(context.Background(), now.Add(20*time.Second)); err != nil || got != fence {
		t.Fatalf("ValidateCurrent(after transient renew failure) fence=%+v error=%v, want the lease still accepted", got, err)
	}
	if got := session.Deadline(); !got.Equal(now.Add(time.Minute)) {
		t.Fatalf("Deadline() after transient failure = %s, want unchanged %s", got, now.Add(time.Minute))
	}

	store.renewErr = nil
	store.renewed = Lease{Fence: fence, Deadline: now.Add(90 * time.Second)}
	if err := session.Renew(context.Background(), now.Add(30*time.Second), time.Minute); err != nil {
		t.Fatalf("Renew(recovered) error = %v", err)
	}
	if got := session.Deadline(); !got.Equal(store.renewed.Deadline) {
		t.Fatalf("Deadline() after recovery = %s, want %s", got, store.renewed.Deadline)
	}

	store.renewErr = transient
	if err := session.Renew(context.Background(), now.Add(90*time.Second), time.Minute); !errors.Is(err, transient) {
		t.Fatalf("Renew(transient at deadline) error = %v, want the store error", err)
	}
	if _, err := session.ValidateCurrent(context.Background(), now.Add(90*time.Second)); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("ValidateCurrent(expired) error = %v, want ErrStaleFence", err)
	}
}

func TestSessionStopsAcceptingOnAuthoritativeRenewDecision(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	for _, decision := range []error{ErrStaleFence, ErrNotDesired} {
		store := &fakeLeaseStore{lease: Lease{
			Fence:    execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"},
			Deadline: now.Add(time.Minute),
		}}
		session, err := OpenSession(context.Background(), store, "query-group-1", "worker-1", now, time.Minute)
		if err != nil {
			t.Fatalf("OpenSession() error = %v", err)
		}
		store.renewErr = decision
		if err := session.Renew(context.Background(), now.Add(time.Second), time.Minute); !errors.Is(err, decision) {
			t.Fatalf("Renew(%v) error = %v", decision, err)
		}
		if _, err := session.ValidateCurrent(context.Background(), now.Add(2*time.Second)); !errors.Is(err, ErrStaleFence) {
			t.Fatalf("ValidateCurrent(after %v) error = %v, want ErrStaleFence", decision, err)
		}
	}
}

type fakeLeaseStore struct {
	lease        Lease
	renewed      Lease
	renewErr     error
	renewCalls   int
	releaseCalls int
	checkErr     error
}

func (store *fakeLeaseStore) Acquire(
	context.Context,
	execution.QueryGroupIdentity,
	string,
	time.Time,
	time.Duration,
) (Lease, error) {
	return store.lease, nil
}

func (store *fakeLeaseStore) Renew(
	context.Context,
	execution.OwnerFence,
	time.Time,
	time.Duration,
) (Lease, error) {
	store.renewCalls++
	if store.renewErr != nil {
		return Lease{}, store.renewErr
	}
	if store.renewed.Fence.QueryGroup == "" {
		return store.lease, nil
	}
	return store.renewed, nil
}

func (store *fakeLeaseStore) CheckFence(context.Context, execution.OwnerFence, time.Time) error {
	return store.checkErr
}

func (store *fakeLeaseStore) Release(context.Context, execution.OwnerFence) error {
	store.releaseCalls++
	return nil
}
