// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"strings"
	"testing"
	"time"
)

// Summarize has two early returns that give up before walking a single
// position, and both report the same thing as a window that was walked and
// found empty: HISTORY_WARMING with zero valid positions. Downstream that is
// counted as an empty window, and the page renders empty windows as 取不到数据 --
// go and look at whether the metric stopped, whether collection broke.
//
// One of those refusals is not that at all. requiredPositions exceeding what
// the state retains is a configuration asking for a window the store can never
// hold: it cannot resolve itself, no amount of looking at the data helps, and
// the wording sends whoever reads it somewhere with nothing to find. A
// permanent failure wearing a transient symptom's clothes is the longest-lived
// kind there is.
//
// It is also, right now, unreachable -- and that is what this pins, because the
// reason it is unreachable lives in three files that know nothing about each
// other:
//
//   - Align refuses to build a window at all when RetentionPoints is below
//     RequiredPoints, when the interval is not a positive whole number of
//     seconds, or when the retention horizon would overflow. So a window that
//     exists has already satisfied every condition in that first refusal except
//     the comparison against the argument.
//   - strategy/compiler.go derives RetentionPoints from RequiredDetectHistoryPoints
//     and never below it: equal for a Level the recovery slack does not reach,
//     larger for one it does. Ordered by construction, which is what this
//     comparison needs. It was equality until decision-022 R5 began retaining a
//     slack past the required window, and the comparison held across that change
//     because it was written as an ordering and not as an equality.
//   - Both callers pass RequiredDetectHistoryPoints as the argument, so the
//     comparison is the required window against a retention that is at least
//     as large.
//
// Any one of those three changing turns a dead branch into a live one that
// reports a permanent misconfiguration as a data outage, and nothing in the
// refusal itself would notice. This fails first instead.
//
// WindowStart is the observable: a walked window sets it to endTime minus the
// span, and both refusals leave it zero. That is only a signature for an
// endTime large enough that the span cannot reach back past zero, which every
// case here uses and real records always are.
func TestASummaryWalksEveryWindowAlignAccepts(t *testing.T) {
	const endTime = int64(1_700_000_400)
	fingerprint := strings.Repeat("a", 64)
	for _, requirement := range []LevelRequirement{
		// The smallest window, which the record fills by itself.
		{LevelID: 1, DetectFingerprint: fingerprint, RequiredPoints: 1, RetentionPoints: 1,
			EvaluationInterval: time.Minute},
		// One of the two shapes the compiler produces: retention equal to the
		// requirement, for a Level the recovery slack does not reach - its
		// window is shorter than the hole tolerance, so retaining more buys it
		// nothing.
		{LevelID: 1, DetectFingerprint: fingerprint, RequiredPoints: 9, RetentionPoints: 9,
			EvaluationInterval: time.Minute},
		// The other: retention above the requirement, for a Level the slack does
		// reach. Both are compiler output since decision-022 R5, so the refusal
		// has to be an ordering and not an equality, and both shapes are here
		// rather than one standing in for the other.
		{LevelID: 1, DetectFingerprint: fingerprint, RequiredPoints: 9, RetentionPoints: 30,
			EvaluationInterval: time.Minute},
		// A long window on a long interval: the largest span these limits allow
		// anywhere near production, and the one closest to reaching back past
		// the epoch.
		{LevelID: 1, DetectFingerprint: fingerprint, RequiredPoints: 32, RetentionPoints: 32,
			EvaluationInterval: 10 * time.Minute},
	} {
		window, err := NewWindow([]LevelRequirement{requirement})
		if err != nil {
			t.Fatalf("Align rejected %+v: %v -- this table is meant to hold only requirements it "+
				"accepts, because the claim is about the windows that exist", requirement, err)
		}
		view, found := window.History(requirement.LevelID)
		if !found {
			t.Fatalf("no history view for level %d on a window built from its own requirement",
				requirement.LevelID)
		}
		summary := view.Summarize(endTime, requirement.RequiredPoints)
		if summary.WindowStart == 0 {
			t.Errorf("Summarize refused to walk %+v: it returns HISTORY_WARMING with no valid "+
				"position, which is counted as an empty window and rendered as 取不到数据 -- a "+
				"configuration the store can never satisfy, reported as data that stopped arriving",
				requirement)
		}
		if summary.RequiredPositions != requirement.RequiredPoints {
			t.Errorf("summary asked for %d positions, want %d", summary.RequiredPositions,
				requirement.RequiredPoints)
		}
	}
}

// Align is the guarantee itself, so it is checked directly rather than through
// the windows it lets through.
//
// The test above can only walk requirements Align accepts, so loosening Align
// does not make it fail -- nothing it builds has retention below its
// requirement, and nothing would until some caller also started asking for one.
// That is two of the three guarantees pinned and the third merely assumed, and
// the assumed one is the load-bearing one: it is what stops a Level whose
// retention was configured below its window from reaching Summarize at all.
//
// Refusing here is also the right place for it. A requirement the store cannot
// satisfy should fail where the window is built, loudly, with the Level named --
// not two layers later as a summary that looks like a series whose data stopped.
func TestAlignRefusesARequirementTheStoreCannotSatisfy(t *testing.T) {
	fingerprint := strings.Repeat("a", 64)
	for _, testCase := range []struct {
		name        string
		requirement LevelRequirement
	}{
		{"retains fewer points than it requires", LevelRequirement{LevelID: 1,
			DetectFingerprint: fingerprint, RequiredPoints: 9, RetentionPoints: 8,
			EvaluationInterval: time.Minute}},
		{"retains nothing", LevelRequirement{LevelID: 1, DetectFingerprint: fingerprint,
			RequiredPoints: 9, RetentionPoints: 0, EvaluationInterval: time.Minute}},
		{"requires nothing", LevelRequirement{LevelID: 1, DetectFingerprint: fingerprint,
			RequiredPoints: 0, RetentionPoints: 9, EvaluationInterval: time.Minute}},
		{"has no evaluation interval", LevelRequirement{LevelID: 1, DetectFingerprint: fingerprint,
			RequiredPoints: 9, RetentionPoints: 9}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := NewWindow([]LevelRequirement{testCase.requirement}); err == nil {
				t.Fatalf("Align accepted %+v: Summarize would then refuse to walk it and report "+
					"HISTORY_WARMING with no valid position, which is counted as an empty window "+
					"and rendered as data that stopped arriving -- a requirement the store can "+
					"never satisfy, told to the reader as a collection problem",
					testCase.requirement)
			}
		})
	}
}

// And zero means "use the requirement's own", not "no window".
//
// The argument is normalised before the refusal is tested, so a caller passing
// zero gets the Level's own requirement rather than a window that declines to
// judge. Worth pinning because the refusal reads as though zero reaches it, and
// a reader checking whether that branch is live would conclude it is.
func TestASummaryAskedForZeroPositionsUsesTheLevelsOwnRequirement(t *testing.T) {
	requirement := LevelRequirement{LevelID: 1, DetectFingerprint: strings.Repeat("a", 64),
		RequiredPoints: 9, RetentionPoints: 9, EvaluationInterval: time.Minute}
	window, err := NewWindow([]LevelRequirement{requirement})
	if err != nil {
		t.Fatal(err)
	}
	view, found := window.History(requirement.LevelID)
	if !found {
		t.Fatal("no history view")
	}
	summary := view.Summarize(1_700_000_400, 0)
	if summary.RequiredPositions != 9 || summary.WindowStart == 0 {
		t.Fatalf("summary = %+v, want the Level's own 9 positions walked: zero is a caller "+
			"declining to override, not a window that cannot be formed", summary)
	}
}
