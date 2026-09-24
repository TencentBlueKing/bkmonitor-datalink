// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// SlotInput is one Slot's no-data evidence for one Plan, as the worker holds it
// before any of it has been turned into a decision.
//
// The series are their dimensions and nothing else. Absence is decided from
// which groups reported, never from what they reported: a group that sent a
// value this item would call anomalous is present, and a group that sent
// nothing is absent, and no value anywhere changes either.
type SlotInput struct {
	Plan           *contract.EvaluationPlanV2
	EvaluationTime int64
	PeriodSeconds  int64
	Completeness   execution.Completeness
	// Series is one entry per series the Slot saw, holding that series'
	// dimensions with values already as text.
	Series []map[string]string
	// KnownHosts is the set of "address|cloud" keys the CMDB index confirmed
	// for this business, for a target roster to be intersected with.
	KnownHosts map[string]struct{}
	// HostsResolved says KnownHosts is an answer about hosts rather than the
	// absence of one. It is false when this process has no host index, and
	// then an empty KnownHosts means "not known" rather than "none".
	HostsResolved bool
	// OutOfBusiness names the groups whose host resolved to another business.
	OutOfBusiness map[string]struct{}
	// TargetResolution is the worker's resolution of the Plan's target plan
	// this Slot. Nil for a Plan without one; nil for a Plan with one means
	// nothing resolved it, which is read as unavailable and never as empty.
	TargetResolution *TargetResolution
	Memory           map[string]GroupMemory
	// TrackingHorizonSeconds and TrackingExhaustedAt are the horizon in force
	// for this Plan and the Plan-level fact the record held. They are carried
	// rather than derived: the horizon lives in the Plan the catalog froze and
	// the fact lives in the memory, and this seam is where the two meet.
	TrackingHorizonSeconds int64
	TrackingExhaustedAt    int64
}

// SlotOutcome says what happened to one no-data Plan in one Slot. Every Plan
// that detects no-data and did not error lands on exactly one of the three
// named outcomes every Slot, and that is the point: a round in which absence
// was not judged has to say so by name.
//
// It cannot be folded into the verdicts. A round skipped for budget looks
// exactly like a round whose query was not complete if both arrive as
// UNAVAILABLE - and they are not the same thing at all. A history roster only
// grows, so a Plan whose synthetic series do not fit the Slot's remaining
// mutation budget does not fit next round either: that is a permanent stop
// wearing the shape of a transient one, and the only thing that tells them
// apart is a name.
type SlotOutcome string

const (
	// OutcomeNone is the zero outcome: no no-data round happened for this Plan.
	// Either the Plan does not detect no-data at all, or the seam refused it and
	// returned an error. It is not one of the three buckets - a caller reading
	// it as a bucket would be counting refusals as rounds - and a caller must
	// check the error before reading the outcome.
	//
	// The refusing case is meant to be unreachable for a compiled Plan: the
	// catalog withholds a Plan whose roster ClassifyRoster refuses, and the seam
	// calls that same function rather than deriving the class a second time, so
	// the two locks cannot disagree. The seam still refuses rather than guess,
	// because a lock that trusts the other lock is one lock.
	OutcomeNone SlotOutcome = ""
	// OutcomeEvaluated means absence was judged this Slot.
	OutcomeEvaluated SlotOutcome = "EVALUATED"
	// OutcomeSkippedQueryNotFull means the Slot did not see the whole period,
	// so absence is not evidence and nothing was judged or remembered.
	OutcomeSkippedQueryNotFull SlotOutcome = "SKIPPED_QUERY_NOT_FULL"
	// OutcomeSkippedSlotBudget means the Slot could not carry this Plan's
	// no-data work. The evaluation never produces it - the budget is the
	// worker's fact, not this package's - and it is named here so that both
	// skips are read from one list.
	OutcomeSkippedSlotBudget SlotOutcome = "SKIPPED_SLOT_BUDGET"
	// OutcomeSkippedMemoryUnreadable means the Plan's stored memory was written
	// in a schema this build does not understand, which happens on a rollback.
	// The record is not loaded, not applied to and not cleared, and only this
	// Plan's no-data detection pauses.
	//
	// It is a bucket rather than an error because of how often it happens when
	// it happens at all: during a rollback it is every no-data Plan, every
	// round, for as long as the rollback lasts. An error would fail the Plan's
	// whole evaluation and take its threshold detection down with it - over a
	// no-data record nobody was asking about.
	//
	// Resuming is correct because the record holds timestamps. However many
	// rounds were skipped, the absence duration comes back from when the group
	// was last seen and first called absent, not from a count of rounds that
	// did not run.
	OutcomeSkippedMemoryUnreadable SlotOutcome = "SKIPPED_MEMORY_UNREADABLE"
	// OutcomeSkippedHostsUnresolved means the item's expected set is a host
	// target and this process has no host index to resolve it against.
	//
	// It is a skip rather than an empty roster because the two are opposite
	// answers wearing one shape. A target that resolves to no host is a real,
	// empty expected set and the item then speaks about itself; a target that
	// could not be resolved is not known to be empty, and treating it as empty
	// sends a no-data alert under the whole-item identity while the item's own
	// host groups keep their absences open with nothing left that would ever
	// close them. Nothing is judged, nothing is remembered and no series is
	// produced, so the round leaves no trace but its name.
	OutcomeSkippedHostsUnresolved SlotOutcome = "SKIPPED_HOSTS_UNRESOLVED"
	// OutcomeSkippedDerivationFailed means this Plan's no-data round could not
	// be worked out at all: its memory was not loaded, its Slot could not be
	// turned into the inputs the evaluation takes, or the evaluation refused
	// them. The evaluation never produces it -- these are the caller's own
	// failures -- and it is named here so that all of them are read from one
	// list.
	OutcomeSkippedDerivationFailed SlotOutcome = "SKIPPED_DERIVATION_FAILED"
	// OutcomeSkippedTargetSelectorUnavailable means the item's expected set
	// is a target plan and at least one of its dynamic selectors could not be
	// resolved this Slot - or nothing resolved it at all. The members that did
	// resolve still admit the ordinary detection's records; only absence is
	// not judged, because "not in the set" and "the set is not known" are
	// opposite answers wearing one shape, and judging on the second would
	// close absences and open new ones over a cache that merely blinked.
	// Nothing is judged, nothing is remembered; the round leaves its name.
	OutcomeSkippedTargetSelectorUnavailable SlotOutcome = "SKIPPED_TARGET_SELECTOR_UNAVAILABLE"
	// OutcomeSkippedTargetMembersDropped means every selector answered but
	// some members failed validation and were dropped. The kept members admit
	// records as usual; absence is not judged because a roster missing the
	// dropped members would read their silence as departure and close their
	// absences. It is named apart from the unavailable case so that one
	// long-malformed member cannot quietly hold a Plan's no-data detection
	// down under a reason that looks like an outage.
	OutcomeSkippedTargetMembersDropped SlotOutcome = "SKIPPED_TARGET_MEMBERS_DROPPED"
	// OutcomeSkippedOutputFailed means the round was decided and its verdicts
	// could not be turned into the series the ordinary evaluation reads.
	//
	// It is separate from a failed derivation because the two send a reader to
	// different places: nothing was decided in the first, and something was
	// decided and could not be said in the second -- which is the one that
	// leaves a group's absence known to this process and to nobody else.
	OutcomeSkippedOutputFailed SlotOutcome = "SKIPPED_OUTPUT_FAILED"
)

// SlotOutcomes is every outcome a Plan that detects no-data can land on, for a
// partition to pre-create and for a reader to bound the family by.
var SlotOutcomes = []SlotOutcome{
	OutcomeEvaluated, OutcomeSkippedQueryNotFull, OutcomeSkippedSlotBudget, OutcomeSkippedMemoryUnreadable,
	OutcomeSkippedHostsUnresolved, OutcomeSkippedTargetSelectorUnavailable, OutcomeSkippedTargetMembersDropped,
	OutcomeSkippedDerivationFailed, OutcomeSkippedOutputFailed,
}

// TargetResolutionState is what the worker's resolution of a target plan
// amounted to for one Slot, as far as absence is concerned.
type TargetResolutionState string

const (
	// TargetResolutionComplete: every selector answered and every member
	// validated; the members are the expected set.
	TargetResolutionComplete TargetResolutionState = "COMPLETE"
	// TargetResolutionIncomplete: every selector answered but members were
	// dropped in validation; the set is a lower bound, not the roster.
	TargetResolutionIncomplete TargetResolutionState = "INCOMPLETE"
	// TargetResolutionUnavailable: at least one selector could not be
	// resolved; the set is not known.
	TargetResolutionUnavailable TargetResolutionState = "UNAVAILABLE"
)

// TargetResolution is the worker's resolution of a target plan for one Slot,
// reduced to what absence reads: the state and, when complete, the member
// keys. It is the same resolution the admission filter read for the Slot's
// records; this package never derives one of its own.
type TargetResolution struct {
	State   TargetResolutionState
	Members []string
}

// EvaluateSlot turns one Slot's evidence into the no-data decision for it.
//
// It is the seam the worker calls: everything above it is state and wiring,
// everything below it is the projection, the roster and the absence rules. It
// reads no clock, no store and no CMDB index, so a Slot that is retried reaches
// the same decision from the same evidence - which is the property the whole
// evaluation is built on, and the one that stops being true the moment any of
// this is decided inside the worker instead.
//
// A Plan that does not detect no-data returns the zero result and no error. The
// caller does not have to ask twice, and a Plan that gains the section later
// starts being evaluated without the caller changing.
//
// The outcome says whether absence was judged. A Slot that did not see the
// whole period still produces a result - every expected group is UNAVAILABLE
// and the memory comes back untouched - and the outcome is what distinguishes
// that from a round that judged and found nothing absent.
func EvaluateSlot(input SlotInput) (AbsenceResult, SlotOutcome, error) {
	if input.Plan == nil || input.Plan.NoData == nil {
		return AbsenceResult{}, OutcomeNone, nil
	}
	config := input.Plan.NoData
	class, err := ClassifyTarget(input.Plan.TargetScope, input.Plan.TargetPlan, config.AggDimension)
	if err != nil {
		return AbsenceResult{}, OutcomeNone, err
	}
	var members []string
	if class.Source == RosterTargetPlan {
		// The dependency check comes before anything is judged, and it is
		// made whether or not the Slot saw a series: a round with no series
		// under an unavailable selector is the one round that must not be
		// read as "everything is absent".
		switch {
		case input.TargetResolution == nil, input.TargetResolution.State == TargetResolutionUnavailable:
			return AbsenceResult{}, OutcomeSkippedTargetSelectorUnavailable, nil
		case input.TargetResolution.State == TargetResolutionIncomplete:
			return AbsenceResult{}, OutcomeSkippedTargetMembersDropped, nil
		case input.TargetResolution.State == TargetResolutionComplete:
			members = input.TargetResolution.Members
		default:
			return AbsenceResult{}, OutcomeNone, fmt.Errorf("alarmd nodata: unknown target resolution state %q", input.TargetResolution.State)
		}
	}
	if class.Source == RosterTargetStatic && !input.HostsResolved {
		// The expected set is a host target and this process cannot resolve
		// hosts. Building the roster anyway would intersect the target with an
		// empty index and produce an empty expected set, which every rule
		// below reads as "this item expects nothing" -- the one reading that
		// is certainly wrong here. The classes that do not look at hosts are
		// untouched: a history roster comes out of the memory and a whole-item
		// one expects nothing by design, and neither becomes less true because
		// the index is cold.
		return AbsenceResult{}, OutcomeSkippedHostsUnresolved, nil
	}
	tally := ProjectSeries(input.Series, config.AggDimension)
	roster, err := BuildRoster(RosterRequest{
		AggDimension:  config.AggDimension,
		Scope:         input.Plan.TargetScope,
		Plan:          input.Plan.TargetPlan,
		TargetMembers: members,
		KnownHosts:    input.KnownHosts,
		Memory:        input.Memory,
	})
	if err != nil {
		return AbsenceResult{}, OutcomeNone, err
	}
	outcome := OutcomeEvaluated
	if input.Completeness != execution.CompletenessFull {
		outcome = OutcomeSkippedQueryNotFull
	}
	return Evaluate(AbsenceInput{
		EvaluationTime:         input.EvaluationTime,
		PeriodSeconds:          input.PeriodSeconds,
		Completeness:           input.Completeness,
		Present:                tally.Groups,
		Dropped:                tally.Dropped,
		Roster:                 roster,
		Memory:                 input.Memory,
		OutOfBusiness:          input.OutOfBusiness,
		TrackingHorizonSeconds: input.TrackingHorizonSeconds,
		TrackingExhaustedAt:    input.TrackingExhaustedAt,
	}), outcome, nil
}
