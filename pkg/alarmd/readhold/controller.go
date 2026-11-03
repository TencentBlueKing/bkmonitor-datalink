// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package readhold

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

const (
	RecordTTL              = 7 * 24 * time.Hour
	RenewInterval          = 24 * time.Hour
	SingleEventQuiet       = time.Hour
	EarlierMatchesRequired = 3
	// FloorDifferentsRequired is the different trials in a row at one
	// candidate that move the search floor up to it: one late round alone
	// does not, as one early round alone does not lower the hold.
	FloorDifferentsRequired = 2
)

var (
	ErrNotRestored   = errors.New("alarmd readhold: Query Group not restored")
	ErrNotConfigured = errors.New("alarmd readhold: Query Group not configured")
	ErrConflict      = errors.New("alarmd readhold: record changed")
	ErrSegmentStale  = errors.New("alarmd readhold: schedule segment already replaced")
	// ErrRecordCorrupt is a stored record that does not decode. The group
	// it belongs to is still restored: its own is replaced at the next write
	// (seed), a predecessor's is read as unknown (previousHold).
	ErrRecordCorrupt = errors.New("alarmd readhold: stored record does not decode")
)

// What a successor took each moved Plan's previous hold from
// (previousHold): its predecessor's closed record, the record's absence --
// zero -- or, for a record open, corrupt or unread, the hold bound, which no
// hold exceeds.
const (
	PredecessorInherited     = "inherited"
	PredecessorZero          = "zero"
	PredecessorRecordOpen    = "record_open"
	PredecessorRecordCorrupt = "record_corrupt"
	PredecessorUnreadable    = "record_unreadable"
)

// PredecessorReasons is the closed list of them.
var PredecessorReasons = []string{PredecessorInherited, PredecessorZero, PredecessorRecordOpen, PredecessorRecordCorrupt, PredecessorUnreadable}

// LinkSkipReasons are the links a successor skips before reading any
// predecessor, counted beside PredecessorReasons: one naming its own group,
// one with a shape no cutover writes, and one past its lifetime.
var LinkSkipReasons = []string{"self_link", "invalid_link", "expired"}

// A Slot whose transition asked for more than the group's hold limit is
// frozen at the limit (SlotReadHold): ClampKnown when the hold that asked
// for it was known, the one ordering a successor could still lose;
// ClampFallback when only a bound standing in for an unknown hold did.
const (
	ClampKnown    = "known"
	ClampFallback = "fallback"
)

// ClampSources is the closed list of them.
var ClampSources = []string{ClampKnown, ClampFallback}

// A Slot whose group's hold could not be prepared or written is frozen with
// the hold the group last read, by what failed: the group's route and delay
// (spec_unreadable), its previous Segment's schedule (previous_unreadable),
// its predecessor links (predecessors_unreadable), a spec the controller
// refused (spec_rejected), closing a Segment (close_failed), a Slot of a
// Segment older than the group's record (stale_segment), the Slot's own hold
// (hold_failed), or the group's record answered by Redis with an error of
// its own (record_unreadable). Ownership and the group's record unread for
// want of an answer still refuse the Slot; nothing else about the hold does.
const (
	DegradedSpecUnreadable         = "spec_unreadable"
	DegradedPreviousUnreadable     = "previous_unreadable"
	DegradedPredecessorsUnreadable = "predecessors_unreadable"
	DegradedSpecRejected           = "spec_rejected"
	DegradedCloseFailed            = "close_failed"
	DegradedStaleSegment           = "stale_segment"
	DegradedHoldFailed             = "hold_failed"
	DegradedRecordUnreadable       = "record_unreadable"
)

// DegradedReasons is the closed list of them.
var DegradedReasons = []string{DegradedSpecUnreadable, DegradedPreviousUnreadable, DegradedPredecessorsUnreadable,
	DegradedSpecRejected, DegradedCloseFailed, DegradedStaleSegment, DegradedHoldFailed, DegradedRecordUnreadable}

// Stats are the controller's counts since it started.
type Stats struct {
	Predecessors map[string]uint64
	Clamped      map[string]uint64
	// OwnCorrupt is the groups whose own record did not decode: read as
	// missing, and replaced at the group's next write.
	OwnCorrupt uint64
	// Seeds is the groups that started from a seeded arrival age, by where
	// it came from (SeedOrigins).
	Seeds map[string]uint64
	// QueryDurationWrites is the records written for the query duration they
	// keep, by why (QueryDurationWriteReasons).
	QueryDurationWrites map[string]uint64
	// EarlierSkipped is the earlier-read trials not run, by why
	// (EarlierSkipReasons).
	EarlierSkipped map[string]uint64
	// FloorProbes is the shadow probes whose end this controller took, by
	// what it made of them (FloorProbeOutcomes), every kind together, and
	// FloorProbesByKind by kind (FloorProbeKinds) and outcome.
	FloorProbes       map[string]uint64
	FloorProbesByKind map[string]map[string]uint64
	// ProbationFindings is the probation findings this controller took, by
	// what it made of them (ProbationFindingOutcomes).
	ProbationFindings map[string]uint64
	// ProbeProgress is the probe progress writes, by what came of them
	// (ProbeProgressOutcomes).
	ProbeProgress map[string]uint64
}

// What a probation's finding made of the group's record, closed: undone, a
// lowering put back to the hold it came from; quiet, nothing to undo - the
// group not lowered - and its quiet started over.
const (
	ProbationFindingUndone = "undone"
	ProbationFindingQuiet  = "quiet"
)

// ProbationFindingOutcomes is every way a probation's finding is taken.
var ProbationFindingOutcomes = []string{ProbationFindingUndone, ProbationFindingQuiet}

// Why an earlier-read trial was not run, closed. at_limit: the group is held
// at its limit (Record.HeldAtLimit) and has not been quiet for its bar since
// (atLimitHeld), so its data arrives past the longest hold and a trial below
// it can show nothing about the hold. search_done: the search below the hold
// has run out, the gap between its floor and the hold no wider than a step
// (execution.CandidateReadHold); its record says when the floor was learned
// (Record.EarlierFloorAtMillis).
const (
	EarlierSkipAtLimit    = "at_limit"
	EarlierSkipSearchDone = "search_done"
)

// EarlierSkipReasons is every reason a trial is not run.
var EarlierSkipReasons = []string{EarlierSkipAtLimit, EarlierSkipSearchDone}

// Control uses the existing ownership store. A value and its Plan
// evidence are one fenced CAS in the owning Query Group's Redis hash slot.
type Control interface {
	ReadControlBatch(context.Context, []execution.QueryGroupIdentity, string) ([]ownership.ControlRead, error)
	FencedCompareAndSet(context.Context, ownership.FencedCASRequest) (ownership.FencedCASStatus, error)
}

type Owner struct {
	Fence        execution.OwnerFence
	ContentScope string
}

type Options struct {
	Control Control
	Prefix  string
	MaxHold time.Duration
	Now     func() time.Time
	Owner   func(execution.QueryGroupIdentity) (Owner, error)
	// OnLimitCut, when set, is told of each Slot frozen more than a step
	// below the Slot before it because the limit was configured under the
	// hold. Called with the group's lock held, after the Slot is frozen.
	OnLimitCut func(LimitCut)
}

// LimitCut is a Slot frozen at a limit configured under the hold, more than
// a step below the Slot before it: a cut the stepping does not soften, so
// the Slot may be ready before the one before it is read and fall behind by
// more Slots than a replay covers. Period is the shortest interval the
// group's Plans are detected at.
type LimitCut struct {
	QueryGroup execution.QueryGroupIdentity
	Slot       execution.EvaluationTime
	From       time.Duration
	To         time.Duration
	Period     time.Duration
}

type PlanRef struct {
	Key execution.PlanKey `json:"key"`
}

// PlanRecord is the bridge to a replacement Query Group. Its previous Slot
// is taken from the closed schedule, never from a lagging runtime cursor.
type PlanRecord struct {
	PlanRef
	ClosedAt               execution.EvaluationTime     `json:"closed_at,omitempty"`
	ClosedQueryGroup       execution.QueryGroupIdentity `json:"closed_query_group,omitempty"`
	InheritedQueryGroup    execution.QueryGroupIdentity `json:"inherited_query_group,omitempty"`
	InheritedClosedAt      execution.EvaluationTime     `json:"inherited_closed_at,omitempty"`
	PreviousHoldUnknown    bool                         `json:"previous_hold_unknown,omitempty"`
	PreviousHoldMillis     int64                        `json:"previous_hold_ms,omitempty"`
	PreviousSlot           execution.EvaluationTime     `json:"previous_slot,omitempty"`
	CompletionOffsetMillis int64                        `json:"completion_offset_ms,omitempty"`
}

type Transition struct {
	Key            execution.PlanKey `json:"key"`
	DeadlineMillis int64             `json:"deadline_ms"`
	// Fallback is a deadline taken from the hold bound in place of a
	// predecessor hold nobody could read: it may ask for more than needed.
	Fallback bool `json:"fallback,omitempty"`
}

type Previous struct {
	QueryGroup execution.QueryGroupIdentity
	ClosedAt   execution.EvaluationTime
	// Links are this activation's Plans that left the group at ClosedAt.
	// Different Plans can leave the same Query Group at different
	// boundaries.
	Links []PlanLink
}

// PlanLink is one moved Plan as its link says, from the cutover that closed
// the old Segment: nothing of the old group's timeline or content is read.
type PlanLink struct {
	PlanRef
	// PreviousSlot is the Plan's last legal Slot in the Segment it left and
	// CompletionOffsetMillis its completion offset there; zero when that
	// Segment held no Slot of it, and there is then no deadline to keep.
	PreviousSlot           execution.EvaluationTime
	CompletionOffsetMillis int64
}

type GroupSpec struct {
	QueryGroup   execution.QueryGroupIdentity
	Plans        []PlanRef
	Delay        time.Duration
	SettlingWait time.Duration
	// HoldLimit is snapshot retention minus its existing minimum lifetime.
	// Zero means no margin, not an unset limit.
	HoldLimit time.Duration
	// Step is the query's data step, below which a lowering goes to zero
	// (execution.LoweredReadHold): the step the early read lowers by too.
	Step     time.Duration
	Previous []Previous
	// SeedArrivalAge is the arrival age a group with no record of its own
	// starts from, and SeedOrigin where it came from (SeedPeer,
	// SeedColdStart). Zero seeds nothing: such a group starts at no hold.
	SeedArrivalAge time.Duration
	SeedOrigin     string
}

// Where a group's first arrival age came from: the measured arrival of the
// other groups reading the same data source, or, on a source no group has
// measured, the cold-start age of a new source.
const (
	SeedPeer      = "peer"
	SeedColdStart = "cold_start"
)

// SeedOrigins is the closed list of them.
var SeedOrigins = []string{SeedPeer, SeedColdStart}

type Evidence struct {
	Contract   execution.FrozenExecutionContractRef
	ArrivalAge time.Duration
	Rung       string
	Buckets    []int64
	Noise      bool
	// Probation is a probation reread's finding: the group's lowering was
	// wrong, and is undone whatever ArrivalAge says (Observe).
	Probation bool
}

type EarlierEvidence struct {
	Contract      execution.FrozenExecutionContractRef
	CandidateHold time.Duration
	Observed      bool
	Equal         bool
}

type entry struct {
	mu                         sync.Mutex
	spec                       GroupSpec
	configured, loaded, seeded bool
	record                     Record
	raw                        []byte
	// corrupt is a stored record that did not decode: raw is its bytes, so
	// the next write replaces it, and record starts empty.
	corrupt bool
	// validated is the Segment whose schedule SlotReadHold last checked: a
	// frozen schedule is checked once per Segment, not once per Slot.
	validated execution.ScheduleSegmentFact
	// measuredQueryDuration is the group's query duration as this process
	// last measured it (MeasureQueryDuration), for its record to keep.
	measuredQueryDuration time.Duration
}

type Controller struct {
	options Options
	mu      sync.RWMutex
	groups  map[execution.QueryGroupIdentity]*entry

	statsMu      sync.Mutex
	predecessors map[string]uint64
	clamped      map[string]uint64
	seeds        map[string]uint64
	ownCorrupt   uint64
	// earlierSkipped is the earlier-read trials not run, by why
	// (EarlierSkipReasons).
	earlierSkipped map[string]uint64
	// floorProbes is the shadow probes taken, by kind|outcome
	// (floorProbeKey), and probationFindings the probation findings
	// (ProbationFindingOutcomes).
	floorProbes       map[string]uint64
	probationFindings map[string]uint64
	// probeProgress is the probe progress writes, by what came of them
	// (ProbeProgressOutcomes).
	probeProgress map[string]uint64
	// durationWrites is the records written for a query duration, by why
	// (QueryDurationWriteReasons).
	durationWrites map[string]uint64
}

func NewController(options Options) (*Controller, error) {
	if options.Control == nil || options.Prefix == "" || options.Owner == nil || options.MaxHold <= 0 ||
		options.MaxHold.Milliseconds() > execution.MaxReadHoldMillis {
		return nil, errors.New("alarmd readhold: invalid controller options")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Controller{options: options, groups: make(map[execution.QueryGroupIdentity]*entry),
		predecessors: make(map[string]uint64), clamped: make(map[string]uint64), seeds: make(map[string]uint64),
		durationWrites: make(map[string]uint64), earlierSkipped: make(map[string]uint64), floorProbes: make(map[string]uint64),
		probationFindings: make(map[string]uint64), probeProgress: make(map[string]uint64)}, nil
}

// Stats copies the counts.
func (controller *Controller) Stats() Stats {
	controller.statsMu.Lock()
	defer controller.statsMu.Unlock()
	stats := Stats{Predecessors: make(map[string]uint64, len(controller.predecessors)),
		Clamped: make(map[string]uint64, len(controller.clamped)), OwnCorrupt: controller.ownCorrupt}
	for reason, count := range controller.predecessors {
		stats.Predecessors[reason] = count
	}
	for source, count := range controller.clamped {
		stats.Clamped[source] = count
	}
	if len(controller.seeds) > 0 {
		stats.Seeds = make(map[string]uint64, len(controller.seeds))
		for origin, count := range controller.seeds {
			stats.Seeds[origin] = count
		}
	}
	stats.EarlierSkipped = make(map[string]uint64, len(EarlierSkipReasons))
	for _, reason := range EarlierSkipReasons {
		stats.EarlierSkipped[reason] = controller.earlierSkipped[reason]
	}
	stats.FloorProbes = make(map[string]uint64, len(FloorProbeOutcomes))
	stats.FloorProbesByKind = make(map[string]map[string]uint64, len(FloorProbeKinds))
	for _, kind := range FloorProbeKinds {
		stats.FloorProbesByKind[kind] = make(map[string]uint64, len(FloorProbeOutcomes))
		for _, outcome := range FloorProbeOutcomes {
			count := controller.floorProbes[floorProbeKey(kind, outcome)]
			stats.FloorProbesByKind[kind][outcome] = count
			stats.FloorProbes[outcome] += count
		}
	}
	stats.ProbeProgress = make(map[string]uint64, len(ProbeProgressOutcomes))
	for _, outcome := range ProbeProgressOutcomes {
		stats.ProbeProgress[outcome] = controller.probeProgress[outcome]
	}
	stats.ProbationFindings = make(map[string]uint64, len(ProbationFindingOutcomes))
	for _, outcome := range ProbationFindingOutcomes {
		stats.ProbationFindings[outcome] = controller.probationFindings[outcome]
	}
	stats.QueryDurationWrites = make(map[string]uint64, len(QueryDurationWriteReasons))
	for _, reason := range QueryDurationWriteReasons {
		stats.QueryDurationWrites[reason] = controller.durationWrites[reason]
	}
	return stats
}

func (controller *Controller) counted(counts map[string]uint64, key string) {
	controller.statsMu.Lock()
	counts[key]++
	controller.statsMu.Unlock()
}

func (controller *Controller) group(qg execution.QueryGroupIdentity) *entry {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.groups[qg] == nil {
		controller.groups[qg] = &entry{}
	}
	return controller.groups[qg]
}

func (controller *Controller) Configure(spec GroupSpec) error {
	if spec.QueryGroup == "" || len(spec.Plans) == 0 || spec.Delay < 0 || spec.SettlingWait < 0 ||
		spec.HoldLimit < 0 || spec.HoldLimit.Milliseconds() > execution.MaxReadHoldMillis {
		return errors.New("alarmd readhold: invalid Query Group spec")
	}
	for _, plan := range spec.Plans {
		if plan.Key.PlanIdentity.Validate() != nil {
			return errors.New("alarmd readhold: invalid Plan")
		}
	}
	for _, previous := range spec.Previous {
		if previous.QueryGroup == "" || previous.QueryGroup == spec.QueryGroup || previous.ClosedAt <= 0 || len(previous.Links) == 0 {
			return errors.New("alarmd readhold: invalid predecessor")
		}
		for _, link := range previous.Links {
			found := false
			for _, wanted := range spec.Plans {
				found = found || wanted == link.PlanRef
			}
			if !found || link.PreviousSlot < 0 || link.PreviousSlot >= previous.ClosedAt || link.CompletionOffsetMillis < 0 {
				return errors.New("alarmd readhold: predecessor Plan not configured")
			}
		}
	}
	spec.Plans = append([]PlanRef(nil), spec.Plans...)
	spec.Previous = append([]Previous(nil), spec.Previous...)
	for index := range spec.Previous {
		spec.Previous[index].Links = append([]PlanLink(nil), spec.Previous[index].Links...)
	}
	state := controller.group(spec.QueryGroup)
	state.mu.Lock()
	defer state.mu.Unlock()
	state.spec, state.configured, state.seeded = spec, true, false
	return nil
}

// RestoreBatch runs before replay classification. An unanswered/corrupt entry
// is left unready; successful entries of the same batch can still proceed.
func (controller *Controller) RestoreBatch(ctx context.Context, groups []execution.QueryGroupIdentity) error {
	for _, qg := range groups {
		state := controller.group(qg)
		state.mu.Lock()
		state.loaded = false
		state.mu.Unlock()
	}
	reads, err := controller.options.Control.ReadControlBatch(ctx, groups, controller.options.Prefix+":"+Namespace)
	if err != nil {
		return err
	}
	if len(reads) != len(groups) {
		return errors.New("alarmd readhold: incomplete control batch")
	}
	var first error
	for index, read := range reads {
		if read.Err != nil {
			if first == nil {
				first = fmt.Errorf("readhold %s: %w", groups[index], read.Err)
			}
			continue
		}
		record, corrupt := Record{SinceSlot: 1}, false
		if !read.Missing {
			decoded, err := Decode(read.Raw)
			if err != nil {
				// Restored all the same, empty and marked: a record that will
				// never decode must not keep its group unrestored until it
				// expires a week later.
				corrupt = true
				if first == nil {
					first = fmt.Errorf("readhold %s: %w: %w", groups[index], ErrRecordCorrupt, err)
				}
			} else {
				record = decoded
			}
		}
		state := controller.group(groups[index])
		state.mu.Lock()
		state.record, state.raw, state.loaded, state.seeded, state.corrupt = record, append([]byte(nil), read.Raw...), true, false, corrupt
		state.mu.Unlock()
	}
	return first
}

func current(record Record) int64 {
	if record.PendingHoldMillis != nil {
		return *record.PendingHoldMillis
	}
	return record.HoldMillis
}

func holdAt(record Record, at execution.EvaluationTime, wait time.Duration) int64 {
	hold := current(record)
	for _, transition := range record.Transitions {
		hold = max(hold, transition.DeadlineMillis-int64(at)*1000-wait.Milliseconds())
	}
	return max(hold, 0)
}

func (controller *Controller) ReadHold(qg execution.QueryGroupIdentity) time.Duration {
	state := controller.group(qg)
	state.mu.Lock()
	defer state.mu.Unlock()
	return time.Duration(current(state.record)) * time.Millisecond
}

// nextCandidate is the hold the next earlier-read trial of record tries
// (execution.CandidateReadHold), in milliseconds, and false when none is,
// at now.
func nextCandidate(qg execution.QueryGroupIdentity, record Record, step time.Duration, now int64) (int64, bool) {
	// A group held at its limit has data arriving past the longest hold it
	// may have: the earlier read and the formal read both come before the
	// data, so their agreeing says the two are equally incomplete, not that
	// the data was there. No trial, and no evidence taken from one.
	if atLimitHeld(qg, record, now) {
		return 0, false
	}
	// A candidate the trials chose is read by a probe before the hold moves,
	// and no trial runs meanwhile: one trial would only choose it again.
	if _, ok := verifying(qg, record, now); ok {
		return 0, false
	}
	floor, floored := time.Duration(0), record.EarlierFloorMillis != nil
	if floored {
		floor = time.Duration(*record.EarlierFloorMillis) * time.Millisecond
	}
	hold, ok := execution.CandidateReadHold(time.Duration(current(record))*time.Millisecond, floor, floored, step)
	return hold.Milliseconds(), ok
}

// atLimitHeld says record is held at its limit with its data still past it:
// read early within the quiet bar (quietBar). Only a read-early arrival
// moves a group at its limit, and a group whose data has come back in time
// has none to give, so once it has been quiet for the bar its trials run
// again, and the lowering they lead to waits for the same quiet. A source
// still landing past the limit is read early again within the bar, which
// starts its quiet over: it is never tried while it does.
func atLimitHeld(qg execution.QueryGroupIdentity, record Record, now int64) bool {
	return record.AtLimitHeld(qg, now)
}

// AtLimitHeld says the record is held at its limit with its data still
// landing past it at now, Unix milliseconds: held at the limit
// (HeldAtLimit) and read early within its quiet bar since. A group held at
// its limit whose data has been quiet for its bar is being tried lower and
// is not one; every reading of "its data lands past the limit" - the trial
// gate, a round read at the limit, the strategy page - asks this.
func (record Record) AtLimitHeld(qg execution.QueryGroupIdentity, now int64) bool {
	if !record.HeldAtLimit() {
		return false
	}
	return record.QuietSinceMillis <= 0 || now-record.QuietSinceMillis < quietBar(qg, record, now)
}

// clearFloor forgets the search below the hold: the hold moved up or was
// set anew, so what was too early under the old one says nothing now, nor
// do the probes of it that failed.
func clearFloor(record *Record) {
	record.EarlierFloorMillis, record.EarlierFloorAtMillis, record.EarlierDifferents = nil, 0, 0
	record.EarlierFloorRetests, record.EarlierFloorProbed = 0, false
}

// setFloor makes floor the search's floor, learned at now, and starts the
// different trials in a row over: they count at the candidate above it. A
// floor that takes another value starts its probes' standing over too, and
// no probe has failed at it yet.
func setFloor(record *Record, floor, now int64) {
	if record.EarlierFloorMillis == nil || *record.EarlierFloorMillis != floor {
		record.EarlierFloorRetests, record.EarlierFloorProbed = 0, false
	}
	record.EarlierFloorMillis, record.EarlierFloorAtMillis, record.EarlierDifferents = &floor, now, 0
}

// EarlierCandidate is the hold the group's next earlier-read trial tries,
// and false when there is none: the one candidate both the lookback that
// reserves the trial and EarlierRead that takes its evidence use.
func (controller *Controller) EarlierCandidate(qg execution.QueryGroupIdentity) (time.Duration, bool) {
	state := controller.group(qg)
	state.mu.Lock()
	defer state.mu.Unlock()
	hold, ok := nextCandidate(qg, state.record, state.spec.Step, controller.options.Now().UnixMilli())
	return time.Duration(hold) * time.Millisecond, ok
}

// EarlierRefused hears that the lookback would have reserved a trial for one
// of the group's Slots and EarlierCandidate gave it none: a trial not run,
// counted by why, so a group never lowered for it says so
// (EarlierSkipReasons).
func (controller *Controller) EarlierRefused(qg execution.QueryGroupIdentity) {
	state := controller.group(qg)
	state.mu.Lock()
	defer state.mu.Unlock()
	switch now := controller.options.Now().UnixMilli(); {
	case atLimitHeld(qg, state.record, now):
		controller.counted(controller.earlierSkipped, EarlierSkipAtLimit)
	case current(state.record) > 0:
		// Held, and not at the limit: no candidate is left above the floor.
		controller.counted(controller.earlierSkipped, EarlierSkipSearchDone)
	}
}

func (controller *Controller) ReadHoldAt(qg execution.QueryGroupIdentity, at execution.EvaluationTime) time.Duration {
	state := controller.group(qg)
	state.mu.Lock()
	defer state.mu.Unlock()
	return time.Duration(holdAt(state.record, at, state.spec.SettlingWait)) * time.Millisecond
}

// LastLowering is the Slot the group's frozen hold last changed at, while
// that change was a lowering - each step of a lowering a step per Slot among
// them, a lowering a shadow probe verified too - and false when it was not,
// or the record is not loaded. A process
// that takes the group over reads it from the record the old owner left, so
// the probation after a lowering outlives the process it began in.
func (controller *Controller) LastLowering(qg execution.QueryGroupIdentity) (execution.EvaluationTime, bool) {
	state := controller.group(qg)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.loaded || state.record.SinceSlot <= 0 || state.record.PreviousHoldMillis <= state.record.HoldMillis {
		return 0, false
	}
	return state.record.SinceSlot, true
}

func (controller *Controller) Reading(qg execution.QueryGroupIdentity) (Record, bool) {
	state := controller.group(qg)
	state.mu.Lock()
	defer state.mu.Unlock()
	return clone(state.record), state.loaded
}

type Inspection struct {
	Record  Record
	Loaded  bool
	Missing bool
	Seeded  bool
	// Corrupt is a stored record that did not decode; Record is then empty.
	Corrupt bool
}

// Hold is the read hold the record states - the pending one while a change
// is pending, the current one otherwise - and whether it is known. A record
// read and decoded is known, and so is none at all: a group that has only
// ever held nothing keeps no record. A record not read yet, or one that did
// not decode, is not known, and every reader says so rather than zero.
//
// One group reads zero here and may not be: a group never prepared, whose
// Plan moved from a group that held, can be held by the transition its first
// prepare seeds; until that first Slot it reads zero.
func (inspection Inspection) Hold() (int64, bool) {
	if !inspection.Loaded || inspection.Corrupt {
		return 0, false
	}
	return current(inspection.Record), true
}

// Hold is the group's read hold as its record states it, and whether it is
// known (Inspection.Hold).
func (controller *Controller) Hold(qg execution.QueryGroupIdentity) (int64, bool) {
	return controller.Inspect(qg).Hold()
}

func (controller *Controller) Inspect(qg execution.QueryGroupIdentity) Inspection {
	controller.mu.RLock()
	state := controller.groups[qg]
	controller.mu.RUnlock()
	if state == nil {
		return Inspection{}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return Inspection{Record: clone(state.record), Loaded: state.loaded, Missing: state.loaded && len(state.raw) == 0, Seeded: state.seeded,
		Corrupt: state.corrupt}
}

// Arrival is the group's arrival age and where it came from (empty for a
// measured one), read without copying the record: a new group's seed reads
// every peer on its source.
func (controller *Controller) Arrival(qg execution.QueryGroupIdentity) (int64, string) {
	controller.mu.RLock()
	state := controller.groups[qg]
	controller.mu.RUnlock()
	if state == nil {
		return 0, ""
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.record.ArrivalAgeMillis, state.record.ArrivalAgeSeed
}

// HasOwnRecord is whether the group's own record has been read and is there
// and decodes: such a group is never seeded, so no seed need be worked out.
func (controller *Controller) HasOwnRecord(qg execution.QueryGroupIdentity) bool {
	controller.mu.RLock()
	state := controller.groups[qg]
	controller.mu.RUnlock()
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.loaded && len(state.raw) > 0 && !state.corrupt
}

func clone(record Record) Record {
	if record.PendingHoldMillis != nil {
		hold := *record.PendingHoldMillis
		record.PendingHoldMillis = &hold
	}
	if record.EarlierFloorMillis != nil {
		floor := *record.EarlierFloorMillis
		record.EarlierFloorMillis = &floor
	}
	if record.VerifyCandidateMillis != nil {
		candidate := *record.VerifyCandidateMillis
		record.VerifyCandidateMillis = &candidate
	}
	if record.Probe != nil {
		probe := *record.Probe
		if probe.FloorMillis != nil {
			floor := *probe.FloorMillis
			probe.FloorMillis = &floor
		}
		record.Probe = &probe
	}
	record.Buckets = append([]int64(nil), record.Buckets...)
	record.EarlyGaps = append([]EarlyGap(nil), record.EarlyGaps...)
	record.Plans = append([]PlanRecord(nil), record.Plans...)
	record.Transitions = append([]Transition(nil), record.Transitions...)
	return record
}

func ready(state *entry) error {
	if !state.loaded {
		return ErrNotRestored
	}
	if !state.configured {
		return ErrNotConfigured
	}
	return nil
}

// persist publishes the new in-memory value only after the fenced CAS wins.
// A conflict invalidates the entry for the caller's next batched reload.
func (controller *Controller) persist(ctx context.Context, qg execution.QueryGroupIdentity, state *entry, next Record, owner *Owner) error {
	if owner == nil {
		value, err := controller.options.Owner(qg)
		if err != nil {
			return err
		}
		owner = &value
	}
	if owner.Fence.QueryGroup != qg {
		return errors.New("alarmd readhold: owner differs from Query Group")
	}
	prunePlans(&next, state.spec.Plans, controller.options.Now().Unix()-int64(RecordTTL/time.Second))
	// A probe's progress kept only while it is of the probe the record has
	// now: a write that moves what the probe reads clears it (ProbeProgress).
	settleProgress(qg, &next, state.spec.Step, controller.options.Now().UnixMilli())
	next.RenewedAtMillis = controller.options.Now().UnixMilli()
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if _, err := Decode(raw); err != nil {
		return err
	}
	// Counted against what the store keeps, whatever writes it: a reading
	// noted while the record was kept only here is first written by whichever
	// change first stores the record.
	var durable *Record
	if len(state.raw) > 0 && !state.corrupt {
		durable = &state.record
	}
	durationWrite := queryDurationWrite(next, durable)
	status, err := controller.options.Control.FencedCompareAndSet(ctx, ownership.FencedCASRequest{
		Fence: owner.Fence, ContentScope: owner.ContentScope, Namespace: controller.options.Prefix + ":" + Namespace,
		ExpectedMissing: len(state.raw) == 0, Expected: state.raw, Value: raw, TTL: RecordTTL,
	})
	if err != nil {
		return err
	}
	switch status {
	case ownership.FencedCASApplied:
		state.record, state.raw, state.corrupt = next, raw, false
		if durationWrite != "" {
			controller.counted(controller.durationWrites, durationWrite)
		}
		return nil
	case ownership.FencedCASConflict:
		state.loaded = false
		return ErrConflict
	case ownership.FencedCASStaleOwner:
		return ownership.ErrStaleFence
	case ownership.FencedCASContentMoved:
		return ownership.ErrContentScopeMoved
	default:
		return errors.New("alarmd readhold: invalid CAS status")
	}
}

func inherited(record Record, spec GroupSpec) bool {
	for _, predecessor := range spec.Previous {
		for _, link := range predecessor.Links {
			found := false
			for _, plan := range record.Plans {
				found = found || (plan.PlanRef == link.PlanRef && plan.InheritedClosedAt == predecessor.ClosedAt && plan.InheritedQueryGroup == predecessor.QueryGroup)
			}
			if !found {
				return false
			}
		}
	}
	return true
}

// previousFact is what a successor takes from its predecessor for one moved
// Plan: the Plan's last Slot there and its offset, the hold that Slot was
// frozen with, what that hold was taken from, and whether it is the bound
// standing in for one nobody could read.
type previousFact struct {
	slot   execution.EvaluationTime
	offset int64
	hold   int64
	reason string
	bound  bool
}

// previousHold reads one moved Plan's previousFact from the old group's
// record alone, and never fails: a group that waited on its predecessor's
// facts could be stopped by them for good.
//
//   - Closed at this boundary: the hold the old owner fixed, exactly.
//   - Missing: zero. A nonzero hold always has a record, so a group at zero
//     -- one alarmd has never held -- reads at once after an edit.
//   - Open, corrupt or unread: the hold bound. No hold exceeds it, so the
//     deadline taken from it is never earlier than the real one (h design
//     section 5); only a held or a faulty predecessor costs its successor's
//     first Slots a later read, and no Slot is refused.
//
// Two residual risks are accepted and counted by overtaken: an old group at
// zero that raises its hold after this read and before freezing its last
// Slot (within one period of an edit), and a held group's record gone by its
// TTL or by Redis evicting it, read as zero.
func (controller *Controller) previousHold(old Inspection, predecessor Previous, link PlanLink) previousFact {
	fact := previousFact{slot: link.PreviousSlot, offset: link.CompletionOffsetMillis}
	bounded := func(reason string) previousFact {
		fact.hold, fact.reason, fact.bound = controller.options.MaxHold.Milliseconds(), reason, true
		return fact
	}
	switch {
	case !old.Loaded:
		return bounded(PredecessorUnreadable)
	case old.Corrupt:
		return bounded(PredecessorRecordCorrupt)
	case old.Missing:
		fact.reason = PredecessorZero
		return fact
	}
	for _, plan := range old.Record.Plans {
		// By key: a Plan whose route changed is the same Plan.
		if plan.Key == link.Key && plan.ClosedAt == predecessor.ClosedAt &&
			(plan.ClosedQueryGroup == "" || plan.ClosedQueryGroup == predecessor.QueryGroup) {
			// The old owner's own bound for a slow Plan's final Slot it no
			// longer had a hold for stays marked as a bound.
			return previousFact{slot: plan.PreviousSlot, offset: plan.CompletionOffsetMillis, hold: plan.PreviousHoldMillis,
				reason: PredecessorInherited, bound: plan.PreviousHoldUnknown}
		}
	}
	// Not closed at this boundary yet: its owner has not come back, or is
	// stuck, and a later observation can still raise the Slots it has not
	// frozen.
	return bounded(PredecessorRecordOpen)
}

func (controller *Controller) seed(state *entry, predecessors map[execution.QueryGroupIdentity]Inspection) (Record, error) {
	next := clone(state.record)
	if state.seeded {
		return next, nil
	}
	if state.corrupt {
		// The stored record did not decode: read as missing, the group
		// relearns its hold, and its next write replaces the bytes. A
		// deadline the record kept is lost with it, a risk overtaken counts.
		controller.statsMu.Lock()
		controller.ownCorrupt++
		controller.statsMu.Unlock()
	}
	previous := state.spec.Previous
	if inherited(next, state.spec) {
		previous = nil
	}
	for _, predecessor := range previous {
		old := predecessors[predecessor.QueryGroup]
		for _, link := range predecessor.Links {
			fact := controller.previousHold(old, predecessor, link)
			if fact.slot == 0 {
				// The Segment it left held no Slot of it: no deadline to
				// keep, so its hold decides nothing and is not counted.
				fact.hold, fact.bound = 0, false
			} else {
				controller.counted(controller.predecessors, fact.reason)
			}
			// The old group's arrival age does not carry over: the new group
			// learns its own, a few windows read early at most.
			mergePlan(&next, PlanRecord{PlanRef: link.PlanRef, ClosedAt: predecessor.ClosedAt, ClosedQueryGroup: predecessor.QueryGroup,
				InheritedQueryGroup: predecessor.QueryGroup, InheritedClosedAt: predecessor.ClosedAt,
				PreviousHoldUnknown: fact.bound, PreviousHoldMillis: fact.hold, PreviousSlot: fact.slot,
				CompletionOffsetMillis: fact.offset})
			if fact.slot > 0 {
				next.Transitions = append(next.Transitions, Transition{Key: link.Key,
					DeadlineMillis: int64(fact.slot)*1000 + fact.hold + fact.offset, Fallback: fact.bound})
			}
		}
	}
	// A group with no record of its own starts where its data has been seen
	// to land, not at the settling wait alone: with the wait at its floor, a
	// group starting at no hold would read every source that lands later
	// than the floor incomplete until its own early reads raised it.
	// A seed that holds nothing seeds nothing: a group with no record is a
	// group at no hold, which is what a missing record tells its successor.
	if (len(state.raw) == 0 || state.corrupt) && next.ArrivalAgeMillis == 0 &&
		state.spec.SeedArrivalAge > state.spec.Delay+state.spec.SettlingWait {
		next.ArrivalAgeMillis, next.ArrivalAgeSeed = state.spec.SeedArrivalAge.Milliseconds(), state.spec.SeedOrigin
		controller.counted(controller.seeds, state.spec.SeedOrigin)
	}
	if next.ArrivalAgeMillis > 0 {
		hold := max(0, next.ArrivalAgeMillis-state.spec.Delay.Milliseconds()-state.spec.SettlingWait.Milliseconds())
		limit := min(state.spec.HoldLimit, controller.options.MaxHold).Milliseconds()
		next.AtLimit, next.LimitMillis = hold > limit, limit
		hold = min(hold, limit)
		if hold != current(next) {
			next.PendingHoldMillis = &hold
			next.EarlierMatches, next.QuietSinceMillis = 0, controller.options.Now().UnixMilli()
			clearFloor(&next)
			next.VerifyCandidateMillis = nil
		}
		// A record holding something with no quiet on it starts its quiet
		// here. A lowering waits for a quiet that has started, and a group
		// at its limit is not tried until one has run its bar (atLimitHeld):
		// with nothing to start one, a group whose data came back in time
		// would keep its hold for good.
		if current(next) > 0 && next.QuietSinceMillis <= 0 {
			next.QuietSinceMillis = controller.options.Now().UnixMilli()
		}
	}
	return next, nil
}

// carries is a record holding anything a successor or a restart needs: a
// hold, an arrival age, a deadline, or a Plan's nonzero closing fact.
func carries(record Record) bool {
	if current(record) != 0 || record.HoldMillis != 0 || record.ArrivalAgeMillis != 0 || len(record.Transitions) != 0 {
		return true
	}
	for _, plan := range record.Plans {
		if plan.PreviousHoldMillis != 0 || plan.PreviousHoldUnknown {
			return true
		}
	}
	return false
}

func mergePlan(record *Record, plan PlanRecord) {
	for index, existing := range record.Plans {
		if existing.PlanRef == plan.PlanRef {
			record.Plans[index] = plan
			return
		}
	}
	record.Plans = append(record.Plans, plan)
}

func prunePlans(record *Record, current []PlanRef, cutoff int64) {
	active := make(map[PlanRef]struct{}, len(current))
	for _, plan := range current {
		active[plan] = struct{}{}
	}
	kept := record.Plans[:0]
	for _, plan := range record.Plans {
		_, configured := active[plan.PlanRef]
		if configured || plan.ClosedAt == 0 || int64(plan.ClosedAt) > cutoff {
			kept = append(kept, plan)
		}
	}
	record.Plans = kept
}

func pruneTransitions(record *Record, readyWithoutHold int64) {
	kept := record.Transitions[:0]
	for _, transition := range record.Transitions {
		if transition.DeadlineMillis > readyWithoutHold {
			kept = append(kept, transition)
		}
	}
	record.Transitions = kept
}

func (controller *Controller) predecessorSnapshot(state *entry) (GroupSpec, map[execution.QueryGroupIdentity]Inspection) {
	state.mu.Lock()
	spec := state.spec
	needed := !state.seeded && !inherited(state.record, spec)
	state.mu.Unlock()
	if !needed {
		return spec, nil
	}
	predecessors := make(map[execution.QueryGroupIdentity]Inspection, len(spec.Previous))
	for _, previous := range spec.Previous {
		predecessors[previous.QueryGroup] = controller.Inspect(previous.QueryGroup)
	}
	return spec, predecessors
}

// SlotReadHold is called before a new Slot is frozen. An unfinished Slot
// bypasses it and keeps its own contract's hold.
func (controller *Controller) SlotReadHold(ctx context.Context, schedule execution.FrozenQueryGroupSchedule, at execution.EvaluationTime, fence execution.OwnerFence) (time.Duration, error) {
	if !schedule.Segment.Contains(at) || len(schedule.DuePlanRefs(at)) == 0 {
		return 0, errors.New("alarmd readhold: invalid Slot schedule")
	}
	qg := schedule.Segment.QueryGroup
	state := controller.group(qg)
	// Snapshot predecessor records before locking this group. Cross-group
	// migrations must not acquire two entry locks in opposite order.
	spec, predecessors := controller.predecessorSnapshot(state)
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := controller.checkSlot(state, schedule, spec); err != nil {
		return 0, err
	}
	owner, err := controller.options.Owner(qg)
	if err != nil {
		return 0, err
	}
	owner.Fence = fence
	frozen, err := controller.slotHoldOf(state, predecessors, schedule, at)
	if err != nil {
		return 0, err
	}
	if frozen.clamp != "" {
		controller.counted(controller.clamped, frozen.clamp)
	}
	next, hold := frozen.next, frozen.hold
	controller.noteQueryDurationLocked(state, &next)
	if !reflect.DeepEqual(next, state.record) {
		// A first zero hold has no durable value to protect. Remember the
		// segment locally without creating a record for the full population
		// -- nor for a group that closed a Segment at zero: a missing record
		// is what tells its successor its hold was zero. A query duration
		// alone does not make one either (query_duration.go).
		if len(state.raw) == 0 && !carries(next) {
			state.record, state.seeded = next, true
			return 0, nil
		}
		if err := controller.persist(ctx, qg, state, next, &owner); err != nil {
			return 0, err
		}
	}
	state.seeded = true
	if frozen.cutFrom > 0 && controller.options.OnLimitCut != nil {
		period := int64(0)
		for _, plan := range schedule.Plans {
			if interval := plan.Spec.EvaluationIntervalSeconds; interval > 0 && (period == 0 || interval < period) {
				period = interval
			}
		}
		controller.options.OnLimitCut(LimitCut{QueryGroup: qg, Slot: at, From: time.Duration(frozen.cutFrom) * time.Millisecond,
			To: time.Duration(hold) * time.Millisecond, Period: time.Duration(period) * time.Second})
	}
	return time.Duration(hold) * time.Millisecond, nil
}

// PeekSlotReadHold is the hold Slot at would be frozen with, asked without
// freezing it: the same check and the same computation as SlotReadHold, on a
// working copy that is thrown away - no record written, no transition
// pruned, no clamp counted. An expired range asks it of its last Slot only
// to compare with its first. A group not seeded yet is refused: seeding
// counts what it finds, and a question counts nothing; the caller then
// freezes the Slot the ordinary way.
func (controller *Controller) PeekSlotReadHold(schedule execution.FrozenQueryGroupSchedule, at execution.EvaluationTime) (time.Duration, error) {
	if !schedule.Segment.Contains(at) || len(schedule.DuePlanRefs(at)) == 0 {
		return 0, errors.New("alarmd readhold: invalid Slot schedule")
	}
	state := controller.group(schedule.Segment.QueryGroup)
	spec, predecessors := controller.predecessorSnapshot(state)
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := controller.checkSlot(state, schedule, spec); err != nil {
		return 0, err
	}
	if !state.seeded {
		return 0, ErrNotSeeded
	}
	frozen, err := controller.slotHoldOf(state, predecessors, schedule, at)
	if err != nil {
		return 0, err
	}
	return time.Duration(frozen.hold) * time.Millisecond, nil
}

// ErrNotSeeded refuses a question about a group whose first Slot has not
// been frozen in this process.
var ErrNotSeeded = errors.New("alarmd readhold: group not seeded")

// checkSlot is what a Slot's schedule and the group's state must be for its
// hold to be computed. Called with the group's lock held.
func (controller *Controller) checkSlot(state *entry, schedule execution.FrozenQueryGroupSchedule, spec GroupSpec) error {
	if !reflect.DeepEqual(state.validated, schedule.Segment) {
		// A frozen Segment's schedule does not change: checked once, not
		// recomputing its digests for every Slot.
		if schedule.Validate() != nil {
			return errors.New("alarmd readhold: invalid Slot schedule")
		}
		state.validated = schedule.Segment
	}
	if err := ready(state); err != nil {
		return err
	}
	if !reflect.DeepEqual(spec, state.spec) {
		return ErrConflict
	}
	return nil
}

// frozenHold is what freezing a Slot gives: its hold, the record freezing it
// leaves, and the clamp source when the hold was cut to the limit.
type frozenHold struct {
	hold  int64
	next  Record
	clamp string
	// cutFrom is the hold of the Slot before when the limit cut this one
	// more than a step below it, and 0 otherwise.
	cutFrom int64
}

// slotHoldOf computes, on a working copy of the group's record, the hold
// Slot at is frozen with and the record freezing it leaves. It writes
// nothing and changes no state but the copy; seeding counts what it finds
// on a group's first Slot, which a caller that must count nothing avoids by
// asking only a seeded group. The one computation both SlotReadHold and
// PeekSlotReadHold run. Called with the group's lock held.
func (controller *Controller) slotHoldOf(state *entry, predecessors map[execution.QueryGroupIdentity]Inspection,
	schedule execution.FrozenQueryGroupSchedule, at execution.EvaluationTime) (frozenHold, error) {
	next, err := controller.seed(state, predecessors)
	if err != nil {
		return frozenHold{}, err
	}
	if schedule.Segment.Start < next.SegmentStart {
		return frozenHold{}, ErrSegmentStale
	}
	if schedule.Segment.Start > next.SegmentStart {
		next.SegmentStart, next.Closed = schedule.Segment.Start, false
	}
	if schedule.Segment.End != nil {
		closeRecord(&next, state.spec, schedule, controller.options.MaxHold.Milliseconds())
	}
	pruneTransitions(&next, int64(at)*1000+state.spec.SettlingWait.Milliseconds())
	hold := holdAt(next, at, state.spec.SettlingWait)
	// A lower hold takes effect a step at a time. The Query Group's Slots
	// run one after another, so a Slot frozen with less hold than the one
	// before it is ready before that one is read, and is first attempted
	// only when it is: a hold come down by more than a few steps at once
	// leaves that Slot already past its deadline and behind by more Slots
	// than a replay may cover, and it is skipped unevaluated - as a hold
	// halved at the limit was. Frozen no more than a step below the Slot
	// before, each Slot is ready when the one before it is, and the hold
	// reaches the lower one Slot by Slot while the lower one stays pending.
	// The limit below still wins: a limit configured under the hold is a
	// bound no Slot is frozen past, and it takes effect at once.
	step := state.spec.Step.Milliseconds()
	stepped := step > 0 && next.SinceSlot > 0 && at > next.SinceSlot
	if stepped && hold < next.HoldMillis-step {
		hold = next.HoldMillis - step
	}
	clamp, cutFrom := "", int64(0)
	if limit := min(state.spec.HoldLimit, controller.options.MaxHold).Milliseconds(); hold > limit {
		// Refusing the Slot refused it on every retry, and the group stopped
		// on it. Frozen at the limit, the old group's last Slot may overtake
		// this one, which is counted, and known apart from a bound standing
		// in for a hold nobody could read.
		clamp = ClampFallback
		known := current(next)
		for _, transition := range next.Transitions {
			if !transition.Fallback {
				known = max(known, transition.DeadlineMillis-int64(at)*1000-state.spec.SettlingWait.Milliseconds())
			}
		}
		if known > limit {
			clamp = ClampKnown
		}
		hold = limit
		if stepped && limit < next.HoldMillis-step {
			cutFrom = next.HoldMillis
		}
	}
	if next.HoldMillis != hold || next.PendingHoldMillis != nil {
		next.PreviousHoldMillis, next.PreviousSinceSlot = next.HoldMillis, next.SinceSlot
		next.HoldMillis, next.SinceSlot = hold, at
		// The base hold can differ from a transition Slot's hold. Keep it
		// pending so the next Slot recomputes its own transition deadline.
		base := current(next)
		if next.PendingHoldMillis == nil {
			base = state.record.HoldMillis
		}
		if base != hold {
			next.PendingHoldMillis = &base
		} else {
			next.PendingHoldMillis = nil
		}
	}
	return frozenHold{hold: hold, next: next, clamp: clamp, cutFrom: cutFrom}, nil
}

// CloseSchedule fixes the old hold and each Plan's last legal Slot before a
// replacement Query Group may seed itself. The old owner must write it.
func (controller *Controller) CloseSchedule(ctx context.Context, schedule execution.FrozenQueryGroupSchedule, fence execution.OwnerFence) error {
	if schedule.Validate() != nil || schedule.Segment.End == nil {
		return errors.New("alarmd readhold: closed schedule required")
	}
	state := controller.group(schedule.Segment.QueryGroup)
	spec, predecessors := controller.predecessorSnapshot(state)
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := ready(state); err != nil {
		return err
	}
	if !reflect.DeepEqual(spec, state.spec) {
		return ErrConflict
	}
	next := clone(state.record)
	if schedule.Segment.Start < next.SegmentStart {
		for _, plan := range schedule.Plans {
			found := false
			for _, existing := range next.Plans {
				found = found || (existing.Key == plan.Key() && existing.ClosedAt == *schedule.Segment.End && existing.ClosedQueryGroup == schedule.Segment.QueryGroup)
			}
			if !found {
				return ErrSegmentStale
			}
		}
		return nil
	}
	next, err := controller.seed(state, predecessors)
	if err != nil {
		return err
	}
	closeRecord(&next, state.spec, schedule, controller.options.MaxHold.Milliseconds())
	if len(state.raw) == 0 && !carries(next) {
		// A successor reads the missing record as a zero hold once this
		// group has frozen its last Slot (previousHold); do not invent a
		// durable zero h.
		state.record, state.seeded = next, true
		return nil
	}
	if reflect.DeepEqual(next, state.record) {
		state.seeded = true
		return nil
	}
	owner, err := controller.options.Owner(schedule.Segment.QueryGroup)
	if err != nil {
		return err
	}
	owner.Fence = fence
	if err := controller.persist(ctx, schedule.Segment.QueryGroup, state, next, &owner); err != nil {
		return err
	}
	state.seeded = true
	return nil
}

func closeRecord(record *Record, spec GroupSpec, schedule execution.FrozenQueryGroupSchedule, holdBound int64) {
	record.SegmentStart, record.Closed = schedule.Segment.Start, true
	refs := append([]PlanRef(nil), spec.Plans...)
	for _, existing := range record.Plans {
		found := false
		for _, ref := range refs {
			found = found || ref == existing.PlanRef
		}
		if !found {
			refs = append(refs, existing.PlanRef)
		}
	}
	for _, plan := range schedule.Plans {
		last := int64(*schedule.Segment.End) - 1
		last -= (last - int64(plan.Spec.Alignment)) % plan.Spec.EvaluationIntervalSeconds
		hasSlot := last >= int64(schedule.Segment.Start) && plan.Spec.IsAligned(execution.EvaluationTime(last))
		previousHold, completionOffset := int64(0), int64(0)
		unknown := false
		if !hasSlot {
			last = 0
		} else if frozen, known := record.HoldAt(execution.EvaluationTime(last)); known {
			previousHold = holdAt(*record, execution.EvaluationTime(last), spec.SettlingWait)
			if last == int64(record.SinceSlot) && record.SinceSlot > 1 {
				// This Slot chose its frozen hold before a later observation
				// could request a raise or a lowering for the next Slot.
				previousHold = frozen
			} else {
				// SinceSlot names only hold changes, not every frozen Slot.
				// If the final Slot may still be ahead, its pending raise and
				// its already-frozen hold must both remain protected.
				previousHold = max(previousHold, frozen, record.HoldMillis)
			}
		} else {
			// The two retained intervals no longer cover this slow Plan's
			// final Slot. The configured bound protects its possible frozen
			// hold without adding a historical hold state machine.
			previousHold, unknown = max(holdAt(*record, execution.EvaluationTime(last), spec.SettlingWait), holdBound), true
		}
		if hasSlot {
			completionOffset = plan.Spec.CompletionOffsetSeconds() * 1000
		}
		for _, ref := range refs {
			if ref.Key != plan.Key() {
				continue
			}
			fixed := false
			for _, existing := range record.Plans {
				fixed = fixed || (existing.PlanRef == ref && existing.ClosedAt == *schedule.Segment.End && existing.ClosedQueryGroup == schedule.Segment.QueryGroup)
			}
			if fixed {
				continue
			}
			closed := PlanRecord{PlanRef: ref,
				ClosedAt:            *schedule.Segment.End,
				ClosedQueryGroup:    schedule.Segment.QueryGroup,
				PreviousHoldUnknown: unknown,
				PreviousHoldMillis:  previousHold, PreviousSlot: execution.EvaluationTime(last),
				CompletionOffsetMillis: completionOffset}
			for _, existing := range record.Plans {
				if existing.PlanRef == ref {
					closed.InheritedQueryGroup, closed.InheritedClosedAt = existing.InheritedQueryGroup, existing.InheritedClosedAt
					if !hasSlot && existing.PreviousSlot > 0 {
						// An empty segment closes no new Slot, but cannot erase
						// an earlier segment's still-protected completion fact.
						closed.PreviousSlot, closed.PreviousHoldMillis = existing.PreviousSlot, existing.PreviousHoldMillis
						closed.CompletionOffsetMillis, closed.PreviousHoldUnknown = existing.CompletionOffsetMillis, existing.PreviousHoldUnknown
					}
					break
				}
			}
			mergePlan(record, closed)
		}
	}
}

func (controller *Controller) Observe(ctx context.Context, evidence Evidence) error {
	if evidence.Contract.Validate() != nil || evidence.ArrivalAge < 0 {
		return errors.New("alarmd readhold: invalid lateness evidence")
	}
	qg := evidence.Contract.Slot.QueryGroup
	state := controller.group(qg)
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := ready(state); err != nil {
		return err
	}
	if evidence.Contract.ScheduleSegmentStart < state.record.SegmentStart || state.record.Closed {
		return nil
	}
	next := clone(state.record)
	if next.SegmentStart == 0 {
		next.SegmentStart = evidence.Contract.ScheduleSegmentStart
	}
	finding := ""
	if evidence.Probation {
		finding = ProbationFindingQuiet
	}
	if evidence.Noise {
		// A series that went after its first read: no age to raise to, but
		// the round was decided on data that changed after it, so no lowering
		// until the group is quiet again.
		next.Noise++
		next.QuietSinceMillis, next.EarlierMatches = controller.options.Now().UnixMilli(), 0
	} else {
		now := controller.options.Now().UnixMilli()
		if next.LastEarlyMillis > 0 && now > next.LastEarlyMillis {
			next.EarlyGaps = withEarlyGap(earlyGapsOf(next), now, now-next.LastEarlyMillis)
			next.MaxEarlyIntervalMillis = longestGap(next.EarlyGaps)
		}
		next.LastEarlyMillis, next.QuietSinceMillis, next.EarlierMatches = now, now, 0
		// The group's own arrival replaces a seed outright: the seed was
		// where reading started, not a measurement to keep the larger of.
		if next.ArrivalAgeSeed != "" {
			next.ArrivalAgeMillis, next.ArrivalAgeSeed = evidence.ArrivalAge.Milliseconds(), ""
		} else {
			next.ArrivalAgeMillis = max(next.ArrivalAgeMillis, evidence.ArrivalAge.Milliseconds())
		}
		next.Rung, next.Buckets = evidence.Rung, append([]int64(nil), evidence.Buckets...)
		for _, ref := range state.spec.Plans {
			plan := PlanRecord{PlanRef: ref}
			for _, existing := range next.Plans {
				if existing.PlanRef == ref {
					plan = existing
					break
				}
			}
			mergePlan(&next, plan)
		}
		limit := min(state.spec.HoldLimit, controller.options.MaxHold).Milliseconds()
		// Only the configured delay and settling wait recur next round: a
		// permit or scheduling delay before the first read cannot pay for its
		// hold, so the arrival age alone decides it.
		target := max(0, evidence.ArrivalAge.Milliseconds()-state.spec.Delay.Milliseconds()-state.spec.SettlingWait.Milliseconds())
		next.AtLimit, next.LimitMillis = target > limit, limit
		// A probation's finding undoes the lowering it watches whatever the
		// round's arrival age: the hold goes back at least to where it came
		// from (below), even when the reread saw the change before the
		// lowered hold - a series that went, or one seen early in its rung.
		undo := evidence.Probation && next.Lowered && next.LoweredFromMillis > current(next)
		if !next.Closed && (min(target, limit) > current(next) || undo) {
			hold := min(target, limit)
			// A raise right after a lowering shows the lowered hold too early:
			// a whole window read there was still arriving. The lowering is
			// undone - the hold goes back at least to where it came from, not
			// only to the one round's arrival, which under an arrival that
			// moves round to round leaves it inside the spread and reads its
			// later rounds early one after another - and the lowered hold is
			// the search's floor, so the next lowering lands above it. Any
			// other raise starts the search afresh.
			raisedFrom := current(next)
			clearFloor(&next)
			// The data came later than the hold: a candidate below it the
			// trials chose is not one to lower to.
			next.VerifyCandidateMillis = nil
			if next.Lowered {
				hold = min(max(hold, next.LoweredFromMillis), limit)
				if raisedFrom < hold {
					setFloor(&next, raisedFrom, now)
					// A verified lowering read early after it: its probe passed
					// by chance, near a line the data is seen on both sides of.
					next.EarlierFloorProbed = next.LoweredVerified
				}
			}
			next.PendingHoldMillis = &hold
			if next.Lowered {
				next.RaisedAfterLowering++
				next.Lowered, next.LoweredVerified = false, false
				if evidence.Probation {
					finding = ProbationFindingUndone
				}
			}
		}
	}
	if err := controller.persist(ctx, qg, state, next, nil); err != nil {
		return err
	}
	if finding != "" {
		controller.counted(controller.probationFindings, finding)
	}
	return nil
}

// EarlierRead takes an earlier-read trial's evidence: one round of the group
// read at the candidate below its hold and compared with the formal read.
// FloorDifferentsRequired different trials in a row move the search's floor
// up to the candidate; EarlierMatchesRequired matching ones in a row, once
// the group has been quiet for its bar, choose the candidate, which a shadow
// probe then reads round by round before the hold moves (Record.
// VerifyCandidateMillis, FloorProbe): a trial reads one round a rest, and a
// candidate inside the spread the data moves over passes three of them by
// chance.
func (controller *Controller) EarlierRead(ctx context.Context, evidence EarlierEvidence) error {
	if evidence.Contract.Validate() != nil || evidence.CandidateHold < 0 {
		return errors.New("alarmd readhold: invalid earlier-read evidence")
	}
	state := controller.group(evidence.Contract.Slot.QueryGroup)
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := ready(state); err != nil {
		return err
	}
	if evidence.Contract.ScheduleSegmentStart < state.record.SegmentStart || state.record.Closed {
		return nil
	}
	hold := current(state.record)
	now := controller.options.Now().UnixMilli()
	candidate, tries := nextCandidate(evidence.Contract.Slot.QueryGroup, state.record, state.spec.Step, now)
	if !evidence.Observed || hold == 0 || !tries || evidence.CandidateHold.Milliseconds() != candidate || evidence.Contract.Slot.EvaluationTime <= state.record.LastEarlierSlot {
		return nil
	}
	next := clone(state.record)
	next.LastEarlierSlot = evidence.Contract.Slot.EvaluationTime
	if !evidence.Equal {
		next.EarlierMatches, next.QuietSinceMillis = 0, now
		// Data still changing at the candidate, in rounds enough in a row:
		// the search moves up to it, and the next candidate is halfway
		// between it and the hold.
		next.EarlierDifferents++
		if next.EarlierDifferents >= FloorDifferentsRequired {
			setFloor(&next, candidate, now)
		}
	} else {
		next.EarlierDifferents = 0
		next.EarlierMatches = min(next.EarlierMatches+1, EarlierMatchesRequired)
		quiet := quietBar(evidence.Contract.Slot.QueryGroup, next, now)
		if next.QuietSinceMillis > 0 && now-next.QuietSinceMillis >= quiet && next.EarlierMatches >= EarlierMatchesRequired {
			// The trials chose the candidate; the hold moves only once a shadow
			// probe has read it clean round by round (FloorProbe, kind verify).
			// A trial reads one round a rest, and a candidate inside the data's
			// spread passes three of them by chance; the probe reads every round.
			next.VerifyCandidateMillis = &candidate
			next.EarlierMatches = 0
		}
	}
	moot := settleVerify(evidence.Contract.Slot.QueryGroup, &next, now)
	if err := controller.persist(ctx, evidence.Contract.Slot.QueryGroup, state, next, nil); err != nil {
		return err
	}
	if moot {
		controller.counted(controller.floorProbes, floorProbeKey(FloorProbeKindVerify, FloorProbeMoot))
	}
	return nil
}

// RenewDue reuses CAS with the same value once a day; untouched records expire
// after seven days. A failed renewal changes no local state and is retryable.
func (controller *Controller) RenewDue(ctx context.Context) error {
	controller.mu.RLock()
	groups := make(map[execution.QueryGroupIdentity]*entry, len(controller.groups))
	for qg, state := range controller.groups {
		groups[qg] = state
	}
	controller.mu.RUnlock()
	var first error
	for qg, state := range groups {
		state.mu.Lock()
		if state.loaded && state.configured && !state.record.Closed && len(state.raw) > 0 && current(state.record) > 0 && controller.options.Now().UnixMilli()-state.record.RenewedAtMillis >= RenewInterval.Milliseconds() {
			// A floor learned a day ago may not hold now that the data comes
			// in sooner: the search starts again from half the hold.
			next := clone(state.record)
			clearFloor(&next)
			if err := controller.persist(ctx, qg, state, next, nil); err != nil && first == nil {
				first = err
			}
		}
		state.mu.Unlock()
	}
	return first
}

// Forget drops a retired Query Group's local state. Its fenced record ages
// out by TTL rather than an unfenced delete by a replacement owner.
func (controller *Controller) Forget(qg execution.QueryGroupIdentity) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	delete(controller.groups, qg)
}
