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
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func execution(queryGroup, outcome string) observability.Observation {
	return observability.Observation{
		ExecuteOutcome: outcome,
		Trace:          observability.TraceFields{QueryGroupKey: queryGroup},
	}
}

// The stall flag promises that the object's rounds stopped ending and will not
// start ending again on their own. An object that has completed degraded for
// hours is anomalous, but every one of those rounds ended; if a single round
// then fails to finish, the rounds stopped ending just now, not hours ago.
// Judging the flag from the anomaly's own start point flagged such objects at
// once and cleared them on the next degraded completion, so the count moved
// with the last round's luck rather than with anything that needed a person.
func TestAStallIsJudgedFromWhenRoundsStoppedFinishingNotFromWhenTheObjectDegraded(t *testing.T) {
	const budget = 10 * time.Minute
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := context.Background()
	stalled := func(step string) bool {
		t.Helper()
		anomalies := tracker.Anomalies()
		if len(anomalies) != 1 {
			t.Fatalf("%s: anomalies = %+v, want the one object", step, anomalies)
		}
		MarkStalled(anomalies, at.at, budget)
		if anomalies[0].Since != now {
			t.Fatalf("%s: since = %v, want the anomaly's own start kept at %v", step, anomalies[0].Since, now)
		}
		return anomalies[0].Stalled
	}

	// Degraded for three budgets: every round ended, badly.
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(ctx, completion("qg-1", "COMPLETED_WITH_UNAVAILABLE", "8930"))
		at.at = at.at.Add(budget)
	}
	if stalled("degraded only") {
		t.Fatal("an object whose every round ended is flagged as never ending")
	}

	// Then one round does not finish.
	tracker.Observe(ctx, execution("qg-1", "retrying"))
	if stalled("first retrying round") {
		t.Fatal("one round that did not finish, after hours of rounds that did, is flagged as a stall")
	}

	// And it keeps not finishing for longer than the budget.
	at.at = at.at.Add(budget + time.Minute)
	tracker.Observe(ctx, execution("qg-1", "retrying"))
	if !stalled("retrying past the budget") {
		t.Fatal("rounds that have not finished for longer than the budget are not flagged")
	}

	// A degraded completion ends the failing stretch, though not the anomaly.
	tracker.Observe(ctx, completion("qg-1", "COMPLETED_WITH_UNAVAILABLE", "8930"))
	if stalled("degraded completion after the stall") {
		t.Fatal("a round that ended, even degraded, left the object flagged as never ending")
	}

	// So does a blocked round: it never reached execution, which is a
	// different thing from reaching it and not finishing.
	tracker.Observe(ctx, execution("qg-1", "error"))
	at.at = at.at.Add(budget + time.Minute)
	tracker.Observe(ctx, execution("qg-1", "error"))
	if !stalled("error past the budget") {
		t.Fatal("a second failing stretch past the budget is not flagged")
	}
	for round := 0; round < DefaultBlockedRounds; round++ {
		tracker.Observe(ctx, runOutcome("qg-1", "source_error"))
	}
	if stalled("blocked after the stall") {
		t.Fatal("a blocked round left the object flagged as a round that never ends")
	}
	tracker.Observe(ctx, execution("qg-1", "incomplete"))
	if stalled("first incomplete round after being blocked") {
		t.Fatal("the failing clock was not restarted by the round after the blocked ones")
	}
}

// The start of the failing stretch travels with the anomaly, because the flag
// is derived where the view is served and the replica that observed the rounds
// is not the process that serves the view.
func TestAnomaliesCarryWhenTheirRoundsStoppedFinishing(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := context.Background()
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(ctx, completion("qg-1", "COMPLETED_WITH_UNAVAILABLE", "8930"))
	}
	if got := tracker.Anomalies()[0].FailingSince; !got.IsZero() {
		t.Fatalf("failing since = %v after completed rounds, want none", got)
	}
	at.at = now.Add(time.Hour)
	tracker.Observe(ctx, execution("qg-1", "retrying"))
	at.at = now.Add(2 * time.Hour)
	tracker.Observe(ctx, execution("qg-1", "retrying"))
	anomaly := tracker.Anomalies()[0]
	if !anomaly.FailingSince.Equal(now.Add(time.Hour)) {
		t.Fatalf("failing since = %v, want the first round that did not finish at %v", anomaly.FailingSince, now.Add(time.Hour))
	}
	if !anomaly.Since.Equal(now) {
		t.Fatalf("since = %v, want the anomaly's start unchanged at %v", anomaly.Since, now)
	}
	encoded, err := json.Marshal(anomaly)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Anomaly
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.FailingSince.Equal(anomaly.FailingSince) {
		t.Fatalf("failing since after the wire = %v, want %v", decoded.FailingSince, anomaly.FailingSince)
	}
	tracker.Observe(ctx, completion("qg-1", "COMPLETED_WITH_UNAVAILABLE", "8930"))
	if got := tracker.Anomalies()[0].FailingSince; !got.IsZero() {
		t.Fatalf("failing since = %v after a round ended, want none", got)
	}
}

// A restart brings back the last completion, never an execution that did not
// finish, so nothing that survived can say when a failing stretch began. The
// restored object is anomalous from where the evidence is, and not stalled
// until this process has watched it fail for the budget.
func TestARestoredObjectIsNotStalledUntilThisProcessSeesItFail(t *testing.T) {
	const budget = 10 * time.Minute
	at := &clock{at: now}
	tracker := newTracker(t, at)
	if !tracker.Restore("qg-1", RestoredState{LastCompletion: "COMPLETED_WITH_UNAVAILABLE", NextSlot: now.Add(-2 * time.Hour)}, now, 0) {
		t.Fatal("restore refused a persisted degraded completion")
	}
	anomalies := tracker.Anomalies()
	MarkStalled(anomalies, at.at, budget)
	if len(anomalies) != 1 || anomalies[0].Stalled || !anomalies[0].FailingSince.IsZero() {
		t.Fatalf("anomalies = %+v, want the restored object reported and not stalled", anomalies)
	}
	tracker.Observe(context.Background(), execution("qg-1", "error"))
	at.at = at.at.Add(budget + time.Minute)
	tracker.Observe(context.Background(), execution("qg-1", "error"))
	anomalies = tracker.Anomalies()
	MarkStalled(anomalies, at.at, budget)
	if len(anomalies) != 1 || !anomalies[0].Stalled || !anomalies[0].FailingSince.Equal(now) {
		t.Fatalf("anomalies = %+v, want the object stalled from the first failure this process saw", anomalies)
	}
}
