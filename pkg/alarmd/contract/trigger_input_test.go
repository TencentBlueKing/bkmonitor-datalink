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
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDecodeTriggerInputWrapsPythonGoldenContract(t *testing.T) {
	t.Parallel()

	fixture := loadPythonGoldenFixture(t, "anomalous")
	normalFixture := loadPythonGoldenFixture(t, "normal")
	payload, err := json.Marshal(map[string]any{
		"schema":                 map[string]any{"name": "trigger-input", "major": 1, "minor": 0},
		"required_features":      []string{},
		"partition_hash_version": "trigger-input-partition-v1",
		"strategy_ir":            fixture.StrategyIR,
		"detection_outcomes":     []json.RawMessage{fixture.Outcome, normalFixture.Outcome},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	input, err := DecodeTriggerInput(payload)
	if err != nil {
		t.Fatalf("DecodeTriggerInput() error = %v", err)
	}
	if len(input.DetectionOutcomes) != 2 || input.DetectionOutcomes[0].Outcome != OutcomeAnomalous || input.DetectionOutcomes[1].Outcome != OutcomeNormal {
		t.Fatalf("outcomes = %#v, want %q then %q", input.DetectionOutcomes, OutcomeAnomalous, OutcomeNormal)
	}
	partitionKey, err := input.PartitionKey()
	if err != nil {
		t.Fatalf("PartitionKey() error = %v", err)
	}
	if got, want := hex.EncodeToString(partitionKey), "76822eff60b83ab18de1ec5ecf6c194f6e933f12af8b28e199f2a43f8a730c27"; got != want {
		t.Fatalf("partition key = %s, want Python publisher key %s", got, want)
	}
}

func TestDecodeTriggerInputRejectsUnsupportedPartitionHashVersion(t *testing.T) {
	t.Parallel()

	fixture := loadPythonGoldenFixture(t, "anomalous")
	payload, err := json.Marshal(map[string]any{
		"schema":                 map[string]any{"name": "trigger-input", "major": 1, "minor": 0},
		"required_features":      []string{},
		"partition_hash_version": "trigger-input-partition-v2",
		"strategy_ir":            fixture.StrategyIR,
		"detection_outcomes":     []json.RawMessage{fixture.Outcome},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if _, err := DecodeTriggerInput(payload); err == nil {
		t.Fatal("DecodeTriggerInput() accepted unsupported partition hash version")
	}
}

func TestDecodeTriggerInputNegotiatesOuterSchemaFailClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(document map[string]any)
		wantErr bool
	}{
		{name: "wrong major", mutate: func(document map[string]any) { document["schema"].(map[string]any)["major"] = 2 }, wantErr: true},
		{name: "negative minor", mutate: func(document map[string]any) { document["schema"].(map[string]any)["minor"] = -1 }, wantErr: true},
		{name: "overflow minor", mutate: func(document map[string]any) { document["schema"].(map[string]any)["minor"] = int64(1 << 31) }, wantErr: true},
		{name: "unknown feature", mutate: func(document map[string]any) { document["required_features"] = []string{"future-required"} }, wantErr: true},
		{name: "minor zero unknown field", mutate: func(document map[string]any) { document["future_optional"] = true }, wantErr: true},
		{
			name: "higher minor optional field",
			mutate: func(document map[string]any) {
				document["schema"].(map[string]any)["minor"] = 1
				document["future_optional"] = true
			},
			wantErr: false,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			document := newTriggerInputDocument(t, "anomalous")
			test.mutate(document)
			payload, err := json.Marshal(document)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			_, err = DecodeTriggerInput(payload)
			if test.wantErr && err == nil {
				t.Fatal("DecodeTriggerInput() error = nil, want schema negotiation error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("DecodeTriggerInput() error = %v, want nil", err)
			}
		})
	}
}

func TestDecodeTriggerInputRejectsNestedStrategyReferenceMismatch(t *testing.T) {
	t.Parallel()

	document := newTriggerInputDocument(t, "anomalous")
	outcomes := document["detection_outcomes"].([]json.RawMessage)
	var outcome map[string]any
	if err := json.Unmarshal(outcomes[0], &outcome); err != nil {
		t.Fatalf("json.Unmarshal(outcome) error = %v", err)
	}
	outcome["strategy_ref"].(map[string]any)["item_id"] = "3"
	document["detection_outcomes"] = []any{outcome}
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if _, err := DecodeTriggerInput(payload); err == nil {
		t.Fatal("DecodeTriggerInput() accepted mismatched nested strategy reference")
	}
}

func TestDecodeTriggerInputRejectsEmptyOrOversizedMicrobatch(t *testing.T) {
	t.Parallel()

	t.Run("empty outcomes", func(t *testing.T) {
		document := newTriggerInputDocument(t, "anomalous")
		document["detection_outcomes"] = []json.RawMessage{}
		payload, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		if _, err := DecodeTriggerInput(payload); err == nil {
			t.Fatal("DecodeTriggerInput() accepted empty outcomes")
		}
	})

	t.Run("too many outcomes", func(t *testing.T) {
		document := newTriggerInputDocument(t, "normal")
		fixture := loadPythonGoldenFixture(t, "normal")
		outcomes := make([]json.RawMessage, MaxTriggerInputItemsV1+1)
		for index := range outcomes {
			outcomes[index] = fixture.Outcome
		}
		document["detection_outcomes"] = outcomes
		payload, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		if _, err := DecodeTriggerInput(payload); err == nil {
			t.Fatal("DecodeTriggerInput() accepted too many outcomes")
		} else if !strings.Contains(err.Error(), "must contain between 1 and 500 outcomes") {
			t.Fatalf("DecodeTriggerInput() error = %v, want item-count limit", err)
		}
	})

	t.Run("encoded bytes exceed limit", func(t *testing.T) {
		payload, err := json.Marshal(newTriggerInputDocument(t, "anomalous"))
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		payload = append(payload, bytes.Repeat([]byte(" "), MaxTriggerInputBytesV1-len(payload)+1)...)
		if _, err := DecodeTriggerInput(payload); err == nil {
			t.Fatal("DecodeTriggerInput() accepted oversized payload")
		}
	})
}

func TestDecodeTriggerInputRejectsMicrobatchIdentityContradictions(t *testing.T) {
	t.Parallel()

	t.Run("batch id mismatch", func(t *testing.T) {
		document := newTriggerInputDocument(t, "anomalous")
		normal := decodeOutcomeDocument(t, loadPythonGoldenFixture(t, "normal").Outcome)
		normal["batch_id"] = "another-batch"
		document["detection_outcomes"] = []any{
			loadPythonGoldenFixture(t, "anomalous").Outcome,
			normal,
		}
		payload, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		if _, err := DecodeTriggerInput(payload); err == nil || !strings.Contains(err.Error(), "share one batch_id") {
			t.Fatalf("DecodeTriggerInput() error = %v, want batch_id mismatch", err)
		}
	})

	t.Run("duplicate input id", func(t *testing.T) {
		document := newTriggerInputDocument(t, "anomalous")
		fixture := loadPythonGoldenFixture(t, "anomalous")
		document["detection_outcomes"] = []json.RawMessage{fixture.Outcome, fixture.Outcome}
		payload, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		if _, err := DecodeTriggerInput(payload); err == nil || !strings.Contains(err.Error(), "duplicate input_id") {
			t.Fatalf("DecodeTriggerInput() error = %v, want duplicate input_id", err)
		}
	})
}

func decodeOutcomeDocument(t *testing.T, payload json.RawMessage) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("json.Unmarshal(outcome) error = %v", err)
	}
	return document
}

func newTriggerInputDocument(t *testing.T, fixtureName string) map[string]any {
	t.Helper()

	fixture := loadPythonGoldenFixture(t, fixtureName)
	return map[string]any{
		"schema":                 map[string]any{"name": "trigger-input", "major": 1, "minor": 0},
		"required_features":      []string{},
		"partition_hash_version": "trigger-input-partition-v1",
		"strategy_ir":            fixture.StrategyIR,
		"detection_outcomes":     []json.RawMessage{fixture.Outcome},
	}
}

func loadPythonGoldenFixture(t *testing.T, name string) goldenFixture {
	t.Helper()

	payload, err := os.ReadFile("testdata/python-v1/detection_outcome_v1.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var fixtures goldenFixtureSet
	if err := json.Unmarshal(payload, &fixtures); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, fixture := range fixtures.Fixtures {
		if fixture.Name == name {
			return fixture
		}
	}
	t.Fatalf("fixture %q not found", name)
	return goldenFixture{}
}
