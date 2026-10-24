package worker

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// queryAvailabilityEvidence folds the existing validated binding traversal.
// Duplicate consumers of a physical query do not change these set predicates.
// Aggregate PRIMARY facts and completion causes deliberately play no role.
type queryAvailabilityEvidence struct {
	primarySeen           bool
	primaryNotUnavailable bool
	available             bool
	primaryDelivered      bool
	nonBackendFailure     bool
	// reason is the first unavailable primary's own reason.
	reason execution.ReasonCode
	// primaryEmptiedOutside says a primary query came back empty only
	// because every series it returned was withheld as outside the
	// monitoring target; primaryWithheldOtherwise that a primary query
	// withheld a series for any other reason, which leaves the claim
	// unproven.
	primaryEmptiedOutside    bool
	primaryWithheldOtherwise bool
	// primaryEmptiedIdentity says a primary query came back empty because
	// every series it returned was withheld as one the target's identity
	// could not read (target_key_missing); primaryWithheldNotIdentity that a
	// primary query withheld a series for any other reason, which leaves
	// the claim unproven. withheld, withheldOutside and withheldKeyMissing
	// add up the primary queries' counts, and identitySamples keeps a few.
	primaryEmptiedIdentity                        bool
	primaryWithheldNotIdentity                    bool
	withheld, withheldOutside, withheldKeyMissing uint64
	identitySamples                               []execution.IdentitySample
	// clock is the slowest primary query's budget and elapsed time.
	clock *execution.PhysicalQueryClock
	// sentLateTimeout says a primary query timed out after being sent late
	// (timedOutSentLate).
	sentLateTimeout bool
}

// observeSentLate notes a primary query that timed out after being sent
// late.
func (e *queryAvailabilityEvidence) observeSentLate(binding execution.NamedInputBinding, sentLate bool) {
	if binding.Role == execution.InputRolePrimary && sentLate {
		e.sentLateTimeout = true
	}
}

func (e *queryAvailabilityEvidence) observe(binding execution.NamedInputBinding, physical execution.PhysicalQueryCompletion, streamed, sourceBackend bool) {
	if binding.Role != execution.InputRolePrimary {
		return
	}
	e.primarySeen = true
	if physical.Clock != nil && (e.clock == nil || physical.Clock.ElapsedMillis > e.clock.ElapsedMillis) {
		clock := *physical.Clock
		e.clock = &clock
	}
	e.nonBackendFailure = e.nonBackendFailure || (physical.Completeness == execution.CompletenessUnavailable && !sourceBackend)
	e.primaryNotUnavailable = e.primaryNotUnavailable || physical.Completeness != execution.CompletenessUnavailable
	e.primaryDelivered = e.primaryDelivered || streamed
	if physical.Withheld > physical.WithheldOutsideTarget {
		e.primaryWithheldOtherwise = true
	} else if physical.Withheld > 0 && physical.DataState == execution.DataStateEmpty {
		e.primaryEmptiedOutside = true
	}
	if physical.Withheld > physical.WithheldKeyMissing {
		e.primaryWithheldNotIdentity = true
	} else if physical.Withheld > 0 && physical.DataState == execution.DataStateEmpty {
		e.primaryEmptiedIdentity = true
	}
	e.withheld += physical.Withheld
	e.withheldOutside += physical.WithheldOutsideTarget
	e.withheldKeyMissing += physical.WithheldKeyMissing
	for _, sample := range physical.IdentitySamples {
		if len(e.identitySamples) < execution.IdentitySamplesPerQuery {
			e.identitySamples = append(e.identitySamples, sample)
		}
	}
	if physical.Completeness == execution.CompletenessUnavailable && e.reason == "" {
		e.reason = execution.AttributedReason(binding.ReasonCode, binding.UnavailableAttribution)
	}
	// FULL_EMPTY establishes availability too, even when a local consumer or
	// algorithm fails. PARTIAL needs actual usable data, not merely an empty
	// partial response. An UNAVAILABLE completion invalidates provisional data.
	e.available = e.available || physical.Completeness == execution.CompletenessFull ||
		(physical.Completeness == execution.CompletenessPartial && streamed && binding.Disposition == execution.AccessAvailable)
}

func (e queryAvailabilityEvidence) availability() execution.QueryAvailability {
	if e.available {
		return execution.QueryAvailabilityAvailable
	}
	if e.primarySeen && !e.primaryNotUnavailable && !e.primaryDelivered && !e.nonBackendFailure {
		return execution.QueryAvailabilityUnavailable
	}
	return execution.QueryAvailabilityUnknown
}

// emptiedByTarget says a FULL, EMPTY primary was empty because the target
// selected none of the data its queries returned.
func (e queryAvailabilityEvidence) emptiedByTarget(primary execution.PrimaryInputFact) bool {
	return primary.Completeness == execution.CompletenessFull && primary.DataState == execution.DataStateEmpty &&
		e.primaryEmptiedOutside && !e.primaryWithheldOtherwise
}

// emptiedByIdentity says a FULL, EMPTY primary was empty because every series
// its queries returned was one the target's identity could not read: data
// arrived and none of it built the target's key or passed its model gate.
func (e queryAvailabilityEvidence) emptiedByIdentity(primary execution.PrimaryInputFact) bool {
	return primary.Completeness == execution.CompletenessFull && primary.DataState == execution.DataStateEmpty &&
		e.primaryEmptiedIdentity && !e.primaryWithheldNotIdentity
}

// identityEvidence is what a round emptied by identity carries: the withheld
// counts by reason and the samples; nil for any other round.
func (e queryAvailabilityEvidence) identityEvidence(primary execution.PrimaryInputFact) *execution.IdentityEvidence {
	if !e.emptiedByIdentity(primary) {
		return nil
	}
	return &execution.IdentityEvidence{Withheld: e.withheld, OutsideTarget: e.withheldOutside, KeyMissing: e.withheldKeyMissing,
		Samples: append([]execution.IdentitySample(nil), e.identitySamples...)}
}

// quietWhenEmpty says a FULL, EMPTY primary is an event count of groups that
// came back with no group at all: none had an event in the range asked, the
// state an event stream spends most of its time in - quiet, not absent. An
// event count's primary is the requirement asked from earlier than its
// window (its provider lead); it counts groups when it requires identity
// columns beside the value. Nothing may have been withheld: a group the
// target turned away is the target's empty, and any other withholding
// leaves the answer unproven.
func (e queryAvailabilityEvidence) quietWhenEmpty(primary execution.PrimaryInputFact, requirements []execution.DataRequirement) bool {
	if primary.Completeness != execution.CompletenessFull || primary.DataState != execution.DataStateEmpty ||
		e.primaryEmptiedOutside || e.primaryWithheldOtherwise {
		return false
	}
	for _, requirement := range requirements {
		if requirement.Role == execution.InputRolePrimary && requirement.ProviderLeadSeconds > 0 && len(requirement.RequiredColumns) > 1 {
			return true
		}
	}
	return false
}

// unfilledWhenEmpty says a FULL, EMPTY primary is a count its provider does
// not zero-fill (DataRequirement.UnfilledCounts): a log count whose groups
// are absent, not zero, while they have no document, so the empty answer
// cannot say whether a group was quiet or its data missing. Grouped or not:
// most ungrouped log counts come back with explicit zeros when quiet, which
// are points and never empty, but some come back with no series at all for
// a range with no document, and that empty is as ambiguous as a group's.
// Withholding rules as for quiet: a group the target turned away is the
// target's empty, and any other withholding leaves the answer unproven.
func (e queryAvailabilityEvidence) unfilledWhenEmpty(primary execution.PrimaryInputFact, requirements []execution.DataRequirement) bool {
	if primary.Completeness != execution.CompletenessFull || primary.DataState != execution.DataStateEmpty ||
		e.primaryEmptiedOutside || e.primaryWithheldOtherwise {
		return false
	}
	for _, requirement := range requirements {
		if requirement.Role == execution.InputRolePrimary && requirement.UnfilledCounts {
			return true
		}
	}
	return false
}

// unavailableReason is why the primary query was unavailable, when it was.
func (e queryAvailabilityEvidence) unavailableReason() execution.ReasonCode {
	if e.availability() != execution.QueryAvailabilityUnavailable {
		return ""
	}
	return e.reason
}

// unknownReason is why a primary query failed when the failure proved
// nothing about the backend: a timeout of a query sent late, and nothing
// else.
func (e queryAvailabilityEvidence) unknownReason() execution.ReasonCode {
	if e.availability() != execution.QueryAvailabilityUnknown || !e.sentLateTimeout {
		return ""
	}
	return execution.ReasonCode(contract.ReasonQueryTimeout)
}
