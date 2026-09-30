// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

import (
	"encoding/json"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

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
	// IdentityFields are the (model, instance) dimension pairs an
	// OBJECT_MODEL_INST record is identified by; see the contract.
	IdentityFields [][2]string
}

type TargetScopeField string

const (
	TargetScopeTopoNode        TargetScopeField = TargetScopeField(contract.TargetScopeTopoNode)
	TargetScopeHost            TargetScopeField = TargetScopeField(contract.TargetScopeHost)
	TargetScopeServiceInstance TargetScopeField = TargetScopeField(contract.TargetScopeServiceInstance)
	TargetScopeObjectModelInst TargetScopeField = TargetScopeField(contract.TargetScopeObjectModelInst)
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
//
// Which attribute a condition reads, and what its absence does, is the
// contract's attribute table; the matcher has no case per field. The one
// thing it adds to the table is a name for the rejections that are never a
// legitimate "outside the target": a record that could not build the object
// identity its strategy filters on, and a record whose identity the target
// did not name. Both are counted under their own reason and, through the
// Reporter, reported with the two sides that disagreed, so that closing the
// gap is a reading of the report rather than a second investigation.
type TargetScopeFilter struct {
	// Reporter receives the samples behind object-identity rejections. Nil
	// means the rejections are still counted, only not described.
	Reporter *IdentityReporter
}

func (TargetScopeFilter) Name() string { return "target_scope" }

func (filter TargetScopeFilter) Admit(plan PlanContext, facts *Facts) Decision {
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
	if facts == nil {
		facts = &Facts{}
	}
	if facts.HostFactsUnavailable {
		// The CMDB facts a target is matched on could not be consulted, so
		// this scope cannot be evaluated at all: with no topology no topology
		// target can match, and with nothing resolved a record never learns
		// the other identity a host target may name it by. Deciding anyway
		// would put every scoped strategy out of scope at once. The record is
		// admitted and the gap is named - the same choice the host status
		// filter makes, and for the same reason.
		return Decision{Admit: true, Reason: facts.FactsUnavailableReason()}
	}
	var trace matchTrace
	for _, group := range scope.Groups {
		if group.matches(facts, &trace) {
			if trace.objectIdentityHit && filter.Reporter != nil {
				filter.Reporter.Admitted(plan)
			}
			return Decision{Admit: true}
		}
	}
	reason := trace.reason()
	if filter.Reporter != nil {
		switch reason {
		case contract.TargetScopeReasonObjectIdentityMissing:
			filter.Reporter.Missing(plan, trace.failed.IdentityFields, facts.Dimensions)
		case contract.TargetScopeReasonObjectIdentityUnmatched:
			filter.Reporter.Unmatched(plan, trace.candidates, trace.failed.Keys)
		}
	}
	return Decision{Reason: reason}
}

// matchTrace remembers why the alternatives failed, so a rejection can be
// named after the attribute that decided it when there is only one such
// attribute. It is a value on the caller's stack: matching runs once per
// series per plan and must not allocate to explain itself.
type matchTrace struct {
	// failed is the condition that ended the most recent failing group, and
	// absent says whether it ended it for lack of candidates rather than for
	// candidates that did not match.
	failed *TargetScopeCondition
	absent bool
	// candidates are the values the failed condition compared, kept for the
	// report. They are the slice the matcher already built, not a copy.
	candidates []string
	// uniform stays true while every failing group ended on the same field
	// for the same kind of reason; it is what allows the rejection to carry
	// that field's reason instead of the plain one.
	uniform  bool
	failures int
	// objectIdentityHit records that the group that matched did so through
	// an OBJECT_MODEL_INST condition, which the reporter needs to tell a
	// target that never matches from one that merely rejects some records.
	// It is set only by a group that matched as a whole: an object identity
	// that hit inside a group another condition then failed proves nothing
	// about the target, and counting it would silence the unmatched report
	// for a window.
	objectIdentityHit bool
}

func (trace *matchTrace) fail(condition *TargetScopeCondition, absent bool, candidates []string) {
	if trace.failures == 0 {
		trace.uniform = true
	} else if trace.failed == nil || trace.failed.Field != condition.Field ||
		trace.failed.Method != condition.Method || trace.absent != absent {
		trace.uniform = false
	}
	trace.failures++
	trace.failed, trace.absent, trace.candidates = condition, absent, candidates
}

// reason is the bounded rejection reason: the deciding attribute's own when
// every alternative failed on it the same way, and out_of_scope otherwise.
//
// A mismatch reason is only claimed for an inclusion. A record that failed an
// exclusion was recognised - its identity is in the excluded set - and there
// is nothing about its representation to report; it is simply outside the
// target, whatever the attribute.
func (trace *matchTrace) reason() string {
	if !trace.uniform || trace.failed == nil {
		return contract.TargetScopeReasonOutOfScope
	}
	attribute, known := contract.TargetScopeAttributeFor(contract.TargetScopeField(trace.failed.Field))
	if !known {
		return contract.TargetScopeReasonOutOfScope
	}
	reason := ""
	switch {
	case trace.absent:
		reason = attribute.AbsenceReason
	case trace.failed.Method == TargetScopeInclude:
		reason = attribute.MismatchReason
	}
	if reason == "" {
		return contract.TargetScopeReasonOutOfScope
	}
	return reason
}

func (group TargetScopeGroup) matches(facts *Facts, trace *matchTrace) bool {
	objectIdentityHit := false
	for index := range group.Conditions {
		condition := &group.Conditions[index]
		attribute, known := contract.TargetScopeAttributeFor(contract.TargetScopeField(condition.Field))
		if !known {
			// A condition nobody can evaluate must not widen the target.
			trace.fail(condition, true, nil)
			return false
		}
		var candidates []string
		switch attribute.Source {
		case contract.TargetScopeSourceDimensionPairs:
			candidates = objectIdentityKeys(facts, condition.IdentityFields)
		default:
			candidates = facts.Candidates(attribute.Attribute)
		}
		if len(candidates) == 0 {
			switch attribute.Absence {
			case contract.TargetScopeAbsenceSkipCondition:
				// Python skips a condition it cannot evaluate, leaving the
				// rest of the group to decide.
				continue
			default:
				// No candidate means the record cannot be placed: Python ends
				// the group here rather than letting a "not equal" condition
				// pass it through.
				trace.fail(condition, true, nil)
				return false
			}
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
			trace.fail(condition, false, candidates)
			return false
		}
		if attribute.Source == contract.TargetScopeSourceDimensionPairs {
			objectIdentityHit = true
		}
	}
	trace.objectIdentityHit = objectIdentityHit
	return true
}

// objectIdentityKeys builds the "model|instance" keys a record is identified
// by, one per dimension pair it carries with both values present. It is
// Python's _build_object_model_target_key over iter_object_model_field_pairs:
// a pair with either value missing yields nothing, and a record that yields
// nothing on every pair has no object identity at all.
//
// A record carries one object identity per representation, so the result is
// normally one key; it is a slice because a strategy may name several pairs
// and the record may answer more than one of them.
func objectIdentityKeys(facts *Facts, pairs [][2]string) []string {
	if facts == nil || len(facts.Dimensions) == 0 {
		return nil
	}
	var keys []string
	for _, pair := range pairs {
		model := dimensionText(facts.Dimensions, pair[0])
		if model == "" {
			continue
		}
		instance := dimensionText(facts.Dimensions, pair[1])
		if instance == "" {
			continue
		}
		key := model + "|" + instance
		duplicate := false
		for _, existing := range keys {
			if existing == key {
				duplicate = true
				break
			}
		}
		if !duplicate {
			keys = append(keys, key)
		}
	}
	return keys
}

// ResolvesToNoHost reports whether no host in the index can satisfy the scope.
//
// This is the one place a scope is evaluated ahead of the data, and it answers
// exactly one question: may the query be skipped entirely? It never decides
// whether a series is admitted - that stays with Admit, so there is only ever
// one implementation of the predicate that can drift.
//
// It can only be answered for a scope whose every condition reads facts a
// host carries. A condition built from the record's own dimensions has no
// candidate on any host, and its absence fails the group, so evaluating such
// a scope against hosts would answer "no host can satisfy it" for every
// object-model target and skip every one of their queries. For those the
// answer is false: not "some host can", but "this cannot be known ahead of
// the data".
func (scope *TargetScope) ResolvesToNoHost(candidates func(func(*Facts) bool)) bool {
	if scope == nil || !scope.decidableFromHosts() {
		return false
	}
	matched := false
	candidates(func(facts *Facts) bool {
		var trace matchTrace
		for _, group := range scope.Groups {
			if group.matches(facts, &trace) {
				matched = true
				return false
			}
		}
		return true
	})
	return !matched
}

// decidableFromHosts reports whether every condition in the scope reads an
// attribute a host fact can carry, so that evaluating the scope against the
// host index is evaluating it at all.
func (scope *TargetScope) decidableFromHosts() bool {
	for _, group := range scope.Groups {
		for _, condition := range group.Conditions {
			attribute, known := contract.TargetScopeAttributeFor(contract.TargetScopeField(condition.Field))
			if !known || attribute.Source != contract.TargetScopeSourceFacts {
				return false
			}
		}
	}
	return true
}

// dimensionNames lists a record's dimension names for a report. Names are
// coordinates, not payload: they say what the data is keyed by, which is
// exactly what a report about a missing identity has to show.
func dimensionNames(dimensions map[string]json.RawMessage) []string {
	names := make([]string, 0, len(dimensions))
	for name := range dimensions {
		names = append(names, name)
	}
	return names
}
