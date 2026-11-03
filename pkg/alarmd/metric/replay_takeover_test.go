// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Slot due before a takeover is counted under what became of it, and both
// outcomes are there at zero before either happens.
func TestTakeoverSlotsAreCountedByOutcome(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	if got := testutil.CollectAndCount(recorder.phaseTwo.replayTakeovers); got != len(observability.ReplayTakeoverOutcomes) {
		t.Fatalf("series before any takeover = %d, want one per outcome", got)
	}
	for _, outcome := range []string{observability.ReplayTakeoverReplayed, observability.ReplayTakeoverReplayed, observability.ReplayTakeoverAgeExceeded} {
		recorder.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageReplayTakeover, Result: observability.ResultSuccess,
			ReplayTakeover: &observability.ReplayTakeoverFacts{Outcome: outcome},
		})
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.replayTakeovers.WithLabelValues(observability.ReplayTakeoverReplayed)); got != 2 {
		t.Fatalf("replayed = %v, want 2", got)
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.replayTakeovers.WithLabelValues(observability.ReplayTakeoverAgeExceeded)); got != 1 {
		t.Fatalf("age_exceeded = %v, want 1", got)
	}
}

// A takeover round sent to replay before it started is counted as diverted,
// beside the rounds replayed after they were missed.
func TestADivertedTakeoverRoundIsCounted(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	// Read off the scrape before anything touches the label, so the series is
	// there at zero because it was created, not because it was asked for.
	if got := testutil.CollectAndCount(recorder.phaseTwo.replayTakeovers); got != 3 {
		t.Fatalf("series before any takeover = %d, want replayed, age_exceeded and diverted", got)
	}
	for range 3 {
		recorder.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageReplayTakeover, Result: observability.ResultSuccess,
			ReplayTakeover: &observability.ReplayTakeoverFacts{Outcome: observability.ReplayTakeoverDiverted, RemainingSeconds: 1.5, FloorSeconds: 5},
		})
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.replayTakeovers.WithLabelValues(observability.ReplayTakeoverDiverted)); got != 3 {
		t.Fatalf("diverted = %v, want 3", got)
	}
}

// A Slot sent to replay because its read would have started too late is
// counted by its reason, a series that is there at zero from startup, and not
// under the takeover counter.
func TestASlotDivertedForReadinessIsCounted(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	if got := testutil.CollectAndCount(recorder.phaseTwo.slotDiversions); got != 1 {
		t.Fatalf("series before any diversion = %d, want readiness_late", got)
	}
	for range 2 {
		recorder.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageSlotDiverted, Result: observability.ResultSuccess,
			SlotDiversion: &observability.SlotDiversionFacts{Reason: observability.SlotDiversionReadinessLate, RemainingSeconds: 0.4, BoundSeconds: 5,
				BoundSource: observability.TakeoverFloorFallback},
		})
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.slotDiversions.WithLabelValues(observability.SlotDiversionReadinessLate)); got != 2 {
		t.Fatalf("readiness_late = %v, want 2", got)
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.replayTakeovers.WithLabelValues(observability.ReplayTakeoverDiverted)); got != 0 {
		t.Fatalf("takeover diverted = %v, want 0", got)
	}
}
