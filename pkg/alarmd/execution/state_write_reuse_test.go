// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package execution

import "testing"

// The steady state this measurement exists for is a series whose Level state
// stands still while the history window moves under it. These build that shape
// explicitly rather than deriving it, so a change to the window's contents
// cannot quietly turn the case being tested into a different one.
func steadyLevels() ([]RuntimeLevelStateView, []RuntimeLevelStateMutation) {
	stored := []RuntimeLevelStateView{{
		LevelID: 1, LevelStateCompatibility: "compat-1", HistoryCompleteness: HistoryFull,
		GapReasonCode: "", WarmupRequirementRef: "warm-1", LastProcessedEventTime: 1700,
	}}
	next := []RuntimeLevelStateMutation{{
		LevelID: 1, LevelStateCompatibility: "compat-1", HistoryCompleteness: HistoryFull,
		GapReasonCode: "", WarmupRequirementRef: "warm-1", LastProcessedEventTime: 1700,
	}}
	return stored, next
}

func TestWriteReuseReportsUnobservedWhenNothingIsStored(t *testing.T) {
	stored, next := steadyLevels()
	view := RuntimeStateView{Levels: stored}
	mutation := StateMutation{MutationDigest: "digest-a", Levels: next}
	// The digests differ here on purpose: with nothing stored there is no
	// comparison to make, and reporting that as "changed" would let a starting
	// worker's warm-up read as a series that is never reusable.
	if got, _ := ClassifyStateWriteReuse(view, mutation); got != StateWriteReuseUnobserved {
		t.Fatalf("no stored digest classified as %q, want %q", got, StateWriteReuseUnobserved)
	}
}

func TestWriteReuseReportsIdenticalWhenTheWholeBlobRepeats(t *testing.T) {
	stored, next := steadyLevels()
	view := RuntimeStateView{PersistedMutationDigest: "digest-a", Levels: stored}
	mutation := StateMutation{MutationDigest: "digest-a", Levels: next}
	if got, _ := ClassifyStateWriteReuse(view, mutation); got != StateWriteReuseIdentical {
		t.Fatalf("repeated blob classified as %q, want %q", got, StateWriteReuseIdentical)
	}
}

func TestWriteReuseReportsDecisionStableWhenOnlyTheWindowMoved(t *testing.T) {
	stored, next := steadyLevels()
	view := RuntimeStateView{
		PersistedMutationDigest: "digest-a", Levels: stored,
		History: []StateHistoryPoint{{RecordID: "r1", SourceTime: 1700}},
	}
	mutation := StateMutation{
		MutationDigest: "digest-b", Levels: next,
		Points: []StateHistoryPoint{{RecordID: "r2", SourceTime: 1760}},
	}
	if got, _ := ClassifyStateWriteReuse(view, mutation); got != StateWriteReuseDecisionStable {
		t.Fatalf("moved window classified as %q, want %q", got, StateWriteReuseDecisionStable)
	}
}

// A series that never leaves history warming is the second steady state, and it
// reaches its write through a different branch than a recovering one. It must
// classify on the same terms: what decides the class is whether the Level state
// moved, not which completeness it is sitting in.
func TestWriteReuseReadsAStandingWarmingSeriesTheSameWay(t *testing.T) {
	stored := []RuntimeLevelStateView{{
		LevelID: 1, LevelStateCompatibility: "compat-1", HistoryCompleteness: HistoryWarming,
		GapReasonCode: "HISTORY_WARMING", WarmupRequirementRef: "warm-1", LastProcessedEventTime: 1700,
	}}
	next := []RuntimeLevelStateMutation{{
		LevelID: 1, LevelStateCompatibility: "compat-1", HistoryCompleteness: HistoryWarming,
		GapReasonCode: "HISTORY_WARMING", WarmupRequirementRef: "warm-1", LastProcessedEventTime: 1700,
	}}
	view := RuntimeStateView{PersistedMutationDigest: "digest-a", Levels: stored}
	mutation := StateMutation{MutationDigest: "digest-b", Levels: next}
	if got, _ := ClassifyStateWriteReuse(view, mutation); got != StateWriteReuseDecisionStable {
		t.Fatalf("standing warming series classified as %q, want %q", got, StateWriteReuseDecisionStable)
	}
}

// LastProcessedEventTime advancing is the difference between a series that
// stood still and one that processed a point. If this field were left out of
// the comparison the classifier would report a reusable write for a series that
// moved, which is the one answer that must never be given.
func TestWriteReuseReportsChangedWhenTheProcessedTimeAdvances(t *testing.T) {
	stored, next := steadyLevels()
	next[0].LastProcessedEventTime = 1760
	view := RuntimeStateView{PersistedMutationDigest: "digest-a", Levels: stored}
	mutation := StateMutation{MutationDigest: "digest-b", Levels: next}
	if got, _ := ClassifyStateWriteReuse(view, mutation); got != StateWriteReuseChanged {
		t.Fatalf("advanced processed time classified as %q, want %q", got, StateWriteReuseChanged)
	}
}

func TestWriteReuseReportsChangedWhenCompletenessMoves(t *testing.T) {
	stored, next := steadyLevels()
	next[0].HistoryCompleteness = HistoryWarming
	view := RuntimeStateView{PersistedMutationDigest: "digest-a", Levels: stored}
	mutation := StateMutation{MutationDigest: "digest-b", Levels: next}
	if got, _ := ClassifyStateWriteReuse(view, mutation); got != StateWriteReuseChanged {
		t.Fatalf("moved completeness classified as %q, want %q", got, StateWriteReuseChanged)
	}
}

func TestWriteReuseReportsChangedWhenTheSeriesGuardAppears(t *testing.T) {
	stored, next := steadyLevels()
	view := RuntimeStateView{PersistedMutationDigest: "digest-a", Levels: stored}
	mutation := StateMutation{
		MutationDigest: "digest-b", Levels: next,
		SeriesGuard: &StateGuardFact{Status: HistoryWarming, ReasonCode: "HISTORY_WARMING", WarmupRequirementRef: "warm-1"},
	}
	if got, _ := ClassifyStateWriteReuse(view, mutation); got != StateWriteReuseChanged {
		t.Fatalf("appearing series guard classified as %q, want %q", got, StateWriteReuseChanged)
	}
}

func TestWriteReuseReportsChangedWhenALevelIsAdded(t *testing.T) {
	stored, next := steadyLevels()
	next = append(next, RuntimeLevelStateMutation{
		LevelID: 2, LevelStateCompatibility: "compat-2", HistoryCompleteness: HistoryFull,
		WarmupRequirementRef: "warm-2", LastProcessedEventTime: 1700,
	})
	view := RuntimeStateView{PersistedMutationDigest: "digest-a", Levels: stored}
	mutation := StateMutation{MutationDigest: "digest-b", Levels: next}
	if got, _ := ClassifyStateWriteReuse(view, mutation); got != StateWriteReuseChanged {
		t.Fatalf("added Level classified as %q, want %q", got, StateWriteReuseChanged)
	}
}

// The reading that prompted this: 1,917,819 admitted writes, every one of them
// changed, none decision_stable. The reason exists to say which field moved,
// because "changed" covers two situations that call for opposite actions -- a
// decision that really moved, or a field that should never have counted as part
// of the decision.
func TestWriteReuseNamesTheProcessedTimeWhenOnlyItAdvances(t *testing.T) {
	stored, next := steadyLevels()
	next[0].LastProcessedEventTime = 1760
	view := RuntimeStateView{PersistedMutationDigest: "digest-a", Levels: stored}
	mutation := StateMutation{MutationDigest: "digest-b", Levels: next}
	class, reason := ClassifyStateWriteReuse(view, mutation)
	if class != StateWriteReuseChanged || reason != StateWriteChangeProcessedTime {
		t.Fatalf("class/reason = %q/%q, want %q/%q", class, reason, StateWriteReuseChanged, StateWriteChangeProcessedTime)
	}
}

// Reasons are first differences in a fixed order, not a count of what differs.
// Read as "how many mutations had this field move" they would double-count a
// mutation that moved several, and the breakdown would stop adding up to the
// changed class.
func TestWriteReuseReportsTheFirstDifferenceNotEveryDifference(t *testing.T) {
	stored, next := steadyLevels()
	next[0].HistoryCompleteness = HistoryWarming
	next[0].LastProcessedEventTime = 1760
	view := RuntimeStateView{PersistedMutationDigest: "digest-a", Levels: stored}
	mutation := StateMutation{MutationDigest: "digest-b", Levels: next}
	_, reason := ClassifyStateWriteReuse(view, mutation)
	if reason != StateWriteChangeCompleteness {
		t.Fatalf("reason = %q, want %q: completeness is compared before the processed time",
			reason, StateWriteChangeCompleteness)
	}
}

func TestWriteReuseNamesTheSeriesGuardAndTheLevelCount(t *testing.T) {
	stored, next := steadyLevels()
	view := RuntimeStateView{PersistedMutationDigest: "digest-a", Levels: stored}

	guarded := StateMutation{
		MutationDigest: "digest-b", Levels: next,
		SeriesGuard: &StateGuardFact{Status: HistoryWarming, ReasonCode: "HISTORY_WARMING", WarmupRequirementRef: "warm-1"},
	}
	if _, reason := ClassifyStateWriteReuse(view, guarded); reason != StateWriteChangeSeriesGuard {
		t.Fatalf("series guard reason = %q, want %q", reason, StateWriteChangeSeriesGuard)
	}

	extra := append(append([]RuntimeLevelStateMutation(nil), next...), RuntimeLevelStateMutation{
		LevelID: 2, LevelStateCompatibility: "compat-2", HistoryCompleteness: HistoryFull,
		WarmupRequirementRef: "warm-2", LastProcessedEventTime: 1700,
	})
	if _, reason := ClassifyStateWriteReuse(view, StateMutation{MutationDigest: "digest-c", Levels: extra}); reason != StateWriteChangeLevelCount {
		t.Fatalf("added Level reason = %q, want %q", reason, StateWriteChangeLevelCount)
	}
}

// A reason is only meaningful for the changed class. Reporting one for the
// others would put a field name beside a comparison that found no difference.
func TestWriteReuseReportsNoReasonForTheOtherClasses(t *testing.T) {
	stored, next := steadyLevels()
	for name, pair := range map[string]struct {
		view     RuntimeStateView
		mutation StateMutation
	}{
		"unobserved": {RuntimeStateView{Levels: stored}, StateMutation{MutationDigest: "digest-a", Levels: next}},
		"identical":  {RuntimeStateView{PersistedMutationDigest: "digest-a", Levels: stored}, StateMutation{MutationDigest: "digest-a", Levels: next}},
		"decision_stable": {
			RuntimeStateView{PersistedMutationDigest: "digest-a", Levels: stored},
			StateMutation{MutationDigest: "digest-b", Levels: next, Points: []StateHistoryPoint{{RecordID: "r", SourceTime: 1}}},
		},
	} {
		if _, reason := ClassifyStateWriteReuse(pair.view, pair.mutation); reason != StateWriteChangeNone {
			t.Fatalf("%s reported reason %q, want %q", name, reason, StateWriteChangeNone)
		}
	}
}
