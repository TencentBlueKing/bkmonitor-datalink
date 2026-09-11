// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package linkdoutput

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func decision(mutate func(*contract.TriggerEventV1)) *contract.TriggerEventV1 {
	event := &contract.TriggerEventV1{
		EventID: strings.Repeat("a", 64), EventKind: contract.TriggerEventAbnormal,
		PrimaryLevelID: 2, TenantID: "tenant-a", BusinessID: "2",
		StrategyRef: &contract.StrategySnapshotRef{
			TenantID: "tenant-a", BusinessID: 2, StrategyID: 123, Revision: 7,
		},
		DedupeMD5: strings.Repeat("b", 32),
		RecordRef: contract.TriggerRecordRefV1{SourceTime: 1756684800},
		Observed:  contract.TriggerObservedV1{Values: map[string]json.RawMessage{"value": json.RawMessage(`92.5`)}, Unit: "%"},
		LevelResults: []contract.LevelResultV1{{
			LevelID: 2, Priority: 1, Result: contract.LevelResultAbnormal,
			DecisionWindow: contract.DecisionWindowV1{Trigger: contract.TriggerWindowEvidenceV1{
				WindowSize: 5, RequiredAnomalies: 2, ObservedAnomalies: 3, AnomalyBeginTime: 1756684680,
			}},
		}},
		Subject: &contract.MonitorSubjectContext{
			Subject:    contract.MonitorSubject{Type: contract.MonitorSubjectHost, ID: "101"},
			Dimensions: map[string]json.RawMessage{"device": json.RawMessage(`"sda"`)},
		},
	}
	if mutate != nil {
		mutate(event)
	}
	return event
}

func convert(t *testing.T, event *contract.TriggerEventV1) map[string]json.RawMessage {
	t.Helper()
	converter, err := NewConverter(func() time.Time { return time.Unix(1756684860, 0).UTC() })
	if err != nil {
		t.Fatalf("NewConverter() error = %v", err)
	}
	written, err := converter.Convert(event)
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	var message map[string]json.RawMessage
	if err := json.Unmarshal(written.Payload, &message); err != nil {
		t.Fatalf("decode written message: %v", err)
	}
	return message
}

// The whole message, field by field, because every one of them is read by
// something downstream and a silently wrong one is not a failure there.
func TestADecisionIsWrittenAsTheStandardRawEvent(t *testing.T) {
	message := convert(t, decision(nil))
	for field, want := range map[string]string{
		"bk_tenant_id":  `"tenant-a"`,
		"alert_id":      `"` + strings.Repeat("b", 32) + `"`,
		"severity":      `"warning"`,
		"action":        `"triggered"`,
		"action_reason": `""`,
		"dimensions":    `{"device":"sda"}`,
		"subject":       `{"system":"cmdb","type":"host","id":"101"}`,
		"occurred_at":   `"2025-09-01T00:00:00Z"`,
		"produced_at":   `"2025-09-01T00:01:00Z"`,
		"labels":        `{"strategy_id":123,"strategy_version":7,"bk_biz_id":2}`,
		"extra_data":    `{"anomaly_begin_time":"2025-08-31T23:58:00Z"}`,
	} {
		if got := string(message[field]); got != want {
			t.Errorf("%s = %s, want %s", field, got, want)
		}
	}
}

// occurred_at is the data point the decision was made from, not the moment the
// message was built. Reading the clock for it would make every replayed Slot
// claim the anomaly happened now.
func TestTheEventTimeIsTheDataPointAndNotTheClock(t *testing.T) {
	message := convert(t, decision(nil))
	if string(message["occurred_at"]) == string(message["produced_at"]) {
		t.Fatal("occurred_at must come from the record, not from the clock")
	}
}

func TestRecoveryIsResolvedAndNeverClosed(t *testing.T) {
	message := convert(t, decision(func(e *contract.TriggerEventV1) {
		e.EventKind = contract.TriggerEventRecovery
		e.LevelResults[0].Result = contract.LevelResultRecovery
	}))
	if got := string(message["action"]); got != `"resolved"` {
		t.Fatalf("action = %s, want resolved", got)
	}
}

// Closing an alert is a lifetime decision, and this process does not own one.
func TestNoDecisionKindProducesAClosedAction(t *testing.T) {
	for _, kind := range []string{contract.TriggerEventAbnormal, contract.TriggerEventRecovery} {
		action, err := actionFor(kind)
		if err != nil || action == "closed" {
			t.Fatalf("kind %s produced %q (err %v)", kind, action, err)
		}
	}
}

// The platform's three levels are named; a level beyond them is passed through
// rather than rejected or guessed at, because levels are stated in the strategy
// snapshot and the platform may grow more.
func TestSeverityNamesTheBuiltInLevelsAndPassesOthersThrough(t *testing.T) {
	for name, test := range map[string]struct {
		level contract.LevelResultV1
		want  string
	}{
		"fatal":                     {contract.LevelResultV1{LevelID: 1}, "critical"},
		"warning":                   {contract.LevelResultV1{LevelID: 2}, "warning"},
		"remind":                    {contract.LevelResultV1{LevelID: 3}, "info"},
		"a level with its own name": {contract.LevelResultV1{LevelID: 9, LevelCode: "notice"}, "notice"},
		"a level with no name":      {contract.LevelResultV1{LevelID: 9}, "level_9"},
	} {
		if got := severityFor(test.level); got != test.want {
			t.Errorf("%s: severity = %q, want %q", name, got, test.want)
		}
	}
	// A derived name is the one case the consumer cannot map, so it has to be
	// distinguishable from the rest: it means this build has no mapping for a
	// level the platform grew, and the alert will land on a default severity.
	if SeverityIsBuiltIn(contract.LevelResultV1{LevelID: 9}) {
		t.Fatal("a level with neither a built-in name nor its own must be reported as unmapped")
	}
	if !SeverityIsBuiltIn(contract.LevelResultV1{LevelID: 9, LevelCode: "notice"}) {
		t.Fatal("a level that names itself is mapped")
	}
}

// A record with no object is a real answer. Writing a subject for it would
// attach the alert to something.
func TestARecordWithNoObjectCarriesNoSubject(t *testing.T) {
	message := convert(t, decision(func(e *contract.TriggerEventV1) {
		e.Subject = &contract.MonitorSubjectContext{Dimensions: map[string]json.RawMessage{}}
	}))
	if _, written := message["subject"]; written {
		t.Fatalf("subject = %s, want none", message["subject"])
	}
}

// A decision with no frozen revision has no alert identity here, so it is
// refused rather than written with holes in it.
func TestADecisionWithNoAlertIdentityIsRefused(t *testing.T) {
	for name, mutate := range map[string]func(*contract.TriggerEventV1){
		"no strategy revision": func(e *contract.TriggerEventV1) { e.StrategyRef = nil },
		"no series identity":   func(e *contract.TriggerEventV1) { e.DedupeMD5 = "" },
	} {
		converter, err := NewConverter(time.Now)
		if err != nil {
			t.Fatalf("NewConverter() error = %v", err)
		}
		if _, err := converter.Convert(decision(mutate)); err == nil {
			t.Fatalf("%s: Convert() accepted a decision with no alert identity", name)
		}
	}
}
