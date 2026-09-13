package worker

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// TestConvergedSlotStillSaysWhyItWasUnavailable pins that the converge path
// reports the completion cause, not just the completion.
//
// The cause is the field that separates the four conditions folded into
// UNAVAILABLE - data that has not landed yet resolves itself, a Plan that could
// not be decided does not. The converge path commits a completion decided
// elsewhere, and it used to pass a literal empty cause, so every Slot that came
// back through activation convergence arrived on the page as an UNAVAILABLE
// that could not say which one it was. That is the shape operators already
// learned to ignore.
//
// Scope: this covers the wiring from the carried completion to the observation.
// The decision - which cause goes with which completion - is covered by
// TestConfigDriftCompletionDoesNotHideAnUnavailablePrimary against the one
// constructor that makes both.
func TestConvergedSlotStillSaysWhyItWasUnavailable(t *testing.T) {
	result, causes := convergeDriftedSlot(t, execution.CompletenessUnavailable)
	if !result.Completed || result.CompletionKind != execution.CompletionUnavailable {
		t.Fatalf("convergence result=%+v, want a completed unavailable Slot", result)
	}
	if len(causes) != 1 || causes[0] != string(execution.CausePrimaryInputUnavailable) {
		t.Fatalf("observed completion causes=%q, want one %q", causes, execution.CausePrimaryInputUnavailable)
	}
}

// The same convergence with a usable primary completes the Slot as
// COMPLETED_WITH_PARTIAL_GAP, and that kind used to reach the commit line with
// no cause at all: the page listed it beside the partial gaps a provider
// caused and could not tell an edited strategy from a hole in storage.
func TestConvergedDriftedSlotSaysItsPlansMoved(t *testing.T) {
	for _, completeness := range []execution.Completeness{execution.CompletenessFull, execution.CompletenessPartial} {
		result, causes := convergeDriftedSlot(t, completeness)
		if !result.Completed || result.CompletionKind != execution.CompletionPartialGap {
			t.Fatalf("%s primary: convergence result=%+v, want a completed partial-gap Slot", completeness, result)
		}
		if len(causes) != 1 || causes[0] != string(execution.CauseConfigDrift) {
			t.Fatalf("%s primary: observed completion causes=%q, want one %q", completeness, causes, execution.CauseConfigDrift)
		}
	}
}

// convergeDriftedSlot drives the converge path for a Slot whose only Plan was
// deselected while it ran, and returns the result together with every cause
// the Progress commit line reported.
func convergeDriftedSlot(t *testing.T, completeness execution.Completeness) (execution.SlotExecutionResult, []string) {
	t.Helper()
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "query-group", EvaluationTime: 1_788_000_000},
		SnapshotRevision: "snapshot-v1", QueryRevision: "query-v1", ScheduleRevision: "schedule-v1",
		ScheduleSegmentStart: 1_787_999_940, DuePlanSetDigest: "due-set-v1",
	}
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "deselected"}
	request := execution.SlotExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
		OwnerFence:       execution.OwnerFence{QueryGroup: "query-group", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "lease-1"},
		ExpectedNextSlot: contractRef.Slot.EvaluationTime,
		DuePlanTargets: execution.FrozenDuePlanTargets{
			DuePlanSetDigest: contractRef.DuePlanSetDigest, Plans: []execution.PlanIdentity{plan},
		},
		EarliestQueryDeadlineUnixMilli: 1_788_000_060_000,
		KeepUntilUnixMilli:             1_788_000_600_000,
	}
	primary := execution.PrimaryInputFact{Completeness: completeness, DataState: execution.DataStateData}
	if completeness == execution.CompletenessUnavailable {
		primary.DataState = execution.DataStateUnknown
	}
	completion, cause := configDriftCompletion(contractRef, &primary)
	if cause == "" {
		t.Fatal("the constructor produced a completion with no cause; the rest of this test proves nothing")
	}

	ports := &convergeObservingPorts{}
	coordinator := &SlotExecutionCoordinator{
		budget: ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10},
		ports: Ports{
			Activation: ports, Sequencer: ports, Admission: ports, GapGuard: ports,
			Events: ports, State: ports, Progress: ports,
			Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				if observation.Stage == observability.StageProgressCommitted {
					ports.committedCauses = append(ports.committedCauses, observation.ProgressCompletionCause)
				}
			}),
		},
	}

	// The Plan is no longer activated, which is what drift means here: nothing
	// is left to protect, so convergence goes straight to the commit that
	// carries the completion. That is the branch under test.
	activationRequest := execution.PlanActivationRequest{Contract: contractRef, Plans: []execution.PlanIdentity{plan}}
	deselected := execution.PlanActivationResult{
		Contract: contractRef,
		Facts:    []execution.PlanActivationFact{{Plan: plan, Selection: execution.ActivationNone}},
	}
	protection := activationProtectionRequiredError{
		activationRequest: activationRequest,
		currentFacts:      deselected,
		activations:       deselected,
		completion:        completion,
		completionCause:   execution.CompletionAttribution{Cause: cause},
	}
	result, err := coordinator.convergeNormalActivation(context.Background(), request, protection)
	if err != nil {
		t.Fatalf("convergeNormalActivation() error: %v", err)
	}
	return result, ports.committedCauses
}

type convergeObservingPorts struct {
	committedCauses []string
}

func (*convergeObservingPorts) Sequence(ctx context.Context, _ execution.SequencingScope, run func(context.Context) error) error {
	return run(ctx)
}

func (*convergeObservingPorts) LoadActivations(_ context.Context, request execution.PlanActivationRequest) (execution.PlanActivationResult, error) {
	result := execution.PlanActivationResult{Contract: request.Contract}
	for _, plan := range request.Plans {
		result.Facts = append(result.Facts, execution.PlanActivationFact{Plan: plan, Selection: execution.ActivationNone})
	}
	return result, nil
}

func (*convergeObservingPorts) Check(context.Context, execution.SideEffectAdmissionRequest) (execution.SideEffectAdmissionResult, error) {
	return execution.SideEffectAdmissionResult{Admitted: true}, nil
}

func (*convergeObservingPorts) LoadGapsInto(context.Context, execution.GapLoadRequest, func(execution.GapGuardSnapshot) error) error {
	return nil
}

func (*convergeObservingPorts) LoadGaps(context.Context, execution.GapLoadRequest) (execution.GapLoadResult, error) {
	return execution.GapLoadResult{}, nil
}

func (*convergeObservingPorts) ApplyGap(context.Context, execution.GapGuardApplyRequest) (execution.GapGuardApplyResult, error) {
	return execution.GapGuardApplyResult{}, nil
}

func (*convergeObservingPorts) WriteBatch(context.Context, []contract.TriggerEventV1) error {
	return nil
}

func (*convergeObservingPorts) LoadRuntime(context.Context, execution.StatePreflightRequest) (execution.StatePreflightResult, error) {
	return execution.StatePreflightResult{}, nil
}

func (*convergeObservingPorts) AdmitRuntime(context.Context, execution.StateApplyRequest) (execution.StateAdmissionResult, error) {
	return execution.StateAdmissionResult{}, nil
}

func (*convergeObservingPorts) ApplyRuntime(context.Context, execution.StateApplyRequest) (execution.StateApplyResult, error) {
	return execution.StateApplyResult{}, nil
}

func (*convergeObservingPorts) LoadProgress(context.Context, execution.ProgressIdentity) (execution.ProgressLoadResult, error) {
	return execution.ProgressLoadResult{}, nil
}

func (*convergeObservingPorts) BeginSlot(context.Context, execution.ProgressBeginRequest) (execution.ProgressBeginResult, error) {
	return execution.ProgressBeginResult{Status: execution.ProgressCommitted}, nil
}

func (*convergeObservingPorts) CommitProgress(context.Context, execution.ProgressCommitRequest) (execution.ProgressCommitResult, error) {
	return execution.ProgressCommitResult{Status: execution.ProgressCommitted}, nil
}
