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
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// skipCatalog is one Segment of one compiled Plan, frozen at any Slot of
// its grid from the one fact it was built with: the timeline the production
// slot source walks.
type skipCatalog struct {
	t        *testing.T
	schedule execution.FrozenQueryGroupSchedule
	fact     execution.FrozenSlotContractFact
}

func (catalog skipCatalog) ReadInitialFrozenSchedule(context.Context, execution.QueryGroupIdentity) (execution.FrozenQueryGroupSchedule, error) {
	return catalog.schedule, nil
}

func (catalog skipCatalog) ReadFrozenSchedule(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.FrozenQueryGroupSchedule, error) {
	return catalog.schedule, nil
}

func (catalog skipCatalog) ReadSuccessorFrozenSchedule(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.FrozenQueryGroupSchedule, error) {
	return execution.FrozenQueryGroupSchedule{}, errors.New("one Segment only")
}

func (catalog skipCatalog) ReadScheduleRetirement(context.Context, execution.QueryGroupIdentity) (execution.EvaluationTime, bool, error) {
	return 0, false, nil
}

func (catalog skipCatalog) NextSlotAfter(_ context.Context, _ execution.QueryGroupIdentity, at execution.EvaluationTime) (execution.EvaluationTime, error) {
	if next, ok := catalog.schedule.NextSlotAfter(at); ok {
		return next, nil
	}
	return 0, controlplane.ErrScheduleUnavailable
}

func (catalog skipCatalog) ReadSegmentRepair(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.SegmentRepair, bool, error) {
	return execution.SegmentRepair{}, false, nil
}

func (catalog skipCatalog) FreezeSlotContract(_ context.Context, request execution.FreezeSlotContractRequest) (execution.FrozenSlotContractFact, error) {
	shift := (int64(request.EvaluationTime) - int64(catalog.fact.Contract.Slot.EvaluationTime)) * 1000
	fact := catalog.fact
	fact.DuePlans = append([]execution.DuePlan(nil), fact.DuePlans...)
	for index := range fact.DuePlans {
		fact.DuePlans[index].CompletionDeadlineUnixMilli += shift
	}
	fact.Requirements = append([]execution.DataRequirement(nil), fact.Requirements...)
	for index := range fact.Requirements {
		consumers := append([]execution.DataRequirementConsumer(nil), fact.Requirements[index].Consumers...)
		for consumer := range consumers {
			consumers[consumer].ConsumerDeadlineUnixMilli += shift
		}
		fact.Requirements[index].Consumers = consumers
	}
	digest, err := execution.DeriveDuePlanSetDigest(fact.DuePlans, fact.Requirements)
	if err != nil {
		catalog.t.Fatal(err)
	}
	fact.Contract.Slot.EvaluationTime, fact.Contract.DuePlanSetDigest, fact.Contract.ReadHoldMillis = request.EvaluationTime, digest, request.ReadHoldMillis
	return fact, nil
}

type skipProgress struct {
	queryGroup     execution.QueryGroupIdentity
	next, lastFull execution.EvaluationTime
}

func (progress skipProgress) LoadProgress(context.Context, execution.ProgressIdentity) (execution.ProgressLoadResult, error) {
	return execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: progress.queryGroup}, NextSlot: progress.next,
		LastFullSlot: progress.lastFull, LastCompletionKind: execution.CompletionFull,
	}}, nil
}

// expiredOnlyExecutor finalises what the source gave up on as the worker
// does -- SNAPSHOT_UNAVAILABLE once past its recovery bound, GAP_SKIPPED
// before it (ResolveFinalization), a range by its proof -- and refuses
// anything else: a Slot that reached it unexpired would be a different test.
type expiredOnlyExecutor struct {
	now      func() time.Time
	requests []execution.SlotExecutionRequest
}

func (executor *expiredOnlyExecutor) Execute(_ context.Context, request execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	executor.requests = append(executor.requests, request)
	if !request.ReplayExpired && request.ExpiredRange == nil {
		return execution.SlotExecutionResult{}, errors.New("an unexpired Slot reached the executor")
	}
	kind, reason := execution.CompletionGapSkipped, execution.ReasonCode(contract.ReasonGapSkipped)
	switch {
	case request.ExpiredRange != nil:
		kind, reason = request.ExpiredRange.CompletionKind(), request.ExpiredRange.CompletionReason()
	case executor.now().UnixMilli() >= request.RecoveryUntilUnixMilli:
		kind, reason = execution.CompletionSnapshotUnavailable, execution.ReasonCode(contract.ReasonSnapshotUnavailable)
	}
	return execution.SlotExecutionResult{Completed: true, CompletionKind: kind, Result: observability.ResultDegraded,
		ReasonCode: reason, QueryFreeCause: execution.CauseExpiredReplay}, nil
}

// The production wiring reports a replay given up on: OpenQueryGroup gives
// the Runner the process observer and the lookback reading, so the Slot the
// production slot source expires writes its slot_skipped line with what the
// lookback holds of the group at that moment.
func TestAReplayExpiredThroughTheProductionRunnerWritesItsSlotSkippedLine(t *testing.T) {
	// A compiled threshold strategy, three-digit id, on a sixty-second grid.
	fact, schedule := productionFrozenFactFromCatalog(t, productionG4Catalog(t, 500, strategy.DetectorKindThreshold, "usage", "system.cpu",
		[]string{"host"}, [][]map[string]any{{{"method": "gte", "threshold": 80}}}))
	queryGroup, first := fact.Contract.Slot.QueryGroup, fact.Contract.Slot.EvaluationTime
	// Past the replay age: a Slot due before this owner took the group over
	// is replayed past the distance rule, never past its age.
	now := time.Unix(int64(first)+12*60, 0)
	limits := scheduler.RecoveryLimits{
		ProcessQueryPermits: 2, RecoveryQueryPermits: 1, ReadyQueueCapacity: 8, RecoveryQueueCapacity: 8,
		MaxQueuedItemsPerQG: 2, MaxReplaySlots: 3, MaxReplayAge: 10 * time.Minute,
		RetryMinDelay: time.Second, RetryMaxDelay: 8 * time.Second,
	}
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(limits, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	catalog := skipCatalog{t: t, schedule: schedule, fact: fact}
	store := &fakePhaseTwoOwnershipStore{now: now, renewed: make(chan struct{})}
	eligibility, err := scheduler.NewStaticWorkerEligibility(ownership.WorkerCompatibility{DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities"})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := scheduler.NewReconciler(scheduler.NewRouter(eligibility), store)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var skipped []observability.Observation
	busy := &busyGroup{}
	executor := &expiredOnlyExecutor{now: func() time.Time { return now }}
	production, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		Store: store, WorkerID: "worker-1", Catalog: catalog, Progress: skipProgress{queryGroup: queryGroup, next: first, lastFull: first - 60},
		Executor: executor, Now: func() time.Time { return now }, ControlLeaderTTL: time.Minute,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			if observation.Stage == observability.StageSlotSkipped {
				mu.Lock()
				skipped = append(skipped, observation)
				mu.Unlock()
			}
		}),
		Reconcile: reconciler, Flights: flights, RecoveryLimits: limits, LookbackBusy: lookbackBusyOf(busy),
		PostRecoveryTerminalDelay: time.Minute, QueryDeadlineReserve: 5 * time.Second, SnapshotRetention: time.Hour,
		PublicationDelayAllowance: time.Minute, SettlingWait: 30 * time.Second, LeaseTTL: 30 * time.Second,
		ReconcileInterval: 5 * time.Second, ContentScopes: noContentScopes,
	})
	if err != nil {
		t.Fatalf("newProductionPhaseTwoOwnership() error = %v", err)
	}
	if err := production.RegisterWorker(context.Background(), ownership.WorkerRegistration{
		WorkerID: "worker-1", AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
		DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities", ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if leader, err := production.TryAcquireControlLeader(context.Background(), now, time.Minute); err != nil || !leader {
		t.Fatalf("TryAcquireControlLeader() = %v %v", leader, err)
	}
	if err := production.PublishAssignments(context.Background(), []execution.QueryGroupIdentity{queryGroup}, now); err != nil {
		t.Fatal(err)
	}
	runner, err := production.OpenQueryGroup(context.Background(), queryGroup, now, time.Minute)
	if err != nil {
		t.Fatalf("OpenQueryGroup() error = %v", err)
	}
	if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
		t.Fatalf("RunOne() = %t %v, requests %+v", attempted, err, executor.requests)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(skipped) != 1 {
		t.Fatalf("slot_skipped observations = %d, want one; requests %+v", len(skipped), executor.requests)
	}
	facts := skipped[0].SlotSkipped
	want := observability.LookbackBusyFacts{Capturing: true, SamplePending: true, ProbePending: true, ProbationRereads: 2,
		DirectedPending: 3, ReadsInFlight: 4, TrialPrepared: true}
	if facts == nil || facts.Lookback == nil || *facts.Lookback != want || busy.asked != queryGroup {
		t.Fatalf("slot_skipped facts %+v, lookback read for %q; want the lookback reading of %q", facts, busy.asked, queryGroup)
	}
	// The source finalises the run of Slots past their age as one range, and
	// an age-expired range is SNAPSHOT_UNAVAILABLE: the line says so in its
	// reason as well as its cause, and covers the range's span.
	if len(executor.requests) != 1 || executor.requests[0].ExpiredRange == nil || facts.Path != observability.SlotSkippedExpiredRange ||
		facts.SlotCount != executor.requests[0].ExpiredRange.Count ||
		facts.Cause != string(execution.CauseExpiredReplay) || facts.Reason != contract.ReasonSnapshotUnavailable ||
		skipped[0].ReasonCode != observability.ReasonCode(contract.ReasonSnapshotUnavailable) || skipped[0].Trace.OwnerEpoch == 0 ||
		skipped[0].HeldBy == nil || facts.FirstSlot != int64(first) {
		t.Fatalf("slot_skipped observation = %+v facts %+v", skipped[0], facts)
	}
}
