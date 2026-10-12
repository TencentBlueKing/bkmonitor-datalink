// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

// A dynamic group target is matched the way bkmonitor/utils/range/target.py
// matches it: the groups' host ids, read when the record is matched, become
// a bk_target_ip condition - a host the groups hold is in, a record naming no
// host skips the condition, and groups that hold no host leave a key that
// matches nothing, so "eq" admits nothing and "neq" everything. What Python
// cannot say and this side does is a group that was not read: Python reads it
// as empty, here only a host the groups are known to hold decides, and every
// other record is refused under its own reason, which never closes an alert.

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func dynamicGroupScope(groups ...[]contract.TargetScopeConditionV2) *TargetScope {
	wire := &contract.TargetScopeV2{}
	for _, conditions := range groups {
		wire.Groups = append(wire.Groups, contract.TargetScopeGroupV2{Conditions: conditions})
	}
	return TargetScopeFromContract(wire)
}

func groupCondition(method contract.TargetScopeMethod, ids ...string) contract.TargetScopeConditionV2 {
	return contract.TargetScopeConditionV2{Field: contract.TargetScopeDynamicGroup, Method: method,
		Keys: contract.CanonicalTargetScopeKeys(ids)}
}

// read answers the groups listed, known or not; a group it does not list was
// not read at all.
func read(groups map[string]GroupMembership) func(string) (GroupMembership, bool) {
	return func(id string) (GroupMembership, bool) {
		membership, found := groups[id]
		return membership, found
	}
}

func TestADynamicGroupTargetMatchesTheHostsItsGroupsHold(t *testing.T) {
	known := map[string]GroupMembership{
		"g1":    {HostIDs: []string{"730001"}, Known: true},
		"g2":    {HostIDs: []string{"730002"}, Known: true},
		"empty": {Known: true},
		// Read before, not read in full now: the hosts it held are kept.
		"held": {HostIDs: []string{"730001"}},
	}
	member, other, unnamed := hostFacts("192.0.2.11|0", "730001"), hostFacts("192.0.2.12|0", "730003"), Facts{}
	for _, c := range []struct {
		name     string
		scope    *TargetScope
		facts    Facts
		admit    bool
		reason   string
		standing RejectionStanding
	}{
		{"eq, a host the group holds", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("EQ", "g1")}), member, true, "", 0},
		{"eq, a host it does not", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("EQ", "g1")}), other, false, "out_of_scope", StandingDefinitive},
		{"neq, a host the group holds", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("NEQ", "g1")}), member, false, "out_of_scope", StandingDefinitive},
		{"neq, a host it does not", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("NEQ", "g1")}), other, true, "", 0},
		{"eq, the union of two groups", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("EQ", "g1", "g2")}), hostFacts("730002"), true, "", 0},
		// Python's {0}: groups that hold no host match nothing.
		{"eq, a group holding no host", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("EQ", "empty")}), member, false, "out_of_scope", StandingDefinitive},
		{"neq, a group holding no host", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("NEQ", "empty")}), member, true, "", 0},
		{"eq, a condition naming no group", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("EQ")}), member, false, "out_of_scope", StandingDefinitive},
		{"neq, a condition naming no group", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("NEQ")}), member, true, "", 0},
		// Python: a record with no host key skips a bk_target_ip condition.
		{"a record naming no host", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("EQ", "g1")}), unnamed, true, "", 0},
		// Not read: only a known member decides.
		{"eq, a group not read", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("EQ", "absent")}), member, false, "dynamic_group_unavailable", StandingCacheUnavailable},
		{"neq, a group not read", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("NEQ", "absent")}), other, false, "dynamic_group_unavailable", StandingCacheUnavailable},
		{"eq, a known member beside a group not read", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("EQ", "g1", "absent")}), member, true, "", 0},
		{"eq, another host beside a group not read", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("EQ", "g1", "absent")}), other, false, "dynamic_group_unavailable", StandingCacheUnavailable},
		{"eq, a host a group held when last read in full", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("EQ", "held")}), member, true, "", 0},
		{"neq, a host a group held when last read in full", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("NEQ", "held")}), member, false, "dynamic_group_unavailable", StandingCacheUnavailable},
		// Alternatives: one that admits admits; a refusal any of whose
		// alternatives waited on a group is not a verdict.
		{"an alternative admits beside a group not read", dynamicGroupScope(
			[]contract.TargetScopeConditionV2{groupCondition("EQ", "absent")},
			[]contract.TargetScopeConditionV2{{Field: contract.TargetScopeHost, Method: "EQ", Keys: []string{"192.0.2.12|0"}}}),
			other, true, "", 0},
		{"no alternative admits beside a group not read", dynamicGroupScope(
			[]contract.TargetScopeConditionV2{groupCondition("EQ", "absent")},
			[]contract.TargetScopeConditionV2{{Field: contract.TargetScopeHost, Method: "EQ", Keys: []string{"192.0.2.99|0"}}}),
			other, false, "dynamic_group_unavailable", StandingCacheUnavailable},
		// Beside the static exclusion the platform's cache appends to a
		// dynamic group target, all conditions of the group must hold.
		{"a member the static exclusion beside it names", dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("EQ", "g1"),
			{Field: contract.TargetScopeHost, Method: "NEQ", Keys: []string{"192.0.2.11|0"}}}), member, false, "out_of_scope", StandingDefinitive},
	} {
		t.Run(c.name, func(t *testing.T) {
			plan := PlanContext{TargetScope: c.scope.WithGroupMemberships(read(known))}
			facts := c.facts
			decision := TargetScopeFilter{}.Admit(plan, &facts)
			if decision.Admit != c.admit || decision.Reason != c.reason {
				t.Fatalf("admit %v %q, want %v %q", decision.Admit, decision.Reason, c.admit, c.reason)
			}
			if !c.admit {
				if standing := RejectionStandingOf(plan, &facts, "target_scope", decision.Reason); standing != c.standing {
					t.Fatalf("standing %v, want %v", standing, c.standing)
				}
			}
		})
	}
}

// A scope nobody read its groups for knows no member: it admits no record by
// a group, under the group's reason, and the scope it was converted from is
// not changed by a reading.
func TestADynamicGroupScopeNotReadAdmitsNoRecordByItsGroups(t *testing.T) {
	unread := dynamicGroupScope([]contract.TargetScopeConditionV2{groupCondition("EQ", "g1")})
	facts := hostFacts("730001")
	if decision := (TargetScopeFilter{}).Admit(PlanContext{TargetScope: unread}, &facts); decision.Admit || decision.Reason != "dynamic_group_unavailable" {
		t.Fatalf("an unread group decided %+v", decision)
	}
	readScope := unread.WithGroupMemberships(read(map[string]GroupMembership{"g1": {HostIDs: []string{"730001"}, Known: true}}))
	if decision := (TargetScopeFilter{}).Admit(PlanContext{TargetScope: readScope}, &facts); !decision.Admit {
		t.Fatalf("a read group refused its member: %+v", decision)
	}
	if !unread.Groups[0].Conditions[0].MembershipUnknown || len(unread.Groups[0].Conditions[0].Keys) != 0 {
		t.Fatalf("reading the groups changed the scope it was read from: %+v", unread.Groups[0].Conditions[0])
	}
	plain := scope(TargetScopeGroup{Conditions: []TargetScopeCondition{host(TargetScopeInclude, "730001")}})
	if plain.WithGroupMemberships(read(nil)) != plain {
		t.Fatal("a scope naming no group was copied")
	}
}
