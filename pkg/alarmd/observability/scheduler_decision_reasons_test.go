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
	"testing"
	"time"
)

// Every word a range gate or a replay expiry puts in its facts is a word the
// reason field keeps: it normalizes to itself on a degraded result, and the
// log limiter has a bucket for it (a reason outside the vocabulary is a line
// the scoped policy never admits).
func TestEverySchedulerDecisionWordSurvivesAsAReasonCode(t *testing.T) {
	t.Parallel()

	if len(SchedulerDecisionReasons) != len(RangeGateOutcomes)+len(ReplayExpiryReasons) {
		t.Fatalf("scheduler decision reasons=%d, want every range gate outcome (%d) and replay expiry reason (%d)",
			len(SchedulerDecisionReasons), len(RangeGateOutcomes), len(ReplayExpiryReasons))
	}
	logged := map[ReasonCode]bool{}
	for _, reason := range AllLogReasons() {
		logged[reason] = true
	}
	for _, reason := range SchedulerDecisionReasons {
		if got := NormalizeReason(reason, ResultDegraded); got != reason {
			t.Fatalf("%q normalizes to %q on a degraded result, want itself", reason, got)
		}
		if !logged[reason] {
			t.Fatalf("%q is not a log reason: the limiter has no bucket for it and the line is never written", reason)
		}
	}
	// The word is counted by its own family (range gate outcome, replay
	// expiry reason), not by the generic stage counter: these are phase-two
	// stages, off that counter by design.
	if IsGenericMetricComponentStage(ComponentScheduler, StageRangeGateDecided) || IsGenericMetricComponentStage(ComponentScheduler, StageReplayExpired) {
		t.Fatal("a scheduler decision stage is on the generic stage counter; its words would become a reason label there")
	}
	// The vocabulary does not admit a word the lists do not carry.
	if got := NormalizeReason("gate_word_nobody_produces", ResultDegraded); got != ReasonOther {
		t.Fatalf("an unknown word normalizes to %q, want %s", got, ReasonOther)
	}
}

// The line as written: reason_code is the word, and it is the same word the
// facts carry, so a grep on either field finds the same lines.
func TestTheRangeGateLineWritesItsWordInBothPlaces(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	limiter, err := newScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 600, MaxScopes: 16}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewScopedBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	observer := NewLoggingObserver(New("alarmd", &output), policy)
	observer.Observe(context.Background(), Observation{
		Component: ComponentScheduler, Stage: StageRangeGateDecided, Result: ResultDegraded,
		ReasonCode: ReasonCode(RangeGateStepsBelowOne), Direction: DirectionInternal,
		Trace:     TraceFields{QueryGroupKey: "qg-a"},
		RangeGate: &RangeGateFacts{Outcome: RangeGateStepsBelowOne, BoundsKnown: true, DeadlineBound: 2},
	})
	observer.Observe(context.Background(), Observation{
		Component: ComponentScheduler, Stage: StageReplayExpired, Result: ResultDegraded,
		ReasonCode: ReasonCode(ReplayExpiryReasons[1]), Direction: DirectionInternal,
		Trace:        TraceFields{QueryGroupKey: "qg-a"},
		ReplayExpiry: &ReplayExpiryFacts{Reason: ReplayExpiryReasons[1], Distance: 7},
	})
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("lines=%d, want the gate line and the expiry line:\n%s", len(lines), output.String())
	}
	var gate, expiry map[string]any
	if err := json.Unmarshal(lines[0], &gate); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(lines[1], &expiry); err != nil {
		t.Fatal(err)
	}
	if gate["reason_code"] != RangeGateStepsBelowOne || gate["range_gate"] != RangeGateStepsBelowOne {
		t.Fatalf("gate line=%v, want reason_code and range_gate both %s", gate, RangeGateStepsBelowOne)
	}
	if expiry["reason_code"] != ReplayExpiryReasons[1] || expiry["replay_expiry_reason"] != ReplayExpiryReasons[1] {
		t.Fatalf("expiry line=%v, want reason_code and replay_expiry_reason both %s", expiry, ReplayExpiryReasons[1])
	}
}
