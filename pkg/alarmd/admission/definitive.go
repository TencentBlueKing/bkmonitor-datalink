// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// DefinitiveMembership is what a target plan resolution says about itself
// when it is asked whether its "not a member" can be acted on. Only a
// resolution that knows every one of its selectors answered from current
// facts says yes; one that is not asked (a membership without the method)
// never does.
type DefinitiveMembership interface {
	Definitive() bool
}

// RejectionStanding is what a target rejection can be acted on as.
type RejectionStanding int

const (
	// StandingNotTarget is a rejection by a filter other than the two
	// target filters: the record is still inside its target.
	StandingNotTarget RejectionStanding = iota
	// StandingDefinitive is the target's own verdict on current facts.
	StandingDefinitive
	// StandingCacheUnavailable is a rejection decided without the facts it
	// needed: an index that could not be read, a host the record names and
	// the cache did not find, a target plan whose selectors did not all
	// answer in full from fresh facts, or that nothing resolved.
	StandingCacheUnavailable
	// StandingIndefinite is a rejection that is itself not a verdict on
	// the record's place: a key or object identity that could not be built.
	StandingIndefinite
)

// RejectionStandingOf classifies a rejection for the target-scope close.
// See DefinitelyOutside for why each case is what it is.
func RejectionStandingOf(plan PlanContext, facts *Facts, filter, reason string) RejectionStanding {
	switch filter {
	case TargetScopeFilter{}.Name():
		if facts == nil || facts.HostFactsUnavailable {
			return StandingCacheUnavailable
		}
		if reason != contract.TargetScopeReasonOutOfScope {
			return StandingIndefinite
		}
		if _, named := facts.HostNaming.LookupKey(); named && !facts.HostResolved {
			return StandingCacheUnavailable
		}
		return StandingDefinitive
	case TargetPlanFilter{}.Name():
		if facts == nil || facts.HostFactsUnavailable || plan.TargetPlan == nil || plan.TargetPlan.Members == nil {
			return StandingCacheUnavailable
		}
		if reason != TargetPlanReasonOutOfTarget {
			return StandingIndefinite
		}
		membership, knows := plan.TargetPlan.Members.(DefinitiveMembership)
		if !knows || !membership.Definitive() {
			return StandingCacheUnavailable
		}
		return StandingDefinitive
	}
	return StandingNotTarget
}

// DefinitelyOutside reports whether a rejection is the monitoring target
// itself saying the record is outside it, decided on facts that were all
// there and current. It is the one question the target-scope close asks of
// a rejection, and it answers yes for far less than every rejection:
//
//   - only the two target filters count. A host turned away for its
//     operational state (a spare, a machine under repair) is still in the
//     strategy's target, and its alert is not this close's to end;
//   - only the plain out-of-target reason counts. A record whose key or
//     object identity could not be built, or a target nobody resolved, is a
//     gap on some side, not a record placed outside;
//   - the facts it was decided on must have been read. A host the record
//     names and the host cache did not find may be a host the cache has not
//     learned yet, and a target plan whose selectors did not all answer
//     from fresh facts is a lower bound, not the target.
//
// Every "no" here costs a close that could have been sent; every wrong
// "yes" closes an alert that is still in scope. The rule leans to the first.
func DefinitelyOutside(plan PlanContext, facts *Facts, filter, reason string) bool {
	return RejectionStandingOf(plan, facts, filter, reason) == StandingDefinitive
}
