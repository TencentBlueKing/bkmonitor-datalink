package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestFinalizePreparedIsolatesPlanEventFailure(t *testing.T) {
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "query-group", EvaluationTime: 1_788_000_000},
		SnapshotRevision: "snapshot-v1", QueryRevision: "query-v1", ScheduleRevision: "schedule-v1",
		ScheduleSegmentStart: 1_787_999_940, DuePlanSetDigest: "due-set-v1",
	}
	request := execution.SlotExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal,
		OwnerFence:       execution.OwnerFence{QueryGroup: "query-group", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "lease-1"},
		ExpectedNextSlot: contractRef.Slot.EvaluationTime,
	}
	failedPlan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "failed"}
	healthyPlan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "healthy"}
	duePlans := []execution.DuePlan{
		{Identity: failedPlan, StateGeneration: "failed-generation", StateApplyEpoch: 1, ScheduleRevision: "failed-schedule"},
		{Identity: healthyPlan, StateGeneration: "healthy-generation", StateApplyEpoch: 1, ScheduleRevision: "healthy-schedule"},
	}
	applyVersion, err := execution.BuildApplyVersion(contractRef, 1)
	if err != nil {
		t.Fatalf("BuildApplyVersion() error: %v", err)
	}
	failedState := execution.StateKeyIdentity{Plan: failedPlan, StateGeneration: "failed-generation", SeriesIdentityDigest: "failed-series"}
	healthyState := execution.StateKeyIdentity{Plan: healthyPlan, StateGeneration: "healthy-generation", SeriesIdentityDigest: "healthy-series"}
	mutation := func(identity execution.StateKeyIdentity, digest execution.MutationDigest) execution.StateMutation {
		return execution.StateMutation{Identity: identity, ApplyVersion: applyVersion, MutationDigest: digest}
	}
	base := &activationSiblingPorts{}
	ports := &planFailurePorts{
		activationSiblingPorts: base,
		activations: map[execution.PlanIdentity]execution.ActivatedPlan{
			failedPlan:  {Identity: failedPlan, StateGeneration: "failed-generation", StateApplyEpoch: 1, ScheduleRevision: "failed-schedule", RequiredFullSlots: 1},
			healthyPlan: {Identity: healthyPlan, StateGeneration: "healthy-generation", StateApplyEpoch: 1, ScheduleRevision: "healthy-schedule", RequiredFullSlots: 1},
		},
		failEventID: "failed-event",
	}
	observations := make([]observability.Observation, 0, 8)
	coordinator := &SlotExecutionCoordinator{ports: Ports{
		Activation: ports, Sequencer: ports, Admission: ports, GapGuard: ports, Events: ports, State: ports, Progress: ports,
		Observer: observability.ObserverFunc(func(ctx context.Context, observation observability.Observation) {
			observation.Trace = observability.TraceFieldsFromContext(ctx)
			observations = append(observations, observability.NormalizeObservation(observation))
		}),
	}}
	header := execution.InternalExecutionHeader{Contract: contractRef, DuePlans: duePlans}
	bindings := []execution.NamedInputBinding{
		{Consumer: execution.ConsumerRef{Plan: failedPlan}, Role: execution.InputRolePrimary, Completeness: execution.CompletenessFull, DataState: execution.DataStateData},
		{Consumer: execution.ConsumerRef{Plan: healthyPlan}, Role: execution.InputRolePrimary, Completeness: execution.CompletenessFull, DataState: execution.DataStateData},
	}
	loaded := execution.StatePreflightResult{Items: []execution.RuntimeStateView{
		{Identity: failedState, VersionComparison: execution.ApplyVersionPersistedOlder},
		{Identity: healthyState, VersionComparison: execution.ApplyVersionPersistedOlder},
	}}
	evaluated := execution.EvaluationResult{
		Contract: contractRef, Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone,
		Plans: []execution.PlanEvaluationResult{
			{Plan: failedPlan, Disposition: execution.PlanDecided, StateResults: []execution.StateEvaluation{{Mutation: mutation(failedState, "failed-digest"), Events: []contract.TriggerEventV1{{EventID: "failed-event", PlanRef: contract.RuntimePlanRefV1{StrategyID: "failed"}}}}}},
			{Plan: healthyPlan, Disposition: execution.PlanDecided, StateResults: []execution.StateEvaluation{{Mutation: mutation(healthyState, "healthy-digest"), Events: []contract.TriggerEventV1{{EventID: "healthy-event", PlanRef: contract.RuntimePlanRefV1{StrategyID: "healthy"}}}}}},
		},
	}

	result, err := coordinator.finalizePrepared(context.Background(), request, header, bindings, loaded, evaluated)
	if err != nil || result.Completed || result.Result != observability.ResultRetrying ||
		result.ReasonCode != execution.ReasonCode(contract.ReasonOutputACKUnknown) {
		t.Fatalf("finalizePrepared() result=%+v error=%v", result, err)
	}
	if len(ports.eventAttempts) != 2 || ports.eventAttempts[0] != "failed-event" || ports.eventAttempts[1] != "healthy-event" {
		t.Fatalf("event attempts=%v, want failed and healthy Plan", ports.eventAttempts)
	}
	if len(base.stateApplied) != 1 || base.stateApplied[0] != healthyState {
		t.Fatalf("applied State=%v, want only healthy sibling", base.stateApplied)
	}
	if base.progressCommits != 0 {
		t.Fatalf("retry-pending Slot committed Progress %d times", base.progressCommits)
	}

	var failedACK, successfulACK bool
	for _, observation := range observations {
		if observation.Stage != observability.StageEventACKed {
			continue
		}
		switch observation.Result {
		case observability.ResultFailed:
			failedACK = observation.ReasonCode == execution.ReasonCode(contract.ReasonOutputACKUnknown) &&
				observation.Trace.StrategyID == "failed"
		case observability.ResultSuccess:
			successfulACK = observation.ReasonCode == observability.ReasonNone &&
				observation.Trace.StrategyID == "healthy"
		}
	}
	if !failedACK || !successfulACK {
		t.Fatalf("event ACK observations=%+v, want failed and successful fixed-reason outcomes", observations)
	}
}

type planFailurePorts struct {
	*activationSiblingPorts
	activations   map[execution.PlanIdentity]execution.ActivatedPlan
	failEventID   string
	eventAttempts []string
}

func (ports *planFailurePorts) LoadActivations(_ context.Context, request execution.PlanActivationRequest) (execution.PlanActivationResult, error) {
	result := execution.PlanActivationResult{Contract: request.Contract}
	for _, plan := range request.Plans {
		result.Facts = append(result.Facts, execution.PlanActivationFact{
			Plan: plan, Selection: execution.ActivationCurrent, Selected: ports.activations[plan],
		})
	}
	return result, nil
}

func (ports *planFailurePorts) WriteBatch(_ context.Context, events []contract.TriggerEventV1) error {
	if len(events) != 1 {
		return errors.New("expected one Plan-local event batch")
	}
	ports.eventAttempts = append(ports.eventAttempts, events[0].EventID)
	if events[0].EventID == ports.failEventID {
		return errors.New("injected Plan-local event failure")
	}
	return nil
}
