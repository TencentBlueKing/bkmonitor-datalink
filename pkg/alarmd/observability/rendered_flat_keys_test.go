// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

// renderObservation returns the line the logger actually writes.
//
// Every case in this file reads the rendered line rather than the fact
// struct. A field can be set on the struct, carried all the way to the
// observer, and never appear on any line -- the renderer here emits a chosen
// list of keys, not the struct -- and that has now happened twice: the range
// gate's two bounds were on the facts and on no line, and held_by rode inside
// one cohort's nested bundle so the Query Groups that most needed it had no
// cause on their completions. A case that asserts on the struct passes in
// both of those states.
func renderObservation(t *testing.T, observation Observation) map[string]any {
	t.Helper()
	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1000})
	if err != nil {
		t.Fatalf("NewWindowLogLimiter() error = %v", err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatalf("NewBoundedLogPolicy() error = %v", err)
	}
	NewLoggingObserver(New(ComponentScheduler, &output), policy).Observe(context.Background(), observation)
	if output.Len() == 0 {
		t.Fatal("the observation was not written at all")
	}
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode log: %v: %s", err, output.String())
	}
	return event
}

// The range gate's two candidate bounds reach the line.
//
// They shipped on RangeGateFacts and were never rendered, so the reading that
// went out said steps_below_one and could not say which of the two bounds held
// the range below a single Slot -- the difference between a Query Group barely
// past its window and one whose own deadline has only just passed.
func TestTheRangeGateBoundsAreOnTheRenderedLine(t *testing.T) {
	event := renderObservation(t, Observation{
		Component: ComponentScheduler, Stage: StageRangeGateDecided, Result: ResultDegraded,
		RangeGate: &RangeGateFacts{
			Outcome: RangeGateStepsBelowOne, ProgressNextSlot: 600, ExpectedNextSlot: 600,
			BoundsKnown: true, DistanceBound: 0, DeadlineBound: 2,
		},
	})
	for key, want := range map[string]any{
		"range_gate":                RangeGateStepsBelowOne,
		"range_gate_bounds_known":   true,
		"range_gate_distance_bound": float64(0),
		"range_gate_deadline_bound": float64(2),
	} {
		if event[key] != want {
			t.Fatalf("line[%q] = %#v, want %#v; line=%#v", key, event[key], want, event)
		}
	}
}

// A refusal that never computed the bounds does not print them.
//
// Zeroes here would be two numbers that do not exist, and a reader dividing
// the refused population by which bound held would put part of it in a bucket
// meaning "not applicable".
func TestARangeGateRefusalWithoutBoundsPrintsNone(t *testing.T) {
	event := renderObservation(t, Observation{
		Component: ComponentScheduler, Stage: StageRangeGateDecided, Result: ResultDegraded,
		RangeGate: &RangeGateFacts{Outcome: RangeGateNoRangeFlight, ProgressNextSlot: 600, ExpectedNextSlot: 600},
	})
	for _, key := range []string{"range_gate_bounds_known", "range_gate_distance_bound", "range_gate_deadline_bound"} {
		if _, present := event[key]; present {
			t.Fatalf("line carries %q for a refusal decided before either bound was computed: %#v", key, event)
		}
	}
}

// A completion line carries what held the previous round, on every cohort.
//
// The first version put it inside short_period_completion, which only the ten,
// fifteen and thirty second Query Groups have. Production read twenty
// GAP_SKIPPED completions and nineteen of them -- every Query Group on sixty
// seconds or slower, which is the bulk of the ones being skipped -- carried no
// cause at all. The fixture here is deliberately a Slot with no short-period
// bundle.
func TestASlowCohortCompletionCarriesHeldByAsFlatKeys(t *testing.T) {
	event := renderObservation(t, Observation{
		Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultSuccess,
		HeldBy: &HeldByFacts{
			Decision: "query_cooldown", AtUnixMilli: 1_700_000_031_200,
			QueryCooldownFailures: 14, QueryCooldownUntilMilli: 1_700_000_240_000,
		},
	})
	if _, bundled := event["short_period_completion"]; bundled {
		t.Fatalf("this fixture is meant to have no short-period bundle: %#v", event)
	}
	for key, want := range map[string]any{
		"held_by":                   "query_cooldown",
		"held_by_at":                float64(1_700_000_031_200),
		"held_by_cooldown_failures": float64(14),
		"held_by_cooldown_until":    float64(1_700_000_240_000),
	} {
		if event[key] != want {
			t.Fatalf("line[%q] = %#v, want %#v; line=%#v", key, event[key], want, event)
		}
	}
}

// "Nothing held it" is a word on the line, not an absent key.
//
// This is what production disproved: held_by never appeared with the value
// none anywhere, because the reader returned nil for it and the key was simply
// dropped. An absent key cannot be told apart from a build that does not
// report held_by, and the share of Slots that nothing held is the denominator
// the other words are read against.
func TestHeldByNothingIsWrittenAsAWord(t *testing.T) {
	event := renderObservation(t, Observation{
		Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultSuccess,
		HeldBy: &HeldByFacts{Decision: HeldByNothing, AtUnixMilli: 1_700_000_031_200},
	})
	if event["held_by"] != HeldByNothing {
		t.Fatalf("line[held_by] = %#v, want the word %q; line=%#v", event["held_by"], HeldByNothing, event)
	}
	// And it carries neither the cooldown's numbers nor the readiness instant.
	for _, key := range []string{"held_by_cooldown_failures", "held_by_cooldown_until", "held_by_ready_at"} {
		if _, present := event[key]; present {
			t.Fatalf("line carries %q beside the word for nothing: %#v", key, event)
		}
	}
}
