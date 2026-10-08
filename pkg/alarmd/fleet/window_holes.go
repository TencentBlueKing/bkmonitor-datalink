// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	model "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// RecentRoundsKept is how many completions the tracker remembers per object
// when the object's worker does not say where its windows start: an older
// worker. With the window start the tracker keeps every round from there,
// which is what any hole of the object's windows can name, and no more.
const RecentRoundsKept = 16

// primaryAnswer is what a round's primary query answered, in the readings
// a hole's minute is told apart by: not recorded, whole with records, whole
// with none, not whole.
type primaryAnswer uint8

const (
	primaryUnrecorded primaryAnswer = iota
	primaryWholeWithData
	primaryWholeEmpty
	primaryNotWhole
)

// answerOf reads a completion's primary facts into its answer.
func answerOf(primary *observability.PrimaryInputFacts) primaryAnswer {
	switch {
	case primary == nil:
		return primaryUnrecorded
	case primary.PrimaryAnsweredWhole():
		return primaryWholeWithData
	case primary.Completeness == "FULL":
		return primaryWholeEmpty
	default:
		return primaryNotWhole
	}
}

// answeredWhole is a round whose primary answered whole, with records or
// without.
func (answer primaryAnswer) answeredWhole() bool {
	return answer == primaryWholeWithData || answer == primaryWholeEmpty
}

// roundMark is one remembered completion in sixteen bytes: the record minute
// the round evaluated (zero when the round carried no record, then inferred
// from the object's Slot offset when one is known), the completion's kind
// and reason as indexes into roundWordTable, and what the primary answered.
// A window reaching back a day holds a day of rounds, so a round holds no
// string and no pointer of its own.
type roundMark struct {
	end         int64
	kind        uint16
	reason      uint16
	answer      primaryAnswer
	endInferred bool
}

// roundWords interns the words a round is filed under -- completion kinds
// and reasons, the contract's closed lists -- once for the process. The
// table grows only by a word it has not seen, and a word past the uint16 a
// round holds reads as none: the round's minute keeps its answer, only its
// word is lost.
type roundWords struct {
	mu    sync.RWMutex
	index map[string]uint16
	words []string
}

var roundWordTable = &roundWords{index: map[string]uint16{"": 0}, words: []string{""}}

func (table *roundWords) of(word string) uint16 {
	table.mu.RLock()
	index, known := table.index[word]
	table.mu.RUnlock()
	if known {
		return index
	}
	table.mu.Lock()
	defer table.mu.Unlock()
	if index, known = table.index[word]; known {
		return index
	}
	if len(table.words) > math.MaxUint16 {
		return 0
	}
	index = uint16(len(table.words))
	table.index[word], table.words = index, append(table.words, word)
	return index
}

func (table *roundWords) word(index uint16) string {
	table.mu.RLock()
	defer table.mu.RUnlock()
	if int(index) >= len(table.words) {
		return ""
	}
	return table.words[index]
}

// kindWord and reasonWord are the round's words back from the table.
func (mark roundMark) kindWord() string   { return roundWordTable.word(mark.kind) }
func (mark roundMark) reasonWord() string { return roundWordTable.word(mark.reason) }

// rememberRound files one completion on the object's rounds, kept in minute
// order, and keeps the Slot-to-minute offset current from any round that
// reported a minute. The rounds kept are every one from where the object's
// windows start (HistoryCoverageFacts.WindowStart, the latest the worker
// said), or the last RecentRoundsKept while it has said nothing.
func rememberRound(state *queryGroupState, slot int64, kind, reason string, coverage *observability.HistoryCoverageFacts, primary *observability.PrimaryInputFacts) {
	mark := roundMark{kind: roundWordTable.of(kind), reason: roundWordTable.of(reason), answer: answerOf(primary)}
	if coverage != nil && coverage.End > 0 {
		mark.end = coverage.End
		if slot > 0 {
			state.slotOffset, state.slotOffsetKnown = slot-coverage.End, true
		}
	} else if state.slotOffsetKnown && slot > 0 {
		mark.end, mark.endInferred = slot-state.slotOffset, true
	}
	if coverage != nil && coverage.WindowStart > 0 {
		state.windowStart = coverage.WindowStart
	}
	if state.firstSlot == 0 && slot > 0 {
		state.firstSlot = slot
	}
	// In minute order, a round of the same minute after the ones already
	// there: a replayed Slot lands where its minute is, and roundAt reads
	// the latest of a minute first.
	i := len(state.rounds)
	state.rounds = append(state.rounds, mark)
	for i > 0 && state.rounds[i-1].end > mark.end {
		state.rounds[i] = state.rounds[i-1]
		i--
	}
	state.rounds[i] = mark
	state.rounds = keptRounds(state.rounds, state.windowStart)
}

// keptRounds drops the rounds no window of the object can name any more:
// every one before the window start, or all but the last RecentRoundsKept
// when there is none. Moved down in place, so the dropped rounds do not stay
// behind in the array.
func keptRounds(rounds []roundMark, windowStart int64) []roundMark {
	drop := 0
	if windowStart > 0 {
		drop = sort.Search(len(rounds), func(i int) bool { return rounds[i].end >= windowStart })
	} else if len(rounds) > RecentRoundsKept {
		drop = len(rounds) - RecentRoundsKept
	}
	if drop == 0 {
		return rounds
	}
	kept := copy(rounds, rounds[drop:])
	return rounds[:kept]
}

// rememberedSince is the record minute of the first round this process
// remembered for the object: a hole before it is one this process never
// had a chance to see (BEFORE_THIS_PROCESS), where a hole after it that no
// remembered round covers has rolled out of memory (NOT_IN_MEMORY). Zero
// while it is not known -- no round remembered with a Slot, or none that
// told the object's Slot-to-minute offset -- and then no minute is before
// it, so no hole is filed before this process.
func rememberedSince(state *queryGroupState) int64 {
	if state.firstSlot <= 0 || !state.slotOffsetKnown {
		return 0
	}
	return state.firstSlot - state.slotOffset
}

// roundAt finds the remembered round that evaluated the minute, a round that
// reported the minute before one whose minute was inferred, the latest of
// either first. The rounds are in minute order, so the minute's rounds are
// found by search, not by a walk over a window of them.
func roundAt(rounds []roundMark, minute int64) (roundMark, bool) {
	from := sort.Search(len(rounds), func(i int) bool { return rounds[i].end >= minute })
	to := from + sort.Search(len(rounds)-from, func(i int) bool { return rounds[from+i].end > minute })
	var inferred roundMark
	inferredFound := false
	for i := to - 1; i >= from; i-- {
		mark := rounds[i]
		if !mark.endInferred {
			return mark, true
		}
		if !inferredFound {
			inferred, inferredFound = mark, true
		}
	}
	return inferred, inferredFound
}

// queryFreeCompletion reports whether the completion kind is one that never
// reached the query stage, from the module's own list rather than a copy.
func queryFreeCompletion(kind string) bool {
	for _, queryFree := range model.QueryFreeCompletionKinds {
		if kind == string(queryFree) {
			return true
		}
	}
	return false
}

// windowKey is the identity a reader follows across rounds.
func windowKey(strategy, series string, level uint32) string {
	key := series + "/" + strconv.FormatUint(uint64(level), 10)
	if strategy == "" {
		return key
	}
	return strategy + "/" + key
}

// windowRows reads every named window of the round against the object's
// remembered rounds: one WindowRow per fact, each listed hole with whose
// minute it is, the unlisted holes counted as beyond memory, and the verdict
// decided from the counts. since is rememberedSince: a hole no remembered
// round covers is BEFORE_THIS_PROCESS when its minute is before it.
func windowRows(rounds []roundMark, facts *observability.HistoryCoverageFacts, since, held int64) []WindowRow {
	if facts == nil || len(facts.Windows) == 0 {
		return nil
	}
	rows := make([]WindowRow, 0, len(facts.Windows))
	for _, window := range facts.Windows {
		row := WindowRow{
			Key:      windowKey(window.Strategy, window.Series, window.Level),
			Strategy: window.Strategy, Business: window.Business, Series: window.Series, Level: window.Level,
			Valid: window.Valid, Required: window.Required, End: time.Unix(window.End, 0).UTC(),
			Guarded: window.Guarded, GuardReason: window.GuardReason, Fresh: window.Fresh,
			MissingTotal: window.MissingTotal, UnusableTotal: window.UnusableTotal,
		}
		for _, minute := range window.Missing {
			hole := WindowHole{At: time.Unix(minute, 0).UTC()}
			if mark, found := roundAt(rounds, minute); found {
				hole.Round, hole.Reason, hole.Inferred = mark.kindWord(), mark.reasonWord(), mark.endInferred
				switch {
				case mark.answer == primaryWholeWithData:
					hole.Cause = HoleAnsweredWithoutSeries
					row.HolesBy.AnsweredWithoutSeries++
				case mark.answer == primaryWholeEmpty:
					hole.Cause = HoleAnsweredEmpty
					row.HolesBy.AnsweredEmpty++
				case mark.answer == primaryNotWhole || queryFreeCompletion(mark.kindWord()):
					// PARTIAL or UNAVAILABLE, or a Slot given up without a
					// query -- the kind itself says no primary was asked
					// for: the minute was not seen whole by this side.
					hole.Cause = HoleInputIncomplete
					row.HolesBy.InputIncomplete++
				default:
					// A round that ran a query and did not record what it
					// answered: not this side's by default, and not the
					// data's either.
					hole.Cause = HolePrimaryUnrecorded
					row.HolesBy.PrimaryUnrecorded++
				}
			} else if minute < since {
				hole.Cause = HoleBeforeThisProcess
				row.HolesBy.BeforeThisProcess++
			} else if held > 0 && minute <= held {
				hole.Cause = HoleHeldByLine
				row.HolesBy.HeldByLine++
			} else {
				hole.Cause = HoleNotInMemory
				row.HolesBy.NotInMemory++
			}
			row.Holes = append(row.Holes, hole)
		}
		for _, minute := range window.Unusable {
			hole := WindowHole{At: time.Unix(minute, 0).UTC(), Cause: HolePointUnusable}
			if mark, found := roundAt(rounds, minute); found {
				hole.Round, hole.Reason, hole.Inferred = mark.kindWord(), mark.reasonWord(), mark.endInferred
			}
			row.HolesBy.Unusable++
			row.Holes = append(row.Holes, hole)
		}
		// Holes beyond the listing bound are counted and not named, and a
		// hole nobody named is one nobody can speak for.
		if unlisted := window.MissingTotal - uint32(len(window.Missing)); unlisted > 0 {
			row.HolesBy.NotInMemory += unlisted
		}
		if unlisted := window.UnusableTotal - uint32(len(window.Unusable)); unlisted > 0 {
			row.HolesBy.Unusable += unlisted
		}
		sort.SliceStable(row.Holes, func(i, j int) bool { return row.Holes[i].At.Before(row.Holes[j].At) })
		row.Verdict = verdictOf(row.HolesBy)
		rows = append(rows, row)
	}
	return rows
}

// unlistedHolesAnswered reads the short windows a round did not name against
// the object's remembered rounds: true only when some went unnamed, the
// worker sent a whole union of their missing minutes and no unusable point,
// and every one of those minutes was evaluated by a remembered round that
// answered its primary whole, with data or empty -- the readings windowRows
// gives a named hole as ROUND_ANSWERED_WITHOUT_SERIES and ROUND_ANSWERED_EMPTY.
// A minute the process does not remember, or a round that answered partly,
// not at all, or without recording its answer, leaves it false: an unnamed
// hole nobody can place is not the data's by default.
func unlistedHolesAnswered(rounds []roundMark, facts *observability.HistoryCoverageFacts) bool {
	if facts == nil || facts.Short <= uint32(len(facts.Windows)) {
		return false
	}
	if facts.MissingMinutesTruncated || facts.ShortUnusable > 0 || len(facts.MissingMinutes) == 0 {
		return false
	}
	for _, minute := range facts.MissingMinutes {
		mark, found := roundAt(rounds, minute)
		if !found || !mark.answer.answeredWhole() {
			return false
		}
	}
	return true
}

// unlistedHolesBeforeThisProcess reads the same unnamed windows for the one
// other reading that is not this side's: every minute of the union either
// evaluated by a remembered round that answered whole, or before the first
// round this process remembers for the object (since, rememberedSince), and
// at least one of the second. The same union rules hold: some windows
// unnamed, the union whole, no unusable point. It never holds together with
// unlistedHolesAnswered.
func unlistedHolesBeforeThisProcess(rounds []roundMark, facts *observability.HistoryCoverageFacts, since int64) bool {
	if facts == nil || facts.Short <= uint32(len(facts.Windows)) {
		return false
	}
	if facts.MissingMinutesTruncated || facts.ShortUnusable > 0 || len(facts.MissingMinutes) == 0 {
		return false
	}
	before := false
	for _, minute := range facts.MissingMinutes {
		mark, found := roundAt(rounds, minute)
		switch {
		case found && mark.answer.answeredWhole():
		case !found && minute < since:
			before = true
		default:
			return false
		}
	}
	return before
}
