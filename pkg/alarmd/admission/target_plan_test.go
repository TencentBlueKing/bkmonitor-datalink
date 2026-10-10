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

type memberSet map[string]struct{}

func (set memberSet) Contains(key string) bool { _, found := set[key]; return found }

func recordFacts(dimensions map[string]string) *Facts {
	raw := make(map[string]json.RawMessage, len(dimensions))
	for name, value := range dimensions {
		encoded, _ := json.Marshal(value)
		raw[name] = encoded
	}
	return &Facts{Dimensions: raw}
}

// The filter admits a record whose key, read by the Plan's identity, is
// among the resolved members, and names the other outcomes apart: no key,
// outside the members, and no resolution at all - which admits nothing, not
// everything, because a target plan whose members are not known is still a
// target. A Plan without a target plan is not this filter's to decide.
func TestTheTargetPlanFilterAdmitsByTheRecordKeyAndNeverWithoutAResolution(t *testing.T) {
	node := contract.TargetPlanIdentityV1{Dimensions: []string{"bcs_cluster_id", "node"}}
	members := memberSet{"cluster-a|node-01": {}, "cluster-b|node-02": {}}
	resolved := PlanContext{TargetPlan: &TargetPlanContext{Identity: node, Members: members}}
	for name, test := range map[string]struct {
		plan   PlanContext
		record map[string]string
		admit  bool
		reason string
	}{
		"member":                    {plan: resolved, record: map[string]string{"bcs_cluster_id": "cluster-a", "node": "node-01"}, admit: true},
		"other member":              {plan: resolved, record: map[string]string{"bcs_cluster_id": "cluster-b", "node": "node-02"}, admit: true},
		"shares one dimension each": {plan: resolved, record: map[string]string{"bcs_cluster_id": "cluster-a", "node": "node-02"}, reason: TargetPlanReasonOutOfTarget},
		"missing a dimension":       {plan: resolved, record: map[string]string{"bcs_cluster_id": "cluster-a"}, reason: TargetPlanReasonKeyMissing},
		"unresolved":                {plan: PlanContext{TargetPlan: &TargetPlanContext{Identity: node}}, record: map[string]string{"bcs_cluster_id": "cluster-a", "node": "node-01"}, reason: TargetPlanReasonUnresolved},
		"no target plan":            {plan: PlanContext{}, record: map[string]string{}, admit: true},
	} {
		t.Run(name, func(t *testing.T) {
			decision := (TargetPlanFilter{}).Admit(test.plan, recordFacts(test.record))
			if decision.Admit != test.admit || decision.Reason != test.reason {
				t.Fatalf("Admit() = %+v, want admit=%v reason=%q", decision, test.admit, test.reason)
			}
		})
	}
	// Nil facts are a record with no dimensions: no key, never a crash.
	if decision := (TargetPlanFilter{}).Admit(resolved, nil); decision.Admit || decision.Reason != TargetPlanReasonKeyMissing {
		t.Fatalf("Admit(nil facts) = %+v", decision)
	}
}

// A host-identity target reads the record's host identity, however the
// record came by it: the bk_host_id dimension, or the id the CMDB fuller
// taught a record that names its host by address. Collected metrics name
// their host by address, and a rule that read the dimension alone would
// drop every one of them under a reason that blames the writer. A record
// with no host id at all cannot be placed.
func TestAHostIdentityTargetReadsTheRecordsHostIdentityHoweverItCameByIt(t *testing.T) {
	plan := PlanContext{TargetPlan: &TargetPlanContext{Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, Members: memberSet{"101": {}}}}
	byID := recordFacts(map[string]string{"bk_host_id": "101"})
	if decision := (TargetPlanFilter{}).Admit(plan, byID); !decision.Admit {
		t.Fatalf("record naming bk_host_id 101 = %+v", decision)
	}
	byAddress := recordFacts(map[string]string{"bk_target_ip": "192.0.2.1", "bk_target_cloud_id": "0"})
	byAddress.AddHostKey("192.0.2.1|0")
	byAddress.AddHostKey("101") // what the CMDB fuller teaches it
	if decision := (TargetPlanFilter{}).Admit(plan, byAddress); !decision.Admit {
		t.Fatalf("record named by address, host id learned from CMDB = %+v; the host identity is read however it came", decision)
	}
	otherHost := recordFacts(map[string]string{"bk_target_ip": "192.0.2.2", "bk_target_cloud_id": "0"})
	otherHost.AddHostKey("192.0.2.2|0")
	otherHost.AddHostKey("202")
	if decision := (TargetPlanFilter{}).Admit(plan, otherHost); decision.Admit || decision.Reason != TargetPlanReasonOutOfTarget {
		t.Fatalf("record of another host = %+v, want out_of_target", decision)
	}
	unplaced := recordFacts(map[string]string{"bk_target_ip": "192.0.2.3", "bk_target_cloud_id": "0"})
	unplaced.AddHostKey("192.0.2.3|0") // the address alone: CMDB did not know it
	if decision := (TargetPlanFilter{}).Admit(plan, unplaced); decision.Admit || decision.Reason != TargetPlanReasonKeyMissing {
		t.Fatalf("record with no host id = %+v, want target_key_missing", decision)
	}
}

// unavailableSet is a resolution one of whose selectors could not answer:
// its members are the ones that did.
type unavailableSet struct{ memberSet }

func (unavailableSet) Unavailable() bool { return true }

// A refusal the index or the resolution could not decide says so. The record
// is refused either way - the writer's contract has a selector that cannot
// answer match nothing - but a record named by address whose host id an
// unreadable index could not teach is not the writer's or the query's
// defect, and a record outside the members of a resolution that could not
// answer is not known to be outside the target. Both stand as facts not
// read, never as the target's verdict. A resolution that answered, an
// incomplete one included, keeps out_of_target.
func TestARefusalTheIndexOrTheResolutionCouldNotDecideSaysSo(t *testing.T) {
	host := contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}
	answered := PlanContext{TargetPlan: &TargetPlanContext{Identity: host, Members: memberSet{"101": {}}}}
	indexDown := recordFacts(map[string]string{"bk_target_ip": "192.0.2.3", "bk_target_cloud_id": "0"})
	indexDown.AddHostKey("192.0.2.3|0")
	indexDown.MarkFactsUnavailable(FactsUnavailableHostIndex)
	decision := (TargetPlanFilter{}).Admit(answered, indexDown)
	if decision.Admit || decision.Reason != FactsUnavailableHostIndex {
		t.Fatalf("address-only record with the host index unreadable = %+v, want refused as %s", decision, FactsUnavailableHostIndex)
	}
	if standing := RejectionStandingOf(answered, indexDown, TargetPlanFilter{}.Name(), decision.Reason); standing != StandingCacheUnavailable {
		t.Fatalf("standing = %v, want cache unavailable", standing)
	}

	node := contract.TargetPlanIdentityV1{Dimensions: []string{"bcs_cluster_id", "node"}}
	partial := unavailableSet{memberSet{"cluster-a|node-01": {}}}
	unavailable := PlanContext{TargetPlan: &TargetPlanContext{Identity: node, Members: partial}}
	outside := recordFacts(map[string]string{"bcs_cluster_id": "cluster-a", "node": "node-09"})
	decision = (TargetPlanFilter{}).Admit(unavailable, outside)
	if decision.Admit || decision.Reason != TargetPlanReasonSelectorUnavailable {
		t.Fatalf("record outside an unavailable resolution = %+v, want refused as %s", decision, TargetPlanReasonSelectorUnavailable)
	}
	if standing := RejectionStandingOf(unavailable, outside, TargetPlanFilter{}.Name(), decision.Reason); standing != StandingCacheUnavailable {
		t.Fatalf("standing = %v, want cache unavailable", standing)
	}
	if member := (TargetPlanFilter{}).Admit(unavailable, recordFacts(map[string]string{"bcs_cluster_id": "cluster-a", "node": "node-01"})); !member.Admit {
		t.Fatalf("a member the answering selectors resolved = %+v, want admitted", member)
	}
	hostUnavailable := PlanContext{TargetPlan: &TargetPlanContext{Identity: host, Members: unavailableSet{memberSet{"101": {}}}}}
	other := recordFacts(map[string]string{"bk_host_id": "202"})
	if decision := (TargetPlanFilter{}).Admit(hostUnavailable, other); decision.Admit || decision.Reason != TargetPlanReasonSelectorUnavailable {
		t.Fatalf("host outside an unavailable resolution = %+v, want %s", decision, TargetPlanReasonSelectorUnavailable)
	}

	answeredNode := PlanContext{TargetPlan: &TargetPlanContext{Identity: node, Members: memberSet{"cluster-a|node-01": {}}}}
	if decision := (TargetPlanFilter{}).Admit(answeredNode, outside); decision.Reason != TargetPlanReasonOutOfTarget {
		t.Fatalf("record outside a resolution that answered = %+v, want out_of_target", decision)
	}
}

// The chain runs both target filters; a Plan carries one form, so exactly
// one of them decides, and the dimensions the fingerprint is derived from
// are not touched by either.
func TestBothTargetFiltersSitOnTheChainAndOnlyTheCarriedFormDecides(t *testing.T) {
	chain := NewChain([]Fuller{IdentityFuller{}}, []Filter{TargetScopeFilter{}, TargetPlanFilter{}})
	dimensions := map[string]json.RawMessage{"bk_host_id": json.RawMessage(`"101"`), "bk_target_ip": json.RawMessage(`"192.0.2.1"`)}
	before, _ := json.Marshal(dimensions)
	facts := chain.Enrich(dimensions)
	planForm := PlanContext{TargetPlan: &TargetPlanContext{Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, Members: memberSet{"102": {}}}}
	if admitted, filter, reason := chain.Admit(planForm, &facts); admitted || filter != "target_plan" || reason != TargetPlanReasonOutOfTarget {
		t.Fatalf("target plan form: Admit() = %v %s %s", admitted, filter, reason)
	}
	scopeForm := PlanContext{TargetScope: &TargetScope{Groups: []TargetScopeGroup{{Conditions: []TargetScopeCondition{{Field: TargetScopeHost, Method: TargetScopeInclude, Keys: map[string]struct{}{"101": {}}}}}}}}
	if admitted, filter, reason := chain.Admit(scopeForm, &facts); !admitted || filter != "" || reason != "" {
		t.Fatalf("target scope form: Admit() = %v %s %s", admitted, filter, reason)
	}
	after, _ := json.Marshal(dimensions)
	if string(before) != string(after) {
		t.Fatalf("dimensions changed under the filters: %s vs %s", before, after)
	}
}
