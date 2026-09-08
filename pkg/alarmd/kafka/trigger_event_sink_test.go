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
	"testing"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestTriggerEventSinkPublishesOfficialWireWithEmptyKeyAfterBrokerACK(t *testing.T) {
	t.Parallel()

	event := triggerEventGolden(t)
	wantPayload, err := contract.EncodeTriggerEventV1(&event)
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
	if message.Topic != "alarmd-trigger-event-shadow" || message.Key != nil || !bytes.Equal(value, wantPayload) {
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
	decoded, err := contract.DecodeTriggerEventV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.EventKind != kind || decoded.Schema.Minor != 2 || decoded.DedupeMD5 != event.DedupeMD5 || decoded.StrategyRef == nil || *decoded.StrategyRef != *event.StrategyRef {
		t.Fatalf("Kafka payload lost snapshot reference: %s", payload)
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
}

func (producer *batchAwareSyncProducer) SendMessage(*sarama.ProducerMessage) (int32, int64, error) {
	producer.singleCalls++
	return 0, 0, nil
}

func (producer *batchAwareSyncProducer) SendMessages(messages []*sarama.ProducerMessage) error {
	producer.batchCalls++
	producer.batchSize = len(messages)
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
