// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package access

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/racedetector"
)

type definitiveSet struct {
	memberSet
	definitive bool
}

func (set definitiveSet) Definitive() bool { return set.definitive }

// recordingSink is the superseded-identity close as the access path sees
// it: what Screen answers, and every call it received.
type recordingSink struct {
	screen   bool
	screens  int
	observed []SupersededOffer
}

func (sink *recordingSink) Screen(execution.PlanIdentity) bool { sink.screens++; return sink.screen }
func (sink *recordingSink) Observe(offer SupersededOffer) {
	sink.observed = append(sink.observed, offer)
}

var scopeOutput = planOutput{strategyID: "42", revision: 3,
	identity: &contract.MonitorOutputIdentity{DimensionFields: []string{"bk_host_id", "device"}}}

func hostPlanContext(definitive bool) admission.PlanContext {
	return admission.PlanContext{TargetPlan: &admission.TargetPlanContext{
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true},
		Members:  definitiveSet{memberSet: memberSet{"101": {}}, definitive: definitive}}}
}

func scopeDropAdapter(t *testing.T, chain *admission.Chain, context admission.PlanContext, sink *recordingSink, output planOutput) (*seriesAdapter, execution.PlanIdentity) {
	t.Helper()
	_, frozen := frozenExecution(t)
	requirement := frozen.Requirements[0]
	plan := requirement.Consumers[0].Consumer.Plan
	context.StrategyID = plan.StrategyID
	return &seriesAdapter{
		consumer: &admissionConsumer{}, query: plannedQueryForTest(requirement), attemptNo: 1,
		admission: chain, scopes: planScopes{plan: context}, supersededSink: sink,
		outputs: planOutputs{plan: output}, round: 1700000060,
	}, plan
}

func deliverSeries(t *testing.T, adapter *seriesAdapter, dimensions map[string]json.RawMessage) {
	t.Helper()
	dataset := execution.NewDataset([]contract.CanonicalRecordV2{{RecordID: "record", SourceTime: 1, BusinessID: "2", Dimensions: dimensions}})
	if err := adapter.ConsumeProviderSeries(context.Background(), execution.ProviderSeriesBatch{
		PhysicalQuery: adapter.query.Spec.Digest, CompletionRef: "result", Dataset: dataset,
		Delivery: execution.SeriesDelivery{PhysicalQuery: adapter.query.Spec.Digest,
			QueryRevision: adapter.query.Spec.PlanFacts.QueryRevision, Series: 1, Records: 1, Digest: "digest"},
	}); err != nil {
		t.Fatalf("consume: %v", err)
	}
}

func targetChain() *admission.Chain {
	return admission.NewChain([]admission.Fuller{admission.IdentityFuller{}, knownHostFuller{}},
		[]admission.Filter{admission.TargetScopeFilter{}, admission.TargetPlanFilter{}})
}

// knownHostFuller stands for a host cache that knows every host a record names
// by id, as the CMDB fuller records it: a rejection of such a host is decided
// on facts that were there. A host the cache did not know is never a verdict
// (decision-024), so the fixtures that mean "a host outside the target" say
// the cache knows it.
type knownHostFuller struct{}

func (knownHostFuller) Name() string { return "known_hosts" }

func (knownHostFuller) Fill(_ map[string]json.RawMessage, facts *admission.Facts) {
	if facts.HostNaming.IDKey != "" {
		facts.HostResolved = true
	}
}

func hostDims(id string) map[string]json.RawMessage {
	return map[string]json.RawMessage{"bk_host_id": json.RawMessage(`"` + id + `"`), "device": json.RawMessage(`"sda"`)}
}

// stateFuller resolves every host into one operational state, standing in
// for the host cache.
type stateFuller struct{ state string }

func (stateFuller) Name() string { return "state" }
func (fuller stateFuller) Fill(_ map[string]json.RawMessage, facts *admission.Facts) {
	facts.HostResolved = true
	facts.HostState = fuller.state
}

// The outputs follow the frozen Plan and the queries that feed it: its
// strategy reference and output identity, no fingerprint where the
// evaluator would compute none, and multi-input when more than one
// requirement feeds it.
func TestPlanOutputsFollowTheFrozenPlan(t *testing.T) {
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "2002"}
	compiled := compilePlanWithTargetPlan(t, "2002", nil)
	consumer := execution.DataRequirementConsumer{Consumer: execution.ConsumerRef{Plan: identity}}
	one := []PlannedQuery{{Requirements: []execution.DataRequirement{{RequirementID: "a", Consumers: []execution.DataRequirementConsumer{consumer}}}}}
	outputs := buildPlanOutputs([]execution.DuePlan{{Identity: identity, CompiledPlan: compiled, ScheduleSpec: execution.ScheduleSpec{EvaluationIntervalSeconds: 1200}}}, one)
	output, found := outputs[identity]
	if !found || output.multiInput || output.strategyID != compiled.StrategyRef().StrategyID || output.revision != int64(compiled.StrategyRef().SnapshotRevision) ||
		output.intervalSeconds != 1200 {
		t.Fatalf("output = %+v", output)
	}
	if compiled.StrategyRef().SnapshotRevision == 0 && output.fingerprint("2", map[string]json.RawMessage{"host": json.RawMessage(`"a"`)}) != "" {
		t.Fatal("a Plan without a frozen revision was given a fingerprint the evaluator never computes")
	}
	two := append(one, PlannedQuery{Requirements: []execution.DataRequirement{{RequirementID: "b", Consumers: []execution.DataRequirementConsumer{consumer}}}})
	if !buildPlanOutputs([]execution.DuePlan{{Identity: identity, CompiledPlan: compiled}}, two)[identity].multiInput {
		t.Fatal("a Plan fed by two requirements was not marked multi-input")
	}
}

// A target rejection is a rejection and nothing more: the series is not
// evaluated, counted rejected by its filter, withheld as outside the target,
// and nothing is offered to any close - not under the fingerprint its alert
// reads, and not under its own for a host CMDB overlays. The alerts of a
// target that left the scope are the control leader's to close, per
// strategy.
func TestATargetRejectionOffersNothingAndIsStillRejected(t *testing.T) {
	sink := &recordingSink{screen: true}
	output := planOutput{strategyID: "42", revision: 3,
		identity: &contract.MonitorOutputIdentity{DimensionFields: []string{"bk_host_id", "bk_target_ip", "bk_target_cloud_id"}}}
	chain := admission.NewChain([]admission.Fuller{admission.IdentityFuller{}, targetOverlayFuller{}},
		[]admission.Filter{admission.TargetScopeFilter{}, admission.TargetPlanFilter{}})
	adapter, _ := scopeDropAdapter(t, chain, hostPlanContext(true), sink, output)
	var observed []string
	adapter.observe = func(filter, result, reason string) { observed = append(observed, filter+"/"+result+"/"+reason) }
	record := map[string]json.RawMessage{"bk_host_id": json.RawMessage(`"102"`), "bk_target_ip": json.RawMessage(`"198.51.100.9"`),
		"bk_target_cloud_id": json.RawMessage(`"0"`)}
	deliverSeries(t, adapter, record)
	if sink.screens != 0 || len(sink.observed) != 0 || adapter.supersededScreens != nil {
		t.Fatalf("screens %d observed %+v, want nothing offered for a target rejection", sink.screens, sink.observed)
	}
	want := "target_plan/rejected/" + admission.TargetPlanReasonOutOfTarget
	if len(observed) != 1 || observed[0] != want {
		t.Fatalf("admission observed %v, want only %s", observed, want)
	}
	completion := adapter.reconcileCompletion(execution.ProviderCompletion{Completeness: execution.CompletenessFull, DataState: execution.DataStateData})
	if completion.DataState != execution.DataStateEmpty || completion.Withheld != 1 || completion.WithheldOutsideTarget != 1 {
		t.Fatalf("completion %+v, want the series withheld as outside the target", completion)
	}
}

// A Plan the close screens out - its strategy has no open alert, the common
// case - costs nothing per record: no hash, no allocation, no call into the
// sink past one Screen per Plan per query.
func TestAScreenedOutPlanDoesNoPerRecordWork(t *testing.T) {
	sink := &recordingSink{}
	identity := execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "42"}
	adapter := &seriesAdapter{supersededSink: sink, outputs: planOutputs{identity: scopeOutput}}
	facts := admission.Facts{Dimensions: hostDims("102"), HostOverlay: map[string]json.RawMessage{"device": json.RawMessage(`"sdb"`)}}
	offer := func() { adapter.reportSuperseded(identity, &facts) }
	offer()
	if allocs := testing.AllocsPerRun(1000, offer); !racedetector.Enabled && allocs != 0 {
		t.Fatalf("a screened-out Plan allocates %.1f per record, want 0", allocs)
	}
	if sink.screens != 1 || len(sink.observed) != 0 {
		t.Fatalf("screens %d observed %d, want one screen and no offer", sink.screens, len(sink.observed))
	}
}

// With no sink the admission step offers nothing and keeps nothing.
func TestWithoutASinkTheAdmissionStepKeepsNothing(t *testing.T) {
	chain := admission.NewChain([]admission.Fuller{admission.IdentityFuller{}, overlayFuller{}}, nil)
	adapter, _ := scopeDropAdapter(t, chain, admission.PlanContext{}, nil, supersededOutput)
	adapter.supersededSink = nil
	deliverSeries(t, adapter, supersededRecord)
	if adapter.supersededScreens != nil {
		t.Fatalf("screens %v, want none without a sink", adapter.supersededScreens)
	}
}

// An offer carries when its strategy's next observation can come: the
// Plan's period, and how long after its round the Slot was frozen to read -
// the close waits that long for the second, not a fixed half hour.
func TestAnOfferCarriesThePeriodAndTheReadOffset(t *testing.T) {
	sink := &recordingSink{screen: true}
	output := supersededOutput
	output.intervalSeconds = 1200
	chain := admission.NewChain([]admission.Fuller{admission.IdentityFuller{}, overlayFuller{}}, nil)
	adapter, plan := scopeDropAdapter(t, chain, admission.PlanContext{}, sink, output)
	adapter.query.ReadyAtUnixMilli = (adapter.round + 8*60) * 1000
	deliverSeries(t, adapter, supersededRecord)
	own := output.fingerprint(plan.BusinessID, supersededRecord)
	if len(sink.observed) != 1 || sink.observed[0].Fingerprint != own || sink.observed[0].Plan != plan ||
		sink.observed[0].StrategyRevision != 3 || sink.observed[0].Round != 1700000060 ||
		sink.observed[0].IntervalSeconds != 1200 || sink.observed[0].ReadOffset != 8*time.Minute {
		t.Fatalf("observed %+v, want the record's own fingerprint %s, revision 3, round 1700000060, the 20 min period and an 8 min read offset",
			sink.observed, own)
	}
}
