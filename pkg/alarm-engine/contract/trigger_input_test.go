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
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestDecodeTriggerInputWrapsPythonGoldenContract(t *testing.T) {
	t.Parallel()

	fixture := loadPythonGoldenFixture(t, "anomalous")
	payload, err := json.Marshal(map[string]any{
		"schema":                 map[string]any{"name": "trigger-input", "major": 1, "minor": 0},
		"required_features":      []string{},
		"partition_hash_version": "trigger-input-partition-v1",
		"strategy_ir":            fixture.StrategyIR,
		"detection_outcome":      fixture.Outcome,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	input, err := DecodeTriggerInput(payload)
	if err != nil {
		t.Fatalf("DecodeTriggerInput() error = %v", err)
	}
	if input.DetectionOutcome.Outcome != OutcomeAnomalous {
		t.Fatalf("outcome = %q, want %q", input.DetectionOutcome.Outcome, OutcomeAnomalous)
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
		"detection_outcome":      fixture.Outcome,
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
	var outcome map[string]any
	if err := json.Unmarshal(document["detection_outcome"].(json.RawMessage), &outcome); err != nil {
		t.Fatalf("json.Unmarshal(outcome) error = %v", err)
	}
	outcome["strategy_ref"].(map[string]any)["item_id"] = "3"
	document["detection_outcome"] = outcome
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if _, err := DecodeTriggerInput(payload); err == nil {
		t.Fatal("DecodeTriggerInput() accepted mismatched nested strategy reference")
	}
}

func newTriggerInputDocument(t *testing.T, fixtureName string) map[string]any {
	t.Helper()

	fixture := loadPythonGoldenFixture(t, fixtureName)
	return map[string]any{
		"schema":                 map[string]any{"name": "trigger-input", "major": 1, "minor": 0},
		"required_features":      []string{},
		"partition_hash_version": "trigger-input-partition-v1",
		"strategy_ir":            fixture.StrategyIR,
		"detection_outcome":      fixture.Outcome,
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
