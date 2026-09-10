// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

import (
	"encoding/json"
	"testing"
)

func keys(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func scope(groups ...TargetScopeGroup) *TargetScope {
	return &TargetScope{Groups: groups}
}

func topo(method TargetScopeMethod, values ...string) TargetScopeCondition {
	return TargetScopeCondition{Field: TargetScopeTopoNode, Method: method, Keys: keys(values...)}
}

func host(method TargetScopeMethod, values ...string) TargetScopeCondition {
	return TargetScopeCondition{Field: TargetScopeHost, Method: method, Keys: keys(values...)}
}

// A host sits under every node of every topology link it has. A strategy
// pointed at one of a host's several modules must still include it, which is
// the case a single-node model gets wrong.
func TestAHostInSeveralModulesMatchesATargetNamingAnyOfThem(t *testing.T) {
	facts := Facts{HostKeys: []string{"10.0.0.1|0"}}
	facts.SetTopoNodes([]string{"module|85", "set|12", "biz|7", "module|91", "set|13"})

	for _, node := range []string{"module|85", "module|91", "set|13", "biz|7"} {
		plan := PlanContext{TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{topo(TargetScopeInclude, node)}})}
		if decision := (TargetScopeFilter{}).Admit(plan, &facts); !decision.Admit {
			t.Errorf("a target naming %s excluded a host that sits under it: %v", node, decision)
		}
	}
	plan := PlanContext{TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{topo(TargetScopeInclude, "module|999")}})}
	if decision := (TargetScopeFilter{}).Admit(plan, &facts); decision.Admit {
		t.Error("a target naming an unrelated module admitted the host")
	}
}

// The production shape that produced the false positives: include a set of
// sets, then exclude some modules under them.
func TestIncludeAndExcludeInOneGroupBothHaveToHold(t *testing.T) {
	group := TargetScopeGroup{Conditions: []TargetScopeCondition{
		topo(TargetScopeInclude, "set|81", "set|87"),
		topo(TargetScopeExclude, "module|7298"),
	}}
	plan := PlanContext{TargetScope: scope(group)}

	inside := Facts{HostKeys: []string{"10.0.0.1|0"}}
	inside.SetTopoNodes([]string{"biz|9", "set|81", "module|100"})
	if decision := (TargetScopeFilter{}).Admit(plan, &inside); !decision.Admit {
		t.Errorf("a host in an included set and no excluded module was dropped: %v", decision)
	}

	excluded := Facts{HostKeys: []string{"10.0.0.2|0"}}
	excluded.SetTopoNodes([]string{"biz|9", "set|81", "module|7298"})
	if decision := (TargetScopeFilter{}).Admit(plan, &excluded); decision.Admit {
		t.Error("a host in an excluded module was admitted")
	}

	elsewhere := Facts{HostKeys: []string{"10.0.0.3|0"}}
	elsewhere.SetTopoNodes([]string{"biz|9", "set|900"})
	if decision := (TargetScopeFilter{}).Admit(plan, &elsewhere); decision.Admit {
		t.Error("a host outside every included set was admitted")
	}
}

// Groups are alternatives.
func TestAnyGroupAdmits(t *testing.T) {
	plan := PlanContext{TargetScope: scope(
		TargetScopeGroup{Conditions: []TargetScopeCondition{topo(TargetScopeInclude, "set|1")}},
		TargetScopeGroup{Conditions: []TargetScopeCondition{host(TargetScopeInclude, "10.0.0.9|0")}},
	)}
	facts := Facts{HostKeys: []string{"10.0.0.9|0"}}
	facts.SetTopoNodes([]string{"set|2"})
	if decision := (TargetScopeFilter{}).Admit(plan, &facts); !decision.Admit {
		t.Errorf("a record matching the second alternative was dropped: %v", decision)
	}
}

// This is the behaviour that decides whether alarmd alerts on machines nobody
// pointed it at. Python ends a group when a topology condition meets a record
// with no topology, so a host CMDB does not know - and a series with no host
// dimensions at all - is out of scope. Reading it the other way admits
// everything the strategy excluded.
func TestATopologyTargetRejectsRecordsWithNoTopology(t *testing.T) {
	plan := PlanContext{TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{topo(TargetScopeInclude, "set|81")}})}

	unknownHost := Facts{HostKeys: []string{"10.9.9.9|0"}}
	if decision := (TargetScopeFilter{}).Admit(plan, &unknownHost); decision.Admit {
		t.Error("a host CMDB does not know was admitted into a topology target")
	}

	noHostAtAll := Facts{}
	if decision := (TargetScopeFilter{}).Admit(plan, &noHostAtAll); decision.Admit {
		t.Error("a series with no host identity was admitted into a topology target")
	}

	// The same holds for an exclusion: an unplaceable record does not pass by
	// virtue of not being in the excluded set.
	excluding := PlanContext{TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{topo(TargetScopeExclude, "module|1")}})}
	if decision := (TargetScopeFilter{}).Admit(excluding, &noHostAtAll); decision.Admit {
		t.Error("an unplaceable record passed an exclusion it could not be evaluated against")
	}
}

// A host condition the record cannot answer is skipped, leaving the rest of
// the group to decide - the opposite of the topology rule, and also Python's.
func TestAHostConditionIsSkippedWhenTheRecordHasNoHost(t *testing.T) {
	plan := PlanContext{TargetScope: scope(TargetScopeGroup{Conditions: []TargetScopeCondition{
		host(TargetScopeInclude, "10.0.0.1|0"),
		topo(TargetScopeInclude, "set|81"),
	}})}
	facts := Facts{}
	facts.SetTopoNodes([]string{"set|81"})
	if decision := (TargetScopeFilter{}).Admit(plan, &facts); !decision.Admit {
		t.Errorf("a record with topology but no host identity was dropped by the host condition: %v", decision)
	}
}

func TestNoScopeAdmitsEverything(t *testing.T) {
	if decision := (TargetScopeFilter{}).Admit(PlanContext{}, &Facts{}); !decision.Admit {
		t.Error("a plan with no target dropped a record")
	}
}

// A scope with no alternative can only mean "matches nothing"; admitting on it
// would turn a lost scope into unrestricted alerting.
func TestAnEmptyScopeMatchesNothing(t *testing.T) {
	if decision := (TargetScopeFilter{}).Admit(PlanContext{TargetScope: &TargetScope{}}, &Facts{}); decision.Admit {
		t.Error("an empty scope admitted a record")
	}
}

// The chain runs enrichment once and admission per plan, and reports which
// filter rejected so a dropped series can be explained.
func TestChainEnrichesOnceAndNamesTheRejectingFilter(t *testing.T) {
	chain := NewChain([]Fuller{IdentityFuller{}}, []Filter{TargetScopeFilter{}})
	dimensions := map[string]json.RawMessage{
		"bk_target_ip":       json.RawMessage(`"10.0.0.1"`),
		"bk_target_cloud_id": json.RawMessage(`0`),
	}
	facts := chain.Enrich(dimensions)
	if len(facts.HostKeys) != 1 || facts.HostKeys[0] != "10.0.0.1|0" {
		t.Fatalf("identity enrichment produced %v", facts.HostKeys)
	}
	admitted, filter, reason := chain.Admit(PlanContext{TargetScope: scope(
		TargetScopeGroup{Conditions: []TargetScopeCondition{host(TargetScopeInclude, "10.0.0.2|0")}},
	)}, &facts)
	if admitted || filter != "target_scope" || reason != "out_of_scope" {
		t.Fatalf("admit = %v, %q, %q", admitted, filter, reason)
	}
}

// Dimensions arrive as raw JSON and the same field is a string in one result
// table and a number in another.
func TestIdentityEnrichmentAcceptsBothDimensionEncodings(t *testing.T) {
	chain := NewChain([]Fuller{IdentityFuller{}}, nil)
	numeric := chain.Enrich(map[string]json.RawMessage{
		"bk_target_ip":       json.RawMessage(`"10.0.0.1"`),
		"bk_target_cloud_id": json.RawMessage(`0`),
		"bk_host_id":         json.RawMessage(`12345`),
	})
	textual := chain.Enrich(map[string]json.RawMessage{
		"bk_target_ip":       json.RawMessage(`"10.0.0.1"`),
		"bk_target_cloud_id": json.RawMessage(`"0"`),
		"bk_host_id":         json.RawMessage(`"12345"`),
	})
	if len(numeric.HostKeys) != 2 || len(textual.HostKeys) != 2 {
		t.Fatalf("host keys: numeric %v textual %v", numeric.HostKeys, textual.HostKeys)
	}
	for index := range numeric.HostKeys {
		if numeric.HostKeys[index] != textual.HostKeys[index] {
			t.Fatalf("encodings disagree: %v vs %v", numeric.HostKeys, textual.HostKeys)
		}
	}
}

// Absent cloud means the direct area, matching how the cache keys hosts.
func TestAnAbsentCloudDefaultsToTheDirectArea(t *testing.T) {
	chain := NewChain([]Fuller{IdentityFuller{}}, nil)
	facts := chain.Enrich(map[string]json.RawMessage{"ip": json.RawMessage(`"10.0.0.1"`)})
	if len(facts.HostKeys) != 1 || facts.HostKeys[0] != "10.0.0.1|0" {
		t.Fatalf("host keys = %v", facts.HostKeys)
	}
}
