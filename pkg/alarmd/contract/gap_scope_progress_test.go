// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import "testing"

// Where the count stands, for every pair of numbers a marker can hold.
//
// The zero requirement and the count past its requirement are in here because
// a total function is the point: this decides a metric label, and a pair that
// fell through would either panic or produce a word the bound does not hold.
func TestGapScopeProgressIsTotalOverTheMarkersNumbers(t *testing.T) {
	for _, test := range []struct {
		name               string
		observed, required uint32
		want               string
	}{
		{"nothing counted yet", 0, 5, GapScopeProgressNone},
		{"part of the way", 3, 5, GapScopeProgressPartial},
		{"one short", 4, 5, GapScopeProgressPartial},
		{"at the requirement", 5, 5, GapScopeProgressReady},
		{"past the requirement", 6, 5, GapScopeProgressReady},
		// A scope with nothing to count toward still reads as none rather than
		// as ready: no round has counted, and a reader told "ready" would go
		// looking for a guard that should have released.
		{"no requirement and no count", 0, 0, GapScopeProgressNone},
		{"no requirement with a count", 1, 0, GapScopeProgressReady},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := GapScopeProgress(test.observed, test.required); got != test.want {
				t.Fatalf("GapScopeProgress(%d, %d) = %q, want %q",
					test.observed, test.required, got, test.want)
			}
		})
	}
}

// Every value the function returns is one the published list holds.
//
// The list is what pre-creates the metric's series and bounds its cardinality,
// so a word the function can return and the list does not hold is a series
// nobody created and a bound that is not one. Driven off the function rather
// than compared with a second literal list.
func TestEveryProgressTheFunctionReturnsIsPublished(t *testing.T) {
	published := make(map[string]bool, len(GapScopeProgressValues))
	for _, value := range GapScopeProgressValues {
		published[value] = true
	}
	for observed := uint32(0); observed <= 4; observed++ {
		for required := uint32(0); required <= 4; required++ {
			if got := GapScopeProgress(observed, required); !published[got] {
				t.Fatalf("GapScopeProgress(%d, %d) = %q, which GapScopeProgressValues does not hold",
					observed, required, got)
			}
		}
	}
}

// Every reason the fold can put on a scope is a reason the scope vocabulary
// names.
//
// The two are derived from the same catalogue, and this is what keeps them
// derived from it rather than from each other: a reason ranked by the fold but
// missing here would reach the metric as other, and a rising other reads as
// "somebody added a failure mode" when it would really be this list lagging.
func TestEveryFoldableGapReasonIsNamedInTheScopeVocabulary(t *testing.T) {
	named := make(map[string]bool, len(GapScopeReasons()))
	for _, reason := range GapScopeReasons() {
		named[reason] = true
	}
	for _, reason := range GapReasonFoldOrder {
		if !named[reason] {
			t.Fatalf("the fold ranks %s and the scope vocabulary does not name it, so a scope holding "+
				"it counts as other", reason)
		}
	}
	// And the one a scope carries that no query result produces.
	if !named[ReasonHistoryWarming] {
		t.Fatalf("a warming scope's own reason is not named: %v", GapScopeReasons())
	}
	// A reason from outside is bounded rather than passed through, because the
	// marker it came off may have been written by another build.
	if got := NormalizeGapScopeReason("SOMETHING_A_LATER_BUILD_ADDED"); got != GapScopeReasonOther {
		t.Fatalf("an unnamed reason normalised to %q, want %q", got, GapScopeReasonOther)
	}
	if got := NormalizeGapScopeReason(ReasonConfigDrift); got != ReasonConfigDrift {
		t.Fatalf("a named reason was normalised away: %q", got)
	}
}
