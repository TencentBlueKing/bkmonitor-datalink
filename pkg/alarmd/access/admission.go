// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package access

import (
	"encoding/json"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// SeriesAdmission is the access-path decision Python makes in its filter chain:
// a series is evaluated for a plan only if it falls inside that strategy's
// monitoring target.
//
// It is split in two because enrichment is per series and admission is per
// plan. One series commonly feeds several plans, and deriving its facts once
// per plan would repeat the CMDB lookup for every one of them.
type SeriesAdmission interface {
	Enrich(dimensions map[string]json.RawMessage) admission.Facts
	Admit(plan admission.PlanContext, facts *admission.Facts) (bool, string, string)
}

// AdmissionObserver counts decisions. It is called once per series per plan, so
// it must stay allocation-free.
type AdmissionObserver func(filter, result, reason string)

// ScopeDrop is one definitive target rejection the close has asked to see
// one by one: which Plan, the fingerprint the evaluator would have given the
// record, and the Slot. Only a Plan whose strategy has open alerts to judge
// against reaches this; every other rejection is counted in bulk (see
// ScopeDropSink).
type ScopeDrop struct {
	Plan             execution.PlanIdentity
	Filter, Reason   string
	Fingerprint      string
	StrategyRevision int64
	// Round is the Slot's evaluation time: two drops of one fingerprint are
	// two observations only when they come from two Slots.
	Round int64
}

// The words the access path counts rejections under by itself, before the
// sink is asked anything. The sink maps them to its own outcomes.
const (
	// ScopeDropIndefinite is a rejection that is itself not a verdict on
	// the record's place: a key or object identity that could not be built,
	// a target nobody resolved (admission.StandingIndefinite).
	ScopeDropIndefinite = "indefinite"
	// ScopeDropCacheUnavailable is a rejection decided without the facts it
	// needed: an index not read, a host the cache did not find, a target
	// plan that did not resolve in full (admission.StandingCacheUnavailable).
	ScopeDropCacheUnavailable = "cache_unavailable"
	// ScopeDropFingerprintUnsupported is a definitive rejection of a Plan
	// fed by more than one input: the record the evaluator fingerprints is
	// built from several series, so no one series here has its fingerprint.
	ScopeDropFingerprintUnsupported = "fingerprint_unsupported"
	// ScopeDropNoFingerprint is a definitive rejection of a Plan that
	// produces no fingerprinted alert (no frozen revision or no output
	// identity), or of a record whose dimensions cannot be fingerprinted.
	ScopeDropNoFingerprint = "no_fingerprint"
)

// ScopeDropSink receives the target filters' rejections for the
// target-scope close.
//
// Its cost shape is the point. A wide table filtered by a target turns most
// of its series away every round, and almost always for a strategy with no
// open alert at all; an MD5 and two shared locks per such series is CPU and
// garbage spent to learn nothing. So the adapter asks Screen once per Plan
// per physical query, and only a Plan Screen clears is fingerprinted series
// by series; every other rejection is tallied in the adapter's own memory
// and handed over with Count once, when the query ends.
type ScopeDropSink interface {
	// Screen answers, once per Plan per physical query, "" to have the
	// Plan's definitive rejections fingerprinted and observed one by one,
	// or the word to count all of them under without that.
	Screen(plan execution.PlanIdentity) string
	// Observe receives one definitive rejection of a Plan Screen cleared.
	Observe(ScopeDrop)
	// Count reports the attempt's rejections of one Plan under a word: one
	// of the ScopeDrop words above, or one Screen returned. It is called
	// once per reporter and word per attempt, after every query of the
	// attempt has ended. A retried Slot reports as the same reporter again,
	// and the sink counts it once; different reporters - Plans of one
	// strategy under another business or shard, or in another Query Group -
	// are different rejections, and the sink sums them.
	Count(reporter ScopeDropReporter, word string, n int)
}

// ScopeDropReporter is who reports a bulk count: one Plan, as one shard,
// in one Slot. Two reports with the same reporter are two attempts of the
// same work.
type ScopeDropReporter struct {
	Slot  execution.SlotIdentity
	Plan  execution.PlanIdentity
	Shard execution.ShardRef
}

// Instance names the reporting Plan instance apart from its strategy and
// round: the Query Group, the business and the shard. Two reports of one
// strategy and round with the same instance are attempts of one piece of
// work; with different instances they are different pieces.
func (reporter ScopeDropReporter) Instance() string {
	shard := reporter.Shard
	return fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%s", reporter.Slot.QueryGroup, reporter.Plan.BusinessID,
		shard.Dimension, shard.Index, shard.Count, shard.MatcherDigest)
}

// planOutput is what the evaluator fingerprints a Plan's records under: the
// frozen strategy reference and the output identity. Held beside the scopes
// so a rejected record can be given the fingerprint the evaluator would
// have given it had the record been admitted.
type planOutput struct {
	strategyID string
	revision   int64
	identity   *contract.MonitorOutputIdentity
	// multiInput is a Plan fed by more than one requirement.
	multiInput bool
	// shard is the Plan's piece of a split strategy, for the reporter.
	shard execution.ShardRef
}

type planOutputs map[execution.PlanIdentity]planOutput

func buildPlanOutputs(duePlans []execution.DuePlan, queries []PlannedQuery) planOutputs {
	inputs := make(map[execution.PlanIdentity]map[execution.RequirementID]struct{}, len(duePlans))
	for _, query := range queries {
		for _, requirement := range query.Requirements {
			for _, consumer := range requirement.Consumers {
				plan := consumer.Consumer.Plan
				if inputs[plan] == nil {
					inputs[plan] = map[execution.RequirementID]struct{}{}
				}
				inputs[plan][requirement.RequirementID] = struct{}{}
			}
		}
	}
	outputs := make(planOutputs, len(duePlans))
	for _, due := range duePlans {
		if due.CompiledPlan == nil {
			continue
		}
		ref := due.CompiledPlan.StrategyRef()
		outputs[due.Identity] = planOutput{strategyID: ref.StrategyID, revision: int64(ref.SnapshotRevision), shard: due.Shard,
			identity: due.CompiledPlan.OutputIdentity(), multiInput: len(inputs[due.Identity]) > 1}
	}
	return outputs
}

// fingerprint is trigger.EvaluateV2's dedupe identity for a record of this
// Plan: the same function over the same strategy, business, dimensions and
// output identity, under the same condition that there is a frozen revision
// and an identity at all.
func (output planOutput) fingerprint(businessID string, dimensions map[string]json.RawMessage) string {
	if output.revision <= 0 || output.identity == nil {
		return ""
	}
	fingerprint, err := contract.MonitorDedupeMD5(output.strategyID, businessID, dimensions, *output.identity)
	if err != nil {
		return ""
	}
	return fingerprint
}

// scopeTallyKey is one bulk count the adapter keeps until its query ends.
type scopeTallyKey struct {
	plan execution.PlanIdentity
	word string
}

// reportScopeDrop hands a target filter's rejection to the target-scope
// close. Rejections by any other filter - the host status filter above all
// - are not reported: the record is still inside its target. Everything but
// a definitive rejection of a Plan the sink cleared is tallied here, with no
// lock and no hash; see ScopeDropSink.
func (adapter *seriesAdapter) reportScopeDrop(identity execution.PlanIdentity, plan admission.PlanContext, facts *admission.Facts, filter, reason string) {
	if adapter.scopeSink == nil {
		return
	}
	if filter != (admission.TargetScopeFilter{}).Name() && filter != (admission.TargetPlanFilter{}).Name() {
		return
	}
	// The screen comes first: a strategy the close cannot act on - its set
	// empty or not judgeable - is answered once per query, and every one of
	// its rejections is counted under that one word, before anything is
	// asked about the rejection itself.
	screen, screened := adapter.scopeScreens[identity]
	if !screened {
		if adapter.scopeScreens == nil {
			adapter.scopeScreens = make(map[execution.PlanIdentity]string, 1)
		}
		screen = adapter.scopeSink.Screen(identity)
		adapter.scopeScreens[identity] = screen
	}
	if screen != "" {
		adapter.tallyScopeDrop(identity, screen)
		return
	}
	switch admission.RejectionStandingOf(plan, facts, filter, reason) {
	case admission.StandingCacheUnavailable:
		adapter.tallyScopeDrop(identity, ScopeDropCacheUnavailable)
		return
	case admission.StandingIndefinite:
		adapter.tallyScopeDrop(identity, ScopeDropIndefinite)
		return
	}
	output := adapter.outputs[identity]
	if output.multiInput {
		adapter.tallyScopeDrop(identity, ScopeDropFingerprintUnsupported)
		return
	}
	fingerprint := output.fingerprint(identity.BusinessID, facts.Dimensions)
	if fingerprint == "" {
		adapter.tallyScopeDrop(identity, ScopeDropNoFingerprint)
		return
	}
	adapter.scopeSink.Observe(ScopeDrop{Plan: identity, Filter: filter, Reason: reason, Fingerprint: fingerprint,
		StrategyRevision: output.revision, Round: adapter.round})
}

func (adapter *seriesAdapter) tallyScopeDrop(plan execution.PlanIdentity, word string) {
	if adapter.scopeTallies == nil {
		adapter.scopeTallies = make(map[scopeTallyKey]int, 1)
	}
	adapter.scopeTallies[scopeTallyKey{plan: plan, word: word}]++
}

// flushScopeDrops hands one attempt's bulk counts to the sink: the tallies
// of every query of the attempt summed, one Count per Plan and word,
// whatever way the queries ended.
func flushScopeDrops(sink ScopeDropSink, adapters []*seriesAdapter, slot execution.SlotIdentity, outputs planOutputs) {
	if sink == nil {
		return
	}
	total := map[scopeTallyKey]int{}
	for _, adapter := range adapters {
		if adapter == nil {
			continue
		}
		for key, n := range adapter.scopeTallies {
			total[key] += n
		}
		adapter.scopeTallies = nil
	}
	for key, n := range total {
		sink.Count(ScopeDropReporter{Slot: slot, Plan: key.plan, Shard: outputs[key.plan].shard}, key.word, n)
	}
}

// planScopes indexes the frozen monitoring targets of the plans in one
// execution. It is built once per execution rather than looked up per series.
type planScopes map[execution.PlanIdentity]admission.PlanContext

func buildPlanScopes(duePlans []execution.DuePlan, targets execution.TargetMemberships) planScopes {
	scopes := make(planScopes, len(duePlans))
	for _, due := range duePlans {
		context := admission.PlanContext{
			TenantID:    due.Identity.TenantID,
			BusinessID:  due.Identity.BusinessID,
			StrategyID:  due.Identity.StrategyID,
			TargetScope: admission.TargetScopeFromContract(due.CompiledPlan.TargetScope()),
		}
		if plan := due.CompiledPlan.TargetPlan(); plan != nil {
			// The resolution is the Slot's, looked up by Plan; a Plan the Slot
			// did not resolve keeps Members nil and admits nothing.
			context.TargetPlan = &admission.TargetPlanContext{Identity: plan.Identity}
			if members, resolved := targets[due.Identity]; resolved && members != nil {
				context.TargetPlan.Members = members
			}
		}
		scopes[due.Identity] = context
	}
	return scopes
}

// seriesDimensions reads the dimensions of a series batch. Every record in the
// batch belongs to the same series, so the first one names it.
func seriesDimensions(dataset *execution.Dataset) map[string]json.RawMessage {
	if dataset == nil || dataset.Len() == 0 {
		return nil
	}
	record, found := dataset.Record(0)
	if !found {
		return nil
	}
	return record.Dimensions()
}
