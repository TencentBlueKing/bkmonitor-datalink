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
	// GroupIDs are the groups a DYNAMIC_GROUP condition names. Its Keys are
	// the host ids those groups held when the Slot read them
	// (WithGroupMemberships), and MembershipUnknown says a group among them
	// could not be read: the keys are then the members known, a lower bound.
	// Converted from the contract and not yet read, every group is unknown.
	GroupIDs          []string
	MembershipUnknown bool
}

type TargetScopeField string

const (
	TargetScopeTopoNode        TargetScopeField = TargetScopeField(contract.TargetScopeTopoNode)
	TargetScopeHost            TargetScopeField = TargetScopeField(contract.TargetScopeHost)
	TargetScopeDynamicGroup    TargetScopeField = TargetScopeField(contract.TargetScopeDynamicGroup)
	TargetScopeServiceInstance TargetScopeField = TargetScopeField(contract.TargetScopeServiceInstance)
	TargetScopeObjectModelInst TargetScopeField = TargetScopeField(contract.TargetScopeObjectModelInst)
)

// GroupMembership is one dynamic group as a Slot read it: the host ids it
// holds, and whether that is the group. Known false - the group was not read,
// is not in the cache, is not a group of hosts, or is past its staleness
// bound - means HostIDs are at most what was last known of it.
type GroupMembership struct {
	HostIDs []string
	Known   bool
}

// WithGroupMemberships is the scope with each DYNAMIC_GROUP condition's keys
// read from its groups' memberships: the union of the host ids they hold, the
// reduction Python makes at match time. A group the lookup does not answer,
// or answers as not known, leaves the condition's membership unknown, keeping
// the hosts it does know. A scope with no such condition is returned as is.
func (scope *TargetScope) WithGroupMemberships(lookup func(id string) (GroupMembership, bool)) *TargetScope {
	if scope == nil {
		return nil
	}
	resolved := scope
	for groupIndex, group := range scope.Groups {
		for conditionIndex, condition := range group.Conditions {
			if condition.Field != TargetScopeDynamicGroup {
				continue
			}
			if resolved == scope {
				resolved = scope.copyGroups()
			}
			keys := make(map[string]struct{})
			unknown := false
			for _, id := range condition.GroupIDs {
				membership, answered := GroupMembership{}, false
				if lookup != nil {
					membership, answered = lookup(id)
				}
				if !answered || !membership.Known {
					unknown = true
				}
				for _, host := range membership.HostIDs {
					keys[host] = struct{}{}
				}
			}
			target := &resolved.Groups[groupIndex].Conditions[conditionIndex]
			target.Keys, target.MembershipUnknown = keys, unknown
		}
	}
	return resolved
}

// copyGroups copies the groups and their condition slices, sharing what the
// conditions point at; only the copy's conditions are then rewritten.
func (scope *TargetScope) copyGroups() *TargetScope {
	copied := &TargetScope{Groups: make([]TargetScopeGroup, len(scope.Groups))}
	for index, group := range scope.Groups {
		copied.Groups[index].Conditions = append([]TargetScopeCondition(nil), group.Conditions...)
	}
	return copied
}

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
	// undecided records that an alternative failed on a dynamic group whose
	// membership was not read: the record may be in that group, so the
	// rejection is named after the group and not after the target.
	undecided bool
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
	if trace.undecided {
		return contract.TargetScopeReasonDynamicGroupUnavailable
	}
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
		if condition.MembershipUnknown {
			// Only a member the groups are known to hold decides, and only
			// for an inclusion: anything else depends on a group that was not
			// read, and waits for it rather than being called outside.
			if condition.Method == TargetScopeInclude && hit {
				continue
			}
			trace.undecided = true
			trace.fail(condition, false, candidates)
			return false
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
