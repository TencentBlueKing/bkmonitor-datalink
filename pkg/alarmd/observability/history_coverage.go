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

func normalizeHistoryCoverageFacts(facts *HistoryCoverageFacts) *HistoryCoverageFacts {
	if facts == nil {
		return nil
	}
	copied := *facts
	// A Short above Levels cannot have come from counting the same windows,
	// so the pair is not describing one run and nothing derived from it can be
	// trusted. Clamping would keep a plausible-looking number; dropping the
	// facts leaves the absence visible.
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
	if copied.Levels == 0 || copied.Short > copied.Levels || copied.Empty > copied.Short ||
		copied.Guarded > copied.Levels || copied.Fresh > copied.Levels ||
		copied.ShortFresh > copied.Short || copied.ShortFresh > copied.Fresh {
		return nil
	}
	if copied.Short == 0 {
		copied.WorstValid, copied.WorstRequired, copied.Empty = 0, 0, 0
	}
	// An empty window is one whose worst valid count is zero by construction.
	// A pair saying otherwise did not come from counting the same windows, so
	// the count is not describing this run.
	if copied.Empty > 0 && copied.WorstValid != 0 {
		return nil
	}
	if copied.WorstValid >= copied.WorstRequired {
		// Nothing was actually short in the pair, whatever Short says. Keeping
		// the counts and clearing the pair is the honest half-answer.
		copied.WorstValid, copied.WorstRequired = 0, 0
	}
	return &copied
}
