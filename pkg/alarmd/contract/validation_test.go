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
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestDetectionOutcomeWireFieldsAreFailClosed(t *testing.T) {
	t.Parallel()

	strategy, outcome := loadGoldenContract(t, "normal")
	payload, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var missingEvaluations map[string]json.RawMessage
	if err := json.Unmarshal(payload, &missingEvaluations); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	delete(missingEvaluations, "evaluations")
	missingEvaluationsPayload, err := json.Marshal(missingEvaluations)
	if err != nil {
		t.Fatalf("json.Marshal(missing evaluations) error = %v", err)
	}
	var nullEvaluations map[string]json.RawMessage
	if err := json.Unmarshal(payload, &nullEvaluations); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	nullEvaluations["evaluations"] = json.RawMessage("null")
	nullEvaluationsPayload, err := json.Marshal(nullEvaluations)
	if err != nil {
		t.Fatalf("json.Marshal(null evaluations) error = %v", err)
	}
	tests := map[string][]byte{
		"missing schema minor": bytes.Replace(payload, []byte(`,"minor":0`), nil, 1),
		"null schema minor":    bytes.Replace(payload, []byte(`"minor":0`), []byte(`"minor":null`), 1),
		"case collision":       append(payload[:len(payload)-1], []byte(`,"Schema":{"name":"detection-outcome","major":1,"minor":0}}`)...),
		"unknown v1 field":     append(payload[:len(payload)-1], []byte(`,"future_optional_diagnostic":true}`)...),
		"missing evaluations":  missingEvaluationsPayload,
		"null evaluations":     nullEvaluationsPayload,
	}
	for name, invalidPayload := range tests {
		name, invalidPayload := name, invalidPayload
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeDetectionOutcome(invalidPayload, strategy); err == nil {
				t.Fatal("DecodeDetectionOutcome() error = nil, want wire validation error")
			}
		})
	}
}

func TestDetectionOutcomeRejectsMissingOrNullEpochCoordinates(t *testing.T) {
	t.Parallel()

	strategy, outcome := loadGoldenContract(t, "normal")
	outcome.Record.RecordID = "2a1850513fa6018c435f9b6359b3fa7d.0"
	outcome.Record.SourceTime = 0
	outcome.Record.DataRaw = json.RawMessage(`{"record_id":"2a1850513fa6018c435f9b6359b3fa7d.0","time":0}`)
	inputID, err := DeriveInputID(InputIdentity{
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
	payload, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	tests := map[string][]byte{
		"missing record source_time": bytes.Replace(payload, []byte(`,"source_time":0`), nil, 1),
		"null record source_time":    bytes.Replace(payload, []byte(`"source_time":0`), []byte(`"source_time":null`), 1),
		"missing data time":          bytes.Replace(payload, []byte(`,"time":0`), nil, 1),
		"null data time":             bytes.Replace(payload, []byte(`"time":0`), []byte(`"time":null`), 1),
	}
	for name, invalidPayload := range tests {
		name, invalidPayload := name, invalidPayload
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeDetectionOutcome(invalidPayload, strategy); err == nil {
				t.Fatal("DecodeDetectionOutcome() error = nil, want wire validation error")
			}
		})
	}
}

func TestDecodersRejectInvalidUTF8(t *testing.T) {
	t.Parallel()

	strategy, outcome := loadGoldenContract(t, "normal")
	payload, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	payload = bytes.Replace(payload, []byte(`"tenant_id":"default"`), []byte{'"', 't', 'e', 'n', 'a', 'n', 't', '_', 'i', 'd', '"', ':', '"', 0xff, '"'}, 1)
	if _, err := DecodeDetectionOutcome(payload, strategy); err == nil {
		t.Fatal("DecodeDetectionOutcome() accepted invalid UTF-8")
	}
}

func TestDecoderRejectsUnpairedJSONSurrogateEscape(t *testing.T) {
	t.Parallel()

	for _, surrogate := range []string{`\ud800`, `\udc00`} {
		strategy, _ := loadGoldenContract(t, "normal")
		payload, err := json.Marshal(strategy)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		payload = bytes.Replace(payload, []byte(`"tenant_id":"default"`), []byte(`"tenant_id":"`+surrogate+`"`), 1)
		if _, err := DecodeTriggerStrategyIR(payload); err == nil {
			t.Fatalf("DecodeTriggerStrategyIR() accepted unpaired JSON surrogate escape %s", surrogate)
		}
	}
}

func TestDecoderAcceptsPairedSurrogateAndReplacementCharacter(t *testing.T) {
	t.Parallel()

	for _, tenant := range []string{`\ud83d\ude00`, `\ufffd`, `\\ud800`} {
		strategy, _ := loadGoldenContract(t, "normal")
		payload, err := json.Marshal(strategy)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		payload = bytes.Replace(payload, []byte(`"tenant_id":"default"`), []byte(`"tenant_id":"`+tenant+`"`), 1)
		if _, err := DecodeTriggerStrategyIR(payload); err != nil {
			t.Fatalf("DecodeTriggerStrategyIR(%s) error = %v", tenant, err)
		}
	}
}

func TestDecoderRejectsFloatingPointOverflow(t *testing.T) {
	t.Parallel()

	strategy, _ := loadGoldenContract(t, "normal")
	payload, err := json.Marshal(strategy)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	payload = append(payload[:len(payload)-1], []byte(`,"future":1e400}`)...)
	payload = bytes.Replace(payload, []byte(`"minor":0`), []byte(`"minor":1`), 1)
	if _, err := DecodeTriggerStrategyIR(payload); err == nil {
		t.Fatal("DecodeTriggerStrategyIR() accepted floating-point overflow")
	}
}

func TestHigherMinorRejectsUnicodeCaseFoldCollision(t *testing.T) {
	t.Parallel()

	strategy, outcome := loadGoldenContract(t, "normal")
	outcome.Schema.Minor++
	payload, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	payload = append(payload[:len(payload)-1], []byte(`,"ſchema":{"name":"detection-outcome","major":1,"minor":1}}`)...)
	if _, err := DecodeDetectionOutcome(payload, strategy); err == nil {
		t.Fatal("DecodeDetectionOutcome() accepted Unicode case-fold collision")
	}
}

func TestTriggerStrategyIRRejectsSemanticContradictions(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*TriggerStrategyIR){
		"unknown major":    func(value *TriggerStrategyIR) { value.Schema.Major = 2 },
		"minor over int32": func(value *TriggerStrategyIR) { value.Schema.Minor = 1 << 31 },
		"unknown required feature": func(value *TriggerStrategyIR) {
			value.RequiredFeatures = append(value.RequiredFeatures, "future-required-feature")
		},
		"unknown purpose": func(value *TriggerStrategyIR) { value.Purpose = "detect" },
		"duplicate required level": func(value *TriggerStrategyIR) {
			value.RequiredLevels = append(value.RequiredLevels, value.RequiredLevels[0])
			value.TriggerConfigs = append(value.TriggerConfigs, value.TriggerConfigs[0])
		},
		"trigger config mismatch": func(value *TriggerStrategyIR) { value.TriggerConfigs[0].Level++ },
		"window over int32":       func(value *TriggerStrategyIR) { value.TriggerConfigs[0].CheckWindowSize = 1 << 31 },
		"content hash mismatch": func(value *TriggerStrategyIR) {
			value.StrategyRef.ContentSHA256 = "0" + value.StrategyRef.ContentSHA256[1:]
		},
		"noncanonical base64": func(value *TriggerStrategyIR) { value.LegacyJSONBase64 += "\n" },
		"duplicate legacy field": func(value *TriggerStrategyIR) {
			legacyJSON := []byte(`{"id":1,"id":2}`)
			value.LegacyJSONBase64 = base64.StdEncoding.EncodeToString(legacyJSON)
			digest := sha256.Sum256(legacyJSON)
			value.StrategyRef.ContentSHA256 = hex.EncodeToString(digest[:])
		},
		"null legacy document": func(value *TriggerStrategyIR) {
			legacyJSON := []byte("null")
			value.LegacyJSONBase64 = base64.StdEncoding.EncodeToString(legacyJSON)
			digest := sha256.Sum256(legacyJSON)
			value.StrategyRef.ContentSHA256 = hex.EncodeToString(digest[:])
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			strategy, _ := loadGoldenContract(t, "normal")
			mutate(strategy)
			payload, err := json.Marshal(strategy)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			if _, err := DecodeTriggerStrategyIR(payload); err == nil {
				t.Fatal("DecodeTriggerStrategyIR() error = nil, want validation error")
			}
		})
	}
}

func TestStrategyIRRejectsFeaturesOwnedByAnotherContract(t *testing.T) {
	t.Parallel()

	strategy, _ := loadGoldenContract(t, "normal")
	strategy.RequiredFeatures = append(strategy.RequiredFeatures, featureRawJSON)
	payload, err := json.Marshal(strategy)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if _, err := DecodeTriggerStrategyIR(payload); err == nil {
		t.Fatal("DecodeTriggerStrategyIR() accepted DetectionOutcome feature")
	}
}

func TestContractControlIntegersUseInt32Bounds(t *testing.T) {
	t.Parallel()

	overflowTests := map[string]func(*TriggerStrategyIR){
		"window unit": func(value *TriggerStrategyIR) { value.CheckWindowUnitSeconds = 1 << 31 },
		"required level": func(value *TriggerStrategyIR) {
			value.RequiredLevels[0] = 1 << 31
			value.TriggerConfigs[0].Level = 1 << 31
		},
		"trigger count": func(value *TriggerStrategyIR) { value.TriggerConfigs[0].TriggerCount = 1 << 31 },
	}
	for name, mutate := range overflowTests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			strategy, _ := loadGoldenContract(t, "normal")
			mutate(strategy)
			if err := strategy.Validate(); err == nil {
				t.Fatal("TriggerStrategyIR.Validate() accepted int32 overflow")
			}
		})
	}

	strategy, _ := loadGoldenContract(t, "normal")
	strategy.Schema.Minor = maxContractInt
	strategy.CheckWindowUnitSeconds = maxContractInt
	strategy.RequiredLevels = []int{maxContractInt}
	strategy.TriggerConfigs = []TriggerConfig{{
		Level: maxContractInt, CheckWindowSize: maxContractInt, TriggerCount: maxContractInt,
	}}
	if err := strategy.Validate(); err != nil {
		t.Fatalf("TriggerStrategyIR.Validate() rejected MaxInt32: %v", err)
	}
}

func TestEvaluationLevelRejectsInt32Overflow(t *testing.T) {
	t.Parallel()

	strategy, outcome := loadGoldenContract(t, "normal")
	outcome.Evaluations[0].Level = 1 << 31
	if err := outcome.Validate(strategy); err == nil {
		t.Fatal("DetectionOutcome.Validate() accepted evaluation.level int32 overflow")
	}
}

func TestDetectionOutcomeRejectsSemanticContradictions(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*DetectionOutcome){
		"unknown major": func(value *DetectionOutcome) { value.Schema.Major = 2 },
		"unknown required feature": func(value *DetectionOutcome) {
			value.RequiredFeatures = append(value.RequiredFeatures, "future-required-feature")
		},
		"feature owned by StrategyIR": func(value *DetectionOutcome) {
			value.RequiredFeatures = append(value.RequiredFeatures, featureRawStrategyBytes)
		},
		"unknown outcome": func(value *DetectionOutcome) { value.Outcome = "DEFERRED" },
		"wrong input id": func(value *DetectionOutcome) {
			value.InputID = "0000000000000000000000000000000000000000000000000000000000000000"
		},
		"wrong record coordinate": func(value *DetectionOutcome) {
			value.Record.SourceTime++
		},
		"incomplete business levels": func(value *DetectionOutcome) {
			value.Evaluations = nil
		},
		"duplicate level": func(value *DetectionOutcome) {
			value.Evaluations = append(value.Evaluations, value.Evaluations[0])
		},
		"normal carries null anomaly": func(value *DetectionOutcome) {
			value.Evaluations[0].Anomaly = json.RawMessage("null")
		},
		"business outcome carries error code": func(value *DetectionOutcome) {
			value.ErrorCode = json.RawMessage(`"UNEXPECTED"`)
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			strategy, outcome := loadGoldenContract(t, "normal")
			mutate(outcome)
			payload, err := json.Marshal(outcome)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			if _, err := DecodeDetectionOutcome(payload, strategy); err == nil {
				t.Fatal("DecodeDetectionOutcome() error = nil, want validation error")
			}
		})
	}
}

func TestBusinessOutcomeRejectsExplicitNullErrorCode(t *testing.T) {
	t.Parallel()

	strategy, outcome := loadGoldenContract(t, "normal")
	payload, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	payload = append(payload[:len(payload)-1], []byte(`,"error_code":null}`)...)
	if _, err := DecodeDetectionOutcome(payload, strategy); err == nil {
		t.Fatal("DecodeDetectionOutcome() accepted explicit null error_code")
	}
}

func TestRecordValuesRejectExplicitNullTimestampAtUnixEpoch(t *testing.T) {
	t.Parallel()

	strategy, outcome := loadGoldenContract(t, "normal")
	outcome.Record.RecordID = "2a1850513fa6018c435f9b6359b3fa7d.0"
	outcome.Record.SourceTime = 0
	outcome.Record.DataRaw = json.RawMessage(`{"record_id":"2a1850513fa6018c435f9b6359b3fa7d.0","time":0,"values":{"timestamp":null}}`)
	inputID, err := DeriveInputID(InputIdentity{
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
	payload, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if _, err := DecodeDetectionOutcome(payload, strategy); err == nil {
		t.Fatal("DecodeDetectionOutcome() accepted explicit null values.timestamp")
	}
}

func TestRecordValuesRemainOpenRawJSON(t *testing.T) {
	t.Parallel()

	for _, values := range []string{`[1,"2"]`, `"opaque"`, `7`} {
		values := values
		t.Run(values, func(t *testing.T) {
			t.Parallel()
			strategy, outcome := loadGoldenContract(t, "normal")
			replaced := bytes.Replace(
				outcome.Record.DataRaw,
				[]byte(`{"load5":50.1,"timestamp":1569246481}`),
				[]byte(values),
				1,
			)
			if bytes.Equal(replaced, outcome.Record.DataRaw) {
				t.Fatal("test fixture did not replace values payload")
			}
			outcome.Record.DataRaw = json.RawMessage(replaced)
			payload, err := json.Marshal(outcome)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			if _, err := DecodeDetectionOutcome(payload, strategy); err != nil {
				t.Fatalf("DecodeDetectionOutcome() error = %v", err)
			}
		})
	}
}

func TestAnomalousEvaluationRequiresMatchingAnomaly(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*DetectionOutcome){
		"missing anomaly": func(value *DetectionOutcome) { value.Evaluations[0].Anomaly = nil },
		"wrong anomaly id": func(value *DetectionOutcome) {
			value.Evaluations[0].Anomaly = json.RawMessage(`{"anomaly_id":"wrong"}`)
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			strategy, outcome := loadGoldenContract(t, "anomalous")
			mutate(outcome)
			payload, err := json.Marshal(outcome)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			if _, err := DecodeDetectionOutcome(payload, strategy); err == nil {
				t.Fatal("DecodeDetectionOutcome() error = nil, want validation error")
			}
		})
	}
}

func TestErrorOutcomeAllowsOnlyUniqueRequiredLevelSubset(t *testing.T) {
	t.Parallel()

	strategy, outcome := loadGoldenContract(t, "error-partial")
	canDrive, err := outcome.CanDriveTrigger(strategy)
	if err != nil {
		t.Fatalf("CanDriveTrigger() error = %v", err)
	}
	if canDrive {
		t.Fatal("ERROR outcome must not drive Trigger")
	}
	outcome.Evaluations = append(outcome.Evaluations, outcome.Evaluations[0])
	payload, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if _, err := DecodeDetectionOutcome(payload, strategy); err == nil {
		t.Fatal("DecodeDetectionOutcome() accepted duplicate partial level")
	}
}

func TestCanDriveTriggerRevalidatesMutatedOutcome(t *testing.T) {
	t.Parallel()

	strategy, outcome := loadGoldenContract(t, "normal")
	outcome.Evaluations = nil
	canDrive, err := outcome.CanDriveTrigger(strategy)
	if err == nil || canDrive {
		t.Fatalf("CanDriveTrigger() = (%v, %v), want (false, validation error)", canDrive, err)
	}
}

func TestHigherMinorIgnoresUnknownOptionalFieldWhenFeaturesAreSupported(t *testing.T) {
	t.Parallel()

	strategy, outcome := loadGoldenContract(t, "normal")
	outcome.Schema.Minor++
	payload, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	payload = append(payload[:len(payload)-1], []byte(`,"future_optional_diagnostic":{"ignored":true}}`)...)
	if _, err := DecodeDetectionOutcome(payload, strategy); err != nil {
		t.Fatalf("DecodeDetectionOutcome() error = %v", err)
	}
}

func TestDecodersRejectDuplicateJSONFields(t *testing.T) {
	t.Parallel()

	if _, err := DecodeTriggerStrategyIR([]byte(`{"schema":null,"schema":null}`)); err == nil {
		t.Fatal("DecodeTriggerStrategyIR() accepted duplicate field")
	}
	strategy, _ := loadGoldenContract(t, "normal")
	if _, err := DecodeDetectionOutcome([]byte(`{"schema":null,"schema":null}`), strategy); err == nil {
		t.Fatal("DecodeDetectionOutcome() accepted duplicate field")
	}
}

func loadGoldenContract(t *testing.T, name string) (*TriggerStrategyIR, *DetectionOutcome) {
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
		if fixture.Name != name {
			continue
		}
		strategy, err := DecodeTriggerStrategyIR(fixture.StrategyIR)
		if err != nil {
			t.Fatalf("DecodeTriggerStrategyIR() error = %v", err)
		}
		outcome, err := DecodeDetectionOutcome(fixture.Outcome, strategy)
		if err != nil {
			t.Fatalf("DecodeDetectionOutcome() error = %v", err)
		}
		return strategy, outcome
	}
	t.Fatalf("fixture %q not found", name)
	return nil, nil
}
