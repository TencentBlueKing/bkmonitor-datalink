package contract

import (
	"encoding/json"
	"os"
	"testing"
)

func TestTriggerEventSnapshotReference(t *testing.T) {
	payload, err := os.ReadFile("testdata/go-v2/trigger_event_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := DecodeTriggerEventV1(payload)
	if err != nil {
		t.Fatal(err)
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
	if *decoded.StrategyRef != *input.StrategyRef {
		t.Fatalf("reference lost: %#v", decoded.StrategyRef)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire["strategy_ref"]) != `{"bk_tenant_id":"default","strategy_bk_biz_id":2,"strategy_id":1001,"strategy_revision":7}` {
		t.Fatalf("reference wire = %s", wire["strategy_ref"])
	}
	decoded.StrategyRef.Revision++
	if err := ValidateTriggerEventV1(decoded); err == nil {
		t.Fatal("accepted altered snapshot reference")
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
	input.StrategyRef.BusinessID = 3
	if _, err := BuildTriggerEventV1(input); err == nil {
		t.Fatal("accepted mismatched business")
	}
}
