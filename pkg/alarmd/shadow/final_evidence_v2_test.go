package shadow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/shadow"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestFinalEvidenceV2EncodedRuntimePath(t *testing.T) {
	in := frozenFinalInput(t, true)
	e, err := shadow.BuildGoFinalEvidenceV2(in, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	want, err := contract.EncodeFinalResultEvidenceV1(e, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := shadow.EncodeGoFinalEvidenceV2(in, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded.CopyBytes(), want) {
		t.Fatal("runtime encoding changed canonical envelope")
	}
	copy := encoded.CopyBytes()
	copy[0] = 'x'
	in.Event.LevelResults[0].Result = "caller mutation"
	if !bytes.Equal(encoded.CopyBytes(), want) {
		t.Fatal("immutable encoding aliases caller")
	}
	if _, err := shadow.EncodeGoFinalEvidenceV2(frozenFinalInput(t, true), len(want)-1); err == nil {
		t.Fatal("encoded path bypassed message bound")
	}
}

func frozenFinalInput(t *testing.T, emptyUnit bool) shadow.GoFrozenEvidenceInputV2 {
	t.Helper()
	due, req, queries := frozenInput(t, func(p *contract.EvaluationPlanV2) {
		if !emptyUnit {
			return
		}
		p.InputProjection.DataUnit, p.StrategyIR.InputProjection.DataUnit = "", ""
		for i := range p.StrategyIR.Levels {
			a := &p.StrategyIR.Levels[i].DetectPlan.Algorithms[0]
			a.Config = []byte(strings.ReplaceAll(string(a.Config), `"percent"`, `""`))
		}
	})
	due.StateApplyEpoch = 1
	cfg, err := shadow.BuildFrozenComparisonConfigV2(due, req, queries)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../contract/testdata/go-v2/trigger_event_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	native, err := contract.DecodeTriggerEventV1(b)
	if err != nil {
		t.Fatal(err)
	}
	for i := range native.LevelResults {
		l := &native.LevelResults[i]
		for _, compiled := range due.CompiledPlan.Levels() {
			if compiled.Definition().LevelID != l.LevelID {
				continue
			}
			l.LevelTriggerFingerprint = compiled.Fingerprints().Trigger
			l.DecisionWindow.Trigger.WindowSize = compiled.Trigger().WindowSize
			l.DecisionWindow.Trigger.RequiredAnomalies = compiled.Trigger().RequiredAnomalies
			l.DecisionWindow.Recovery.RequiredConsecutiveWindows = compiled.Recovery().ConsecutiveWindows
		}
	}
	event, err := contract.BuildTriggerEventV1(contract.TriggerEventBuildInputV1{EventKind: native.EventKind, TenantID: due.Identity.TenantID, BusinessID: due.Identity.BusinessID, PlanRef: due.CompiledPlan.PlanRef(), RecordRef: native.RecordRef, Observed: native.Observed, LevelResults: native.LevelResults, EvaluationTime: native.EvaluationTime, DetectPlanFingerprint: due.CompiledPlan.Fingerprints().Detect, TriggerStateFingerprint: due.CompiledPlan.Fingerprints().Trigger, ExecutionID: native.Trace.ExecutionID, MaxEvidenceBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	var requirement strategy.EffectiveTimeRequirement
	for _, level := range due.CompiledPlan.Levels() {
		if level.Definition().LevelID == event.PrimaryLevelID {
			requirement = level.EffectiveTimeRequirement()
		}
	}
	facts, err := strategy.NewStaticScheduleProvider(nil).Resolve(context.Background(), []strategy.EffectiveTimeRequest{{TenantID: due.Identity.TenantID, BusinessID: due.Identity.BusinessID, EvaluationTime: event.EvaluationTime, Requirement: requirement}})
	if err != nil {
		t.Fatal(err)
	}
	frozen := execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "actual-qg", EvaluationTime: execution.EvaluationTime(event.EvaluationTime)}, SnapshotRevision: "snapshot", QueryRevision: "qg-query-revision", ScheduleRevision: "qg-schedule", ScheduleSegmentStart: 60, DuePlanSetDigest: execution.DuePlanSetDigest(strings.Repeat("a", 64))}
	version, err := execution.BuildApplyVersion(frozen, due.StateApplyEpoch)
	if err != nil {
		t.Fatal(err)
	}
	_, digest, err := contract.CanonicalComparisonConfigV2(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return shadow.GoFrozenEvidenceInputV2{EpochID: "epoch", IdentityVersion: "identity-v1", ProjectionVersion: "primary-v1", Event: *event, ACK: shadow.BusinessACK{Confirmed: true, EventID: event.EventID, SemanticDigest: event.EventSemanticDigest}, Due: due, Requirements: req, Queries: queries, Frozen: frozen, PrimaryEffectiveTime: facts[0],
		Context:      contract.ShadowContextV1{ComparisonConfigDigest: digest, PlanScheduleRevision: string(due.ScheduleRevision), EvaluationTime: event.EvaluationTime, SlotIdentity: string(version.SlotDigest), SnapshotRevision: string(frozen.SnapshotRevision), QueryRevision: string(frozen.QueryRevision), QueryGroupScheduleRevision: string(frozen.ScheduleRevision), ScheduleSegmentStart: int64(frozen.ScheduleSegmentStart), DuePlanSetDigest: string(frozen.DuePlanSetDigest), EffectiveTimeRequirementDigest: facts[0].RequirementDigest(), EffectiveTimeFactDigest: facts[0].FactDigest()},
		Completeness: contract.ShadowCompletenessV1{Input: "FULL", Readiness: "READY", History: "FULL"}}
}

func TestFinalEvidenceV2RealFrozenAdapter(t *testing.T) {
	for _, empty := range []bool{false, true} {
		input := frozenFinalInput(t, empty)
		before, _ := json.Marshal(input)
		e, err := shadow.BuildGoFinalEvidenceV2(input, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		wantUnit := "%"
		if empty {
			wantUnit = ""
		}
		original, _ := contract.CanonicalJSONV2(input.Event)
		cloned, _ := contract.CanonicalJSONV2(e.GoEvent)
		if e.Primary.Unit != wantUnit || e.Primary.LevelID != 5 || string(original) != string(cloned) {
			t.Fatalf("lost native or normalized facts unit=%q want=%q primary=%d native_equal=%v", e.Primary.Unit, wantUnit, e.Primary.LevelID, string(original) == string(cloned))
		}
		after, _ := json.Marshal(input)
		if string(before) != string(after) {
			t.Fatal("mutated frozen caller")
		}
		e.GoEvent.LevelResults[0].Priority = 99
		e.Native.LevelResults[0].Priority = 98
		again, err := shadow.BuildGoFinalEvidenceV2(input, 1<<20)
		if err != nil || again.Native.LevelResults[0].Priority != input.Event.LevelResults[0].Priority {
			t.Fatal("caller isolation", err)
		}
	}
}

func TestFinalEvidenceV2RejectsMismatchedFacts(t *testing.T) {
	for name, change := range map[string]func(*shadow.GoFrozenEvidenceInputV2){
		"ack":          func(i *shadow.GoFrozenEvidenceInputV2) { i.ACK.Confirmed = false },
		"ack_event":    func(i *shadow.GoFrozenEvidenceInputV2) { i.ACK.EventID = "other" },
		"config":       func(i *shadow.GoFrozenEvidenceInputV2) { i.Context.ComparisonConfigDigest = strings.Repeat("b", 64) },
		"plan":         func(i *shadow.GoFrozenEvidenceInputV2) { i.Due.Identity.StrategyID = "other" },
		"schedule":     func(i *shadow.GoFrozenEvidenceInputV2) { i.Context.PlanScheduleRevision = "other" },
		"query":        func(i *shadow.GoFrozenEvidenceInputV2) { i.Context.QueryRevision = "other" },
		"slot":         func(i *shadow.GoFrozenEvidenceInputV2) { i.Context.SlotIdentity = "other" },
		"snapshot":     func(i *shadow.GoFrozenEvidenceInputV2) { i.Context.SnapshotRevision = "other" },
		"epoch":        func(i *shadow.GoFrozenEvidenceInputV2) { i.Context.ScheduleSegmentStart++ },
		"unknown_fact": func(i *shadow.GoFrozenEvidenceInputV2) { i.PrimaryEffectiveTime = strategy.EffectiveTimeFact{} },
		"fact_digest":  func(i *shadow.GoFrozenEvidenceInputV2) { i.Context.EffectiveTimeFactDigest = strings.Repeat("b", 64) },
		"requirement_digest": func(i *shadow.GoFrozenEvidenceInputV2) {
			i.Context.EffectiveTimeRequirementDigest = strings.Repeat("b", 64)
		},
		"missing_dependencies": func(i *shadow.GoFrozenEvidenceInputV2) { i.Requirements = nil },
		"incomplete":           func(i *shadow.GoFrozenEvidenceInputV2) { i.Completeness.Input = "UNAVAILABLE" },
	} {
		t.Run(name, func(t *testing.T) {
			input := frozenFinalInput(t, false)
			change(&input)
			if _, err := shadow.BuildGoFinalEvidenceV2(input, 1<<20); err == nil {
				t.Fatal("accepted mismatched facts")
			}
		})
	}
}

func rebuildFrozenEvent(t *testing.T, input *shadow.GoFrozenEvidenceInputV2) {
	t.Helper()
	g := input.Event
	e, err := contract.BuildTriggerEventV1(contract.TriggerEventBuildInputV1{EventKind: g.EventKind, TenantID: g.TenantID, BusinessID: g.BusinessID, PlanRef: g.PlanRef, RecordRef: g.RecordRef, Observed: g.Observed, LevelResults: g.LevelResults, EvaluationTime: g.EvaluationTime, DetectPlanFingerprint: g.DetectPlanFingerprint, TriggerStateFingerprint: g.TriggerStateFingerprint, ExecutionID: g.Trace.ExecutionID, MaxEvidenceBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	input.Event = *e
	input.ACK = shadow.BusinessACK{Confirmed: true, EventID: e.EventID, SemanticDigest: e.EventSemanticDigest}
}

func TestFinalEvidenceV2ValidNativeWrongFrozenPlan(t *testing.T) {
	for name, change := range map[string]func(*contract.TriggerEventV1){
		"plan_revision":      func(e *contract.TriggerEventV1) { e.PlanRef.StrategyRevision = "other" },
		"detect_fingerprint": func(e *contract.TriggerEventV1) { e.DetectPlanFingerprint = strings.Repeat("b", 64) },
		"level_fingerprint":  func(e *contract.TriggerEventV1) { e.LevelResults[0].LevelTriggerFingerprint = strings.Repeat("b", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			input := frozenFinalInput(t, false)
			change(&input.Event)
			rebuildFrozenEvent(t, &input)
			if _, err := shadow.BuildGoFinalEvidenceV2(input, 1<<20); err == nil {
				t.Fatal("accepted internally valid but differently frozen event")
			}
		})
	}
}

func TestFinalEvidenceV2Recovery(t *testing.T) {
	input := frozenFinalInput(t, true)
	input.Event.EventKind = contract.TriggerEventRecovery
	for i := range input.Event.LevelResults {
		l := &input.Event.LevelResults[i]
		l.Result = contract.LevelResultNormal
		if l.LevelID == 5 {
			l.Result = contract.LevelResultRecovery
		}
		l.DetectEvidence.DetectionResult = "NORMAL"
		l.DecisionWindow.Trigger.ObservedAnomalies = 0
		l.DecisionWindow.Recovery.ObservedConsecutiveMisses = 0
		if l.LevelID == 5 {
			l.DecisionWindow.Recovery.ObservedConsecutiveMisses = l.DecisionWindow.Recovery.RequiredConsecutiveWindows
		}
	}
	rebuildFrozenEvent(t, &input)
	e, err := shadow.BuildGoFinalEvidenceV2(input, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if e.ResultKind != contract.TriggerEventRecovery || e.Primary.Unit != "" {
		t.Fatal("lost recovery or known empty unit")
	}
	input.Completeness.Input = "PARTIAL_ACCEPTED"
	if _, err := shadow.BuildGoFinalEvidenceV2(input, 1<<20); err == nil {
		t.Fatal("partial recovery accepted")
	}
}
