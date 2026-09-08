package kafka

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestTriggerEventBatchUsesStableDedupePartitionKey(t *testing.T) {
	legacy := triggerEventGolden(t)
	input := contract.TriggerEventBuildInputV1{
		EventKind: legacy.EventKind, TenantID: legacy.TenantID, BusinessID: legacy.BusinessID,
		PlanRef: legacy.PlanRef, RecordRef: legacy.RecordRef, Observed: legacy.Observed,
		LevelResults: append([]contract.LevelResultV1(nil), legacy.LevelResults...), EvaluationTime: legacy.EvaluationTime,
		DetectPlanFingerprint: legacy.DetectPlanFingerprint, TriggerStateFingerprint: legacy.TriggerStateFingerprint,
		ExecutionID: legacy.Trace.ExecutionID, MaxEvidenceBytes: 64 << 10,
		StrategyRef: &contract.StrategySnapshotRef{TenantID: "default", BusinessID: 2, StrategyID: 1001, Revision: 7},
	}
	build := func() contract.TriggerEventV1 {
		t.Helper()
		event, err := contract.BuildTriggerEventV1(input)
		if err != nil {
			t.Fatal(err)
		}
		return *event
	}
	oldSnapshot := build()
	input.DedupeMD5 = "0260bae09d2ae3f75683bd06a76e9479"
	abnormal := build()
	input.StrategyRef.Revision = 8
	input.EventKind = contract.TriggerEventRecovery
	for i := range input.LevelResults {
		level := &input.LevelResults[i]
		level.Result = contract.LevelResultRecovery
		level.DetectEvidence.DetectionResult = "NORMAL"
		level.DetectEvidence.NormalizedValue = json.RawMessage(`0`)
		level.DecisionWindow.Trigger.ObservedAnomalies = 0
		level.DecisionWindow.Recovery.ObservedConsecutiveMisses = level.DecisionWindow.Recovery.RequiredConsecutiveWindows
	}
	recovery := build()
	if abnormal.EventID == recovery.EventID {
		t.Fatal("fixture must have different event IDs")
	}
	events := []contract.TriggerEventV1{legacy, oldSnapshot, abnormal, recovery}
	producer := &batchAwareSyncProducer{}
	sink, err := newTriggerEventSink("native-output", producer, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if err := sink.WriteBatch(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	if producer.batchCalls != 1 || producer.singleCalls != 0 {
		t.Fatal("expected actual SendMessages batch path")
	}
	config, err := NewDecisionProducerOnlyConfig(validDecisionSinkConfig())
	if err != nil {
		t.Fatal(err)
	}
	partitioner := config.Producer.Partitioner("native-output")
	var expectedPartition int32
	for i, message := range producer.messages {
		payload, err := message.Value.Encode()
		if err != nil {
			t.Fatal(err)
		}
		wantPayload, err := contract.EncodeTriggerEventV1(&events[i])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(payload, wantPayload) {
			t.Fatal("partition key changed payload")
		}
		if i < 2 {
			if message.Key != nil {
				t.Fatal("legacy must keep nil key")
			}
			continue
		}
		key, err := message.Key.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if len(key) != 32 || string(key) != input.DedupeMD5 {
			t.Fatalf("key=%q, want 32-byte ASCII hex", key)
		}
		partition, err := partitioner.Partition(message, 17)
		if err != nil {
			t.Fatal(err)
		}
		if i == 2 {
			expectedPartition = partition
		} else if partition != expectedPartition {
			t.Fatal("same series changed partition across kind/revision/event ID")
		}
	}
	invalid := recovery
	invalid.DedupeMD5 = "INVALID"
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{abnormal, invalid}); err == nil {
		t.Fatal("invalid 1.2 accepted")
	}
	if producer.batchCalls != 1 {
		t.Fatal("invalid batch was published")
	}
}
