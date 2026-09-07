package worker_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

type rangeControl struct {
	raw           []byte
	failAt, calls int
}

func (c *rangeControl) ReadControl(context.Context, execution.QueryGroupIdentity, string) ([]byte, bool, error) {
	return append([]byte(nil), c.raw...), c.raw == nil, nil
}
func (c *rangeControl) FencedCompareAndSet(_ context.Context, r ownership.FencedCASRequest) (ownership.FencedCASStatus, error) {
	c.calls++
	if c.calls == c.failAt || !bytes.Equal(c.raw, r.Expected) {
		return ownership.FencedCASConflict, nil
	}
	c.raw = append([]byte(nil), r.Value...)
	return ownership.FencedCASApplied, nil
}

type rangeSlots struct{}

func (rangeSlots) NextSlotAfter(_ context.Context, _ execution.QueryGroupIdentity, at execution.EvaluationTime) (execution.EvaluationTime, error) {
	return at + 60, nil
}

func workerRangeRequest(t *testing.T) execution.SlotExecutionRequest {
	t.Helper()
	request := slotRequest(execution.OperationNormal)
	spec := execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Timezone: "UTC"}
	rev, err := execution.DerivePlanScheduleRevision(spec)
	if err != nil {
		t.Fatal(err)
	}
	plans := []execution.FrozenPlanSchedule{{Identity: planIdentity(), Spec: spec, ScheduleRevision: rev}}
	sched, err := execution.DeriveQueryGroupScheduleRevision(plans)
	if err != nil {
		t.Fatal(err)
	}
	request.Contract.ScheduleRevision = sched
	first := request.UnfinishedProjection()
	request.Contract.Slot.EvaluationTime += 120
	request.EarliestQueryDeadlineUnixMilli += 120000
	request.RecoveryUntilUnixMilli += 120000
	request.KeepUntilUnixMilli += 120000
	request.ReplayExpired = true
	proof, err := execution.SealExpiredRange(execution.ExpiredRangeProjectionV1{
		Schedule: execution.FrozenQueryGroupSchedule{Segment: execution.ScheduleSegmentFact{Publication: execution.SnapshotPublicationRef{SnapshotRevision: request.Contract.SnapshotRevision, PublicationEpoch: 1}, QueryGroup: request.Contract.Slot.QueryGroup, QueryRevision: request.Contract.QueryRevision, ScheduleRevision: sched, Start: request.Contract.ScheduleSegmentStart}, Plans: plans},
		First:    first, Last: request.UnfinishedProjection(), Next: request.Contract.Slot.EvaluationTime + 60, Count: 3,
		QueryReserveMillis: 59000, ReplayAgeMillis: 600000, JudgedAtMillis: request.RecoveryUntilUnixMilli,
	})
	if err != nil {
		t.Fatal(err)
	}
	request.ExpiredRange = &proof
	return request
}

func TestExpiredRangeTailGuardCommitConflictRestartsSameProof(t *testing.T) {
	testRangeTailGuardCommitConflict(t, false)
}

func TestExpiredRangeV2DistanceGuardCommitConflictRestartsSameProof(t *testing.T) {
	testRangeTailGuardCommitConflict(t, true)
}

func testRangeTailGuardCommitConflict(t *testing.T, distance bool) {
	ctx := context.Background()
	request := workerRangeRequest(t)
	if distance {
		p := request.ExpiredRange.Clone()
		p.JudgedAtMillis = p.Last.EarliestQueryDeadlineUnixMilli + 180000
		p.EligibilityV2 = &execution.ExpiredRangeEligibilityV2{Reason: execution.RangeDistanceExpired, MaxReplaySlots: 3, DistanceHead: p.Last.Contract.Slot.EvaluationTime + 180}
		var err error
		p, err = execution.SealExpiredRange(p)
		if err != nil {
			t.Fatal(err)
		}
		request.ExpiredRange = &p
	}
	now := time.UnixMilli(request.ExpiredRange.JudgedAtMillis)
	activation := activePlanResult("state-v2", 2)
	activation.Contract = request.Contract
	fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activation})
	control := &rangeControl{}
	newStore := func() *progress.Store {
		s, err := progress.NewStore(progress.StoreOptions{Prefix: "alarmd", Control: control, Slots: rangeSlots{}, Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	store := newStore()
	// Seed an actual committed predecessor, not a synthetic pending object.
	prior := request.ExpiredRange.First.Contract
	prior.Slot.EvaluationTime -= 60
	_, err := store.CommitProgress(ctx, execution.ProgressCommitRequest{Identity: execution.ProgressIdentity{QueryGroup: prior.Slot.QueryGroup}, OwnerFence: request.OwnerFence, ExpectedNextSlot: prior.Slot.EvaluationTime,
		Completion: execution.SlotCompletion{Contract: prior, Kind: execution.CompletionFullEmpty, Result: observability.ResultSuccess, Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty}}})
	if err != nil {
		t.Fatal(err)
	}
	initialRaw := append([]byte(nil), control.raw...)
	newCoordinator := func(s *progress.Store) *worker.SlotExecutionCoordinator {
		ports := fixture.ports
		c, err := worker.NewSlotExecutionCoordinator(worker.Ports{Finalization: ports, Activation: ports, Query: ports, Sequencer: ports, Evaluator: ports, Admission: ports, GapGuard: ports, Events: ports, State: ports, Progress: s, Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {})}, worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	control.failAt = control.calls + 2 // Begin succeeds, Guard writes, Commit CAS conflicts.
	result, err := newCoordinator(store).Execute(ctx, request)
	if err == nil || result.Completed || len(fixture.ports.mutations) != 1 {
		t.Fatalf("first result=%+v err=%v guards=%d", result, err, len(fixture.ports.mutations))
	}
	if fixture.ports.mutations[0].ApplyVersion.EvaluationTime != request.Contract.Slot.EvaluationTime {
		t.Fatal("Guard did not use actual last Slot")
	}
	load, err := newStore().LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: prior.Slot.QueryGroup})
	if err != nil || load.Progress.UnfinishedRange == nil || !load.Progress.UnfinishedRange.Equal(*request.ExpiredRange) {
		t.Fatalf("pending=%+v err=%v", load, err)
	}
	now = now.Add(time.Hour) // Recovery must keep the original reason, not reclassify by new time.
	result, err = newCoordinator(newStore()).Execute(ctx, request)
	if err != nil || !result.Completed {
		t.Fatalf("restart result=%+v err=%v", result, err)
	}
	if result.CompletionKind != request.ExpiredRange.CompletionKind() || result.ReasonCode != request.ExpiredRange.CompletionReason() {
		t.Fatalf("changed cause: %+v", result)
	}
	load, err = store.LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: prior.Slot.QueryGroup})
	if err != nil || load.Progress.NextSlot != request.ExpiredRange.Next || load.Progress.UnfinishedRange != nil || load.Progress.CurrentOrRecentGap.Count != 3 || load.Progress.LastFullSlot != prior.Slot.EvaluationTime {
		t.Fatalf("final=%+v err=%v", load, err)
	}
	// Counterexample: retain the actual tail Guard but represent Progress as
	// the old head-only pending value. A restart cannot safely recover it by
	// running the head: the existing Guard comparison rejects its older version.
	control.raw = initialRaw
	head := request
	head.ExpiredRange = nil
	head.Contract = request.ExpiredRange.First.Contract
	head.DuePlanTargets = request.ExpiredRange.First.DuePlanTargets.Clone()
	head.EarliestQueryDeadlineUnixMilli = request.ExpiredRange.First.EarliestQueryDeadlineUnixMilli
	head.RecoveryUntilUnixMilli = head.EarliestQueryDeadlineUnixMilli + request.ExpiredRange.ReplayAgeMillis
	head.KeepUntilUnixMilli = request.ExpiredRange.First.KeepUntilUnixMilli
	fixture.ports.finalization.Contract = head.Contract
	fixture.ports.finalization.Targets = head.DuePlanTargets.Clone()
	fixture.ports.activations[0].Contract = head.Contract
	result, err = newCoordinator(newStore()).Execute(ctx, head)
	if err == nil || result.Completed || !strings.Contains(err.Error(), "marker is newer than the Slot") {
		t.Fatalf("head-only unsafe representation result=%+v err=%v", result, err)
	}
}
