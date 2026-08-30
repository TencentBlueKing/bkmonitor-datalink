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

type fakeAssignmentStore struct {
	workers            []ownership.WorkerRegistration
	publishedWorker    string
	publishedAuthority ownership.PublicationAuthority
	expectedRevision   uint64
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
	return ownership.AssignmentRecord{
		QueryGroup: decision.QueryGroup, DesiredWorkerID: decision.DesiredWorkerID, AssignmentGeneration: 1, RecordRevision: 1,
		ControlEpoch: authority.Fence.OwnerEpoch, PlacementReason: decision.PlacementReason, AssignedAt: decision.DecidedAt,
	}, nil
}

func (store *fakeAssignmentStore) ReadAssignment(
	context.Context,
	execution.QueryGroupIdentity,
) (ownership.AssignmentRecord, error) {
	return ownership.AssignmentRecord{}, ownership.ErrAssignmentAbsent
}
