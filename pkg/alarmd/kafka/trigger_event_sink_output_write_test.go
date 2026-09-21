// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The sink tells its caller how many messages the batch became. Under the
// Python-compatible protocol a recovery has no message: a batch of them is
// zero messages, no producer call, and a success -- and the count says so,
// where the line used to read as a write. A batch with anomalies counts the
// messages it handed over, and the recoveries beside them as events without
// one.
func TestTheSinkCountsTheMessagesABatchBecame(t *testing.T) {
	producer := &batchAwareSyncProducer{}
	sink, err := newTriggerEventSink("alarmd-native-results", producer, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if err := sink.ConfigureLegacyOutput(legacyConverterFunc(func(_ context.Context, events []contract.TriggerEventV1) ([]LegacyConvertedEvent, error) {
		converted := make([]LegacyConvertedEvent, len(events))
		for index := range events {
			converted[index] = LegacyConvertedEvent{EventID: events[index].EventID, Payload: json.RawMessage(`{"legacy":true}`), DedupeMD5: "0260bae09d2ae3f75683bd06a76e9479"}
		}
		return converted, nil
	}), "alarmd_legacy", 64<<10); err != nil {
		t.Fatal(err)
	}
	abnormal := legacyEventForTest(t)
	abnormal.WireFormat = contract.WireFormatPythonCompatible
	recovery := legacyRecoveryEventForTest(t)
	recovery.WireFormat = contract.WireFormatPythonCompatible

	ctx, read := observability.ContextWithOutputWriteReport(context.Background())
	if err := sink.WriteBatch(ctx, []contract.TriggerEventV1{recovery, recovery, recovery}); err != nil {
		t.Fatalf("WriteBatch(recoveries) = %v", err)
	}
	if producer.batchCalls != 0 || producer.singleCalls != 0 {
		t.Fatalf("recoveries reached the producer: %+v", producer)
	}
	if facts := read(); facts == nil || facts.Published != 0 || facts.WithoutMessage != 3 {
		t.Fatalf("count for three recoveries = %+v, want 0 messages, 3 events without one", facts)
	}

	ctx, read = observability.ContextWithOutputWriteReport(context.Background())
	if err := sink.WriteBatch(ctx, []contract.TriggerEventV1{abnormal, recovery, abnormal}); err != nil {
		t.Fatalf("WriteBatch(mixed) = %v", err)
	}
	if producer.batchCalls+producer.singleCalls == 0 || len(producer.messages) != 2 {
		t.Fatalf("mixed batch produced %d messages: %+v", len(producer.messages), producer)
	}
	if facts := read(); facts == nil || facts.Published != 2 || facts.WithoutMessage != 1 {
		t.Fatalf("count for two anomalies and a recovery = %+v, want 2 and 1", facts)
	}

	// A caller that gave no place for the count is served as before.
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{abnormal}); err != nil {
		t.Fatalf("WriteBatch without a report = %v", err)
	}
}

// legacyRecoveryEventForTest is the golden event turned into a valid
// recovery -- every level recovered, the observed value normal -- rebuilt
// through the contract so its kind and levels agree, with the frozen
// compatibility context the Python-compatible protocol needs.
func legacyRecoveryEventForTest(t *testing.T) contract.TriggerEventV1 {
	t.Helper()
	legacy := legacyTriggerEventGolden(t)
	legacy.EventKind = contract.TriggerEventRecovery
	for i := range legacy.LevelResults {
		level := &legacy.LevelResults[i]
		level.Result = contract.LevelResultRecovery
		level.DetectEvidence.DetectionResult = "NORMAL"
		level.DetectEvidence.NormalizedValue = json.RawMessage(`0`)
		level.DecisionWindow.Trigger.ObservedAnomalies = 0
		level.DecisionWindow.Recovery.ObservedConsecutiveMisses = level.DecisionWindow.Recovery.RequiredConsecutiveWindows
	}
	legacy.Observed.Values["value"] = json.RawMessage(`0`)
	event, err := contract.BuildTriggerEventV1(contract.TriggerEventBuildInputV1{
		EventKind: legacy.EventKind, TenantID: legacy.TenantID, BusinessID: legacy.BusinessID,
		PlanRef: legacy.PlanRef, RecordRef: legacy.RecordRef, Observed: legacy.Observed,
		LevelResults: legacy.LevelResults, EvaluationTime: legacy.EvaluationTime,
		DetectPlanFingerprint: legacy.DetectPlanFingerprint, TriggerStateFingerprint: legacy.TriggerStateFingerprint,
		ExecutionID: legacy.Trace.ExecutionID, MaxEvidenceBytes: 64 << 10,
		StrategyRef: &contract.StrategySnapshotRef{TenantID: legacy.TenantID, BusinessID: 2, StrategyID: 1001, Revision: 7},
		DedupeMD5:   "0260bae09d2ae3f75683bd06a76e9479",
	})
	if err != nil {
		t.Fatal(err)
	}
	event.LegacyOutput = &contract.LegacyEventContext{Configuration: contract.FreezeLegacyOutput(&contract.LegacyOutputContext{Strategy: json.RawMessage(`{"id":1001,"bk_biz_id":2,"update_time":1,"name":"frozen name"}`), DimensionFields: []string{"host"}, ItemID: "11"}), AnomalyTimestamps: []int64{event.RecordRef.SourceTime}}
	return *event
}
