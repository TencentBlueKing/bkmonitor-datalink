// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v2

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestLegacyGroupOfOneUsesASeparateConcreteInput(t *testing.T) {
	t.Parallel()

	payload := legacyDetectInputPayload(t)
	legacy, err := DecodeLegacyGroupOfOne(context.Background(), payload)
	if err != nil {
		t.Fatalf("DecodeLegacyGroupOfOne() error = %v", err)
	}
	if legacy.Mode() != contract.CompatibilityModeLegacyGroupOfOne {
		t.Fatalf("Mode() = %q, want LEGACY_GROUP_OF_ONE", legacy.Mode())
	}
	if legacy.BatchID() != "legacy-batch" || legacy.RecordCount() != 1 {
		t.Fatalf("legacy input = (%q, %d), want (legacy-batch, 1)", legacy.BatchID(), legacy.RecordCount())
	}
	record, ok := legacy.Record(0)
	if !ok || len(record) == 0 {
		t.Fatal("legacy Record(0) is missing")
	}
	record[0] = 'x'
	again, _ := legacy.Record(0)
	if again[0] == 'x' {
		t.Fatal("LegacyEvaluationInput leaked mutable record backing")
	}
	strategy := legacy.StrategyIR()
	if strategy == nil || len(strategy.RequiredLevels) == 0 {
		t.Fatal("legacy StrategyIR() is missing")
	}
	strategy.RequiredLevels[0] = 99
	if legacy.StrategyIR().RequiredLevels[0] == 99 {
		t.Fatal("LegacyEvaluationInput leaked mutable strategy backing")
	}

	v2Result, err := New(readerLimits()).Decode(context.Background(), payload)
	if err != nil {
		t.Fatalf("v2 Decode() error = %v", err)
	}
	if !v2Result.Rejected || v2Result.Input != nil || v2Result.Terminals.Len() != 1 {
		t.Fatalf("v2 Decode(legacy) = %#v, want one message rejection and no v2 input", v2Result)
	}
}

func legacyDetectInputPayload(t *testing.T) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contract", "testdata", "python-v1", "detection_outcome_v1.json"))
	if err != nil {
		t.Fatalf("ReadFile(python golden) error = %v", err)
	}
	var document struct {
		Fixtures []struct {
			StrategyIR json.RawMessage `json:"strategy_ir"`
			Outcome    json.RawMessage `json:"outcome"`
		} `json:"fixtures"`
	}
	if err := json.Unmarshal(payload, &document); err != nil || len(document.Fixtures) == 0 {
		t.Fatalf("json.Unmarshal(python golden) = %v, fixtures=%d", err, len(document.Fixtures))
	}
	fixture := document.Fixtures[0]
	triggerPayload, err := json.Marshal(map[string]any{
		"schema": map[string]any{"name": "trigger-input", "major": 1, "minor": 0}, "required_features": []string{},
		"partition_hash_version": contract.PartitionHashVersionV1, "strategy_ir": fixture.StrategyIR,
		"detection_outcomes": []json.RawMessage{fixture.Outcome},
	})
	if err != nil {
		t.Fatalf("json.Marshal(trigger input) error = %v", err)
	}
	triggerInput, err := contract.DecodeTriggerInput(triggerPayload)
	if err != nil {
		t.Fatalf("DecodeTriggerInput() error = %v", err)
	}
	detectPayload, err := json.Marshal(map[string]any{
		"schema": map[string]any{"name": "detect-input", "major": 1, "minor": 0}, "required_features": []string{},
		"partition_hash_version": contract.PartitionHashVersionV1, "strategy_ir": fixture.StrategyIR,
		"batch_id": "legacy-batch", "records": []json.RawMessage{triggerInput.DetectionOutcomes[0].Record.DataRaw},
	})
	if err != nil {
		t.Fatalf("json.Marshal(detect input) error = %v", err)
	}
	return detectPayload
}
