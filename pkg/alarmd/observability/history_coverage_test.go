// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import "testing"

// Counts that could not have come from one set of windows are dropped, not
// clamped.
//
// Every bound here holds by construction where the counts are made: the short
// windows are a subset of the summarised ones, the empty ones a subset of the
// short, the fresh short ones a subset of both the short and the fresh. A value
// that breaks one did not come from counting these windows, and the numbers
// beside it are as suspect as the one that broke.
//
// Clamping is the tempting fix and the wrong one: it leaves a plausible number
// on the page, and every count on that row is then read as measured. Dropping
// the facts leaves the absence visible, which is the only honest form of "these
// did not describe one round".
func TestCoverageCountsThatCannotDescribeOneRoundAreDropped(t *testing.T) {
	cases := []struct {
		name  string
		facts HistoryCoverageFacts
	}{
		{"more short than summarised", HistoryCoverageFacts{Levels: 2, Short: 3}},
		{"more empty than short", HistoryCoverageFacts{Levels: 4, Short: 1, Empty: 2}},
		{"more guarded than summarised", HistoryCoverageFacts{Levels: 2, Short: 1, Guarded: 3}},
		// Fresh is counted over the same windows as Levels.
		{"more fresh than summarised", HistoryCoverageFacts{Levels: 2, Short: 1, Fresh: 3}},
		// And the short fresh ones over the same windows as Short.
		{"more short-fresh than short", HistoryCoverageFacts{Levels: 4, Short: 1, Fresh: 3, ShortFresh: 2}},
		// A short fresh window is a fresh one, so it cannot outnumber them
		// either. This is the bound that catches a translation crossing the two
		// counts over, which would otherwise read as a churning strategy.
		{"more short-fresh than fresh", HistoryCoverageFacts{Levels: 4, Short: 4, Fresh: 1, ShortFresh: 2}},
		// Nothing short and a fresh short window is the same contradiction with
		// the subset empty, and it is the one that matters most: it is the shape
		// a stale value left over from an earlier round would take, and a reader
		// would act on it by editing a strategy whose windows all just filled.
		{"short-fresh with nothing short", HistoryCoverageFacts{Levels: 4, Short: 0, Fresh: 2, ShortFresh: 1}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			facts := testCase.facts
			if got := normalizeHistoryCoverageFacts(&facts); got != nil {
				t.Fatalf("normalised to %+v, want the facts dropped: a count that could not have "+
					"come from these windows makes every number beside it unreadable, and a "+
					"plausible-looking clamp is what hides that", got)
			}
		})
	}
}

// A round where every window filled is still a measurement, and the counts that
// are not about the short ones have to survive it.
//
// Fresh is counted over every window, so an object whose series were all new
// this round and whose windows all filled is a real and readable state -- a Plan
// that has just been re-keyed looks exactly like that. Clearing it here because
// its sibling ShortFresh is about the short windows would put a zero on a round
// that measured four, and the run counter downstream would start from a round
// that was never zero.
func TestCountsNotAboutShortWindowsSurviveARoundWithNoneShort(t *testing.T) {
	facts := HistoryCoverageFacts{Levels: 9, Short: 0, Empty: 0, WorstValid: 3, WorstRequired: 9,
		Guarded: 2, Fresh: 4, ShortFresh: 0}
	got := normalizeHistoryCoverageFacts(&facts)
	if got == nil {
		t.Fatal("facts dropped: a round where every window filled is a measurement, and its absence " +
			"reads as a round nobody measured")
	}
	if got.WorstValid != 0 || got.WorstRequired != 0 || got.Empty != 0 {
		t.Errorf("worst pair %d/%d and empty %d survived a round with nothing short",
			got.WorstValid, got.WorstRequired, got.Empty)
	}
	// Fresh is not about the short windows and must survive. An object whose
	// every series is new this round and whose windows all filled is a real
	// state -- a Plan that has just been re-keyed -- and clearing it here would
	// make the next round's run counter start from a round that was never zero.
	if got.Fresh != 4 {
		t.Errorf("fresh = %d, want 4 kept: it is counted over every window, not over the short ones",
			got.Fresh)
	}
	if got.Guarded != 2 {
		t.Errorf("guarded = %d, want 2 kept for the same reason", got.Guarded)
	}
}
