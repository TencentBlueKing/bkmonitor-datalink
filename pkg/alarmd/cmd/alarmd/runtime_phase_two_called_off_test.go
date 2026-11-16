// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A refresh round the process stops in the middle of is not a failed round:
// no failed snapshot_refreshed line, and no renewal on the context that was
// called off, which would only fail and be written as a failure in turn. The
// same source failing under a live context is a failed round, written and
// renewed after as before.
func TestARefreshRoundCalledOffIsNotAFailedRound(t *testing.T) {
	for _, tc := range []struct {
		name      string
		calledOff bool
		err       error
	}{
		{name: "called off", calledOff: true, err: context.Canceled},
		{name: "source failing under a live context", err: errors.New("source unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-current", PublicationEpoch: 2}
			reconciler := &fakeSourceReconciler{results: []controlplane.SourceRefreshResult{{}}, errs: []error{tc.err}}
			repository := &fakeProductionCatalogRepository{activation: controlplane.ActivationState{RecordRevision: 2, Current: publication},
				snapshot: controlplane.PublishedSnapshot{Publication: publication}}
			var failed []observability.Observation
			control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
				Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: reconciler,
				Activator: &fakeInitialScheduleActivator{state: repository.activation}, Repository: repository,
				Schedules: &fakeScheduleProjection{}, Progress: &fakeProductionProgressReader{},
				Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
					if observation.Result == observability.ResultFailed && observation.Stage == observability.StageSnapshotRefreshed {
						failed = append(failed, observation)
					}
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.calledOff {
				cancel()
			}
			_, _ = control.Refresh(ctx)
			if tc.calledOff {
				if len(failed) != 0 || repository.renewCalls != 0 {
					t.Fatalf("a round called off: failed lines %+v, renewals %d; want none", failed, repository.renewCalls)
				}
				return
			}
			if len(failed) != 1 || repository.renewCalls != 1 {
				t.Fatalf("a source failing under a live context: failed lines %+v, renewals %d; want one each", failed, repository.renewCalls)
			}
		})
	}
}

// A healthy round whose success mark fails is reported, unless the round was
// called off, when the mark failed only because nothing may run any more.
func TestASuccessMarkCalledOffIsNotAFailure(t *testing.T) {
	for _, calledOff := range []bool{true, false} {
		publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-current", PublicationEpoch: 2}
		reconciler := &fakeSourceReconciler{results: []controlplane.SourceRefreshResult{
			{Status: controlplane.SourceRefreshUnchanged, Observation: "observation-current", Publication: publication}}}
		repository := &fakeProductionCatalogRepository{activation: controlplane.ActivationState{RecordRevision: 2, Current: publication},
			snapshot: controlplane.PublishedSnapshot{Publication: publication,
				QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-healthy"}}},
			successMarkErr: errors.New("mark failed")}
		var failed []observability.Observation
		control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
			Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: reconciler,
			Activator: &fakeInitialScheduleActivator{state: repository.activation}, Repository: repository,
			Schedules: &fakeScheduleProjection{}, Progress: &fakeProductionProgressReader{},
			Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				if observation.Result == observability.ResultFailed && observation.Stage == observability.StageSnapshotRefreshed {
					failed = append(failed, observation)
				}
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		if calledOff {
			cancel()
		}
		_, _ = control.Refresh(ctx)
		cancel()
		if calledOff && len(failed) != 0 {
			t.Fatalf("a success mark called off was reported failed: %+v", failed)
		}
		if !calledOff && len(failed) != 1 {
			t.Fatalf("a success mark failing under a live context: failed lines %+v, want one", failed)
		}
	}
}

// The Leader's sweep and index write, the worker's index read, and the lease
// and ownership failure lines: each failing under a live context is reported,
// and each failing because its caller called it off is not.
func TestAnOwnershipStepCalledOffIsNotAFailure(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	groups := []execution.QueryGroupIdentity{"query-group-1"}
	failure := errors.New("store unavailable")
	for _, step := range []struct {
		name  string
		stage observability.Stage
		run   func(context.Context, *productionPhaseTwoOwnership, *rebalanceOwnershipStore, observability.Observer)
	}{
		{"sweep", observability.StageAssignmentSwept, func(ctx context.Context, production *productionPhaseTwoOwnership, store *rebalanceOwnershipStore, _ observability.Observer) {
			store.sweepErr = failure
			production.sweepRetiredAssignments(ctx, production.authority, groups)
		}},
		{"index write", observability.StageAssignmentIndexWritten, func(ctx context.Context, production *productionPhaseTwoOwnership, store *rebalanceOwnershipStore, _ observability.Observer) {
			store.indexErr = failure
			production.publishAssignmentIndex(ctx, production.authority, map[execution.QueryGroupIdentity]string{"query-group-1": "worker-1"}, store.workers, now)
		}},
		{"index read", observability.StageAssignmentIndexRead, func(ctx context.Context, production *productionPhaseTwoOwnership, store *rebalanceOwnershipStore, _ observability.Observer) {
			store.indexErr = failure
			_, _ = production.AssignedQueryGroups(ctx, groups)
		}},
		{"lease renewal", observability.StageLeaseRenewed, func(ctx context.Context, _ *productionPhaseTwoOwnership, _ *rebalanceOwnershipStore, observer observability.Observer) {
			observeProductionRenewalFailure(ctx, observer, observability.StageLeaseRenewed, failure)
		}},
		{"ownership transition", observability.StageAssignmentLost, func(ctx context.Context, _ *productionPhaseTwoOwnership, _ *rebalanceOwnershipStore, observer observability.Observer) {
			observeProductionOwnership(ctx, observer, observability.StageAssignmentLost, failure)
		}},
	} {
		for _, calledOff := range []bool{true, false} {
			store := newIndexStore(now, groups)
			production, observations := newIndexOwnershipHarness(t, now, store)
			if leader, err := production.TryAcquireControlLeader(context.Background(), now, time.Minute); err != nil || !leader {
				t.Fatalf("TryAcquireControlLeader() leader=%v error=%v", leader, err)
			}
			observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				*observations = append(*observations, observation)
			})
			*observations = nil
			ctx, cancel := context.WithCancel(context.Background())
			if calledOff {
				cancel()
			}
			step.run(ctx, production, store, observer)
			cancel()
			failed := 0
			for _, observation := range *observations {
				if observation.Stage == step.stage && observation.Result == observability.ResultFailed {
					failed++
				}
			}
			if calledOff && failed != 0 {
				t.Fatalf("%s called off was reported failed %d times", step.name, failed)
			}
			if !calledOff && failed == 0 {
				t.Fatalf("%s failing under a live context was not reported failed", step.name)
			}
		}
	}
}

// A deadline that passed is not a caller calling the work off: an ownership
// step that ran out of time failed, and is reported.
func TestACalledOffCheckDoesNotSwallowADeadline(t *testing.T) {
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second)) // fixed-wait: a deadline already past, not a wait
	defer stop()
	failed := 0
	observeProductionOwnership(expired, observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		if observation.Result == observability.ResultFailed {
			failed++
		}
	}), observability.StageAssignmentLost, context.DeadlineExceeded)
	if failed != 1 {
		t.Fatalf("an ownership step past its deadline was reported failed %d times, want 1", failed)
	}
}
