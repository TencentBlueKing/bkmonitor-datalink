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
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// awaitingViewTracker drives the rounds the view refuses, on a clock the test
// moves: a refusal that is the view's one wait (not_in_view, AwaitingView on
// the round line) and one that is not (scope_mismatch).
type awaitingViewTracker struct {
	t       *testing.T
	tracker *Tracker
	now     time.Time
}

func newAwaitingViewTracker(t *testing.T, bound time.Duration) *awaitingViewTracker {
	tr := &awaitingViewTracker{t: t, now: time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)}
	tr.tracker = NewTracker(nil, "pod-a", func() time.Time { return tr.now })
	if bound > 0 {
		tr.tracker.SetAwaitingViewBound(bound)
	}
	return tr
}

func (tr *awaitingViewTracker) at(offset time.Duration) time.Time {
	return time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC).Add(offset)
}

func (tr *awaitingViewTracker) refused(queryGroup string, offset time.Duration, awaiting bool) {
	tr.now = tr.at(offset)
	word := "scope_mismatch"
	if awaiting {
		word = "not_in_view"
	}
	tr.tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageRunnerReturned,
		Result: observability.ResultTerminal, RunOutcome: "view_not_executable", AwaitingView: awaiting,
		Err:   errors.New("alarmd scheduler: the executable view does not allow this Query Group: " + word),
		Trace: observability.TraceFields{QueryGroupKey: queryGroup},
	})
}

func (tr *awaitingViewTracker) answered(queryGroup string, offset time.Duration, outcome string) {
	tr.now = tr.at(offset)
	tr.tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageRunnerReturned,
		Result: observability.ResultTerminal, RunOutcome: outcome,
		Trace: observability.TraceFields{QueryGroupKey: queryGroup},
	})
}

func (tr *awaitingViewTracker) completed(queryGroup string, offset time.Duration, kind string) {
	tr.now = tr.at(offset)
	tr.tracker.Observe(context.Background(), observability.Observation{ProgressCompletionKind: kind,
		Trace: observability.TraceFields{QueryGroupKey: queryGroup, EvaluationTime: tr.now.Unix()}})
}

func (tr *awaitingViewTracker) executionFailed(queryGroup string, offset time.Duration) {
	tr.now = tr.at(offset)
	tr.tracker.Observe(context.Background(), observability.Observation{
		ExecuteOutcome: "error", Err: errors.New("redis: connection pool timeout"), Operation: "commit",
		Trace: observability.TraceFields{QueryGroupKey: queryGroup, EvaluationTime: tr.now.Unix()}})
}

// row is the object's listed row with its line decided, or nil.
func (tr *awaitingViewTracker) row(queryGroup string) *Anomaly {
	tr.t.Helper()
	rows := tr.tracker.Anomalies()
	Attribute(rows, tr.now)
	for index := range rows {
		if rows[index].QueryGroup == queryGroup {
			return &rows[index]
		}
	}
	return nil
}

// A Query Group waiting for its view - every one at a Worker's start, each
// one moved to it - is not listed while the wait is one the design expects,
// and is listed past the bound under its own line, saying since when, for
// how long and against what bound. Not as a dependency that is down: the
// view is the deployment's own control plane, and nothing it depends on
// failed.
func TestAWaitForTheViewIsListedOnlyPastItsBoundAndOnItsOwnLine(t *testing.T) {
	tr := newAwaitingViewTracker(t, 75*time.Second)
	for offset := 5 * time.Second; offset <= 70*time.Second; offset += 5 * time.Second {
		tr.refused("qg-wait", offset, true)
		if row := tr.row("qg-wait"); row != nil {
			t.Fatalf("listed %s into a wait the design expects: %+v", offset, row)
		}
	}
	tr.refused("qg-wait", 80*time.Second, true)
	row := tr.row("qg-wait")
	if row == nil {
		t.Fatal("a wait past its bound is not listed")
	}
	if row.Finding.Check != CheckAwaitingView {
		t.Fatalf("listed under %s, want %s", row.Finding.Check, CheckAwaitingView)
	}
	want := AwaitingView{Since: tr.at(5 * time.Second), WaitedSeconds: 75, BoundSeconds: 75}
	if row.AwaitingView == nil || *row.AwaitingView != want {
		t.Fatalf("the row's wait = %+v, want %+v", row.AwaitingView, want)
	}
	if row.Blocked == nil || row.Blocked.Dependency != DependencyNone {
		t.Fatalf("the row's reading = %+v, want no dependency named", row.Blocked)
	}
}

// Every other refusal of the view is read as today: listed after two rounds,
// as a dependency that is down.
func TestARefusalOtherThanTheWaitIsADependencyDownAfterTwoRounds(t *testing.T) {
	tr := newAwaitingViewTracker(t, 75*time.Second)
	tr.refused("qg-scope", 1*time.Second, false)
	tr.refused("qg-scope", 2*time.Second, false)
	if row := tr.row("qg-scope"); row == nil || row.Finding.Check != CheckDependencyDown || row.AwaitingView != nil {
		t.Fatalf("a scope mismatch twice = %+v, want listed under %s with no wait on it", row, CheckDependencyDown)
	}
}

// A wait ends where a blocked run does - the source answering not due, a
// round completing whole or degraded, a round that reached execution and
// failed: each a round the view let through - and the next wait counts from
// its own first round, not from the one before.
func TestAWaitEndsWhereABlockedRunDoesAndTheNextCountsFromItsOwnStart(t *testing.T) {
	for _, end := range []string{"source_not_due", "completed", "completed degraded", "execution failed"} {
		t.Run(end, func(t *testing.T) {
			tr := newAwaitingViewTracker(t, 75*time.Second)
			tr.refused("qg", 5*time.Second, true)
			tr.refused("qg", 85*time.Second, true)
			if row := tr.row("qg"); row == nil {
				t.Fatal("the first wait past its bound is not listed")
			}
			switch end {
			case "completed":
				tr.completed("qg", 90*time.Second, "FULL_COMPLETED")
			case "completed degraded":
				tr.completed("qg", 90*time.Second, "COMPLETED_WITH_UNAVAILABLE")
			case "execution failed":
				tr.executionFailed("qg", 90*time.Second)
			default:
				tr.answered("qg", 90*time.Second, end)
			}
			if row := tr.row("qg"); row != nil {
				t.Fatalf("listed after the wait ended: %+v", row)
			}
			tr.refused("qg", 200*time.Second, true)
			tr.refused("qg", 250*time.Second, true)
			if row := tr.row("qg"); row != nil {
				t.Fatalf("a new wait 50s old is listed: %+v", row)
			}
			tr.refused("qg", 280*time.Second, true)
			if row := tr.row("qg"); row == nil || row.AwaitingView == nil || !row.AwaitingView.Since.Equal(tr.at(200*time.Second)) {
				t.Fatalf("the new wait past its bound = %+v, want listed since its own first round", row)
			}
		})
	}
}

// A tracker given no bound - one put together without the deployment's
// timings - lists the wait as any blocked run, after two rounds, but on the
// wait's own line.
func TestAWaitWithNoBoundIsListedAfterTwoRoundsOnItsOwnLine(t *testing.T) {
	tr := newAwaitingViewTracker(t, 0)
	tr.refused("qg", 1*time.Second, true)
	if row := tr.row("qg"); row != nil {
		t.Fatalf("a wait with no bound is listed after one round: %+v", row)
	}
	tr.refused("qg", 2*time.Second, true)
	if row := tr.row("qg"); row == nil || row.Finding.Check != CheckAwaitingView {
		t.Fatalf("a wait with no bound = %+v, want listed under %s", row, CheckAwaitingView)
	}
}

// A rollout moves its Query Groups a batch a round, so its moves trail for
// longer than the bound; each Query Group's wait counts from its own move,
// because its view entry arrives with its own Assignment. One moved in a
// later batch is not listed when the first batch's would be.
func TestAQueryGroupMovedInALaterBatchWaitsFromItsOwnMove(t *testing.T) {
	tr := newAwaitingViewTracker(t, 75*time.Second)
	tr.refused("qg-first-batch", 0, true)
	tr.refused("qg-later-batch", 80*time.Second, true)
	tr.refused("qg-first-batch", 100*time.Second, true)
	tr.refused("qg-later-batch", 100*time.Second, true)
	if row := tr.row("qg-first-batch"); row == nil || row.Finding.Check != CheckAwaitingView {
		t.Fatalf("the first batch's wait at 100s = %+v, want listed under %s", row, CheckAwaitingView)
	}
	if row := tr.row("qg-later-batch"); row != nil {
		t.Fatalf("a later batch's wait 20s old is listed: %+v", row)
	}
}

// A run in which the wait alternates with another refusal is not a wait: it
// is listed after two rounds as a dependency that is down, and the waits
// after that do not take it off the line - restarting the wait's clock on
// every other refusal would have kept it off every line.
func TestARunAlternatingTheWaitWithAnotherRefusalIsADependencyDown(t *testing.T) {
	tr := newAwaitingViewTracker(t, 75*time.Second)
	tr.refused("qg", 1*time.Second, true)
	tr.refused("qg", 2*time.Second, false)
	for offset := 3 * time.Second; offset <= 10*time.Second; offset += time.Second {
		tr.refused("qg", offset, offset%(2*time.Second) == 0)
		row := tr.row("qg")
		if row == nil || row.Finding.Check != CheckDependencyDown || row.AwaitingView != nil {
			t.Fatalf("at %s an alternating run = %+v, want listed under %s", offset, row, CheckDependencyDown)
		}
	}
}
