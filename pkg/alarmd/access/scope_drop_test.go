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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scopeclose"
)

type definitiveSet struct {
	memberSet
	definitive bool
}

func (set definitiveSet) Definitive() bool { return set.definitive }

// recordingSink is the close as the access path sees it: what Screen
// answers, and every call it received.
type recordingSink struct {
	screen   string
	screens  int
	observed []ScopeDrop
	counts   map[string]int
}

func (sink *recordingSink) Screen(execution.PlanIdentity) string { sink.screens++; return sink.screen }
func (sink *recordingSink) Observe(drop ScopeDrop)               { sink.observed = append(sink.observed, drop) }
func (sink *recordingSink) Count(_ ScopeDropReporter, word string, n int) {
	if sink.counts == nil {
		sink.counts = map[string]int{}
	}
	sink.counts[word] += n
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
		admission: chain, scopes: planScopes{plan: context}, scopeSink: sink,
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
	return admission.NewChain([]admission.Fuller{admission.IdentityFuller{}}, []admission.Filter{admission.TargetScopeFilter{}, admission.TargetPlanFilter{}})
}

func hostDims(id string) map[string]json.RawMessage {
	return map[string]json.RawMessage{"bk_host_id": json.RawMessage(`"` + id + `"`), "device": json.RawMessage(`"sda"`)}
}

// A definitive target-plan rejection of a strategy the close cleared
// reaches it with the fingerprint the evaluator would have given the
// record: the monitor dedupe identity of the frozen strategy, the Plan's
// business and the record's own dimensions under the Plan's output
// identity.
func TestADefinitiveRejectionCarriesTheEvaluatorsFingerprint(t *testing.T) {
	sink := &recordingSink{}
	adapter, plan := scopeDropAdapter(t, targetChain(), hostPlanContext(true), sink, scopeOutput)
	deliverSeries(t, adapter, hostDims("101"))
	deliverSeries(t, adapter, hostDims("102"))
	flushScopeDrops(adapter.scopeSink, []*seriesAdapter{adapter}, execution.SlotIdentity{QueryGroup: "group", EvaluationTime: execution.EvaluationTime(adapter.round)}, adapter.outputs)

	want, err := contract.MonitorDedupeMD5("42", plan.BusinessID, hostDims("102"),
		contract.MonitorOutputIdentity{DimensionFields: []string{"bk_host_id", "device"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.observed) != 1 || len(sink.counts) != 0 {
		t.Fatalf("observed %+v counted %v, want only the out-of-target series observed", sink.observed, sink.counts)
	}
	drop := sink.observed[0]
	if drop.Fingerprint != want || drop.Filter != "target_plan" || drop.Reason != admission.TargetPlanReasonOutOfTarget ||
		drop.Plan != plan || drop.StrategyRevision != 3 || drop.Round != 1700000060 {
		t.Fatalf("drop = %+v, want out_of_target with fingerprint %s, revision 3, round 1700000060", drop, want)
	}
}

// Every other rejection is counted in bulk, once per word, when the query
// ends: the indefinite ones, a Plan fed by several inputs, a Plan with no
// fingerprinted output, and whatever Screen turned away.
func TestRejectionsTheCloseCannotUseAreCountedInBulk(t *testing.T) {
	cases := []struct {
		name    string
		context admission.PlanContext
		output  planOutput
		screen  string
		word    string
		screens int
	}{
		{"not resolved in full", hostPlanContext(false), scopeOutput, "", ScopeDropCacheUnavailable, 1},
		{"multi-input", hostPlanContext(true), func() planOutput { o := scopeOutput; o.multiInput = true; return o }(), "", ScopeDropFingerprintUnsupported, 1},
		// A set that cannot be judged answers first, before the
		// rejection's standing or the Plan's inputs are looked at.
		{"set unavailable before multi-input", hostPlanContext(true), func() planOutput { o := scopeOutput; o.multiInput = true; return o }(), "set_unavailable", "set_unavailable", 1},
		{"set unavailable before standing", hostPlanContext(false), scopeOutput, "set_unavailable", "set_unavailable", 1},
		{"no frozen revision", hostPlanContext(true), planOutput{strategyID: "42", identity: scopeOutput.identity}, "", ScopeDropNoFingerprint, 1},
		{"screened out", hostPlanContext(true), scopeOutput, "not_member", "not_member", 1},
	}
	for _, c := range cases {
		sink := &recordingSink{screen: c.screen}
		adapter, _ := scopeDropAdapter(t, targetChain(), c.context, sink, c.output)
		for _, id := range []string{"102", "103", "104"} {
			deliverSeries(t, adapter, hostDims(id))
		}
		if len(sink.counts) != 0 {
			t.Fatalf("%s: counted before the query ended: %v", c.name, sink.counts)
		}
		flushScopeDrops(adapter.scopeSink, []*seriesAdapter{adapter}, execution.SlotIdentity{QueryGroup: "group", EvaluationTime: execution.EvaluationTime(adapter.round)}, adapter.outputs)
		if len(sink.observed) != 0 || sink.counts[c.word] != 3 || len(sink.counts) != 1 || sink.screens != c.screens {
			t.Errorf("%s: observed %d counts %v screens %d, want 3 under %q and %d screens", c.name, len(sink.observed), sink.counts, sink.screens, c.word, c.screens)
		}
	}
}

// A host turned away by the host status filter is still inside its target;
// the close does not hear about it at all.
func TestAHostStatusRejectionIsNotReported(t *testing.T) {
	filter, installed := admission.NewHostStatusFilter([]string{"spare"})
	if !installed {
		t.Fatal("host status filter not installed")
	}
	chain := admission.NewChain([]admission.Fuller{admission.IdentityFuller{}, stateFuller{state: "spare"}},
		[]admission.Filter{filter, admission.TargetScopeFilter{}})
	sink := &recordingSink{}
	adapter, _ := scopeDropAdapter(t, chain, admission.PlanContext{TargetScope: hostScope("7")}, sink, scopeOutput)
	deliverSeries(t, adapter, map[string]json.RawMessage{"bk_host_id": json.RawMessage(`"7"`)})
	flushScopeDrops(adapter.scopeSink, []*seriesAdapter{adapter}, execution.SlotIdentity{QueryGroup: "group", EvaluationTime: execution.EvaluationTime(adapter.round)}, adapter.outputs)
	if len(sink.observed) != 0 || len(sink.counts) != 0 || sink.screens != 0 {
		t.Fatalf("sink = %+v, want the host status rejection unreported", sink)
	}
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
	outputs := buildPlanOutputs([]execution.DuePlan{{Identity: identity, CompiledPlan: compiled}}, one)
	output, found := outputs[identity]
	if !found || output.multiInput || output.strategyID != compiled.StrategyRef().StrategyID || output.revision != int64(compiled.StrategyRef().SnapshotRevision) {
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

// The zero-member path - the strategy has no open alert, the common case
// on a filtered wide table - must cost nothing per record: no hash, no
// allocation, no call into the sink (and so no lock). One Screen per Plan
// per query, one Count when the query ends.
func TestTheZeroMemberPathDoesNoPerRecordWork(t *testing.T) {
	sink := &recordingSink{screen: "not_member"}
	identity := execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "42"}
	adapter := &seriesAdapter{scopeSink: sink, outputs: planOutputs{identity: scopeOutput}}
	facts := admission.Facts{Dimensions: hostDims("102")}
	plan := admission.PlanContext{TargetScope: &admission.TargetScope{}}
	reject := func() {
		adapter.reportScopeDrop(identity, plan, &facts, "target_scope", contract.TargetScopeReasonOutOfScope)
	}
	reject()
	if allocs := testing.AllocsPerRun(1000, reject); allocs != 0 {
		t.Fatalf("zero-member path allocates %.1f per record, want 0", allocs)
	}
	if sink.screens != 1 || len(sink.observed) != 0 {
		t.Fatalf("screens %d observed %d, want one screen and no observation", sink.screens, len(sink.observed))
	}
	flushScopeDrops(adapter.scopeSink, []*seriesAdapter{adapter}, execution.SlotIdentity{QueryGroup: "group", EvaluationTime: execution.EvaluationTime(adapter.round)}, adapter.outputs)
	if sink.counts["not_member"] != 1002 {
		t.Fatalf("counts = %v, want all 1002 rejections in one bulk count", sink.counts)
	}
}

// BenchmarkReportScopeDrop measures one definitive rejection: zero_members
// is the path almost every rejection takes, members the path of a strategy
// with open alerts (hash plus the sink's lookup).
func BenchmarkReportScopeDrop(b *testing.B) {
	identity := execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "42"}
	facts := admission.Facts{Dimensions: map[string]json.RawMessage{"bk_host_id": json.RawMessage(`"102"`),
		"device": json.RawMessage(`"sda"`), "mount": json.RawMessage(`"/data"`)}}
	plan := admission.PlanContext{TargetScope: &admission.TargetScope{}}
	output := planOutput{strategyID: "42", revision: 3,
		identity: &contract.MonitorOutputIdentity{DimensionFields: []string{"bk_host_id", "device", "mount"}}}
	for _, c := range []struct{ name, screen string }{{"zero_members", "not_member"}, {"members", ""}} {
		b.Run(c.name, func(b *testing.B) {
			adapter := &seriesAdapter{scopeSink: &discardSink{screen: c.screen}, outputs: planOutputs{identity: output}}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				adapter.reportScopeDrop(identity, plan, &facts, "target_scope", contract.TargetScopeReasonOutOfScope)
			}
		})
	}
}

type discardSink struct{ screen string }

func (sink *discardSink) Screen(execution.PlanIdentity) string { return sink.screen }
func (sink *discardSink) Observe(ScopeDrop)                    {}
func (sink *discardSink) Count(ScopeDropReporter, string, int) {}

// Through Source.Execute: the query's bulk counts reach the sink when the
// query ends. A rejection counted here and never handed over would read as
// "the target turned nothing away".
func TestTheQuerysBulkCountsReachTheSinkWhenItEnds(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	frozen.DuePlans[0].CompiledPlan = compilePlanForStrategy(t, "1001", hostScopeContract("192.0.2.10|0"))
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)
	sink := &recordingSink{}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, &hostStreamingProvider{hosts: []string{"192.0.2.10", "192.0.2.98", "192.0.2.99"}},
		&recordingQueryPermits{}, Config{MinReadyDelay: time.Second, Admission: scopedChain(), ScopeDrops: sink})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return time.UnixMilli(2_000_000_000_000) }
	source.wait = func(context.Context, time.Duration) error { return nil }
	if _, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
	}, &recordingConsumer{}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The two hosts outside the target name themselves by address and no
	// host cache resolved them, so the rejection is not definitive.
	if sink.counts[ScopeDropCacheUnavailable] != 2 || len(sink.observed) != 0 {
		t.Fatalf("counts %v observed %d, want both rejections counted as cache_unavailable", sink.counts, len(sink.observed))
	}
}

// closerSink hands the access path's counts to a real close, as the
// production sink does, with the words taken as they are.
type closerSink struct {
	closer *scopeclose.Closer
	calls  int
}

func (sink *closerSink) Screen(execution.PlanIdentity) string { return "" }
func (sink *closerSink) Observe(ScopeDrop)                    {}
func (sink *closerSink) Count(reporter ScopeDropReporter, word string, n int) {
	sink.calls++
	sink.closer.Count(openalerts.StrategyKey{TenantID: reporter.Plan.TenantID, StrategyID: reporter.Plan.StrategyID},
		reporter.Instance(), int64(reporter.Slot.EvaluationTime), word, n)
}

// A retried Slot runs Execute again for the same round. Each attempt hands
// over its counts once, after all its queries ended, and the close counts
// the round once: the two rejected hosts are two, not four.
func TestARetriedQueryCountsItsRejectionsOnce(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	frozen.DuePlans[0].CompiledPlan = compilePlanForStrategy(t, "1001", hostScopeContract("192.0.2.10|0"))
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)
	sink := &closerSink{closer: scopeclose.New(scopeclose.Options{})}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, &hostStreamingProvider{hosts: []string{"192.0.2.10", "192.0.2.98", "192.0.2.99"}},
		&recordingQueryPermits{}, Config{MinReadyDelay: time.Second, Admission: scopedChain(), ScopeDrops: sink})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return time.UnixMilli(2_000_000_000_000) }
	source.wait = func(context.Context, time.Duration) error { return nil }
	for attempt := uint32(1); attempt <= 2; attempt++ {
		if _, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
			Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: attempt,
		}, &recordingConsumer{}); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	if sink.calls != 2 {
		t.Fatalf("Count called %d times over two attempts, want once per attempt", sink.calls)
	}
	if got := sink.closer.Stats()[ScopeDropCacheUnavailable]; got != 2 {
		t.Fatalf("cache_unavailable = %d after a retried round, want 2", got)
	}
}

// An attempt with several queries feeding one Plan hands over one sum per
// Plan and word: the close keeps the largest total per round, so two
// per-query reports of one round would count only the larger of them.
func TestAnAttemptHandsOverOneSumPerPlanAndWord(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "42"}
	first, second := &seriesAdapter{}, &seriesAdapter{}
	first.tallyScopeDrop(plan, ScopeDropCacheUnavailable)
	first.tallyScopeDrop(plan, ScopeDropCacheUnavailable)
	second.tallyScopeDrop(plan, ScopeDropCacheUnavailable)
	sink := &recordingSink{}
	flushScopeDrops(sink, []*seriesAdapter{first, nil, second}, execution.SlotIdentity{QueryGroup: "group", EvaluationTime: 1700000000}, nil)
	if sink.counts[ScopeDropCacheUnavailable] != 3 || len(sink.counts) != 1 {
		t.Fatalf("counts = %v, want one sum of 3", sink.counts)
	}
	flushScopeDrops(sink, []*seriesAdapter{first, second}, execution.SlotIdentity{QueryGroup: "group", EvaluationTime: 1700000000}, nil)
	if sink.counts[ScopeDropCacheUnavailable] != 3 {
		t.Fatalf("a second flush handed the same tallies over again: %v", sink.counts)
	}
}

// Two physical queries feed the same Plan in one attempt: each rejects two
// of its three hosts, so the attempt rejected four. Handed over per query,
// the second report of the same reporter and round would read as a retry
// and the close would keep two; handed over once per attempt it keeps
// four, and a retry of the whole attempt still reads four.
func TestTwoQueriesOfOnePlanAreSummedAndRetriesAreNot(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	frozen.DuePlans[0].CompiledPlan = compilePlanForStrategy(t, "1001", hostScopeContract("192.0.2.10|0"))
	second := frozen.Requirements[0]
	second.RequirementID, second.DatasetName = "secondary", "secondary"
	second.RelativeWindow.StartOffsetSeconds = -120
	frozen.Requirements = append(frozen.Requirements, second)
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)
	sink := &closerSink{closer: scopeclose.New(scopeclose.Options{})}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, &hostStreamingProvider{hosts: []string{"192.0.2.10", "192.0.2.98", "192.0.2.99"}},
		&recordingQueryPermits{}, Config{MinReadyDelay: time.Second, Admission: scopedChain(), ScopeDrops: sink})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return time.UnixMilli(2_000_000_000_000) }
	source.wait = func(context.Context, time.Duration) error { return nil }
	for attempt := uint32(1); attempt <= 2; attempt++ {
		completion, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
			Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: attempt,
		}, &recordingConsumer{})
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if len(completion.PhysicalQueries) != 2 {
			t.Fatalf("fixture ran %d physical queries, want 2", len(completion.PhysicalQueries))
		}
		if got := sink.closer.Stats()[ScopeDropCacheUnavailable]; got != 4 {
			t.Fatalf("after attempt %d cache_unavailable = %d, want 4", attempt, got)
		}
	}
}

// The reporter instance tells apart the Query Group, the business and the
// shard, and nothing else: the round and the strategy travel beside it.
func TestTheReporterInstanceNamesQueryGroupBusinessAndShard(t *testing.T) {
	base := ScopeDropReporter{Slot: execution.SlotIdentity{QueryGroup: "group", EvaluationTime: 1700000000},
		Plan: execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "42"}}
	retry := base
	retry.Slot.EvaluationTime = 1700000060
	if base.Instance() != retry.Instance() {
		t.Fatal("the round changed the instance")
	}
	for name, mutate := range map[string]func(*ScopeDropReporter){
		"query group": func(r *ScopeDropReporter) { r.Slot.QueryGroup = "other" },
		"business":    func(r *ScopeDropReporter) { r.Plan.BusinessID = "3" },
		"shard":       func(r *ScopeDropReporter) { r.Shard = execution.ShardRef{Dimension: "host", Index: 1, Count: 2} },
	} {
		other := base
		mutate(&other)
		if other.Instance() == base.Instance() {
			t.Errorf("another %s reads as the same reporter", name)
		}
	}
}

// With no sink the admission step reports nothing and keeps nothing: no
// screen, no tally, whatever the target turned away.
func TestWithoutASinkTheAdmissionStepKeepsNothing(t *testing.T) {
	adapter, _ := scopeDropAdapter(t, targetChain(), hostPlanContext(true), &recordingSink{}, scopeOutput)
	adapter.scopeSink = nil
	for _, id := range []string{"102", "103"} {
		deliverSeries(t, adapter, hostDims(id))
	}
	if adapter.scopeScreens != nil || adapter.scopeTallies != nil {
		t.Fatalf("screens %v tallies %v, want none without a sink", adapter.scopeScreens, adapter.scopeTallies)
	}
}
