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

// The fold answers by the set, not by the order it is read in.
//
// This is the property the whole change rests on. Three derivations used to
// pick a reason out of the same inputs by three different rules, and a scope
// whose inputs failed differently made them disagree -- which failed the
// Plan's evaluation every round. One order called from all three only helps
// if it gives one answer, and "one answer" has to mean independent of which
// input the caller happened to walk first.
func TestTheFoldAnswersByTheSetNotTheOrder(t *testing.T) {
	for _, test := range []struct {
		name    string
		reasons []string
		want    string
	}{
		{name: "unavailable outranks timeout", reasons: []string{ReasonQueryUnavailable, ReasonQueryTimeout}, want: ReasonQueryUnavailable},
		{name: "and the other way round", reasons: []string{ReasonQueryTimeout, ReasonQueryUnavailable}, want: ReasonQueryUnavailable},
		{name: "partial is the weakest", reasons: []string{ReasonQueryPartial, ReasonQueryTimeout}, want: ReasonQueryTimeout},
		{name: "partial alone is itself", reasons: []string{ReasonQueryPartial}, want: ReasonQueryPartial},
		{name: "budget outranks drift", reasons: []string{ReasonConfigDrift, ReasonExecutionBudgetExhausted}, want: ReasonExecutionBudgetExhausted},
		{name: "empty reasons are not choices", reasons: []string{"", ReasonQueryPartial, ""}, want: ReasonQueryPartial},
		{name: "nothing folds to nothing", reasons: nil, want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := FoldGapReason(test.reasons); got != test.want {
				t.Fatalf("FoldGapReason(%v) = %q, want %q", test.reasons, got, test.want)
			}
			reversed := make([]string, 0, len(test.reasons))
			for index := len(test.reasons) - 1; index >= 0; index-- {
				reversed = append(reversed, test.reasons[index])
			}
			if got := FoldGapReason(reversed); got != test.want {
				t.Fatalf("FoldGapReason(%v) = %q, want %q: the fold read the same set the other way "+
					"round and answered differently, so a retry can write a marker the first attempt "+
					"would not have", reversed, got, test.want)
			}
		})
	}
}

// A reason this order does not list wins, and two of them still fold
// deterministically.
//
// Ranking an unlisted reason last would let a failure mode introduced after
// this table was written disappear behind a milder one that happens to share
// the scope -- on every round, with the table looking complete.
func TestAnUnrankedReasonIsPreferredAndStillDeterministic(t *testing.T) {
	const newer = "SOMETHING_NOBODY_RANKED"
	if got := FoldGapReason([]string{ReasonQueryUnavailable, newer}); got != newer {
		t.Fatalf("FoldGapReason() = %q, want the unranked reason: ranked last it would be invisible "+
			"behind a reason this table happens to know", got)
	}
	const other = "ALSO_UNRANKED"
	first := FoldGapReason([]string{newer, other})
	second := FoldGapReason([]string{other, newer})
	if first != second || first != other {
		t.Fatalf("two unranked reasons folded to %q and %q; the answer must not depend on the order",
			first, second)
	}
}

// Every reason a query result can carry is ranked.
//
// Derived from the reason catalogue rather than listed here, because a
// hand-written list is a second statement of the same set and drifts from it
// silently: the day somebody adds a query reason, this fails and they rank it,
// instead of it folding as unranked for ever with nothing saying so.
func TestEveryQueryResultReasonIsRanked(t *testing.T) {
	ranked := make(map[string]struct{}, len(GapReasonFoldOrder))
	for _, reason := range GapReasonFoldOrder {
		if _, duplicate := ranked[reason]; duplicate {
			t.Fatalf("%q is ranked twice, so the order does not say which rank it has", reason)
		}
		if !IsKnownReasonV2(reason) {
			t.Fatalf("%q is ranked and is not a reason this build declares", reason)
		}
		ranked[reason] = struct{}{}
	}
	for _, definition := range ReasonCatalogV2() {
		if !definition.Domains.Has(ReasonDomainQueryResult) {
			continue
		}
		if _, listed := ranked[definition.Code]; !listed {
			t.Fatalf("%q can be a query result's reason and is not ranked: it would fold as unranked, "+
				"outranking everything, which is loud but is not a decision anybody made", definition.Code)
		}
	}
}
