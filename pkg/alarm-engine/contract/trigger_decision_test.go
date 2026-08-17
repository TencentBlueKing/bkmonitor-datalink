// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestDeriveTriggerDecisionIDGolden(t *testing.T) {
	t.Parallel()

	inputID := strings.Repeat("a", 64)
	got, err := DeriveTriggerDecisionID(inputID)
	if err != nil {
		t.Fatalf("DeriveTriggerDecisionID() error = %v", err)
	}
	const want = "ff722dbf77ffa1b06945ab996e22adbefd6f22c32dec2173fd1ed659b874a130"
	if got != want {
		t.Fatalf("decision_id = %q, want %q", got, want)
	}
	if _, err := DeriveTriggerDecisionID("not-a-canonical-input-id"); err == nil {
		t.Fatal("DeriveTriggerDecisionID() accepted a non-canonical input_id")
	}
}

func TestTriggerDecisionBatchRoundTripAndPartitionKey(t *testing.T) {
	t.Parallel()

	input := decodedTriggerInputForDecision(t)
	decisions := []TriggerDecision{
		newTriggerDecision(t, input.DetectionOutcomes[0], DecisionOutcomeTrigger, DecisionReasonTriggerConditionMet, intPointer(3), []int64{1569246480}),
		newTriggerDecision(t, input.DetectionOutcomes[1], DecisionOutcomeNoTrigger, DecisionReasonInputNormal, nil, []int64{}),
	}
	batch, err := input.BuildTriggerDecisionBatch(decisions)
	if err != nil {
		t.Fatalf("BuildTriggerDecisionBatch() error = %v", err)
	}
	payload, err := EncodeTriggerDecisionBatch(batch)
	if err != nil {
		t.Fatalf("EncodeTriggerDecisionBatch() error = %v", err)
	}
	decoded, err := DecodeTriggerDecisionBatch(payload)
	if err != nil {
		t.Fatalf("DecodeTriggerDecisionBatch() error = %v", err)
	}
	if !reflect.DeepEqual(decoded.Decisions, decisions) {
		t.Fatalf("decoded decisions = %#v, want %#v", decoded.Decisions, decisions)
	}
	inputKey, err := input.PartitionKey()
	if err != nil {
		t.Fatalf("input.PartitionKey() error = %v", err)
	}
	decisionKey, err := decoded.PartitionKey()
	if err != nil {
		t.Fatalf("decoded.PartitionKey() error = %v", err)
	}
	if !bytes.Equal(decisionKey, inputKey) {
		t.Fatalf("decision partition key = %x, want input key %x", decisionKey, inputKey)
	}
}

func TestTriggerDecisionValidatesOutcomeReasonMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		outcome    string
		reason     string
		level      *int
		timestamps []int64
		wantErr    bool
	}{
		{name: "trigger", outcome: DecisionOutcomeTrigger, reason: DecisionReasonTriggerConditionMet, level: intPointer(1), timestamps: []int64{90, 100}},
		{name: "trigger without level", outcome: DecisionOutcomeTrigger, reason: DecisionReasonTriggerConditionMet, timestamps: []int64{100}, wantErr: true},
		{name: "trigger with duplicate timestamp", outcome: DecisionOutcomeTrigger, reason: DecisionReasonTriggerConditionMet, level: intPointer(1), timestamps: []int64{100, 100}, wantErr: true},
		{name: "trigger with negative timestamp", outcome: DecisionOutcomeTrigger, reason: DecisionReasonTriggerConditionMet, level: intPointer(1), timestamps: []int64{-1, 100}, wantErr: true},
		{name: "trigger with future timestamp", outcome: DecisionOutcomeTrigger, reason: DecisionReasonTriggerConditionMet, level: intPointer(1), timestamps: []int64{100, 101}, wantErr: true},
		{name: "trigger missing current source time", outcome: DecisionOutcomeTrigger, reason: DecisionReasonTriggerConditionMet, level: intPointer(1), timestamps: []int64{90, 99}, wantErr: true},
		{name: "normal no trigger", outcome: DecisionOutcomeNoTrigger, reason: DecisionReasonInputNormal, timestamps: []int64{}},
		{name: "normal no trigger with level", outcome: DecisionOutcomeNoTrigger, reason: DecisionReasonInputNormal, level: intPointer(1), timestamps: []int64{}, wantErr: true},
		{name: "anomalous no trigger", outcome: DecisionOutcomeNoTrigger, reason: DecisionReasonTriggerConditionNotMet, timestamps: []int64{}},
		{name: "anomalous no trigger with candidate details", outcome: DecisionOutcomeNoTrigger, reason: DecisionReasonTriggerConditionNotMet, level: intPointer(1), timestamps: []int64{100}, wantErr: true},
		{name: "error", outcome: OutcomeError, reason: "ALGORITHM_ERROR", timestamps: []int64{}},
		{name: "error with level", outcome: OutcomeError, reason: "ALGORITHM_ERROR", level: intPointer(1), timestamps: []int64{}, wantErr: true},
		{name: "unsupported", outcome: OutcomeUnsupported, reason: "UNSUPPORTED_STRATEGY", timestamps: []int64{}},
		{name: "wrong bounded reason", outcome: OutcomeUnsupported, reason: "INTERNAL_ERROR", timestamps: []int64{}, wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			inputID := strings.Repeat("a", 64)
			decisionID, err := DeriveTriggerDecisionID(inputID)
			if err != nil {
				t.Fatalf("DeriveTriggerDecisionID() error = %v", err)
			}
			decision := TriggerDecision{
				DecisionID:        decisionID,
				InputID:           inputID,
				RecordID:          strings.Repeat("c", 32) + ".100",
				Outcome:           test.outcome,
				ReasonCode:        test.reason,
				Level:             test.level,
				AnomalyTimestamps: test.timestamps,
			}
			err = decision.Validate()
			if test.wantErr && err == nil {
				t.Fatal("Validate() error = nil, want failure")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("Validate() error = %v, want nil", err)
			}
		})
	}
}

func TestDecodeTriggerDecisionBatchFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr bool
	}{
		{name: "unknown major", mutate: func(document map[string]any) { document["schema"].(map[string]any)["major"] = 2 }, wantErr: true},
		{name: "unknown feature", mutate: func(document map[string]any) { document["required_features"] = []string{"future-required"} }, wantErr: true},
		{name: "minor zero unknown field", mutate: func(document map[string]any) { document["future_optional"] = true }, wantErr: true},
		{name: "higher minor optional field", mutate: func(document map[string]any) {
			document["schema"].(map[string]any)["minor"] = 1
			document["future_optional"] = true
		}},
		{name: "duplicate input id", mutate: func(document map[string]any) {
			decisions := document["decisions"].([]any)
			document["decisions"] = append(decisions, decisions[0])
		}, wantErr: true},
		{name: "input id mismatch", mutate: func(document map[string]any) {
			decisions := document["decisions"].([]any)
			decisions[0].(map[string]any)["input_id"] = strings.Repeat("d", 64)
		}, wantErr: true},
		{name: "decision id mismatch", mutate: func(document map[string]any) {
			decisions := document["decisions"].([]any)
			decisions[0].(map[string]any)["decision_id"] = strings.Repeat("d", 64)
		}, wantErr: true},
		{name: "null timestamps", mutate: func(document map[string]any) {
			decisions := document["decisions"].([]any)
			decisions[0].(map[string]any)["anomaly_timestamps"] = nil
		}, wantErr: true},
		{name: "null level", mutate: func(document map[string]any) {
			decisions := document["decisions"].([]any)
			decisions[0].(map[string]any)["level"] = nil
		}, wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			document := validDecisionBatchDocument(t)
			test.mutate(document)
			payload, err := json.Marshal(document)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			_, err = DecodeTriggerDecisionBatch(payload)
			if test.wantErr && err == nil {
				t.Fatal("DecodeTriggerDecisionBatch() error = nil, want fail-closed rejection")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("DecodeTriggerDecisionBatch() error = %v, want forward-compatible acceptance", err)
			}
		})
	}
}

func TestTriggerDecisionBatchLimits(t *testing.T) {
	t.Parallel()

	document := validDecisionBatchDocument(t)
	decision := document["decisions"].([]any)[0]
	decisions := make([]any, MaxTriggerDecisionItemsV1+1)
	for index := range decisions {
		clone := cloneJSONMap(t, decision)
		clone["input_id"] = strings.Repeat("0", 56) + fmt.Sprintf("%08d", index)
		clone["decision_id"] = strings.Repeat("1", 56) + fmt.Sprintf("%08d", index)
		decisions[index] = clone
	}
	document["decisions"] = decisions
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if _, err := DecodeTriggerDecisionBatch(payload); err == nil || !strings.Contains(err.Error(), "between 1 and 500") {
		t.Fatalf("DecodeTriggerDecisionBatch() error = %v, want item-count limit", err)
	}

	validPayload, err := json.Marshal(validDecisionBatchDocument(t))
	if err != nil {
		t.Fatalf("json.Marshal(valid) error = %v", err)
	}
	oversized := append(validPayload, bytes.Repeat([]byte(" "), MaxTriggerDecisionBytesV1-len(validPayload)+1)...)
	if _, err := DecodeTriggerDecisionBatch(oversized); err == nil {
		t.Fatal("DecodeTriggerDecisionBatch() accepted oversized payload")
	}
}

func TestBuildTriggerDecisionBatchRejectsSourceContradictions(t *testing.T) {
	t.Parallel()

	input := decodedTriggerInputForDecision(t)
	source := input.DetectionOutcomes[0]
	tests := []struct {
		name       string
		level      int
		timestamps []int64
	}{
		{name: "level is not anomalous", level: 2, timestamps: []int64{source.Record.SourceTime}},
		{name: "missing current source time", level: 3, timestamps: []int64{source.Record.SourceTime - 1}},
		{name: "outside trigger window", level: 3, timestamps: []int64{source.Record.SourceTime - 300, source.Record.SourceTime}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			decisions := []TriggerDecision{
				newTriggerDecision(t, source, DecisionOutcomeTrigger, DecisionReasonTriggerConditionMet, intPointer(test.level), test.timestamps),
				newTriggerDecision(t, input.DetectionOutcomes[1], DecisionOutcomeNoTrigger, DecisionReasonInputNormal, nil, []int64{}),
			}
			if _, err := input.BuildTriggerDecisionBatch(decisions); err == nil {
				t.Fatal("BuildTriggerDecisionBatch() accepted a decision contradicting its source input")
			}
		})
	}
}

func TestBuildTriggerDecisionBatchRejectsCountOrderAndTriggerCountContradictions(t *testing.T) {
	t.Parallel()

	input := decodedTriggerInputForDecision(t)
	valid := []TriggerDecision{
		newTriggerDecision(t, input.DetectionOutcomes[0], DecisionOutcomeTrigger, DecisionReasonTriggerConditionMet, intPointer(3), []int64{1569246480}),
		newTriggerDecision(t, input.DetectionOutcomes[1], DecisionOutcomeNoTrigger, DecisionReasonInputNormal, nil, []int64{}),
	}
	if _, err := input.BuildTriggerDecisionBatch(valid[:1]); err == nil {
		t.Fatal("BuildTriggerDecisionBatch() accepted fewer decisions than source outcomes")
	}
	swapped := []TriggerDecision{valid[1], valid[0]}
	if _, err := input.BuildTriggerDecisionBatch(swapped); err == nil {
		t.Fatal("BuildTriggerDecisionBatch() accepted decisions in a different source order")
	}

	document := newTriggerInputDocument(t, "anomalous")
	var strategy map[string]any
	if err := json.Unmarshal(document["strategy_ir"].(json.RawMessage), &strategy); err != nil {
		t.Fatalf("json.Unmarshal(strategy_ir) error = %v", err)
	}
	strategy["trigger_configs"].([]any)[0].(map[string]any)["trigger_count"] = float64(2)
	document["strategy_ir"] = strategy
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("json.Marshal(input) error = %v", err)
	}
	countTwoInput, err := DecodeTriggerInput(payload)
	if err != nil {
		t.Fatalf("DecodeTriggerInput(trigger_count=2) error = %v", err)
	}
	source := countTwoInput.DetectionOutcomes[0]
	oneTimestamp := []TriggerDecision{
		newTriggerDecision(t, source, DecisionOutcomeTrigger, DecisionReasonTriggerConditionMet, intPointer(3), []int64{source.Record.SourceTime}),
	}
	if _, err := countTwoInput.BuildTriggerDecisionBatch(oneTimestamp); err == nil || !strings.Contains(err.Error(), "trigger count") {
		t.Fatalf("BuildTriggerDecisionBatch() error = %v, want trigger-count rejection", err)
	}
}

func TestTriggerDecisionBatchRejectsBusinessOutcomeForUnsupportedPurpose(t *testing.T) {
	t.Parallel()

	batch := directNormalDecisionBatch(t, 1)
	batch.Purpose = PurposeNodata
	for index := range batch.Decisions {
		decision := &batch.Decisions[index]
		inputID, err := DeriveInputID(InputIdentity{
			TenantID:              batch.TenantID,
			Purpose:               batch.Purpose,
			StrategyID:            batch.StrategyRef.StrategyID,
			ItemID:                batch.StrategyRef.ItemID,
			StrategyContentSHA256: batch.StrategyRef.ContentSHA256,
			RecordID:              decision.RecordID,
		})
		if err != nil {
			t.Fatalf("DeriveInputID() error = %v", err)
		}
		decision.InputID = inputID
		decision.DecisionID, err = DeriveTriggerDecisionID(inputID)
		if err != nil {
			t.Fatalf("DeriveTriggerDecisionID() error = %v", err)
		}
	}
	if err := batch.Validate(); err == nil || !strings.Contains(err.Error(), "UNSUPPORTED_STRATEGY") {
		t.Fatalf("Validate() error = %v, want unsupported-purpose decision rejection", err)
	}
}

func TestTriggerDecisionBatchEncodeExactLimitAndRejectsOverflow(t *testing.T) {
	t.Parallel()

	batch := directNormalDecisionBatch(t, 1)
	baseline, err := EncodeTriggerDecisionBatch(batch)
	if err != nil {
		t.Fatalf("EncodeTriggerDecisionBatch(baseline) error = %v", err)
	}
	batch.BatchID += strings.Repeat("x", MaxTriggerDecisionBytesV1-len(baseline))
	atLimit, err := EncodeTriggerDecisionBatch(batch)
	if err != nil {
		t.Fatalf("EncodeTriggerDecisionBatch(at limit) error = %v", err)
	}
	if len(atLimit) != MaxTriggerDecisionBytesV1 {
		t.Fatalf("encoded bytes = %d, want exact limit %d", len(atLimit), MaxTriggerDecisionBytesV1)
	}
	batch.BatchID += "x"
	if _, err := EncodeTriggerDecisionBatch(batch); err == nil {
		t.Fatal("EncodeTriggerDecisionBatch() accepted payload above the byte limit")
	}
}

func TestTriggerDecisionBatchAcceptsExactlyFiveHundredDecisions(t *testing.T) {
	t.Parallel()

	batch := directNormalDecisionBatch(t, MaxTriggerDecisionItemsV1)
	payload, err := EncodeTriggerDecisionBatch(batch)
	if err != nil {
		t.Fatalf("EncodeTriggerDecisionBatch() error = %v", err)
	}
	decoded, err := DecodeTriggerDecisionBatch(payload)
	if err != nil {
		t.Fatalf("DecodeTriggerDecisionBatch() error = %v", err)
	}
	if len(decoded.Decisions) != MaxTriggerDecisionItemsV1 {
		t.Fatalf("decoded decisions = %d, want %d", len(decoded.Decisions), MaxTriggerDecisionItemsV1)
	}
}

func TestTriggerDecisionBatchOwnsDecisionDetails(t *testing.T) {
	t.Parallel()

	input := decodedTriggerInputForDecision(t)
	level := 3
	timestamps := []int64{1569246480}
	decisions := []TriggerDecision{
		newTriggerDecision(t, input.DetectionOutcomes[0], DecisionOutcomeTrigger, DecisionReasonTriggerConditionMet, &level, timestamps),
		newTriggerDecision(t, input.DetectionOutcomes[1], DecisionOutcomeNoTrigger, DecisionReasonInputNormal, nil, []int64{}),
	}
	batch, err := input.BuildTriggerDecisionBatch(decisions)
	if err != nil {
		t.Fatalf("BuildTriggerDecisionBatch() error = %v", err)
	}
	level = 2
	timestamps[0] = 0
	decisions[0].AnomalyTimestamps[0] = 1
	if batch.Decisions[0].Level == nil || *batch.Decisions[0].Level != 3 || batch.Decisions[0].AnomalyTimestamps[0] != 1569246480 {
		t.Fatalf("batch decision was mutated through caller-owned values: %#v", batch.Decisions[0])
	}
}

func TestTriggerDecisionBatchFixedWireV1(t *testing.T) {
	t.Parallel()

	batch := directNormalDecisionBatch(t, 1)
	payload, err := EncodeTriggerDecisionBatch(batch)
	if err != nil {
		t.Fatalf("EncodeTriggerDecisionBatch() error = %v", err)
	}
	const want = `{"schema":{"name":"trigger-decision-batch","major":1,"minor":0},"required_features":[],"partition_hash_version":"trigger-input-partition-v1","batch_id":"batch-1","tenant_id":"default","purpose":"DETECT","strategy_ref":{"strategy_id":"1","item_id":"2","generation":"generation-1","content_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"decision_algorithm":"trigger-window-v1","decisions":[{"decision_id":"ab519a572fb1e1330524cd6be77c1b26870c5e92bfd7769d6ca75b486babeb6f","input_id":"44f5e39d042744060c34aa811e8309058b266bd73d4e93aaa4e2a676d6bbdf45","record_id":"cccccccccccccccccccccccccccccccc.1000","outcome":"NO_TRIGGER","reason_code":"INPUT_NORMAL","anomaly_timestamps":[]}]}`
	if string(payload) != want {
		t.Fatalf("wire payload changed:\n got: %s\nwant: %s", payload, want)
	}
}

func validDecisionBatchDocument(t *testing.T) map[string]any {
	t.Helper()
	input := decodedTriggerInputForDecision(t)
	decisions := []TriggerDecision{
		newTriggerDecision(t, input.DetectionOutcomes[0], DecisionOutcomeTrigger, DecisionReasonTriggerConditionMet, intPointer(3), []int64{1569246480}),
		newTriggerDecision(t, input.DetectionOutcomes[1], DecisionOutcomeNoTrigger, DecisionReasonInputNormal, nil, []int64{}),
	}
	batch, err := input.BuildTriggerDecisionBatch(decisions)
	if err != nil {
		t.Fatalf("BuildTriggerDecisionBatch() error = %v", err)
	}
	payload, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("json.Marshal(batch) error = %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("json.Unmarshal(batch) error = %v", err)
	}
	return document
}

func decodedTriggerInputForDecision(t *testing.T) *TriggerInput {
	t.Helper()
	document := newTriggerInputDocument(t, "anomalous")
	document["detection_outcomes"] = []json.RawMessage{
		loadPythonGoldenFixture(t, "anomalous").Outcome,
		loadPythonGoldenFixture(t, "normal").Outcome,
	}
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("json.Marshal(input) error = %v", err)
	}
	input, err := DecodeTriggerInput(payload)
	if err != nil {
		t.Fatalf("DecodeTriggerInput() error = %v", err)
	}
	return input
}

func newTriggerDecision(t *testing.T, outcome *DetectionOutcome, decisionOutcome, reason string, level *int, timestamps []int64) TriggerDecision {
	t.Helper()
	decisionID, err := DeriveTriggerDecisionID(outcome.InputID)
	if err != nil {
		t.Fatalf("DeriveTriggerDecisionID() error = %v", err)
	}
	return TriggerDecision{
		DecisionID:        decisionID,
		InputID:           outcome.InputID,
		RecordID:          outcome.Record.RecordID,
		Outcome:           decisionOutcome,
		ReasonCode:        reason,
		Level:             level,
		AnomalyTimestamps: timestamps,
	}
}

func directNormalDecisionBatch(t *testing.T, count int) *TriggerDecisionBatch {
	t.Helper()

	batch := &TriggerDecisionBatch{
		Schema:               Schema{Name: triggerDecisionBatchSchema, Major: 1, Minor: 0},
		RequiredFeatures:     []string{},
		PartitionHashVersion: PartitionHashVersionV1,
		BatchID:              "batch-1",
		TenantID:             "default",
		Purpose:              PurposeDetect,
		StrategyRef: StrategyRef{
			StrategyID:    "1",
			ItemID:        "2",
			Generation:    "generation-1",
			ContentSHA256: strings.Repeat("a", 64),
		},
		DecisionAlgorithm: DecisionAlgorithmV1,
		Decisions:         make([]TriggerDecision, 0, count),
	}
	for index := 0; index < count; index++ {
		recordID := fmt.Sprintf("%s.%d", strings.Repeat("c", 32), 1000+index)
		inputID, err := DeriveInputID(InputIdentity{
			TenantID:              batch.TenantID,
			Purpose:               batch.Purpose,
			StrategyID:            batch.StrategyRef.StrategyID,
			ItemID:                batch.StrategyRef.ItemID,
			StrategyContentSHA256: batch.StrategyRef.ContentSHA256,
			RecordID:              recordID,
		})
		if err != nil {
			t.Fatalf("DeriveInputID() error = %v", err)
		}
		decisionID, err := DeriveTriggerDecisionID(inputID)
		if err != nil {
			t.Fatalf("DeriveTriggerDecisionID() error = %v", err)
		}
		batch.Decisions = append(batch.Decisions, TriggerDecision{
			DecisionID:        decisionID,
			InputID:           inputID,
			RecordID:          recordID,
			Outcome:           DecisionOutcomeNoTrigger,
			ReasonCode:        DecisionReasonInputNormal,
			AnomalyTimestamps: []int64{},
		})
	}
	if err := batch.Validate(); err != nil {
		t.Fatalf("batch.Validate() error = %v", err)
	}
	return batch
}

func intPointer(value int) *int { return &value }

func cloneJSONMap(t *testing.T, value any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(clone) error = %v", err)
	}
	var clone map[string]any
	if err := json.Unmarshal(payload, &clone); err != nil {
		t.Fatalf("json.Unmarshal(clone) error = %v", err)
	}
	return clone
}
