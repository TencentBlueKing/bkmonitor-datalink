// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

// TargetScope is the frozen monitoring target a plan carries, in the shape the
// filter evaluates: key sets rather than ordered slices, so matching costs a
// map lookup. TargetScopeFromContract is the single conversion from the wire
// shape, which keeps a wire change from quietly altering what the predicate
// means.
type TargetScope struct {
	Groups []TargetScopeGroup
}

type TargetScopeGroup struct {
	Conditions []TargetScopeCondition
}

type TargetScopeCondition struct {
	Field  TargetScopeField
	Method TargetScopeMethod
	Keys   map[string]struct{}
}

type TargetScopeField string

const (
	TargetScopeTopoNode        TargetScopeField = "TOPO_NODE"
	TargetScopeHost            TargetScopeField = "HOST"
	TargetScopeServiceInstance TargetScopeField = "SERVICE_INSTANCE"
)

type TargetScopeMethod string

const (
	TargetScopeInclude TargetScopeMethod = "EQ"
	TargetScopeExclude TargetScopeMethod = "NEQ"
)

// TargetScopeFilter reproduces bkmonitor/utils/range/target.py's is_match.
//
// The transcription keeps two behaviours that read like accidents but decide
// real alerts:
//
//   - groups are alternatives and conditions inside a group all have to hold,
//     with the first failing condition ending that group;
//   - a topology condition on a record with no topology fails that group
//     outright. A host that CMDB does not know, or a series with no host
//     dimensions at all, is therefore out of scope rather than in it. Python
//     treats such data as invalid, and matching that matters: the opposite
//     reading alerts on everything the strategy was never pointed at.
type TargetScopeFilter struct{}

func (TargetScopeFilter) Name() string { return "target_scope" }

func (TargetScopeFilter) Admit(plan PlanContext, facts *Facts) Decision {
	scope := plan.TargetScope
	if scope == nil {
		return Decision{Admit: true}
	}
	if len(scope.Groups) == 0 {
		// A stated target that reduced to nothing matches no record. The
		// compiler rejects such a plan, so reaching here means the scope was
		// built by hand; refusing is the safe reading either way.
		return Decision{Reason: "scope_empty"}
	}
	if facts != nil && facts.HostFactsUnavailable {
		// The CMDB facts a target is matched on could not be consulted, so
		// this scope cannot be evaluated at all: with no topology no topology
		// target can match, and with nothing resolved a record never learns
		// the other identity a host target may name it by. Deciding anyway
		// would put every scoped strategy out of scope at once. The record is
		// admitted and the gap is named - the same choice the host status
		// filter makes, and for the same reason.
		return Decision{Admit: true, Reason: "host_facts_unavailable"}
	}
	for _, group := range scope.Groups {
		if group.matches(facts) {
			return Decision{Admit: true}
		}
	}
	return Decision{Reason: "out_of_scope"}
}

func (group TargetScopeGroup) matches(facts *Facts) bool {
	for _, condition := range group.Conditions {
		var candidates []string
		switch condition.Field {
		case TargetScopeTopoNode:
			if len(facts.TopoNodes) == 0 {
				// No topology means the record cannot be placed: Python ends
				// the group here rather than letting a "not equal" condition
				// pass it through.
				return false
			}
			candidates = facts.TopoNodes
		case TargetScopeHost:
			if len(facts.HostKeys) == 0 {
				// Python skips a host condition it cannot evaluate, leaving
				// the rest of the group to decide.
				continue
			}
			candidates = facts.HostKeys
		case TargetScopeServiceInstance:
			if len(facts.ServiceInstanceKeys) == 0 {
				continue
			}
			candidates = facts.ServiceInstanceKeys
		default:
			return false
		}
		hit := false
		for _, candidate := range candidates {
			if _, found := condition.Keys[candidate]; found {
				hit = true
				break
			}
		}
		if condition.Method == TargetScopeExclude {
			hit = !hit
		}
		if !hit {
			return false
		}
	}
	return true
}

// ResolvesToNoHost reports whether no host in the index can satisfy the scope.
//
// This is the one place a scope is evaluated ahead of the data, and it answers
// exactly one question: may the query be skipped entirely? It never decides
// whether a series is admitted - that stays with Admit, so there is only ever
// one implementation of the predicate that can drift.
func (scope *TargetScope) ResolvesToNoHost(candidates func(func(*Facts) bool)) bool {
	if scope == nil {
		return false
	}
	matched := false
	candidates(func(facts *Facts) bool {
		for _, group := range scope.Groups {
			if group.matches(facts) {
				matched = true
				return false
			}
		}
		return true
	})
	return !matched
}
