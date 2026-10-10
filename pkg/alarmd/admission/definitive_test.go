// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type definitiveMembers struct {
	members    map[string]struct{}
	definitive bool
}

func (m definitiveMembers) Contains(key string) bool { _, ok := m.members[key]; return ok }
func (m definitiveMembers) Definitive() bool         { return m.definitive }

type plainMembers map[string]struct{}

func (m plainMembers) Contains(key string) bool { _, ok := m[key]; return ok }

// Each row is a rejection as the chain produced it, and whether the
// target-scope close may act on it. The filters are run for real so that the
// reason is the one production would carry, not one typed in here.
func TestOnlyATargetsOwnVerdictOnCurrentFactsIsDefinitive(t *testing.T) {
	topoScope := PlanContext{TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{topo(TargetScopeInclude, "module|1")}})}
	resolvedHost := func() *Facts {
		facts := factsFor(map[string]json.RawMessage{"bk_host_id": raw(`"7"`)}, func(f *Facts) {
			f.AddHostKey("7")
			f.HostResolved = true
			f.SetTopoNodes([]string{"module|2"})
		})
		return facts
	}
	hostPlan := func(members TargetMembership) PlanContext {
		return PlanContext{TargetPlan: &TargetPlanContext{
			Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, Members: members}}
	}
	cases := []struct {
		name  string
		plan  PlanContext
		facts *Facts
		want  bool
	}{
		{"a resolved host outside the topology target", topoScope, resolvedHost(), true},
		{"the host index could not be read", topoScope, func() *Facts {
			f := resolvedHost()
			f.MarkFactsUnavailable(FactsUnavailableHostIndex)
			return f
		}(), false},
		{"the service instance index could not be read", topoScope, func() *Facts {
			f := resolvedHost()
			f.MarkFactsUnavailable(FactsUnavailableServiceInstanceIndex)
			return f
		}(), false},
		{"a host the cache has not found", topoScope, factsFor(map[string]json.RawMessage{"bk_host_id": raw(`"7"`)}, func(f *Facts) {
			f.AddHostKey("7")
		}), false},
		{"a record that names no host at all", topoScope, factsFor(map[string]json.RawMessage{"device": raw(`"sda"`)}, nil), true},
		{"an object identity that could not be built", PlanContext{TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{{
			Field: TargetScopeObjectModelInst, Method: TargetScopeInclude, Keys: keys("m|1"), IdentityFields: [][2]string{{"model", "inst"}}}}})},
			factsFor(map[string]json.RawMessage{}, nil), false},
		{"a complete fresh target plan", hostPlan(definitiveMembers{members: keys("8"), definitive: true}), resolvedHost(), true},
		{"a target plan with a selector that did not answer", hostPlan(definitiveMembers{members: keys("8")}), resolvedHost(), false},
		{"a membership that cannot say", hostPlan(plainMembers(keys("8"))), resolvedHost(), false},
		{"a target plan nobody resolved", hostPlan(nil), resolvedHost(), false},
		{"a target plan asked while the host index could not be read", hostPlan(definitiveMembers{members: keys("8"), definitive: true}), func() *Facts {
			f := resolvedHost()
			f.MarkFactsUnavailable(FactsUnavailableHostIndex)
			return f
		}(), false},
		{"a record with no key for the target plan", hostPlan(definitiveMembers{members: keys("8"), definitive: true}),
			factsFor(map[string]json.RawMessage{"device": raw(`"sda"`)}, nil), false},
	}
	chain := NewChain(nil, []Filter{TargetScopeFilter{}, TargetPlanFilter{}})
	for _, c := range cases {
		admitted, filter, reason := chain.Admit(c.plan, c.facts)
		if admitted {
			// The target filter admits what it cannot decide on, so the
			// close never sees these; asked anyway, under the plain
			// reason, the answer must still be no.
			if c.want {
				t.Fatalf("%s: the fixture was admitted", c.name)
			}
			filter, reason = TargetScopeFilter{}.Name(), contract.TargetScopeReasonOutOfScope
		}
		if got := DefinitelyOutside(c.plan, c.facts, filter, reason); got != c.want {
			t.Errorf("%s: DefinitelyOutside(%s, %s) = %v, want %v", c.name, filter, reason, got, c.want)
		}
	}
}

// A host turned away for its operational state is still inside the target:
// the host status filter's rejections are never definitive, whatever reason
// they carry.
func TestAHostStatusRejectionIsNeverDefinitive(t *testing.T) {
	filter := hostStatusFilter(t, "spare")
	facts := factsFor(map[string]json.RawMessage{"bk_host_id": raw(`"7"`)}, func(f *Facts) {
		f.HostResolved = true
		f.HostState = "spare"
	})
	decision := filter.Admit(PlanContext{}, facts)
	if decision.Admit {
		t.Fatal("the fixture host in a disabled state was admitted")
	}
	for _, reason := range []string{decision.Reason, contract.TargetScopeReasonOutOfScope, TargetPlanReasonOutOfTarget} {
		if DefinitelyOutside(PlanContext{}, facts, filter.Name(), reason) {
			t.Errorf("a host status rejection with reason %q was taken as out of target", reason)
		}
	}
}

// A rejection that is not definitive is told apart by why: decided without
// its facts (cache_unavailable) or not a verdict on the record's place at
// all (indefinite).
func TestAnIndefiniteRejectionIsToldApartFromAMissingCache(t *testing.T) {
	topoScope := PlanContext{TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{topo(TargetScopeInclude, "module|1")}})}
	objectScope := PlanContext{TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{{
		Field: TargetScopeObjectModelInst, Method: TargetScopeInclude, Keys: keys("m|1"), IdentityFields: [][2]string{{"model", "inst"}}}}})}
	hostPlan := func(members TargetMembership) PlanContext {
		return PlanContext{TargetPlan: &TargetPlanContext{
			Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, Members: members}}
	}
	unresolvedHost := factsFor(map[string]json.RawMessage{"bk_host_id": raw(`"7"`)}, func(f *Facts) { f.AddHostKey("7") })
	noKey := factsFor(map[string]json.RawMessage{"device": raw(`"sda"`)}, nil)
	cases := []struct {
		name  string
		plan  PlanContext
		facts *Facts
		want  RejectionStanding
	}{
		{"a named host the cache did not find", topoScope, unresolvedHost, StandingCacheUnavailable},
		{"an object identity that could not be built", objectScope, noKey, StandingIndefinite},
		{"a target plan key that could not be built", hostPlan(definitiveMembers{members: keys("8"), definitive: true}), noKey, StandingIndefinite},
		{"a target plan not resolved in full", hostPlan(definitiveMembers{members: keys("8")}), unresolvedHost, StandingCacheUnavailable},
		{"a target plan nobody resolved", hostPlan(nil), unresolvedHost, StandingCacheUnavailable},
	}
	chain := NewChain(nil, []Filter{TargetScopeFilter{}, TargetPlanFilter{}})
	for _, c := range cases {
		admitted, filter, reason := chain.Admit(c.plan, c.facts)
		if admitted {
			t.Fatalf("%s: the fixture was admitted", c.name)
		}
		if got := RejectionStandingOf(c.plan, c.facts, filter, reason); got != c.want {
			t.Errorf("%s: standing(%s, %s) = %d, want %d", c.name, filter, reason, got, c.want)
		}
	}
	if got := RejectionStandingOf(PlanContext{}, unresolvedHost, "host_status", "monitoring_disabled"); got != StandingNotTarget {
		t.Errorf("a host status rejection has standing %d, want not-target", got)
	}
}
