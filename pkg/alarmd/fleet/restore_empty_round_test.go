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
	"testing"
	"time"
)

// A restart restores an object's run of empty rounds from its record; what
// the last of them said about its emptiness comes back with it, so a daily
// event count at rest reads quiet, and an object whose series were all
// outside its target reads so, from the moment the replica that took it over
// restores it - not a day later at its first round.
//
// The record's word is the Plan's and the round's: it is trusted only under
// the content the owner runs now. Under other content - a target or a
// recovery window edited since - or when either side does not name its
// content, the row reads as it did before the record kept the word: the
// strategy's empty line, cause unknown, until the first round says.
func TestARestoredEmptyRunKeepsWhatItsLastRoundSaidUnderTheSameContent(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	lastSlot := now.Add(-24 * time.Hour)
	since := now.Add(-20 * 24 * time.Hour)
	restore := func(queryGroup string, empty *RestoredEmptyRound, contentNow string) {
		t.Helper()
		if !tracker.Restore(queryGroup, RestoredState{
			LastCompletion: "FULL_EMPTY_COMPLETED", NextSlot: now.Add(time.Hour),
			LastRound: &RestoredRound{Slot: lastSlot, CompletedAt: lastSlot.Add(2 * time.Second), Kind: "FULL_EMPTY_COMPLETED",
				Empty: empty},
			EmptyRunSince: since, ContentScopeNow: contentNow,
		}, now, 0) {
			t.Fatalf("%s was not restored", queryGroup)
		}
	}
	restore("qg-at-rest", &RestoredEmptyRound{Quiet: true, ContentScope: "content-a"}, "content-a")
	restore("qg-outside", &RestoredEmptyRound{EmptiedByTarget: true, ContentScope: "content-a"}, "content-a")
	restore("qg-edited", &RestoredEmptyRound{Quiet: true, ContentScope: "content-a"}, "content-b")
	restore("qg-owner-unnamed", &RestoredEmptyRound{Quiet: true, ContentScope: "content-a"}, "")
	restore("qg-record-unnamed", &RestoredEmptyRound{Quiet: true}, "")
	restore("qg-no-word", nil, "content-a")

	rows := tracker.NoData()
	if _, listed := rowsOfKind(rows, KindQuiet)["qg-at-rest"]; !listed {
		t.Errorf("an event count whose record says it was at rest, under the content its owner runs, is not quiet on restore: %+v", rows)
	}
	every := rowsOfKind(rows, KindEmptyEveryRound)
	outside, listed := every["qg-outside"]
	if !listed || outside.EmptyEveryRound == nil || outside.EmptyEveryRound.Cause != EmptyEveryRoundCauseOutsideTarget {
		t.Errorf("an object whose record says its series were all outside the target reads %+v, want the empty line with the target named", outside)
	} else {
		list := []Anomaly{outside}
		Attribute(list, now)
		if list[0].Finding.Check != CheckEmptyAfterTarget {
			t.Errorf("check %s, want %s", list[0].Finding.Check, CheckEmptyAfterTarget)
		}
	}
	for _, queryGroup := range []string{"qg-edited", "qg-owner-unnamed", "qg-record-unnamed", "qg-no-word"} {
		row, listed := every[queryGroup]
		if !listed || row.EmptyEveryRound == nil || row.EmptyEveryRound.Cause != EmptyEveryRoundCauseUnknown {
			t.Errorf("%s reads %+v (listed %t), want the empty line with the cause unknown, as before the record kept its word", queryGroup, row, listed)
		}
	}
}
