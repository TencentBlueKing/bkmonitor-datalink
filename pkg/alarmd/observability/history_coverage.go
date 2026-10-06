// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

// HistoryCoverageFacts is how much of the detection window arrived, against
// how much the algorithm asked for, summed over the series evaluated in one
// run.
//
// It carries counts and not a verdict. Whether "short on every round for
// ever" means anything is a question about the strategy, and the answer
// depends on how long its series live -- something this package cannot know
// and must not guess. What it can do is stop throwing away the two numbers
// that let a reader decide.
type HistoryCoverageFacts struct {
	// Levels is how many Level windows were summarised, Short how many of
	// those held fewer valid positions than they required.
	//
	// Short == 0 with Levels > 0 says every window was complete. That is a
	// different statement from Levels == 0, which says no window was
	// summarised at all -- a run that failed before evaluation, or one whose
	// series were all held back. Reporting the second as though it were the
	// first is how a stalled run comes to look healthy.
	Levels uint32 `json:"levels"`
	Short  uint32 `json:"short"`
	// Resumed and Constrained are the series this run handled without
	// summarising a window for them: Resumed because their State was already
	// applied at this Slot's version, so the round was bookkeeping and not an
	// evaluation; Constrained because their State could not be loaded, so
	// there was nothing to evaluate. They are the denominator Levels lacks.
	//
	// Levels alone answers "how many windows did this run summarise", and a
	// reader who takes it for "how many Levels is this object being watched
	// on" is wrong by these two counts. A run that resumed 227 of 249 series
	// reports Levels = 22, which without these is the same shape on the page
	// as an object that has 22 -- a partial round rendered as a small healthy
	// one. Kept as two counts and not one because they are different answers,
	// and because a reading that carries them names its own mechanism.
	Resumed     uint32 `json:"resumed,omitempty"`
	Constrained uint32 `json:"constrained,omitempty"`
	// Empty is how many of those held no valid position at all. Separate from
	// Short because zero points and few points are different situations that
	// report the same reason: few points is a series not yet old enough, no
	// points is a series whose record produced nothing usable -- which is what
	// a series whose data has stopped looks like once its last real point has
	// slid out of the window.
	Empty uint32 `json:"empty"`
	// WorstValid and WorstRequired are the pair belonging to the single worst
	// window -- the one with the largest shortfall -- and never a minimum over
	// one field beside a maximum over the other. Combined independently they
	// would describe a window no series reported, and a reader deciding
	// whether a strategy can ever converge would be acting on a number that
	// does not exist.
	WorstValid    uint32 `json:"worst_valid"`
	WorstRequired uint32 `json:"worst_required"`
	// Guarded is how many of these windows reported a completeness held over
	// from a guard rather than computed from the window this round.
	//
	// While a Level's persisted state or a Plan gap record forces WARMING or
	// GAPPED, the freshly computed verdict is discarded and the held one is
	// reported -- and the position counts beside it are still the live ones. So
	// the reason and the numbers under it can be from two different moments, and
	// a window that has already refilled keeps reporting the verdict it had
	// before it did. Whoever reads the reason has to be able to tell "this is
	// what the window says now" from "this is what it said, and nothing else has
	// been allowed through yet".
	Guarded uint32 `json:"guarded,omitempty"`
	// Fresh is how many of these windows belong to a series for which no
	// persisted state was loaded this round, and ShortFresh how many of the
	// short ones do.
	//
	// They are the difference between a strategy whose series identity churns
	// and a series whose data has holes, and nothing else published here can
	// tell those apart: both hold a short window on every round, for ever, and
	// report the same completeness while doing it. The first is a different
	// series each time -- new, with nothing loaded -- and the second is the same
	// one that has been evaluated for hours.
	//
	// Two counts rather than one share, because the denominators differ: Fresh
	// is out of Levels and ShortFresh out of Short. Reading ShortFresh against
	// Levels would be a fraction of two populations, which is the shape that
	// produces a number no window ever reported.
	//
	// A round following a StateGeneration change reports every window fresh
	// without anything having churned, so a single round of this says nothing;
	// only a run of them does.
	Fresh      uint32 `json:"fresh,omitempty"`
	ShortFresh uint32 `json:"short_fresh,omitempty"`
	// Abnormal is how many Level verdicts were ABNORMAL, AbnormalOnIncomplete
	// how many of those were reached on a window that was not FULL. The
	// trigger decides ABNORMAL before reading completeness and the output
	// contract permits exactly that on WARMING and GAPPED history; this pair
	// is how much alerting actually rides on it.
	Abnormal             uint32 `json:"abnormal,omitempty"`
	AbnormalOnIncomplete uint32 `json:"abnormal_on_incomplete,omitempty"`
	// Unusable is how many Levels could not use this round's record -- the
	// detection returned UNAVAILABLE or ERROR for it -- and UnusableReason the
	// first such Level's reason code. An empty window is made of exactly these
	// rounds: a record that arrives and cannot be used. A record that does not
	// arrive is never evaluated and never reaches a window at all.
	Unusable       uint32 `json:"unusable,omitempty"`
	UnusableReason string `json:"unusable_reason,omitempty"`
	// Windows names the worst of the short windows -- which series and Level,
	// how full, which positions are empty -- worst first, at most
	// MaxHistoryWindows of them. The counts above are the fold over every
	// window; these are the ones a reader would ask about next. Short above
	// their number says the rest were counted and not named.
	Windows []HistoryWindowFact `json:"windows,omitempty"`
	// End is the newest record source time any window of this run ended at:
	// the minute this round evaluated, on every run that summarised a
	// window. It is what lets a later hole at that minute be matched to
	// this round.
	End int64 `json:"end,omitempty"`
}

// MaxHistoryWindows and MaxHistoryWindowHoles are the bounds the evaluator
// names windows and holes under; facts beyond them did not come from it.
const (
	MaxHistoryWindows     = 8
	MaxHistoryWindowHoles = 16
)

// HistoryWindowFact is one short window by identity: the series digest and
// Level, the pair, the source time it ends at, and its holes. Missing are the
// positions no record of the series was ever applied at -- the series was
// not in that round's result, or the round never ran -- and Unusable the
// positions holding a record the Level could not use. Both oldest first and
// bounded; the totals count them all. Guarded and GuardReason say the verdict
// on this window is held by a guard and under which reason it was raised;
// Fresh that no state was loaded for the series this round.
type HistoryWindowFact struct {
	// Strategy and Business name the Plan the window belongs to. Level is an
	// ordinal inside a Plan, so a window named by series and Level alone
	// cannot be acted on: the thing a reader goes and changes is a strategy,
	// and two of them sharing a Query Group both report "Level 1".
	Strategy      string  `json:"strategy_id,omitempty"`
	Business      string  `json:"business_id,omitempty"`
	Series        string  `json:"series"`
	Level         uint32  `json:"level"`
	Valid         uint32  `json:"valid"`
	Required      uint32  `json:"required"`
	End           int64   `json:"end"`
	Missing       []int64 `json:"missing,omitempty"`
	MissingTotal  uint32  `json:"missing_total"`
	Unusable      []int64 `json:"unusable,omitempty"`
	UnusableTotal uint32  `json:"unusable_total"`
	Guarded       bool    `json:"guarded,omitempty"`
	GuardReason   string  `json:"guard_reason,omitempty"`
	Fresh         bool    `json:"fresh,omitempty"`
}

// Shortfall is how many points the worst window was missing. Zero when
// nothing was short, which is why callers must read Short to tell "nothing
// was short" from "nothing was measured".
func (facts HistoryCoverageFacts) Shortfall() uint32 {
	if facts.Short == 0 || facts.WorstRequired <= facts.WorstValid {
		return 0
	}
	return facts.WorstRequired - facts.WorstValid
}

// CoverageRejectionRule names one reason normalize refused a coverage fact
// set. Each is one inequality or one shape the evaluator's own count cannot
// produce; the list is closed so that each has a counter and a word.
type CoverageRejectionRule string

const (
	CoverageRejectLevelsZero             CoverageRejectionRule = "LEVELS_ZERO"
	CoverageRejectShortOverLevels        CoverageRejectionRule = "SHORT_OVER_LEVELS"
	CoverageRejectEmptyOverShort         CoverageRejectionRule = "EMPTY_OVER_SHORT"
	CoverageRejectGuardedOverLevels      CoverageRejectionRule = "GUARDED_OVER_LEVELS"
	CoverageRejectFreshOverLevels        CoverageRejectionRule = "FRESH_OVER_LEVELS"
	CoverageRejectShortFreshOverShort    CoverageRejectionRule = "SHORT_FRESH_OVER_SHORT"
	CoverageRejectShortFreshOverFresh    CoverageRejectionRule = "SHORT_FRESH_OVER_FRESH"
	CoverageRejectUnusableOverLevels     CoverageRejectionRule = "UNUSABLE_OVER_LEVELS"
	CoverageRejectUnusableReasonUnpaired CoverageRejectionRule = "UNUSABLE_REASON_UNPAIRED"
	CoverageRejectEmptyWithValidPoints   CoverageRejectionRule = "EMPTY_WITH_VALID_POINTS"
	CoverageRejectWindowsOverShort       CoverageRejectionRule = "WINDOWS_OVER_SHORT"
	CoverageRejectWindowsOverBound       CoverageRejectionRule = "WINDOWS_OVER_BOUND"
	CoverageRejectWindowUnnamed          CoverageRejectionRule = "WINDOW_UNNAMED"
	CoverageRejectWindowRequiredZero     CoverageRejectionRule = "WINDOW_REQUIRED_ZERO"
	CoverageRejectWindowNotShort         CoverageRejectionRule = "WINDOW_NOT_SHORT"
	CoverageRejectWindowHoleArithmetic   CoverageRejectionRule = "WINDOW_HOLE_ARITHMETIC"
	CoverageRejectWindowHoleListOverrun  CoverageRejectionRule = "WINDOW_HOLE_LIST_OVERRUN"
	CoverageRejectWindowGuardReasonFree  CoverageRejectionRule = "WINDOW_GUARD_REASON_UNGUARDED"
)

// CoverageRejectionRules is the closed list, in the order normalize checks.
var CoverageRejectionRules = []CoverageRejectionRule{
	CoverageRejectLevelsZero, CoverageRejectShortOverLevels, CoverageRejectEmptyOverShort, CoverageRejectGuardedOverLevels,
	CoverageRejectFreshOverLevels, CoverageRejectShortFreshOverShort, CoverageRejectShortFreshOverFresh, CoverageRejectUnusableOverLevels,
	CoverageRejectUnusableReasonUnpaired, CoverageRejectEmptyWithValidPoints, CoverageRejectWindowsOverShort, CoverageRejectWindowsOverBound,
	CoverageRejectWindowUnnamed, CoverageRejectWindowRequiredZero, CoverageRejectWindowNotShort, CoverageRejectWindowHoleArithmetic,
	CoverageRejectWindowHoleListOverrun, CoverageRejectWindowGuardReasonFree,
}

// CoverageRejection is what is left of a coverage fact set normalize refused:
// the one rule it broke and its anchor. The anchor is what the rule alone
// determines and what a reader goes to look at next: a rule that judges one
// window carries that window's series; a rule that judges a property of the
// whole set carries nothing, because the set's identity is the row itself.
// Nothing the rule judged untrustworthy travels with it -- not the counts,
// not the pair -- so a reader cannot pick a number out of the shell and
// judge by it. It exists because a refused fact set used to leave
// nothing at all: the row's coverage was simply absent, which on the page is
// the same shape as every window complete. A reading the server itself
// declared incoherent has to say so where the reading would have been.
type CoverageRejection struct {
	Rule   CoverageRejectionRule `json:"rule"`
	Series string                `json:"series,omitempty"`
}

// reportsNothing says whether every field of the set is zero: a run that
// summarised no window and said nothing else about one either. It names every
// field on purpose -- the struct holds a slice, so Go will not compare it to
// its zero value -- and the test that feeds each field alone through
// normalize is what catches a field added to the struct and not to this list.
// A set that is zero on the counts but carries a worst pair, a verdict count,
// a reason or an end minute is not a run that summarised nothing: it is a
// contradiction, and falls through to the rule that names it.
func (f HistoryCoverageFacts) reportsNothing() bool {
	return f.Levels == 0 && f.Short == 0 && f.Empty == 0 &&
		f.WorstValid == 0 && f.WorstRequired == 0 &&
		f.Guarded == 0 && f.Fresh == 0 && f.ShortFresh == 0 &&
		f.Abnormal == 0 && f.AbnormalOnIncomplete == 0 &&
		f.Unusable == 0 && f.UnusableReason == "" &&
		len(f.Windows) == 0 && f.End == 0 &&
		f.Resumed == 0 && f.Constrained == 0
}

// summarisedNothing says whether the set summarised no window. Distinct from
// reportsNothing: a run that summarised nothing and says why -- every series
// resumed, or none of their State loadable -- is a reading and not a silence,
// and it is the reading a reader most needs. Only the window fields are asked
// about here, so the two counts that explain a zero cannot be what keeps the
// zero from being recognised.
func (f HistoryCoverageFacts) summarisedNothing() bool {
	return f.Levels == 0 && f.Short == 0 && f.Empty == 0 &&
		f.WorstValid == 0 && f.WorstRequired == 0 &&
		f.Guarded == 0 && f.Fresh == 0 && f.ShortFresh == 0 &&
		f.Abnormal == 0 && f.AbnormalOnIncomplete == 0 &&
		f.Unusable == 0 && f.UnusableReason == "" &&
		len(f.Windows) == 0 && f.End == 0
}

func rejectCoverage(rule CoverageRejectionRule) (*HistoryCoverageFacts, *CoverageRejection) {
	return nil, &CoverageRejection{Rule: rule}
}

func rejectWindow(rule CoverageRejectionRule, window HistoryWindowFact) (*HistoryCoverageFacts, *CoverageRejection) {
	return nil, &CoverageRejection{Rule: rule, Series: window.Series}
}

// normalizeHistoryCoverageFacts returns the facts it accepts, or nil and the
// rule under which it refused them. Nil facts in give nil and no rejection:
// a run that reported no coverage is not a run whose coverage was refused.
func normalizeHistoryCoverageFacts(facts *HistoryCoverageFacts) (*HistoryCoverageFacts, *CoverageRejection) {
	if facts == nil {
		return nil, nil
	}
	copied := *facts
	// A Short above Levels cannot have come from counting the same windows,
	// so the pair is not describing one run and nothing derived from it can be
	// trusted. Clamping would keep a plausible-looking number; dropping the
	// facts leaves the absence visible -- and, since the rules were named,
	// leaves the rule in its place.
	// Guarded is counted over the same windows as Levels, short or not, so it
	// cannot exceed them either. Checked here with the rest rather than clamped:
	// a count that could not have come from these windows makes every number
	// beside it suspect, and a plausible-looking clamp hides that.
	// Fresh is counted over the same windows as Levels and ShortFresh over the
	// same windows as Short, and every short fresh window is also a fresh one --
	// so all three bounds hold by construction, and a pair that breaks one did
	// not come from counting these windows. Checked rather than clamped for the
	// same reason as the rest: this is the count a reader uses to decide whether
	// a strategy's dimensions are at fault, and a clamped one would still look
	// like an answer.
	// A set that is zero everywhere is a run that summarised no window, which
	// the field's own comment calls a meaningful statement; it is dropped
	// as no coverage, not refused. Zero windows with something counted on
	// them is the contradiction.
	if copied.reportsNothing() {
		return nil, nil
	}
	// A run that summarised no window and says why is a reading, not a
	// contradiction: every series was resumed, or none of their State could be
	// loaded, and the counts saying so are the whole of what it has to report.
	// Checked before the rule below, which would otherwise refuse the one
	// reading that explains a zero.
	if copied.summarisedNothing() {
		return &copied, nil
	}
	switch {
	case copied.Levels == 0:
		return rejectCoverage(CoverageRejectLevelsZero)
	case copied.Short > copied.Levels:
		return rejectCoverage(CoverageRejectShortOverLevels)
	case copied.Empty > copied.Short:
		return rejectCoverage(CoverageRejectEmptyOverShort)
	case copied.Guarded > copied.Levels:
		return rejectCoverage(CoverageRejectGuardedOverLevels)
	case copied.Fresh > copied.Levels:
		return rejectCoverage(CoverageRejectFreshOverLevels)
	case copied.ShortFresh > copied.Short:
		return rejectCoverage(CoverageRejectShortFreshOverShort)
	case copied.ShortFresh > copied.Fresh:
		return rejectCoverage(CoverageRejectShortFreshOverFresh)
	case copied.Unusable > copied.Levels:
		return rejectCoverage(CoverageRejectUnusableOverLevels)
	}
	// A reason with no unusable Level, or unusable Levels with no reason, did
	// not come from the evaluator: it records the first reason as it counts.
	if (copied.Unusable == 0) != (copied.UnusableReason == "") {
		return rejectCoverage(CoverageRejectUnusableReasonUnpaired)
	}
	if copied.Short == 0 {
		copied.WorstValid, copied.WorstRequired, copied.Empty = 0, 0, 0
	}
	// An empty window is one whose worst valid count is zero by construction.
	// A pair saying otherwise did not come from counting the same windows, so
	// the count is not describing this run.
	if copied.Empty > 0 && copied.WorstValid != 0 {
		return rejectCoverage(CoverageRejectEmptyWithValidPoints)
	}
	if copied.WorstValid >= copied.WorstRequired {
		// Nothing was actually short in the pair, whatever Short says. Keeping
		// the counts and clearing the pair is the honest half-answer.
		copied.WorstValid, copied.WorstRequired = 0, 0
	}
	// The named windows are short windows of this run: no more of them than
	// were short, none of them full, each with holes that add up to its own
	// shortfall and lists no longer than their totals or the bound. A list
	// that breaks any of these did not come from walking these windows, and
	// the same rule as above applies -- the whole facts go, not the list,
	// because the pair and the names claim to describe the same window.
	if uint32(len(copied.Windows)) > copied.Short {
		return rejectCoverage(CoverageRejectWindowsOverShort)
	}
	if len(copied.Windows) > MaxHistoryWindows {
		return rejectCoverage(CoverageRejectWindowsOverBound)
	}
	windows := make([]HistoryWindowFact, 0, len(copied.Windows))
	for _, window := range copied.Windows {
		switch {
		case window.Series == "":
			return rejectWindow(CoverageRejectWindowUnnamed, window)
		case window.Required == 0:
			return rejectWindow(CoverageRejectWindowRequiredZero, window)
		case window.Valid >= window.Required:
			return rejectWindow(CoverageRejectWindowNotShort, window)
		case window.MissingTotal+window.UnusableTotal != window.Required-window.Valid:
			return rejectWindow(CoverageRejectWindowHoleArithmetic, window)
		case uint32(len(window.Missing)) > window.MissingTotal || uint32(len(window.Unusable)) > window.UnusableTotal ||
			len(window.Missing) > MaxHistoryWindowHoles || len(window.Unusable) > MaxHistoryWindowHoles:
			return rejectWindow(CoverageRejectWindowHoleListOverrun, window)
		case window.GuardReason != "" && !window.Guarded:
			return rejectWindow(CoverageRejectWindowGuardReasonFree, window)
		}
		window.Missing = append([]int64(nil), window.Missing...)
		window.Unusable = append([]int64(nil), window.Unusable...)
		windows = append(windows, window)
	}
	copied.Windows = windows
	if len(copied.Windows) == 0 {
		copied.Windows = nil
	}
	return &copied, nil
}
