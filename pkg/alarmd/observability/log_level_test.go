// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// levelObserver is a logging observer under the production budget shape -- a
// failure a minute, anything else an hour -- with its clock in the case's
// hands.
func levelObserver(t *testing.T, output *bytes.Buffer, now *time.Time) *LoggingObserver {
	t.Helper()
	limiter, err := newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 1024}, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewScopedBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	return NewLoggingObserver(New("alarmd", output), policy)
}

func decodeLines(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("decode %q: %v", raw, err)
		}
		lines = append(lines, line)
	}
	output.Reset()
	return lines
}

// Each written line takes the level the rules give it: a handover refusal
// is INFO wherever it is met, on the handover or on a Slot that was running
// when its Query Group moved; any other failure is ERROR; a held result is
// INFO, and so is a round refused while the view has not carried its Query
// Group yet; any other result takes its reason's level.
func TestEachLineTakesTheLevelItsResultAndReasonGive(t *testing.T) {
	t.Parallel()

	held := &HeldByFacts{Decision: "query_cooldown"}
	for _, tc := range []struct {
		name        string
		observation Observation
		level       string
	}{
		{"handover refusal on a takeover", Observation{Component: ComponentOwnership, Stage: StageTakeoverCompleted, Result: ResultFailed,
			ReasonCode: ReasonCode(contract.ReasonOwnershipLeaseBusy), Err: errors.New("busy")}, "INFO"},
		{"handover refusal on a lost assignment", Observation{Component: ComponentOwnership, Stage: StageAssignmentLost, Result: ResultFailed,
			ReasonCode: ReasonCode(contract.ReasonOwnershipNotDesired), Err: errors.New("not desired")}, "INFO"},
		{"the same refusal on a Slot", Observation{Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultFailed,
			ReasonCode: ReasonCode(contract.ReasonOwnershipNotDesired), Err: errors.New("not desired")}, "INFO"},
		{"a fence the handover moved", Observation{Component: ComponentOwnership, Stage: StageFenceChecked, Result: ResultFailed,
			ReasonCode: ReasonCode(contract.ReasonOwnershipNotDesired), Err: errors.New("not desired")}, "INFO"},
		{"a round refused while the view has not carried its Query Group", Observation{Component: ComponentScheduler, Stage: StageScheduleDue, Result: ResultRetrying,
			ReasonCode: ReasonCode(contract.ReasonViewNotExecutable), AwaitingView: true}, "INFO"},
		{"a round the view refused on another check", Observation{Component: ComponentScheduler, Stage: StageScheduleDue, Result: ResultRetrying,
			ReasonCode: ReasonCode(contract.ReasonViewNotExecutable)}, "WARN"},
		{"a round the supplement held", Observation{Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultSkipped,
			ReasonCode: ReasonHeldBySupplement}, "INFO"},
		{"a retryable failure class not failed", Observation{Component: ComponentControlPlane, Stage: StageSnapshotUnavailable, Result: ResultDegraded,
			ReasonCode: ReasonContractRetryable}, "WARN"},
		{"a Slot cancelled from above", Observation{Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultFailed,
			ReasonCode: ReasonSlotCancelled, Err: errors.New("context canceled")}, "INFO"},
		{"Progress refused for a stale owner", Observation{Component: ComponentProgress, Stage: StageProgressCommitted, Result: ResultFailed,
			ReasonCode: ReasonCode(contract.ReasonOwnershipStaleFence), Err: errors.New("progress not committed: STALE_OWNER")}, "INFO"},
		{"a no-data stall", Observation{Component: ComponentEvaluation, Stage: StageNoDataDecided, Result: ResultDegraded,
			ReasonCode: "SKIPPED_HOSTS_UNRESOLVED"}, "WARN"},
		{"a failure", Observation{Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultFailed,
			ReasonCode: "QUERY_PERMIT_DEADLINE", Err: errors.New("deadline")}, "ERROR"},
		{"a timeout", Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultTimeout,
			ReasonCode: ReasonCode(contract.ReasonQueryTimeout)}, "ERROR"},
		{"a skip something held", Observation{Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultDegraded,
			ReasonCode: ReasonCode(contract.ReasonGapSkipped), HeldBy: held}, "INFO"},
		{"the same skip unheld", Observation{Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultDegraded,
			ReasonCode: ReasonCode(contract.ReasonGapSkipped)}, "WARN"},
		{"a backend not answering", Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultDegraded,
			ReasonCode: ReasonCode(contract.ReasonQueryUnavailable)}, "WARN"},
		{"warming after a start", Observation{Component: ComponentEvaluation, Stage: StageEvaluationCompleted, Result: ResultDegraded,
			ReasonCode: ReasonCode(contract.ReasonHistoryWarming)}, "INFO"},
		{"a strategy's target missing", Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultDegraded,
			ReasonCode: ReasonCode(contract.ReasonQueryTargetMissing)}, "INFO"},
		{"a result that names no reason", Observation{Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultDegraded}, "WARN"},
		{"an activation still draining", Observation{Component: ComponentControlPlane, Stage: StageActivationFailed, Result: ResultDegraded,
			ReasonCode: "schedule_cutover/not_drained"}, "INFO"},
		{"an activation corrupt", Observation{Component: ComponentControlPlane, Stage: StageActivationFailed, Result: ResultDegraded,
			ReasonCode: "schedule_cutover/corrupt"}, "WARN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			now := time.Unix(1_000, 0)
			observation := tc.observation
			observation.Trace.QueryGroupKey = "qg-a"
			levelObserver(t, &output, &now).Observe(context.Background(), observation)
			lines := decodeLines(t, &output)
			if len(lines) != 1 || lines[0]["level"] != tc.level {
				t.Fatalf("lines=%v, want one at %s", lines, tc.level)
			}
			// The reader of an INFO refusal needs to see why it is not a
			// WARN: the line says it is awaiting the view.
			if awaiting, _ := lines[0]["awaiting_view"].(bool); awaiting != tc.observation.AwaitingView {
				t.Fatalf("line=%v, want awaiting_view only on a round refused while the view has not carried its Query Group", lines[0])
			}
		})
	}
}

// A routine stage's success is counted and not written; a Control Leader
// round is written when it changed something and counted when it did not; a
// lifecycle line is always written; and a workflow stage's success that is an
// event, not a step, is written under the hourly sample.
func TestWhichSuccessesAreWrittenAndWhichAreCounted(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	now := time.Unix(1_000, 0)
	observer := levelObserver(t, &output, &now)
	ctx := context.Background()
	qg := TraceFields{QueryGroupKey: "qg-a"}
	for _, observation := range []Observation{
		{Component: ComponentScheduler, Stage: StageSlotStarted, Result: ResultStarted, Trace: qg},
		{Component: ComponentEvaluation, Stage: StageEvaluationCompleted, Result: ResultSuccess, Trace: qg},
		{Component: ComponentOwnership, Stage: StageTakeoverCompleted, Result: ResultSuccess, Trace: qg},
		{Component: ComponentOwnership, Stage: StageRebalancePlanned, Result: ResultSuccess, Rebalance: &RebalanceFacts{Assigned: 4}},
		{Component: ComponentControlPlane, Stage: StageSnapshotRefreshed, Result: ResultSuccess,
			SourceRefresh: &SourceRefreshFacts{Status: SourceRefreshUnchanged}},
		{Component: ComponentOwnership, Stage: StageControlReadsSpent, Result: ResultSuccess},
	} {
		observer.Observe(ctx, observation)
	}
	if lines := decodeLines(t, &output); len(lines) != 0 {
		t.Fatalf("routine and unchanged lines were written: %v", lines)
	}
	counts := observer.LineCounts()
	for _, stage := range []Stage{StageSlotStarted, StageEvaluationCompleted, StageTakeoverCompleted, StageRebalancePlanned,
		StageSnapshotRefreshed, StageControlReadsSpent} {
		if counts.Unwritten[stage] != 1 {
			t.Fatalf("%s unwritten=%d, want 1", stage, counts.Unwritten[stage])
		}
	}

	moved := Observation{Component: ComponentOwnership, Stage: StageRebalancePlanned, Result: ResultSuccess,
		Rebalance: &RebalanceFacts{PlannedMoves: 2}}
	changed := Observation{Component: ComponentControlPlane, Stage: StageSnapshotRefreshed, Result: ResultSuccess,
		SourceRefresh: &SourceRefreshFacts{Status: SourceRefreshPublished}}
	observer.Observe(ctx, moved)
	observer.Observe(ctx, moved)
	observer.Observe(ctx, changed)
	observer.Observe(ctx, Observation{Component: ComponentRuntime, Stage: StageStartup, Result: ResultStarted})
	if lines := decodeLines(t, &output); len(lines) != 4 {
		t.Fatalf("lines=%v, want both rounds that moved, the changed refresh and the startup", lines)
	}

	turnaway := Observation{Component: ComponentScheduler, Stage: StageDispatchTurnaway, Result: ResultSuccess, Trace: qg}
	observer.Observe(ctx, turnaway)
	observer.Observe(ctx, turnaway)
	lines := decodeLines(t, &output)
	if len(lines) != 1 || lines[0]["stage"] != string(StageDispatchTurnaway) || lines[0]["sampled"] != true {
		t.Fatalf("turnaway lines=%v, want the first, as the hour's sample", lines)
	}
	now = now.Add(time.Hour)
	observer.Observe(ctx, turnaway)
	if lines := decodeLines(t, &output); len(lines) != 1 || lines[0]["suppressed_logs"] != float64(1) {
		t.Fatalf("next hour's turnaway=%v, want one line carrying the one merged", lines)
	}
}

// A worker's index read is written when the index did not advance for two
// rounds or more, or could not be read; one stale round is the race between a
// worker reading and the Leader writing, counted and not written.
func TestAnIndexReadOneRoundBehindIsCountedAndTwoIsWritten(t *testing.T) {
	t.Parallel()

	read := func(facts AssignmentIndexFacts) Observation {
		return Observation{Component: ComponentOwnership, Stage: StageAssignmentIndexRead, Result: ResultSuccess, AssignmentIndex: &facts}
	}
	for _, tc := range []struct {
		name    string
		facts   AssignmentIndexFacts
		written bool
	}{
		{"fresh", AssignmentIndexFacts{Result: AssignmentIndexFresh}, false},
		{"one round behind", AssignmentIndexFacts{Result: AssignmentIndexStale, StaleRounds: 1}, false},
		{"two rounds behind", AssignmentIndexFacts{Result: AssignmentIndexStale, StaleRounds: 2}, true},
		{"missing", AssignmentIndexFacts{Result: AssignmentIndexMissing}, true},
		{"invalid", AssignmentIndexFacts{Result: AssignmentIndexInvalid}, true},
		{"fresh but rewritten", AssignmentIndexFacts{Result: AssignmentIndexFresh, Rewritten: 3}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			now := time.Unix(1_000, 0)
			observer := levelObserver(t, &output, &now)
			observer.Observe(context.Background(), read(tc.facts))
			if written := output.Len() > 0; written != tc.written {
				t.Fatalf("written=%v, want %v: %s", written, tc.written, output.String())
			}
			if unwritten := observer.LineCounts().Unwritten[StageAssignmentIndexRead]; (unwritten == 1) == tc.written {
				t.Fatalf("unwritten=%d with written=%v", unwritten, tc.written)
			}
		})
	}
}

// A result that is not a failure keeps one line per (reason, stage, Query
// Group) an hour; a failure keeps one a minute; and the recovery after a run
// of failures is not counted against them.
func TestTheHourlySampleAndTheFailureWindowAreKeptApart(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	now := time.Unix(1_000, 0)
	observer := levelObserver(t, &output, &now)
	ctx := context.Background()
	degraded := Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultDegraded,
		ReasonCode: ReasonCode(contract.ReasonQueryTimeout), Trace: TraceFields{QueryGroupKey: "qg-a"}}
	failed := Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultFailed,
		ReasonCode: ReasonCode(contract.ReasonQueryTimeout), Trace: TraceFields{QueryGroupKey: "qg-a"}, Err: errors.New("x")}
	for minute := 0; minute < 3; minute++ {
		observer.Observe(ctx, degraded)
		observer.Observe(ctx, failed)
		now = now.Add(time.Minute)
	}
	lines := decodeLines(t, &output)
	var degradedLines, failedLines int
	for _, line := range lines {
		switch line["result"] {
		case string(ResultDegraded):
			degradedLines++
		case string(ResultFailed):
			failedLines++
		}
	}
	if degradedLines != 1 || failedLines != 3 {
		t.Fatalf("degraded %d failed %d over three minutes, want 1 and 3: %v", degradedLines, failedLines, lines)
	}
	observer.Observe(ctx, Observation{Component: ComponentRuntime, Stage: StageFleetSnapshotPublish, Result: ResultFailed, Err: errors.New("x")})
	observer.Observe(ctx, Observation{Component: ComponentRuntime, Stage: StageFleetSnapshotPublish, Result: ResultResumed})
	if lines := decodeLines(t, &output); len(lines) != 2 {
		t.Fatalf("lines=%v, want the failure and the recovery after it", lines)
	}
}

// An assignment change is one line: the counts, the first few names of each
// list, and whether a list was longer than its sample.
func TestAnAssignmentChangeIsOneLineWithItsCountsAndSamples(t *testing.T) {
	t.Parallel()

	var applied AssignmentAppliedFacts
	if applied.Changed() {
		t.Fatal("an empty change says it changed")
	}
	for index := 0; index < MaxAssignmentAppliedSamples+2; index++ {
		applied.Note(fmt.Sprintf("qg-%02d", index), false, false)
	}
	applied.Note("qg-lost", true, false)
	applied.Note("qg-busy", false, true)
	var output bytes.Buffer
	now := time.Unix(1_000, 0)
	levelObserver(t, &output, &now).Observe(context.Background(), Observation{
		Component: ComponentOwnership, Stage: StageAssignmentApplied, Result: ResultSuccess, AssignmentApplied: &applied,
	})
	lines := decodeLines(t, &output)
	if len(lines) != 1 {
		t.Fatalf("lines=%v, want one", lines)
	}
	line := lines[0]
	if _, sampled := line["sampled"]; sampled {
		t.Fatalf("assignment line=%v, want it written as a lifecycle line, not as an hourly sample", line)
	}
	if line["level"] != "INFO" || line["assignment_acquired"] != float64(MaxAssignmentAppliedSamples+2) ||
		line["assignment_lost"] != float64(1) || line["assignment_failed"] != float64(1) ||
		line["assignment_lost_sample"] != "qg-lost" || line["assignment_failed_sample"] != "qg-busy" ||
		line["assignment_samples_truncated"] != true ||
		strings.Count(line["assignment_acquired_sample"].(string), ",") != MaxAssignmentAppliedSamples-1 {
		t.Fatalf("assignment line=%v", line)
	}
}

// A no-data stall is written at WARN whichever skip it stalled on: it is
// reported once per stall, and it says a Plan has stopped detecting absence.
// The stall words live in two lists, the catalogue and the WARN set, and a
// skip added to one and not the other would stall at INFO.
func TestEveryNoDataStallIsWrittenAtWarn(t *testing.T) {
	for _, reason := range NoDataStallReasons {
		if got := ReasonLogLevel(reason); got != slog.LevelWarn {
			t.Errorf("a stall on %s is written at %s, want WARN", reason, got)
		}
	}
}
