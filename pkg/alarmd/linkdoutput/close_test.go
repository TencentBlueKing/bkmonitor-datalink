package linkdoutput

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestCloseIsAtEveryLevelAndHasAStableIdentity(t *testing.T) {
	r := CloseRequest{TenantID: "tenant-test", Fingerprint: "0123456789abcdef0123456789abcdef", AlertInstanceID: "active-instance",
		StrategyID: 123, StrategyRevision: 4, BusinessID: 2, OccurredAt: time.Unix(1800000000, 0)}
	a, err := ConvertClose(r)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ConvertClose(r)
	if a.EventID != b.EventID || string(a.Payload) != string(b.Payload) {
		t.Fatal("retry changed identity")
	}
	var payload wireEvent
	if err := json.Unmarshal(a.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.AlertID != r.Fingerprint || len(payload.Evaluations) != 1 || payload.Evaluations[0].Action != "closed" || payload.Evaluations[0].Severity != "__ALL__" || payload.Evaluations[0].ActionReason != CloseReasonInactive {
		t.Fatalf("wrong close: %+v", payload)
	}
	if len(payload.Values) != 0 {
		t.Fatal("closure fabricated a metric recovery")
	}
	r.AlertInstanceID = "next-instance"
	c, _ := ConvertClose(r)
	if a.EventID == c.EventID {
		t.Fatal("new instance reused event identity")
	}
	if err := checkStandardPayload(a.Payload); err != nil {
		t.Fatalf("the consumer would refuse the close: %v", err)
	}
}

// Both strategy closes go out at every level: one evaluation, "__ALL__", the
// level agreed with the alert link for "whatever level the alert is at".
// Neither depends on the reconciliation telling it the alert's level, which
// the link has never done.
func TestBothStrategyClosesGoOutAtEveryLevel(t *testing.T) {
	for _, reason := range []string{"", CloseReasonInactive, CloseReasonAbsent, CloseReasonTargetOutOfScope} {
		r := CloseRequest{TenantID: "tenant-test", Fingerprint: "0123456789abcdef0123456789abcdef", AlertInstanceID: "active-instance",
			StrategyID: 123, StrategyRevision: 4, BusinessID: 2, OccurredAt: time.Unix(1800000000, 0), Reason: reason}
		event, err := ConvertClose(r)
		if err != nil {
			t.Fatalf("%q: %v", reason, err)
		}
		var payload wireEvent
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Evaluations) != 1 || payload.Evaluations[0].Severity != SeverityAllLevels || payload.Evaluations[0].Action != ActionClosed {
			t.Fatalf("%q: a strategy close is not one close at every level: %s", reason, event.Payload)
		}
	}
}

func TestClosePreservesNativeSignedBusinessIdentity(t *testing.T) {
	for _, businessID := range []int64{2, -42} {
		t.Run(strconv.FormatInt(businessID, 10), func(t *testing.T) {
			trigger := decision(func(event *contract.TriggerEventV1) {
				event.BusinessID = strconv.FormatInt(businessID, 10)
				event.StrategyRef.BusinessID = businessID
			})
			opened := convertRaw(t, trigger)
			closed, err := ConvertClose(CloseRequest{
				TenantID: trigger.TenantID, Fingerprint: opened.AlertID, AlertInstanceID: "active-instance",
				StrategyID:       trigger.StrategyRef.StrategyID,
				StrategyRevision: trigger.StrategyRef.Revision, BusinessID: businessID,
				OccurredAt: time.Unix(trigger.EvaluationTime+60, 0),
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range []Event{opened, closed} {
				var wire wireEvent
				if err := json.Unmarshal(event.Payload, &wire); err != nil {
					t.Fatal(err)
				}
				if wire.Labels.BusinessID != businessID || wire.AlertID != opened.AlertID {
					t.Fatalf("event changed business or alert identity: %s", event.Payload)
				}
			}
		})
	}
}
