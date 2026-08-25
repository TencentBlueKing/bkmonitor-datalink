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
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestWindowAlignsDynamicLevelsWithoutResettingSiblings(t *testing.T) {
	one := requirement(1, "1", 2, 4)
	five := requirement(5, "5", 2, 4)
	window, err := NewWindow([]LevelRequirement{five, one})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	results := mustApply(t, window, []StatePoint{
		point(100, "a", fact(one, LevelFactAnomalous), fact(five, LevelFactNormal)),
		point(160, "b", fact(one, LevelFactNormal), fact(five, LevelFactAnomalous)),
	})
	assertPointStatuses(t, results, PointApplied, PointApplied)
	window.MarkPersisted()

	fiveChanged := requirement(5, "6", 2, 4)
	seven := requirement(7, "7", 2, 4)
	if err := window.Align([]LevelRequirement{seven, fiveChanged, one}); err != nil {
		t.Fatalf("Align() error = %v", err)
	}
	if !window.Changed() {
		t.Fatal("Changed() = false after Level layout change")
	}
	oneHistory, ok := window.History(1)
	if !ok {
		t.Fatal("History(level 1) missing")
	}
	if summary := oneHistory.Summarize(160, 2); summary.Completeness != HistoryFull || summary.ValidPositions != 2 {
		t.Fatalf("level 1 summary = %+v, want retained FULL history", summary)
	}
	for _, levelID := range []uint32{5, 7} {
		history, exists := window.History(levelID)
		if !exists {
			t.Fatalf("History(level %d) missing", levelID)
		}
		if summary := history.Summarize(160, 2); summary.Completeness != HistoryWarming || summary.ValidPositions != 0 {
			t.Fatalf("level %d summary = %+v, want WARMING reset", levelID, summary)
		}
	}
}

func TestWindowReplayIsIdempotentAndConflictingBodyIsIsolated(t *testing.T) {
	one := requirement(1, "1", 1, 3)
	window, err := NewWindow([]LevelRequirement{one})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	original := point(100, "a", fact(one, LevelFactAnomalous))
	assertPointStatuses(t, mustApply(t, window, []StatePoint{original}), PointApplied)
	window.MarkPersisted()

	assertPointStatuses(t, mustApply(t, window, []StatePoint{original}), PointNoop)
	if window.Changed() {
		t.Fatal("Changed() = true after identical replay")
	}

	differentFact := point(100, "a", fact(one, LevelFactNormal))
	result := mustApply(t, window, []StatePoint{differentFact})
	assertPointStatuses(t, result, PointTerminal)
	if result[0].ReasonCode != contract.ReasonRecordIdentityConflict {
		t.Fatalf("ReasonCode = %q, want %q", result[0].ReasonCode, contract.ReasonRecordIdentityConflict)
	}
	differentRecord := point(100, "b", fact(one, LevelFactAnomalous))
	result = mustApply(t, window, []StatePoint{differentRecord})
	assertPointStatuses(t, result, PointTerminal)
	if result[0].ReasonCode != contract.ReasonRecordIdentityConflict {
		t.Fatalf("ReasonCode = %q, want %q", result[0].ReasonCode, contract.ReasonRecordIdentityConflict)
	}
	if window.Changed() {
		t.Fatal("conflicting replay changed persisted state")
	}
}

func TestWindowPositionSummaryIsEventTimeBounded(t *testing.T) {
	one := requirement(1, "1", 3, 4)
	window, err := NewWindow([]LevelRequirement{one})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	results := mustApply(t, window, []StatePoint{
		point(220, "c", fact(one, LevelFactAnomalous)),
		point(100, "a", fact(one, LevelFactNormal)),
		point(160, "b", fact(one, LevelFactAnomalous)),
	})
	assertPointStatuses(t, results, PointApplied, PointApplied, PointApplied)
	history, _ := window.History(1)

	beforeFuture := history.Summarize(160, 2)
	if beforeFuture.Completeness != HistoryFull || beforeFuture.AnomalyCount != 1 || beforeFuture.WindowEnd != 160 {
		t.Fatalf("Summarize(160) = %+v, future point must not leak", beforeFuture)
	}
	full := history.Summarize(220, 3)
	if full.Completeness != HistoryFull || full.ValidPositions != 3 || full.AnomalyCount != 2 {
		t.Fatalf("Summarize(220) = %+v, want FULL 3 positions", full)
	}
}

func TestWindowDistinguishesWarmingGapAndUnavailableLevel(t *testing.T) {
	one := requirement(1, "1", 3, 4)
	five := requirement(5, "5", 3, 4)
	window, err := NewWindow([]LevelRequirement{one, five})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	results := mustApply(t, window, []StatePoint{
		point(100, "a", fact(one, LevelFactNormal), fact(five, LevelFactUnavailable)),
		point(220, "c", fact(one, LevelFactAnomalous), fact(five, LevelFactNormal)),
	})
	assertPointStatuses(t, results, PointApplied, PointApplied)
	oneHistory, _ := window.History(1)
	if summary := oneHistory.Summarize(220, 3); summary.Completeness != HistoryGapped {
		t.Fatalf("level 1 summary = %+v, want GAPPED internal position", summary)
	}
	fiveHistory, _ := window.History(5)
	if summary := fiveHistory.Summarize(220, 3); summary.Completeness != HistoryWarming || summary.ValidPositions != 1 {
		t.Fatalf("level 5 summary = %+v, want WARMING from unavailable history", summary)
	}
}

func TestWindowAcceptsBoundedLatePointAndRejectsExpiredPoint(t *testing.T) {
	one := requirement(1, "1", 2, 3)
	window, err := NewWindow([]LevelRequirement{one})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	assertPointStatuses(t, mustApply(t, window, []StatePoint{
		point(160, "b", fact(one, LevelFactNormal)),
		point(220, "c", fact(one, LevelFactAnomalous)),
	}), PointApplied, PointApplied)
	late := mustApply(t, window, []StatePoint{point(100, "a", fact(one, LevelFactNormal))})
	assertPointStatuses(t, late, PointApplied)
	if late[0].Late != true {
		t.Fatalf("late result = %+v, want Late", late[0])
	}
	expired := mustApply(t, window, []StatePoint{point(40, "d", fact(one, LevelFactNormal))})
	assertPointStatuses(t, expired, PointTerminal)
	if expired[0].ReasonCode != contract.ReasonLateOutOfWindow {
		t.Fatalf("ReasonCode = %q, want %q", expired[0].ReasonCode, contract.ReasonLateOutOfWindow)
	}
}

func TestWindowRejectsFingerprintDriftLocally(t *testing.T) {
	one := requirement(1, "1", 1, 2)
	window, err := NewWindow([]LevelRequirement{one})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	drifted := one
	drifted.DetectFingerprint = strings.Repeat("2", 64)
	if _, err := window.Apply([]StatePoint{point(100, "a", fact(drifted, LevelFactNormal))}); !errors.Is(err, ErrStateInvariant) {
		t.Fatalf("Apply() error = %v, want state invariant", err)
	}
	if window.Changed() {
		t.Fatal("fingerprint drift changed state")
	}
}

func TestWindowInvalidFuturePointDoesNotPruneValidHistory(t *testing.T) {
	one := requirement(1, "1", 2, 3)
	window, err := NewWindow([]LevelRequirement{one})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	assertPointStatuses(t, mustApply(t, window, []StatePoint{
		point(100, "a", fact(one, LevelFactNormal)),
		point(160, "b", fact(one, LevelFactAnomalous)),
	}), PointApplied, PointApplied)

	invalid := point(10_000, "c", fact(one, LevelFactNormal))
	invalid.RecordID = "not-a-digest"
	if _, err := window.Apply([]StatePoint{invalid}); !errors.Is(err, ErrStateInvariant) {
		t.Fatalf("Apply() error = %v, want state invariant", err)
	}
	history, _ := window.History(1)
	if summary := history.Summarize(160, 2); summary.Completeness != HistoryFull || summary.ValidPositions != 2 {
		t.Fatalf("history = %+v, invalid future point must not advance retention", summary)
	}
}

func TestWindowNilReceiverIsInternalInvariant(t *testing.T) {
	var window *Window
	if _, err := window.Apply(nil); !errors.Is(err, ErrStateInvariant) {
		t.Fatalf("Apply() error = %v, want state invariant", err)
	}
}

func TestWindowObserverAggregatesApplyAndSummaryOutcomes(t *testing.T) {
	requirement := requirement(1, "1", 3, 3)
	window, err := NewWindow([]LevelRequirement{requirement})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	observations := make([]Observation, 0, 6)
	window.setObserver(ObserverFunc(func(_ context.Context, observation Observation) {
		observations = append(observations, observation)
	}))
	ctx := context.Background()
	history, _ := window.History(1)
	if summary := history.SummarizeContext(ctx, 300, 3); summary.Completeness != HistoryWarming {
		t.Fatalf("empty summary = %+v, want WARMING", summary)
	}
	if _, err := window.ApplyContext(ctx, []StatePoint{
		point(180, "a", fact(requirement, LevelFactNormal)),
		point(300, "b", fact(requirement, LevelFactAnomalous)),
	}); err != nil {
		t.Fatalf("ApplyContext(initial) error = %v", err)
	}
	if summary := history.SummarizeContext(ctx, 300, 3); summary.Completeness != HistoryGapped {
		t.Fatalf("summary = %+v, want GAPPED", summary)
	}
	results, err := window.ApplyContext(ctx, []StatePoint{
		point(240, "c", fact(requirement, LevelFactNormal)),
		point(120, "d", fact(requirement, LevelFactNormal)),
	})
	if err != nil {
		t.Fatalf("ApplyContext(late) error = %v", err)
	}
	assertPointStatuses(t, results, PointApplied, PointTerminal)
	if summary := history.SummarizeContext(ctx, 300, 3); summary.Completeness != HistoryFull {
		t.Fatalf("summary = %+v, want FULL", summary)
	}

	if len(observations) != 5 {
		t.Fatalf("observations = %+v, want warming/apply/gapped/apply/full", observations)
	}
	if observations[0].Stage != StageHistorySummarized || observations[0].WarmingSummaries != 1 {
		t.Fatalf("warming summary observation = %+v", observations[0])
	}
	if observations[1].Stage != StageWindowApplied || observations[1].AppliedPoints != 2 {
		t.Fatalf("initial apply observation = %+v", observations[1])
	}
	if observations[2].Stage != StageHistorySummarized || observations[2].GappedSummaries != 1 {
		t.Fatalf("gapped summary observation = %+v", observations[2])
	}
	if observations[3].LateAcceptedPoints != 1 || observations[3].LateOutOfWindowPoints != 1 ||
		observations[3].TerminalPoints != 1 || observations[3].Result != OperationPartial {
		t.Fatalf("late apply observation = %+v", observations[3])
	}
	if observations[4].FullSummaries != 1 {
		t.Fatalf("full summary observation = %+v", observations[4])
	}

	drifted := requirement
	drifted.DetectFingerprint = strings.Repeat("2", 64)
	if _, err := window.ApplyContext(ctx, []StatePoint{point(360, "e", fact(drifted, LevelFactNormal))}); !errors.Is(err, ErrStateInvariant) {
		t.Fatalf("ApplyContext(invariant) error = %v", err)
	}
	last := observations[len(observations)-1]
	if last.Result != OperationFailed || last.InvariantViolations != 1 || last.ReasonCode != "" {
		t.Fatalf("invariant observation = %+v", last)
	}
}

func requirement(levelID uint32, fingerprintDigit string, required, retained uint32) LevelRequirement {
	return LevelRequirement{
		LevelID:            levelID,
		DetectFingerprint:  strings.Repeat(fingerprintDigit, 64),
		RequiredPoints:     required,
		RetentionPoints:    retained,
		EvaluationInterval: time.Minute,
		LatenessTolerance:  0,
	}
}

func point(sourceTime int64, digestDigit string, facts ...PointLevelFact) StatePoint {
	return StatePoint{RecordID: strings.Repeat(digestDigit, 64), SourceTime: sourceTime, Levels: facts}
}

func fact(requirement LevelRequirement, result LevelFactResult) PointLevelFact {
	return PointLevelFact{
		LevelID: requirement.LevelID, DetectFingerprint: requirement.DetectFingerprint, Result: result,
	}
}

func assertPointStatuses(t *testing.T, results []PointResult, statuses ...PointStatus) {
	t.Helper()
	if len(results) != len(statuses) {
		t.Fatalf("results count = %d, want %d", len(results), len(statuses))
	}
	for index, status := range statuses {
		if results[index].Status != status {
			t.Fatalf("result[%d] = %+v, want status %q", index, results[index], status)
		}
	}
}

func mustApply(t testing.TB, window *Window, points []StatePoint) []PointResult {
	t.Helper()
	results, err := window.Apply(points)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	return results
}
