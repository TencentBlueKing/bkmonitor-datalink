package linkdoutput

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

// The event id of each existing close reason, to the byte, for one fixed
// request: a close a consumer already took keeps its id when a reason is
// added beside it. "" is the inactive close, the reason every caller sent
// before there was a second one.
func TestTheExistingCloseReasonsKeepTheirEventIDs(t *testing.T) {
	for reason, want := range map[string]string{
		CloseReasonTargetOutOfScope: "1fae3870432d0a3505cd3a39a0562267c2d13546ef09bd52a433c4aa5e24adfa",
		CloseReasonAbsent:           "5cd1810a66374277c3e69e491b8d0368ad0f77ada38fa809758d2b58ae543cb8",
		CloseReasonInactive:         "3fa4c221a6f17cdfea54523e4bcde9ce6a7acaa2f5bff497da38547dc6f094a3",
		"":                          "3fa4c221a6f17cdfea54523e4bcde9ce6a7acaa2f5bff497da38547dc6f094a3",
	} {
		event, err := ConvertClose(CloseRequest{TenantID: "tenant-test", Fingerprint: "0123456789abcdef0123456789abcdef",
			AlertInstanceID: "active-instance", StrategyID: 123, StrategyRevision: 4, BusinessID: 2,
			OccurredAt: time.Unix(1800000000, 0), Reason: reason})
		if err != nil {
			t.Fatalf("%q: %v", reason, err)
		}
		if event.EventID != want {
			t.Errorf("%q: event id %s, want %s", reason, event.EventID, want)
		}
	}
}

// A strategy_changed close names the change in its content and is its own
// event: the same alert closed as absent and as changed is two events.
func TestAStrategyChangedCloseNamesTheChange(t *testing.T) {
	request := CloseRequest{TenantID: "tenant-test", Fingerprint: "0123456789abcdef0123456789abcdef",
		AlertInstanceID: "active-instance", StrategyID: 123, StrategyRevision: 4, BusinessID: 2,
		OccurredAt: time.Unix(1800000000, 0), Reason: CloseReasonStrategyChanged, Detail: StrategyChangedLevel}
	event, err := ConvertClose(request)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Title       string `json:"title"`
		Content     string `json:"content"`
		Evaluations []struct {
			Action       string `json:"action"`
			ActionReason string `json:"action_reason"`
		} `json:"evaluations"`
	}
	if err := json.Unmarshal(event.Payload, &wire); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(wire.Content, " Change: level_removed.") || wire.Title != "Strategy was changed and no longer produces this alert" ||
		len(wire.Evaluations) != 1 || wire.Evaluations[0].Action != ActionClosed || wire.Evaluations[0].ActionReason != "strategy_changed" {
		t.Fatalf("event = %s, want a strategy_changed close naming level_removed", event.Payload)
	}
	request.Reason = CloseReasonAbsent
	absent, err := ConvertClose(request)
	if err != nil || absent.EventID == event.EventID {
		t.Fatalf("absent close id %q err %v, want an id of its own", absent.EventID, err)
	}
}

// The capability a close names is cut to closeDetailLimit bytes, and on a
// rune boundary: a cut through a rune would reach the alert as U+FFFD.
func TestACloseDetailIsCutOnARuneBoundary(t *testing.T) {
	long := strings.Repeat("a", closeDetailLimit+20)
	split := strings.Repeat("a", closeDetailLimit-1) + "能力"
	for detail, want := range map[string]string{
		"NO_DATA_ROSTER_UNSUPPORTED": " Capability: NO_DATA_ROSTER_UNSUPPORTED.",
		long:                         " Capability: " + long[:closeDetailLimit] + ".",
		split:                        " Capability: " + strings.Repeat("a", closeDetailLimit-1) + ".",
	} {
		event, err := ConvertClose(CloseRequest{TenantID: "tenant-test", Fingerprint: "0123456789abcdef0123456789abcdef",
			AlertInstanceID: "active-instance", StrategyID: 123, StrategyRevision: 4, BusinessID: 2,
			OccurredAt: time.Unix(1800000000, 0), Reason: CloseReasonUnsupported, Detail: detail})
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(event.Payload, &wire); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(wire.Content, want) || strings.ContainsRune(wire.Content, utf8.RuneError) {
			t.Fatalf("content for a %d-byte detail = %q, want it to end %q", len(detail), wire.Content, want)
		}
	}
}
