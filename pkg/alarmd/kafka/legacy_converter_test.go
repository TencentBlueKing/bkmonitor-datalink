package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
	"testing"
)

func legacyEventForTest(t *testing.T) contract.TriggerEventV1 {
	event := legacyTriggerEventGolden(t)
	event.LegacyOutput = &contract.LegacyEventContext{Configuration: contract.FreezeLegacyOutput(&contract.LegacyOutputContext{Strategy: json.RawMessage(`{"id":1001,"bk_biz_id":2,"update_time":1,"name":"frozen name"}`), DimensionFields: []string{"host"}, ItemID: "11"}), AnomalyTimestamps: []int64{event.RecordRef.SourceTime}}
	return event
}

type legacyConverterFunc func(context.Context, []contract.TriggerEventV1) ([]LegacyConvertedEvent, error)

func (f legacyConverterFunc) ConvertBatch(ctx context.Context, events []contract.TriggerEventV1) ([]LegacyConvertedEvent, error) {
	return f(ctx, events)
}

func TestLegacyConversionFailureNeverPublishesOrFallsBack(t *testing.T) {
	// Revision selects the protocol; conversion failure never changes it.
	producer := &batchAwareSyncProducer{}
	sink, _ := newTriggerEventSink("native", producer, &fakeCloser{})
	defer sink.Close()
	if err := sink.ConfigureLegacyOutput(legacyConverterFunc(func(context.Context, []contract.TriggerEventV1) ([]LegacyConvertedEvent, error) {
		return nil, fmt.Errorf("converter must not be reached")
	}), "alarmd_legacy", 64<<10); err != nil {
		t.Fatal(err)
	}
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{legacyTriggerEventGolden(t)}); err == nil {
		t.Fatal("old event lacking context silently fell back to native")
	}
	for _, failure := range []string{"error", "count", "identity", "payload", "dedupe"} {
		t.Run(failure, func(t *testing.T) {
			event := legacyEventForTest(t)
			producer := &batchAwareSyncProducer{}
			sink, _ := newTriggerEventSink("native", producer, &fakeCloser{})
			defer sink.Close()
			converter := legacyConverterFunc(func(context.Context, []contract.TriggerEventV1) ([]LegacyConvertedEvent, error) {
				item := LegacyConvertedEvent{EventID: event.EventID, Payload: json.RawMessage(`{}`), DedupeMD5: "0260bae09d2ae3f75683bd06a76e9479"}
				switch failure {
				case "error":
					return nil, fmt.Errorf("snapshot dependency failed")
				case "count":
					return nil, nil
				case "identity":
					item.EventID = "wrong"
				case "payload":
					item.Payload = json.RawMessage(`invalid`)
				case "dedupe":
					item.DedupeMD5 = "wrong"
				}
				return []LegacyConvertedEvent{item}, nil
			})
			sink.ConfigureLegacyOutput(converter, "alarmd_python-events-1", 1024)
			if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{triggerEventGolden(t), event}); err == nil {
				t.Fatal("conversion failure accepted")
			}
			if producer.batchCalls != 0 || producer.singleCalls != 0 {
				t.Fatal("published before successful conversion")
			}
		})
	}
}

type snapshotStoreFunc func(context.Context, []legacyoutput.Snapshot) error

func (f snapshotStoreFunc) SaveBatch(ctx context.Context, s []legacyoutput.Snapshot) error {
	return f(ctx, s)
}

func TestBuiltInPythonOutputMissingStoreNeverPublishesNative(t *testing.T) {
	producer := &batchAwareSyncProducer{}
	sink, err := newTriggerEventSink("alarmd_event", producer, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{legacyEventForTest(t)}); err == nil {
		t.Fatal("missing snapshot dependency accepted")
	}
	if producer.batchCalls != 0 || producer.singleCalls != 0 {
		t.Fatal("missing snapshot dependency caused a native publish")
	}
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{triggerEventGolden(t)}); err != nil {
		t.Fatalf("native revision must not require service Redis: %v", err)
	}
}

func TestLocalLegacyConverterRoutesFinalKafkaBatch(t *testing.T) {
	for _, kind := range []string{contract.TriggerEventAbnormal, contract.TriggerEventRecovery} {
		t.Run(kind, func(t *testing.T) { testLocalLegacyConverterRoutesFinalKafkaBatch(t, kind) })
	}
}

func testLocalLegacyConverterRoutesFinalKafkaBatch(t *testing.T, kind string) {
	event := legacyEventForTest(t)
	event.LevelResults[1].LevelID = 2
	if kind == contract.TriggerEventRecovery {
		event.EventKind = kind
		event.LegacyOutput.AnomalyTimestamps = nil
		for i := range event.LevelResults {
			level := &event.LevelResults[i]
			level.Result = contract.LevelResultRecovery
			level.DetectEvidence.DetectionResult = "NORMAL"
			level.DetectEvidence.NormalizedValue = json.RawMessage(`0`)
			level.DecisionWindow.Trigger.ObservedAnomalies = 0
			level.DecisionWindow.Recovery.ObservedConsecutiveMisses = level.DecisionWindow.Recovery.RequiredConsecutiveWindows
		}
	}
	rebuilt, err := contract.BuildTriggerEventV1(contract.TriggerEventBuildInputV1{EventKind: event.EventKind, TenantID: event.TenantID, BusinessID: event.BusinessID, PlanRef: event.PlanRef, RecordRef: event.RecordRef, Observed: event.Observed, LevelResults: event.LevelResults, EvaluationTime: event.EvaluationTime, DetectPlanFingerprint: event.DetectPlanFingerprint, TriggerStateFingerprint: event.TriggerStateFingerprint, ExecutionID: event.Trace.ExecutionID, MaxEvidenceBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	rebuilt.LegacyOutput = event.LegacyOutput
	event = *rebuilt
	event.LegacyOutput.Configuration = contract.FreezeLegacyOutput(&contract.LegacyOutputContext{Strategy: json.RawMessage(`{"id":1001,"bk_biz_id":2,"update_time":1,"name":"frozen","scenario":"os","items":[{"id":11,"name":"load","query_configs":[{"metric_id":"system.load","data_type_label":"time_series"}]}]}`), DimensionFields: []string{"host"}, ItemID: "11"})
	saved := 0
	producer := &batchAwareSyncProducer{}
	sink, _ := newTriggerEventSink("native", producer, &fakeCloser{})
	defer sink.Close()
	// Use the built-in converter without calling ConfigureLegacyOutput. Only
	// supply the environment's snapshot store, never an enable flag or topic.
	converter, ok := sink.legacyConverter.(*legacyoutput.Converter)
	if !ok {
		t.Fatal("Go protocol converter is not built in")
	}
	converter.SnapshotPrefix = "prefix"
	converter.Store = snapshotStoreFunc(func(_ context.Context, snapshots []legacyoutput.Snapshot) error { saved += len(snapshots); return nil })
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{event, triggerEventGolden(t), event}); err != nil {
		t.Fatal(err)
	}
	if kind == contract.TriggerEventRecovery {
		// Python's producer builds every message from the anomaly list, so the
		// protocol has no recovery event. The two compatibility-routed events
		// therefore produce no message and no snapshot, and only the native
		// event in the middle is published.
		// One message left, so the producer takes its single-send path rather
		// than the batch one; what matters is that nothing was converted and
		// no snapshot was written.
		if saved != 0 || len(producer.messages) != 1 {
			t.Fatalf("recovery reached the compatibility topic: saved=%d messages=%d", saved, len(producer.messages))
		}
		if producer.messages[0].Topic != "native" {
			t.Fatalf("surviving message went to %q", producer.messages[0].Topic)
		}
		return
	}
	if saved != 1 || producer.batchCalls != 1 || len(producer.messages) != 3 {
		t.Fatal("batch snapshot/ACK boundary lost")
	}
	for i, message := range producer.messages {
		if i == 1 {
			if message.Topic != "native" {
				t.Fatal("native routed to compatibility")
			}
			continue
		}
		raw, _ := message.Value.Encode()
		key, _ := message.Key.Encode()
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		// The partition key stays the dedupe digest so a series keeps one
		// partition, but the payload no longer carries it: Python's producer
		// does not put it on the wire, and a reader that finds one uses it
		// instead of computing its own.
		if _, carried := payload["dedupe_md5"]; carried {
			t.Fatal("compatibility payload carries a field Python never sends")
		}
		if message.Topic != "alarmd_0bkmonitor_backend_event" || payload["status"] != "ABNORMAL" || payload["plugin_id"] != "bkmonitor" || len(key) != 32 {
			t.Fatal("Python payload/topic/key mismatch")
		}
	}
	converter.Store = snapshotStoreFunc(func(context.Context, []legacyoutput.Snapshot) error { return fmt.Errorf("service unavailable") })
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{event}); err == nil || producer.singleCalls != 0 {
		t.Fatal("snapshot failure published event")
	}
}
