// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import "sort"

// How far a gap scope has got toward releasing, as a bounded word.
//
// Derived from the marker's own two numbers and nothing else. The question a
// reader asks is "is this guard moving", and the obvious way to answer it --
// compare this round's count with the last one -- needs a memory of the last
// round that no process here has: a Query Group changes owner, and the new
// owner has no previous value for a scope it has never seen. That reading is
// wrong exactly after a rollout, which is when somebody is looking.
//
// So the label says where the count stands and the time series says whether it
// is moving: a scope reported none on every round for an hour is a guard that
// has had no complete round in an hour, and that is visible as a rate without
// anybody remembering anything.
const (
	// GapScopeProgressNone is a scope with no complete round since it was
	// raised or last reset. Any round that is not complete reopens the scope
	// and puts the count back to zero, so this is not "early", it is "nothing
	// has counted yet".
	GapScopeProgressNone = "none"
	// GapScopeProgressPartial is a scope part of the way to its requirement.
	GapScopeProgressPartial = "partial"
	// GapScopeProgressReady is a scope whose count has reached its
	// requirement. A warming scope cannot persist in this state -- the store
	// refuses to write one -- so a reading here is either a gapped scope that
	// kept its count across a same-Slot extension, or a scope that should have
	// been released and was not. The two are worth telling apart from a scope
	// that is merely part way, which is why this is its own word rather than
	// folded into partial.
	GapScopeProgressReady = "ready"
)

// GapScopeProgressValues is every value the progress label takes, for the
// partition to pre-create and for a reader to bound the family by.
var GapScopeProgressValues = []string{
	GapScopeProgressNone, GapScopeProgressPartial, GapScopeProgressReady,
}

// GapScopeProgress names where one scope's count stands against its
// requirement.
//
// Total over every pair of numbers, including the ones that should not occur:
// a requirement of zero with no count reads as none, which is what a scope
// nothing has counted for is, and a count past its requirement reads as ready
// rather than falling through to a fourth word nobody would have a meaning
// for.
func GapScopeProgress(observed, required uint32) string {
	switch {
	case observed == 0:
		return GapScopeProgressNone
	case observed < required:
		return GapScopeProgressPartial
	default:
		return GapScopeProgressReady
	}
}

// GapScopeReasonOther is where a reason this build does not name lands.
//
// The reason on a scope comes off a persisted marker, which another build may
// have written, so the input is open however closed this build's own set is.
// A catch-all is what keeps the label bounded; a rising one is a reason
// somebody added without naming it here, which is a finding rather than a
// gap in the chart.
const GapScopeReasonOther = "other"

// GapScopeReasons is every reason this build can put on a gap scope.
//
// Derived from the reason catalogue rather than written out: a gap scope's
// reason is the fold of its incomplete inputs' reasons, and those are exactly
// the reasons a query result may carry. The same derivation is what found
// READINESS_BUDGET_INVALID for the fold order, and it is the reason a source
// added later cannot quietly go unnamed here.
//
// HISTORY_WARMING is added because it is the one reason a scope carries that
// no query result produced: a warming scope is opened by the evaluation, not
// by an input that failed.
//
// GapScopeQueryFreeReasons is the other producer, and leaving it out is what
// sent 46.6% of the counter to "other" on the first deployment that read it:
// a Slot that never queried writes its own reason straight onto the scope
// rather than folding one out of inputs it does not have, and neither of the
// two it writes is in any query result.
func GapScopeReasons() []string {
	reasons := make([]string, 0, len(GapReasonFoldOrder)+1+len(GapScopeQueryFreeReasons))
	for _, definition := range ReasonCatalogV2() {
		if definition.Domains&ReasonDomainQueryResult != 0 {
			reasons = append(reasons, definition.Code)
		}
	}
	reasons = append(reasons, ReasonHistoryWarming)
	reasons = append(reasons, GapScopeQueryFreeReasons...)
	sort.Strings(reasons)
	return reasons
}

// GapScopeQueryFreeReasons is what a Slot that ran no query puts on a scope.
//
// These arrive by a different route from every other reason here. The fold
// reduces the reasons of a scope's incomplete inputs, so it can only produce
// reasons a query result carries; a query-free finalization has no inputs to
// fold and writes the reason its own mode requires. The two the modes require
// are these, and execution's own test scans every finalization mode against
// this list rather than trusting it -- a mode added later that carries Plan
// targets fails that scan instead of quietly landing in "other".
var GapScopeQueryFreeReasons = []string{ReasonSnapshotUnavailable, ReasonGapSkipped}

// NormalizeGapScopeReason bounds a scope's reason to the published set.
func NormalizeGapScopeReason(code string) string {
	for _, reason := range GapScopeReasons() {
		if reason == code {
			return code
		}
	}
	return GapScopeReasonOther
}
