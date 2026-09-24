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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A round that completed with a deterministic refusal on one of its series
// is listed on that round: the same round run again refuses the same way,
// so waiting for DefaultDegradedRounds of them buys nothing and, on an
// hourly strategy, costs two hours. A degraded completion that is not
// terminal still waits, as before: a short window fills.
func TestATerminalCompletionIsListedOnItsFirstRound(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	round := func(group, kind, reason string) {
		at.at = at.at.Add(time.Hour)
		tracker.Observe(context.Background(), observability.Observation{
			ProgressCompletionKind: kind, ProgressCompletionCause: "LEVEL_OUTCOME_TERMINAL", ProgressCompletionReason: reason,
			Trace: observability.TraceFields{QueryGroupKey: group, StrategyID: "4101", BusinessID: "10", EvaluationTime: at.at.Unix()},
		})
	}
	round("qg-refused", "COMPLETED_WITH_TERMINAL", "STATE_CORRUPT")
	rows := tracker.Anomalies()
	if len(rows) != 1 || rows[0].QueryGroup != "qg-refused" || rows[0].Kind != KindDegradedRun || rows[0].ReasonCode != "COMPLETED_WITH_TERMINAL" {
		t.Fatalf("after one terminal round rows = %+v, want the object listed as a degraded run on COMPLETED_WITH_TERMINAL", rows)
	}
	// The same object completing healthily leaves the line, and the
	// recovery is on record: the predicate for "is a row" is one.
	round("qg-refused", "FULL_COMPLETED", "")
	if rows := tracker.Anomalies(); len(rows) != 0 {
		t.Fatalf("after a healthy round rows = %+v, want none", rows)
	}
	if recovered := tracker.Recovered(); len(recovered) != 1 {
		t.Fatalf("recovered = %+v, want the one object's recovery on record", recovered)
	}
	// A degraded completion that is not terminal waits for the threshold.
	for i := 0; i < DefaultDegradedRounds-1; i++ {
		round("qg-short", "COMPLETED_WITH_UNAVAILABLE", "QUERY_UNAVAILABLE")
	}
	if rows := tracker.Anomalies(); len(rows) != 0 {
		t.Fatalf("after %d degraded rounds rows = %+v, want none before the threshold", DefaultDegradedRounds-1, rows)
	}
	round("qg-short", "COMPLETED_WITH_UNAVAILABLE", "QUERY_UNAVAILABLE")
	if rows := tracker.Anomalies(); len(rows) != 1 || rows[0].QueryGroup != "qg-short" {
		t.Fatalf("at the threshold rows = %+v, want the degraded object", rows)
	}
}

// Terminal and a non-terminal degraded completion alternating: the row is
// listed on the terminal round, off on the skip -- the latest round is not a
// deterministic refusal -- and back on the next terminal, where it also
// reaches the degraded threshold and stays. Leaving the list because the
// latest round changed is not a recovery: only a healthy completion is, and
// the run's recovery record has nothing in it throughout.
func TestATerminalRowAlternatingWithASkipFlickersOnceAndRecordsNoRecovery(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	round := func(kind, cause, reason string) {
		at.at = at.at.Add(time.Hour)
		tracker.Observe(context.Background(), observability.Observation{
			ProgressCompletionKind: kind, ProgressCompletionCause: cause, ProgressCompletionReason: reason,
			Trace: observability.TraceFields{QueryGroupKey: "qg-alternating", StrategyID: "4101", BusinessID: "10", EvaluationTime: at.at.Unix()},
		})
	}
	listed := func() bool {
		for _, row := range tracker.Anomalies() {
			if row.QueryGroup == "qg-alternating" {
				return true
			}
		}
		return false
	}
	round("COMPLETED_WITH_TERMINAL", "LEVEL_OUTCOME_TERMINAL", "STATE_CORRUPT")
	if !listed() {
		t.Fatal("first terminal round: not listed")
	}
	round("GAP_SKIPPED", "LEVEL_OUTCOME_UNKNOWN", "GAP_SKIPPED")
	if listed() {
		t.Fatal("a skipped round after one terminal round: listed, but the latest round is not a deterministic refusal and the threshold is not reached")
	}
	round("COMPLETED_WITH_TERMINAL", "LEVEL_OUTCOME_TERMINAL", "STATE_CORRUPT")
	if !listed() {
		t.Fatal("second terminal round: not listed")
	}
	round("GAP_SKIPPED", "LEVEL_OUTCOME_UNKNOWN", "GAP_SKIPPED")
	if !listed() {
		t.Fatal("past the degraded threshold a skipped round takes the row off: the flicker is bounded to the rounds before it")
	}
	if recovered := tracker.Recovered(); len(recovered) != 0 {
		t.Fatalf("leaving the list on a changed latest round was recorded as a recovery: %+v", recovered)
	}
}
