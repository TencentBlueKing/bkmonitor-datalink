// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

import (
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// TargetMembership answers whether a record key is in the resolved target.
// The worker's resolution of a target plan for one Slot implements it; the
// filter asks it once per record and nothing else.
type TargetMembership interface {
	Contains(key string) bool
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
)

// TargetPlanFilter admits a record when its key, read by the Plan's frozen
// identity, is among the members the Slot resolved the target plan to. It
// is the second target filter on the chain, beside TargetScopeFilter, and
// the two are told apart by which frozen form the Plan carries; a Plan
// carries at most one.
type TargetPlanFilter struct{}

func (TargetPlanFilter) Name() string { return "target_plan" }

// hostIDCandidates are the host ids a record may be matched by: the
// bk_host_id dimension when it carries one, and the bare host ids among its
// host identities (the same dimension as the identity fuller records it,
// and the id the host cache taught it). Address identities are not ids.
func hostIDCandidates(facts *Facts) []string {
	candidates := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	if id := dimensionText(facts.Dimensions, contract.HostIdentityDimension); id != "" {
		candidates, seen[id] = append(candidates, id), struct{}{}
	}
	for _, candidate := range facts.HostKeys() {
		if candidate == "" || strings.Contains(candidate, contract.TargetPlanKeySeparator) {
			continue
		}
		if _, duplicate := seen[candidate]; duplicate {
			continue
		}
		candidates, seen[candidate] = append(candidates, candidate), struct{}{}
	}
	return candidates
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
			return Decision{Reason: TargetPlanReasonKeyMissing}
		}
		return Decision{Reason: TargetPlanReasonOutOfTarget}
	}
	key, ok := target.Identity.Key(func(name string) string { return dimensionText(facts.Dimensions, name) })
	if !ok {
		return Decision{Reason: TargetPlanReasonKeyMissing}
	}
	if target.Members.Contains(key) {
		return Decision{Admit: true}
	}
	return Decision{Reason: TargetPlanReasonOutOfTarget}
}
