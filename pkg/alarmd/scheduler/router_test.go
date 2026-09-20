// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

func TestRouterUsesStableReadyWorkerMembership(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	workers := []ownership.WorkerRegistration{
		{WorkerID: "worker-2", AssignmentReadiness: ownership.WorkerDraining, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "standard", CapabilitiesDigest: "cap-v1", ExpiresAt: now.Add(time.Minute)},
		{WorkerID: "worker-1", AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyDegraded,
			DeploymentProfile: "standard", CapabilitiesDigest: "cap-v1", ExpiresAt: now.Add(time.Minute)},
		{WorkerID: "worker-expired", AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "standard", CapabilitiesDigest: "cap-v1", ExpiresAt: now.Add(-time.Second)},
	}
	router := NewRouter(nil)
	selected, err := router.Select("query-group-1", workers, now)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selected.WorkerID != "worker-1" {
		t.Fatalf("Select() worker = %q, want worker-1", selected.WorkerID)
	}
}

func TestRouterAppliesAdditionalEligibilityWithoutChangingG1MembershipRules(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	workers := []ownership.WorkerRegistration{
		{WorkerID: "worker-standard", AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "standard", CapabilitiesDigest: "cap-v1", ExpiresAt: now.Add(time.Minute)},
		{WorkerID: "worker-large", AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "large", CapabilitiesDigest: "cap-v1", ExpiresAt: now.Add(time.Minute)},
	}
	router := NewRouter(profileEligibility("large"))
	selected, err := router.Select("query-group-1", workers, now)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selected.WorkerID != "worker-large" {
		t.Fatalf("Select() worker = %q, want worker-large", selected.WorkerID)
	}
}

func TestRouterUsesStaticProfileAndCapabilityBeforeStableRendezvous(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	required := ownership.WorkerCompatibility{DeploymentProfile: "standard", CapabilitiesDigest: "cap-v2"}
	eligibility, err := NewStaticWorkerEligibility(required)
	if err != nil {
		t.Fatalf("NewStaticWorkerEligibility() error = %v", err)
	}
	eligible := []ownership.WorkerRegistration{
		{WorkerID: "worker-a", AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyDegraded,
			DeploymentProfile: "standard", CapabilitiesDigest: "cap-v2", ExpiresAt: now.Add(time.Minute)},
		{WorkerID: "worker-b", AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "standard", CapabilitiesDigest: "cap-v2", ExpiresAt: now.Add(time.Minute)},
	}
	incompatible := []ownership.WorkerRegistration{
		{WorkerID: "worker-wrong-profile", AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "large", CapabilitiesDigest: "cap-v2", ExpiresAt: now.Add(time.Minute)},
		{WorkerID: "worker-wrong-capability", AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "standard", CapabilitiesDigest: "cap-v1", ExpiresAt: now.Add(time.Minute)},
	}
	router := NewRouter(eligibility)
	want, err := router.Select("query-group-1", eligible, now)
	if err != nil {
		t.Fatalf("Select(eligible) error = %v", err)
	}
	for _, workers := range [][]ownership.WorkerRegistration{
		{incompatible[0], eligible[1], incompatible[1], eligible[0]},
		{eligible[0], incompatible[1], eligible[1], incompatible[0]},
	} {
		got, selectErr := router.Select("query-group-1", workers, now)
		if selectErr != nil {
			t.Fatalf("Select(mixed) error = %v", selectErr)
		}
		if got.WorkerID != want.WorkerID {
			t.Fatalf("Select(mixed) worker = %q, want stable %q", got.WorkerID, want.WorkerID)
		}
	}
	if _, err := router.Select("query-group-1", incompatible, now); err != ErrNoEligibleWorker {
		t.Fatalf("Select(incompatible) error = %v, want ErrNoEligibleWorker", err)
	}
}

func TestStaticWorkerEligibilityRejectsIncompleteRequirement(t *testing.T) {
	for _, required := range []ownership.WorkerCompatibility{
		{CapabilitiesDigest: "cap-v1"},
		{DeploymentProfile: "standard"},
	} {
		if _, err := NewStaticWorkerEligibility(required); err == nil {
			t.Fatalf("NewStaticWorkerEligibility(%#v) succeeded", required)
		}
	}
}

func TestReconcilerPublishesRouterDecisionWithControlAuthority(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	authority := ownership.PublicationAuthority{
		Fence: execution.OwnerFence{
			QueryGroup: ownership.ControlLeaderIdentity, OwnerID: "control-1", OwnerEpoch: 3, LeaseToken: "leader-token",
		},
		Deadline: now.Add(time.Minute),
	}
	store := &fakeAssignmentStore{workers: []ownership.WorkerRegistration{{
		WorkerID: "worker-1", AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
		DeploymentProfile: "standard", CapabilitiesDigest: "cap-v1", ExpiresAt: now.Add(time.Minute),
	}}}
	reconciler, err := NewReconciler(NewRouter(nil), store)
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	record, err := reconciler.Reconcile(context.Background(), authority, "query-group-1", now)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if record.DesiredWorkerID != "worker-1" || store.publishedWorker != "worker-1" || store.publishedAuthority != authority || store.expectedRevision != 0 {
		t.Fatalf("published record=%+v worker=%q authority=%+v", record, store.publishedWorker, store.publishedAuthority)
	}
}

func TestReconcilerKeepsReadyIncumbentWithoutRerunningRendezvous(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	authority := ownership.PublicationAuthority{
		Fence: execution.OwnerFence{
			QueryGroup: ownership.ControlLeaderIdentity, OwnerID: "control-1", OwnerEpoch: 3, LeaseToken: "leader-token",
		},
		Deadline: now.Add(time.Minute),
	}
	workers := []ownership.WorkerRegistration{readyWorker("worker-1", now), readyWorker("worker-2", now)}
	router := NewRouter(nil)
	rendezvous, err := router.Select("query-group-1", workers, now)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	// The incumbent is the Worker Rendezvous would not pick, so a kept
	// Assignment proves the incumbent wins over a fresh Rendezvous pass.
	incumbent := "worker-1"
	if rendezvous.WorkerID == incumbent {
		incumbent = "worker-2"
	}
	current := ownership.AssignmentRecord{
		QueryGroup: "query-group-1", DesiredWorkerID: incumbent, AssignmentGeneration: 2, RecordRevision: 4,
		ControlEpoch: 2, PlacementReason: ownership.PlacementRendezvous, AssignedAt: now.Add(-time.Minute),
	}
	store := &fakeAssignmentStore{workers: workers, current: current}
	reconciler, err := NewReconciler(router, store)
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	record, err := reconciler.Reconcile(context.Background(), authority, "query-group-1", now)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if record != current || store.publishCalls != 0 {
		t.Fatalf("Reconcile(ready incumbent) record=%+v publishes=%d, want the current record and no publish", record, store.publishCalls)
	}
}

func TestReconcilerRerunsRendezvousWhenIncumbentIsNotReady(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	authority := ownership.PublicationAuthority{
		Fence: execution.OwnerFence{
			QueryGroup: ownership.ControlLeaderIdentity, OwnerID: "control-1", OwnerEpoch: 3, LeaseToken: "leader-token",
		},
		Deadline: now.Add(time.Minute),
	}
	current := ownership.AssignmentRecord{
		QueryGroup: "query-group-1", DesiredWorkerID: "worker-1", AssignmentGeneration: 2, RecordRevision: 4,
		ControlEpoch: 2, PlacementReason: ownership.PlacementRendezvous, AssignedAt: now.Add(-time.Minute),
	}
	expired := readyWorker("worker-1", now)
	expired.ExpiresAt = now.Add(-time.Second)
	draining := readyWorker("worker-1", now)
	draining.AssignmentReadiness = ownership.WorkerDraining
	ineligible := readyWorker("worker-1", now)
	ineligible.DeploymentProfile = "standard"
	survivor := readyWorker("worker-2", now)
	survivor.DeploymentProfile = "large"
	for _, test := range []struct {
		name        string
		workers     []ownership.WorkerRegistration
		eligibility WorkerEligibility
	}{
		{name: "absent", workers: []ownership.WorkerRegistration{readyWorker("worker-2", now)}},
		{name: "expired", workers: []ownership.WorkerRegistration{expired, readyWorker("worker-2", now)}},
		{name: "draining", workers: []ownership.WorkerRegistration{draining, readyWorker("worker-2", now)}},
		{name: "ineligible", workers: []ownership.WorkerRegistration{ineligible, survivor}, eligibility: profileEligibility("large")},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeAssignmentStore{workers: test.workers, current: current}
			reconciler, err := NewReconciler(NewRouter(test.eligibility), store)
			if err != nil {
				t.Fatalf("NewReconciler() error = %v", err)
			}
			record, err := reconciler.Reconcile(context.Background(), authority, "query-group-1", now)
			if err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			if record.DesiredWorkerID != "worker-2" || store.publishedWorker != "worker-2" ||
				store.expectedRevision != current.RecordRevision || store.publishCalls != 1 {
				t.Fatalf("Reconcile(%s incumbent) record=%+v published=%q revision=%d publishes=%d, want worker-2 at revision %d",
					test.name, record, store.publishedWorker, store.expectedRevision, store.publishCalls, current.RecordRevision)
			}
		})
	}
}

func readyWorker(workerID string, now time.Time) ownership.WorkerRegistration {
	return ownership.WorkerRegistration{
		WorkerID: workerID, AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
		DeploymentProfile: "standard", CapabilitiesDigest: "cap-v1", ExpiresAt: now.Add(time.Minute),
	}
}

type fakeAssignmentStore struct {
	workers            []ownership.WorkerRegistration
	current            ownership.AssignmentRecord
	publishedWorker    string
	publishedAuthority ownership.PublicationAuthority
	expectedRevision   uint64
	publishCalls       int
}

type profileEligibility string

func (profile profileEligibility) Eligible(
	_ execution.QueryGroupIdentity,
	worker ownership.WorkerRegistration,
	_ time.Time,
) bool {
	return worker.DeploymentProfile == string(profile)
}

func (store *fakeAssignmentStore) ListReadyWorkers(context.Context, time.Time) ([]ownership.WorkerRegistration, error) {
	return append([]ownership.WorkerRegistration(nil), store.workers...), nil
}

func (store *fakeAssignmentStore) PublishAssignment(
	_ context.Context,
	authority ownership.PublicationAuthority,
	decision ownership.AssignmentDecision,
) (ownership.AssignmentRecord, error) {
	store.publishedAuthority = authority
	store.publishedWorker = decision.DesiredWorkerID
	store.expectedRevision = decision.ExpectedRecordRevision
	store.publishCalls++
	return ownership.AssignmentRecord{
		QueryGroup: decision.QueryGroup, DesiredWorkerID: decision.DesiredWorkerID, AssignmentGeneration: 1, RecordRevision: 1,
		ControlEpoch: authority.Fence.OwnerEpoch, PlacementReason: decision.PlacementReason, AssignedAt: decision.DecidedAt,
	}, nil
}

func (store *fakeAssignmentStore) ReadAssignment(
	context.Context,
	execution.QueryGroupIdentity,
) (ownership.AssignmentRecord, error) {
	if store.current.QueryGroup == "" {
		return ownership.AssignmentRecord{}, ownership.ErrAssignmentAbsent
	}
	return store.current, nil
}
