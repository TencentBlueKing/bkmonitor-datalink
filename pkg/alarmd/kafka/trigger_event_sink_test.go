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
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{triggerEventGolden(t)}); !errors.Is(err, want) {
		t.Fatalf("WriteBatch() error = %v, want broker error", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
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
