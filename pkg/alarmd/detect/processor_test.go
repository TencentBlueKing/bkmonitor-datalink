// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type recordingDecisionSink struct {
	batches []*contract.TriggerDecisionBatch
}

func (s *recordingDecisionSink) WriteBatch(_ context.Context, batch *contract.TriggerDecisionBatch) error {
	s.batches = append(s.batches, batch)
	return nil
}

func TestProcessorRunsThresholdDetectBeforeTrigger(t *testing.T) {
	legacy := []byte(`{"id":1,"update_time":1,"items":[{"id":2,"query_configs":[{"agg_interval":60}],"algorithms":[{"level":1,"type":"Threshold","config":[[{"method":"gte","threshold":50}]]}],"no_data_config":{"is_enabled":false}}],"detects":[{"level":1,"connector":"and","trigger_config":{"count":2,"check_window":2}}]}`)
	digest := sha256.Sum256(legacy)
	strategy := map[string]any{
		"schema":                    map[string]any{"name": "trigger-strategy-ir", "major": 1, "minor": 0},
		"required_features":         []string{"raw-strategy-bytes-v1"},
		"tenant_id":                 "default",
		"purpose":                   "DETECT",
		"strategy_ref":              map[string]any{"strategy_id": "1", "item_id": "2", "generation": "1", "content_sha256": hex.EncodeToString(digest[:])},
		"required_levels":           []int{1},
		"check_window_unit_seconds": 60,
		"trigger_configs":           []map[string]any{{"level": 1, "check_window_size": 2, "trigger_count": 2}},
		"legacy_json_b64":           base64.StdEncoding.EncodeToString(legacy),
	}

	sink := &recordingDecisionSink{}
	processor := NewProcessor(sink)
	for index, sourceTime := range []int64{100, 160} {
		recordID := "342a08e0f85f169a7e099c18db3708ed." + strconv.FormatInt(sourceTime, 10)
		document := map[string]any{
			"schema":                 map[string]any{"name": "detect-input", "major": 1, "minor": 0},
			"required_features":      []string{},
			"partition_hash_version": "trigger-input-partition-v1",
			"strategy_ir":            strategy,
			"batch_id":               "batch-" + strconv.Itoa(index+1),
			"records":                []map[string]any{{"record_id": recordID, "time": sourceTime, "value": 60 + index}},
		}
		payload, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		input, err := contract.DecodeDetectInput(payload)
		if err != nil {
			t.Fatalf("DecodeDetectInput() error = %v", err)
		}
		key, err := input.PartitionKey()
		if err != nil {
			t.Fatal(err)
		}
		if err := processor.Process(context.Background(), key, payload); err != nil {
			t.Fatalf("Process(%d) error = %v", sourceTime, err)
		}
	}

	if len(sink.batches) != 2 {
		t.Fatalf("batches = %d, want 2", len(sink.batches))
	}
	first := sink.batches[0].Decisions[0]
	if first.Outcome != "NO_TRIGGER" || first.ReasonCode != "TRIGGER_CONDITION_NOT_MET" {
		t.Fatalf("first decision = %#v", first)
	}
	second := sink.batches[1].Decisions[0]
	if second.Outcome != "TRIGGER" || second.Level == nil || *second.Level != 1 {
		t.Fatalf("second decision = %#v", second)
	}
	if len(second.AnomalyTimestamps) != 2 || second.AnomalyTimestamps[0] != 100 || second.AnomalyTimestamps[1] != 160 {
		t.Fatalf("anomaly timestamps = %v", second.AnomalyTimestamps)
	}
}

func TestProcessorMatchesPythonThresholdUnitConversion(t *testing.T) {
	t.Parallel()

	legacy := []byte(`{"id":1,"update_time":1,"items":[{"id":2,"query_configs":[{"agg_interval":60,"unit":"percentunit"}],"algorithms":[{"level":1,"type":"Threshold","unit_prefix":"%","config":[[{"method":"gte","threshold":80}]]}],"no_data_config":{"is_enabled":false}}],"detects":[{"level":1,"connector":"and","trigger_config":{"count":1,"check_window":1}}]}`)
	digest := sha256.Sum256(legacy)
	strategy := map[string]any{
		"schema":                    map[string]any{"name": "trigger-strategy-ir", "major": 1, "minor": 0},
		"required_features":         []string{"raw-strategy-bytes-v1"},
		"tenant_id":                 "default",
		"purpose":                   "DETECT",
		"strategy_ref":              map[string]any{"strategy_id": "1", "item_id": "2", "generation": "1", "content_sha256": hex.EncodeToString(digest[:])},
		"required_levels":           []int{1},
		"check_window_unit_seconds": 60,
		"trigger_configs":           []map[string]any{{"level": 1, "check_window_size": 1, "trigger_count": 1}},
		"legacy_json_b64":           base64.StdEncoding.EncodeToString(legacy),
	}
	recordID := "342a08e0f85f169a7e099c18db3708ed.100"
	invalidRecordID := "342a08e0f85f169a7e099c18db3708ed.160"
	payload, err := json.Marshal(map[string]any{
		"schema":                 map[string]any{"name": "detect-input", "major": 1, "minor": 0},
		"required_features":      []string{},
		"partition_hash_version": contract.PartitionHashVersionV1,
		"strategy_ir":            strategy,
		"batch_id":               "batch-1",
		"records": []map[string]any{
			{"record_id": recordID, "time": 100, "value": 0.8},
			{"record_id": invalidRecordID, "time": 160, "value": nil},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	input, err := contract.DecodeDetectInput(payload)
	if err != nil {
		t.Fatalf("DecodeDetectInput() error = %v", err)
	}
	key, err := input.PartitionKey()
	if err != nil {
		t.Fatal(err)
	}

	sink := &recordingDecisionSink{}
	if err := NewProcessor(sink).Process(context.Background(), key, payload); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	decision := sink.batches[0].Decisions[0]
	if decision.Outcome != contract.DecisionOutcomeTrigger {
		t.Fatalf("decision = %#v, want 0.8 percentunit to equal 80 percent", decision)
	}
	if decision := sink.batches[0].Decisions[1]; decision.Outcome != contract.DecisionOutcomeNoTrigger || decision.ReasonCode != contract.DecisionReasonInputNormal {
		t.Fatalf("invalid-value decision = %#v, want Python-compatible INPUT_NORMAL", decision)
	}
}

func TestThresholdUnitScalesMatchPythonBaseConversion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		unit            string
		thresholdSuffix string
		wantValue       float64
		wantThreshold   float64
	}{
		{name: "percent unit", unit: "percentunit", thresholdSuffix: "%", wantValue: 100, wantThreshold: 1},
		{name: "binary data", unit: "Data (IEC)||kbytes", thresholdSuffix: "Mi", wantValue: 1024, wantThreshold: 1024 * 1024},
		{name: "decimal rate", unit: "MBs", thresholdSuffix: "k", wantValue: 1_000_000, wantThreshold: 1_000},
		{name: "time", unit: "s", thresholdSuffix: "m", wantValue: 1_000_000_000, wantThreshold: 60_000_000_000},
		{name: "unknown suffix", unit: "bytes", thresholdSuffix: "bogus", wantValue: 1, wantThreshold: 1},
		{name: "fixed unit", unit: "celsius", thresholdSuffix: "", wantValue: 1, wantThreshold: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value, threshold := thresholdUnitScales(test.unit, test.thresholdSuffix)
			if value != test.wantValue || threshold != test.wantThreshold {
				t.Fatalf("thresholdUnitScales(%q, %q) = (%v, %v), want (%v, %v)", test.unit, test.thresholdSuffix, value, threshold, test.wantValue, test.wantThreshold)
			}
		})
	}
}

func TestProcessorDoesNotApplyTriggerWireLimitInsideTheProcess(t *testing.T) {
	t.Parallel()

	legacy := []byte(`{"id":1,"update_time":1,"items":[{"id":2,"query_configs":[{"agg_interval":60}],"algorithms":[{"level":1,"type":"Threshold","config":[[{"method":"gte","threshold":50}]]}],"no_data_config":{"is_enabled":false}}],"detects":[{"level":1,"connector":"and","trigger_config":{"count":1,"check_window":1}}]}`)
	digest := sha256.Sum256(legacy)
	strategy := map[string]any{
		"schema":                    map[string]any{"name": "trigger-strategy-ir", "major": 1, "minor": 0},
		"required_features":         []string{"raw-strategy-bytes-v1"},
		"tenant_id":                 "default",
		"purpose":                   "DETECT",
		"strategy_ref":              map[string]any{"strategy_id": "1", "item_id": "2", "generation": "1", "content_sha256": hex.EncodeToString(digest[:])},
		"required_levels":           []int{1},
		"check_window_unit_seconds": 60,
		"trigger_configs":           []map[string]any{{"level": 1, "check_window_size": 1, "trigger_count": 1}},
		"legacy_json_b64":           base64.StdEncoding.EncodeToString(legacy),
	}
	recordID := "342a08e0f85f169a7e099c18db3708ed.100"
	document := map[string]any{
		"schema":                 map[string]any{"name": "detect-input", "major": 1, "minor": 0},
		"required_features":      []string{},
		"partition_hash_version": contract.PartitionHashVersionV1,
		"strategy_ir":            strategy,
		"batch_id":               "batch-1",
		"records":                []map[string]any{{"record_id": recordID, "time": 100, "value": 60, "padding": ""}},
	}
	basePayload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	padding := contract.MaxDetectInputBytesV1 - len(basePayload) - 16
	document["records"].([]map[string]any)[0]["padding"] = strings.Repeat("x", padding)
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > contract.MaxDetectInputBytesV1 {
		t.Fatalf("detect input bytes = %d, want <= %d", len(payload), contract.MaxDetectInputBytesV1)
	}
	input, err := contract.DecodeDetectInput(payload)
	if err != nil {
		t.Fatalf("DecodeDetectInput() error = %v", err)
	}
	key, err := input.PartitionKey()
	if err != nil {
		t.Fatal(err)
	}

	sink := &recordingDecisionSink{}
	if err := NewProcessor(sink).Process(context.Background(), key, payload); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if len(sink.batches) != 1 || len(sink.batches[0].Decisions) != 1 {
		t.Fatalf("batches = %#v, want one terminal decision", sink.batches)
	}
}

func TestProcessorProcessesMaximumRecordCount(t *testing.T) {
	legacy := []byte(`{"id":1,"update_time":1,"items":[{"id":2,"query_configs":[{"agg_interval":60}],"algorithms":[{"level":1,"type":"Threshold","config":[[{"method":"gte","threshold":50}]]}],"no_data_config":{"is_enabled":false}}],"detects":[{"level":1,"connector":"and","trigger_config":{"count":1,"check_window":1}}]}`)
	digest := sha256.Sum256(legacy)
	strategy := map[string]any{
		"schema":                    map[string]any{"name": "trigger-strategy-ir", "major": 1, "minor": 0},
		"required_features":         []string{"raw-strategy-bytes-v1"},
		"tenant_id":                 "default",
		"purpose":                   "DETECT",
		"strategy_ref":              map[string]any{"strategy_id": "1", "item_id": "2", "generation": "1", "content_sha256": hex.EncodeToString(digest[:])},
		"required_levels":           []int{1},
		"check_window_unit_seconds": 60,
		"trigger_configs":           []map[string]any{{"level": 1, "check_window_size": 1, "trigger_count": 1}},
		"legacy_json_b64":           base64.StdEncoding.EncodeToString(legacy),
	}

	records := make([]map[string]any, 0, contract.MaxDetectInputRecordsV1)
	for index := 0; index < contract.MaxDetectInputRecordsV1; index++ {
		sourceTime := int64(100 + index*60)
		records = append(records, map[string]any{
			"record_id": "342a08e0f85f169a7e099c18db3708ed." + strconv.FormatInt(sourceTime, 10),
			"time":      sourceTime,
			"value":     60,
		})
	}
	document := map[string]any{
		"schema":                 map[string]any{"name": "detect-input", "major": 1, "minor": 0},
		"required_features":      []string{},
		"partition_hash_version": contract.PartitionHashVersionV1,
		"strategy_ir":            strategy,
		"batch_id":               strings.Repeat("batch", 250),
		"records":                records,
	}
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > contract.MaxDetectInputBytesV1 {
		t.Fatalf("detect input bytes = %d, want <= %d", len(payload), contract.MaxDetectInputBytesV1)
	}
	input, err := contract.DecodeDetectInput(payload)
	if err != nil {
		t.Fatalf("DecodeDetectInput() error = %v", err)
	}
	key, err := input.PartitionKey()
	if err != nil {
		t.Fatal(err)
	}

	sink := &recordingDecisionSink{}
	if err := NewProcessor(sink).Process(context.Background(), key, payload); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	total := 0
	for _, batch := range sink.batches {
		total += len(batch.Decisions)
	}
	if total != contract.MaxDetectInputRecordsV1 {
		t.Fatalf("decisions = %d, want %d", total, contract.MaxDetectInputRecordsV1)
	}
}
