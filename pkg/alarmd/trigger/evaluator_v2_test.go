// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package trigger

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestEvaluatorV2MatchesPythonTriggerAndRecoveryWindows(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 9, 3, 2, 2, nil)})
	tests := []struct {
		name      string
		source    int64
		fact      string
		points    map[int64]bool
		want      string
		wantCount uint32
		wantMiss  uint32
	}{
		{
			name: "current anomaly reaches N of M", source: 300, fact: DetectionAnomalous,
			points: map[int64]bool{120: false, 180: false, 240: true, 300: true},
			want:   contract.LevelResultAbnormal, wantCount: 2,
		},
		{
			name: "current normal never emits abnormal even when history triggers", source: 300, fact: DetectionNormal,
			points: map[int64]bool{120: false, 180: true, 240: true, 300: false},
			want:   contract.LevelResultNormal, wantCount: 2,
		},
		{
			name: "current anomaly may recover when consecutive trigger windows miss", source: 360, fact: DetectionAnomalous,
			points: map[int64]bool{180: false, 240: false, 300: false, 360: true},
			want:   contract.LevelResultRecovery, wantCount: 1, wantMiss: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := requestV2(t, plan, test.source, []DetectionFact{factV2(plan.Levels()[0], test.fact)}, []LevelHistory{{
				LevelID: 5, View: pointHistory{step: 60, points: test.points},
			}}, activeFactsV2(t, plan, test.source))
			result, err := EvaluateV2(request)
			if err != nil {
				t.Fatalf("EvaluateV2() error = %v", err)
			}
			if result.Completion != CompletionEvaluated || result.RecordResult != test.want || len(result.LevelOutcomes) != 1 {
				t.Fatalf("EvaluateV2() = %#v", result)
			}
			outcome := result.LevelOutcomes[0]
			if outcome.Result != test.want || outcome.StateDisposition != StateAdvance || outcome.DecisionWindow == nil ||
				outcome.DecisionWindow.Trigger.ObservedAnomalies != test.wantCount ||
				outcome.DecisionWindow.Recovery.ObservedConsecutiveMisses != test.wantMiss {
				t.Fatalf("level outcome = %#v", outcome)
			}
			wantEvent := test.want != contract.LevelResultNormal
			if (result.TriggerEvent != nil) != wantEvent {
				t.Fatalf("TriggerEvent present = %t, want %t", result.TriggerEvent != nil, wantEvent)
			}
		})
	}
}

func TestEvaluatorV2TreatsRequiredAnomaliesAboveWindowAsNeverTriggered(t *testing.T) {
	for _, test := range []struct {
		name      string
		level     contract.LevelIRV2
		want      string
		wantEvent bool
	}{
		{name: "recovery disabled", level: levelWithoutRecoveryV2(5, 1, 2, 3), want: contract.LevelResultNormal},
		{name: "recovery enabled", level: levelV2(5, 1, 2, 3, 1, nil), want: contract.LevelResultRecovery, wantEvent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := compilePlanV2(t, []contract.LevelIRV2{test.level})
			level := plan.Levels()[0]
			if level.RequiredDetectHistoryPoints() != 2 {
				t.Fatalf("RequiredDetectHistoryPoints() = %d, want trigger window 2", level.RequiredDetectHistoryPoints())
			}
			request := requestV2(t, plan, 300,
				[]DetectionFact{factV2(level, DetectionAnomalous)},
				[]LevelHistory{{LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{240: true, 300: true}}}},
				activeFactsV2(t, plan, 300),
			)

			result, err := EvaluateV2(request)
			if err != nil {
				t.Fatalf("EvaluateV2() error = %v", err)
			}
			if result.RecordResult != test.want || (result.TriggerEvent != nil) != test.wantEvent ||
				len(result.LevelOutcomes) != 1 || result.LevelOutcomes[0].Result != test.want {
				t.Fatalf("EvaluateV2() = %#v, want %s event=%t", result, test.want, test.wantEvent)
			}
		})
	}
}

func TestEvaluatorV2KeepsWarmingAndLevelIsolationExplicit(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{
		levelV2(1, 20, 2, 2, 1, staticUptimeV2()),
		levelV2(5, 1, 2, 2, 1, nil),
	})
	levels := plan.Levels()
	source := int64(64800)
	facts := effectiveFactsV2(t, plan, source, func(ref string) (*time.Location, error) { return time.UTC, nil })
	if facts[0].Fact.Status() != strategy.EffectiveTimeInactive || facts[1].Fact.Status() != strategy.EffectiveTimeActive {
		t.Fatalf("effective facts = %#v", facts)
	}
	result, err := EvaluateV2(requestV2(t, plan, source,
		[]DetectionFact{factV2(levels[0], DetectionAnomalous), factV2(levels[1], DetectionAnomalous)},
		[]LevelHistory{
			{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source - 60: true, source: true}}},
			{LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{source - 60: true, source: true}}},
		}, facts))
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if result.RecordResult != contract.LevelResultAbnormal || result.TriggerEvent == nil || len(result.TriggerEvent.LevelResults) != 1 || result.TriggerEvent.PrimaryLevelID != 5 {
		t.Fatalf("result = %#v", result)
	}
	if result.LevelOutcomes[0].SuppressedReason != contract.ReasonEffectiveTimeInactive || result.LevelOutcomes[0].StateDisposition != StateAdvance {
		t.Fatalf("inactive outcome = %#v", result.LevelOutcomes[0])
	}

	// Two consecutive windows required and one observed position to offer, so
	// the recovery this Level would need is not there. The fixture asked for
	// one window and had two positions until decision-022, which was enough
	// for recovery once an incomplete window stopped forbidding it - and this
	// case is about warming reporting itself, not about recovery, so it says
	// so by not satisfying it.
	warming := requestV2(t, compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 3, 2, 2, nil)}), 300,
		[]DetectionFact{}, nil, nil)
	warming.Record.LevelFacts = []DetectionFact{factV2(warming.Plan.Levels()[0], DetectionNormal)}
	warming.Histories = []LevelHistory{{LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{300: false}}}}
	warming.EffectiveTimeFacts = activeFactsV2(t, warming.Plan, 300)
	warmingResult, err := EvaluateV2(warming)
	if err != nil {
		t.Fatalf("warming EvaluateV2() error = %v", err)
	}
	if warmingResult.Completion != CompletionUnavailable || warmingResult.LevelOutcomes[0].UnavailableReason != contract.ReasonHistoryWarming || warmingResult.LevelOutcomes[0].StateDisposition != StateAdvance {
		t.Fatalf("warming result = %#v", warmingResult)
	}
}

func TestEvaluatorV2UnknownAndUnavailableFreezeWithoutBlockingSibling(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{
		levelV2(1, 10, 1, 1, 1, staticUptimeV2()),
		levelV2(5, 1, 1, 1, 1, nil),
	})
	levels := plan.Levels()
	source := int64(36000)
	effective := effectiveFactsV2(t, plan, source, func(ref string) (*time.Location, error) {
		return nil, strategy.ErrEffectiveTimeUnknown
	})
	result, err := EvaluateV2(requestV2(t, plan, source,
		[]DetectionFact{
			factV2(levels[0], DetectionAnomalous),
			unavailableFactV2(levels[1], contract.ReasonRequiredValueMissing),
		},
		[]LevelHistory{
			{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source: true}}},
			{LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
		}, effective))
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if result.Completion != CompletionUnavailable || result.TriggerEvent != nil || len(result.LevelOutcomes) != 2 {
		t.Fatalf("result = %#v", result)
	}
	if result.LevelOutcomes[0].UnavailableReason != contract.ReasonEffectiveTimeUnknown || result.LevelOutcomes[0].StateDisposition != StateFreeze ||
		result.LevelOutcomes[1].UnavailableReason != contract.ReasonRequiredValueMissing || result.LevelOutcomes[1].StateDisposition != StateFreeze {
		t.Fatalf("outcomes = %#v", result.LevelOutcomes)
	}
}

func TestStateEligibilityV2(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, staticUptimeV2())})
	level := plan.Levels()[0]
	active := effectiveFactsV2(t, plan, 36000, func(string) (*time.Location, error) { return time.UTC, nil })[0].Fact
	inactive := effectiveFactsV2(t, plan, 64800, func(string) (*time.Location, error) { return time.UTC, nil })[0].Fact
	unknown := effectiveFactsV2(t, plan, 36000, func(string) (*time.Location, error) {
		return nil, strategy.ErrEffectiveTimeUnknown
	})[0].Fact
	errorFact := unavailableFactV2(level, contract.ReasonRequiredValueNormalizationFailed)
	errorFact.Result = DetectionError

	tests := []struct {
		name           string
		evaluationTime int64
		detect         DetectionFact
		effective      strategy.EffectiveTimeFact
		want           string
	}{
		{name: "anomalous active", evaluationTime: 36000, detect: factV2(level, DetectionAnomalous), effective: active, want: StateAdvance},
		{name: "normal active", evaluationTime: 36000, detect: factV2(level, DetectionNormal), effective: active, want: StateAdvance},
		{name: "anomalous inactive", evaluationTime: 64800, detect: factV2(level, DetectionAnomalous), effective: inactive, want: StateAdvance},
		{name: "normal inactive", evaluationTime: 64800, detect: factV2(level, DetectionNormal), effective: inactive, want: StateAdvance},
		{name: "unavailable active", evaluationTime: 36000, detect: unavailableFactV2(level, contract.ReasonRequiredValueMissing), effective: active, want: StateFreeze},
		{name: "error inactive", evaluationTime: 64800, detect: errorFact, effective: inactive, want: StateFreeze},
		{name: "anomalous unknown", evaluationTime: 36000, detect: factV2(level, DetectionAnomalous), effective: unknown, want: StateFreeze},
		{name: "unavailable unknown", evaluationTime: 36000, detect: unavailableFactV2(level, contract.ReasonRequiredValueMissing), effective: unknown, want: StateFreeze},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			eligibility, err := EvaluateStateEligibilityV2(test.evaluationTime, level, test.detect, test.effective)
			if err != nil || eligibility.StateDisposition() != test.want {
				t.Fatalf("EvaluateStateEligibilityV2() = %#v, %v; want %s", eligibility, err, test.want)
			}
		})
	}

	otherPlan := compilePlanV2(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil)})
	otherFact := activeFactsV2(t, otherPlan, 36000)[0].Fact
	for _, test := range []struct {
		name           string
		evaluationTime int64
		detect         DetectionFact
		effective      strategy.EffectiveTimeFact
	}{
		{name: "requirement mismatch", evaluationTime: 36000, detect: factV2(level, DetectionNormal), effective: otherFact},
		{name: "expired validity interval", evaluationTime: active.ValidUntil(), detect: factV2(level, DetectionNormal), effective: active},
		{name: "invalid Detect result", evaluationTime: 36000, detect: DetectionFact{Definition: level.Definition(), DetectFingerprint: level.Fingerprints().Detect, Result: "INVALID"}, effective: active},
	} {
		t.Run(test.name, func(t *testing.T) {
			eligibility, err := EvaluateStateEligibilityV2(test.evaluationTime, level, test.detect, test.effective)
			if !errors.Is(err, ErrInvariantV2) || eligibility != (StateEligibilityV2{}) {
				t.Fatalf("EvaluateStateEligibilityV2() = %#v, %v; want zero result and ErrInvariantV2", eligibility, err)
			}
		})
	}
}

func TestEvaluatorV2RejectsBrokenCrossModuleInvariants(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil)})
	request := requestV2(t, plan, 300, nil,
		[]LevelHistory{{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{300: true}}}},
		activeFactsV2(t, plan, 300))
	if _, err := EvaluateV2(request); !errors.Is(err, ErrInvariantV2) {
		t.Fatalf("missing fact error = %v", err)
	}
	request.Record.LevelFacts = []DetectionFact{factV2(plan.Levels()[0], DetectionAnomalous), factV2(plan.Levels()[0], DetectionNormal)}
	if _, err := EvaluateV2(request); !errors.Is(err, ErrInvariantV2) {
		t.Fatalf("duplicate fact error = %v", err)
	}
}

func TestEvaluatorV2RejectsEffectiveTimeFactRequirementMismatch(t *testing.T) {
	t.Run("facts exchanged between Levels", func(t *testing.T) {
		plan := compilePlanV2(t, []contract.LevelIRV2{
			levelV2(1, 1, 1, 1, 1, staticUptimeV2()),
			levelV2(5, 2, 1, 1, 1, nil),
		})
		levels := plan.Levels()
		facts := activeFactsV2(t, plan, 36000)
		facts[0].Fact, facts[1].Fact = facts[1].Fact, facts[0].Fact
		request := requestV2(t, plan, 36000,
			[]DetectionFact{factV2(levels[0], DetectionAnomalous), factV2(levels[1], DetectionAnomalous)},
			[]LevelHistory{
				{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{36000: true}}},
				{LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{36000: true}}},
			}, facts)
		if _, err := EvaluateV2(request); !errors.Is(err, ErrInvariantV2) {
			t.Fatalf("exchanged EffectiveTime facts error = %v", err)
		}
	})

	t.Run("fact from old requirement revision", func(t *testing.T) {
		oldPlan := compilePlanV2(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, staticUptimeV2())})
		oldFact := activeFactsV2(t, oldPlan, 36000)[0]
		newUptime := map[string]any{
			"time_ranges":      []any{map[string]any{"start": "08:00", "end": "18:00"}},
			"active_calendars": []any{},
			"calendars":        []any{},
		}
		plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, newUptime)})
		level := plan.Levels()[0]
		oldFact.LevelID = level.Definition().LevelID
		request := requestV2(t, plan, 36000,
			[]DetectionFact{factV2(level, DetectionAnomalous)},
			[]LevelHistory{{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{36000: true}}}},
			[]LevelEffectiveTimeFact{oldFact})
		if _, err := EvaluateV2(request); !errors.Is(err, ErrInvariantV2) {
			t.Fatalf("old EffectiveTime fact revision error = %v", err)
		}
	})

	t.Run("unavailable Detect facts do not bypass exchanged requirements", func(t *testing.T) {
		plan := compilePlanV2(t, []contract.LevelIRV2{
			levelV2(1, 1, 1, 1, 1, staticUptimeV2()),
			levelV2(5, 2, 1, 1, 1, nil),
		})
		levels := plan.Levels()
		facts := activeFactsV2(t, plan, 36000)
		facts[0].Fact, facts[1].Fact = facts[1].Fact, facts[0].Fact
		request := requestV2(t, plan, 36000,
			[]DetectionFact{
				unavailableFactV2(levels[0], contract.ReasonRequiredValueMissing),
				unavailableFactV2(levels[1], contract.ReasonRequiredValueMissing),
			},
			[]LevelHistory{
				{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{36000: false}}},
				{LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{36000: false}}},
			}, facts)
		result, err := EvaluateV2(request)
		assertInvariantWithoutResultV2(t, result, err)
	})

	t.Run("error Detect fact does not bypass old requirement", func(t *testing.T) {
		oldPlan := compilePlanV2(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, staticUptimeV2())})
		oldFact := activeFactsV2(t, oldPlan, 36000)[0]
		newUptime := map[string]any{
			"time_ranges":      []any{map[string]any{"start": "08:00", "end": "18:00"}},
			"active_calendars": []any{},
			"calendars":        []any{},
		}
		plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, newUptime)})
		level := plan.Levels()[0]
		oldFact.LevelID = level.Definition().LevelID
		detectFact := unavailableFactV2(level, contract.ReasonRequiredValueNormalizationFailed)
		detectFact.Result = DetectionError
		request := requestV2(t, plan, 36000,
			[]DetectionFact{detectFact},
			[]LevelHistory{{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{36000: false}}}},
			[]LevelEffectiveTimeFact{oldFact})
		result, err := EvaluateV2(request)
		assertInvariantWithoutResultV2(t, result, err)
	})
}

func assertInvariantWithoutResultV2(t *testing.T, result EvaluationResultV2, err error) {
	t.Helper()
	if !errors.Is(err, ErrInvariantV2) || result.Completion != "" || result.RecordResult != "" ||
		len(result.LevelOutcomes) != 0 || result.TriggerEvent != nil || result.Counts != (EvaluationCountsV2{}) {
		t.Fatalf("EvaluateV2() result = %#v, error = %v; want zero result and ErrInvariantV2", result, err)
	}
}

func TestEvaluatorV2UsesStableM0EventIdentity(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 1, 1, 1, nil)})
	history := []LevelHistory{{LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{300: true}}}}
	first := requestV2(t, plan, 300, []DetectionFact{factV2(plan.Levels()[0], DetectionAnomalous)}, history, activeFactsV2(t, plan, 300))
	second := first
	second.ExecutionID = "execution-replay"
	firstResult, err := EvaluateV2(first)
	if err != nil {
		t.Fatal(err)
	}
	secondResult, err := EvaluateV2(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.TriggerEvent.EventID != secondResult.TriggerEvent.EventID || firstResult.TriggerEvent.EventSemanticDigest != secondResult.TriggerEvent.EventSemanticDigest {
		t.Fatalf("event identity changed: %s / %s", firstResult.TriggerEvent.EventID, secondResult.TriggerEvent.EventID)
	}
	if firstResult.TriggerEvent.Trace.ExecutionID == secondResult.TriggerEvent.Trace.ExecutionID {
		t.Fatal("execution trace did not preserve replay execution id")
	}
}

func TestEvaluatorV2AggregatesDynamicLevelsByResultThenPriority(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{
		levelV2(1, 20, 1, 1, 1, nil),
		levelV2(5, 1, 1, 1, 1, nil),
		levelV2(7, 1, 1, 1, 1, nil),
	})
	levels := plan.Levels()
	request := requestV2(t, plan, 300,
		[]DetectionFact{
			factV2(levels[0], DetectionNormal),
			factV2(levels[1], DetectionAnomalous),
			factV2(levels[2], DetectionAnomalous),
		},
		[]LevelHistory{
			{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{300: false}}},
			{LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{300: true}}},
			{LevelID: 7, View: pointHistory{step: 60, points: map[int64]bool{300: true}}},
		}, activeFactsV2(t, plan, 300))
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if result.RecordResult != contract.LevelResultAbnormal || result.TriggerEvent == nil || result.TriggerEvent.PrimaryLevelID != 5 ||
		len(result.TriggerEvent.LevelResults) != 3 || result.TriggerEvent.LevelResults[0].Result != contract.LevelResultRecovery ||
		result.TriggerEvent.LevelResults[1].LevelID != 5 || result.TriggerEvent.LevelResults[2].LevelID != 7 {
		t.Fatalf("aggregated result = %#v", result)
	}
}

// An incomplete window may escalate and may close what it opened, but may not
// call the Level normal, and may not claim a recovery it has no evidence for.
//
// This case used to be named for the rule it pins and pinned a narrower one:
// WARMING and GAPPED permitted ABNORMAL alone. decision-022 section 9.1
// overturned that half deliberately, on numbers the original trade did not
// have - 35 of 3305 strategies have a window longer than the interval between
// releases and so never reach FULL, so "wait for the window to fill" is not a
// wait for them, it is never. The other half stands: an incomplete window
// still cannot say a Level is fine.
//
// The two recovery branches differ by one position, because that is the whole
// question. Evidence one short must be refused, or the rule reads as "any
// incomplete window may claim recovery".
//
// What counts as evidence is a window, not a position. Recovery asks for N
// consecutive windows that did not trigger, and a window did not trigger only
// when its anomalies plus its holes stay under the threshold - if every hole
// could have been the anomaly that fired it, the window has not answered. So
// this Level, at window 3 and threshold 2, needs a third observed position to
// reach two answered windows: with two it can answer the newest window (one
// hole, under the threshold) and not the one behind it (two holes, at it).
// The first draft of this test asked for two positions and called them two
// windows, which is the same conflation the walk itself made.
func TestEvaluatorV2IncompleteWindowAllowsAbnormalAndEvidencedRecovery(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 3, 2, 2, nil)})
	level := plan.Levels()[0]
	request := requestV2(t, plan, 300, []DetectionFact{factV2(level, DetectionAnomalous)},
		[]LevelHistory{{LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{240: true, 300: true}}}},
		activeFactsV2(t, plan, 300))
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if result.RecordResult != contract.LevelResultAbnormal || result.TriggerEvent == nil ||
		result.LevelOutcomes[0].HistoryCompleteness != HistoryWarming {
		t.Fatalf("warming abnormal = %#v", result)
	}

	// Three observed, non-anomalous positions, which answer the two windows
	// this Level requires. The history is still short of FULL, and that no
	// longer stands in the way.
	request.Record.LevelFacts[0] = factV2(level, DetectionNormal)
	request.Histories[0].View = pointHistory{step: 60, points: map[int64]bool{180: false, 240: false, 300: false}}
	result, err = EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2(evidenced recovery) error = %v", err)
	}
	if result.RecordResult != contract.LevelResultRecovery || result.TriggerEvent == nil ||
		result.LevelOutcomes[0].HistoryCompleteness == HistoryFull {
		t.Fatalf("evidenced recovery on an incomplete window = %#v", result)
	}

	// One position short of it, and nothing else changed, so the evidence is
	// the only thing that can decide this. Two observed positions answer the
	// newest window and leave the one behind it at the threshold in holes,
	// which is one answered window against the two required.
	request.Histories[0].View = pointHistory{step: 60, points: map[int64]bool{240: false, 300: false}}
	result, err = EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2(recovery one short) error = %v", err)
	}
	if result.Completion != CompletionUnavailable || result.TriggerEvent != nil {
		t.Fatalf("recovery one position short still produced %#v; an incomplete window may not claim a "+
			"recovery it has not observed", result)
	}
}

// The walk keeps going past a window it could not answer, and it may reach as
// far back as the history is retained to do it.
//
// Both halves need a run whose last answered window lies *behind* a skipped
// one. Every other case in this file has its answered windows before the first
// skip, where stepping over a window and stopping at it are the same thing and
// a walk bounded at the required number of windows reaches just as far.
//
// Window 3, threshold 2, two consecutive windows required, so the history is
// retained for 3+2-1 = 4 window offsets. Positions 360 and 300 are observed,
// 240 and 180 are not, 120 and 60 are:
//
//	offset 0  {240,300,360}  2 observed, 1 hole   answered, miss 1
//	offset 1  {180,240,300}  1 observed, 2 holes  cannot answer, stepped over
//	offset 2  {120,180,240}  1 observed, 2 holes  cannot answer, stepped over
//	offset 3  { 60,120,180}  2 observed, 1 hole   answered, miss 2 -> RECOVERY
//
// Stopping at offset 1 leaves one miss, and so does a walk that only looks at
// two offsets. Either way the answer changes, which is what makes this case
// worth its fixture.
func TestEvaluatorV2RecoveryWalkPassesSkippedWindowsToReachTheRetainedOnes(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 3, 2, 2, nil)})
	level := plan.Levels()[0]
	history := pointHistory{step: 60, points: map[int64]bool{60: false, 120: false, 300: false, 360: false}}
	request := requestV2(t, plan, 360, []DetectionFact{factV2(level, DetectionNormal)},
		[]LevelHistory{{LevelID: 5, View: history}}, activeFactsV2(t, plan, 360))
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if result.RecordResult != contract.LevelResultRecovery {
		t.Fatalf("record result = %q, want RECOVERY: the walk stopped at the first window it could not "+
			"answer, or would not look past the windows it strictly needed", result.RecordResult)
	}
	recovery := result.LevelOutcomes[0].DecisionWindow.Recovery
	if recovery.ObservedConsecutiveMisses != 2 || recovery.SkippedWindows != 2 {
		t.Fatalf("recovery evidence = %+v, want two answered windows and two stepped over", recovery)
	}
	// The oldest window reached is offset 3's, which only exists because the
	// history is retained for the trigger window as well as the recovery run.
	if want := int64(180 - 3*60 + 1); recovery.OldestWindowStart != want {
		t.Fatalf("oldest window start = %d, want %d: the walk did not reach the last retained window",
			recovery.OldestWindowStart, want)
	}
}

// The walk reads the positions the record is retained for past the ones its
// window requires, and a recovery reachable only from those positions is the
// only thing that says so.
//
// Window 5, threshold 1, twenty consecutive windows required, on a one minute
// step: the window requires 5+19 = 24 positions and the compiler retains 38.
// Every position on the grid is observed and normal except the eighth back,
// which is missing:
//
//	offsets 0..3    whole windows                       answered, misses 1..4
//	offsets 4..8    the hole lies inside each of them   stepped over, 5 skipped
//	offsets 9..24   whole windows again                 answered, misses 5..20
//
// The twentieth answered window is reached at offset 24, past the 24 positions
// the window requires and inside the 38 retained. A walk bounded at the
// required size stops at offset 23 holding nineteen - one short - and the Level
// reports its incomplete history instead of the recovery.
//
// The hole is what separates the two bounds. Without it the twentieth answered
// window falls on offset 19 and either bound reaches it, which is why no case
// written before the retention grew a slack can tell them apart.
func TestEvaluatorV2RecoveryWalkReadsThePositionsRetainedPastTheWindow(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 5, 1, 20, nil)})
	level := plan.Levels()[0]
	requirement := level.StateRequirement()
	if requirement.RequiredDetectHistoryPoints != 24 || requirement.RetentionPoints != 38 {
		t.Fatalf("StateRequirement() = %+v, want 24 required and 38 retained: the distance between the "+
			"two is what this case walks into", requirement)
	}
	const source, step = int64(3600), int64(60)
	points := make(map[int64]bool, requirement.RetentionPoints)
	for offset := uint32(0); offset < requirement.RetentionPoints; offset++ {
		points[source-int64(offset)*step] = false
	}
	delete(points, source-8*step)

	request := requestV2(t, plan, source, []DetectionFact{factV2(level, DetectionNormal)},
		[]LevelHistory{{LevelID: 5, View: pointHistory{step: step, points: points}}}, activeFactsV2(t, plan, source))
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if result.RecordResult != contract.LevelResultRecovery {
		t.Fatalf("record result = %q, completion = %v, want RECOVERY: the walk stopped at the required "+
			"window instead of reading the positions retained past it", result.RecordResult, result.Completion)
	}
	recovery := result.LevelOutcomes[0].DecisionWindow.Recovery
	if recovery.ObservedConsecutiveMisses != 20 || recovery.SkippedWindows != 5 {
		t.Fatalf("recovery evidence = %+v, want twenty answered windows and five stepped over", recovery)
	}
}

func TestEvaluatorV2AllInactiveIsSuppressedNotNormal(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, staticUptimeV2())})
	level := plan.Levels()[0]
	source := int64(64800)
	result, err := EvaluateV2(requestV2(t, plan, source, []DetectionFact{factV2(level, DetectionAnomalous)},
		[]LevelHistory{{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source: true}}}},
		effectiveFactsV2(t, plan, source, func(string) (*time.Location, error) { return time.UTC, nil })))
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if result.Completion != CompletionSuppressed || result.RecordResult != "" || result.TriggerEvent != nil ||
		result.Counts.Suppressed != 1 || result.LevelOutcomes[0].SuppressedReason != contract.ReasonEffectiveTimeInactive {
		t.Fatalf("suppressed result = %#v", result)
	}
}

func TestEvaluatorV2EnforcesComputeAndEvidenceBudgets(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 3, 2, 2, nil)})
	level := plan.Levels()[0]
	request := requestV2(t, plan, 300, []DetectionFact{factV2(level, DetectionAnomalous)},
		[]LevelHistory{{LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{120: false, 180: false, 240: true, 300: true}}}},
		activeFactsV2(t, plan, 300))
	request.Limits.MaxComputeCost = 3
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2(exact compute) error = %v", err)
	}
	evidence, err := contract.CanonicalJSONV2(result.TriggerEvent.LevelResults)
	if err != nil {
		t.Fatal(err)
	}
	request.Limits.MaxEvidenceBytesPerEvent = len(evidence)
	if _, err := EvaluateV2(request); err != nil {
		t.Fatalf("EvaluateV2(exact evidence) error = %v", err)
	}
	request.Limits.MaxEvidenceBytesPerEvent--
	if _, err := EvaluateV2(request); !errors.Is(err, ErrInvariantV2) {
		t.Fatalf("evidence over budget error = %v", err)
	}
	request.Limits.MaxEvidenceBytesPerEvent = 64 << 10
	request.Limits.MaxComputeCost = 2
	if _, err := EvaluateV2(request); !errors.Is(err, ErrInvariantV2) {
		t.Fatalf("compute over budget error = %v", err)
	}
}

func TestEvaluatorV2DisabledRecoveryAndFuturePointsDoNotChangeCurrentWindow(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelWithoutRecoveryV2(5, 1, 2, 2)})
	level := plan.Levels()[0]
	request := requestV2(t, plan, 300, []DetectionFact{factV2(level, DetectionAnomalous)},
		[]LevelHistory{{LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{240: false, 300: true, 360: true}}}},
		activeFactsV2(t, plan, 300))
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if result.RecordResult != contract.LevelResultNormal || result.TriggerEvent != nil ||
		result.LevelOutcomes[0].DecisionWindow.Recovery.Enabled ||
		result.LevelOutcomes[0].DecisionWindow.Trigger.ObservedAnomalies != 1 {
		t.Fatalf("disabled recovery result = %#v", result)
	}
}

func TestCanonicalNormalizedValueV2(t *testing.T) {
	for _, value := range []string{"0.000000", "50.100000", "-0.000001"} {
		if !validCanonicalDecimalV2(value) {
			t.Fatalf("validCanonicalDecimalV2(%q) = false", value)
		}
	}
	for _, value := range []string{"", "1", ".000000", "01.000000", "1.00000", "1.0000000", "+1.000000", "-0.000000", "NaN"} {
		if validCanonicalDecimalV2(value) {
			t.Fatalf("validCanonicalDecimalV2(%q) = true", value)
		}
	}
}

func BenchmarkEvaluateV2(b *testing.B) {
	plan := compilePlanV2(b, []contract.LevelIRV2{levelV2(5, 1, 5, 3, 3, nil)})
	level := plan.Levels()[0]
	for _, benchmark := range []struct {
		name string
		fact string
	}{
		{name: "normal_no_event", fact: DetectionNormal},
		{name: "abnormal_event", fact: DetectionAnomalous},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			request := requestV2ForBenchmark(b, plan, level)
			request.Record.LevelFacts[0] = factV2(level, benchmark.fact)
			b.ReportAllocs()
			for index := 0; index < b.N; index++ {
				if _, err := EvaluateV2(request); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func requestV2ForBenchmark(b *testing.B, plan *strategy.CompiledPlan, level strategy.CompiledLevel) EvaluationRequestV2 {
	b.Helper()
	points := map[int64]bool{60: false, 120: false, 180: true, 240: true, 300: true, 360: true, 420: true}
	recordID, err := contract.DeriveRecordIDV2(strings.Repeat("c", 64), 420)
	if err != nil {
		b.Fatal(err)
	}
	return EvaluationRequestV2{
		TenantID: "default", BusinessID: "2", Plan: plan,
		Record:    DetectionRecord{RecordID: recordID, SourceTime: 420, ProjectedValues: []ProjectedValue{{CanonicalDecimal: "50.100000", Available: true}}, LevelFacts: []DetectionFact{factV2(level, DetectionAnomalous)}},
		RecordRef: contract.TriggerRecordRefV1{RecordID: recordID, SourceTime: 420, DimensionIdentityDigest: strings.Repeat("c", 64), Dimensions: map[string]json.RawMessage{"host": json.RawMessage(`"127.0.0.1"`)}},
		Observed:  contract.TriggerObservedV1{Values: map[string]json.RawMessage{"value": json.RawMessage(`50.1`)}, Unit: "percent"},
		Histories: []LevelHistory{{LevelID: 5, View: pointHistory{step: 60, points: points}}}, EffectiveTimeFacts: activeFactsBenchmark(b, plan, 420),
		EvaluationTime: 420, ExecutionID: "execution-1",
		Limits: EvaluationLimitsV2{MaxLevels: 16, MaxTriggerWindowSize: 64, MaxRecoveryConsecutiveWindows: 64, MaxRequiredHistoryPoints: 256, MaxLevelResultsPerEvent: 16, MaxEvidenceBytesPerEvent: 64 << 10, MaxComputeCost: 4096},
	}
}

func activeFactsBenchmark(b *testing.B, plan *strategy.CompiledPlan, evaluationTime int64) []LevelEffectiveTimeFact {
	b.Helper()
	provider := strategy.NewStaticScheduleProvider(nil)
	levels := plan.Levels()
	requests := make([]strategy.EffectiveTimeRequest, len(levels))
	for index, level := range levels {
		requests[index] = strategy.EffectiveTimeRequest{TenantID: "default", BusinessID: "2", EvaluationTime: evaluationTime, Requirement: level.EffectiveTimeRequirement()}
	}
	facts, err := provider.Resolve(context.Background(), requests)
	if err != nil {
		b.Fatal(err)
	}
	result := make([]LevelEffectiveTimeFact, len(levels))
	for index, level := range levels {
		result[index] = LevelEffectiveTimeFact{LevelID: level.Definition().LevelID, Fact: facts[index]}
	}
	return result
}

type pointHistory struct {
	step   int64
	points map[int64]bool
}

// CountObserved mirrors the real view: a position is observed when the history
// holds a point for it, whatever that point said.
//
// Counted over the range, the way CountAnomalies just above is, not by
// stepping the grid from fromTime. The window start the walk passes in is
// windowEnd - WindowSize*step + 1, which is one second after a grid position
// rather than on one, so a grid walk from there lands between the points and
// reports an entirely observed window as empty.
func (h pointHistory) CountObserved(fromTime, untilTime int64) uint32 {
	var count uint32
	for timestamp := range h.points {
		if timestamp >= fromTime && timestamp <= untilTime {
			count++
		}
	}
	return count
}

func (h pointHistory) Summarize(endTime int64, requiredPositions uint32) HistorySummary {
	start := endTime - int64(requiredPositions-1)*h.step
	summary := HistorySummary{Completeness: HistoryFull, WindowStart: start, WindowEnd: endTime}
	digest := sha256.New()
	var encoded [8]byte
	seen := false
	for timestamp := start; timestamp <= endTime; timestamp += h.step {
		anomalous, ok := h.points[timestamp]
		if !ok {
			if seen {
				summary.Completeness = HistoryGapped
			} else if summary.Completeness == HistoryFull {
				summary.Completeness = HistoryWarming
			}
			continue
		}
		seen = true
		summary.ValidPositions++
		if anomalous {
			summary.AnomalyCount++
			binary.BigEndian.PutUint64(encoded[:], uint64(timestamp))
			_, _ = digest.Write(encoded[:])
		}
	}
	copy(summary.AnomalyDigest[:], digest.Sum(nil))
	return summary
}

func (h pointHistory) CountAnomalies(fromTime, untilTime int64) uint32 {
	var count uint32
	for timestamp, anomalous := range h.points {
		if anomalous && timestamp >= fromTime && timestamp <= untilTime {
			count++
		}
	}
	return count
}

// ForEachAnomaly walks in ascending source time, as the real one does: the
// compatibility converter checks the timestamps it collects against the count
// the window reported, and a map's own order would make that check flap.
func (h pointHistory) ForEachAnomaly(fromTime, untilTime int64, visit func(int64) bool) {
	times := make([]int64, 0, len(h.points))
	for timestamp, anomalous := range h.points {
		if anomalous && timestamp >= fromTime && timestamp <= untilTime {
			times = append(times, timestamp)
		}
	}
	sort.Slice(times, func(left, right int) bool { return times[left] < times[right] })
	for _, timestamp := range times {
		if !visit(timestamp) {
			return
		}
	}
}

func (h pointHistory) FirstAnomaly(fromTime, untilTime int64) (int64, bool) {
	first, found := int64(0), false
	for timestamp, anomalous := range h.points {
		if !anomalous || timestamp < fromTime || timestamp > untilTime {
			continue
		}
		if !found || timestamp < first {
			first, found = timestamp, true
		}
	}
	return first, found
}

func requestV2(t *testing.T, plan *strategy.CompiledPlan, source int64, facts []DetectionFact, histories []LevelHistory, effective []LevelEffectiveTimeFact) EvaluationRequestV2 {
	t.Helper()
	recordID, err := contract.DeriveRecordIDV2(strings.Repeat("c", 64), source)
	if err != nil {
		t.Fatal(err)
	}
	return EvaluationRequestV2{
		TenantID: "default", BusinessID: "2", Plan: plan,
		Record:    DetectionRecord{RecordID: recordID, SourceTime: source, ProjectedValues: []ProjectedValue{{CanonicalDecimal: "50.100000", Available: true}}, LevelFacts: facts},
		RecordRef: contract.TriggerRecordRefV1{RecordID: recordID, SourceTime: source, DimensionIdentityDigest: strings.Repeat("c", 64), Dimensions: map[string]json.RawMessage{"host": json.RawMessage(`"127.0.0.1"`)}},
		Observed:  contract.TriggerObservedV1{Values: map[string]json.RawMessage{"value": json.RawMessage(`50.1`)}, Unit: "percent"},
		Histories: histories, EffectiveTimeFacts: effective, EvaluationTime: source, ExecutionID: "execution-1",
		LateAccepted: false,
		Limits: EvaluationLimitsV2{
			MaxLevels: 16, MaxTriggerWindowSize: 64, MaxRecoveryConsecutiveWindows: 64,
			MaxRequiredHistoryPoints: 256, MaxLevelResultsPerEvent: 16,
			MaxEvidenceBytesPerEvent: 64 << 10, MaxComputeCost: 4096,
		},
	}
}

func factV2(level strategy.CompiledLevel, result string) DetectionFact {
	ordinal := uint32(0)
	algorithm := uint32(0)
	group := uint32(0)
	return DetectionFact{
		Definition: level.Definition(), DetectFingerprint: level.Fingerprints().Detect, Result: result,
		Evidence: DetectionEvidence{PredicateDigest: level.Detectors()[0].PredicateDigest(), ProjectedValueOrdinal: &ordinal, MatchedAlgorithmOrdinal: &algorithm, MatchedGroupOrdinal: &group},
	}
}

func unavailableFactV2(level strategy.CompiledLevel, reason string) DetectionFact {
	fact := factV2(level, DetectionUnavailable)
	fact.ReasonCode = reason
	fact.Evidence.ResultReason = reason
	return fact
}

func activeFactsV2(t *testing.T, plan *strategy.CompiledPlan, evaluationTime int64) []LevelEffectiveTimeFact {
	t.Helper()
	return effectiveFactsV2(t, plan, evaluationTime, func(string) (*time.Location, error) { return time.UTC, nil })
}

func effectiveFactsV2(t *testing.T, plan *strategy.CompiledPlan, evaluationTime int64, resolve func(string) (*time.Location, error)) []LevelEffectiveTimeFact {
	t.Helper()
	provider := strategy.NewStaticScheduleProvider(strategy.TimezoneResolverFunc(func(_ context.Context, ref, _, _ string) (*time.Location, error) {
		return resolve(ref)
	}))
	levels := plan.Levels()
	requests := make([]strategy.EffectiveTimeRequest, len(levels))
	for index, level := range levels {
		requests[index] = strategy.EffectiveTimeRequest{TenantID: "default", BusinessID: "2", EvaluationTime: evaluationTime, Requirement: level.EffectiveTimeRequirement()}
	}
	facts, err := provider.Resolve(context.Background(), requests)
	if err != nil {
		t.Fatal(err)
	}
	result := make([]LevelEffectiveTimeFact, len(levels))
	for index := range levels {
		result[index] = LevelEffectiveTimeFact{LevelID: levels[index].Definition().LevelID, Fact: facts[index]}
	}
	return result
}

func compilePlanV2(t testing.TB, levels []contract.LevelIRV2) *strategy.CompiledPlan {
	return compilePlanV2WithOutput(t, levels, nil)
}

func compilePlanV2WithOutput(t testing.TB, levels []contract.LevelIRV2, shape func(*contract.EvaluationPlanV2)) *strategy.CompiledPlan {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxRequiredHistoryPoints: 4096,
		MaxTriggerWindowSize: 4096, MaxRecoveryConsecutiveWindows: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 64, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "trigger-test-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := contract.StrategyRefV2{TenantID: "default", StrategyID: "1001", Revision: "strategy-r1"}
	projection := contract.InputProjectionV2{ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id", MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired}
	plan := contract.EvaluationPlanV2{
		PlanID: "1001", StrategyRef: ref, InputProjection: projection,
		StrategyIR: contract.StrategyIRV2{
			Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2, Minor: 0}, RequiredFeatures: []string{}, StrategyRef: ref,
			ExecutionSemantics: contract.ExecutionSemanticsV2{EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300, AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120},
			InputProjection:    projection, Levels: levels,
		},
	}
	if shape != nil {
		shape(&plan)
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{
		Plan:            plan,
		DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64), IdentityFields: []string{"host"}, SourceTimeField: "time", ReceivedTimeField: "received_time"},
		StateSemantics:  strategy.StateSemantics{StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-state-codec-v1", IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-seconds-v1", HistoryCellSemanticsVersion: "detect-history-cell-v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("compiler terminals = %#v / %#v", result.PlanTerminal(), result.LevelTerminals())
	}
	return compiled
}

func levelV2(levelID, priority, window, required, recovery uint32, uptime map[string]any) contract.LevelIRV2 {
	trigger := map[string]any{"window_size": window, "required_anomalies": required, "step_seconds": 60}
	if uptime != nil {
		trigger["timezone_ref"] = "BUSINESS_LOCAL"
		trigger["uptime"] = uptime
	}
	return contract.LevelIRV2{
		Definition: contract.LevelDefinitionV2{LevelID: levelID, LevelCode: "level", Priority: priority}, Connector: contract.LevelConnectorAND,
		DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{Type: "Threshold", Version: 1, Config: mustJSONV2(map[string]any{
			"value_field": "value", "data_unit": "percent", "threshold_unit_prefix": "", "precision": map[string]any{"decimal_places": 6, "rounding": "HALF_EVEN"},
			"groups": []any{map[string]any{"conditions": []any{map[string]any{"operator": "GTE", "threshold_decimal": "50"}}}},
		})}}},
		TriggerPlan:  contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: mustJSONV2(trigger)},
		RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: mustJSONV2(map[string]any{"enabled": true, "consecutive_windows": recovery})},
	}
}

func levelWithoutRecoveryV2(levelID, priority, window, required uint32) contract.LevelIRV2 {
	level := levelV2(levelID, priority, window, required, 1, nil)
	level.RecoveryPlan.Config = mustJSONV2(map[string]any{"enabled": false, "consecutive_windows": 0})
	return level
}

func staticUptimeV2() map[string]any {
	return map[string]any{"time_ranges": []any{map[string]any{"start": "09:00", "end": "17:00"}}, "active_calendars": []any{}, "calendars": []any{}}
}

func mustJSONV2(value any) json.RawMessage {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}

// A downstream that owns an alert's lifetime opens it from a point in time, and
// the window's own edges are not that point: a window whose anomalies started
// in the middle of it must not report its start. The earliest anomaly inside
// the window is what the decision was made from, so that is what travels.
func TestTheWindowReportsWhenItsAnomaliesStarted(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 9, 3, 2, 2, nil)})
	const source = int64(300)
	request := requestV2(t, plan, source, []DetectionFact{factV2(plan.Levels()[0], DetectionAnomalous)}, []LevelHistory{{
		LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{120: false, 180: false, 240: true, 300: true}},
	}}, activeFactsV2(t, plan, source))
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	window := result.LevelOutcomes[0].DecisionWindow.Trigger
	if window.AnomalyBeginTime != 240 {
		t.Fatalf("anomaly begin time = %d, want the earliest anomaly 240 (window starts at %d)",
			window.AnomalyBeginTime, window.WindowStart)
	}
	if window.AnomalyBeginTime == window.WindowStart {
		t.Fatal("the window's own start must not be reported as when its anomalies began")
	}
}

// A window that saw no anomaly reports no beginning, rather than a zero that
// reads as the epoch.
func TestAWindowWithNoAnomalyReportsNoBeginning(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 9, 3, 2, 2, nil)})
	const source = int64(300)
	request := requestV2(t, plan, source, []DetectionFact{factV2(plan.Levels()[0], DetectionNormal)}, []LevelHistory{{
		LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{120: false, 180: false, 240: false, 300: false}},
	}}, activeFactsV2(t, plan, source))
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if got := result.LevelOutcomes[0].DecisionWindow.Trigger.AnomalyBeginTime; got != 0 {
		t.Fatalf("anomaly begin time = %d, want none", got)
	}
}

// A deployment that forces the compatibility protocol makes strategies that do
// have a frozen revision publish it too, and the conversion reads a context
// only the Plan can supply. Attaching that context on the revision instead of
// on the format leaves exactly those events unconvertible, and the failure
// arrives at the sink, per event, rather than at configuration time.
func TestAPlanThatPublishesTheCompatibleProtocolCarriesItsContext(t *testing.T) {
	plan := compilePlanV2WithOutput(t, []contract.LevelIRV2{levelV2(5, 9, 3, 2, 2, nil)},
		func(p *contract.EvaluationPlanV2) {
			p.WireFormat = contract.WireFormatPythonCompatible
			p.StrategyRef.SnapshotRevision = 7
			p.StrategyIR.StrategyRef.SnapshotRevision = 7
			p.OutputIdentity = &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
			p.LegacyOutput = &contract.LegacyOutputContext{
				Strategy:        json.RawMessage(`{"id":1001,"bk_biz_id":2,"update_time":1756684800}`),
				DimensionFields: []string{"host"}, ItemID: "1",
			}
		})
	if plan.LegacyOutput() == nil {
		t.Fatal("the fixture did not produce a Plan carrying a compatibility context")
	}
	const source = int64(300)
	request := requestV2(t, plan, source, []DetectionFact{factV2(plan.Levels()[0], DetectionAnomalous)}, []LevelHistory{{
		LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{180: true, 240: true, 300: true}},
	}}, activeFactsV2(t, plan, source))
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if result.TriggerEvent == nil {
		t.Fatal("expected an event")
	}
	if result.TriggerEvent.StrategyRef == nil {
		t.Fatal("the fixture was meant to carry a frozen revision")
	}
	if result.TriggerEvent.LegacyOutput == nil {
		t.Fatal("a Plan publishing the compatibility protocol produced an event with no context to convert")
	}
}
