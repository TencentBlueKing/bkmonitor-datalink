package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestFinalizePreparedPreservesStableSiblingReceiptsDuringForceWarmingActivationConvergence(t *testing.T) {
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "query-group", EvaluationTime: 1_788_000_000},
		SnapshotRevision: "snapshot-v1", QueryRevision: "query-v1", ScheduleRevision: "schedule-v1",
		ScheduleSegmentStart: 1_787_999_940, DuePlanSetDigest: "due-set-v1",
	}
	request := execution.SlotExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
		OwnerFence:       execution.OwnerFence{QueryGroup: "query-group", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "lease-1"},
		ExpectedNextSlot: contractRef.Slot.EvaluationTime,
	}
	changedPlan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "changed"}
	stablePlan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "stable"}
	duePlans := []execution.DuePlan{
		{Identity: changedPlan, StateGeneration: "old-changed", StateApplyEpoch: 1, ScheduleRevision: "old-changed-schedule"},
		{Identity: stablePlan, StateGeneration: "stable-generation", StateApplyEpoch: 1, ScheduleRevision: "stable-schedule"},
	}
	applyVersion, err := execution.BuildApplyVersion(contractRef, 1)
	if err != nil {
		t.Fatalf("BuildApplyVersion() error: %v", err)
	}
	changedState := execution.StateKeyIdentity{Plan: changedPlan, StateGeneration: "old-changed", SeriesIdentityDigest: "changed-series"}
	stableState := execution.StateKeyIdentity{Plan: stablePlan, StateGeneration: "stable-generation", SeriesIdentityDigest: "stable-series"}
	mutation := func(identity execution.StateKeyIdentity, digest execution.MutationDigest) execution.StateMutation {
		return execution.StateMutation{Identity: identity, ApplyVersion: applyVersion, MutationDigest: digest}
	}
	ports := &activationSiblingPorts{contract: contractRef, changedPlan: changedPlan, stablePlan: stablePlan}
	coordinator := &SlotExecutionCoordinator{ports: Ports{
		Activation: ports, Sequencer: ports, Admission: ports, GapGuard: ports, Events: ports, State: ports, Progress: ports,
		Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {}),
	}}
	header := execution.InternalExecutionHeader{Contract: contractRef, DuePlans: duePlans}
	bindings := []execution.NamedInputBinding{
		{Consumer: execution.ConsumerRef{Plan: changedPlan}, Role: execution.InputRolePrimary, Completeness: execution.CompletenessFull, DataState: execution.DataStateData},
		{Consumer: execution.ConsumerRef{Plan: stablePlan}, Role: execution.InputRolePrimary, Completeness: execution.CompletenessFull, DataState: execution.DataStateData},
	}
	loaded := execution.StatePreflightResult{Items: []execution.RuntimeStateView{
		{Identity: changedState, VersionComparison: execution.ApplyVersionPersistedOlder},
		{Identity: stableState, VersionComparison: execution.ApplyVersionPersistedOlder},
	}}
	evaluated := execution.EvaluationResult{
		Contract: contractRef, Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone,
		Plans: []execution.PlanEvaluationResult{
			{Plan: changedPlan, Disposition: execution.PlanDecided, StateResults: []execution.StateEvaluation{{Mutation: mutation(changedState, "changed-digest"), Events: []contract.TriggerEventV1{{EventID: "changed-event"}}}}},
			{Plan: stablePlan, Disposition: execution.PlanDecided, StateResults: []execution.StateEvaluation{{Mutation: mutation(stableState, "stable-digest"), Events: []contract.TriggerEventV1{{EventID: "stable-event"}}}}},
		},
	}

	_, err = coordinator.finalizePrepared(context.Background(), request, header, bindings, loaded, evaluated)
	var protection *activationProtectionRequiredError
	if !errors.As(err, &protection) {
		t.Fatalf("finalizePrepared() error=%v, want activation convergence", err)
	}
	if len(ports.events) != 1 || ports.events[0].EventID != "stable-event" ||
		len(ports.stateApplied) != 1 || ports.stateApplied[0] != stableState {
		t.Fatalf("stable sibling receipts events=%+v state=%+v", ports.events, ports.stateApplied)
	}
	if len(ports.admitted) != 2 || ports.admitted[0] != stablePlan || ports.admitted[1] != stablePlan {
		t.Fatalf("pre-Guard admission Plans=%+v", ports.admitted)
	}

	result, err := coordinator.convergeNormalActivation(context.Background(), request, *protection)
	if err != nil || result.Completed || result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) {
		t.Fatalf("convergeNormalActivation() result=%+v error=%v", result, err)
	}
	if len(ports.guards) != 1 || ports.guards[0].Identity != (execution.PlanGapIdentity{Plan: changedPlan, StateGeneration: "new-changed"}) {
		t.Fatalf("changed Plan Guard=%+v", ports.guards)
	}
	if len(ports.admitted) != 3 || ports.admitted[2] != changedPlan || ports.progressCommits != 0 {
		t.Fatalf("convergence admissions=%+v Progress commits=%d", ports.admitted, ports.progressCommits)
	}
}

type activationSiblingPorts struct {
	contract        execution.FrozenExecutionContractRef
	changedPlan     execution.PlanIdentity
	stablePlan      execution.PlanIdentity
	admitted        []execution.PlanIdentity
	events          []contract.TriggerEventV1
	stateApplied    []execution.StateKeyIdentity
	guards          []execution.PlanGapMutation
	progressCommits int
}

func (*activationSiblingPorts) Sequence(ctx context.Context, _ execution.SequencingScope, run func(context.Context) error) error {
	return run(ctx)
}

func (ports *activationSiblingPorts) LoadActivations(_ context.Context, request execution.PlanActivationRequest) (execution.PlanActivationResult, error) {
	result := execution.PlanActivationResult{Contract: request.Contract}
	for _, plan := range request.Plans {
		selected := execution.ActivatedPlan{Identity: plan, StateApplyEpoch: 1, RequiredFullSlots: 1}
		switch plan {
		case ports.changedPlan:
			selected.StateGeneration, selected.StateApplyEpoch, selected.ScheduleRevision = "new-changed", 2, "new-changed-schedule"
			selected.ForceWarming = true
		case ports.stablePlan:
			selected.StateGeneration, selected.ScheduleRevision = "stable-generation", "stable-schedule"
		}
		result.Facts = append(result.Facts, execution.PlanActivationFact{Plan: plan, Selection: execution.ActivationCurrent, Selected: selected})
	}
	return result, nil
}

func (ports *activationSiblingPorts) Check(_ context.Context, request execution.SideEffectAdmissionRequest) (execution.SideEffectAdmissionResult, error) {
	ports.admitted = append(ports.admitted, request.Plan)
	return execution.SideEffectAdmissionResult{Admitted: true}, nil
}

func (ports *activationSiblingPorts) LoadGaps(_ context.Context, request execution.GapLoadRequest) (execution.GapLoadResult, error) {
	items := make([]execution.GapGuardSnapshot, len(request.Items))
	for index, item := range request.Items {
		items[index] = execution.GapGuardSnapshot{Identity: item.Identity, Status: execution.GapMissing}
	}
	return execution.GapLoadResult{Items: items}, nil
}

func (ports *activationSiblingPorts) ApplyGap(_ context.Context, request execution.GapGuardApplyRequest) (execution.GapGuardApplyResult, error) {
	ports.guards = append(ports.guards, request.Items...)
	items := make([]execution.GapGuardApplyItemResult, len(request.Items))
	for index, item := range request.Items {
		items[index] = execution.GapGuardApplyItemResult{Identity: item.Identity, Status: execution.GapGuardApplied}
	}
	return execution.GapGuardApplyResult{Items: items}, nil
}

func (ports *activationSiblingPorts) WriteBatch(_ context.Context, events []contract.TriggerEventV1) error {
	ports.events = append(ports.events, events...)
	return nil
}

func (*activationSiblingPorts) LoadRuntime(context.Context, execution.StatePreflightRequest) (execution.StatePreflightResult, error) {
	return execution.StatePreflightResult{}, nil
}

func (*activationSiblingPorts) AdmitRuntime(_ context.Context, request execution.StateApplyRequest) (execution.StateAdmissionResult, error) {
	items := make([]execution.StateAdmissionItemResult, len(request.Items))
	for index, item := range request.Items {
		items[index] = execution.StateAdmissionItemResult{Identity: item.Identity, Status: execution.StateAdmissionAccepted}
	}
	return execution.StateAdmissionResult{Items: items}, nil
}

func (ports *activationSiblingPorts) ApplyRuntime(_ context.Context, request execution.StateApplyRequest) (execution.StateApplyResult, error) {
	items := make([]execution.StateApplyItemResult, len(request.Items))
	for index, item := range request.Items {
		ports.stateApplied = append(ports.stateApplied, item.Identity)
		items[index] = execution.StateApplyItemResult{Identity: item.Identity, Status: execution.StateApplied}
	}
	return execution.StateApplyResult{Items: items}, nil
}

func (*activationSiblingPorts) LoadProgress(context.Context, execution.ProgressIdentity) (execution.ProgressLoadResult, error) {
	return execution.ProgressLoadResult{}, nil
}

func (ports *activationSiblingPorts) CommitProgress(context.Context, execution.ProgressCommitRequest) (execution.ProgressCommitResult, error) {
	ports.progressCommits++
	return execution.ProgressCommitResult{}, errors.New("unexpected Progress commit")
}
