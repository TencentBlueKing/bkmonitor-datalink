// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// A resolution's "not a member" can close an alert only when every selector
// answered in full from current facts. Each lesser resolution still admits
// the members it has; it just does not say anything about the rest.
func TestOnlyACompleteFreshResolutionIsDefinitive(t *testing.T) {
	selector := func(state targetplan.SelectorState, mutate func(*targetplan.SelectorResult)) targetplan.SelectorResult {
		result := targetplan.SelectorResult{Kind: targetplan.SelectorKindGroup, ID: "g", State: state,
			Members: map[string]struct{}{"101": {}}}
		if mutate != nil {
			mutate(&result)
		}
		return result
	}
	cases := []struct {
		name     string
		selector targetplan.SelectorResult
		want     bool
	}{
		{"every selector answered", selector(targetplan.SelectorOK, nil), true},
		{"a selector answered empty", selector(targetplan.SelectorOKEmpty, nil), true},
		{"a selector dropped members", selector(targetplan.SelectorIncomplete, func(r *targetplan.SelectorResult) { r.Dropped = 1 }), false},
		{"a selector could not answer", selector(targetplan.SelectorUnavailable, nil), false},
		{"a selector ate a stale snapshot", selector(targetplan.SelectorOK, func(r *targetplan.SelectorResult) { r.StaleAge = time.Second }), false},
		// decision-017 section 3.2: a missing or foreign node is a resolved
		// empty answer for its reference, not facts unread.
		{"a reference to a node the cache does not list", selector(targetplan.SelectorOKEmpty, func(r *targetplan.SelectorResult) { r.NodeMissing = true }), true},
		{"a reference to a node under another business", selector(targetplan.SelectorOKEmpty, func(r *targetplan.SelectorResult) { r.NodeForeign = true }), true},
	}
	for _, c := range cases {
		resolution := &targetplan.Resolution{Selectors: []targetplan.SelectorResult{c.selector}}
		resolution.Compose()
		target := newResolvedTarget(resolution)
		var membership admission.DefinitiveMembership = target
		if got := membership.Definitive(); got != c.want {
			t.Errorf("%s: Definitive() = %v, want %v", c.name, got, c.want)
		}
	}
	unresolved := newResolvedTarget(&targetplan.Resolution{State: targetplan.ResolutionComplete})
	unresolved.unresolved = true
	if unresolved.Definitive() {
		t.Error("a plan nothing resolved answered as definitive")
	}
}

// A plan whose static members resolved and one of whose topology references
// names a node the topology cache no longer lists - a global strategy whose
// member business was deleted - is fully resolved: the reference contributes
// nothing, and the static members' out-of-target verdicts stand.
func TestANodeMissingReferenceLeavesThePlansOtherMembershipsDefinitive(t *testing.T) {
	resolution := &targetplan.Resolution{
		Static: map[string]struct{}{"101": {}},
		Selectors: []targetplan.SelectorResult{{
			Kind: targetplan.SelectorKindTopology, ID: "7|module|85", State: targetplan.SelectorOKEmpty,
			Reason: targetplan.ReasonNodeMissing, NodeMissing: true,
		}},
	}
	resolution.Compose()
	if resolution.State != targetplan.ResolutionComplete {
		t.Fatalf("fixture: state %s", resolution.State)
	}
	if !newResolvedTarget(resolution).Definitive() {
		t.Error("a complete resolution with a node_missing reference is not definitive: every out-of-target close of the strategy is withheld")
	}
}
