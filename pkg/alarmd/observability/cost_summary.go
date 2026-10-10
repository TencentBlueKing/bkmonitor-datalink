// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package observability

import (
	"cmp"
	"context"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// CostPlanIdentity is supplied by the executing consumer, never inferred from
// a query group's members or an inherited trace's strategy field.
type CostPlanIdentity struct {
	TenantID   string `json:"tenant_id"`
	BusinessID string `json:"business_id"`
	StrategyID string `json:"strategy_id"`
}

func (p CostPlanIdentity) valid() bool {
	return p.TenantID != "" && p.BusinessID != "" && p.StrategyID != ""
}

// CostGroup is an existing managed object's identity-only projection. The
// caller reconciles it on its existing catalog/report tick, not per record.
type CostGroup struct {
	QueryGroupKey    string             `json:"query_group_key"`
	SnapshotRevision string             `json:"snapshot_revision"`
	QueryRevision    string             `json:"query_revision"`
	ScheduleRevision string             `json:"schedule_revision"`
	Members          []CostPlanIdentity `json:"members"`
	TotalMembers     int                `json:"total_members"`

	// Schedules are the group's Plans' schedules: they say which of the
	// group and its Plans a window should have seen (see costDue). Not
	// published. A group without them is due in every window, and so is a
	// Plan without one.
	Schedules []CostSchedule `json:"-"`
}

// CostSchedule is when one Plan of a group is due: at its alignment plus
// every multiple of its interval, each Slot completing its completion offset
// later, all in Unix seconds.
type CostSchedule struct {
	Plan                    CostPlanIdentity
	IntervalSeconds         int64
	AlignmentSeconds        int64
	CompletionOffsetSeconds int64
}

// costDue is one Plan's schedule as the coverage reads it; the zero value is
// a schedule not known.
type costDue struct{ interval, alignment, offset int64 }

// costDueBytes is what one kept schedule adds to the roster's metadata.
const costDueBytes = int(unsafe.Sizeof(costDue{}))

// dueIn says a Slot of the schedule had its whole time inside [start, end]:
// the first evaluation time at or after start completes by end. A Slot that
// had it and left no observation in the window is one the window missed; a
// schedule whose Slots fall due only after the window - an hour's, read in a
// window of minutes - is not missing from it. A schedule that is not known is
// due in every window: a missing schedule must not read as nothing expected.
func (due costDue) dueIn(start, end int64) bool {
	if due.interval <= 0 {
		return true
	}
	offset := due.offset
	if offset <= 0 {
		offset = due.interval
	}
	first := due.alignment
	if start > first {
		first += (start - first + due.interval - 1) / due.interval * due.interval
	}
	return first+offset <= end
}

func costDueOf(schedule CostSchedule) costDue {
	return costDue{interval: schedule.IntervalSeconds, alignment: schedule.AlignmentSeconds, offset: schedule.CompletionOffsetSeconds}
}

type CostSummaryOptions struct {
	ProcessID     string
	Window        time.Duration
	GroupCapacity int
	PlanCapacity  int
	MetadataBytes int
	TopN          int
	Now           func() time.Time
	// Admit, when set, sizes the summary by the roster it is given instead
	// of the capacities above: each Reconcile asks it for the bytes a larger
	// roster would reserve (CostSummaryCapacityBytes) beyond what is held,
	// and grows only if they are admitted. A refused roster is tracked as
	// far as the capacity held reaches, the rest reported on the coverage.
	// A smaller roster gives its reservation up, so growing back asks again.
	Admit func(bytes uint64) bool
}

// CostSummaryCapacityBytes estimates a conservative reservation, including two
// rolling counters, map entries, reconciliation replacement, Publish's copy of
// every tracked window, and cached/read snapshots. Go allocator/map overhead
// varies: validate the reservation against the deployment's measured
// allocations. All population/metadata limits are supplied by the caller's
// resource budget; there is no permanent default cap. The per-entry allowance
// includes map buckets and membership slice capacity;
// BenchmarkCostSummaryReconcile measures the replacement generation as well.
func CostSummaryCapacityBytes(o CostSummaryOptions) int64 {
	if o.GroupCapacity <= 0 || o.PlanCapacity <= 0 || o.MetadataBytes <= 0 || o.TopN <= 0 {
		return 0
	}
	groups, plans := int64(o.GroupCapacity), int64(o.PlanCapacity)
	// Two scopes times the dimensions, TopN rows each.
	rankingRows := int64(2 * len(costDimensions) * o.TopN)
	rows := min(groups+plans, rankingRows)
	group := int64(unsafe.Sizeof(costGroupState{}) + unsafe.Sizeof(costAccount{}))
	plan := int64(unsafe.Sizeof(costPlanState{}) + unsafe.Sizeof(costWindows{}))
	return 2*(groups*(group+256)+plans*(plan+256)+int64(o.MetadataBytes)) +
		(groups+plans)*int64(unsafe.Sizeof(costCopy{})) +
		3*(rows*int64(unsafe.Sizeof(CostContributor{}))+plans*int64(unsafe.Sizeof(CostPlanIdentity{}))+rankingRows*8)
}

type CostWall struct {
	ObservedNS int64  `json:"observed_ns"`
	Measured   uint64 `json:"measured"`
	Unknown    uint64 `json:"unknown"`
}

// CostScalars are observed work, not CPU. Query wall contains streaming
// callbacks; run wall is the outer Slot Execute call. These overlap evaluation
// and state wall and must not be added to one another.
type CostScalars struct {
	Observations             uint64   `json:"observations"`
	Attempts                 uint64   `json:"attempts"`
	RunReturns               uint64   `json:"run_returns"`
	FailedRunReturns         uint64   `json:"failed_run_returns"`
	RetryingRunReturns       uint64   `json:"retrying_run_returns,omitempty"`
	ProgressCommits          uint64   `json:"progress_commits"`
	UnavailableCommits       uint64   `json:"unavailable_commits,omitempty"`
	GapSkippedCommits        uint64   `json:"gap_skipped_commits,omitempty"`
	Evaluations              uint64   `json:"evaluations"`
	FailedEvaluations        uint64   `json:"failed_evaluations"`
	EvaluationRecords        uint64   `json:"evaluation_records"`
	EvaluationRecordsUnknown uint64   `json:"evaluation_records_unknown"`
	UnattributedEvaluations  uint64   `json:"unattributed_evaluations"`
	EvaluationWall           CostWall `json:"evaluation_wall"`
	QueryScopes              uint64   `json:"query_scopes"`
	QueryWall                CostWall `json:"query_wall"`
	StateCalls               uint64   `json:"state_calls"`
	StateKeys                uint64   `json:"state_keys"`
	StateKeysUnknown         uint64   `json:"state_keys_unknown"`
	// StateBytes is the encoded state the calls carried, as the store
	// measured it; a call that reported keys and no bytes counts under
	// StateBytesUnknown rather than as zero bytes. The dimension that says
	// which object is the 86 MB one: keys alone read 249 keys as small.
	StateBytes        uint64   `json:"state_bytes"`
	StateBytesUnknown uint64   `json:"state_bytes_unknown"`
	StateWall         CostWall `json:"state_wall"`
	// RetainedBytesPeak is the largest retained-byte usage one Slot of the
	// object reported in the window: from the completion row's own account of
	// the five budgets, and from a refusal of the retained-byte budget, what
	// the Slot held plus the addition it was refused -- a refused Slot has no
	// completion row, and without its refusal the object that fills the pool
	// every round would be the one object with no peak. A peak, not a sum or
	// a mean: the pool is broken by peaks, and a mean over a bimodal object
	// is the wrong statistic. Zero when no row in the window carried either.
	RetainedBytesPeak uint64 `json:"retained_bytes_peak"`
	// RetainedHardStops and RetainedShareStops are this object's refusals on
	// the retained-byte budget in the window: the pool full of everyone's
	// bytes (RESOURCE_HARD_STOP), and this object alone over the share any
	// one of them may hold (QG_BUDGET_SHARE_EXCEEDED). The two ask for
	// different actions -- move a neighbour, or shard this one.
	RetainedHardStops  uint64   `json:"retained_hard_stops"`
	RetainedShareStops uint64   `json:"retained_share_stops"`
	RunWall            CostWall `json:"run_wall"`
	MaxLagNS           int64    `json:"max_lag_ns"`
	LagMeasured        uint64   `json:"lag_measured"`
	LagUnknown         uint64   `json:"lag_unknown"`
	LastProgressUnix   int64    `json:"last_progress_unix"`

	// HeldRounds are the rounds the scheduler returned without running the
	// Slot on purpose or because it could not - a query cooldown, a source
	// that asked to retry or refused, an operation not ready, a busy or lost
	// owner (see costHeldOutcomes). They are not work and not observations:
	// a group held for the whole window has no cost missing from it, and is
	// counted as held rather than unseen.
	HeldRounds uint64 `json:"held_rounds,omitempty"`

	// OtherRevisionObservations are observations of this group's key that
	// carried another revision than the roster's - the group changed
	// revision and the roster had not caught up. They are untracked and
	// cost nothing here; counted against the key, they name a group that
	// looks as if it left no record because its rounds ran under the next
	// revision.
	OtherRevisionObservations uint64 `json:"other_revision_observations,omitempty"`
}

// costHeldOutcomes are the scheduler's words for a round it returned without
// running a Slot that was due: every run outcome but the round that ran
// (execute_returned), the round with nothing due (source_not_due), and the
// ones that did not return as designed (panic, other_error), which stay
// unaccounted rather than excused.
var costHeldOutcomes = map[string]bool{
	"query_cooldown": true, "single_flight_busy": true, "ownership_rejected": true, "source_backoff": true,
	"source_retry": true, "source_blocked": true, "source_error": true, "operation_not_ready": true,
	"admission_denied": true, "view_not_executable": true, "cancelled": true,
}

// costUnevaluatedOutcomes are the round results that account for a due Plan
// its group did not evaluate: results that by definition evaluate nothing,
// each read from what the window observed of the group. failed is a Slot
// returned failed; gap_skipped a round committed giving a span up;
// unavailable a round committed with its inputs unavailable
// (COMPLETED_WITH_UNAVAILABLE, SNAPSHOT_UNAVAILABLE); retrying a Slot
// returned to be run again (QUERY_NOT_READY, VIEW_NOT_EXECUTABLE); in_flight
// a Slot started and not returned when the window was read; held a round the
// scheduler returned without running (costHeldOutcomes). When a window holds
// several, the first in this order names the Plan: the rounds that ran
// before the ones that did not. A round that completed normally is none of
// them: it evaluates what is due.
var costUnevaluatedOutcomes = []string{"failed", "gap_skipped", "unavailable", "retrying", "in_flight", "held"}

// Why a due Plan its group did not evaluate is unobserved - what the window
// should have seen and did not: another Plan of its group was evaluated and
// it was not; its group completed a round normally and evaluated nothing;
// its group left no record in the window at all, or none but observations
// of its key under the revision after the roster's (revision_changed: its
// rounds ran, the roster had not caught up); or its group's records hold
// none of costUnevaluatedOutcomes.
const (
	costMissSiblingEvaluated     = "sibling_evaluated"
	costMissCompletedUnevaluated = "completed_unevaluated"
	costMissNoRecord             = "no_record"
	costMissRevisionChanged      = "revision_changed"
	costMissUnexplained          = "unexplained"
)

// costUnevaluated says what accounts for a due Plan its group did not
// evaluate over w, both buckets: one of costUnevaluatedOutcomes and true, or
// the costMiss reason it is unobserved and false. An evaluation of another
// Plan, and a round completed normally, are read first: either leaves this
// Plan's evaluation missing whatever else the window holds.
func costUnevaluated(w costWindows) (string, bool) {
	c, p := w.current, w.previous
	commits := c.ProgressCommits + p.ProgressCommits
	unavailable, skipped := c.UnavailableCommits+p.UnavailableCommits, c.GapSkippedCommits+p.GapSkippedCommits
	switch {
	case c.Evaluations+p.Evaluations > 0:
		return costMissSiblingEvaluated, false
	case commits > unavailable+skipped:
		return costMissCompletedUnevaluated, false
	case c.FailedRunReturns+p.FailedRunReturns > 0:
		return "failed", true
	case skipped > 0:
		return "gap_skipped", true
	case unavailable > 0:
		return "unavailable", true
	case c.RetryingRunReturns+p.RetryingRunReturns > 0:
		return "retrying", true
	case c.Attempts+p.Attempts > c.RunReturns+p.RunReturns:
		return "in_flight", true
	case c.HeldRounds+p.HeldRounds > 0:
		return "held", true
	case c.OtherRevisionObservations+p.OtherRevisionObservations > 0:
		return costMissRevisionChanged, false
	case c.Observations+p.Observations == 0:
		return costMissNoRecord, false
	}
	return costMissUnexplained, false
}

type costWindows struct {
	epoch    int64
	current  CostScalars
	previous CostScalars
}

// rotate brings the windows to epoch. An epoch older than the one they hold
// leaves them as they are: a Publish reads its clock at the start of its
// tick, and an observation that has since moved the group into the next
// window must not have both windows cleared by it. The copy is then a
// window ahead of the snapshot's; nothing is lost.
func (w *costWindows) rotate(epoch int64) {
	if epoch <= w.epoch {
		return
	}
	if w.epoch+1 == epoch {
		w.previous = w.current
	} else {
		w.previous = CostScalars{}
	}
	w.current = CostScalars{}
	w.epoch = epoch
}

type costPlanState struct {
	identity CostPlanIdentity
	// windows is guarded by the group's account, and carried by pointer
	// across a reconciliation that keeps both the group and the Plan.
	windows *costWindows
	since   time.Time
	// due is the Plan's schedule, zero when the roster did not carry one.
	due costDue
}

// costAccount is one Query Group's windows and the lock that guards them
// and its Plans'. It is the only lock Observe takes, so an observation can be
// lost only to another holder of the same group's account, and every other
// holder holds it for a copy of that one group's windows. A reconciliation
// that keeps the group's revisions hands the new state the same account,
// which is how an observation made against the roster it replaced still
// lands in the counters that are read.
type costAccount struct {
	mu      sync.Mutex
	windows costWindows
}

type costGroupState struct {
	group   CostGroup
	plans   map[CostPlanIdentity]*costPlanState
	account *costAccount
	since   time.Time
	// replaced says the roster tracked this key under other revisions before
	// this one: the group changed revision, and its window starts over.
	replaced bool
	// due is every distinct schedule of the group's Plans, tracked or not:
	// the group runs for all of them. Empty when the roster carried none.
	due []costDue
}

// dueIn says the group had a Slot due in the window; see costDue.dueIn.
func (g *costGroupState) dueIn(start, end int64) bool {
	if len(g.due) == 0 {
		return true
	}
	for _, due := range g.due {
		if due.dueIn(start, end) {
			return true
		}
	}
	return false
}

// costScope is one reconciliation's roster. It is stored whole and never
// modified afterwards, so it is read without a lock.
type costScope struct {
	groups   map[string]*costGroupState
	coverage CostCoverage
}

// costEventWindows counts process-level events by the window they happened
// in, without a lock: the path that counts them is the one that could not,
// or need not, take a group's account. Each slot is one word, the window's
// number in the high half and its count in the low, so a count is never read
// under another window's number. Four slots, where two are read, leave a
// late add for a window long gone nothing to overwrite.
type costEventWindows struct {
	slots [4]atomic.Uint64
}

func (w *costEventWindows) add(epoch int64) {
	slot, tag := &w.slots[epoch&3], uint32(epoch)
	for {
		v := slot.Load()
		next := uint64(tag)<<32 | 1
		switch held := uint32(v >> 32); {
		case held == tag:
			if uint32(v) == ^uint32(0) {
				return
			}
			next = v + 1
		case int32(tag-held) < 0:
			// A window older than the one the slot already holds.
			return
		}
		if slot.CompareAndSwap(v, next) {
			return
		}
	}
}

func (w *costEventWindows) count(epoch int64) uint64 {
	v := w.slots[epoch&3].Load()
	if uint32(v>>32) != uint32(epoch) {
		return 0
	}
	return uint64(uint32(v))
}

// window is the count over the window the reading is for and the one
// before it, as the scalar windows are read.
func (w *costEventWindows) window(epoch int64) uint64 {
	return w.count(epoch) + w.count(epoch-1)
}

type CostCoverage struct {
	// ContentionDroppedTotal is the process's cumulative count of
	// observations lost to a held account, a counter for rates.
	// ContentionDropped is the part of it in this reading's window, counted
	// by the window each loss happened in: it is what makes the window
	// incomplete, so one loss marks the windows it fell in and not every
	// window after it.
	ContentionDroppedTotal  uint64 `json:"contention_dropped_total"`
	ContentionDropped       uint64 `json:"contention_dropped"`
	CatalogComplete         bool   `json:"catalog_complete"`
	TotalGroups             int    `json:"total_groups"`
	TrackedGroups           int    `json:"tracked_groups"`
	TotalPlans              int    `json:"total_plans"`
	TrackedPlans            int    `json:"tracked_plans"`
	ObservedGroups          int    `json:"observed_groups"`
	ObservedPlans           int    `json:"observed_plans"`
	PartialWindowGroups     int    `json:"partial_window_groups"`
	PartialWindowPlans      int    `json:"partial_window_plans"`
	UnknownWallObservations uint64 `json:"unknown_wall_observations"`
	MetadataBytes           int    `json:"metadata_bytes"`
	UntrackedObservations   uint64 `json:"untracked_observations"`
	UnattributedEvaluations uint64 `json:"unattributed_evaluations"`
	Incomplete              bool   `json:"incomplete"`

	// DueGroups and DuePlans are the tracked ones that had a Slot due in the
	// window - its evaluation time at or after the window's start, its
	// completion deadline by the window's end. Of those, HeldDueGroups have
	// no work in the window because the scheduler held the group's rounds
	// (HeldRounds): nothing ran, so no cost is missing. UnevaluatedDuePlans
	// counts the due Plans their group did not evaluate by the round result
	// that accounts for it (costUnevaluatedOutcomes, every one present): the
	// window saw what happened, and whether a Plan was evaluated is the
	// object's health to report, not this ledger's. UnobservedDueGroups and
	// UnobservedDuePlans are what makes the window incomplete, what it
	// should have seen and did not: a group due that left nothing at all,
	// and a due Plan nothing accounts for (costUnevaluated). A group on a
	// long interval has no Slot due in most windows and is not missing from
	// them, which is why observed is not held to tracked.
	DueGroups           int `json:"due_groups"`
	UnobservedDueGroups int `json:"unobserved_due_groups"`
	HeldDueGroups       int `json:"held_due_groups"`
	DuePlans            int `json:"due_plans"`
	UnobservedDuePlans  int `json:"unobserved_due_plans"`

	// UnevaluatedDuePlans has one entry per costUnevaluatedOutcomes word, in
	// that order, zeros included.
	UnevaluatedDuePlans []CostUnevaluated `json:"unevaluated_due_plans"`

	// UnobservedDueSample names up to costDueMissSampleLimit of the groups
	// and Plans counted in UnobservedDueGroups and UnobservedDuePlans, in key
	// order, each with what the window holds of its group. The counts said
	// how many and never which: a replica read incomplete for a few Plans
	// and nothing on it said whose they were or what their group had done.
	UnobservedDueSample []CostDueMiss `json:"unobserved_due_sample,omitempty"`

	// PartialWindowSample names up to costDueMissSampleLimit of the groups
	// counted in PartialWindowGroups, in key order: tracked since the window
	// began, a group new to the roster or one that changed revision
	// (Replaced) and started its window over. The count alone made a
	// replica read incomplete for a quarter of an hour with nothing to say
	// which group, or that it had only changed revision.
	PartialWindowSample []CostPartialGroup `json:"partial_window_sample,omitempty"`
}

// CostPartialGroup is one group tracked since after the window began.
type CostPartialGroup struct {
	QueryGroupKey string    `json:"query_group_key"`
	TrackedSince  time.Time `json:"tracked_since"`
	Replaced      bool      `json:"replaced"`
}

// CostUnevaluated is how many due Plans one round result accounted for.
type CostUnevaluated struct {
	Outcome string `json:"outcome"`
	Plans   int    `json:"plans"`
}

// costDueMissSampleLimit bounds the sample: enough to name a handful, never
// the roster.
const costDueMissSampleLimit = 8

// CostDueMiss is one tracked group - or one Plan of it, when Scope is
// strategy_owned - that had a Slot due in the window and left no
// observation in it, with its group's counts over the window. Counts only:
// what the group did, not why. Every count is the group's, a strategy_owned
// miss's too: a Plan has no observations of its own in the window, which is
// what made it a miss, and what its group did is the question.
type CostDueMiss struct {
	Scope            string           `json:"scope"`
	QueryGroupKey    string           `json:"query_group_key"`
	Plan             CostPlanIdentity `json:"plan"`
	Observations     uint64           `json:"observations"`
	Attempts         uint64           `json:"attempts"`
	RunReturns       uint64           `json:"run_returns"`
	FailedRunReturns uint64           `json:"failed_run_returns"`
	Evaluations      uint64           `json:"evaluations"`
	HeldRounds       uint64           `json:"held_rounds"`
	ProgressCommits  uint64           `json:"progress_commits"`

	// Reason is why the miss is unobserved (costUnevaluated's costMiss
	// words); a group's own miss left no record (no_record). The counts
	// after it are the rest of what that reading used: returns to be run
	// again, and the commits that completed with inputs unavailable or gave
	// a span up - the rest of ProgressCommits completed normally.
	Reason             string `json:"reason"`
	RetryingRunReturns uint64 `json:"retrying_run_returns"`
	UnavailableCommits uint64 `json:"unavailable_commits"`
	GapSkippedCommits  uint64 `json:"gap_skipped_commits"`

	// OtherRevisionObservations are the group's key observed under another
	// revision than the roster's (revision_changed).
	OtherRevisionObservations uint64 `json:"other_revision_observations"`
}

// compareDueMiss orders misses by group key, then the group before its
// Plans, then the Plan, field by field.
func compareDueMiss(a, b CostDueMiss) int {
	return cmp.Or(cmp.Compare(a.QueryGroupKey, b.QueryGroupKey), cmp.Compare(a.Scope, b.Scope),
		cmp.Compare(a.Plan.TenantID, b.Plan.TenantID), cmp.Compare(a.Plan.BusinessID, b.Plan.BusinessID),
		cmp.Compare(a.Plan.StrategyID, b.Plan.StrategyID))
}

// keepDueMiss adds miss to kept, the first costDueMissSampleLimit misses in
// order, and keeps no more: an outage that misses every group holds eight
// records while the window is read, not one per group.
func keepDueMiss(kept []CostDueMiss, miss CostDueMiss) []CostDueMiss {
	at, _ := slices.BinarySearchFunc(kept, miss, compareDueMiss)
	if at >= costDueMissSampleLimit {
		return kept
	}
	if len(kept) == costDueMissSampleLimit {
		kept = kept[:costDueMissSampleLimit-1]
	}
	return slices.Insert(kept, at, miss)
}

// keepPartialGroup adds group to kept, the first costDueMissSampleLimit
// groups in key order, and keeps no more.
func keepPartialGroup(kept []CostPartialGroup, group CostPartialGroup) []CostPartialGroup {
	at, _ := slices.BinarySearchFunc(kept, group, func(a, b CostPartialGroup) int { return cmp.Compare(a.QueryGroupKey, b.QueryGroupKey) })
	if at >= costDueMissSampleLimit {
		return kept
	}
	if len(kept) == costDueMissSampleLimit {
		kept = kept[:costDueMissSampleLimit-1]
	}
	return slices.Insert(kept, at, group)
}

// costDueMissOf is a miss of the group, or of plan when it is not nil, for
// reason, with the group's counts over its two windows.
func costDueMissOf(group *costGroupState, plan *costPlanState, windows costWindows, reason string) CostDueMiss {
	miss := CostDueMiss{Scope: "query_group", QueryGroupKey: group.group.QueryGroupKey, Reason: reason}
	if plan != nil {
		miss.Scope, miss.Plan = "strategy_owned", plan.identity
	}
	for _, s := range [2]CostScalars{windows.current, windows.previous} {
		miss.Observations += s.Observations
		miss.Attempts += s.Attempts
		miss.RunReturns += s.RunReturns
		miss.FailedRunReturns += s.FailedRunReturns
		miss.Evaluations += s.Evaluations
		miss.HeldRounds += s.HeldRounds
		miss.ProgressCommits += s.ProgressCommits
		miss.RetryingRunReturns += s.RetryingRunReturns
		miss.UnavailableCommits += s.UnavailableCommits
		miss.GapSkippedCommits += s.GapSkippedCommits
		miss.OtherRevisionObservations += s.OtherRevisionObservations
	}
	return miss
}

type CostContributor struct {
	Scope        string           `json:"scope"`
	Group        CostGroup        `json:"group"`
	Plan         CostPlanIdentity `json:"plan"`
	TrackedSince time.Time        `json:"tracked_since"`
	Observed     bool             `json:"observed"`
	Current      CostScalars      `json:"current"`
	Previous     CostScalars      `json:"previous"`
}

// Indexes refer to Contributors, which keeps members once even when a group
// ranks in several dimensions. These are candidates from this process only.
type CostRanking struct {
	Scope     string `json:"scope"`
	Dimension string `json:"dimension"`
	Indexes   []int  `json:"indexes"`
}

// CostRetainedReading is one process's answer to the placement question:
// if every object this replica evaluated in the window peaked in the same
// round, how much of the retained-byte pool would they hold. It is the sum
// over those objects of each one's largest per-Slot retained bytes -- the
// bytes a refused Slot asked for included -- against the pool: an upper
// bound on the simultaneous peak, not a utilization.
// The peaks need not coincide, so the share can pass 1 with no refusal, and
// the pool can refuse at a share under 1 when the ones that do coincide are
// enough. What the pool actually held when it refused is capacity_shared_used
// on the refusal line; this reading is what decides whether these objects
// belong on one replica at all. Three objects of one strategy landed on one
// replica at 175-404 MB each, every one inside its own share, and together
// filled a 1 GiB pool for thirteen refusals every ten minutes; the budget
// held each object and nobody read the sum.
type CostRetainedReading struct {
	PeakSumBytes uint64 `json:"retained_bytes_peak_sum"`
	// GroupsWithPeak is how many objects contributed a non-zero peak: the
	// denominator of "how many would have to move".
	GroupsWithPeak int `json:"groups_with_peak"`
	// LimitBytes is the pool, MaxRetainedBytes, as the completion rows carry
	// it; LimitKnown false before any row has, and the share is then 0 rather
	// than a division by nothing read as "no pressure".
	LimitBytes uint64  `json:"retained_bytes_limit"`
	LimitKnown bool    `json:"retained_bytes_limit_known"`
	PeakShare  float64 `json:"retained_bytes_peak_share"`
	// HardStops and ShareStops are the window's refusals on this budget in
	// this process, pool-full and over-share, counted where they are raised
	// whether or not the object is tracked here, so "93% and 13 refusals" can
	// be read off one row.
	HardStops  uint64 `json:"resource_hard_stops"`
	ShareStops uint64 `json:"share_stops"`
}

type CostSnapshot struct {
	Enabled                bool                `json:"enabled"`
	DisabledReason         string              `json:"disabled_reason,omitempty"`
	ProcessID              string              `json:"process_id"`
	Scope                  string              `json:"scope"`
	GeneratedAt            time.Time           `json:"generated_at"`
	WindowStart            time.Time           `json:"window_start"`
	CurrentWindowStart     time.Time           `json:"current_window_start"`
	WindowEnd              time.Time           `json:"window_end"`
	CapacityBytesEstimated int64               `json:"capacity_bytes_estimated"`
	Coverage               CostCoverage        `json:"coverage"`
	Contributors           []CostContributor   `json:"contributors"`
	Rankings               []CostRanking       `json:"rankings"`
	Retained               CostRetainedReading `json:"retained"`
}

// CostSummary observes live events only. It never consumes diagnostic records
// or snapshots: rereads cannot double count, while genuine same-Slot retries
// (including different owners) remain separate attempts. No run/series set is
// retained. Reconcile and Publish belong to existing control/report ticks.
//
// Observe takes one lock, the account of the group it counts for, and
// nothing that Reconcile, Publish, Snapshot or RetainedPeaks does in
// proportion to the roster happens under it: Reconcile builds the next
// roster aside and stores it whole, Publish copies each group's windows
// under that group's account and ranks the copy, and Snapshot reads a
// published snapshot nobody modifies.
type CostSummary struct {
	options CostSummaryOptions
	enabled bool
	// capacity is what Reconcile tracks up to under Admit: the roster's, as
	// far as it was admitted. Reconcile alone writes it, under reconciling;
	// capacityBytes is its reservation, for the snapshot. Without Admit the
	// options' capacities are the capacity.
	capacity      CostSummaryOptions
	capacityBytes atomic.Int64
	scope         atomic.Pointer[costScope]
	// reconciling and publishing order the maintenance calls among
	// themselves; Observe takes neither.
	reconciling sync.Mutex
	publishing  sync.Mutex
	// copies is Publish's copy of every tracked window, kept for the next
	// Publish so the copy is not an allocation every tick.
	copies   []costCopy
	snapshot atomic.Pointer[CostSnapshot]
	// untracked, the retained-byte refusals and contention are counted by
	// window at the process level; retainedLimit is the pool's size once a
	// completion row has carried it.
	untracked         costEventWindows
	hardStops         costEventWindows
	shareStops        costEventWindows
	contention        costEventWindows
	retainedLimit     atomic.Uint64
	contentionDropped atomic.Uint64
	// rankingStarted, when set, is called by Publish between the copy and
	// the ranking; a test holds the ranking there.
	rankingStarted func()
	// yield is what an observation does between its tries at a held
	// account: runtime.Gosched, which a test replaces to act at that moment.
	yield func()
	// retryClock times those tries: time.Now, which a test replaces. The
	// summary's own clock places observations in windows and is not a
	// stopwatch.
	retryClock func() time.Time
}

// costAccountRetry is how long an observation keeps trying a held account
// before it is lost. Every holder holds an account for one group's copy or
// one observation's addition: a Publish copies a group's windows and its
// Plans', about 200 ns for a group of one Plan and a few microseconds for the
// largest group a replica holds - a group of a few tens of Plans at some 600
// bytes of windows each. 20 us waits out that copy, or another observer of
// the same group, several times over, and is nothing beside a Slot's own
// work. A holder the scheduler has taken off the CPU mid-hold is gone for
// milliseconds; waiting that out would put execution behind maintenance, so
// that loss stays lost, and counted.
const costAccountRetry = 20 * time.Microsecond

// lockAccount takes an account for an observation, yielding to a holder and
// trying again until costAccountRetry has passed, then giving up. The time
// is checked after each yield, so it bounds the tries, not how long one
// yield takes on a saturated processor.
func (c *CostSummary) lockAccount(account *costAccount) bool {
	if account.mu.TryLock() {
		return true
	}
	started := c.retryClock()
	for {
		c.yield()
		if account.mu.TryLock() {
			return true
		}
		if c.retryClock().Sub(started) >= costAccountRetry {
			return false
		}
	}
}

func NewCostSummary(o CostSummaryOptions) *CostSummary {
	if o.Now == nil {
		o.Now = time.Now
	}
	admitted := o.Admit != nil && o.TopN > 0
	c := &CostSummary{options: o, enabled: o.ProcessID != "" && o.Window > 0 && (admitted || CostSummaryCapacityBytes(o) > 0), yield: runtime.Gosched,
		retryClock: time.Now, capacity: o}
	// Under Admit nothing is reserved until a roster asks.
	c.capacity.GroupCapacity, c.capacity.PlanCapacity, c.capacity.MetadataBytes = 0, 0, 0
	snapshot := &CostSnapshot{Enabled: c.enabled, ProcessID: o.ProcessID, Scope: "process_observed_candidates", CapacityBytesEstimated: c.capacityEstimate()}
	snapshot.Coverage.Incomplete = true
	if !c.enabled {
		snapshot.DisabledReason = "resource_budget_or_process_identity_missing"
		snapshot.Coverage.Incomplete = true
	}
	c.snapshot.Store(snapshot)
	return c
}

// Reconcile accepts the caller's managed scope, not a new authority. Incomplete
// catalogs must pass complete=false. Input order determines admission when the
// allocation cannot cover the scope; coverage explicitly reports the shortfall.
// Version changes begin fresh counters rather than relabel old measurements.
func (c *CostSummary) Reconcile(groups []CostGroup, complete bool) {
	if c == nil || !c.enabled {
		return
	}
	c.reconciling.Lock()
	defer c.reconciling.Unlock()
	var previous map[string]*costGroupState
	if scope := c.scope.Load(); scope != nil {
		previous = scope.groups
	}
	capacity := c.options
	if c.options.Admit != nil {
		c.admitRoster(groups)
		capacity = c.capacity
	}
	coverage := CostCoverage{CatalogComplete: complete, TotalGroups: len(groups)}
	next := make(map[string]*costGroupState, min(len(groups), capacity.GroupCapacity))
	now := c.options.Now()
	for _, input := range groups {
		coverage.TotalPlans += len(input.Members)
		bytes := len(input.QueryGroupKey) + len(input.QueryRevision) + len(input.SnapshotRevision) + len(input.ScheduleRevision)
		if input.QueryGroupKey == "" || len(next) >= capacity.GroupCapacity || coverage.MetadataBytes+bytes > capacity.MetadataBytes {
			continue
		}
		if _, exists := next[input.QueryGroupKey]; exists {
			continue
		}
		old := previous[input.QueryGroupKey]
		state := &costGroupState{since: now, plans: make(map[CostPlanIdentity]*costPlanState)}
		if old != nil && old.group.QueryRevision == input.QueryRevision && old.group.SnapshotRevision == input.SnapshotRevision && old.group.ScheduleRevision == input.ScheduleRevision {
			state.account, state.since, state.replaced = old.account, old.since, old.replaced
		} else {
			state.replaced = old != nil
			old = nil
			state.account = &costAccount{}
		}
		state.group = CostGroup{QueryGroupKey: strings.Clone(input.QueryGroupKey), QueryRevision: strings.Clone(input.QueryRevision), SnapshotRevision: strings.Clone(input.SnapshotRevision), ScheduleRevision: strings.Clone(input.ScheduleRevision), TotalMembers: len(input.Members)}
		coverage.MetadataBytes += bytes
		planDue := make(map[CostPlanIdentity]costDue, len(input.Schedules))
		for _, schedule := range input.Schedules {
			due := costDueOf(schedule)
			if due.interval <= 0 {
				continue
			}
			// A split Plan has one schedule per piece; the most frequent is
			// the one a window sees first.
			if kept, found := planDue[schedule.Plan]; !found || due.interval < kept.interval {
				planDue[schedule.Plan] = due
			}
			if !slices.Contains(state.due, due) {
				state.due = append(state.due, due)
			}
		}
		// All of the group's schedules or none: a group read on some of them
		// could miss the one due in a window and read as not due when it was.
		// With none it is due in every window, which can only over-report.
		if coverage.MetadataBytes+len(state.due)*costDueBytes > capacity.MetadataBytes {
			state.due = nil
		}
		coverage.MetadataBytes += len(state.due) * costDueBytes
		for _, member := range input.Members {
			bytes := len(member.TenantID) + len(member.BusinessID) + len(member.StrategyID)
			if !member.valid() || coverage.TrackedPlans >= capacity.PlanCapacity || coverage.MetadataBytes+bytes > capacity.MetadataBytes {
				continue
			}
			if _, exists := state.plans[member]; exists {
				continue
			}
			identity := CostPlanIdentity{strings.Clone(member.TenantID), strings.Clone(member.BusinessID), strings.Clone(member.StrategyID)}
			plan := &costPlanState{identity: identity, since: now, due: planDue[member]}
			if old != nil && old.plans[member] != nil {
				plan.windows, plan.since = old.plans[member].windows, old.plans[member].since
			} else {
				plan.windows = &costWindows{}
			}
			state.plans[identity] = plan
			state.group.Members = append(state.group.Members, identity)
			coverage.TrackedPlans++
			coverage.MetadataBytes += bytes
		}
		next[state.group.QueryGroupKey] = state
	}
	coverage.TrackedGroups = len(next)
	coverage.Incomplete = !complete || coverage.TrackedGroups != coverage.TotalGroups || coverage.TrackedPlans != coverage.TotalPlans
	c.scope.Store(&costScope{groups: next, coverage: coverage})
}

// capacityEstimate is the reservation the summary holds: the roster's as
// admitted under Admit, the options' otherwise.
func (c *CostSummary) capacityEstimate() int64 {
	if c.options.Admit != nil {
		return c.capacityBytes.Load()
	}
	return CostSummaryCapacityBytes(c.options)
}

// admitRoster sizes the capacity to the roster under Admit: the roster's
// groups, Plans and metadata, as Reconcile counts them. Growth is asked for
// as the reservation it adds; refused, the capacity held stays. Called
// under reconciling.
func (c *CostSummary) admitRoster(groups []CostGroup) {
	want := c.capacity
	want.GroupCapacity, want.PlanCapacity, want.MetadataBytes = 0, 0, 0
	for _, input := range groups {
		if input.QueryGroupKey == "" {
			continue
		}
		want.GroupCapacity++
		want.PlanCapacity += len(input.Members)
		want.MetadataBytes += len(input.QueryGroupKey) + len(input.QueryRevision) + len(input.SnapshotRevision) + len(input.ScheduleRevision) +
			len(input.Schedules)*costDueBytes
		for _, member := range input.Members {
			want.MetadataBytes += len(member.TenantID) + len(member.BusinessID) + len(member.StrategyID)
		}
	}
	// A reservation of nothing is not a summary: the smallest roster still
	// reserves one group's worth.
	want.GroupCapacity, want.PlanCapacity, want.MetadataBytes = max(want.GroupCapacity, 1), max(want.PlanCapacity, 1), max(want.MetadataBytes, 1)
	held, wanted := CostSummaryCapacityBytes(c.capacity), CostSummaryCapacityBytes(want)
	if wanted > held && !c.options.Admit(uint64(wanted-held)) {
		return
	}
	c.capacity = want
	c.capacityBytes.Store(wanted)
}

// Observe never waits for another observer, reconciliation, publication, or an
// API read. Contention loses only observability facts and is explicitly
// counted; it is contention for one group's account, which the maintenance
// calls hold for a copy of that group's windows and no longer.
func (c *CostSummary) Observe(ctx context.Context, o Observation) {
	if c == nil || !c.enabled {
		return
	}
	// A supplement is not a round: it evaluates an earlier, completed Slot's
	// late series. Counted here it was an attempt, made its Query Group and
	// Plans read observed, ranked the group by the supplemented Slot's age,
	// and filed its old revisions as a revision change. The lookback counts
	// supplements and what they held.
	if o.Operation == OperationSupplement {
		return
	}
	if o.Stage == StageRunnerReturned {
		c.observeHeld(ctx, o)
		return
	}
	switch o.Stage {
	case StageSlotStarted, StageSlotCompleted, StageProgressCommitted, StageEvaluationCompleted, StageQueryCompleted, StageStatePreflight, StageStateApplied:
	case StageResourceHard:
		if o.Component != ComponentResource || o.CapacityBudget != CapacityBudgetRetainedBytes {
			return
		}
	default:
		return
	}
	trace := mergeTraceFields(o.Trace, TraceFieldsFromContext(ctx))
	now := c.options.Now()
	epoch := now.UnixNano() / int64(c.options.Window)
	// Counted where it is raised, before the object lookup: a refusal on an
	// object this summary does not track is still a refusal of this pool.
	if o.Stage == StageResourceHard {
		switch string(o.ReasonCode) {
		case contract.ReasonResourceHardStop:
			c.hardStops.add(epoch)
		case contract.ReasonQGBudgetShareExceeded:
			c.shareStops.add(epoch)
		}
	}
	// Learned only from a row that carries it: a completion whose stream was
	// never built reports the zero account, and a zero read as the pool would
	// clear a limit already learned and collapse the share to nothing in the
	// very round a Slot failed to start. Under real configuration the pool is
	// never zero, so zero can only be that row.
	if usage := o.SlotBudgetUsage; usage != nil && usage.RetainedBytesLimit > 0 {
		c.retainedLimit.Store(usage.RetainedBytesLimit)
	}
	var g *costGroupState
	if scope := c.scope.Load(); scope != nil {
		g = scope.groups[trace.QueryGroupKey]
	}
	if g == nil {
		c.untracked.add(epoch)
		return
	}
	if (trace.SnapshotRevision != "" && trace.SnapshotRevision != g.group.SnapshotRevision) || (trace.QueryRevision != "" && trace.QueryRevision != g.group.QueryRevision) || (trace.ScheduleRevision != "" && trace.ScheduleRevision != g.group.ScheduleRevision) {
		c.untracked.add(epoch)
		c.observeOtherRevision(g, epoch)
		return
	}
	account := g.account
	if !c.lockAccount(account) {
		c.contentionDropped.Add(1)
		c.contention.add(epoch)
		return
	}
	defer account.mu.Unlock()
	account.windows.rotate(epoch)
	addCost(&account.windows.current, o, trace, now)
	if o.Stage == StageEvaluationCompleted {
		if p := g.plans[o.EvaluationOwner]; o.EvaluationOwner.valid() && p != nil {
			p.windows.rotate(epoch)
			addCost(&p.windows.current, o, trace, now)
		} else {
			account.windows.current.UnattributedEvaluations++
		}
	}
}

// observeOtherRevision counts an observation of g's key under another
// revision against g, so the group's window can say its rounds ran under the
// next one. Contention loses this count as it loses any other.
func (c *CostSummary) observeOtherRevision(g *costGroupState, epoch int64) {
	if !c.lockAccount(g.account) {
		c.contentionDropped.Add(1)
		c.contention.add(epoch)
		return
	}
	defer g.account.mu.Unlock()
	g.account.windows.rotate(epoch)
	g.account.windows.current.OtherRevisionObservations++
}

// observeHeld counts a round the scheduler held, against its group. Only a
// held round takes the group's account - the rounds that ran and the ones
// with nothing due, dozens a second, take nothing - and a held round of a
// group the roster does not track is not an untracked observation: it is
// not cost.
func (c *CostSummary) observeHeld(ctx context.Context, o Observation) {
	if !costHeldOutcomes[o.RunOutcome] {
		return
	}
	trace := mergeTraceFields(o.Trace, TraceFieldsFromContext(ctx))
	var g *costGroupState
	if scope := c.scope.Load(); scope != nil {
		g = scope.groups[trace.QueryGroupKey]
	}
	if g == nil {
		return
	}
	epoch := c.options.Now().UnixNano() / int64(c.options.Window)
	if !c.lockAccount(g.account) {
		c.contentionDropped.Add(1)
		c.contention.add(epoch)
		return
	}
	defer g.account.mu.Unlock()
	g.account.windows.rotate(epoch)
	g.account.windows.current.HeldRounds++
}

func addWall(w *CostWall, o Observation) {
	if o.Duration >= 0 && (o.DurationKnown || o.Duration > 0) {
		w.Measured++
		w.ObservedNS += max(0, int64(o.Duration))
	} else {
		w.Unknown++
	}
}

func addCost(s *CostScalars, o Observation, trace TraceFields, now time.Time) {
	s.Observations++
	switch o.Stage {
	case StageSlotStarted:
		s.Attempts++
	case StageSlotCompleted:
		s.RunReturns++
		// A Slot returned to be run again carries its reason as an error
		// when the view refused it; it is a retry either way, not a failure.
		switch {
		case o.Result == ResultRetrying:
			s.RetryingRunReturns++
		case o.Err != nil || o.Result == ResultFailed:
			s.FailedRunReturns++
		}
		if usage := o.SlotBudgetUsage; usage != nil {
			s.RetainedBytesPeak = max(s.RetainedBytesPeak, usage.RetainedBytes)
		}
		addWall(&s.RunWall, o)
		if trace.EvaluationTime > 0 {
			s.LagMeasured++
			s.MaxLagNS = max(s.MaxLagNS, max(0, now.Sub(time.Unix(trace.EvaluationTime, 0)).Nanoseconds()))
		} else {
			s.LagUnknown++
		}
	case StageProgressCommitted:
		if o.Err == nil && ValidProgressCompletionKind(o.ProgressCompletionKind) {
			s.ProgressCommits++
			s.LastProgressUnix = now.Unix()
			switch o.ProgressCompletionKind {
			case "COMPLETED_WITH_UNAVAILABLE", "SNAPSHOT_UNAVAILABLE":
				s.UnavailableCommits++
			case "GAP_SKIPPED":
				s.GapSkippedCommits++
			}
		}
	case StageEvaluationCompleted:
		s.Evaluations++
		if o.Err != nil || o.Result == ResultFailed {
			s.FailedEvaluations++
		}
		if o.EvaluationRecordsKnown || o.Counts.Records > 0 {
			s.EvaluationRecords += uint64(max(0, o.Counts.Records))
		} else {
			s.EvaluationRecordsUnknown++
		}
		addWall(&s.EvaluationWall, o)
	case StageQueryCompleted:
		s.QueryScopes++
		addWall(&s.QueryWall, o)
	case StageResourceHard:
		if !addRetainedStop(s, o) {
			return
		}
		// A Slot the pool refused has no completion row, so read from
		// completions alone the object that fills the pool every round is the
		// one object with no peak. The refusal names what the Slot held and
		// the addition that crossed the line; their sum is the least it would
		// have retained had it run through, and it is that Slot's peak.
		//
		// OwnUsed absent is not zero and not unknown: it is a nested holder
		// of a query-free finalization, whose figure does not describe the
		// execution (ownReservation withholds it for that reason), and an
		// increment folded in without it is not the same quantity as the
		// other samples. Skipped, rather than folded as a smaller number that
		// the max happens to hide.
		if f := o.CapacityRejection; f != nil && f.OwnUsed != nil {
			s.RetainedBytesPeak = max(s.RetainedBytesPeak, *f.OwnUsed+f.Requested)
		}
	case StageStatePreflight, StageStateApplied:
		s.StateCalls++
		if o.Counts.Keys > 0 {
			s.StateKeys += uint64(o.Counts.Keys)
		} else {
			s.StateKeysUnknown++
		}
		if o.Counts.StateBytes > 0 {
			s.StateBytes += uint64(o.Counts.StateBytes)
		} else {
			s.StateBytesUnknown++
		}
		addWall(&s.StateWall, o)
	}
}

// addRetainedStop counts one refusal of the retained-byte budget by which
// refusal it was: the pool full, or this object over its share. A refusal
// under any other word on this budget is neither, and reports so.
func addRetainedStop(s *CostScalars, o Observation) bool {
	switch string(o.ReasonCode) {
	case contract.ReasonResourceHardStop:
		s.RetainedHardStops++
	case contract.ReasonQGBudgetShareExceeded:
		s.RetainedShareStops++
	default:
		return false
	}
	return true
}

// costDimensions are the rankings a snapshot carries, per scope. query_wall
// and state_bytes were added for the two questions the others could not
// answer: which object's query is the slow one (run wall contains the wait
// for readiness and the state work), and which object's state is the big
// one (state_keys read a 249-key, 86 MB object as small).
//
// retained_bytes_peak ranks by the largest per-Slot retained bytes in the
// window: the objects that make up the replica's peak sum, in the order a
// placement decision would move them.
var costDimensions = [...]string{"evaluation_records", "evaluation_wall_observed", "state_calls", "state_keys", "run_wall", "lag", "query_wall", "state_bytes", "retained_bytes_peak"}

// CostDimensions is the closed list, for readers that bound a ranking by it.
func CostDimensions() []string { return append([]string(nil), costDimensions[:]...) }

func costRank(w costWindows, dimension int) int64 {
	a, b := w.current, w.previous
	switch dimension {
	case 0:
		return int64(a.EvaluationRecords + b.EvaluationRecords)
	case 1:
		return a.EvaluationWall.ObservedNS + b.EvaluationWall.ObservedNS
	case 2:
		return int64(a.StateCalls + b.StateCalls)
	case 3:
		return int64(a.StateKeys + b.StateKeys)
	case 4:
		return a.RunWall.ObservedNS + b.RunWall.ObservedNS
	case 5:
		return max(a.MaxLagNS, b.MaxLagNS)
	case 6:
		return a.QueryWall.ObservedNS + b.QueryWall.ObservedNS
	case 7:
		return int64(a.StateBytes + b.StateBytes)
	default:
		return int64(max(a.RetainedBytesPeak, b.RetainedBytesPeak))
	}
}

// costCopy is one tracked object's windows as Publish copied them under its
// group's account, beside the object they belong to, whose identity does not
// change once its roster is stored.
type costCopy struct {
	group   *costGroupState
	plan    *costPlanState
	windows costWindows
}

type costCandidate struct {
	copy  *costCopy
	value int64
}

// Publish computes bounded candidate rankings once on the existing report tick.
// Snapshot only reads this cache. No Redis, JSON, series copies, or I/O occurs
// here or on Observe. Unobserved catalog members are not measured-zero rows.
//
// Each group's windows, its own and its Plans', are rotated and copied under
// the group's account, one group at a time; the coverage, the rankings and
// the rows are made from the copy with no account held. Ranking under a lock
// Observe takes is what lost observations: the ranking is in proportion to
// the roster, and every observation that arrived while it ran was dropped.
func (c *CostSummary) Publish(now time.Time) {
	if c == nil || !c.enabled {
		return
	}
	c.publishing.Lock()
	defer c.publishing.Unlock()
	epoch := now.UnixNano() / int64(c.options.Window)
	start := time.Unix(0, epoch*int64(c.options.Window))
	snapshot := CostSnapshot{Enabled: true, ProcessID: c.options.ProcessID, Scope: "process_observed_candidates", GeneratedAt: now, WindowStart: start.Add(-c.options.Window), CurrentWindowStart: start, WindowEnd: now, CapacityBytesEstimated: c.capacityEstimate()}
	copies := c.copies[:0]
	if scope := c.scope.Load(); scope != nil {
		snapshot.Coverage = scope.coverage
		for _, g := range scope.groups {
			g.account.mu.Lock()
			g.account.windows.rotate(epoch)
			copies = append(copies, costCopy{group: g, windows: g.account.windows})
			for _, p := range g.plans {
				p.windows.rotate(epoch)
				copies = append(copies, costCopy{group: g, plan: p, windows: *p.windows})
			}
			g.account.mu.Unlock()
		}
	}
	c.copies = copies
	if c.rankingStarted != nil {
		c.rankingStarted()
	}
	snapshot.Coverage.UntrackedObservations = c.untracked.window(epoch)
	snapshot.Coverage.ContentionDroppedTotal = c.contentionDropped.Load()
	snapshot.Coverage.ContentionDropped = c.contention.window(epoch)
	var rankings [2 * len(costDimensions)][]costCandidate
	add := func(entry *costCopy) {
		for dim := range costDimensions {
			value := costRank(entry.windows, dim)
			if value <= 0 {
				continue
			}
			candidate := costCandidate{entry, value}
			if entry.plan != nil {
				dim += len(costDimensions)
			}
			rows := rankings[dim]
			at := sort.Search(len(rows), func(i int) bool {
				if rows[i].value != value {
					return rows[i].value < value
				}
				return costCandidateLess(candidate, rows[i])
			})
			if at >= c.options.TopN {
				continue
			}
			if len(rows) < c.options.TopN {
				rows = append(rows, costCandidate{})
			}
			copy(rows[at+1:], rows[at:len(rows)-1])
			rows[at] = candidate
			rankings[dim] = rows
		}
	}
	dueStart, dueEnd := snapshot.WindowStart.Unix(), snapshot.WindowEnd.Unix()
	// A group the window has no work of and whose rounds the scheduler held
	// is held, not unseen.
	//
	// A due Plan its group did not evaluate is counted by the round result
	// that accounts for it (costUnevaluated), and is unseen only when none
	// does: the window answers whether it missed what it should have seen,
	// not whether every due Plan was evaluated - that is the object's own
	// named result. What it should have seen and did not is a Plan left out
	// of a round that evaluated its siblings, a round that completed
	// normally and evaluated nothing, and a group with no record at all.
	held := make(map[*costGroupState]bool)
	groupWindows := make(map[*costGroupState]costWindows)
	for i := range copies {
		if copies[i].plan != nil {
			continue
		}
		groupWindows[copies[i].group] = copies[i].windows
		w := copies[i].windows
		if w.current.HeldRounds+w.previous.HeldRounds > 0 && w.current.Observations+w.previous.Observations == 0 {
			held[copies[i].group] = true
		}
	}
	snapshot.Coverage.UnevaluatedDuePlans = make([]CostUnevaluated, len(costUnevaluatedOutcomes))
	for i, outcome := range costUnevaluatedOutcomes {
		snapshot.Coverage.UnevaluatedDuePlans[i].Outcome = outcome
	}
	var misses []CostDueMiss
	var partial []CostPartialGroup
	for i := range copies {
		entry := &copies[i]
		w := entry.windows
		observed := w.current.Observations+w.previous.Observations > 0
		if entry.plan == nil {
			if observed {
				snapshot.Coverage.ObservedGroups++
			}
			if entry.group.dueIn(dueStart, dueEnd) {
				snapshot.Coverage.DueGroups++
				switch {
				case observed:
				case held[entry.group]:
					snapshot.Coverage.HeldDueGroups++
				default:
					snapshot.Coverage.UnobservedDueGroups++
					reason := costMissNoRecord
					if w.current.OtherRevisionObservations+w.previous.OtherRevisionObservations > 0 {
						reason = costMissRevisionChanged
					}
					misses = keepDueMiss(misses, costDueMissOf(entry.group, nil, w, reason))
				}
			}
			if entry.group.since.After(snapshot.WindowStart) {
				snapshot.Coverage.PartialWindowGroups++
				partial = keepPartialGroup(partial, CostPartialGroup{QueryGroupKey: entry.group.group.QueryGroupKey,
					TrackedSince: entry.group.since, Replaced: entry.group.replaced})
			}
			for _, s := range [2]CostScalars{w.current, w.previous} {
				snapshot.Coverage.UnknownWallObservations += s.EvaluationWall.Unknown + s.StateWall.Unknown + s.QueryWall.Unknown + s.RunWall.Unknown
			}
			snapshot.Coverage.UnattributedEvaluations += w.current.UnattributedEvaluations + w.previous.UnattributedEvaluations
			if peak := max(w.current.RetainedBytesPeak, w.previous.RetainedBytesPeak); peak > 0 {
				snapshot.Retained.PeakSumBytes += peak
				snapshot.Retained.GroupsWithPeak++
			}
		} else {
			if observed {
				snapshot.Coverage.ObservedPlans++
			}
			if entry.plan.due.dueIn(dueStart, dueEnd) {
				snapshot.Coverage.DuePlans++
				if !observed {
					groupWindow := groupWindows[entry.group]
					if outcome, accounted := costUnevaluated(groupWindow); accounted {
						for i := range snapshot.Coverage.UnevaluatedDuePlans {
							if snapshot.Coverage.UnevaluatedDuePlans[i].Outcome == outcome {
								snapshot.Coverage.UnevaluatedDuePlans[i].Plans++
							}
						}
					} else {
						snapshot.Coverage.UnobservedDuePlans++
						misses = keepDueMiss(misses, costDueMissOf(entry.group, entry.plan, groupWindow, outcome))
					}
				}
			}
			if entry.plan.since.After(snapshot.WindowStart) {
				snapshot.Coverage.PartialWindowPlans++
			}
		}
		add(entry)
	}
	snapshot.Retained.HardStops = c.hardStops.window(epoch)
	snapshot.Retained.ShareStops = c.shareStops.window(epoch)
	if limit := c.retainedLimit.Load(); limit > 0 {
		snapshot.Retained.LimitBytes, snapshot.Retained.LimitKnown = limit, true
		snapshot.Retained.PeakShare = float64(snapshot.Retained.PeakSumBytes) / float64(limit)
	}
	snapshot.Coverage.Incomplete = snapshot.Coverage.Incomplete || snapshot.Coverage.ContentionDropped > 0 || snapshot.Coverage.UntrackedObservations > 0 || snapshot.Coverage.UnattributedEvaluations > 0 || snapshot.Coverage.UnknownWallObservations > 0 || snapshot.Coverage.PartialWindowGroups > 0 || snapshot.Coverage.UnobservedDueGroups > 0 || snapshot.Coverage.UnobservedDuePlans > 0
	snapshot.Coverage.UnobservedDueSample = misses
	snapshot.Coverage.PartialWindowSample = partial
	indexes := make(map[*costCopy]int)
	for dim, rows := range rankings {
		ranking := CostRanking{Dimension: costDimensions[dim%len(costDimensions)], Scope: "query_group"}
		if dim >= len(costDimensions) {
			ranking.Scope = "strategy_owned"
		}
		for _, candidate := range rows {
			entry := candidate.copy
			index, exists := indexes[entry]
			if !exists {
				w, since := entry.windows, entry.group.since
				row := CostContributor{Scope: "query_group", Group: entry.group.group}
				if entry.plan != nil {
					row.Scope, row.Plan = "strategy_owned", entry.plan.identity
					row.Group.Members = nil
					since = entry.plan.since
				} else {
					row.Group.Members = append([]CostPlanIdentity(nil), row.Group.Members...)
				}
				row.TrackedSince, row.Observed, row.Current, row.Previous = since, w.current.Observations+w.previous.Observations > 0, w.current, w.previous
				index = len(snapshot.Contributors)
				indexes[entry] = index
				snapshot.Contributors = append(snapshot.Contributors, row)
			}
			ranking.Indexes = append(ranking.Indexes, index)
		}
		snapshot.Rankings = append(snapshot.Rankings, ranking)
	}
	// The copy keeps its windows for the next tick, not the roster it was
	// taken from.
	for i := range copies {
		copies[i].group, copies[i].plan = nil, nil
	}
	c.snapshot.Store(&snapshot)
}

func costCandidateLess(a, b costCandidate) bool {
	if a.copy.group.group.QueryGroupKey != b.copy.group.group.QueryGroupKey {
		return a.copy.group.group.QueryGroupKey < b.copy.group.group.QueryGroupKey
	}
	if a.copy.plan == nil || b.copy.plan == nil {
		return a.copy.plan == nil && b.copy.plan != nil
	}
	aID, bID := a.copy.plan.identity, b.copy.plan.identity
	if aID.TenantID != bID.TenantID {
		return aID.TenantID < bID.TenantID
	}
	if aID.BusinessID != bID.BusinessID {
		return aID.BusinessID < bID.BusinessID
	}
	return aID.StrategyID < bID.StrategyID
}

// Snapshot returns an independent copy of the last published candidates, not a
// fold over live objects. Callers can serialize/mutate it without racing Observe.
func (c *CostSummary) Snapshot() CostSnapshot {
	if c == nil {
		return CostSnapshot{DisabledReason: "resource_budget_missing", Coverage: CostCoverage{Incomplete: true}}
	}
	out := *c.snapshot.Load()
	out.Contributors = append([]CostContributor(nil), out.Contributors...)
	for i := range out.Contributors {
		out.Contributors[i].Group.Members = append([]CostPlanIdentity(nil), out.Contributors[i].Group.Members...)
	}
	out.Rankings = append([]CostRanking(nil), out.Rankings...)
	for i := range out.Rankings {
		out.Rankings[i].Indexes = append([]int(nil), out.Rankings[i].Indexes...)
	}
	out.Coverage.UnobservedDueSample = append([]CostDueMiss(nil), out.Coverage.UnobservedDueSample...)
	out.Coverage.UnevaluatedDuePlans = append([]CostUnevaluated(nil), out.Coverage.UnevaluatedDuePlans...)
	out.Coverage.PartialWindowSample = append([]CostPartialGroup(nil), out.Coverage.PartialWindowSample...)
	return out
}

// CostRetainedPeak is one observed Query Group's cost reading over the window,
// by the key the caller reconciled it under.
type CostRetainedPeak struct {
	QueryGroupKey     string
	RetainedBytesPeak uint64
	// ComputeWallNS is the evaluation and state wall this object spent: the
	// work this replica did for it. Query wall is deliberately not in it -
	// it contains the streaming callbacks and overlaps evaluation, so adding
	// the two double counts, and what it measures is the backend waiting
	// rather than this replica working. Run wall is not either: it contains
	// the readiness wait, which would report an object that spends half its
	// period waiting for data as the most expensive thing on the replica.
	ComputeWallNS int64
	// ComputeWallUnknown is how many of those observations carried no
	// duration. A window with any of them cannot be divided into a rate: the
	// numerator is short by an unknown amount, and a rate that is quietly low
	// is the one that leaves an object where it is.
	ComputeWallUnknown uint64
}

// RetainedPeaks is every observed group's retained-byte peak over the window.
//
// The same number Publish sums into Retained.PeakSumBytes, taken the same way
// -- the larger of the two windows, because the window the reading is for is
// the one that has not finished rotating. Exposed per group so the Worker's
// heartbeat reports what the read-only column shows, from one derivation
// rather than two: a second accumulator over the same observations would
// answer the same question with a different number, and the difference would
// be invisible on both pages.
//
// Read-only. It does not rotate the windows, so calling it between Publish
// ticks neither advances nor disturbs them. Each group's windows are read
// under its own account, one group at a time, and the list is sorted with
// none held.
func (c *CostSummary) RetainedPeaks() []CostRetainedPeak {
	if c == nil || !c.enabled {
		return nil
	}
	scope := c.scope.Load()
	if scope == nil {
		return []CostRetainedPeak{}
	}
	peaks := make([]CostRetainedPeak, 0, len(scope.groups))
	for key, g := range scope.groups {
		g.account.mu.Lock()
		w := g.account.windows
		g.account.mu.Unlock()
		reading := CostRetainedPeak{QueryGroupKey: key,
			RetainedBytesPeak: max(w.current.RetainedBytesPeak, w.previous.RetainedBytesPeak)}
		for _, s := range [2]CostScalars{w.current, w.previous} {
			reading.ComputeWallNS += s.EvaluationWall.ObservedNS + s.StateWall.ObservedNS
			reading.ComputeWallUnknown += s.EvaluationWall.Unknown + s.StateWall.Unknown
		}
		if reading.RetainedBytesPeak > 0 || reading.ComputeWallNS > 0 {
			peaks = append(peaks, reading)
		}
	}
	sort.Slice(peaks, func(i, j int) bool { return peaks[i].QueryGroupKey < peaks[j].QueryGroupKey })
	return peaks
}
