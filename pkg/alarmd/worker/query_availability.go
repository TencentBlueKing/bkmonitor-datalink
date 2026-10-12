package worker

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"

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
	// clock is the slowest primary query's budget and elapsed time.
	clock *execution.PhysicalQueryClock
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

// unavailableReason is why the primary query was unavailable, when it was.
func (e queryAvailabilityEvidence) unavailableReason() execution.ReasonCode {
	if e.availability() != execution.QueryAvailabilityUnavailable {
		return ""
	}
	return e.reason
}
