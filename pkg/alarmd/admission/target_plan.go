// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

import (
	"encoding/json"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// TargetMembership answers whether a record key is in the resolved target.
// The worker's resolution of a target plan for one Slot implements it; the
// filter asks it once per record and nothing else.
type TargetMembership interface {
	Contains(key string) bool
}

// UnavailableMembership is what a target plan resolution says when one of
// its selectors could not answer this Slot: its members are the ones that
// did, and a key outside them is not known to be outside the target. A
// membership without the method is never unavailable.
type UnavailableMembership interface {
	Unavailable() bool
}

// outsideReason is why a record whose key the members do not hold is
// refused: outside the target when the resolution answered, and not known
// otherwise.
func outsideReason(members TargetMembership) string {
	if membership, knows := members.(UnavailableMembership); knows && membership.Unavailable() {
		return TargetPlanReasonSelectorUnavailable
	}
	return TargetPlanReasonOutOfTarget
}

// TargetPlanContext is a Plan's target in its second frozen form together
// with what this Slot resolved it to. Members nil means nothing resolved the
// target this Slot, and the filter admits nothing rather than everything: a
// target plan whose members are not known is a target, not the absence of
// one, and the old rule "no scope means no target" must not reach it.
type TargetPlanContext struct {
	Identity contract.TargetPlanIdentityV1
	Members  TargetMembership
}

// The rejection reasons of the target plan filter. They are apart from the
// target scope's because they say different things: a record whose key
// could not be built is a defect on the writing or querying side, a record
// outside the resolved members is the filter working, and a target nobody
// resolved is this process not filtering.
const (
	// TargetPlanReasonKeyMissing says the record does not carry the
	// dimensions the target's key is read from, or its model gate did not
	// hold. Never a legitimate out-of-target record.
	TargetPlanReasonKeyMissing = "target_key_missing"
	// TargetPlanReasonOutOfTarget says the record built a key and the
	// resolved members do not hold it.
	TargetPlanReasonOutOfTarget = "out_of_target"
	// TargetPlanReasonUnresolved says the Plan has a target plan and this
	// Slot has no resolution for it. Structurally unreachable once the worker
	// resolves every target-plan Plan before its records arrive; kept as a
	// refusal so that a path that skipped the resolution drops records under
	// a name instead of admitting them under none.
	TargetPlanReasonUnresolved = "target_plan_unresolved"
	// TargetPlanReasonSelectorUnavailable says the record's key is not among
	// the members and this Slot's resolution was unavailable: a selector
	// could not answer, so "not a member" is not the target's answer. The
	// record is refused all the same - the writer's contract has a selector
	// that cannot answer match nothing - but the refusal must not read as the
	// filter working. When some selectors answered and one did not, every
	// record outside the members that did is counted here: this Slot cannot
	// say any of them is outside the target.
	TargetPlanReasonSelectorUnavailable = "target_selector_unavailable"
)

// TargetPlanFilter admits a record when its key, read by the Plan's frozen
// identity, is among the members the Slot resolved the target plan to. It
// is the second target filter on the chain, beside TargetScopeFilter, and
// the two are told apart by which frozen form the Plan carries; a Plan
// carries at most one.
type TargetPlanFilter struct{}

func (TargetPlanFilter) Name() string { return "target_plan" }

// hostIDCandidates is the host id a record is matched by under the host_id
// rule (decision-017 section 2.3): its bk_host_id when it carries a true one,
// and otherwise the id the host cache found from its address - the 09-21 host
// model exemption, for collected metrics that name their host by address.
// That is the bk_host_id Python's fuller leaves in the record, which the CMDB
// fuller records as HostNaming.IDKey. One id, not a union: a record that
// carries 700002 is host 700002 whatever host its address belongs to.
func hostIDCandidates(facts *Facts) []string {
	if id := facts.HostNaming.IDKey; id != "" {
		return []string{id}
	}
	return nil
}

func (TargetPlanFilter) Admit(plan PlanContext, facts *Facts) Decision {
	target := plan.TargetPlan
	if target == nil {
		return Decision{Admit: true}
	}
	if target.Members == nil {
		return Decision{Reason: TargetPlanReasonUnresolved}
	}
	if facts == nil {
		facts = &Facts{}
	}
	if target.Identity.HostIdentity {
		// The key is the record's host identity: bk_host_id when the record
		// carries it, and every host id the fullers taught the record - the
		// one the host cache resolves from its address. Reading the dimension
		// alone would drop every collected metric that names its host by
		// address, under a reason that says the writer was at fault. The
		// address form of the identity is skipped: members are held under
		// host ids.
		placed := false
		for _, candidate := range hostIDCandidates(facts) {
			placed = true
			if target.Members.Contains(target.Identity.HostKey(candidate)) {
				return Decision{Admit: true}
			}
		}
		if !placed {
			// No host id to match by. When the host index could not be read,
			// a record naming its host by address could not be taught one:
			// that is the index, and not the writer or the query.
			if facts.HostFactsUnavailable {
				return Decision{Reason: facts.FactsUnavailableReason()}
			}
			return Decision{Reason: TargetPlanReasonKeyMissing}
		}
		return Decision{Reason: outsideReason(target.Members)}
	}
	key, ok := targetPlanRecordKey(target.Identity, facts.Dimensions)
	if !ok {
		return Decision{Reason: TargetPlanReasonKeyMissing}
	}
	if target.Members.Contains(key) {
		return Decision{Admit: true}
	}
	return Decision{Reason: outsideReason(target.Members)}
}

// targetPlanRecordKey reads a record's key under the plan's identity: its
// address, strictly and through the protocol's aliases, for an ip_cloud
// target; its dimensions as text by name for the others.
func targetPlanRecordKey(identity contract.TargetPlanIdentityV1, dimensions map[string]json.RawMessage) (string, bool) {
	if identity.Address {
		return contract.ReadIPCloudKey(func(name string) (json.RawMessage, bool) {
			raw, present := dimensions[name]
			return raw, present
		})
	}
	return identity.Key(func(name string) string { return dimensionText(dimensions, name) })
}
