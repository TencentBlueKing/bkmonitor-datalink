package worker_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestCostSummaryObservesRealWorkerWithoutChangingExecution(t *testing.T) {
	for _, failure := range []string{"", "evaluate", "state_apply", "progress_commit"} {
		t.Run(failure, func(t *testing.T) {
			request := slotRequest(execution.OperationNormal)
			identity := planIdentity()
			owner := observability.CostPlanIdentity{TenantID: identity.TenantID, BusinessID: identity.BusinessID, StrategyID: identity.StrategyID}
			now := time.Unix(int64(request.Contract.Slot.EvaluationTime)+1, 0)
			summary := observability.NewCostSummary(observability.CostSummaryOptions{ProcessID: "worker", Window: time.Minute, GroupCapacity: 1, PlanCapacity: 1, MetadataBytes: 1024, TopN: 1, Now: func() time.Time { return now }})
			summary.Reconcile([]observability.CostGroup{{QueryGroupKey: string(request.Contract.Slot.QueryGroup), SnapshotRevision: string(request.Contract.SnapshotRevision), QueryRevision: string(request.Contract.QueryRevision), ScheduleRevision: string(request.Contract.ScheduleRevision), Members: []observability.CostPlanIdentity{owner}}}, true)
			var evaluations []observability.Observation
			recorder := observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
				if o.Stage == observability.StageEvaluationCompleted {
					evaluations = append(evaluations, o)
				}
			})
			enabled := newFixtureWithObserver(t, true, failure, observability.Multi(summary, recorder))
			disabled := newFixtureWithObserver(t, true, failure, observability.NopObserver{})
			got, gotErr := enabled.coordinator.Execute(context.Background(), request)
			want, wantErr := disabled.coordinator.Execute(context.Background(), request)
			// The wall clock is the one thing two runs of the same Slot cannot
			// be expected to agree on, so it is taken out of the comparison by
			// name rather than by loosening it. Everything else -- including
			// every budget the Slot used -- is a count of work and is still
			// compared exactly: that is what makes this a statement about the
			// observer and not about the machine's speed.
			gotTiming, wantTiming := got.Timing, want.Timing
			got.Timing, want.Timing = execution.SlotTiming{}, execution.SlotTiming{}
			if (gotErr != nil) != (wantErr != nil) || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(*enabled.trace, *disabled.trace) || !reflect.DeepEqual(enabled.ports.lastProgress, disabled.ports.lastProgress) || enabled.ports.eventCount != disabled.ports.eventCount || enabled.ports.stateApplyCalls != disabled.ports.stateApplyCalls {
				t.Fatalf("summary changed execution: got=%+v/%v want=%+v/%v", got, gotErr, want, wantErr)
			}
			// And the exclusion does not get to hide the field going away: a
			// Slot that ran reports a duration for having run.
			if got.Completed && (gotTiming == execution.SlotTiming{} || wantTiming == execution.SlotTiming{}) {
				t.Fatalf("a completed Slot reported no timing at all: with=%+v without=%+v", gotTiming, wantTiming)
			}
			if len(evaluations) != 1 || evaluations[0].EvaluationOwner != owner || !evaluations[0].DurationKnown || evaluations[0].EvaluationRecordsKnown != (failure != "evaluate") {
				t.Fatalf("real consumer/time facts missing: %+v", evaluations)
			}
			summary.Publish(now)
			snapshot := summary.Snapshot()
			if snapshot.Coverage.ObservedPlans != 1 || snapshot.Coverage.UnattributedEvaluations != 0 {
				t.Fatalf("worker not actually feeding summary: %+v", snapshot)
			}
		})
	}
}

func TestCostCompletionOnlyOwnsIdentityWithoutInventingTimer(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	f, request := newCompletionOnlyFixture(t, plans, requirements, nil)
	if _, err := f.coordinator.Execute(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	for _, o := range *f.observations {
		if o.Stage == observability.StageEvaluationCompleted {
			if o.EvaluationOwner.TenantID != plans[0].Identity.TenantID || o.EvaluationOwner.StrategyID != plans[0].Identity.StrategyID || !o.EvaluationRecordsKnown || o.DurationKnown || o.Duration != 0 {
				t.Fatalf("completion-only facts fabricated/missing: %+v", o)
			}
			return
		}
	}
	t.Fatal("completion-only evaluation fact missing")
}
