// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// partialEventError is what the sink returns for a batch it wrote except for
// some events: the ones it refused and the ones it withheld beside them.
type partialEventError struct {
	notWritten []string
}

func (err *partialEventError) Error() string { return "some events rejected" }
func (err *partialEventError) OutputRejectionReason() string {
	return contract.ReasonOutputConversionRejected
}
func (err *partialEventError) OutputNotWrittenEventIDs() []string { return err.notWritten }

// partialEventsPorts writes every batch, and answers a batch holding the
// refused event with the partial refusal.
type partialEventsPorts struct {
	*planFailurePorts
	refused string
	partial *partialEventError
	// whole, when set, answers the refused batch instead of partial.
	whole   error
	batches [][]string
}

func (ports *partialEventsPorts) WriteBatch(_ context.Context, events []contract.TriggerEventV1) error {
	ids := make([]string, len(events))
	for index, event := range events {
		ids[index] = event.EventID
	}
	ports.batches = append(ports.batches, ids)
	for _, id := range ids {
		if id == ports.refused {
			if ports.whole != nil {
				return ports.whole
			}
			return ports.partial
		}
	}
	return nil
}

type acknowledgingCopy struct {
	acknowledged []string
}

func (*acknowledgingCopy) Contains(string, string, string) bool                              { return true }
func (*acknowledgingCopy) TrackPlans(execution.QueryGroupIdentity, []execution.PlanIdentity) {}
func (copy *acknowledgingCopy) Acknowledged(events []contract.TriggerEventV1) {
	for _, event := range events {
		copy.acknowledged = append(copy.acknowledged, event.EventID)
	}
}

type outputIsolationFixture struct {
	*planIsolationFixture
	events   *partialEventsPorts
	copy     *acknowledgingCopy
	progress *committingProgressPorts
	refused  execution.StateKeyIdentity
	kept     execution.StateKeyIdentity
}

// newOutputIsolationFixture is the isolation fixture with a second series on
// the Plan whose output is refused in part: refused-series decided the event
// the sink refuses, kept-series an event it writes. The Plan also carries a
// gap statement to apply after its State.
func newOutputIsolationFixture(t *testing.T, notWritten ...string) *outputIsolationFixture {
	t.Helper()
	base := newPlanIsolationFixture(t, nil)
	failedPlan := base.header.DuePlans[0].Identity
	refused := execution.StateKeyIdentity{Plan: failedPlan, StateGeneration: "failed-generation", SeriesIdentityDigest: "failed-series"}
	kept := execution.StateKeyIdentity{Plan: failedPlan, StateGeneration: "failed-generation", SeriesIdentityDigest: "kept-series"}
	applyVersion := base.evaluated.Plans[0].StateResults[0].Mutation.ApplyVersion
	base.loaded.Items = append(base.loaded.Items, execution.RuntimeStateView{Identity: kept, VersionComparison: execution.ApplyVersionPersistedOlder})
	plan := &base.evaluated.Plans[0]
	plan.StateResults[0].Events = []contract.TriggerEventV1{{EventID: "failed-event", PlanRef: contract.RuntimePlanRefV1{StrategyID: "failed"}}}
	plan.StateResults = append(plan.StateResults, execution.StateEvaluation{
		Mutation: execution.StateMutation{Identity: kept, ApplyVersion: applyVersion, MutationDigest: "kept-digest"},
		Events:   []contract.TriggerEventV1{{EventID: "kept-event", PlanRef: contract.RuntimePlanRefV1{StrategyID: "failed"}}},
	})
	plan.GuardAfterState = []execution.PlanGapMutation{{
		Identity:       execution.PlanGapIdentity{Plan: failedPlan, StateGeneration: "failed-generation"},
		MutationDigest: "failed-gap-digest", ExpectedMarkerRevision: 3,
	}}
	if len(notWritten) == 0 {
		notWritten = []string{"failed-event"}
	}
	fixture := &outputIsolationFixture{planIsolationFixture: base, refused: refused, kept: kept, copy: &acknowledgingCopy{}}
	fixture.events = &partialEventsPorts{planFailurePorts: base.ports, refused: "failed-event", partial: &partialEventError{notWritten: notWritten}}
	fixture.progress = &committingProgressPorts{planFailurePorts: base.ports}
	fixture.coordinator.ports.Events = fixture.events
	fixture.coordinator.ports.Progress = fixture.progress
	fixture.coordinator.ports.OpenAlerts = fixture.copy
	evaluationMillis := int64(base.request.Contract.Slot.EvaluationTime) * 1000
	base.request.DuePlanTargets = execution.FrozenDuePlanTargets{
		DuePlanSetDigest: base.request.Contract.DuePlanSetDigest,
		Plans:            []execution.PlanKey{base.header.DuePlans[0].Key(), base.header.DuePlans[1].Key()},
	}
	base.request.EarliestQueryDeadlineUnixMilli = evaluationMillis + 1_000
	base.request.RecoveryUntilUnixMilli = evaluationMillis + 601_000
	base.request.KeepUntilUnixMilli = evaluationMillis + 677_000
	return fixture
}

func (fixture *outputIsolationFixture) finalize(t *testing.T) (execution.SlotExecutionResult, error) {
	t.Helper()
	return fixture.coordinator.finalizePrepared(
		context.Background(), fixture.request, fixture.header, fixture.bindings, fixture.loaded, fixture.evaluated,
	)
}

// A series whose event the sink refused did not send what it decided, so its
// State stays where it was: the next round decides it again from the State
// it had, and nothing records an alert the consumer never received, nor a
// recovery behind one. Every other series of the same Plan moves as it
// would have, the copy hears only of the events that were written, the
// Plan's gap statement is applied as for any round, and the Slot completes
// by the rejection's name.
func TestARefusedSeriesKeepsItsStateWhileTheRestOfThePlanMoves(t *testing.T) {
	fixture := newOutputIsolationFixture(t)
	result, err := fixture.finalize(t)
	if err != nil || !result.Completed || result.Result != observability.ResultTerminal ||
		result.ReasonCode != execution.ReasonCode(contract.ReasonOutputConversionRejected) {
		t.Fatalf("finalizePrepared() result=%+v error=%v, want a completed TERMINAL Slot named %s", result, err, contract.ReasonOutputConversionRejected)
	}
	applied := map[execution.StateKeyIdentity]bool{}
	for _, identity := range fixture.base.stateApplied {
		applied[identity] = true
	}
	if applied[fixture.refused] {
		t.Fatalf("applied State %v includes the refused series: it would record an event the consumer never received", fixture.base.stateApplied)
	}
	if !applied[fixture.kept] || !applied[fixture.healthyState] || len(applied) != 2 {
		t.Fatalf("applied State %v, want the kept series and the healthy sibling", fixture.base.stateApplied)
	}
	acknowledged := map[string]bool{}
	for _, id := range fixture.copy.acknowledged {
		acknowledged[id] = true
	}
	if acknowledged["failed-event"] || !acknowledged["kept-event"] || !acknowledged["healthy-event"] {
		t.Fatalf("acknowledged %v, want the written events and not the refused one", fixture.copy.acknowledged)
	}
	gapApplied := false
	for _, guard := range fixture.base.guards {
		if guard.MutationDigest == "failed-gap-digest" {
			gapApplied = true
		}
	}
	if !gapApplied {
		t.Fatalf("gap statements applied %v, want the Plan's statement applied: the other series moved this round", fixture.base.guards)
	}
	if len(fixture.progress.commits) != 1 || fixture.progress.commits[0].Completion.ReasonCode != execution.ReasonCode(contract.ReasonOutputConversionRejected) {
		t.Fatalf("Progress commits=%+v, want the Slot written down once by the rejection's name", fixture.progress.commits)
	}
}

// A sink that wrote one event of a series and not another would leave the
// consumer holding an alert whose State this process never recorded. The
// coordinator does not apply that: it fails the Slot instead.
func TestAPartlyWrittenSeriesIsRefusedRatherThanApplied(t *testing.T) {
	fixture := newOutputIsolationFixture(t, "failed-event")
	plan := &fixture.evaluated.Plans[0]
	plan.StateResults[0].Events = append(plan.StateResults[0].Events,
		contract.TriggerEventV1{EventID: "failed-sibling-event", PlanRef: contract.RuntimePlanRefV1{StrategyID: "failed"}})
	if _, err := fixture.finalize(t); err == nil {
		t.Fatal("finalizePrepared() applied a series whose events were written in part")
	}
	for _, identity := range fixture.base.stateApplied {
		if identity == fixture.refused {
			t.Fatalf("applied State %v includes the partly written series", fixture.base.stateApplied)
		}
	}
}

// A Plan none of whose output was written moved nothing this round: its gap
// statement waits with its State, as it always has.
func TestAWhollyRefusedPlanAppliesNoGapStatement(t *testing.T) {
	fixture := newOutputIsolationFixture(t)
	fixture.events.whole = &rejectedPlanEventError{reason: contract.ReasonOutputConversionRejected, detail: "the sink's own sentence"}
	if _, err := fixture.finalize(t); err != nil {
		t.Fatalf("finalizePrepared() error = %v", err)
	}
	for _, guard := range fixture.base.guards {
		if guard.MutationDigest == "failed-gap-digest" {
			t.Fatalf("gap statements applied %v, want none for a Plan whose output was refused whole", fixture.base.guards)
		}
	}
	for _, identity := range fixture.base.stateApplied {
		if identity.Plan == fixture.refused.Plan {
			t.Fatalf("applied State %v includes the wholly refused Plan", fixture.base.stateApplied)
		}
	}
}
