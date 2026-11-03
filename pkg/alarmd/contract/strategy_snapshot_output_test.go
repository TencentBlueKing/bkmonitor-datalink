package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestTriggerEventSnapshotReference(t *testing.T) {
	for _, kind := range []string{TriggerEventAbnormal, TriggerEventRecovery} {
		t.Run(kind, func(t *testing.T) { testTriggerEventSnapshotReference(t, kind) })
	}
}

func testTriggerEventSnapshotReference(t *testing.T, kind string) {
	payload, err := os.ReadFile("testdata/go-v2/trigger_event_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := DecodeTriggerEventV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	if kind == TriggerEventRecovery {
		legacy.EventKind = kind
		for i := range legacy.LevelResults {
			level := &legacy.LevelResults[i]
			level.Result = LevelResultRecovery
			level.DetectEvidence.DetectionResult = "NORMAL"
			level.DetectEvidence.NormalizedValue = json.RawMessage(`0`)
			level.DecisionWindow.Trigger.ObservedAnomalies = 0
			level.DecisionWindow.Recovery.ObservedConsecutiveMisses = level.DecisionWindow.Recovery.RequiredConsecutiveWindows
		}
		legacy.Observed.Values["value"] = json.RawMessage(`0`)
	}
	input := TriggerEventBuildInputV1{
		EventKind: legacy.EventKind, TenantID: legacy.TenantID, BusinessID: legacy.BusinessID,
		PlanRef: legacy.PlanRef, RecordRef: legacy.RecordRef, Observed: legacy.Observed,
		LevelResults: legacy.LevelResults, EvaluationTime: legacy.EvaluationTime,
		DetectPlanFingerprint: legacy.DetectPlanFingerprint, TriggerStateFingerprint: legacy.TriggerStateFingerprint,
		ExecutionID: legacy.Trace.ExecutionID, MaxEvidenceBytes: len(payload),
		StrategyRef: &StrategySnapshotRef{TenantID: "default", BusinessID: 2, StrategyID: 1001, Revision: 7},
	}
	event, err := BuildTriggerEventV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if event.Schema.Minor != 1 || event.EventID == legacy.EventID {
		t.Fatalf("snapshot event identity: %#v", event)
	}
	encoded, err := EncodeTriggerEventV1(event)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeTriggerEventV1(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.EventKind != kind || decoded.StrategyRef == nil || *decoded.StrategyRef != *input.StrategyRef {
		t.Fatalf("reference lost: %#v", decoded.StrategyRef)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire["strategy_ref"]) != `{"bk_tenant_id":"default","strategy_bk_biz_id":2,"strategy_id":1001,"strategy_revision":7}` {
		t.Fatalf("reference wire = %s", wire["strategy_ref"])
	}
	retry, err := BuildTriggerEventV1(input)
	if err != nil {
		t.Fatal(err)
	}
	retryBytes, err := EncodeTriggerEventV1(retry)
	if err != nil {
		t.Fatal(err)
	}
	if retry.EventID != event.EventID || !bytes.Equal(encoded, retryBytes) {
		t.Fatal("same frozen input changed on retry")
	}
	decoded.StrategyRef.Revision++
	if err := ValidateTriggerEventV1(decoded); err == nil {
		t.Fatal("accepted altered snapshot reference")
	}
	tampered, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeTriggerEventV1(tampered); err == nil {
		t.Fatal("reader accepted altered snapshot reference")
	}
	input.StrategyRef.Revision++
	next, err := BuildTriggerEventV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if next.EventID == event.EventID {
		t.Fatal("different revisions share event identity")
	}
	if event.StrategyRef.Revision != 7 {
		t.Fatal("event retains mutable input reference")
	}
	input.StrategyRef.Revision = 7
	input.DedupeMD5 = "0260bae09d2ae3f75683bd06a76e9479"
	withDedupe, err := BuildTriggerEventV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if withDedupe.Schema.Minor != 2 || withDedupe.EventID != event.EventID || withDedupe.EventSemanticDigest != event.EventSemanticDigest {
		t.Fatal("dedupe output changed point identity")
	}
	withDedupeBytes, err := EncodeTriggerEventV1(withDedupe)
	if err != nil {
		t.Fatal(err)
	}
	roundtrip, err := DecodeTriggerEventV1(withDedupeBytes)
	if err != nil || roundtrip.DedupeMD5 != input.DedupeMD5 {
		t.Fatalf("dedupe roundtrip = %+v, %v", roundtrip, err)
	}
	input.DedupeMD5 = "d7cd01b6bca54d4e2e7794f1cb418072"
	changedDedupe, err := BuildTriggerEventV1(input)
	if err != nil || changedDedupe.EventID != event.EventID || changedDedupe.EventSemanticDigest != event.EventSemanticDigest {
		t.Fatalf("dedupe value affected point identity: %v", err)
	}
	withDedupe.DedupeMD5 = "not-md5"
	invalidBytes, err := json.Marshal(withDedupe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeTriggerEventV1(invalidBytes); err == nil {
		t.Fatal("reader accepted malformed dedupe")
	}
	withDedupe.DedupeMD5 = ""
	if err := ValidateTriggerEventV1(withDedupe); err == nil {
		t.Fatal("1.2 accepted missing dedupe")
	}
	input.StrategyRef.BusinessID = 3
	if _, err := BuildTriggerEventV1(input); err == nil {
		t.Fatal("accepted mismatched business")
	}
}

// The reader takes back the recovery evidence the writer puts on a level:
// the windows the walk stepped over and a recovery withheld.
func TestTriggerEventRecoveryEvidenceRoundTrips(t *testing.T) {
	payload, err := os.ReadFile("testdata/go-v2/trigger_event_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := DecodeTriggerEventV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	quiet := &legacy.LevelResults[0]
	if quiet.Result != LevelResultNormal {
		t.Fatalf("fixture level 0 = %s, want NORMAL", quiet.Result)
	}
	quiet.DecisionWindow.Recovery.ObservedConsecutiveMisses = quiet.DecisionWindow.Recovery.RequiredConsecutiveWindows
	quiet.DecisionWindow.Recovery.SkippedWindows = 2
	quiet.DecisionWindow.Recovery.Withheld = RecoveryWithheldZeroCount
	event, err := BuildTriggerEventV1(TriggerEventBuildInputV1{
		EventKind: legacy.EventKind, TenantID: legacy.TenantID, BusinessID: legacy.BusinessID,
		PlanRef: legacy.PlanRef, RecordRef: legacy.RecordRef, Observed: legacy.Observed,
		LevelResults: legacy.LevelResults, EvaluationTime: legacy.EvaluationTime,
		DetectPlanFingerprint: legacy.DetectPlanFingerprint, TriggerStateFingerprint: legacy.TriggerStateFingerprint,
		ExecutionID: legacy.Trace.ExecutionID, MaxEvidenceBytes: len(payload) * 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeTriggerEventV1(event)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeTriggerEventV1(encoded)
	if err != nil {
		t.Fatalf("reader refused the writer's recovery evidence: %v", err)
	}
	if got := decoded.LevelResults[0].DecisionWindow.Recovery; got.SkippedWindows != 2 || got.Withheld != RecoveryWithheldZeroCount {
		t.Fatalf("recovery evidence = %+v, want skipped_windows 2 and withheld zero_count", got)
	}
}

// The reader takes back a recovery's closed ending, which the converter reads
// to write closed rather than resolved.
func TestTriggerEventRecoveryEndRoundTrips(t *testing.T) {
	payload, err := os.ReadFile("testdata/go-v2/trigger_event_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := DecodeTriggerEventV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	legacy.EventKind = TriggerEventRecovery
	for i := range legacy.LevelResults {
		level := &legacy.LevelResults[i]
		level.Result = LevelResultRecovery
		level.DetectEvidence.DetectionResult = "NORMAL"
		level.DetectEvidence.NormalizedValue = json.RawMessage(`0`)
		level.DecisionWindow.Trigger.ObservedAnomalies = 0
		level.DecisionWindow.Recovery.ObservedConsecutiveMisses = level.DecisionWindow.Recovery.RequiredConsecutiveWindows
		level.DecisionWindow.Recovery.End = RecoveryEndClose
	}
	legacy.Observed.Values["value"] = json.RawMessage(`0`)
	event, err := BuildTriggerEventV1(TriggerEventBuildInputV1{
		EventKind: legacy.EventKind, TenantID: legacy.TenantID, BusinessID: legacy.BusinessID,
		PlanRef: legacy.PlanRef, RecordRef: legacy.RecordRef, Observed: legacy.Observed,
		LevelResults: legacy.LevelResults, EvaluationTime: legacy.EvaluationTime,
		DetectPlanFingerprint: legacy.DetectPlanFingerprint, TriggerStateFingerprint: legacy.TriggerStateFingerprint,
		ExecutionID: legacy.Trace.ExecutionID, MaxEvidenceBytes: len(payload) * 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeTriggerEventV1(event)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeTriggerEventV1(encoded)
	if err != nil {
		t.Fatalf("reader refused the writer's recovery end: %v", err)
	}
	for _, level := range decoded.LevelResults {
		if level.DecisionWindow.Recovery.End != RecoveryEndClose {
			t.Fatalf("level %d recovery end = %q, want close", level.LevelID, level.DecisionWindow.Recovery.End)
		}
	}
}
