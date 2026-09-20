// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

type countingAssignmentStore struct {
	workers   []ownership.WorkerRegistration
	listCalls int
	listErr   error
	records   map[execution.QueryGroupIdentity]ownership.AssignmentRecord
	published int
}

func (store *countingAssignmentStore) ListReadyWorkers(context.Context, time.Time) ([]ownership.WorkerRegistration, error) {
	store.listCalls++
	if store.listErr != nil {
		return nil, store.listErr
	}
	return append([]ownership.WorkerRegistration(nil), store.workers...), nil
}

func (store *countingAssignmentStore) ReadAssignment(_ context.Context, queryGroup execution.QueryGroupIdentity) (ownership.AssignmentRecord, error) {
	record, ok := store.records[queryGroup]
	if !ok {
		return ownership.AssignmentRecord{}, ownership.ErrAssignmentAbsent
	}
	return record, nil
}

func (store *countingAssignmentStore) PublishAssignment(
	_ context.Context,
	_ ownership.PublicationAuthority,
	decision ownership.AssignmentDecision,
) (ownership.AssignmentRecord, error) {
	store.published++
	record := ownership.AssignmentRecord{
		QueryGroup: decision.QueryGroup, DesiredWorkerID: decision.DesiredWorkerID, AssignmentGeneration: 1,
		RecordRevision: 1, ControlEpoch: 1, PlacementReason: decision.PlacementReason, AssignedAt: decision.DecidedAt,
	}
	store.records[decision.QueryGroup] = record
	return record, nil
}

// A round lists the ready set once and settles every Query Group against
// it: ReconcileWith never lists, decides over the set it was given even
// when the registry has moved on, and Reconcile lists exactly once for the
// single Query Group it settles. A listing that fails fails the round with
// nothing published.
func TestReconcilerRoundListsTheReadySetOnceAndDecidesOverIt(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	worker := func(id string) ownership.WorkerRegistration {
		return rebalanceWorker(id, ownership.WorkerReady, now.Add(time.Minute), "shadow")
	}
	store := &countingAssignmentStore{
		workers: []ownership.WorkerRegistration{worker("a"), worker("b")},
		records: map[execution.QueryGroupIdentity]ownership.AssignmentRecord{},
	}
	reconciler, err := NewReconciler(NewRouter(nil), store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, authority := context.Background(), ownership.PublicationAuthority{}
	workers, err := reconciler.ListReadyWorkers(ctx, now)
	if err != nil || len(workers) != 2 || store.listCalls != 1 {
		t.Fatalf("ListReadyWorkers() = %d workers, error %v, %d listings", len(workers), err, store.listCalls)
	}
	for _, queryGroup := range []execution.QueryGroupIdentity{"query-group-1", "query-group-2", "query-group-3"} {
		if _, err := reconciler.ReconcileWith(ctx, authority, queryGroup, workers, now); err != nil {
			t.Fatalf("ReconcileWith(%s) error = %v", queryGroup, err)
		}
	}
	if store.listCalls != 1 || store.published != 3 {
		t.Fatalf("after a round of three: %d listings and %d publications, want 1 and 3", store.listCalls, store.published)
	}
	// The registry now only knows a; the round still decides over the set
	// it listed, which is how every Query Group of one round sees the same
	// world.
	store.workers = []ownership.WorkerRegistration{worker("a")}
	record, err := reconciler.ReconcileWith(ctx, authority, "query-group-4", []ownership.WorkerRegistration{worker("b")}, now)
	if err != nil || record.DesiredWorkerID != "b" || store.listCalls != 1 {
		t.Fatalf("ReconcileWith over the round's set = %+v, error %v, %d listings; want b without listing", record, err, store.listCalls)
	}
	if _, err := reconciler.ReconcileWith(ctx, authority, "query-group-5", nil, now); !errors.Is(err, ErrNoEligibleWorker) || store.listCalls != 1 {
		t.Fatalf("ReconcileWith over an empty set error = %v with %d listings, want no eligible worker without listing", err, store.listCalls)
	}
	if record, err := reconciler.Reconcile(ctx, authority, "query-group-6", now); err != nil || record.DesiredWorkerID != "a" || store.listCalls != 2 {
		t.Fatalf("Reconcile() = %+v, error %v, %d listings; want a with exactly one more listing", record, err, store.listCalls)
	}
	store.listErr = errors.New("registry unavailable")
	published := store.published
	if _, err := reconciler.ListReadyWorkers(ctx, now); !errors.Is(err, store.listErr) {
		t.Fatalf("ListReadyWorkers() error = %v, want the registry failure", err)
	}
	if _, err := reconciler.Reconcile(ctx, authority, "query-group-7", now); !errors.Is(err, store.listErr) || store.published != published {
		t.Fatalf("Reconcile() after a failed listing error = %v, published %d; want the failure and no publication", err, store.published-published)
	}
	var nilReconciler *Reconciler
	if _, err := nilReconciler.ListReadyWorkers(ctx, now); err == nil {
		t.Fatal("nil reconciler listed workers")
	}
	if _, err := nilReconciler.ReconcileWith(ctx, authority, "query-group-8", workers, now); err == nil {
		t.Fatal("nil reconciler reconciled")
	}
}
