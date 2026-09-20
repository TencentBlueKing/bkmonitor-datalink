// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package rabbitmq

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	"linkd/internal/consume"
)

func TestSessionReceiveAndSingleAck(t *testing.T) {
	t.Parallel()

	acknowledger := &fakeAcknowledger{}
	deliveryChannel := make(chan amqp.Delivery, 2)
	deliveryChannel <- rabbitDelivery(acknowledger, 1, "message-1")
	deliveryChannel <- rabbitDelivery(acknowledger, 2, "message-2")
	session := newSession(testRabbitConfig(), &fakeConnection{}, &fakeChannel{}, deliveryChannel, func() {})
	deliveries, err := session.Receive(context.Background(), consume.ReceiveLimits{MaxMessages: 2, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if len(deliveries) != 2 || deliveries[0].Message.TenantID != "tenant-a" {
		t.Fatalf("Receive() deliveries = %#v", deliveries)
	}
	if err := session.Confirm(context.Background(), []consume.Receipt{deliveries[1].Receipt}); err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if got := acknowledger.calls(); !slices.Equal(got, []ackCall{{tag: 2, multiple: false}}) {
		t.Fatalf("Ack() calls = %#v", got)
	}
}

func TestSessionBuffersDeliveryBeyondCurrentByteBudget(t *testing.T) {
	t.Parallel()

	acknowledger := &fakeAcknowledger{}
	deliveryChannel := make(chan amqp.Delivery, 2)
	deliveryChannel <- rabbitDelivery(acknowledger, 1, "message-1")
	deliveryChannel <- rabbitDelivery(acknowledger, 2, "message-2")
	session := newSession(testRabbitConfig(), &fakeConnection{}, &fakeChannel{}, deliveryChannel, func() {})

	first, err := session.Receive(context.Background(), consume.ReceiveLimits{MaxMessages: 2, MaxBytes: len("payload")})
	if err != nil {
		t.Fatalf("Receive(first) error = %v", err)
	}
	second, err := session.Receive(context.Background(), consume.ReceiveLimits{MaxMessages: 2, MaxBytes: len("payload")})
	if err != nil {
		t.Fatalf("Receive(second) error = %v", err)
	}
	if len(first) != 1 || len(second) != 1 || first[0].Message.ID != "message-1" || second[0].Message.ID != "message-2" {
		t.Fatalf("buffered receive = first %#v, second %#v", first, second)
	}
}

func TestSessionRequiresStableMessageID(t *testing.T) {
	t.Parallel()

	deliveryChannel := make(chan amqp.Delivery, 1)
	deliveryChannel <- rabbitDelivery(&fakeAcknowledger{}, 1, "")
	session := newSession(testRabbitConfig(), &fakeConnection{}, &fakeChannel{}, deliveryChannel, func() {})
	_, err := session.Receive(context.Background(), consume.ReceiveLimits{MaxMessages: 1, MaxBytes: 1024})
	if err == nil {
		t.Fatal("Receive() error = nil, want missing message id")
	}
}

func testRabbitConfig() Config {
	return (Config{URL: "amqp://rabbitmq/", Queue: "raw-events"}).WithDefaults()
}

func rabbitDelivery(acknowledger amqp.Acknowledger, tag uint64, messageID string) amqp.Delivery {
	return amqp.Delivery{
		Acknowledger: acknowledger,
		Headers:      amqp.Table{"bk_tenant_id": "tenant-a", "order_key": "order-a"},
		MessageId:    messageID,
		DeliveryTag:  tag,
		Body:         []byte("payload"),
	}
}

type ackCall struct {
	tag      uint64
	multiple bool
}

type fakeAcknowledger struct {
	mu       sync.Mutex
	ackCalls []ackCall
}

func (a *fakeAcknowledger) Ack(tag uint64, multiple bool) error {
	a.mu.Lock()
	a.ackCalls = append(a.ackCalls, ackCall{tag: tag, multiple: multiple})
	a.mu.Unlock()
	return nil
}

func (a *fakeAcknowledger) Nack(uint64, bool, bool) error { return errors.New("unexpected nack") }

func (a *fakeAcknowledger) Reject(uint64, bool) error { return errors.New("unexpected reject") }

func (a *fakeAcknowledger) calls() []ackCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]ackCall(nil), a.ackCalls...)
}

type fakeChannel struct{}

func (fakeChannel) Qos(int, int, bool) error { return nil }

func (fakeChannel) ConsumeWithContext(context.Context, string, string, bool, bool, bool, bool, amqp.Table) (<-chan amqp.Delivery, error) {
	return nil, errors.New("not implemented")
}

func (fakeChannel) Close() error { return nil }

type fakeConnection struct{}

func (fakeConnection) Close() error { return nil }

var _ amqp.Acknowledger = (*fakeAcknowledger)(nil)

var _ amqpChannel = (*fakeChannel)(nil)

var _ amqpConnection = (*fakeConnection)(nil)
