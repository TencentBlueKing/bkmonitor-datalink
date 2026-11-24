// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package access

import (
	"context"
	"encoding/json"
	"time"

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
// per plan would repeat the CMDB lookup for every one of them. Enrichment is
// for a tenant: the CMDB facts are read from that tenant's cache.
type SeriesAdmission interface {
	EnrichFor(tenant string, dimensions map[string]json.RawMessage) admission.Facts
	Admit(plan admission.PlanContext, facts *admission.Facts) (bool, string, string)
}

// TenantPrimer is a SeriesAdmission that has a tenant's facts to read before
// it can enrich that tenant's records (admission.Chain.PrimeFor). A query
// primes its Plans' tenants within its own deadline before it is sent.
type TenantPrimer interface {
	PrimeFor(ctx context.Context, tenant string)
}

// primeQueryTenants primes, within ctx, the tenant of every Plan the query
// feeds, each once, before its series are enriched.
func primeQueryTenants(ctx context.Context, admission SeriesAdmission, query PlannedQuery) {
	primer, ok := admission.(TenantPrimer)
	if !ok {
		return
	}
	primed := make(map[string]struct{})
	for _, requirement := range query.Requirements {
		for _, consumer := range requirement.Consumers {
			tenant := consumer.Consumer.Plan.TenantID
			if _, done := primed[tenant]; done {
				continue
			}
			primed[tenant] = struct{}{}
			primer.PrimeFor(ctx, tenant)
		}
	}
}

// AdmissionObserver counts decisions. It is called once per series per plan, so
// it must stay allocation-free.
type AdmissionObserver func(filter, result, reason string)

// SupersededOffer is the fingerprint an admitted series' alerts were filed
// under before its alert identity read CMDB's address over its own
// (admission.Facts.HostOverlay): its own dimensions'.
type SupersededOffer struct {
	Plan             execution.PlanIdentity
	Fingerprint      string
	StrategyRevision int64
	// Round is the Slot's evaluation time: two offers of one fingerprint are
	// two observations only when they come from two Slots.
	Round int64
	// IntervalSeconds is the Plan's evaluation period, and ReadOffset how
	// long after Round the Slot was frozen to read: together, when the next
	// observation can come.
	IntervalSeconds int64
	ReadOffset      time.Duration
}

// SupersededSink receives the offers for the superseded-identity close
// (scopeclose). Its cost shape is the point: a Plan whose strategy has no
// open alert is asked once per physical query (Screen) and none of its
// series is fingerprinted twice.
type SupersededSink interface {
	// Screen answers, once per Plan per physical query, whether the Plan's
	// series are worth an offer each.
	Screen(plan execution.PlanIdentity) bool
	// Observe receives one offer of a Plan Screen cleared.
	Observe(SupersededOffer)
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
	// intervalSeconds is the Plan's evaluation period.
	intervalSeconds int64
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
		outputs[due.Identity] = planOutput{strategyID: ref.StrategyID, revision: int64(ref.SnapshotRevision),
			identity: due.CompiledPlan.OutputIdentity(), multiInput: len(inputs[due.Identity]) > 1,
			intervalSeconds: due.ScheduleSpec.EvaluationIntervalSeconds}
	}
	return outputs
}

// groupsByTargetIP is whether the query groups by bk_target_ip: its
// dataset's identity fields, which every Plan it feeds builds its alert
// identity from under either protocol (the output identity, or the
// compatible output's dimension fields), as Python builds its target and
// tags from the strategy's agg_dimension. Those Plans are the ones whose
// identity Python's rewrite of that dimension to CMDB's address reaches.
func (query PlannedQuery) groupsByTargetIP() bool {
	for _, field := range query.Spec.PlanFacts.Normalization.DatasetContract.IdentityFields {
		if field == "bk_target_ip" {
			return true
		}
	}
	return false
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

// reportSuperseded hands the close the fingerprint an admitted series' alerts
// were filed under before its alert identity read CMDB's address: its own
// dimensions', when that is not the one its alert reads now. The close asks
// its open alert copy whether the consumer still holds an alert under it,
// and closes it under the decision superseded_identity: nothing
// evaluates that alert any more, whatever the series decides now. A Plan
// the close screens out, one fed by several inputs, or one without a
// fingerprinted alert hands nothing. What the close takes in is bounded and
// counted there: past its entry bound an observation is refused as
// memory_full and observed again by a later Slot.
func (adapter *seriesAdapter) reportSuperseded(identity execution.PlanIdentity, facts *admission.Facts) {
	if adapter.supersededSink == nil {
		return
	}
	output := adapter.outputs[identity]
	if output.multiInput {
		return
	}
	offered, screened := adapter.supersededScreens[identity]
	if !screened {
		if adapter.supersededScreens == nil {
			adapter.supersededScreens = make(map[execution.PlanIdentity]bool, 1)
		}
		offered = adapter.supersededSink.Screen(identity)
		adapter.supersededScreens[identity] = offered
	}
	if !offered {
		return
	}
	own := output.fingerprint(identity.BusinessID, facts.Dimensions)
	if own == "" {
		return
	}
	if output.fingerprint(identity.BusinessID, alertedDimensions(facts)) == own {
		return
	}
	var readOffset time.Duration
	if ready := adapter.query.ReadyAtUnixMilli; ready > 0 {
		readOffset = max(0, time.Duration(ready-adapter.round*1000)*time.Millisecond)
	}
	adapter.supersededSink.Observe(SupersededOffer{Plan: identity, Fingerprint: own, StrategyRevision: output.revision,
		Round: adapter.round, IntervalSeconds: output.intervalSeconds, ReadOffset: readOffset})
}

// alertedDimensions are a record's dimensions as its alert identity reads
// them: its own, with its host overlay written over them where there is one
// (evaluation's alertDimensions).
func alertedDimensions(facts *admission.Facts) map[string]json.RawMessage {
	if len(facts.HostOverlay) == 0 {
		return facts.Dimensions
	}
	alerted := make(map[string]json.RawMessage, len(facts.Dimensions)+len(facts.HostOverlay))
	for key, value := range facts.Dimensions {
		alerted[key] = value
	}
	for key, value := range facts.HostOverlay {
		alerted[key] = value
	}
	return alerted
}

// planScopes indexes the frozen monitoring targets of the plans in one
// execution. It is built once per execution rather than looked up per series.
type planScopes map[execution.PlanIdentity]admission.PlanContext

func buildPlanScopes(duePlans []execution.DuePlan, targets execution.TargetMemberships, groups execution.ScopeGroupMemberships) planScopes {
	scopes := make(planScopes, len(duePlans))
	for _, due := range duePlans {
		tenant := due.Identity.TenantID
		// A dynamic group condition matches the hosts this Slot read its
		// groups to hold; one the Slot did not read knows no host.
		scope := admission.TargetScopeFromContract(due.CompiledPlan.TargetScope()).WithGroupMemberships(
			func(id string) (admission.GroupMembership, bool) {
				read, found := groups[execution.ScopeGroupRef{TenantID: tenant, GroupID: id}]
				return admission.GroupMembership{HostIDs: read.HostIDs, Known: read.Known}, found
			})
		context := admission.PlanContext{
			TenantID:    tenant,
			BusinessID:  due.Identity.BusinessID,
			StrategyID:  due.Identity.StrategyID,
			TargetScope: scope,
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
