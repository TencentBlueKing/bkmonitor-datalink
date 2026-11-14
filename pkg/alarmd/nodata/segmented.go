// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A no-data round over a memory read in segments (decision-020 batch 8, 2.7).
//
// The worker keeps the round's present set as its series land (PresentSet),
// reads the memory's header, scans its groups a segment at a time feeding the
// present ones, reads the header again, and then reads the groups it judged
// absent by name, a window at a time, feeding those. The round
// (SegmentedRound) judges each group as it is fed (PlanJudge), folds what the
// group is stored as into the memory digest (K2) and its stored change into
// the delta, and lets the group go: what it holds across the round is the
// present set with a few bits a key, the delta, and the fold.
//
// What a group is stored as depends on the round's present-as-of, which is a
// Plan-level fact - did anything report - known for sure only once every
// group is judged. It is settled before the first group outside the present
// set is fed (Settle), from what can already be known then, and checked
// against the round's own answer when it finishes:
//
//   - A present group the round keeps is kept as seen in this round, so its
//     own judgement is proof the round had data: it is stored against this
//     round's time whenever it comes, before or after the settling.
//   - Once every present group in the memory has been fed - the scan is done
//     - every other present group is one the memory does not hold, and the
//     round keeps it as seen unless it is the whole item or a host of another
//     business known before the read. So whether the round had data is known
//     from the present set, except for a memory written at this very round (a
//     retry) whose groups were seen now; then the stored present-as-of is this
//     round's time already and the two answers are one.
//
// A group stored as seen at this round's time in a record whose present-as-of
// is older would break the second point: it is seen after the Plan last had
// data, which no writer since v2 stores (BuildPlanNoDataMutation refuses it).
// Such a group, kept as it is, fails the round by name (fold) rather than
// being written against the wrong present-as-of.

// The flags a present key carries in its set.
const (
	// presentFlagSeen is the seen bit: the memory held the key and it was fed.
	presentFlagSeen uint8 = 1 << iota
	// presentFlagNormal is the present group's NORMAL, presentFlagClosing
	// that it is the closing one (AbsenceResult.Closing).
	presentFlagNormal
	presentFlagClosing
	// presentFlagStaged is a present group the caller staged ahead of the
	// statement, as seen at this round (PresentSet.MarkStaged), and
	// presentFlagStagedKept one the round kept as exactly that.
	presentFlagStaged
	presentFlagStagedKept
)

// PresentSet is one Plan's present groups in one Slot, kept by the caller as
// the Slot's series land: each group's key with a few bits, and the count of
// series that project onto no group. It is handed to one round
// (PlanSlotInput.Present), which takes it over.
type PresentSet struct {
	aggDimension []string
	keys         map[string]uint8
	dropped      uint64
	claimed      bool
}

// NewPresentSet starts an empty set for a Plan whose groups are its
// aggDimension.
func NewPresentSet(aggDimension []string) *PresentSet {
	return &PresentSet{aggDimension: slices.Clone(aggDimension), keys: map[string]uint8{}}
}

// Add projects one series' dimensions onto its group and keeps the group's
// key. It returns the key when the set did not hold it yet, for the caller to
// account it; a series that projects onto no group is counted (Dropped) and
// returns false. Once a round has the set it takes nothing more: the round
// was handed every series of the Plan, and a series added again after - its
// window landing later - would count a dropped series twice.
func (set *PresentSet) Add(dimensions map[string]string) (string, bool) {
	if set.claimed {
		return "", false
	}
	group, ok := Project(dimensions, set.aggDimension)
	if !ok {
		set.dropped++
		return "", false
	}
	key := group.Key()
	if _, held := set.keys[key]; held {
		return "", false
	}
	set.keys[key] = 0
	return key, true
}

// Len is how many groups the set holds.
func (set *PresentSet) Len() int { return len(set.keys) }

// MarkStaged records that the caller staged a held key ahead of the
// statement as a group seen at this round; the round then leaves it
// out of the delta it holds when it keeps it as that, and says when it did
// not (SegmentedResult.StagedDiffers). Taken only before a round has the set.
func (set *PresentSet) MarkStaged(key string) {
	if flags, held := set.keys[key]; held && !set.claimed {
		set.keys[key] = flags | presentFlagStaged
	}
}

// StagedCount is how many held keys were marked staged.
func (set *PresentSet) StagedCount() uint64 {
	var staged uint64
	for _, flags := range set.keys {
		if flags&presentFlagStaged != 0 {
			staged++
		}
	}
	return staged
}

// claim hands the set to a round, once, refusing one kept for other groups
// than the round's.
func (set *PresentSet) claim(aggDimension []string) error {
	if set.claimed {
		return errors.New("alarmd nodata: a present set was handed to a second round")
	}
	if !slices.Equal(set.aggDimension, aggDimension) {
		return fmt.Errorf("alarmd nodata: a present set kept for %v was handed to a round over %v",
			set.aggDimension, aggDimension)
	}
	set.claimed = true
	return nil
}

// SegmentedRound is one Plan's round fed one group at a time, assembling the
// memory it results in as it goes.
type SegmentedRound struct {
	judge *PlanJudge
	input PlanSlotInput
	// replace says the statement replaces the whole record: the round read
	// none (a missing or a carried memory), so it sets every group it keeps.
	replace bool
	// presentAsOf is what the stored values are written against once settled;
	// fresh says a present group has been kept as seen.
	presentAsOf int64
	settled     bool
	fresh       bool
	memory      execution.NoDataMemoryDigestAccumulator
	set         []execution.NoDataGroupDelta
	del         []string
	held        uint64
	// staged is what the caller staged of a whole record ahead of the
	// statement (UseStaged), stagedKept the staged groups the round kept as
	// staged, and stagedDiffers that one was kept otherwise or not kept.
	staged        *execution.NoDataStaged
	stagedKept    uint64
	stagedDiffers bool
}

// UseStaged hands the round what the caller staged of the whole record
// ahead of the statement, before Finish: the groups marked staged in the
// present set (PresentSet.MarkStaged).
func (round *SegmentedRound) UseStaged(staged *execution.NoDataStaged) { round.staged = staged }

// stagedFlags is the present set's flags of key, and whether it was staged.
func (round *SegmentedRound) stagedFlags(key string) (uint8, bool) {
	if round.judge.absence == nil {
		return 0, false
	}
	flags, present := round.judge.absence.present[key]
	return flags, present && flags&presentFlagStaged != 0
}

// NewSegmentedRound prepares one Plan's round; input.Present is required and
// input.Snapshot.Groups is not read.
func NewSegmentedRound(input PlanSlotInput) (*SegmentedRound, error) {
	if input.Present == nil {
		return nil, errors.New("alarmd nodata: a segmented round needs the present set")
	}
	judge, err := NewPlanJudge(input)
	if err != nil {
		return nil, err
	}
	return &SegmentedRound{judge: judge, input: input,
		replace: input.Snapshot.Representation != execution.NoDataRepresentationPerGroup}, nil
}

// Judges says the round judges any group at all. A round that does not - no
// no-data, an unreadable memory, hosts unresolved - reads no group and its
// Finish is its outcome alone.
func (round *SegmentedRound) Judges() bool { return round.judge.absence != nil }

// Outcome is the round's outcome as prepared; a round that judges may still
// finish without being judged (PlanJudgement.Judged).
func (round *SegmentedRound) Outcome() SlotOutcome { return round.judge.outcome }

// Present says the round's present set holds the key.
func (round *SegmentedRound) Present(key string) bool {
	if round.judge.absence == nil {
		return false
	}
	_, present := round.judge.absence.present[key]
	return present
}

// MarkOutOfBusiness names one group as a host of another business before it
// is fed: the history roster's hosts are the memory's, so they are resolved
// as the scan reads them rather than before it.
func (round *SegmentedRound) MarkOutOfBusiness(key string) {
	if round.judge.absence == nil {
		return
	}
	if round.judge.absence.foreign == nil {
		round.judge.absence.foreign = map[string]struct{}{}
	}
	round.judge.absence.foreign[key] = struct{}{}
}

// Feed judges one stored group. A present group may come at any time and as
// often as the scan returns it; any other comes once (the caller deduplicates
// it) and only after Settle.
func (round *SegmentedRound) Feed(group execution.NoDataGroupMemory) (GroupJudgement, bool, error) {
	if round.judge.absence == nil {
		return GroupJudgement{}, false, nil
	}
	if !round.Present(group.GroupKey) && !round.settled {
		return GroupJudgement{}, false, errors.New(
			"alarmd nodata: a group outside the present set was fed before the round settled its present-as-of")
	}
	if err := execution.ValidateNoDataScannedGroup(group); err != nil {
		return GroupJudgement{}, false, err
	}
	judgement, judged, err := round.judge.feed(group)
	if err != nil || !judged {
		return GroupJudgement{}, false, err
	}
	if err := round.fold(judgement); err != nil {
		return GroupJudgement{}, false, err
	}
	return judgement, true, nil
}

// Settle fixes the present-as-of the stored values are written against; see
// the head of the file. Called once every present group the memory holds has
// been fed, and before any other group.
func (round *SegmentedRound) Settle() {
	if round.settled || round.judge.absence == nil {
		round.settled = true
		return
	}
	round.settled = true
	absence := round.judge.absence
	whole := WholeItemGroup().Key()
	fresh := round.fresh
	if _, wholePresent := absence.present[whole]; wholePresent {
		fresh = true
	}
	for key, flags := range absence.present {
		if fresh {
			break
		}
		if _, foreign := absence.foreign[key]; flags&presentFlagSeen == 0 && key != whole && !foreign {
			fresh = true
		}
	}
	round.presentAsOf = presentAsOf(fresh, round.input.Snapshot.PresentAsOf, round.input.EvaluationTime)
}

// fold takes one judgement into the memory digest and the delta.
func (round *SegmentedRound) fold(judgement GroupJudgement) error {
	stored := judgement.Kept && judgement.Memory != (GroupMemory{})
	at := round.presentAsOf
	if round.Present(judgement.Key) && stored {
		// A present group kept is kept as seen now (judgePresent), which is
		// the proof the round had data.
		if judgement.Memory != (GroupMemory{LastSeen: round.input.EvaluationTime}) {
			return fmt.Errorf("alarmd nodata: present group %q was kept as %+v", judgement.Key, judgement.Memory)
		}
		at, round.fresh = round.input.EvaluationTime, true
	} else if stored && !round.settled {
		return fmt.Errorf("alarmd nodata: group %q was stored before the round settled its present-as-of", judgement.Key)
	} else if stored && judgement.Memory == (GroupMemory{LastSeen: round.input.EvaluationTime}) &&
		round.presentAsOf != round.input.EvaluationTime {
		return fmt.Errorf("alarmd nodata: group %q is stored as seen at %d, after the record's present-as-of %d",
			judgement.Key, round.input.EvaluationTime, round.input.Snapshot.PresentAsOf)
	}
	set, del := execution.NoDataStoredChange(judgement.Key, storedMemory(judgement.Key, judgement.Loaded),
		judgement.InMemory, round.input.Snapshot.PresentAsOf, storedMemory(judgement.Key, judgement.Memory),
		stored, at, round.replace)
	if stored {
		if err := round.memory.Add(storedMemory(judgement.Key, judgement.Memory)); err != nil {
			return err
		}
	}
	if flags, staged := round.stagedFlags(judgement.Key); staged {
		// Staged as seen at this round: kept as that, it is in the staging
		// key already and not held here; kept otherwise, the stage is not
		// this round's record (Finish).
		if round.replace && set != nil && set.Absent == nil && at == round.input.EvaluationTime {
			round.judge.absence.present[judgement.Key] = flags | presentFlagStagedKept
			round.stagedKept++
			set = nil
		} else {
			round.stagedDiffers = true
		}
	}
	if set != nil {
		round.set = append(round.set, *set)
		round.held += SegmentedDeltaBytes(*set)
	}
	if del {
		round.del = append(round.del, judgement.Key)
		round.held += segmentedDeleteBytes + uint64(len(judgement.Key))
	}
	return nil
}

func storedMemory(key string, memory GroupMemory) execution.NoDataGroupMemory {
	return execution.NoDataGroupMemory{
		GroupKey: key, LastSeen: memory.LastSeen, FirstAbsent: memory.FirstAbsent, SuppressedAt: memory.SuppressedAt,
	}
}

// The bytes one entry of the delta holds: the entry, its key, and an absence's
// three timestamps.
const (
	segmentedDeltaEntryBytes   = 24
	segmentedDeltaAbsenceBytes = 24
	segmentedDeleteBytes       = 16
)

// SegmentedDeltaBytes is what one entry of a round's delta holds.
func SegmentedDeltaBytes(delta execution.NoDataGroupDelta) uint64 {
	bytes := uint64(segmentedDeltaEntryBytes + len(delta.GroupKey))
	if delta.Absent != nil {
		bytes += segmentedDeltaAbsenceBytes
	}
	return bytes
}

// HeldBytes is what the round's delta holds so far.
func (round *SegmentedRound) HeldBytes() uint64 { return round.held }

// SegmentedResult is a finished round.
type SegmentedResult struct {
	Outcome SlotOutcome
	Facts   AbsenceFacts
	// Judged is PlanJudgement.Judged: the round's verdicts are said and its
	// memory written. A round not judged says nothing, and the caller sends
	// nothing of it.
	Judged bool
	// Rest is the judgements Finish made that the caller has to say: groups
	// outside the present set no stored entry named - an expected group never
	// remembered, the whole item - with a verdict to say, in key order.
	Rest []GroupJudgement
	// PresentKeys is the present groups with a verdict to say, in key order
	// (SynthesizePresent).
	PresentKeys []string
	// Mutation is nil when nothing moved or the round is not judged.
	Mutation *execution.PlanNoDataMutation
	// StagedDiffers says why the groups staged ahead of the statement are
	// not this round's to put in place (StagedDiffersReasons), empty when
	// they are or none were: the statement then carries every group, and the
	// caller discards what it staged. A round not judged, or with nothing to
	// write, leaves its stage to discard as well.
	StagedDiffers string
}

// Why a round's stage is not its record, closed: a staged group the round
// kept otherwise or not at all; the stage's groups not the ones the round
// marked and kept (a segment staged and not marked, or the reverse); the
// round's present-as-of not the one they were staged against.
const (
	StagedDiffersKeptOtherwise = "kept_otherwise"
	StagedDiffersCount         = "count_mismatch"
	StagedDiffersPresentAsOf   = "present_as_of"
)

// StagedDiffersReasons is every reason.
var StagedDiffersReasons = [3]string{StagedDiffersKeptOtherwise, StagedDiffersCount, StagedDiffersPresentAsOf}

// Finish judges the groups no stored entry named, settles the Plan and builds
// the statement of the memory it results in.
func (round *SegmentedRound) Finish(version execution.ApplyVersion) (SegmentedResult, error) {
	round.Settle()
	judgement, rest, err := round.judge.finish()
	if err != nil {
		return SegmentedResult{}, err
	}
	result := SegmentedResult{Outcome: judgement.Outcome, Facts: judgement.Facts, Judged: judgement.Judged}
	for _, entry := range rest {
		if err := round.fold(entry); err != nil {
			return SegmentedResult{}, err
		}
		if !round.Present(entry.Key) && entry.Verdict != "" && entry.Verdict != VerdictUnavailable {
			result.Rest = append(result.Rest, entry)
		}
	}
	if !judgement.Judged {
		return SegmentedResult{Outcome: judgement.Outcome, Facts: judgement.Facts}, nil
	}
	if judgement.PresentAsOf != round.presentAsOf {
		// Settled from the present set and found otherwise by the round:
		// only a record holding a group seen after its own present-as-of, or
		// one written ahead of this round, gets here, and either is a record
		// this round cannot write against.
		return SegmentedResult{}, fmt.Errorf(
			"alarmd nodata: the round settled its present-as-of at %d and finished at %d",
			round.presentAsOf, judgement.PresentAsOf)
	}
	sort.Slice(result.Rest, func(left, right int) bool { return result.Rest[left].Key < result.Rest[right].Key })
	for key, flags := range round.judge.absence.present {
		if flags&presentFlagNormal != 0 {
			result.PresentKeys = append(result.PresentKeys, key)
		}
	}
	sort.Strings(result.PresentKeys)
	if judgement.Unchanged {
		return result, nil
	}
	input := round.input
	staged := round.staged
	differs := ""
	switch {
	case staged == nil:
	case round.stagedDiffers || !round.replace:
		differs = StagedDiffersKeptOtherwise
	case round.stagedKept != staged.Groups() || round.stagedKept != input.Present.StagedCount():
		differs = StagedDiffersCount
	case staged.PresentAsOf != judgement.PresentAsOf:
		differs = StagedDiffersPresentAsOf
	}
	if differs != "" {
		// Not this round's record: every group goes with the statement, the
		// ones kept as staged back in the delta.
		for key, flags := range round.judge.absence.present {
			if flags&presentFlagStagedKept != 0 {
				round.set = append(round.set, execution.NoDataGroupDelta{GroupKey: key})
			}
		}
		staged, result.StagedDiffers = nil, differs
	}
	mutation, err := execution.BuildPlanNoDataSegmentedMutation(execution.PlanNoDataSegmentedUpdate{
		Identity: input.Identity, GroupLimit: input.MaxGroups,
		DerivedFrom: input.Snapshot.Representation, ExpectedMarkerRevision: input.Snapshot.MarkerRevision,
		LoadedApplyVersion: input.Snapshot.PersistedApplyVersion, ApplyVersion: version,
		ScheduleRevision: input.ScheduleRevision, RosterVersion: judgement.RosterVersion,
		PresentAsOf: judgement.PresentAsOf, TrackingExhaustedAt: judgement.TrackingExhaustedAt,
		LoadedPresentAsOf: input.Snapshot.PresentAsOf, LoadedTrackingExhaustedAt: input.Snapshot.TrackingExhaustedAt,
		Set: round.set, Del: round.del, Memory: &round.memory, Staged: staged,
	})
	if err != nil {
		return SegmentedResult{}, err
	}
	round.set, round.del = nil, nil
	result.Mutation = &mutation
	return result, nil
}

// Synthesize turns judgements with a verdict to say - fed groups outside the
// present set, or Finish's Rest - into the series the ordinary evaluation
// reads, in the order given.
func (round *SegmentedRound) Synthesize(judgements []GroupJudgement) ([]SyntheticSeries, error) {
	return syntheticSeriesOf(round.input.EvaluationTime, round.input.PeriodSeconds, judgements, round.rosterGroups())
}

// SynthesizePresent turns present keys with a verdict (SegmentedResult.
// PresentKeys) into their series.
func (round *SegmentedRound) SynthesizePresent(keys []string) ([]SyntheticSeries, error) {
	judgements := make([]GroupJudgement, 0, len(keys))
	for _, key := range keys {
		flags := round.judge.absence.present[key]
		if flags&presentFlagNormal == 0 {
			return nil, fmt.Errorf("alarmd nodata: present group %q has no verdict to say", key)
		}
		judgements = append(judgements, GroupJudgement{Key: key, Verdict: VerdictNormal, Closing: flags&presentFlagClosing != 0})
	}
	return round.Synthesize(judgements)
}

func (round *SegmentedRound) rosterGroups() map[string]Group {
	if round.judge.absence == nil {
		return nil
	}
	return round.judge.absence.roster.Groups
}
