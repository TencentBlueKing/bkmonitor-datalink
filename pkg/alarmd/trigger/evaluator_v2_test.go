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

	warming := requestV2(t, compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 3, 2, 1, nil)}), 300,
		[]DetectionFact{}, nil, nil)
	warming.Record.LevelFacts = []DetectionFact{factV2(warming.Plan.Levels()[0], DetectionNormal)}
	warming.Histories = []LevelHistory{{LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{240: false, 300: false}}}}
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

func TestEvaluatorV2WarmingAllowsOnlyMonotonicAbnormal(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 3, 2, 1, nil)})
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

	request.Record.LevelFacts[0] = factV2(level, DetectionNormal)
	request.Histories[0].View = pointHistory{step: 60, points: map[int64]bool{180: false, 300: false}}
	result, err = EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2(gapped) error = %v", err)
	}
	if result.Completion != CompletionUnavailable || result.LevelOutcomes[0].UnavailableReason != contract.ReasonHistoryGapped || result.TriggerEvent != nil {
		t.Fatalf("gapped normal = %#v", result)
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
