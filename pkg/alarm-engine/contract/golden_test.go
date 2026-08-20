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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

type goldenFixtureSet struct {
	SchemaVersion string          `json:"schema_version"`
	Fixtures      []goldenFixture `json:"fixtures"`
}

type goldenFixture struct {
	Name       string          `json:"name"`
	StrategyIR json.RawMessage `json:"strategy_ir"`
	Outcome    json.RawMessage `json:"outcome"`
}

type semanticFixtureSet struct {
	SchemaVersion string            `json:"schema_version"`
	Fixtures      []semanticFixture `json:"fixtures"`
}

type semanticFixture struct {
	Name       string             `json:"name"`
	StrategyIR *TriggerStrategyIR `json:"strategy_ir"`
	Outcome    *DetectionOutcome  `json:"outcome"`
}

func TestPythonV1GoldenFixtures(t *testing.T) {
	t.Parallel()

	payload, err := os.ReadFile("testdata/python-v1/detection_outcome_v1.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	checksum, err := os.ReadFile("testdata/python-v1/SHA256SUMS")
	if err != nil {
		t.Fatalf("ReadFile(checksum) error = %v", err)
	}
	wantHashes := parseChecksums(t, checksum)
	gotHashBytes := sha256.Sum256(payload)
	if gotHash := hex.EncodeToString(gotHashBytes[:]); gotHash != wantHashes["detection_outcome_v1.json"] {
		t.Fatalf("fixture hash = %s, want %s", gotHash, wantHashes["detection_outcome_v1.json"])
	}

	var fixtures goldenFixtureSet
	if err := json.Unmarshal(payload, &fixtures); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if fixtures.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %q, want %q", fixtures.SchemaVersion, SchemaVersion)
	}

	wantNames := []string{"normal", "anomalous", "error-partial", "unsupported-empty", "retry-same-input"}
	inputIDs := make(map[string]string, len(fixtures.Fixtures))
	for index, fixture := range fixtures.Fixtures {
		if index >= len(wantNames) || fixture.Name != wantNames[index] {
			t.Fatalf("fixture[%d].name = %q, want %q", index, fixture.Name, wantNames[index])
		}
		strategyIR, err := DecodeTriggerStrategyIR(fixture.StrategyIR)
		if err != nil {
			t.Fatalf("DecodeTriggerStrategyIR(%s) error = %v", fixture.Name, err)
		}
		outcome, err := DecodeDetectionOutcome(fixture.Outcome, strategyIR)
		if err != nil {
			t.Fatalf("DecodeDetectionOutcome(%s) error = %v", fixture.Name, err)
		}
		inputIDs[fixture.Name] = outcome.InputID

		wantCanDrive := fixture.Name == "normal" || fixture.Name == "anomalous" || fixture.Name == "retry-same-input"
		got, err := outcome.CanDriveTrigger(strategyIR)
		if err != nil {
			t.Fatalf("CanDriveTrigger(%s) error = %v", fixture.Name, err)
		}
		if got != wantCanDrive {
			t.Fatalf("CanDriveTrigger(%s) = %v, want %v", fixture.Name, got, wantCanDrive)
		}
		if fixture.Name == "anomalous" && !bytes.Contains(outcome.Record.DataRaw, []byte("9007199254740993")) {
			t.Fatal("large integer was not retained in record.data_raw")
		}
	}
	if len(fixtures.Fixtures) != len(wantNames) {
		t.Fatalf("fixture count = %d, want %d", len(fixtures.Fixtures), len(wantNames))
	}
	if inputIDs["retry-same-input"] != inputIDs["anomalous"] {
		t.Fatal("retry fixture changed stable input_id")
	}
}

func TestGoSemanticProjectionMatchesCrossLanguageGolden(t *testing.T) {
	t.Parallel()

	payload, err := os.ReadFile("testdata/python-v1/detection_outcome_v1.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var fixtures goldenFixtureSet
	if err := json.Unmarshal(payload, &fixtures); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	projection := semanticFixtureSet{SchemaVersion: SchemaVersion}
	for _, fixture := range fixtures.Fixtures {
		strategy, err := DecodeTriggerStrategyIR(fixture.StrategyIR)
		if err != nil {
			t.Fatalf("DecodeTriggerStrategyIR(%s) error = %v", fixture.Name, err)
		}
		outcome, err := DecodeDetectionOutcome(fixture.Outcome, strategy)
		if err != nil {
			t.Fatalf("DecodeDetectionOutcome(%s) error = %v", fixture.Name, err)
		}
		projection.Fixtures = append(projection.Fixtures, semanticFixture{
			Name:       fixture.Name,
			StrategyIR: strategy,
			Outcome:    outcome,
		})
	}
	gotPayload, err := json.Marshal(projection)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	wantPayload, err := os.ReadFile("testdata/python-v1/go_semantic_v1.json")
	if err != nil {
		t.Fatalf("ReadFile(go semantic) error = %v", err)
	}
	checksum, err := os.ReadFile("testdata/python-v1/SHA256SUMS")
	if err != nil {
		t.Fatalf("ReadFile(checksum) error = %v", err)
	}
	wantHashes := parseChecksums(t, checksum)
	gotHashBytes := sha256.Sum256(wantPayload)
	if gotHash := hex.EncodeToString(gotHashBytes[:]); gotHash != wantHashes["go_semantic_v1.json"] {
		t.Fatalf("Go semantic hash = %s, want %s", gotHash, wantHashes["go_semantic_v1.json"])
	}
	got, err := decodeSemanticJSON(gotPayload)
	if err != nil {
		t.Fatalf("decode generated semantic JSON: %v", err)
	}
	want, err := decodeSemanticJSON(wantPayload)
	if err != nil {
		t.Fatalf("decode expected semantic JSON: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("Go semantic projection differs from Python verifier fixture")
	}
}

func parseChecksums(t *testing.T, payload []byte) map[string]string {
	t.Helper()

	fields := strings.Fields(string(payload))
	if len(fields)%2 != 0 {
		t.Fatal("SHA256SUMS must contain hash/file pairs")
	}
	checksums := make(map[string]string, len(fields)/2)
	for index := 0; index < len(fields); index += 2 {
		checksums[fields[index+1]] = fields[index]
	}
	return checksums
}

func TestSchemaVersionFileMatchesContract(t *testing.T) {
	t.Parallel()

	payload, err := os.ReadFile("../SCHEMA_VERSION")
	if err != nil {
		t.Fatalf("ReadFile(SCHEMA_VERSION) error = %v", err)
	}
	if got := strings.TrimSpace(string(payload)); got != SchemaVersion {
		t.Fatalf("SCHEMA_VERSION = %q, want %q", got, SchemaVersion)
	}
}

func decodeSemanticJSON(payload []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}
