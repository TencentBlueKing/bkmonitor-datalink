// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// One event the converter will not write used to take its whole batch with
// it -- every series of the strategy, every round, for as long as the one
// series kept deciding the same thing. The refusal is now that event's: the
// others go out, the refused one is named by the rule it broke, and the
// other events of its series are withheld beside it, since the series'
// State moves as one.

// standardSeriesEvent is the golden standard event as the decision of one
// series: its own event, alert and dimension identities.
func standardSeriesEvent(t *testing.T, series, event string) contract.TriggerEventV1 {
	t.Helper()
	decision := triggerEventGolden(t)
	decision.WireFormat = contract.WireFormatStandardRawEvent
	decision.DedupeMD5 = fmt.Sprintf("%032x", len(series)*7919+int(series[len(series)-1]))
	restamp(t, &decision, series, event)
	return decision
}

// restamp makes decision the one of series, told apart from the others of
// that series by event: its own record and the event identity derived from
// it, so the event still validates as the stable identity of its content.
func restamp(t *testing.T, decision *contract.TriggerEventV1, series, event string) {
	t.Helper()
	decision.RecordRef.DimensionIdentityDigest = strings.Repeat(series[len(series)-1:], 64)
	recordID, err := contract.DeriveRecordIDV2(strings.Repeat(event[len(event)-1:], 64), decision.RecordRef.SourceTime)
	if err != nil {
		t.Fatal(err)
	}
	decision.RecordRef.RecordID = recordID
	decision.EventID, err = contract.DeriveTriggerEventIDV1(decision.TenantID, decision.BusinessID, decision.PlanRef.StrategyID,
		decision.PlanRef.StateCompatibilityHash, decision.RecordRef.RecordID, decision.EventKind, decision.EventSemanticDigest)
	if err != nil {
		t.Fatal(err)
	}
}

type countingProducer struct {
	mu   sync.Mutex
	keys []string
}

func (producer *countingProducer) sink(t *testing.T) *TriggerEventSink {
	t.Helper()
	fake := &fakeSyncProducer{send: func(message *sarama.ProducerMessage) (int32, int64, error) {
		producer.mu.Lock()
		defer producer.mu.Unlock()
		key, _ := message.Key.Encode()
		producer.keys = append(producer.keys, string(key))
		return 0, 0, nil
	}}
	sink, err := newTriggerEventSink("alarmd-trigger-event", fake, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	return sink
}

func partialOf(t *testing.T, err error) *OutputPartiallyRejectedError {
	t.Helper()
	var partial *OutputPartiallyRejectedError
	if !errors.As(err, &partial) {
		t.Fatalf("WriteBatch() = %v (%T), want an OutputPartiallyRejectedError", err, err)
	}
	var dependency interface{ RetryableOutputDependency() }
	if errors.As(err, &dependency) {
		t.Fatalf("WriteBatch() = %v marks RetryableOutputDependency", err)
	}
	var whole *OutputRejectedError
	if errors.As(err, &whole) {
		t.Fatalf("WriteBatch() = %v is a whole-batch rejection, want the rest of the batch written", err)
	}
	if partial.OutputRejectionReason() != contract.ReasonOutputConversionRejected {
		t.Fatalf("reason = %q, want %q", partial.OutputRejectionReason(), contract.ReasonOutputConversionRejected)
	}
	return partial
}

func TestARefusedEventLeavesTheRestOfItsBatchWritten(t *testing.T) {
	t.Parallel()
	producer := &countingProducer{}
	sink := producer.sink(t)
	good, refused, other := standardSeriesEvent(t, "series-a", "a"), standardSeriesEvent(t, "series-b", "b"), standardSeriesEvent(t, "series-c", "c")
	refused.BusinessID = "biz-2"

	ctx, report := observability.ContextWithOutputWriteReport(context.Background())
	partial := partialOf(t, sink.WriteBatch(ctx, []contract.TriggerEventV1{good, refused, other}))

	if len(producer.keys) != 2 || producer.keys[0] != good.DedupeMD5 || producer.keys[1] != other.DedupeMD5 {
		t.Fatalf("written alert keys = %v, want the two events that were not refused", producer.keys)
	}
	if len(partial.Rejected) != 1 || partial.Rejected[0].EventID != refused.EventID ||
		partial.Rejected[0].Rule != linkdoutput.RuleBusinessIdentity || partial.Rejected[0].StrategyID != refused.PlanRef.StrategyID {
		t.Fatalf("rejected = %+v, want the one refused event by the rule it broke", partial.Rejected)
	}
	if len(partial.Withheld) != 0 {
		t.Fatalf("withheld = %v, want none: no other event shares the refused series", partial.Withheld)
	}
	if ids := partial.OutputNotWrittenEventIDs(); len(ids) != 1 || ids[0] != refused.EventID {
		t.Fatalf("not written = %v, want only the refused event", ids)
	}
	facts := report()
	if facts == nil || facts.Published != 2 || facts.WithoutMessage != 0 || len(facts.Rejected) != 1 ||
		facts.Rejected[0].Rule != observability.OutputRejectStandardBusinessIdentity || facts.Withheld != 0 {
		t.Fatalf("write report = %+v, want 2 published, none without message, 1 rejected by its rule", facts)
	}
}

// A series goes out whole or not at all: an event written while its
// sibling was refused would sit at the consumer with no State behind it.
func TestTheOtherEventsOfARefusedSeriesAreWithheld(t *testing.T) {
	t.Parallel()
	producer := &countingProducer{}
	sink := producer.sink(t)
	refused, sibling, other := standardSeriesEvent(t, "series-a", "a1"), standardSeriesEvent(t, "series-a", "a2"), standardSeriesEvent(t, "series-c", "c")
	refused.PrimaryLevelID = 99

	ctx, report := observability.ContextWithOutputWriteReport(context.Background())
	partial := partialOf(t, sink.WriteBatch(ctx, []contract.TriggerEventV1{refused, sibling, other}))

	if len(producer.keys) != 1 || producer.keys[0] != other.DedupeMD5 {
		t.Fatalf("written alert keys = %v, want only the event of the other series", producer.keys)
	}
	if len(partial.Rejected) != 1 || partial.Rejected[0].Rule != linkdoutput.RuleLevelsInvalid {
		t.Fatalf("rejected = %+v, want the refused event by its rule", partial.Rejected)
	}
	if len(partial.Withheld) != 1 || partial.Withheld[0] != sibling.EventID {
		t.Fatalf("withheld = %v, want the refused event's sibling", partial.Withheld)
	}
	if facts := report(); facts == nil || facts.Withheld != 1 || len(facts.Rejected) != 1 || facts.Published != 1 {
		t.Fatalf("write report = %+v, want 1 published, 1 rejected and 1 withheld", facts)
	}
}

// When nothing of the batch is left to write, the batch is refused as it
// always was, so a caller that knows only the whole refusal still reads it.
func TestABatchWithNothingLeftIsRefusedWhole(t *testing.T) {
	t.Parallel()
	producer := &countingProducer{}
	sink := producer.sink(t)
	refused, sibling := standardSeriesEvent(t, "series-a", "a1"), standardSeriesEvent(t, "series-a", "a2")
	refused.BusinessID = "biz-2"

	rejected := outputRejectionOf(t, sink.WriteBatch(context.Background(), []contract.TriggerEventV1{refused, sibling}))
	if rejected.EventID != refused.EventID || len(producer.keys) != 0 {
		t.Fatalf("rejection = %+v with %d written, want the refused event named and nothing written", rejected, len(producer.keys))
	}
}

// The sink's own refusals are per event too: an event this build has no
// format for is refused alone rather than failing the Slot.
func TestAnEventOfNoKnownFormatIsRefusedAlone(t *testing.T) {
	t.Parallel()
	producer := &countingProducer{}
	sink := producer.sink(t)
	good, unknown := standardSeriesEvent(t, "series-a", "a"), standardSeriesEvent(t, "series-b", "b")
	unknown.WireFormat = "unheard_of"

	partial := partialOf(t, sink.WriteBatch(context.Background(), []contract.TriggerEventV1{good, unknown}))
	if len(producer.keys) != 1 || len(partial.Rejected) != 1 || partial.Rejected[0].EventID != unknown.EventID ||
		partial.Rejected[0].Rule != observability.OutputRejectFormatUnsupported {
		t.Fatalf("written %v, rejected %+v; want the known event written and the unknown one refused by format_unsupported", producer.keys, partial.Rejected)
	}
}

// Every rule the standard converter and this sink name is one the metric is
// created for, so a refusal never folds to _other for want of a cell.
func TestEveryConverterRuleHasAMetricCell(t *testing.T) {
	for _, rule := range []string{
		linkdoutput.RuleIdentityMissing, linkdoutput.RuleActionUnknown, linkdoutput.RuleLevelsInvalid,
		linkdoutput.RuleTooManyLevels, linkdoutput.RuleBusinessIdentity, linkdoutput.RuleEncode,
		observability.OutputRejectEventInvalid, observability.OutputRejectFormatUnsupported,
		observability.OutputRejectLegacyContextMissing, observability.OutputRejectLegacyConversion,
		observability.OutputRejectLegacyStrategyInvalid, observability.OutputRejectLegacyOutputInvalid,
		observability.OutputRejectLegacyPayloadTooLarge,
	} {
		if observability.NormalizeOutputRejectRule(rule) != rule {
			t.Errorf("converter rule %q has no metric cell", rule)
		}
	}
}

// eachConverter answers ConvertEach from a function of one event: an error
// for the event is that event's refusal.
type eachConverter struct {
	convert func(contract.TriggerEventV1) (LegacyConvertedEvent, error)
	calls   int
}

func (converter *eachConverter) ConvertEach(_ context.Context, events []contract.TriggerEventV1) ([]LegacyConvertedEvent, []error, error) {
	converter.calls++
	converted := make([]LegacyConvertedEvent, len(events))
	failures := make([]error, len(events))
	for index, event := range events {
		converted[index], failures[index] = converter.convert(event)
	}
	return converted, failures, nil
}

func legacySeriesEvent(t *testing.T, series, event string) contract.TriggerEventV1 {
	t.Helper()
	decision := legacyEventForTest(t)
	restamp(t, &decision, series, event)
	return decision
}

// The compatible path isolates in one pass: the converter is asked once for
// the group, an event it refuses and an event whose message is too large are
// refused alone, and the rest is written. A refusal the strategy's own
// configuration causes is named apart from one alarmd causes.
func TestTheCompatiblePathRefusesEachEventAlone(t *testing.T) {
	t.Parallel()
	producer := &countingProducer{}
	sink := producer.sink(t)
	good, refused, large := legacySeriesEvent(t, "series-a", "a"), legacySeriesEvent(t, "series-b", "b"), legacySeriesEvent(t, "series-c", "c")
	misconfigured := legacySeriesEvent(t, "series-d", "d")
	converter := &eachConverter{convert: func(event contract.TriggerEventV1) (LegacyConvertedEvent, error) {
		payload := json.RawMessage(`{}`)
		switch event.EventID {
		case refused.EventID:
			return LegacyConvertedEvent{}, errors.New("legacy protocol carries anomaly points only")
		case misconfigured.EventID:
			return LegacyConvertedEvent{}, &legacyoutput.StrategyConfigError{Err: errors.New("invalid legacy item/severity")}
		case large.EventID:
			payload = json.RawMessage(`{"padding":"` + strings.Repeat("x", 2048) + `"}`)
		}
		return LegacyConvertedEvent{EventID: event.EventID, Payload: payload, DedupeMD5: fmt.Sprintf("%032x", len(event.EventID))}, nil
	}}
	if err := sink.ConfigureLegacyOutput(converter, "alarmd_python-events-1", 1024); err != nil {
		t.Fatal(err)
	}

	partial := partialOf(t, sink.WriteBatch(context.Background(), []contract.TriggerEventV1{good, refused, large, misconfigured}))
	if converter.calls != 1 {
		t.Fatalf("converter asked %d times, want once for the group: isolating must not convert again", converter.calls)
	}
	if len(producer.keys) != 1 {
		t.Fatalf("written %v, want only the good event", producer.keys)
	}
	rules := map[string]string{}
	for _, rejected := range partial.Rejected {
		rules[rejected.EventID] = rejected.Rule
	}
	if len(rules) != 3 || rules[refused.EventID] != observability.OutputRejectLegacyConversion || rules[large.EventID] != observability.OutputRejectLegacyPayloadTooLarge ||
		rules[misconfigured.EventID] != observability.OutputRejectLegacyStrategyInvalid {
		t.Fatalf("rejected = %+v, want the refused event by legacy_conversion_rejected, the large one by legacy_payload_too_large and the misconfigured one by legacy_strategy_invalid", partial.Rejected)
	}
}
