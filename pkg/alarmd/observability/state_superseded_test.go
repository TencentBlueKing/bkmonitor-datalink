// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func supersededSampleForTest() *StateSupersededSample {
	return &StateSupersededSample{Series: "series-a",
		Slot:   ApplyVersionFacts{StateApplyEpoch: 1, EvaluationTime: 1_788_000_060, SlotDigest: "slot-k"},
		Stored: ApplyVersionFacts{StateApplyEpoch: 1, EvaluationTime: 1_788_000_120, SlotDigest: "slot-k1"}}
}

// A successful completion that skipped series a later Slot had written is
// written, with the count and the sample's two versions, under its Query
// Group's repeated-line limit; the same completion with nothing skipped is
// routine, counted and not written. Without this the count and sample the
// worker carries to the completion would ride on a line nobody sees.
func TestACompletionThatSkippedSupersededSeriesIsWritten(t *testing.T) {
	var output bytes.Buffer
	limiter, _ := NewScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Hour, MaxEvents: 2, MaxScopes: 1024})
	policy, _ := NewScopedBoundedLogPolicy(limiter)
	observer := NewLoggingObserver(New("alarmd", &output), policy)
	ctx := context.Background()
	for range 3 {
		observer.Observe(ctx, Observation{Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultSuccess,
			SlotSuperseded: &SlotSupersededFacts{Series: 2, Sample: supersededSampleForTest()},
			Trace:          TraceFields{QueryGroupKey: "qg-skipped"}})
	}
	for range 4 {
		observer.Observe(ctx, Observation{Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultSuccess,
			SlotSuperseded: &SlotSupersededFacts{}, Trace: TraceFields{QueryGroupKey: "qg-whole"}})
	}
	counts := observer.LineCounts()
	written, limited := counts.Written[StageSlotCompleted], counts.Limited[StageSlotCompleted]
	if written == 0 || written+limited != 3 || counts.Unwritten[StageSlotCompleted] != 4 {
		t.Fatalf("slot_completed written %d limited %d unwritten %d, want the three skipping rounds written or held "+
			"back, and the four with nothing skipped unwritten", written, limited, counts.Unwritten[StageSlotCompleted])
	}
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		for _, want := range []string{`"superseded_series":2`, `"series_identity_digest":"series-a"`,
			`"slot_apply_version":{"state_apply_epoch":1,"evaluation_time":1788000060,"slot_digest":"slot-k"}`,
			`"stored_apply_version":{"state_apply_epoch":1,"evaluation_time":1788000120,"slot_digest":"slot-k1"}`} {
			if !strings.Contains(line, want) {
				t.Fatalf("line lacks %s:\n%s", want, line)
			}
		}
	}
}

// The skip's own line names its site and count, and the series' sample; the
// round that commits carries history_superseded beside the other two counts
// of series handled without a window, and only when there is one to carry.
func TestTheSkipAndTheRoundThatCommitsItSayHowMuchWasSuperseded(t *testing.T) {
	line := func(observation Observation) string {
		buffer := &bytes.Buffer{}
		logger := &Logger{component: "worker", next: slog.New(slog.NewTextHandler(buffer, nil))}
		logger.logObservation(context.Background(), observation, LogAdmission{})
		return buffer.String()
	}
	skip := line(Observation{Component: ComponentState, Stage: StageMutationCompared, Result: ResultSuccess,
		ReasonCode:      ReasonStateSupersededBeforeEvaluation,
		StateSuperseded: &StateSupersededFacts{Site: StateSupersededAtState, Count: 1, Sample: supersededSampleForTest()}})
	for _, want := range []string{"state_superseded_state=1", "state_superseded_sample=", "series-a", "reason_code=state_superseded_before_evaluation"} {
		if !strings.Contains(skip, want) {
			t.Errorf("the skip's line lacks %q:\n%s", want, skip)
		}
	}
	gap := line(Observation{Component: ComponentState, Stage: StageGapGuardCommitted, Result: ResultSuccess,
		StateSuperseded: &StateSupersededFacts{Site: StateSupersededAtGap, Count: 2}})
	if !strings.Contains(gap, "state_superseded_gap=2") || strings.Contains(gap, "state_superseded_sample") {
		t.Errorf("the gap skip's line:\n%s", gap)
	}
	committed := line(Observation{Component: ComponentProgress, Stage: StageProgressCommitted, Result: ResultSuccess,
		HistoryCoverage: &HistoryCoverageFacts{Levels: 3, Superseded: 2}})
	for _, want := range []string{"history_superseded=2", "history_levels=3", "history_resumed=0"} {
		if !strings.Contains(committed, want) {
			t.Errorf("the committing line lacks %q:\n%s", want, committed)
		}
	}
	quiet := line(Observation{Component: ComponentProgress, Stage: StageProgressCommitted, Result: ResultSuccess,
		HistoryCoverage: &HistoryCoverageFacts{Levels: 3}, StateSuperseded: &StateSupersededFacts{Site: StateSupersededAtState}})
	if strings.Contains(quiet, "superseded") {
		t.Errorf("a round that skipped nothing names a skip:\n%s", quiet)
	}
}

// A run whose every series was superseded summarised no window and says why:
// accepted as that reading, not dropped as nothing and not refused as a
// zero-window set counting something.
func TestARunWhoseEverySeriesWasSupersededIsAReading(t *testing.T) {
	in := HistoryCoverageFacts{Superseded: 4}
	kept, rejected := normalizeHistoryCoverageFacts(&in)
	if rejected != nil || kept == nil || kept.Superseded != 4 {
		t.Fatalf("kept %+v rejected %+v, want the count carried", kept, rejected)
	}
}

// The counter's labels are closed: a site or operation this build does not
// name folds into other.
func TestSupersededLabelsAreClosed(t *testing.T) {
	for site, want := range map[StateSupersededSite]StateSupersededSite{
		StateSupersededAtState: StateSupersededAtState, StateSupersededAtGap: StateSupersededAtGap, "noise": stateSupersededOther,
	} {
		if got := NormalizeStateSupersededSite(site); got != want {
			t.Errorf("site %q -> %q, want %q", site, got, want)
		}
	}
	for operation, want := range map[Operation]Operation{
		OperationNormal: OperationNormal, OperationRetry: OperationRetry, OperationReplay: OperationReplay,
		OperationProbe: OperationProbe, OperationSupplement: OperationOther, OperationWrite: OperationOther,
	} {
		if got := NormalizeStateSupersededOperation(operation); got != want {
			t.Errorf("operation %q -> %q, want %q", operation, got, want)
		}
	}
}

// The skip's word is kept as itself wherever a reason is normalized -- on a
// line and in a metric label -- as the already-applied word beside it is:
// folded into other, the one success that says a later Slot was there first
// would read like any unclassified one.
func TestTheBeforeEvaluationWordsAreKeptOnLinesAndLabels(t *testing.T) {
	for _, reason := range []ReasonCode{ReasonStateAlreadyAppliedBeforeEvaluation, ReasonStateSupersededBeforeEvaluation} {
		if got := NormalizeReason(reason, ResultSuccess); got != reason {
			t.Errorf("line reason %s -> %s", reason, got)
		}
		if got := NormalizeMetricReason(ComponentState, reason, ResultSuccess); got != reason {
			t.Errorf("metric reason %s -> %s", reason, got)
		}
	}
}
