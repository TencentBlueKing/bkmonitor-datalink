// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package kafka

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// partlyRefusingProducer answers a batch the way SendMessages does: the
// messages it lists failed, every other one landed (sarama sync_producer.go,
// SendMessages returns only the failures). It refuses the messages at the
// given positions with the cause.
type partlyRefusingProducer struct {
	refuse map[int]bool
	cause  error
	sent   int
}

func (producer *partlyRefusingProducer) SendMessage(*sarama.ProducerMessage) (int32, int64, error) {
	return 0, 0, errors.New("single sends are not used")
}

func (producer *partlyRefusingProducer) SendMessages(messages []*sarama.ProducerMessage) error {
	var failures sarama.ProducerErrors
	for position, message := range messages {
		if producer.refuse[position] {
			failures = append(failures, &sarama.ProducerError{Msg: message, Err: producer.cause})
			continue
		}
		producer.sent++
	}
	if len(failures) == 0 {
		return nil
	}
	return failures
}

func (*partlyRefusingProducer) Close() error { return nil }

// eventOfSeries is the golden standard event for another series: the same
// strategy, a record of its own.
func eventOfSeries(t *testing.T, series string) contract.TriggerEventV1 {
	t.Helper()
	return eventOfSeriesRecord(t, series, series)
}

// eventOfSeriesRecord is one record of a series, so a batch can carry two
// records of one series.
func eventOfSeriesRecord(t *testing.T, series, recordName string) contract.TriggerEventV1 {
	t.Helper()
	legacy := legacyTriggerEventGolden(t)
	digest := sha256.Sum256([]byte(series))
	legacy.RecordRef.DimensionIdentityDigest = hex.EncodeToString(digest[:])
	record := sha256.Sum256([]byte("record " + recordName))
	legacy.RecordRef.RecordID = hex.EncodeToString(record[:])
	event, err := contract.BuildTriggerEventV1(contract.TriggerEventBuildInputV1{EventKind: legacy.EventKind, TenantID: legacy.TenantID, BusinessID: legacy.BusinessID, PlanRef: legacy.PlanRef, RecordRef: legacy.RecordRef, Observed: legacy.Observed, LevelResults: legacy.LevelResults, EvaluationTime: legacy.EvaluationTime, DetectPlanFingerprint: legacy.DetectPlanFingerprint, TriggerStateFingerprint: legacy.TriggerStateFingerprint, ExecutionID: legacy.Trace.ExecutionID, MaxEvidenceBytes: 64 << 10, DedupeMD5: "0260bae09d2ae3f75683bd06a76e9479", StrategyRef: &contract.StrategySnapshotRef{TenantID: legacy.TenantID, BusinessID: 2, StrategyID: 1001, Revision: 7}})
	if err != nil {
		t.Fatal(err)
	}
	return *event
}

// A batch the client or a broker refused only some messages of is reported
// as partly written: the refused events named, the rest written. Before, a
// batch whose failures were all of the client's kind - ErrMessageSizeTooLarge,
// which a broker's MESSAGE_TOO_LARGE also comes back as - was reported as
// refused whole, "nothing was sent", while the other messages had landed:
// their Plans' State did not move, the open-alert copy never heard of the
// alerts they opened, and the next round sent them again.
func TestABatchPartlyRefusedAfterSendingIsReportedPartlyWritten(t *testing.T) {
	events := []contract.TriggerEventV1{
		eventOfSeries(t, "series-a"), eventOfSeries(t, "series-b"), eventOfSeries(t, "series-c"),
	}
	producer := &partlyRefusingProducer{refuse: map[int]bool{1: true}, cause: sarama.ErrMessageSizeTooLarge}
	sink := newTriggerEventSinkForTest(t, producer, &fakeCloser{})
	ctx, report := observability.ContextWithOutputWriteReport(context.Background())
	err := sink.WriteBatch(ctx, events)
	var partial *OutputPartiallyRejectedError
	if !errors.As(err, &partial) {
		t.Fatalf("WriteBatch() = %v (%T), want the batch reported partly written", err, err)
	}
	// The Slot's report names the refusal by its rule and strategy, and
	// nothing withheld: the reader counts it under the producer's refusal.
	if facts := report(); facts == nil || len(facts.Rejected) != 1 || facts.Rejected[0].Rule != "producer_refused" ||
		facts.Rejected[0].StrategyID != events[1].PlanRef.StrategyID || facts.Withheld != 0 {
		t.Fatalf("output write report = %+v, want one producer_refused event of strategy %s and none withheld", facts, events[1].PlanRef.StrategyID)
	}
	if producer.sent != 2 {
		t.Fatalf("fixture: %d messages landed, want 2", producer.sent)
	}
	if got := partial.OutputNotWrittenEventIDs(); len(got) != 1 || got[0] != events[1].EventID {
		t.Fatalf("not written = %v, want only the refused event %s", got, events[1].EventID)
	}
	if partial.Rejected[0].Rule != observability.OutputRejectProducerRefused || partial.OutputRejectionReason() != contract.ReasonOutputClientRejected {
		t.Fatalf("rule = %q reason = %q, want %q under %s", partial.Rejected[0].Rule, partial.OutputRejectionReason(),
			observability.OutputRejectProducerRefused, contract.ReasonOutputClientRejected)
	}
}

// An event of the same series as a refused one is reported not written
// beside it, though it landed: a series moves its State whole, and an event
// written while its sibling was refused would leave the alert at the consumer
// with no State behind it. It is sent again with its series, under the same
// event id, which the consumer deduplicates.
func TestAnEventOfARefusedSeriesIsReportedNotWrittenThoughItLanded(t *testing.T) {
	events := []contract.TriggerEventV1{
		eventOfSeries(t, "series-a"), eventOfSeriesRecord(t, "series-b", "first"), eventOfSeriesRecord(t, "series-b", "second"),
	}
	producer := &partlyRefusingProducer{refuse: map[int]bool{1: true}, cause: sarama.ErrMessageSizeTooLarge}
	sink := newTriggerEventSinkForTest(t, producer, &fakeCloser{})
	ctx, report := observability.ContextWithOutputWriteReport(context.Background())
	err := sink.WriteBatch(ctx, events)
	var partial *OutputPartiallyRejectedError
	if !errors.As(err, &partial) {
		t.Fatalf("WriteBatch() = %v, want the batch reported partly written", err)
	}
	if facts := report(); facts == nil || len(facts.Rejected) != 1 || facts.Withheld != 1 {
		t.Fatalf("output write report = %+v, want the refused event and its sibling withheld", facts)
	}
	got := map[string]bool{}
	for _, id := range partial.OutputNotWrittenEventIDs() {
		got[id] = true
	}
	if len(got) != 2 || !got[events[1].EventID] || !got[events[2].EventID] || got[events[0].EventID] {
		t.Fatalf("not written = %v, want both events of series-b and not series-a's", partial.OutputNotWrittenEventIDs())
	}
}

// A batch refused whole is still refused whole, and a broker failure among
// the refusals keeps the batch a dependency failure to send again.
func TestAWholeRefusalAndABrokerFailureAreAsBefore(t *testing.T) {
	events := []contract.TriggerEventV1{eventOfSeries(t, "series-a"), eventOfSeries(t, "series-b")}
	whole := newTriggerEventSinkForTest(t, &partlyRefusingProducer{refuse: map[int]bool{0: true, 1: true}, cause: sarama.ErrMessageSizeTooLarge}, &fakeCloser{})
	var rejected *OutputRejectedError
	if err := whole.WriteBatch(context.Background(), events); !errors.As(err, &rejected) {
		t.Fatalf("a batch refused whole = %v, want refused whole", err)
	}
	broker := newTriggerEventSinkForTest(t, &partlyRefusingProducer{refuse: map[int]bool{1: true}, cause: sarama.ErrNotLeaderForPartition}, &fakeCloser{})
	if err := broker.WriteBatch(context.Background(), events); !isRetryableDependency(err) {
		t.Fatalf("a broker failure = %v, want a dependency failure sent again", err)
	}
}

// A failure the producer names that is not a message of the batch, or no
// failure at all, cannot be placed on an event, so the batch is not read as
// partly written and the caller refuses it whole, as before the batch could
// be refused in part. Sarama lists the batch's own messages, so this is the
// direction a broken answer falls, not a path a deployment takes.
func TestAFailureThatNamesNoMessageOfTheBatchLeavesItRefusedWhole(t *testing.T) {
	events := []contract.TriggerEventV1{eventOfSeries(t, "series-a"), eventOfSeries(t, "series-b"), eventOfSeries(t, "series-c")}
	messages := []*sarama.ProducerMessage{{Topic: "alarmd-trigger-event"}, {Topic: "alarmd-trigger-event"}, {Topic: "alarmd-trigger-event"}}
	eventOf := []int{0, 1, 2}
	formats := []string{"standard", "standard", "standard"}
	refused := make([]refusal, len(events))
	foreign := &sarama.ProducerError{Msg: &sarama.ProducerMessage{Topic: "alarmd-trigger-event"}, Err: sarama.ErrMessageSizeTooLarge}
	ours := &sarama.ProducerError{Msg: messages[0], Err: sarama.ErrMessageSizeTooLarge}
	for name, failures := range map[string]sarama.ProducerErrors{
		"a message of another batch":                    {foreign},
		"a message of another batch beside one of ours": {ours, foreign},
		"no failure": {nil},
	} {
		if got := partlyRefused(context.Background(), failures, messages, eventOf, events, formats, refused); got != nil {
			t.Fatalf("%s: partlyRefused() = %v, want nil so the batch is refused whole", name, got)
		}
	}
}

// A batch with an event the sink refused before sending and another the
// producer refused after it is reported with both: the Slot's report is the
// batch's, and the refusal found later does not replace the one found
// earlier. Both events are not written; the third landed.
func TestARefusalBeforeSendingAndOneAfterAreBothReported(t *testing.T) {
	before, landed, after := standardSeriesEvent(t, "series-b", "b"), standardSeriesEvent(t, "series-a", "a"), standardSeriesEvent(t, "series-c", "c")
	before.BusinessID = "biz-2"
	// The producer sees the two messages left once the refused event is
	// taken out, and refuses the second of them: series-c's.
	producer := &partlyRefusingProducer{refuse: map[int]bool{1: true}, cause: sarama.ErrMessageSizeTooLarge}
	sink := newTriggerEventSinkForTest(t, producer, &fakeCloser{})
	ctx, report := observability.ContextWithOutputWriteReport(context.Background())
	err := sink.WriteBatch(ctx, []contract.TriggerEventV1{before, landed, after})
	var partial *OutputPartiallyRejectedError
	if !errors.As(err, &partial) {
		t.Fatalf("WriteBatch() = %v (%T), want the batch reported partly written", err, err)
	}
	if producer.sent != 1 {
		t.Fatalf("fixture: %d messages landed, want 1", producer.sent)
	}
	notWritten := map[string]bool{}
	for _, id := range partial.OutputNotWrittenEventIDs() {
		notWritten[id] = true
	}
	if len(notWritten) != 2 || !notWritten[before.EventID] || !notWritten[after.EventID] {
		t.Fatalf("not written = %v, want the event refused before sending and the one refused after", partial.OutputNotWrittenEventIDs())
	}
	facts := report()
	rules := map[string]string{}
	for _, rejected := range facts.Rejected {
		rules[rejected.Rule] = rejected.StrategyID
	}
	if facts == nil || len(facts.Rejected) != 2 || rules["producer_refused"] == "" || rules["standard_business_identity"] == "" || facts.Withheld != 0 {
		t.Fatalf("output write report = %+v, want both refusals by their rules and none withheld", facts)
	}
}
