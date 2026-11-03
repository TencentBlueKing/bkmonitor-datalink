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
	"encoding/json"
	"os"
	"testing"
)

// A built event shares nothing with the input it was built from: writing the
// input afterwards - its levels' Detect evidence (a pointer holding a raw
// value and ordinals by reference), the record's dimensions, the observed
// values - leaves the event's bytes as they were. A caller deriving one event
// from another's input, as a recovery from an abnormal, must not rewrite the
// first.
func TestABuiltEventSharesNothingWithItsInput(t *testing.T) {
	payload, err := os.ReadFile("testdata/go-v2/trigger_event_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	golden, err := DecodeTriggerEventV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	input := TriggerEventBuildInputV1{
		EventKind: golden.EventKind, TenantID: golden.TenantID, BusinessID: golden.BusinessID,
		PlanRef: golden.PlanRef, RecordRef: golden.RecordRef, Observed: golden.Observed, LevelResults: golden.LevelResults,
		EvaluationTime: golden.EvaluationTime, DetectPlanFingerprint: golden.DetectPlanFingerprint,
		TriggerStateFingerprint: golden.TriggerStateFingerprint, ExecutionID: golden.Trace.ExecutionID, MaxEvidenceBytes: 64 << 10,
	}
	// The ordinals are references too; the fixture carries none, so each
	// level is given one the build has to copy.
	for index := range input.LevelResults {
		matched := uint32(0)
		input.LevelResults[index].DetectEvidence.MatchedAlgorithmOrdinal = &matched
	}
	event, err := BuildTriggerEventV1(input)
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	ordinal := uint32(7)
	for index := range input.LevelResults {
		evidence := input.LevelResults[index].DetectEvidence
		if evidence == nil {
			t.Fatal("fixture: a level without Detect evidence")
		}
		evidence.DetectionResult = "NORMAL"
		for at := range evidence.NormalizedValue {
			evidence.NormalizedValue[at] = '9'
		}
		*evidence.MatchedAlgorithmOrdinal = ordinal
		evidence.MatchedGroupOrdinal = &ordinal
	}
	if len(input.RecordRef.Dimensions) == 0 || len(input.Observed.Values) == 0 {
		t.Fatal("fixture: no record dimensions or observed values to write")
	}
	for key, value := range input.RecordRef.Dimensions {
		for at := range value {
			value[at] = '9'
		}
		input.RecordRef.Dimensions[key+"-added"] = json.RawMessage(`"x"`)
	}
	for key, value := range input.Observed.Values {
		for at := range value {
			value[at] = '9'
		}
		input.Observed.Values[key+"-added"] = json.RawMessage(`1`)
	}
	after, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("writing the input changed the built event:\nbefore %s\nafter  %s", before, after)
	}
}
