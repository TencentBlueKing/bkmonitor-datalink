// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trigger

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
)

func TestEvaluatorProcessesNormalAndAnomalousOutcomes(t *testing.T) {
	t.Parallel()

	strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 3, TriggerCount: 2}})
	evaluator := NewEvaluator()

	decision, err := evaluator.Process(strategy, newOutcome(t, strategy, 100, map[int]bool{1: false}))
	if err != nil {
		t.Fatalf("Process(normal) error = %v", err)
	}
	if decision != nil {
		t.Fatalf("Process(normal) decision = %#v, want nil", decision)
	}

	decision, err = evaluator.Process(strategy, newOutcome(t, strategy, 110, map[int]bool{1: true}))
	if err != nil {
		t.Fatalf("Process(first anomaly) error = %v", err)
	}
	if decision == nil || decision.Outcome != DecisionNoTrigger || decision.Level != 0 {
		t.Fatalf("Process(first anomaly) decision = %#v, want NO_TRIGGER", decision)
	}
	assertTimestamps(t, decision.AnomalyTimestamps, []int64{110})

	decision, err = evaluator.Process(strategy, newOutcome(t, strategy, 120, map[int]bool{1: true}))
	if err != nil {
		t.Fatalf("Process(second anomaly) error = %v", err)
	}
	if decision == nil || decision.Outcome != DecisionTrigger || decision.Level != 1 {
		t.Fatalf("Process(second anomaly) decision = %#v, want level 1 TRIGGER", decision)
	}
	assertTimestamps(t, decision.AnomalyTimestamps, []int64{110, 120})
}

func TestEvaluatorIsIdempotentForRedeliveredOutcome(t *testing.T) {
	t.Parallel()

	strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 3, TriggerCount: 2}})
	evaluator := NewEvaluator()
	outcome := newOutcome(t, strategy, 100, map[int]bool{1: true})

	for attempt := 0; attempt < 2; attempt++ {
		decision, err := evaluator.Process(strategy, outcome)
		if err != nil {
			t.Fatalf("Process(attempt %d) error = %v", attempt, err)
		}
		if decision == nil || decision.Outcome != DecisionNoTrigger {
			t.Fatalf("Process(attempt %d) decision = %#v, want NO_TRIGGER", attempt, decision)
		}
		assertTimestamps(t, decision.AnomalyTimestamps, []int64{100})
	}
}

func TestEvaluatorUsesCurrentAnomalousLevelsAndHigherLevelPriority(t *testing.T) {
	t.Parallel()

	strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{
		{Level: 1, CheckWindowSize: 3, TriggerCount: 2},
		{Level: 2, CheckWindowSize: 3, TriggerCount: 2},
	})
	evaluator := NewEvaluator()

	for _, sourceTime := range []int64{100, 110} {
		if _, err := evaluator.Process(strategy, newOutcome(t, strategy, sourceTime, map[int]bool{1: true, 2: true})); err != nil {
			t.Fatalf("Process(%d) error = %v", sourceTime, err)
		}
	}

	decision, err := evaluator.Process(strategy, newOutcome(t, strategy, 120, map[int]bool{1: false, 2: true}))
	if err != nil {
		t.Fatalf("Process(level 2 only) error = %v", err)
	}
	if decision == nil || decision.Outcome != DecisionTrigger || decision.Level != 2 {
		t.Fatalf("Process(level 2 only) decision = %#v, want level 2 TRIGGER", decision)
	}

	decision, err = evaluator.Process(strategy, newOutcome(t, strategy, 130, map[int]bool{1: true, 2: true}))
	if err != nil {
		t.Fatalf("Process(two levels) error = %v", err)
	}
	if decision == nil || decision.Outcome != DecisionTrigger || decision.Level != 1 {
		t.Fatalf("Process(two levels) decision = %#v, want higher-priority level 1 TRIGGER", decision)
	}
}

func TestEvaluatorIsolatesDimensionsAndStrategyGeneration(t *testing.T) {
	t.Parallel()

	strategyV1 := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 3, TriggerCount: 2}})
	strategyV2 := newStrategy(t, "generation-2", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 3, TriggerCount: 2}})
	evaluator := NewEvaluator()

	first := newOutcome(t, strategyV1, 100, map[int]bool{1: true})
	if _, err := evaluator.Process(strategyV1, first); err != nil {
		t.Fatalf("Process(first) error = %v", err)
	}

	otherDimension := newOutcome(t, strategyV1, 110, map[int]bool{1: true})
	setDimensions(t, otherDimension, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	decision, err := evaluator.Process(strategyV1, otherDimension)
	if err != nil {
		t.Fatalf("Process(other dimension) error = %v", err)
	}
	if decision == nil || decision.Outcome != DecisionNoTrigger {
		t.Fatalf("Process(other dimension) decision = %#v, want NO_TRIGGER", decision)
	}

	decision, err = evaluator.Process(strategyV2, newOutcome(t, strategyV2, 110, map[int]bool{1: true}))
	if err != nil {
		t.Fatalf("Process(other generation) error = %v", err)
	}
	if decision == nil || decision.Outcome != DecisionNoTrigger {
		t.Fatalf("Process(other generation) decision = %#v, want NO_TRIGGER", decision)
	}
}

func TestEvaluatorEvictsWindowAndDoesNotAdvanceOnError(t *testing.T) {
	t.Parallel()

	strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 2, TriggerCount: 2}})
	evaluator := NewEvaluator()

	if _, err := evaluator.Process(strategy, newOutcome(t, strategy, 100, map[int]bool{1: true})); err != nil {
		t.Fatalf("Process(first) error = %v", err)
	}
	errorOutcome := newOutcome(t, strategy, 110, map[int]bool{1: true})
	errorOutcome.Outcome = contract.OutcomeError
	errorOutcome.Evaluations = nil
	errorOutcome.ErrorCode = json.RawMessage(`"ALGORITHM_ERROR"`)
	if decision, err := evaluator.Process(strategy, errorOutcome); err != nil || decision != nil {
		t.Fatalf("Process(error) = (%#v, %v), want (nil, nil)", decision, err)
	}

	decision, err := evaluator.Process(strategy, newOutcome(t, strategy, 121, map[int]bool{1: true}))
	if err != nil {
		t.Fatalf("Process(outside window) error = %v", err)
	}
	if decision == nil || decision.Outcome != DecisionNoTrigger {
		t.Fatalf("Process(outside window) decision = %#v, want NO_TRIGGER", decision)
	}
}

func TestEvaluatorWindowIncludesExactLowerBoundAndExcludesOneSecondEarlier(t *testing.T) {
	t.Parallel()

	strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 2, TriggerCount: 2}})

	atLowerBound := NewEvaluator()
	if _, err := atLowerBound.Process(strategy, newOutcome(t, strategy, 100, map[int]bool{1: true})); err != nil {
		t.Fatalf("Process(first lower-bound point) error = %v", err)
	}
	decision, err := atLowerBound.Process(strategy, newOutcome(t, strategy, 119, map[int]bool{1: true}))
	if err != nil {
		t.Fatalf("Process(exact lower-bound point) error = %v", err)
	}
	if decision == nil || decision.Outcome != DecisionTrigger {
		t.Fatalf("Process(exact lower-bound point) decision = %#v, want TRIGGER", decision)
	}
	assertTimestamps(t, decision.AnomalyTimestamps, []int64{100, 119})

	beforeLowerBound := NewEvaluator()
	if _, err := beforeLowerBound.Process(strategy, newOutcome(t, strategy, 100, map[int]bool{1: true})); err != nil {
		t.Fatalf("Process(first excluded point) error = %v", err)
	}
	decision, err = beforeLowerBound.Process(strategy, newOutcome(t, strategy, 120, map[int]bool{1: true}))
	if err != nil {
		t.Fatalf("Process(point after lower bound) error = %v", err)
	}
	if decision == nil || decision.Outcome != DecisionNoTrigger {
		t.Fatalf("Process(point after lower bound) decision = %#v, want NO_TRIGGER", decision)
	}
	assertTimestamps(t, decision.AnomalyTimestamps, []int64{120})
}

func TestEvaluatorDoesNotAdvanceStateForNonBusinessOutcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		outcome   string
		errorCode string
	}{
		{name: "error", outcome: contract.OutcomeError, errorCode: "ALGORITHM_ERROR"},
		{name: "unsupported", outcome: contract.OutcomeUnsupported, errorCode: "UNSUPPORTED_STRATEGY"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 3, TriggerCount: 2}})
			evaluator := NewEvaluator()
			nonBusiness := newOutcome(t, strategy, 100, map[int]bool{1: true})
			nonBusiness.Outcome = test.outcome
			nonBusiness.Evaluations = nil
			nonBusiness.ErrorCode = json.RawMessage(fmt.Sprintf("%q", test.errorCode))
			if decision, err := evaluator.Process(strategy, nonBusiness); err != nil || decision != nil {
				t.Fatalf("Process(%s) = (%#v, %v), want (nil, nil)", test.name, decision, err)
			}

			decision, err := evaluator.Process(strategy, newOutcome(t, strategy, 110, map[int]bool{1: true}))
			if err != nil {
				t.Fatalf("Process(anomaly after %s) error = %v", test.name, err)
			}
			if decision == nil || decision.Outcome != DecisionNoTrigger {
				t.Fatalf("Process(anomaly after %s) decision = %#v, want NO_TRIGGER", test.name, decision)
			}
			assertTimestamps(t, decision.AnomalyTimestamps, []int64{110})
		})
	}
}

func TestEvaluatorRejectsNodataUntilItsTriggerSemanticsAreImplemented(t *testing.T) {
	t.Parallel()

	strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 3, TriggerCount: 2}})
	strategy.Purpose = contract.PurposeNodata
	outcome := newOutcome(t, strategy, 100, map[int]bool{1: true})

	decision, err := NewEvaluator().Process(strategy, outcome)
	if !errors.Is(err, ErrUnsupportedPurpose) || decision != nil {
		t.Fatalf("Process(nodata) = (%#v, %v), want (nil, ErrUnsupportedPurpose)", decision, err)
	}
}

func newStrategy(t *testing.T, generation string, configs []contract.TriggerConfig) *contract.TriggerStrategyIR {
	t.Helper()

	legacyJSON := []byte(`{"id":1,"items":[{"id":2}]}`)
	digest := sha256.Sum256(legacyJSON)
	levels := make([]int, 0, len(configs))
	for _, config := range configs {
		levels = append(levels, config.Level)
	}
	strategy := &contract.TriggerStrategyIR{
		Schema:                 contract.Schema{Name: "trigger-strategy-ir", Major: 1, Minor: 0},
		RequiredFeatures:       []string{"raw-strategy-bytes-v1"},
		TenantID:               "default",
		Purpose:                contract.PurposeDetect,
		StrategyRef:            contract.StrategyRef{StrategyID: "1", ItemID: "2", Generation: generation, ContentSHA256: hex.EncodeToString(digest[:])},
		RequiredLevels:         levels,
		CheckWindowUnitSeconds: 10,
		TriggerConfigs:         configs,
		LegacyJSONBase64:       base64.StdEncoding.EncodeToString(legacyJSON),
	}
	if err := strategy.Validate(); err != nil {
		t.Fatalf("strategy.Validate() error = %v", err)
	}
	return strategy
}

func newOutcome(t *testing.T, strategy *contract.TriggerStrategyIR, sourceTime int64, anomalous map[int]bool) *contract.DetectionOutcome {
	t.Helper()

	dimensionsMD5 := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	recordID := fmt.Sprintf("%s.%d", dimensionsMD5, sourceTime)
	evaluations := make([]contract.Evaluation, 0, len(strategy.RequiredLevels))
	outcomeName := contract.OutcomeNormal
	for _, level := range strategy.RequiredLevels {
		evaluation := contract.Evaluation{Level: level, Result: contract.EvaluationNormal}
		if anomalous[level] {
			outcomeName = contract.OutcomeAnomalous
			evaluation.Result = contract.EvaluationAnomalous
			evaluation.Anomaly = json.RawMessage(fmt.Sprintf(`{"anomaly_id":"%s.%s.%s.%d"}`, recordID, strategy.StrategyRef.StrategyID, strategy.StrategyRef.ItemID, level))
		}
		evaluations = append(evaluations, evaluation)
	}
	inputID, err := contract.DeriveInputID(contract.InputIdentity{
		TenantID:              strategy.TenantID,
		Purpose:               strategy.Purpose,
		StrategyID:            strategy.StrategyRef.StrategyID,
		ItemID:                strategy.StrategyRef.ItemID,
		StrategyContentSHA256: strategy.StrategyRef.ContentSHA256,
		RecordID:              recordID,
	})
	if err != nil {
		t.Fatalf("DeriveInputID() error = %v", err)
	}
	outcome := &contract.DetectionOutcome{
		Schema:           contract.Schema{Name: "detection-outcome", Major: 1, Minor: 0},
		RequiredFeatures: []string{"full-level-evaluations-v1", "raw-json-v1"},
		InputID:          inputID,
		BatchID:          "batch-1",
		TenantID:         strategy.TenantID,
		Purpose:          strategy.Purpose,
		StrategyRef:      strategy.StrategyRef,
		Record: contract.DetectionRecord{
			RecordID:      recordID,
			SourceTime:    sourceTime,
			DimensionsMD5: dimensionsMD5,
			DataRaw:       json.RawMessage(fmt.Sprintf(`{"record_id":"%s","time":%d}`, recordID, sourceTime)),
		},
		Evaluations: evaluations,
		Outcome:     outcomeName,
	}
	if err := outcome.Validate(strategy); err != nil {
		t.Fatalf("outcome.Validate() error = %v", err)
	}
	return outcome
}

func setDimensions(t *testing.T, outcome *contract.DetectionOutcome, dimensionsMD5 string) {
	t.Helper()

	outcome.Record.DimensionsMD5 = dimensionsMD5
	outcome.Record.RecordID = fmt.Sprintf("%s.%d", dimensionsMD5, outcome.Record.SourceTime)
	outcome.Record.DataRaw = json.RawMessage(fmt.Sprintf(`{"record_id":"%s","time":%d}`, outcome.Record.RecordID, outcome.Record.SourceTime))
	for index := range outcome.Evaluations {
		if outcome.Evaluations[index].Result != contract.EvaluationAnomalous {
			continue
		}
		outcome.Evaluations[index].Anomaly = json.RawMessage(fmt.Sprintf(
			`{"anomaly_id":"%s.%s.%s.%d"}`,
			outcome.Record.RecordID,
			outcome.StrategyRef.StrategyID,
			outcome.StrategyRef.ItemID,
			outcome.Evaluations[index].Level,
		))
	}
	inputID, err := contract.DeriveInputID(contract.InputIdentity{
		TenantID:              outcome.TenantID,
		Purpose:               outcome.Purpose,
		StrategyID:            outcome.StrategyRef.StrategyID,
		ItemID:                outcome.StrategyRef.ItemID,
		StrategyContentSHA256: outcome.StrategyRef.ContentSHA256,
		RecordID:              outcome.Record.RecordID,
	})
	if err != nil {
		t.Fatalf("DeriveInputID() error = %v", err)
	}
	outcome.InputID = inputID
}

func assertTimestamps(t *testing.T, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("timestamps = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("timestamps = %v, want %v", got, want)
		}
	}
}
