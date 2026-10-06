// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package linkdoutput

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func decision(mutate func(*contract.TriggerEventV1)) *contract.TriggerEventV1 {
	event := &contract.TriggerEventV1{
		EventID: strings.Repeat("a", 64), EventKind: contract.TriggerEventAbnormal,
		EventSemanticDigest: strings.Repeat("c", 64),
		PrimaryLevelID:      2, TenantID: "tenant-a", BusinessID: "2",
		SignalType: contract.SignalTypeMetric,
		StrategyRef: &contract.StrategySnapshotRef{
			TenantID: "tenant-a", BusinessID: 2, StrategyID: 123, Revision: 7,
		},
		DedupeMD5:      strings.Repeat("b", 32),
		EvaluationTime: 1756684860,
		RecordRef: contract.TriggerRecordRefV1{SourceTime: 1756684800, Dimensions: map[string]json.RawMessage{
			"bk_target_ip":       json.RawMessage(`"127.0.0.1"`),
			"bk_target_cloud_id": json.RawMessage(`"0"`),
			"device":             json.RawMessage(`"sda"`),
		}},
		Observed: contract.TriggerObservedV1{Values: map[string]json.RawMessage{"value": json.RawMessage(`92.5`)}, Unit: "%"},
		LevelResults: []contract.LevelResultV1{{
			LevelID: 2, Priority: 1, Result: contract.LevelResultAbnormal,
			DecisionWindow: contract.DecisionWindowV1{Trigger: contract.TriggerWindowEvidenceV1{
				WindowSize: 5, RequiredAnomalies: 2, ObservedAnomalies: 3, AnomalyBeginTime: 1756684680,
			}},
		}},
		Subject: &contract.MonitorSubjectContext{
			Subject:    contract.MonitorSubject{Type: contract.MonitorSubjectHost, ID: "127.0.0.1|0"},
			Dimensions: map[string]json.RawMessage{"device": json.RawMessage(`"sda"`)},
		},
	}
	if mutate != nil {
		mutate(event)
	}
	return event
}

// noDataDecision is the same strategy's absence round: the tag in the record's
// dimensions, the period count carried on the point, and no measurement.
func noDataDecision() *contract.TriggerEventV1 {
	return decision(func(event *contract.TriggerEventV1) {
		event.RecordRef.Dimensions[contract.NoDataDimensionTag] = json.RawMessage("true")
		event.Observed = contract.TriggerObservedV1{Values: map[string]json.RawMessage{
			contract.NoDataPeriodFactField: json.RawMessage("5"),
		}}
		event.DedupeMD5 = strings.Repeat("d", 32)
	})
}

// twoLevelDecision is the shape the consumer's multi-level contract exists
// for: the same data point crossed the fatal threshold while the warning
// level's recovery window completed.
func twoLevelDecision() *contract.TriggerEventV1 {
	return decision(func(event *contract.TriggerEventV1) {
		event.PrimaryLevelID = 1
		event.LevelResults = []contract.LevelResultV1{
			{LevelID: 1, Priority: 0, Result: contract.LevelResultAbnormal,
				DecisionWindow: contract.DecisionWindowV1{Trigger: contract.TriggerWindowEvidenceV1{
					WindowSize: 5, RequiredAnomalies: 2, ObservedAnomalies: 2, AnomalyBeginTime: 1756684740,
				}}},
			{LevelID: 2, Priority: 1, Result: contract.LevelResultRecovery},
			{LevelID: 3, Priority: 2, Result: contract.LevelResultNormal},
		}
	})
}

func convertRaw(t *testing.T, event *contract.TriggerEventV1) Event {
	t.Helper()
	converter, err := NewConverter(nil)
	if err != nil {
		t.Fatalf("NewConverter() error = %v", err)
	}
	written, err := converter.Convert(event)
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	return written
}

func convert(t *testing.T, event *contract.TriggerEventV1) map[string]json.RawMessage {
	t.Helper()
	written := convertRaw(t, event)
	var message map[string]json.RawMessage
	if err := json.Unmarshal(written.Payload, &message); err != nil {
		t.Fatalf("decode written message: %v", err)
	}
	if err := checkStandardPayload(written.Payload); err != nil {
		t.Fatalf("the consumer's cleaner would refuse this message: %v\n%s", err, written.Payload)
	}
	return message
}

func assertFields(t *testing.T, message map[string]json.RawMessage, want map[string]string) {
	t.Helper()
	for field, expected := range want {
		if got := string(message[field]); got != expected {
			t.Errorf("%s = %s, want %s", field, got, expected)
		}
	}
}

func assertAbsent(t *testing.T, message map[string]json.RawMessage, fields ...string) {
	t.Helper()
	for _, field := range fields {
		if _, written := message[field]; written {
			t.Errorf("%s = %s, want it absent", field, message[field])
		}
	}
}

// The whole message, field by field, because every one of them is read by
// something downstream and a silently wrong one is not a failure there.
//
// The field names are the consumer's, taken from its standard cleaner
// (pkg/linkd/internal/cleaner/raw_event.go), not names chosen here: a message
// whose fields this process finds reasonable and the consumer does not read
// is a message that arrives and does nothing - or, for this consumer, one
// that is refused whole.
func TestAnAbnormalDecisionIsWrittenAsAStandardEvent(t *testing.T) {
	message := convert(t, decision(nil))
	assertFields(t, message, map[string]string{
		"bk_tenant_id": `"tenant-a"`,
		"event_id":     `"` + strings.Repeat("a", 64) + `"`,
		"alert_id":     `"` + strings.Repeat("b", 32) + `"`,
		"title":        `"Strategy 123 level 2 triggered"`,
		"content":      `"value=92.5% at 2025-09-01T00:00:00Z; 3 of 5 points in the window were anomalous"`,
		"values":       `{"value":92.5}`,
		"evaluations":  `[{"severity":"warning","action":"triggered","action_reason":""}]`,
		// Whole, with the target identity still in it.
		"dimensions":  `{"bk_target_cloud_id":"0","bk_target_ip":"127.0.0.1","device":"sda"}`,
		"subject":     `{"system":"cmdb","type":"host","id":"127.0.0.1|0"}`,
		"occurred_at": `"2025-09-01T00:00:00Z"`,
		"produced_at": `"2025-09-01T00:01:00Z"`,
		"labels":      `{"strategy_id":123,"strategy_version":7,"bk_biz_id":2}`,
		"extra_data": `{"anomaly_begin_time":"2025-08-31T23:58:00Z","window":{"size":5,"anomalies":3,"required":2},` +
			`"event_semantic_digest":"` + strings.Repeat("c", 64) + `","signal_type":"metric",` +
			`"evaluation_family":"metric_algorithm","unit":"%"}`,
	})
	// The previous protocol's fields are gone, not renamed beside the new
	// ones: the consumer does not read a top-level severity or action any
	// more, and a message carrying both shapes would be read as whichever
	// the reader happened to look at.
	assertAbsent(t, message, "severity", "action", "action_reason", "data_time", "occurred_time",
		"observation", "strategy", "extra", "record_id", "received_at", "alarm_source_id")
}

// The recovery of the same series: the same alert_id, the consumer's own word
// for it, and the round that decided it.
func TestARecoveryDecisionIsWrittenAsAStandardEvent(t *testing.T) {
	message := convert(t, decision(func(event *contract.TriggerEventV1) {
		event.EventKind = contract.TriggerEventRecovery
		event.LevelResults[0].Result = contract.LevelResultRecovery
		event.EvaluationTime = 1756685160
	}))
	assertFields(t, message, map[string]string{
		"alert_id":    `"` + strings.Repeat("b", 32) + `"`,
		"evaluations": `[{"severity":"warning","action":"resolved","action_reason":""}]`,
		"title":       `"Strategy 123 level 2 resolved"`,
		"occurred_at": `"2025-09-01T00:00:00Z"`,
		"produced_at": `"2025-09-01T00:06:00Z"`,
	})
}

// Every decided level is written and every undecided one is left out. The
// consumer keeps a lifecycle per severity and reads an absent level as
// "nothing said": writing resolved for a level that stayed normal would close
// nothing, and a resolution sent only at the primary level would miss the
// level the consumer's alert is actually open at.
func TestEveryDecidedLevelIsAnEvaluationAndUndecidedOnesAreNot(t *testing.T) {
	message := convert(t, twoLevelDecision())
	assertFields(t, message, map[string]string{
		"evaluations": `[{"severity":"critical","action":"triggered","action_reason":""},` +
			`{"severity":"warning","action":"resolved","action_reason":""}]`,
		"title": `"Strategy 123 level 1 triggered"`,
	})
	var extra map[string]json.RawMessage
	if err := json.Unmarshal(message["extra_data"], &extra); err != nil {
		t.Fatal(err)
	}
	// The window and the anomaly start are the primary level's.
	if string(extra["window"]) != `{"size":5,"anomalies":2,"required":2}` || string(extra["anomaly_begin_time"]) != `"2025-08-31T23:59:00Z"` {
		t.Fatalf("extra_data = %s, want the primary level's window", message["extra_data"])
	}
	unavailable := convert(t, decision(func(event *contract.TriggerEventV1) {
		event.LevelResults = append(event.LevelResults, contract.LevelResultV1{LevelID: 1, Result: contract.LevelResultUnavailable})
	}))
	if string(unavailable["evaluations"]) != `[{"severity":"warning","action":"triggered","action_reason":""}]` {
		t.Fatalf("evaluations = %s, want a level this round could not evaluate left unsaid", unavailable["evaluations"])
	}
}

// An absence round: its own family, no value, the tag in the dimensions, and
// the period count as the evaluation carried it.
func TestANoDataDecisionIsWrittenAsAStandardEvent(t *testing.T) {
	message := convert(t, noDataDecision())
	assertFields(t, message, map[string]string{
		"evaluations": `[{"severity":"warning","action":"triggered","action_reason":""}]`,
		"alert_id":    `"` + strings.Repeat("d", 32) + `"`,
		"content":     `"no data for 5 periods, as of 2025-09-01T00:00:00Z"`,
		"dimensions":  `{"__NO_DATA_DIMENSION__":true,"bk_target_cloud_id":"0","bk_target_ip":"127.0.0.1","device":"sda"}`,
	})
	// No values: the synthetic point carries a marker, not a measurement,
	// and the consumer would accept the count as a number.
	assertAbsent(t, message, "values")
	var extra map[string]json.RawMessage
	if err := json.Unmarshal(message["extra_data"], &extra); err != nil {
		t.Fatal(err)
	}
	if string(extra["no_data_periods"]) != "5" || string(extra["evaluation_family"]) != `"no_data"` {
		t.Fatalf("extra_data = %s, want the period count and the no_data family", message["extra_data"])
	}
	if _, written := extra["unit"]; written {
		t.Fatalf("extra_data = %s, want no unit on an absence", message["extra_data"])
	}
}

// The action has two values and no others.
//
// closed is the consumer's third and is a lifetime decision: it says an alert
// timed out. This process does not see that, and writing it would take a
// decision away from the only component that can make it. The words are
// written out rather than taken from this package's constants: they are the
// consumer's vocabulary, and they are the thing under test.
func TestTheActionIsOnlyEverTriggeredOrResolved(t *testing.T) {
	want := map[string]string{
		contract.TriggerEventAbnormal: "triggered",
		contract.TriggerEventRecovery: "resolved",
	}
	seen := map[string]bool{}
	for kind, expected := range want {
		action, err := actionFor(kind)
		if err != nil {
			t.Fatalf("kind %s: %v", kind, err)
		}
		if action != expected {
			t.Fatalf("kind %s produced action %q, want %q", kind, action, expected)
		}
		seen[action] = true
	}
	if len(seen) != 2 {
		t.Fatalf("the two decision kinds produced %d actions, want one each", len(seen))
	}
	for _, kind := range []string{"", "UPDATED", "CLOSED", contract.LevelResultNormal} {
		if _, err := actionFor(kind); err == nil {
			t.Fatalf("kind %q produced an action", kind)
		}
	}
}

// A declared identity dimension the series did not carry is null here, and
// null is not a value the consumer's dimension type has: one such entry
// refuses the whole message. It is left out, which says the same thing.
func TestANullDimensionIsLeftOutRatherThanRefusedDownstream(t *testing.T) {
	message := convert(t, decision(func(event *contract.TriggerEventV1) {
		event.RecordRef.Dimensions["bk_host_id"] = json.RawMessage("null")
		event.RecordRef.Dimensions["mount"] = json.RawMessage(" null ")
	}))
	if string(message["dimensions"]) != `{"bk_target_cloud_id":"0","bk_target_ip":"127.0.0.1","device":"sda"}` {
		t.Fatalf("dimensions = %s, want the null entries left out", message["dimensions"])
	}
}

// The projection's additional dimensions never repeat a key the record
// carries - the consumer refuses a key present in both maps - and never carry
// a null.
func TestAdditionalDimensionsNeverRepeatARecordDimension(t *testing.T) {
	message := convert(t, decision(func(event *contract.TriggerEventV1) {
		event.RecordRef.Dimensions["app_name"] = json.RawMessage(`"shop"`)
		event.Subject.Subject.Additional = map[string]json.RawMessage{
			"app_name":     json.RawMessage(`"shop-from-strategy"`),
			"service_name": json.RawMessage(`"api"`),
			"empty":        json.RawMessage("null"),
		}
	}))
	var extra map[string]json.RawMessage
	if err := json.Unmarshal(message["extra_data"], &extra); err != nil {
		t.Fatal(err)
	}
	if string(extra["additional_dimensions"]) != `{"service_name":"api"}` {
		t.Fatalf("additional_dimensions = %s, want only what the record did not carry", extra["additional_dimensions"])
	}
	none := convert(t, decision(func(event *contract.TriggerEventV1) {
		event.Subject.Subject.Additional = map[string]json.RawMessage{"device": json.RawMessage(`"sdb"`)}
	}))
	var noneExtra map[string]json.RawMessage
	if err := json.Unmarshal(none["extra_data"], &noneExtra); err != nil {
		t.Fatal(err)
	}
	if _, written := noneExtra["additional_dimensions"]; written {
		t.Fatalf("extra_data = %s, want no additional dimensions when every one repeats the record", none["extra_data"])
	}
}

// Two rounds of the same series carry the same alert key, and a different
// series carries a different one.
//
// The consumer keys its lifecycle on it, so a key that moved between rounds
// would leave every alert open; one shared across series would close
// somebody else's.
func TestTheAlertKeyIsTheSeriesDedupeKey(t *testing.T) {
	event := decision(nil)
	first := convert(t, event)
	second := convert(t, decision(func(e *contract.TriggerEventV1) {
		e.RecordRef.SourceTime = 1756684860
		e.EvaluationTime = 1756684920
	}))
	if string(first["alert_id"]) != string(second["alert_id"]) {
		t.Fatalf("alert_id = %s then %s; two rounds of one series must carry one key",
			first["alert_id"], second["alert_id"])
	}
	if string(first["alert_id"]) != `"`+event.DedupeMD5+`"` {
		t.Fatalf("alert_id = %s, want the series dedupe key %q", first["alert_id"], event.DedupeMD5)
	}
	other := convert(t, noDataDecision())
	if string(other["alert_id"]) == string(first["alert_id"]) {
		t.Fatalf("the absence alert and the threshold alert on one series share key %s", first["alert_id"])
	}
}

// occurred_at is the data point and produced_at is the round that judged it.
// Neither is the clock: the consumer asks that a retried or replayed message
// keep its produced_at, and a converter that read the clock could not
// promise that. So the same decision converts to the same bytes, every time.
func TestTheTimesAreFactsOfTheDecisionAndAConversionIsRepeatable(t *testing.T) {
	event := decision(nil)
	first := convertRaw(t, event)
	second := convertRaw(t, event)
	if !bytes.Equal(first.Payload, second.Payload) {
		t.Fatalf("two conversions of one decision differ:\n%s\n%s", first.Payload, second.Payload)
	}
	message := convert(t, event)
	if string(message["produced_at"]) != `"2025-09-01T00:01:00Z"` || string(message["occurred_at"]) != `"2025-09-01T00:00:00Z"` {
		t.Fatalf("occurred_at = %s produced_at = %s, want the data point and the round", message["occurred_at"], message["produced_at"])
	}
}

// The three labels the consumer's enrichment requires are positive integers
// on the wire - number tokens, not strings - because its processors refuse
// a string, a zero or a fraction with invalid_field and the alert then
// carries no strategy, resource or metric.
func TestTheLabelsArePositiveIntegerNumbers(t *testing.T) {
	message := convert(t, decision(nil))
	var labels map[string]json.Number
	decoder := json.NewDecoder(bytes.NewReader(message["labels"]))
	decoder.UseNumber()
	if err := decoder.Decode(&labels); err != nil {
		t.Fatalf("labels = %s: %v", message["labels"], err)
	}
	for _, name := range []string{"strategy_id", "strategy_version", "bk_biz_id"} {
		value, err := labels[name].Int64()
		if err != nil || value <= 0 {
			t.Fatalf("labels.%s = %s, want a positive integer", name, labels[name])
		}
	}
	converter, _ := NewConverter(nil)
	if _, err := converter.Convert(decision(func(e *contract.TriggerEventV1) { e.BusinessID = "biz" })); err == nil {
		t.Fatal("a business identity that is not a number was written")
	}
}

// The platform's three levels are named; a level beyond them is passed through
// rather than rejected or guessed at, because levels are stated in the strategy
// snapshot and the platform may grow more. Two levels that end up under one
// name are refused, because the consumer would refuse the whole message.
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
	if SeverityIsBuiltIn(contract.LevelResultV1{LevelID: 9}) {
		t.Fatal("a level with neither a built-in name nor its own must be reported as unmapped")
	}
	if !SeverityIsBuiltIn(contract.LevelResultV1{LevelID: 9, LevelCode: "notice"}) {
		t.Fatal("a level that names itself is mapped")
	}
	unmapped := 0
	converter, _ := NewConverter(func(uint32) { unmapped++ })
	if _, err := converter.Convert(decision(func(e *contract.TriggerEventV1) {
		e.LevelResults = append(e.LevelResults, contract.LevelResultV1{LevelID: 9, Result: contract.LevelResultAbnormal})
	})); err != nil || unmapped != 1 {
		t.Fatalf("an unnamed level: err %v, reported %d times, want shipped and reported once", err, unmapped)
	}
	if _, err := converter.Convert(decision(func(e *contract.TriggerEventV1) {
		e.LevelResults = append(e.LevelResults, contract.LevelResultV1{LevelID: 9, LevelCode: "warning", Result: contract.LevelResultAbnormal})
	})); err == nil {
		t.Fatal("two levels under one severity name were written; the consumer refuses that whole")
	}
}

// The consumer refuses a severity over 32 bytes whole. A level code that long
// is written as the derived name and counted as unmapped: the alert arrives
// at the consumer's default level, visibly, instead of not arriving.
func TestALevelCodeTheConsumerCannotCarryIsDerivedAndCounted(t *testing.T) {
	long := strings.Repeat("s", MaxSeverityBytes+1)
	exact := strings.Repeat("s", MaxSeverityBytes)
	if got := severityFor(contract.LevelResultV1{LevelID: 9, LevelCode: long}); got != "level_9" {
		t.Fatalf("severity for a %d-byte code = %q, want the derived name", len(long), got)
	}
	if got := severityFor(contract.LevelResultV1{LevelID: 9, LevelCode: exact}); got != exact {
		t.Fatalf("severity for a %d-byte code = %q, want the code itself: the bound is inclusive", len(exact), got)
	}
	if SeverityIsBuiltIn(contract.LevelResultV1{LevelID: 9, LevelCode: long}) {
		t.Fatal("a level whose code the consumer cannot carry must be reported as unmapped")
	}
	unmapped := 0
	converter, _ := NewConverter(func(uint32) { unmapped++ })
	event, err := converter.Convert(decision(func(e *contract.TriggerEventV1) {
		e.LevelResults = append(e.LevelResults, contract.LevelResultV1{LevelID: 9, LevelCode: long, Result: contract.LevelResultAbnormal})
	}))
	if err != nil || unmapped != 1 {
		t.Fatalf("a level with an over-long code: err %v, reported %d times, want shipped and reported once", err, unmapped)
	}
	if err := checkStandardPayload(event.Payload); err != nil {
		t.Fatalf("the shipped message would be refused by the consumer: %v", err)
	}
	if !strings.Contains(string(event.Payload), `"severity":"level_9"`) || strings.Contains(string(event.Payload), long) {
		t.Fatalf("payload = %s, want the derived name and not the over-long code", event.Payload)
	}
}

// A message carries at most 32 evaluations. The level budget is held below
// that at configuration time (config refuses a larger max_levels_per_plan),
// so the converter's own refusal is a contract violation for a decision that
// was never compiled - and it is a refusal, not a truncation.
func TestMoreDecidedLevelsThanOneMessageCarriesIsRefused(t *testing.T) {
	converter, _ := NewConverter(nil)
	_, err := converter.Convert(decision(func(e *contract.TriggerEventV1) {
		for id := uint32(4); len(e.LevelResults) <= MaxEvaluations; id++ {
			e.LevelResults = append(e.LevelResults, contract.LevelResultV1{LevelID: id, Result: contract.LevelResultAbnormal})
		}
	}))
	if err == nil || !strings.Contains(err.Error(), "evaluations") {
		t.Fatalf("err = %v, want a refusal naming the evaluations bound", err)
	}
	if _, err := converter.Convert(decision(func(e *contract.TriggerEventV1) {
		for id := uint32(4); len(e.LevelResults) < MaxEvaluations; id++ {
			e.LevelResults = append(e.LevelResults, contract.LevelResultV1{LevelID: id, Result: contract.LevelResultAbnormal})
		}
	})); err != nil {
		t.Fatalf("exactly %d decided levels refused: %v", MaxEvaluations, err)
	}
}

// Every object type the projection can produce has a spelling for the
// consumer, and the table spells nothing the projection cannot produce; the
// unknown branch of wireSubjectFor is therefore the empty type alone. Walked
// over the contract's list rather than the ones somebody remembered.
func TestEverySubjectTypeTheProjectionProducesHasAConsumerSpelling(t *testing.T) {
	types := contract.MonitorSubjectTypes()
	if len(types) == 0 {
		t.Fatal("the contract lists no subject types")
	}
	for _, kind := range types {
		naming, known := subjectSystems[kind]
		if !known || naming.System == "" || naming.Type == "" {
			t.Fatalf("subject type %q has no consumer spelling", kind)
		}
		subject := wireSubjectFor(contract.MonitorSubject{Type: kind, ID: "x"})
		if subject == nil || subject.System != naming.System || subject.Type != naming.Type || subject.ID != "x" {
			t.Fatalf("subject type %q written as %+v", kind, subject)
		}
	}
	if len(subjectSystems) != len(types) {
		t.Fatalf("subjectSystems spells %d types, the projection produces %d", len(subjectSystems), len(types))
	}
	if wireSubjectFor(contract.MonitorSubject{Type: "", ID: "x"}) != nil || wireSubjectFor(contract.MonitorSubject{Type: types[0]}) != nil {
		t.Fatal("an empty type or an empty id must write no subject")
	}
}

// A record with no object is a real answer. Writing a subject for it would
// attach the alert to something.
func TestARecordWithNoObjectCarriesNoSubject(t *testing.T) {
	message := convert(t, decision(func(e *contract.TriggerEventV1) {
		e.Subject = &contract.MonitorSubjectContext{Dimensions: map[string]json.RawMessage{}}
	}))
	assertAbsent(t, message, "subject")
	var dimensions map[string]json.RawMessage
	if err := json.Unmarshal(message["dimensions"], &dimensions); err != nil {
		t.Fatal(err)
	}
	if len(dimensions) != 3 {
		t.Fatalf("dimensions = %v, want the record's own", dimensions)
	}
}

// A Plan this build could not label leaves the field out rather than guessing.
func TestAnUnnamedSignalTypeIsOmittedRatherThanGuessed(t *testing.T) {
	message := convert(t, decision(func(e *contract.TriggerEventV1) { e.SignalType = "" }))
	var extra map[string]json.RawMessage
	if err := json.Unmarshal(message["extra_data"], &extra); err != nil {
		t.Fatal(err)
	}
	if _, written := extra["signal_type"]; written {
		t.Fatalf("extra_data = %v, want no signal type", extra)
	}
	if string(extra["evaluation_family"]) != `"metric_algorithm"` {
		t.Fatalf("extra_data = %v: the family is decided here and is always written", extra)
	}
}

// A decision over several values has no single observed value; none is
// written rather than one picked.
func TestSeveralObservedValuesWriteNoValue(t *testing.T) {
	message := convert(t, decision(func(e *contract.TriggerEventV1) {
		e.Observed.Values["other"] = json.RawMessage(`1`)
	}))
	assertAbsent(t, message, "values")
	if !strings.Contains(string(message["content"]), "other=1%") {
		t.Fatalf("content = %s, want every value stated", message["content"])
	}
}

// A decision with no frozen revision has no alert identity here, so it is
// refused rather than written with holes in it.
func TestADecisionWithNoAlertIdentityIsRefused(t *testing.T) {
	for name, mutate := range map[string]func(*contract.TriggerEventV1){
		"no strategy revision": func(e *contract.TriggerEventV1) { e.StrategyRef = nil },
		"no series identity":   func(e *contract.TriggerEventV1) { e.DedupeMD5 = "" },
		"no decided level":     func(e *contract.TriggerEventV1) { e.LevelResults[0].Result = contract.LevelResultNormal },
	} {
		converter, err := NewConverter(nil)
		if err != nil {
			t.Fatalf("NewConverter() error = %v", err)
		}
		if _, err := converter.Convert(decision(mutate)); err == nil {
			t.Fatalf("%s: Convert() accepted a decision it cannot write", name)
		}
	}
}
