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
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The absence evaluation, one group at a time (decision-020 batch 8, 2.7).
//
// A round's verdict about one group depends on that group's own memory, on
// whether it reported, whether its host is in another business, whether the
// roster expects it, and on a handful of Plan-level facts: which path the round
// takes (A1, A2/A3, the confirmed-empty target plan, or the ordinary one), the
// horizon, recovery, and whether anything at all reported. None of it depends
// on another group's memory. So the judgement is prepared from what is known
// before the stored memory is read, fed the memory one group at a time in
// whatever order the store returns it, and finished with the Plan-level counts
// - which is what lets the memory be scanned in segments rather than read
// whole.
//
// One fact is the exception, and it is held rather than guessed: whether a
// history roster is empty. A history roster is the memory's groups with a
// last-seen time, so it is known only once the whole memory has been read. The
// only group whose verdict depends on it is the whole-item group - an empty
// roster makes the item speak about itself (A2), a non-empty one closes a
// whole-item absence the roster now accounts for (A8) - and every other group
// is judged the same either way. The whole-item group is therefore held and
// judged when the round finishes, and nothing else waits.
//
// The store may return one field twice in a scan (HSCAN guarantees each field
// at least once, not exactly once), so every group is judged once and a second
// entry under the same key is ignored: a present group through the seen bit on
// the present set, every other group through the set of keys already judged.
//
// The history roster's size and version are folded from the same
// deduplication (rosterVersionFold): a key the round judges is a key the fold
// counts, once, and no key is held for the roster. The roster's groups are not
// rebuilt either - a history roster's group is its key parsed back, which is
// what every reader of a key outside a stated roster already does - so the
// judgement keeps no history key beyond the one deduplication set.

// GroupJudgement is what one round says about one group.
type GroupJudgement struct {
	Key string
	// Verdict is empty when the round says nothing about the group.
	Verdict Verdict
	// Closing is AbsenceResult.Closing for this group.
	Closing bool
	// Memory is what the group remembers after the round, and Kept whether it
	// is remembered at all. A group not kept has a zero Memory.
	Memory GroupMemory
	Kept   bool
	// Changed says what the record stores for this group moved against what
	// was loaded for it: it was stored and is no longer, it is stored now and
	// was not, or its timestamps differ. It is what the round's delta is made
	// of.
	Changed bool
	// Loaded is what the record held for the group as it was fed, before any
	// manual close, and InMemory whether it held anything: what a round that
	// sees one group at a time takes the group's stored change against
	// (SegmentedRound).
	Loaded   GroupMemory
	InMemory bool
}

// judgeMode is which of the evaluation's paths a round takes. Each is a
// Plan-level fact settled before any group is judged, except the history
// roster's emptiness, which only the whole-item group reads (see above).
type judgeMode uint8

const (
	// modeExpected is a FULL round against a non-empty expected set.
	modeExpected judgeMode = iota
	// modePartial is A1: not FULL, possibly cut, or against an expected set
	// that did not resolve in full.
	modePartial
	// modeNothingExpected is A2 and A3: an empty expected set that is not a
	// target plan's.
	modeNothingExpected
	// modeEmptyTargetPlan is a target plan that resolved, completely, to no
	// member.
	modeEmptyTargetPlan
)

// absenceJudge is the evaluation for one round, fed one group at a time.
type absenceJudge struct {
	evaluationTime    int64
	horizonSeconds    int64
	loadedExhaustedAt int64
	recoveryDisabled  bool
	mode              judgeMode
	// roster is the expected set when it is known before the memory is read:
	// a target, the whole item, or one a caller of Evaluate stated. It is nil
	// with deriveHistory, where the expected set is the memory's groups with a
	// last-seen time and is learned one group at a time.
	roster        Roster
	deriveHistory bool
	// present is the present set, each key with its flags: the seen bit
	// (presentFlagSeen), which is the deduplication for present groups, and
	// for a round whose caller keeps the set (PresentSet) the verdict the
	// round gave the group, so the caller says it without the judgement
	// holding one per present group.
	present map[string]uint8
	foreign map[string]struct{}
	closes  *ManualCloses

	// judged is every other key a group was judged under; with the seen bit
	// it is the one deduplication. A round whose caller deduplicates what it
	// feeds (callerDedupes, a segmented round, which keeps the names it
	// judged absent itself) keeps here only the keys its roster names, which
	// is what Finish reads, and not every name a second time.
	judged        map[string]struct{}
	callerDedupes bool
	// history is the derived history roster folded into its size and
	// version, fed through the deduplication above. It holds no key.
	history  rosterVersionFold
	whole    GroupMemory
	wholeFed bool

	facts        AbsenceFacts
	kept         uint64
	freshPresent bool
	finished     bool
}

// absenceSummary is the Plan-level half of a finished round.
type absenceSummary struct {
	Facts               AbsenceFacts
	Roster              Roster
	TrackingExhaustedAt int64
	WholeItemPresent    bool
	// FreshPresent says some group is remembered as seen in this round with
	// no absence: the round had data the memory records.
	FreshPresent bool
}

// newAbsenceJudge prepares one round's judgement. input.Memory is not read;
// the caller feeds it. With deriveHistory the expected set is the history
// roster, derived from the groups as they are fed, and input.Roster names
// only its source.
func newAbsenceJudge(input AbsenceInput, deriveHistory bool, closes *ManualCloses) *absenceJudge {
	return newAbsenceJudgeOver(input, deriveHistory, closes, nil)
}

// newAbsenceJudgeOver is newAbsenceJudge over a present set the caller keeps
// (PresentSet.keys), in place of input.Present; nil builds it from
// input.Present.
func newAbsenceJudgeOver(
	input AbsenceInput, deriveHistory bool, closes *ManualCloses, present map[string]uint8,
) *absenceJudge {
	if present == nil {
		present = make(map[string]uint8, len(input.Present))
		for key := range input.Present {
			present[key] = 0
		}
	}
	judge := &absenceJudge{
		evaluationTime:    input.EvaluationTime,
		horizonSeconds:    input.TrackingHorizonSeconds,
		loadedExhaustedAt: input.TrackingExhaustedAt,
		recoveryDisabled:  input.RecoveryDisabled,
		roster:            input.Roster,
		deriveHistory:     deriveHistory,
		present:           present,
		foreign:           input.OutOfBusiness,
		closes:            closes,
		judged:            map[string]struct{}{},
		facts: AbsenceFacts{
			Present: uint64(len(present)), Dropped: input.Dropped,
			RosterSource: input.Roster.Source, RosterVersion: input.Roster.Version,
		},
	}
	switch {
	case input.Completeness != execution.CompletenessFull || input.AnswerTruncated || input.RosterUnresolved:
		judge.mode = modePartial
	case deriveHistory:
		// Settled per group as modeExpected: every group but the whole item is
		// judged the same whether the roster turns out empty or not.
		judge.mode = modeExpected
	case len(input.Roster.Groups) == 0 && input.Roster.Source == RosterTargetPlan:
		judge.mode = modeEmptyTargetPlan
	case len(input.Roster.Groups) == 0:
		judge.mode = modeNothingExpected
	default:
		judge.mode = modeExpected
	}
	return judge
}

// Feed judges one stored group. A key already judged is ignored, and so is the
// whole-item group until the round finishes; judged says which happened.
func (judge *absenceJudge) Feed(key string, loaded GroupMemory) (judgement GroupJudgement, judged bool, err error) {
	if judge.finished {
		return GroupJudgement{}, false, errors.New("alarmd nodata: a group was fed to a finished no-data round")
	}
	if flags, present := judge.present[key]; present {
		if flags&presentFlagSeen != 0 {
			return GroupJudgement{}, false, nil
		}
		judge.present[key] = flags | presentFlagSeen
	} else if judge.callerDedupes {
		if _, expected := judge.roster.Groups[key]; expected {
			judge.judged[key] = struct{}{}
		}
	} else {
		if _, seen := judge.judged[key]; seen {
			return GroupJudgement{}, false, nil
		}
		judge.judged[key] = struct{}{}
	}
	if key == WholeItemGroup().Key() {
		judge.whole, judge.wholeFed = loaded, true
		return GroupJudgement{}, false, nil
	}
	if judge.deriveHistory && loaded.LastSeen != 0 {
		// A remembered key that does not parse back into a group is refused by
		// name rather than left out; see historyGroups.
		if _, ok := ParseGroupKey(key); !ok {
			return GroupJudgement{}, false, unparseableGroupKey(key)
		}
		judge.history.add(key)
	}
	return judge.judge(key, loaded, true, judge.mode), true, nil
}

// Finish judges the groups no stored entry named - present groups seen for the
// first time, expected groups never remembered, and the whole-item group - and
// returns their judgements with the Plan-level summary.
func (judge *absenceJudge) Finish() (absenceSummary, []GroupJudgement, error) {
	if judge.finished {
		return absenceSummary{}, nil, errors.New("alarmd nodata: a no-data round was finished twice")
	}
	judge.finished = true
	whole := WholeItemGroup().Key()
	var unfed []string
	for key, flags := range judge.present {
		if flags&presentFlagSeen == 0 && key != whole {
			unfed = append(unfed, key)
		}
	}
	for key := range judge.roster.Groups {
		if _, present := judge.present[key]; present || key == whole {
			continue
		}
		if _, seen := judge.judged[key]; !seen {
			unfed = append(unfed, key)
		}
	}
	sort.Strings(unfed)
	judgements := make([]GroupJudgement, 0, len(unfed)+1)
	for _, key := range unfed {
		judgements = append(judgements, judge.judge(key, GroupMemory{}, false, judge.mode))
	}

	// A derived history roster leaves Finish as its source and version alone:
	// its groups were never held (see above). judgeAbsenceResult, which has
	// the whole memory in hand, names them for its result.
	roster := judge.roster
	expected := uint64(len(roster.Groups))
	if judge.deriveHistory {
		version, err := judge.history.version(RosterHistory)
		if err != nil {
			return absenceSummary{}, nil, err
		}
		roster = Roster{Version: version, Source: RosterHistory}
		expected = judge.history.size()
		judge.facts.RosterVersion = version
	}
	judge.facts.Expected = expected

	wholeMode := judge.mode
	if judge.deriveHistory && judge.mode == modeExpected && judge.history.size() == 0 {
		wholeMode = modeNothingExpected
	}
	judgements = append(judgements, judge.judge(whole, judge.whole, judge.wholeFed, wholeMode))

	// The Plan-level fact, decided last because it is about what the round
	// left behind rather than about any one group.
	//
	// It is set only for a history roster, and only when the horizon has just
	// emptied it: that is the one case where the next round meets an empty
	// roster that is not "this item has no groups".
	exhaustedAt := int64(0)
	if wholeMode == modeExpected && roster.Source == RosterHistory && judge.facts.Expired > 0 &&
		judge.kept == 0 && len(judge.present) == 0 {
		exhaustedAt = judge.evaluationTime
	}
	// Then the one rule for every path, which is Evaluate's: see there.
	_, wholePresent := judge.present[whole]
	if judge.kept != 0 || wholePresent {
		exhaustedAt = 0
	} else if exhaustedAt == 0 {
		exhaustedAt = judge.loadedExhaustedAt
	}
	return absenceSummary{
		Facts: judge.facts, Roster: roster, TrackingExhaustedAt: exhaustedAt,
		WholeItemPresent: wholePresent, FreshPresent: judge.freshPresent,
	}, judgements, nil
}

// expects says whether the expected set holds this group.
func (judge *absenceJudge) expects(key string, entry GroupMemory, inMemory bool) bool {
	if judge.deriveHistory {
		return inMemory && key != WholeItemGroup().Key() && entry.LastSeen != 0
	}
	_, expected := judge.roster.Groups[key]
	return expected
}

// groupRound is one group while it is being judged: its memory as the round
// read it after the manual closes, and what the round has made of it so far.
type groupRound struct {
	key      string
	input    GroupMemory
	inMemory bool
	current  GroupMemory
	kept     bool
	verdict  Verdict
	closing  bool
	present  bool
	foreign  bool
	whole    bool
}

// judge applies the rules of one path to one group, in the order the whole
// evaluation applied them: what arrived (judgePresent), what the roster
// expects, and what the roster stopped expecting (closeDropped). Each step
// touches only this group, which is why the order of the groups does not
// matter and the order of the steps does.
func (judge *absenceJudge) judge(key string, loaded GroupMemory, inMemory bool, mode judgeMode) GroupJudgement {
	entry := loaded
	if inMemory {
		var closed bool
		if entry, closed = manualCloseOf(judge.closes, key, loaded, judge.evaluationTime); closed {
			judge.facts.ManualClosed++
		}
	}
	_, present := judge.present[key]
	_, foreign := judge.foreign[key]
	group := &groupRound{
		key: key, input: entry, inMemory: inMemory, current: entry, kept: inMemory,
		present: present, foreign: foreign, whole: key == WholeItemGroup().Key(),
	}
	expected := judge.expects(key, entry, inMemory)
	switch mode {
	case modePartial:
		judge.judgePresent(group)
		if expected {
			judge.judgeUnavailable(group)
		}
	case modeEmptyTargetPlan:
		// A target plan that resolved, completely, to no member expects
		// nothing and opens nothing: the whole-item absence is the old
		// target's reading and is not claimed for the new form. Data that
		// arrives is still data and recovers what it names. What a
		// confirmed-empty target decides on top of that is that every member
		// whose absence was open has left the target, so those absences close
		// once, here - a whole-item absence an earlier build left open among
		// them - and the whole-item entry is not kept.
		judge.judgePresent(group)
		judge.closeDropped(group, expected, false)
		if group.whole {
			group.kept = false
		}
	case modeNothingExpected:
		// A2 and A3. With nothing expected, the item speaks about itself: it
		// is absent when nothing arrived, and recovers when its own data does.
		// The groups that arrive are judged present like any other, which is
		// also where a history roster grows from. The declared roster source
		// is left as it is: an empty roster that was declared TARGET_STATIC is
		// a target that resolved to no host, and rewriting it to WHOLE would
		// hide exactly that.
		judge.judgePresent(group)
		if group.whole && len(judge.present) == 0 && judge.loadedExhaustedAt == 0 {
			// An empty roster has two causes that look identical from here:
			// this item never had groups, or the horizon emptied its history
			// roster. Only the second carries the Plan-level fact, and without
			// consulting it this branch rebuilds as a whole-item absence
			// exactly what the horizon has just stopped, every round.
			judge.judgeWholeItemAbsence(group)
		}
		judge.closeDropped(group, expected, true)
	default:
		judge.judgePresent(group)
		if expected {
			judge.judgeExpected(group)
		}
		judge.closeDropped(group, expected, false)
	}

	if !group.kept {
		group.current = GroupMemory{}
	}
	stored := group.kept && group.current != (GroupMemory{})
	judgement := GroupJudgement{
		Key: key, Verdict: group.verdict, Closing: group.closing, Memory: group.current, Kept: group.kept,
		Changed: inMemory != stored || (inMemory && stored && loaded != group.current),
		Loaded:  loaded, InMemory: inMemory,
	}
	if group.kept {
		judge.kept++
		if group.current.FirstAbsent == 0 && group.current.LastSeen == judge.evaluationTime {
			judge.freshPresent = true
		}
	}
	return judgement
}

// judgePresent is what a FULL round's own data says, before anything is said
// about what did not arrive (retention proposal, section 4 item 2).
//
// A4 and A7. Every group that reported is present, whether or not the roster
// expects it: it gets a NORMAL and a whole new memory entry. The NORMAL is
// what recovers an alert the roster no longer accounts for - a host the
// target dropped while its absence was open, a history group the horizon
// forgot - and the trigger's open-alert gate is what keeps it from reaching
// the consumer when nothing is open, as it does for every healthy series. The
// new entry is what clears a suppression: data arriving is the one thing that
// restarts tracking, and it restarts it from nothing rather than resuming the
// absence it interrupted. There is no "arrived but older than the last
// checkpoint" branch: a Slot is evaluated after its readiness, so what it
// holds is this period's, and the backend's staleness test exists because its
// input arrives by push.
//
// A round with any data also gives the whole item a NORMAL and drops its
// entry, every such round rather than once: the whole-item absence is the one
// no roster names, so nothing else would ever say it ended, and a recovery the
// gate held once has to be offered again. Any data means any: a host of
// another business that reported is data the item's query admitted, and the
// backend recovers the whole item on whatever arrived without asking whose
// host it was (nodata.py:85 and :100, against _process_dimensions, which
// filters nothing by business). With an empty agg_dimension every series
// projects onto the whole-item group itself, which then has its one verdict
// from here. That group is never remembered as seen: it is not a series, and a
// LastSeen would carry it into a history roster as one.
//
// A group whose host belongs to another business is not this item's to alert
// on or to remember. It is dropped from the memory, since remembering it would
// carry it into a later history roster, and it is not judged here: whether it
// closes with a NORMAL depends on what its own memory had open, which the
// roster step and the dropped-group step decide.
func (judge *absenceJudge) judgePresent(group *groupRound) {
	if group.present {
		if group.foreign {
			group.kept = false
		} else if keptAsSeen(group.whole, group.foreign) {
			if !judge.recoveryDisabled {
				group.verdict = VerdictNormal
			}
			group.current, group.kept = GroupMemory{LastSeen: judge.evaluationTime}, true
		}
	}
	if group.whole && len(judge.present) > 0 {
		if !judge.recoveryDisabled {
			group.verdict = VerdictNormal
		}
		group.kept = false
	}
}

// keptAsSeen is whether a present group is kept as seen at this round: any
// but the whole item and a host of another business (judgePresent).
func keptAsSeen(whole, foreign bool) bool { return !whole && !foreign }

// KeptAsSeen is whether a present group of key is kept as seen at this
// round, given the hosts of other businesses known before the read
// (HostResolution.OutOfBusiness): the rule judgePresent keeps it by, for a
// caller that stages it ahead. A history roster's hosts the scan
// resolves are not known before it, and a round with a stored record stages
// nothing.
func KeptAsSeen(key string, outOfBusiness map[string]struct{}) bool {
	_, foreign := outOfBusiness[key]
	return keptAsSeen(key == WholeItemGroup().Key(), foreign)
}

// heldPresent is a group judgePresent found present and left without its
// verdict because recovery is disabled: present all the same, so neither
// absent nor dropped. With recovery enabled every such group carries its
// NORMAL, and this is false.
func (judge *absenceJudge) heldPresent(group *groupRound) bool {
	if !judge.recoveryDisabled {
		return false
	}
	if group.whole {
		return len(judge.present) > 0
	}
	return group.present && !group.foreign
}

// judgedPresent says judgePresent has already settled this group: a verdict,
// or the present group recovery leaves without one.
func (judge *absenceJudge) judgedPresent(group *groupRound) bool {
	return group.verdict != "" || judge.heldPresent(group)
}

// judgeUnavailable is A1 for one expected group. Not FULL, or full by its own
// account and possibly cut at the query service's group cap, or judged against
// an expected set that did not resolve in full: an answer that may have left
// groups out is no evidence about the ones it left out, and a roster that may
// have left members out is no evidence that they left, so every expected group
// that did not arrive is unavailable - no absence is raised, kept, aged or
// closed on it. A group that did arrive is a different fact: its data is in
// the answer, and an answer cannot hold a series that was not sent. So it is
// judged present exactly as in a FULL round (judgePresent, and A6 for one in
// another business), which is what recovers its absence. Skipping it too left
// a strategy whose answer is always cut - one at the group cap, which is its
// size and not a blip - with absences that never recover however much data
// comes back. An absence the roster has stopped expecting is not closed here:
// the roster change is known, but that close is an absence verdict, and the
// next FULL round makes it.
func (judge *absenceJudge) judgeUnavailable(group *groupRound) {
	if group.present {
		if group.foreign && group.input.SuppressedAt == 0 {
			// A6 for a group judgePresent has just forgotten: its recovery is
			// said here, or its alert stays open with nothing left to close
			// it.
			group.verdict = VerdictNormal
		}
		if judge.judgedPresent(group) || group.input.SuppressedAt != 0 {
			return
		}
	}
	group.verdict = VerdictUnavailable
	judge.facts.Unavailable++
}

// judgeExpected is a FULL round's judgement of one group the roster expects.
func (judge *absenceJudge) judgeExpected(group *groupRound) {
	// A6. A host that belongs to another business is not this item's to alert
	// on: it recovers and is forgotten, which is the backend's
	// recover-and-skip. Checked before presence, because a group that is out
	// of business is out of business whether or not it reported.
	//
	// An absence the horizon already stopped is forgotten without the
	// recovery. Nothing was standing on it that this item still produces, and
	// the contract allows no NORMAL out of the expiry path by any route
	// (retention proposal, section 4 item 4).
	if group.foreign {
		if group.input.SuppressedAt == 0 {
			group.verdict = VerdictNormal
		}
		group.kept = false
		return
	}
	// A4 and A7. Present groups were judged by judgePresent, together with the
	// ones the roster does not expect.
	if judge.judgedPresent(group) {
		return
	}
	entry := GroupMemory{}
	if group.kept {
		entry = group.current
	}
	history := judge.rosterSource() == RosterHistory
	if entry.SuppressedAt != 0 {
		// Tracking of this absence has already stopped. No verdict, so no
		// synthetic anomaly, and the entry is left exactly as it is: a horizon
		// raised after the fact must not restart what a lower one stopped, and
		// the only thing that restarts it is data arriving.
		//
		// Except that a history roster still forgets it at the horizon. The
		// horizon never marks a history group - it forgets it - so a marked one
		// is a manual close (decision-024), and the typical close is of a group
		// whose data never returns. Kept, it would sit in a memory the running
		// Plan renews every round; forgotten at the horizon it leaves exactly as
		// an unmarked absence would, with nothing said.
		if history && trackingExpired(entry.FirstAbsent, judge.evaluationTime, judge.horizonSeconds) {
			judge.facts.Expired++
			group.kept = false
			return
		}
		judge.facts.Suppressed++
		return
	}
	// Only the first absent round starts the clock. Restarting it every round
	// would hold the reported period count at one however long the group
	// stayed away.
	if entry.FirstAbsent == 0 {
		entry.FirstAbsent = judge.evaluationTime
	}
	if trackingExpired(entry.FirstAbsent, judge.evaluationTime, judge.horizonSeconds) {
		// The horizon is reached: stop producing this absence, and do it
		// without a verdict of any kind. A NORMAL here would be a recovery
		// nobody observed, which is the reading this whole mechanism exists to
		// avoid.
		judge.facts.Expired++
		if history {
			// A history roster is derived from this memory, so forgetting the
			// group is what stops it being expected. There is nowhere else for
			// it to come back from.
			group.kept = false
			return
		}
		// Something outside this memory expects this group and will go on
		// expecting it, so forgetting it would let the next round meet it as
		// new and start the clock again. The stopping is recorded instead,
		// compactly, beside the timestamps that explain it.
		entry.SuppressedAt = judge.evaluationTime
		group.current, group.kept = entry, true
		return
	}
	group.verdict = VerdictAnomaly
	judge.facts.Absent++
	judge.facts.AbsentAges.count(entry.FirstAbsent, judge.evaluationTime)
	group.current, group.kept = entry, true
}

// judgeWholeItemAbsence is A2 for the whole-item group: nothing is expected
// and nothing arrived.
func (judge *absenceJudge) judgeWholeItemAbsence(group *groupRound) {
	entry := GroupMemory{}
	if group.kept {
		entry = group.current
	}
	if entry.SuppressedAt != 0 {
		// The whole-item absence itself was stopped. Same rule as a group's: no
		// verdict, and the entry stays as it is.
		judge.facts.Suppressed++
		return
	}
	if entry.FirstAbsent == 0 {
		entry.FirstAbsent = judge.evaluationTime
	}
	// The whole-item group is not a series and never records a LastSeen: one
	// would carry it into a history roster as if it were.
	entry.LastSeen = 0
	if trackingExpired(entry.FirstAbsent, judge.evaluationTime, judge.horizonSeconds) {
		// Marked rather than forgotten. This group is not in any roster - it
		// is what an empty roster produces - so deleting it would have the next
		// empty round build it again from nothing.
		judge.facts.Expired++
		entry.SuppressedAt = judge.evaluationTime
		group.current, group.kept = entry, true
		return
	}
	group.verdict = VerdictAnomaly
	judge.facts.Absent++
	judge.facts.AbsentAges.count(entry.FirstAbsent, judge.evaluationTime)
	group.current, group.kept = entry, true
}

// closeDropped is A8: a remembered group the roster has stopped expecting is
// handled by what it has open.
//
// One with an open absence -- a FirstAbsent, so an alert may be standing on it
// -- gets exactly one NORMAL and is then forgotten. The roster no longer
// expects it, so no later round will ever say NORMAL for it; without this one
// verdict the alert stands until somebody closes it by hand. That is the case
// a host leaving the target, and an item's whole-item absence turning into
// target groups, both land in.
//
// One with nothing open keeps its memory and gets no verdict, which is where a
// history roster grows from.
//
// One rule rather than a branch in each path, because the reason is one
// reason and it does not care which way the roster changed: a target roster
// becoming empty strands the host groups exactly as a target roster arriving
// strands the whole-item group. The NORMAL is not counted as an absence -- it
// is the end of one.
//
// It reads the group's memory as loaded, not what the steps before have made
// of it, so the answer does not depend on what they have already written and
// a retried Slot reaching here with the same memory gets the same answer. That
// includes whether the absence was stopped: a group whose host reported for
// another business has already had its entry removed, and reading the stop off
// what is left would turn that stopped absence into the recovery the horizon
// exists to avoid. A group already judged this round keeps that verdict: the
// roster expects it, it reported, or it is the whole-item group this round has
// just spoken about.
//
// wholeItemDecided says the whole-item path has already settled that group
// this round, including by deciding to say nothing about it. Without it the
// silent decisions are not silent: the whole-item group is not in any roster,
// so "the roster stopped expecting it" is true of it on every empty round, and
// this step turns a stopped absence into the closing NORMAL the horizon exists
// to avoid - then deletes the entry, so the next empty round opens the absence
// again from nothing. The paths that give the group a verdict do not need it;
// they are skipped as judged. The confirmed-empty target plan passes false on
// purpose: there the whole-item reading really is retired and closing it is the
// point.
func (judge *absenceJudge) closeDropped(group *groupRound, expected, wholeItemDecided bool) {
	if !group.inMemory || group.input.FirstAbsent == 0 {
		return
	}
	if (wholeItemDecided && group.whole) || expected {
		return
	}
	if judge.judgedPresent(group) {
		return
	}
	if group.input.SuppressedAt != 0 {
		// Tracking of this absence was stopped before the roster dropped the
		// group. Nothing is standing to close, so the group is forgotten
		// without a verdict rather than recovered with one.
		group.kept = false
		return
	}
	group.verdict, group.closing, group.kept = VerdictNormal, true, false
}

func (judge *absenceJudge) rosterSource() RosterSource {
	if judge.deriveHistory {
		return RosterHistory
	}
	return judge.roster.Source
}

// judgeAbsenceResult feeds every key of memory, in the order keys names them
// and ignoring repeats, and assembles the whole result the evaluation has
// always returned.
func judgeAbsenceResult(
	judge *absenceJudge, memory map[string]GroupMemory, keys []string,
) (AbsenceResult, error) {
	result := AbsenceResult{
		Verdicts: make(map[string]Verdict, len(judge.roster.Groups)+1),
		Memory:   copyGroupMemory(memory),
	}
	apply := func(judgement GroupJudgement) {
		if judgement.Verdict != "" {
			result.Verdicts[judgement.Key] = judgement.Verdict
		}
		if judgement.Closing {
			if result.Closing == nil {
				result.Closing = map[string]bool{}
			}
			result.Closing[judgement.Key] = true
		}
		if judgement.Kept {
			result.Memory[judgement.Key] = judgement.Memory
		} else {
			delete(result.Memory, judgement.Key)
		}
	}
	for _, key := range keys {
		judgement, judged, err := judge.Feed(key, memory[key])
		if err != nil {
			return AbsenceResult{}, err
		}
		if judged {
			apply(judgement)
		}
	}
	summary, rest, err := judge.Finish()
	if err != nil {
		return AbsenceResult{}, err
	}
	for _, judgement := range rest {
		apply(judgement)
	}
	result.Facts = summary.Facts
	result.Roster = summary.Roster
	if judge.deriveHistory {
		// The whole read's result names the roster it was judged against, and
		// the whole read has the memory to name it from; the judgement did not
		// hold it. Every key the judgement folded parsed when it was fed, and
		// historyGroups selects the same keys.
		if result.Roster.Groups, err = historyGroups(memory); err != nil {
			return AbsenceResult{}, err
		}
	}
	result.TrackingExhaustedAt = summary.TrackingExhaustedAt
	result.WholeItemPresent = summary.WholeItemPresent
	return result, nil
}

func memoryKeys(memory map[string]GroupMemory) []string {
	keys := make([]string, 0, len(memory))
	for key := range memory {
		keys = append(keys, key)
	}
	return keys
}
