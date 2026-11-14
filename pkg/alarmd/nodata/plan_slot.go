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
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// PlanSlotInput is one Plan's whole no-data round: what the Slot saw, what the
// store held, and the version facts a write would need.
type PlanSlotInput struct {
	// NoData and Scope are the two things the decision reads off the Plan. The
	// worker holds the compiled form rather than the frozen one, and passing a
	// half-filled Plan across so this could read two fields off it would put an
	// object in the code that looks like a Plan and is not one.
	NoData *contract.NoDataConfigV1
	Scope  *contract.TargetScopeV2
	// Plan is the target's second frozen form, and TargetResolution what the
	// worker resolved it to this Slot; both nil for a Plan without one.
	Plan             *contract.TargetPlanV1
	TargetResolution *TargetResolution
	// AnswerTruncated is SlotInput.AnswerTruncated, forwarded.
	AnswerTruncated  bool
	Identity         execution.PlanNoDataIdentity
	Snapshot         execution.NoDataMemorySnapshot
	ApplyVersion     execution.ApplyVersion
	ScheduleRevision execution.PlanScheduleRevision
	EvaluationTime   int64
	PeriodSeconds    int64
	Completeness     execution.Completeness
	Series           []map[string]string
	// Present is the round's present set kept by the caller as its series
	// landed (PresentSet), in place of Series, for a round judged one segment
	// at a time (SegmentedRound). The round takes it over: its seen bits and
	// the present groups' verdicts are written into it.
	Present       *PresentSet
	KnownHosts    map[string]struct{}
	HostsResolved bool
	OutOfBusiness map[string]struct{}
	// TrackingHorizonSeconds is the limited tracking horizon in force for this
	// Plan, in seconds, zero meaning the deployment configured none and
	// tracking is unlimited.
	//
	// It arrives as its own field rather than being read off NoData here
	// because the horizon is resolved from several layers - deployment default
	// and the item's own override - and that resolution belongs in exactly one
	// place. Compilation is that place: it settles the layers once and freezes
	// the effective number into the Plan, and the caller forwards it. Reading
	// the field off the Plan here as well would make this a second place that
	// decides what the effective horizon is, and the two would disagree the
	// moment either changed.
	TrackingHorizonSeconds int64
	// MaxGroups is the Plan's own bound on its memory's groups, frozen by
	// compilation from the item's max_groups, zero for the deployment's.
	// Forwarded to the store, which is where the bound is checked.
	MaxGroups uint32
	// ManualCloses is SlotInput.ManualCloses, forwarded.
	ManualCloses *ManualCloses
}

// PlanSlotResult is everything the worker needs from one Plan's no-data round.
//
// Mutation is nil when the memory must not be touched, which is three different
// situations wearing one shape: the round judged nothing, the memory came back
// unreadable, or the memory is already exactly what this round would write.
// Outcome is what tells them apart, and it is the reason Outcome exists.
type PlanSlotResult struct {
	Outcome  SlotOutcome
	Series   []SyntheticSeries
	Mutation *execution.PlanNoDataMutation
	Facts    AbsenceFacts
}

// EvaluatePlanSlot turns one Plan's Slot into its no-data decision, the series
// that decision becomes, and the memory to store.
//
// A store that could not be read is an error rather than an outcome. It is a
// dependency failure, the same kind the gap load already raises, and the Slot
// has machinery for those. That is exactly why an unreadable schema is not one:
// a record written by a newer build is not a failure of anything, it is a state
// the system is briefly in, and routing it through the error path would take
// the Plan's threshold detection down with it.
//
// It is the round judged one group at a time (PlanJudge) with every stored
// group fed in the order the snapshot holds them.
func EvaluatePlanSlot(input PlanSlotInput) (PlanSlotResult, error) {
	judge, err := NewPlanJudge(input)
	if err != nil {
		return PlanSlotResult{}, err
	}
	for _, group := range input.Snapshot.Groups {
		if err := judge.Feed(group); err != nil {
			return PlanSlotResult{}, err
		}
	}
	return judge.planSlotResult(input.Snapshot.Groups)
}

// PlanJudge is one Plan's no-data round judged one group at a time: prepared
// from everything known before the stored memory is read, fed the stored
// groups in any order - a group fed twice is judged once - and finished with
// the Plan-level outcome. See judge.go for why the order does not matter.
type PlanJudge struct {
	input   PlanSlotInput
	outcome SlotOutcome
	// absence is nil for a round that judges nothing, whose outcome is then
	// already the whole answer.
	absence  *absenceJudge
	delta    []GroupJudgement
	verdicts []GroupJudgement
	// changed counts the groups whose stored value moved, which is what
	// "nothing moved" reads; delta holds them only for a round that keeps
	// them (not streaming).
	changed int
	// streaming is a round over a caller's present set (PlanSlotInput.
	// Present): it keeps no delta and no verdict, hands each judgement to the
	// caller as it is made (feed, finish), and keeps a present group's verdict
	// on the set.
	streaming bool
}

// PlanJudgement is a finished PlanJudge.
type PlanJudgement struct {
	Outcome SlotOutcome
	Facts   AbsenceFacts
	// Judged says the round's verdicts are to be said and its memory carried
	// forward. A round that judged nothing writes nothing: the memory it would
	// write is the memory it started with, and writing it back would burn a
	// state mutation to store what is already there - but more importantly,
	// the absence clocks must not move on evidence the round did not have.
	//
	// A partial round that judged groups present (A1) is judged: their
	// NORMAL and their last-seen are evidence it had. Its unavailable groups
	// produce no series and keep their memory as loaded.
	//
	// The fields below are set only on a judged round.
	Judged bool
	// Unchanged says the memory the round produced is the memory it loaded,
	// so nothing is to be written. See planSlotResult.
	Unchanged           bool
	RosterVersion       string
	PresentAsOf         int64
	TrackingExhaustedAt int64
	// Delta is every group whose stored value moved, in key order: groups the
	// round changed or forgot, and groups seen for the first time.
	Delta []GroupJudgement
	// Verdicts is every group the round says something about, in key order -
	// the series to synthesize. An UNAVAILABLE group is not here: it produces
	// no series.
	Verdicts []GroupJudgement
}

// NewPlanJudge prepares one Plan's round. input.Snapshot.Groups is not read:
// the caller feeds the stored groups.
func NewPlanJudge(input PlanSlotInput) (*PlanJudge, error) {
	if input.NoData == nil {
		return &PlanJudge{outcome: OutcomeNone}, nil
	}
	switch input.Snapshot.Status {
	case execution.NoDataMemoryUnreadable:
		return &PlanJudge{outcome: OutcomeSkippedMemoryUnreadable}, nil
	case execution.NoDataMemoryMissing, execution.NoDataMemoryFound:
	default:
		return nil, fmt.Errorf(
			"alarmd nodata: no-data memory for plan %s was not read: %s (%s)",
			input.Identity.Plan.StrategyID, input.Snapshot.Status, input.Snapshot.ReasonCode)
	}
	absence, outcome, err := newSlotJudgeWith(SlotInput{
		Plan:             &contract.EvaluationPlanV2{NoData: input.NoData, TargetScope: input.Scope, TargetPlan: input.Plan},
		EvaluationTime:   input.EvaluationTime,
		PeriodSeconds:    input.PeriodSeconds,
		Completeness:     input.Completeness,
		Series:           input.Series,
		KnownHosts:       input.KnownHosts,
		HostsResolved:    input.HostsResolved,
		OutOfBusiness:    input.OutOfBusiness,
		TargetResolution: input.TargetResolution,
		AnswerTruncated:  input.AnswerTruncated,
		// The Plan-level fact comes out of the record, never out of the groups:
		// the round it describes is one where there are no groups left to
		// derive it from.
		TrackingHorizonSeconds: input.TrackingHorizonSeconds,
		TrackingExhaustedAt:    input.Snapshot.TrackingExhaustedAt,
		ManualCloses:           input.ManualCloses,
	}, input.Present)
	if err != nil {
		return nil, err
	}
	input.Snapshot.Groups = nil
	judge := &PlanJudge{input: input, outcome: outcome, absence: absence, streaming: input.Present != nil}
	if absence != nil && judge.streaming {
		absence.callerDedupes = true
	}
	return judge, nil
}

// Feed judges one stored group.
func (judge *PlanJudge) Feed(group execution.NoDataGroupMemory) error {
	_, _, err := judge.feed(group)
	return err
}

// feed judges one stored group and returns the judgement, judged false for a
// group ignored as a repeat or held to the end (the whole item).
func (judge *PlanJudge) feed(group execution.NoDataGroupMemory) (GroupJudgement, bool, error) {
	if judge.absence == nil {
		return GroupJudgement{}, false, nil
	}
	judgement, judged, err := judge.absence.Feed(group.GroupKey, GroupMemory{
		LastSeen: group.LastSeen, FirstAbsent: group.FirstAbsent, SuppressedAt: group.SuppressedAt,
	})
	if err != nil || !judged {
		return GroupJudgement{}, false, err
	}
	if err := judge.record(judgement); err != nil {
		return GroupJudgement{}, false, err
	}
	return judgement, true, nil
}

func (judge *PlanJudge) record(judgement GroupJudgement) error {
	if judgement.Changed {
		judge.changed++
	}
	said := judgement.Verdict != "" && judgement.Verdict != VerdictUnavailable
	if !judge.streaming {
		if judgement.Changed {
			judge.delta = append(judge.delta, judgement)
		}
		if said {
			judge.verdicts = append(judge.verdicts, judgement)
		}
		return nil
	}
	flags, present := judge.absence.present[judgement.Key]
	if !present || !said {
		return nil
	}
	// A present group is judged present: its verdict is the NORMAL of what
	// arrived, or the closing NORMAL of a host gone to another business
	// (closeDropped), and never an absence.
	if judgement.Verdict != VerdictNormal {
		return fmt.Errorf("alarmd nodata: present group %q was judged %s", judgement.Key, judgement.Verdict)
	}
	flags |= presentFlagNormal
	if judgement.Closing {
		flags |= presentFlagClosing
	}
	judge.absence.present[judgement.Key] = flags
	return nil
}

// Finish judges the groups no stored entry named and settles the Plan.
func (judge *PlanJudge) Finish() (PlanJudgement, error) {
	judgement, _, err := judge.finish()
	return judgement, err
}

// finish is Finish, returning as well the judgements of the groups no stored
// entry named, for a streaming round's caller.
func (judge *PlanJudge) finish() (PlanJudgement, []GroupJudgement, error) {
	if judge.absence == nil {
		return PlanJudgement{Outcome: judge.outcome}, nil, nil
	}
	summary, rest, err := judge.absence.Finish()
	if err != nil {
		return PlanJudgement{}, nil, err
	}
	for _, judgement := range rest {
		if err := judge.record(judgement); err != nil {
			return PlanJudgement{}, nil, err
		}
	}
	judgement := PlanJudgement{Outcome: judge.outcome, Facts: summary.Facts}
	partial := judge.outcome == OutcomeSkippedQueryNotFull || judge.outcome == OutcomeSkippedAnswerTruncated ||
		judge.outcome == OutcomeSkippedTargetSelectorUnavailable || judge.outcome == OutcomeSkippedTargetMembersDropped
	// judgedPresent: a last-seen the round wrote now, or the whole item
	// present. With recovery enabled each such group also carries its
	// NORMAL; with it disabled the last-seen is still news.
	judgedPresent := summary.FreshPresent || summary.WholeItemPresent
	if judge.outcome != OutcomeEvaluated && (!partial || !judgedPresent) {
		return judgement, rest, nil
	}
	judgement.Judged = true
	if !judge.streaming {
		sort.Slice(judge.delta, func(left, right int) bool { return judge.delta[left].Key < judge.delta[right].Key })
		sort.Slice(judge.verdicts, func(left, right int) bool {
			return judge.verdicts[left].Key < judge.verdicts[right].Key
		})
		judgement.Delta, judgement.Verdicts = judge.delta, judge.verdicts
	}

	snapshot := judge.input.Snapshot
	judgement.RosterVersion = summary.Roster.Version
	if partial && snapshot.RosterVersion != "" {
		// A partial round decided nothing about the expected set, so it does
		// not restate which derivation the record was judged against - the
		// stored string goes forward as it is, a v1 value as well as a v2 one
		// (rosterVersionDomain).
		judgement.RosterVersion = snapshot.RosterVersion
	}
	judgement.PresentAsOf = presentAsOf(judgedPresent, snapshot.PresentAsOf, judge.input.EvaluationTime)
	judgement.TrackingExhaustedAt = summary.TrackingExhaustedAt
	// Nothing moved. Sending the mutation anyway would be correct and
	// idempotent - the digest would match and the store would say so - but it
	// costs a round trip per Plan per Slot for a write that changes nothing,
	// and the Slot's mutation budget is the scarce thing here.
	//
	// The present-as-of is part of "nothing moved" because it is part of the
	// memory: it is the last-seen time of every group stored without an
	// absence, so a round where it advanced changed what those groups say even
	// though their own entries are unchanged.
	//
	// The tracking fact is part of it for the same reason and one more: it is
	// the only part of the memory that can move while the groups do not. The
	// round that exhausts a roster empties the memory, so the groups move with
	// it - but the round that clears the fact need not, and a comparison of
	// three that left it out would read "nothing moved" on a round whose whole
	// content was the fact, drop the write, and leave the store stating an
	// exhaustion the evaluation had already ended.
	judgement.Unchanged = judge.changed == 0 && snapshot.RosterVersion == judgement.RosterVersion &&
		judgement.PresentAsOf == snapshot.PresentAsOf && judgement.TrackingExhaustedAt == snapshot.TrackingExhaustedAt
	return judgement, rest, nil
}

// planSlotResult finishes the round into what EvaluatePlanSlot returns. The
// memory to store is the loaded record with the delta applied, which needs
// the loaded groups; the caller that fed them holds them.
func (judge *PlanJudge) planSlotResult(loaded []execution.NoDataGroupMemory) (PlanSlotResult, error) {
	judgement, err := judge.Finish()
	if err != nil {
		return PlanSlotResult{}, err
	}
	slot := PlanSlotResult{Outcome: judgement.Outcome, Facts: judgement.Facts}
	if !judgement.Judged {
		return slot, nil
	}
	input := judge.input
	slot.Series, err = syntheticSeriesOf(input.EvaluationTime, input.PeriodSeconds, judgement.Verdicts,
		judge.absence.roster.Groups)
	if err != nil {
		return PlanSlotResult{}, err
	}
	if judgement.Unchanged {
		return slot, nil
	}
	memory := make(map[string]GroupMemory, len(loaded)+len(judgement.Delta))
	for _, group := range loaded {
		memory[group.GroupKey] = GroupMemory{
			LastSeen: group.LastSeen, FirstAbsent: group.FirstAbsent, SuppressedAt: group.SuppressedAt,
		}
	}
	for _, change := range judgement.Delta {
		if change.Kept {
			memory[change.Key] = change.Memory
		} else {
			delete(memory, change.Key)
		}
	}
	mutation, err := execution.BuildPlanNoDataMutation(execution.PlanNoDataMemoryUpdate{
		Identity:   input.Identity,
		GroupLimit: input.MaxGroups,
		// Which record the revision and the loaded groups below came out of:
		// a statement derived from no record replaces the whole record rather
		// than claiming to be a delta against one.
		DerivedFrom:            input.Snapshot.Representation,
		LoadedApplyVersion:     input.Snapshot.PersistedApplyVersion,
		ExpectedMarkerRevision: input.Snapshot.MarkerRevision,
		ApplyVersion:           input.ApplyVersion,
		ScheduleRevision:       input.ScheduleRevision,
		RosterVersion:          judgement.RosterVersion,
		PresentAsOf:            judgement.PresentAsOf,
		Memory:                 storedGroups(memory),
		Loaded:                 loaded,
		LoadedPresentAsOf:      input.Snapshot.PresentAsOf,
		// Both halves, for the same reason PresentAsOf has both: the delta has
		// to know whether the fact moved, and the contract's two invariants are
		// stated against what was loaded rather than against this round alone.
		TrackingExhaustedAt:       judgement.TrackingExhaustedAt,
		LoadedTrackingExhaustedAt: input.Snapshot.TrackingExhaustedAt,
	})
	if err != nil {
		return PlanSlotResult{}, err
	}
	slot.Mutation = &mutation
	return slot, nil
}

// presentAsOf is the round this Plan last had data in.
//
// It is read off the memory the round produced rather than off the round's
// inputs (judgedPresent is absenceSummary.FreshPresent): every group the
// evaluation saw is written with this round's time and no absence, by the two
// places that record presence, so a memory holding one of those is a round
// that had data. Taking it from the inputs instead would have to decide what
// "had data" means for a series that arrived for a group the roster had
// already dropped, and the memory has already decided that.
//
// The one group the memory cannot answer for is the whole-item group. It is
// never written - it is not a series, and a LastSeen would carry it into a
// history roster as one - so a round whose only data arrived under it produces
// an empty memory and would read as a round with no data at all. For that
// group the round's own present count is the only witness there is, and it has
// to be used: the contract layer refuses a memory that cleared the exhaustion
// mark without the present-as-of advancing, so a Plan exhausted as history
// whose data comes back under the whole-item group could never write again.
//
// With nothing seen, the stored value is carried forward. It must never go
// backwards: it is the last-seen time of every group stored as present, so
// moving it back would move those groups' clocks back with it.
func presentAsOf(judgedPresent bool, stored int64, evaluationTime int64) int64 {
	if judgedPresent {
		return evaluationTime
	}
	return stored
}

// storedGroups is the evaluation's memory as the record holds it: canonical
// order, and nothing that remembers nothing.
func storedGroups(memory map[string]GroupMemory) []execution.NoDataGroupMemory {
	groups := make([]execution.NoDataGroupMemory, 0, len(memory))
	for key, entry := range memory {
		if entry.LastSeen == 0 && entry.FirstAbsent == 0 && entry.SuppressedAt == 0 {
			continue
		}
		groups = append(groups, execution.NoDataGroupMemory{
			GroupKey: key, LastSeen: entry.LastSeen, FirstAbsent: entry.FirstAbsent,
			SuppressedAt: entry.SuppressedAt,
		})
	}
	sort.Slice(groups, func(left, right int) bool { return groups[left].GroupKey < groups[right].GroupKey })
	return groups
}
