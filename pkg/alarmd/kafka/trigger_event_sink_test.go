// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
)

func TestTriggerEventSinkPublishesRawEventAfterBrokerACK(t *testing.T) {
	t.Parallel()

	event := triggerEventGolden(t)
	converter, _ := linkdoutput.NewConverter(nil)
	wantEvent, err := converter.Convert(&event)
	wantPayload := wantEvent.Payload
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan *sarama.ProducerMessage, 1)
	release := make(chan error, 1)
	producer := &fakeSyncProducer{send: func(message *sarama.ProducerMessage) (int32, int64, error) {
		started <- message
		return 2, 17, <-release
	}}
	sink, err := newTriggerEventSink("alarmd-trigger-event-shadow", producer, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- sink.WriteBatch(context.Background(), []contract.TriggerEventV1{event}) }()
	message := <-started
	select {
	case err := <-done:
		t.Fatalf("WriteBatch() returned before broker ACK: %v", err)
	default:
	}
	value, err := message.Value.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if message.Topic != "alarmd-trigger-event-shadow" || message.Key == nil || !bytes.Equal(value, wantPayload) {
		t.Fatalf("message = topic:%q key:%#v value:%s", message.Topic, message.Key, value)
	}
	release <- nil
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTriggerEventSinkPublishesSnapshotProtocol(t *testing.T) {
	for _, kind := range []string{contract.TriggerEventAbnormal, contract.TriggerEventRecovery} {
		t.Run(kind, func(t *testing.T) { testTriggerEventSinkPublishesSnapshotProtocol(t, kind) })
	}
}

func testTriggerEventSinkPublishesSnapshotProtocol(t *testing.T, kind string) {
	legacy := triggerEventGolden(t)
	if kind == contract.TriggerEventRecovery {
		legacy.EventKind = kind
		for i := range legacy.LevelResults {
			level := &legacy.LevelResults[i]
			level.Result = contract.LevelResultRecovery
			level.DetectEvidence.DetectionResult = "NORMAL"
			level.DetectEvidence.NormalizedValue = json.RawMessage(`0`)
			level.DecisionWindow.Trigger.ObservedAnomalies = 0
			level.DecisionWindow.Recovery.ObservedConsecutiveMisses = level.DecisionWindow.Recovery.RequiredConsecutiveWindows
		}
		legacy.Observed.Values["value"] = json.RawMessage(`0`)
	}
	event, err := contract.BuildTriggerEventV1(contract.TriggerEventBuildInputV1{
		EventKind: legacy.EventKind, TenantID: legacy.TenantID, BusinessID: legacy.BusinessID,
		PlanRef: legacy.PlanRef, RecordRef: legacy.RecordRef, Observed: legacy.Observed,
		LevelResults: legacy.LevelResults, EvaluationTime: legacy.EvaluationTime,
		DetectPlanFingerprint: legacy.DetectPlanFingerprint, TriggerStateFingerprint: legacy.TriggerStateFingerprint,
		ExecutionID: legacy.Trace.ExecutionID, MaxEvidenceBytes: 64 << 10,
		StrategyRef: &contract.StrategySnapshotRef{TenantID: "default", BusinessID: 2, StrategyID: 1001, Revision: 7},
		DedupeMD5:   "0260bae09d2ae3f75683bd06a76e9479",
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload []byte
	sends := 0
	producer := &fakeSyncProducer{send: func(message *sarama.ProducerMessage) (int32, int64, error) {
		sends++
		key, keyErr := message.Key.Encode()
		if keyErr != nil || string(key) != event.DedupeMD5 || len(key) != 32 {
			t.Fatalf("single-message key=%q error=%v", key, keyErr)
		}
		var err error
		payload, err = message.Value.Encode()
		return 0, 1, err
	}}
	sink, err := newTriggerEventSink("alarmd-native-results", producer, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{*event}); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Labels struct {
			StrategyID int64 `json:"strategy_id"`
			Revision   int64 `json:"strategy_version"`
		} `json:"labels"`
		Evaluations []struct {
			Action string `json:"action"`
		} `json:"evaluations"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	wantAction := "triggered"
	if kind == contract.TriggerEventRecovery {
		wantAction = "resolved"
	}
	if decoded.Labels.StrategyID != event.StrategyRef.StrategyID || decoded.Labels.Revision != event.StrategyRef.Revision || decoded.Evaluations[0].Action != wantAction {
		t.Fatalf("RawEvent lost snapshot/action: %s", payload)
	}

	firstPayload := append([]byte(nil), payload...)
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{*event}); err != nil {
		t.Fatal(err)
	}
	if sends != 2 || !bytes.Equal(payload, firstPayload) {
		t.Fatal("retry changed Kafka payload")
	}
	event.StrategyRef.Revision++
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{*event}); err == nil {
		t.Fatal("sink accepted tampered snapshot reference")
	}
	if sends != 2 {
		t.Fatal("sink published tampered snapshot reference")
	}
}

func TestTriggerEventSinkPublishesMultiEventBatchWithOneProducerBatchACK(t *testing.T) {
	t.Parallel()

	events := []contract.TriggerEventV1{triggerEventGolden(t), triggerEventGolden(t), triggerEventGolden(t)}
	producer := &batchAwareSyncProducer{}
	sink, err := newTriggerEventSink("alarmd-trigger-event-shadow", producer, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}

	if err := sink.WriteBatch(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	if producer.batchCalls != 1 || producer.singleCalls != 0 || producer.batchSize != len(events) {
		t.Fatalf(
			"producer calls = batch:%d single:%d batch_size:%d, want 1/0/%d",
			producer.batchCalls, producer.singleCalls, producer.batchSize, len(events),
		)
	}
}

type batchAwareSyncProducer struct {
	batchCalls  int
	singleCalls int
	batchSize   int
	messages    []*sarama.ProducerMessage
}

func (producer *batchAwareSyncProducer) SendMessage(message *sarama.ProducerMessage) (int32, int64, error) {
	producer.singleCalls++
	producer.messages = append(producer.messages, message)
	return 0, 0, nil
}

func (producer *batchAwareSyncProducer) SendMessages(messages []*sarama.ProducerMessage) error {
	producer.batchCalls++
	producer.batchSize = len(messages)
	producer.messages = messages
	return nil
}

func (*batchAwareSyncProducer) Close() error { return nil }

func TestTriggerEventSinkValidatesWholeBatchBeforePublishing(t *testing.T) {
	t.Parallel()

	valid := triggerEventGolden(t)
	invalid := valid
	invalid.EventID = "invalid"
	sends := 0
	producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
		sends++
		return 0, 0, nil
	}}
	sink, err := newTriggerEventSink("alarmd-trigger-event-shadow", producer, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{valid, invalid}); err == nil {
		t.Fatal("WriteBatch() accepted an invalid event")
	}
	if sends != 0 {
		t.Fatalf("producer sends = %d, want 0 before whole-batch validation", sends)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTriggerEventSinkReturnsBrokerFailureForReplay(t *testing.T) {
	t.Parallel()

	want := errors.New("broker ACK failed")
	producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
		return -1, -1, want
	}}
	sink, err := newTriggerEventSink("alarmd-trigger-event-shadow", producer, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	err = sink.WriteBatch(context.Background(), []contract.TriggerEventV1{triggerEventGolden(t)})
	if !errors.Is(err, want) {
		t.Fatalf("WriteBatch() error = %v, want broker error", err)
	}
	var retryable interface{ RetryableOutputDependency() }
	if !errors.As(err, &retryable) {
		t.Fatalf("WriteBatch() error = %T, want retryable output dependency marker", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTriggerEventSinkDoesNotMarkEncodingFailureRetryable(t *testing.T) {
	t.Parallel()

	event := triggerEventGolden(t)
	event.EventID = "invalid"
	sink, err := newTriggerEventSink("alarmd-trigger-event-shadow", &fakeSyncProducer{}, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	err = sink.WriteBatch(context.Background(), []contract.TriggerEventV1{event})
	if err == nil {
		t.Fatal("WriteBatch() accepted invalid event")
	}
	var retryable interface{ RetryableOutputDependency() }
	if errors.As(err, &retryable) {
		t.Fatalf("WriteBatch() encoding error = %T, must not be retryable dependency", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTriggerEventSinkDoesNotMarkLocalLifecycleFailureRetryable(t *testing.T) {
	t.Parallel()

	sink, err := newTriggerEventSink("alarmd-trigger-event-shadow", &fakeSyncProducer{}, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	err = sink.WriteBatch(context.Background(), []contract.TriggerEventV1{triggerEventGolden(t)})
	if !errors.Is(err, ErrDecisionSinkClosed) {
		t.Fatalf("WriteBatch() error = %v, want closed sink", err)
	}
	var retryable interface{ RetryableOutputDependency() }
	if errors.As(err, &retryable) {
		t.Fatalf("WriteBatch() lifecycle error = %T, must not be retryable dependency", err)
	}
}

func triggerEventGolden(t testing.TB) contract.TriggerEventV1 {
	t.Helper()
	legacy := legacyTriggerEventGolden(t)
	event, err := contract.BuildTriggerEventV1(contract.TriggerEventBuildInputV1{EventKind: legacy.EventKind, TenantID: legacy.TenantID, BusinessID: legacy.BusinessID, PlanRef: legacy.PlanRef, RecordRef: legacy.RecordRef, Observed: legacy.Observed, LevelResults: legacy.LevelResults, EvaluationTime: legacy.EvaluationTime, DetectPlanFingerprint: legacy.DetectPlanFingerprint, TriggerStateFingerprint: legacy.TriggerStateFingerprint, ExecutionID: legacy.Trace.ExecutionID, MaxEvidenceBytes: 64 << 10, DedupeMD5: "0260bae09d2ae3f75683bd06a76e9479", StrategyRef: &contract.StrategySnapshotRef{TenantID: legacy.TenantID, BusinessID: 2, StrategyID: 1001, Revision: 7}})
	if err != nil {
		t.Fatal(err)
	}
	return *event
}

func legacyTriggerEventGolden(t testing.TB) contract.TriggerEventV1 {
	t.Helper()
	payload, err := os.ReadFile("../contract/testdata/go-v2/trigger_event_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	event, err := contract.DecodeTriggerEventV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	return *event
}

// The switch has to reach the wire, not just the configuration: a Plan built as
// native publishes the standard raw event, keyed by the alert identity so the
// consumer's partitions hold one alert's history together.
func TestTriggerEventSinkPublishesTheRawEventWhenThePlanSaysSo(t *testing.T) {
	t.Parallel()

	event := triggerEventGolden(t)
	event.WireFormat = contract.WireFormatStandardRawEvent
	event.StrategyRef = &contract.StrategySnapshotRef{
		TenantID: event.TenantID, BusinessID: 2, StrategyID: 1001, Revision: 7,
	}
	event.DedupeMD5 = strings.Repeat("b", 32)
	event.BusinessID = "2"
	event.Schema.Minor = 2

	sent := make(chan *sarama.ProducerMessage, 1)
	producer := &fakeSyncProducer{send: func(message *sarama.ProducerMessage) (int32, int64, error) {
		sent <- message
		return 0, 1, nil
	}}
	sink, err := newTriggerEventSink("alarmd-trigger-event-shadow", producer, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{event}); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	message := <-sent
	if message.Topic != "alarmd-trigger-event-shadow" {
		t.Fatalf("topic = %q, want the native topic", message.Topic)
	}
	key, err := message.Key.Encode()
	if err != nil || string(key) != event.DedupeMD5 {
		t.Fatalf("key = %s (err %v), want the alert identity", key, err)
	}
	value, err := message.Value.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var written map[string]json.RawMessage
	if err := json.Unmarshal(value, &written); err != nil {
		t.Fatalf("written message is not the raw event: %v", err)
	}
	// The consumer's own field names. Naming them here rather than checking
	// "some JSON came out" is the point: the sink's job is to put this
	// shape on that topic, and a message the consumer does not read is
	// indistinguishable from one it does until somebody looks at linkd.
	for _, field := range []string{"alert_id", "evaluations", "dimensions", "occurred_at", "produced_at", "labels", "extra_data"} {
		if _, present := written[field]; !present {
			t.Fatalf("written message has no %s: %s", field, value)
		}
	}
	// And it is not the decision event: that one has no alert_id at all.
	if _, decision := written["event_kind"]; decision {
		t.Fatalf("the decision event was written instead of the raw event: %s", value)
	}
	// The tenant rides in the record header the consumer's adapter reads,
	// and agrees with the payload the consumer's cleaner reads.
	tenant := ""
	for _, header := range message.Headers {
		if string(header.Key) == "bk_tenant_id" {
			tenant = string(header.Value)
		}
	}
	if tenant != event.TenantID || string(written["bk_tenant_id"]) != `"`+event.TenantID+`"` {
		t.Fatalf("tenant header = %q, payload = %s, want both to carry %q", tenant, written["bk_tenant_id"], event.TenantID)
	}
}

// Old frozen Plans no longer leak the internal envelope onto the output topic.
func TestAPlanWithNoFormatPublishesRawEvent(t *testing.T) {
	t.Parallel()

	event := triggerEventGolden(t)
	converter, _ := linkdoutput.NewConverter(nil)
	wantEvent, err := converter.Convert(&event)
	wantPayload := wantEvent.Payload
	if err != nil {
		t.Fatal(err)
	}
	sent := make(chan *sarama.ProducerMessage, 1)
	producer := &fakeSyncProducer{send: func(message *sarama.ProducerMessage) (int32, int64, error) {
		sent <- message
		return 0, 1, nil
	}}
	sink, err := newTriggerEventSink("alarmd-trigger-event-shadow", producer, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{event}); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	value, err := (<-sent).Value.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(value, wantPayload) {
		t.Fatalf("value = %s, want the standard RawEvent", value)
	}
}

func TestTriggerEventSinkOnlyPublishesTwoFormatsInMixedBatch(t *testing.T) {
	for _, format := range []string{"", contract.WireFormatTriggerEvent, contract.WireFormatStandardRawEvent} {
		t.Run(format, func(t *testing.T) {
			native := triggerEventGolden(t)
			native.WireFormat = format
			// Even with a revision, a frozen Python context must keep the legacy route.
			legacy := triggerEventGolden(t)
			legacy.WireFormat = contract.WireFormatPythonCompatible
			legacy.LegacyOutput = legacyEventForTest(t).LegacyOutput

			producer := &batchAwareSyncProducer{}
			sink, err := newTriggerEventSink("native", producer, &fakeCloser{})
			if err != nil {
				t.Fatal(err)
			}
			defer sink.Close()
			if err := sink.ConfigureLegacyOutput(legacyConverterFunc(func(_ context.Context, events []contract.TriggerEventV1) ([]LegacyConvertedEvent, error) {
				if len(events) != 1 {
					t.Fatalf("legacy batch = %d, want 1", len(events))
				}
				result := make([]LegacyConvertedEvent, len(events))
				for i, event := range events {
					result[i] = LegacyConvertedEvent{EventID: event.EventID, DedupeMD5: event.DedupeMD5, Payload: []byte(`{"status":"ABNORMAL"}`)}
				}
				return result, nil
			}), "alarmd_legacy", 64<<10); err != nil {
				t.Fatal(err)
			}
			if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{native, legacy}); err != nil {
				t.Fatal(err)
			}
			if producer.batchCalls != 1 || len(producer.messages) != 2 {
				t.Fatal("mixed batch did not share broker ACK")
			}
			for i, message := range producer.messages {
				payload, _ := message.Value.Encode()
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(payload, &fields); err != nil {
					t.Fatal(err)
				}
				if fields["event_kind"] != nil || fields["schema"] != nil {
					t.Fatalf("internal TriggerEvent leaked: %s", payload)
				}
				if i == 0 {
					if message.Topic != "native" || fields["alert_id"] == nil || fields["labels"] == nil || len(message.Headers) != 1 {
						t.Fatalf("not RawEvent: %+v %s", message, payload)
					}
				} else if message.Topic != "alarmd_legacy" || fields["status"] == nil {
					t.Fatalf("not Python compatible: %s", payload)
				}
			}
		})
	}
}

func TestTriggerEventSinkRefusesInvalidRoutingWithoutPublishingPartialBatch(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*contract.TriggerEventV1)
	}{
		{"unknown format", func(e *contract.TriggerEventV1) { e.WireFormat = "unknown" }},
		{"missing series identity", func(e *contract.TriggerEventV1) { e.DedupeMD5 = ""; e.Schema.Minor = 1 }},
		{"python without frozen context", func(e *contract.TriggerEventV1) { e.WireFormat = contract.WireFormatPythonCompatible }},
		{"invalid snapshot identity", func(e *contract.TriggerEventV1) { e.StrategyRef.Revision = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			good := triggerEventGolden(t)
			bad := triggerEventGolden(t)
			test.change(&bad)
			producer := &batchAwareSyncProducer{}
			sink, err := newTriggerEventSink("native", producer, &fakeCloser{})
			if err != nil {
				t.Fatal(err)
			}
			defer sink.Close()
			if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{good, bad}); err == nil {
				t.Fatal("invalid routing accepted")
			}
			if producer.batchCalls != 0 || producer.singleCalls != 0 {
				t.Fatal("partial batch published")
			}
		})
	}
}
