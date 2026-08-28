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
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	alarmdcoordinator "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/coordinator"
)

var _ alarmdcoordinator.ReceiptPublisher = (*ReceiptPublisher)(nil)

func TestReceiptPublisherTryEnqueueIsBoundedAndPublishesOfficialWire(t *testing.T) {
	t.Parallel()

	receipt := messageReceiptGolden(t)
	wantPayload, err := contract.EncodeMessageReceiptV1(&receipt)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan *sarama.ProducerMessage, 1)
	release := make(chan error, 1)
	producer := &fakeSyncProducer{send: func(message *sarama.ProducerMessage) (int32, int64, error) {
		started <- message
		return 0, 1, <-release
	}}
	publisher, err := newReceiptPublisher(
		"alarmd-message-receipt-shadow", producer, &fakeCloser{},
		ReceiptPublisherLimits{MaxQueuedMessages: 1, MaxQueuedBytes: len(wantPayload)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !publisher.TryEnqueue(&receipt) {
		t.Fatal("first TryEnqueue() = false")
	}
	message := <-started
	if publisher.TryEnqueue(&receipt) {
		t.Fatal("second TryEnqueue() exceeded the outstanding message budget")
	}
	value, err := message.Value.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if message.Topic != "alarmd-message-receipt-shadow" || message.Key != nil || !bytes.Equal(value, wantPayload) {
		t.Fatalf("message = topic:%q key:%#v value:%s", message.Topic, message.Key, value)
	}
	release <- nil
	result := publisher.Shutdown(context.Background())
	if result.Status != ReceiptDrainWithDrop || result.Enqueued != 1 || result.Acked != 1 ||
		result.PendingMessages != 0 || result.PendingBytes != 0 || result.Drops.QueueMessages != 1 {
		t.Fatalf("Shutdown() = %#v", result)
	}
}

func TestReceiptPublisherClassifiesEncodeAndByteBudgetDrops(t *testing.T) {
	t.Parallel()

	receipt := messageReceiptGolden(t)
	payload, err := contract.EncodeMessageReceiptV1(&receipt)
	if err != nil {
		t.Fatal(err)
	}
	invalid := receipt
	invalid.ReceiptID = "invalid"
	sends := 0
	producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
		sends++
		return 0, 0, nil
	}}
	validated := make(chan *contract.MessageReceiptV1, 1)
	publisher, err := newReceiptPublisherWithDiagnostics(
		"alarmd-message-receipt-shadow", producer, &fakeCloser{},
		ReceiptPublisherLimits{MaxQueuedMessages: 2, MaxQueuedBytes: len(payload) - 1},
		ReceiptPublisherDiagnostics{OnValidated: func(receipt *contract.MessageReceiptV1) { validated <- receipt }},
	)
	if err != nil {
		t.Fatal(err)
	}
	if publisher.TryEnqueue(&invalid) {
		t.Fatal("TryEnqueue() accepted an invalid Receipt")
	}
	if publisher.TryEnqueue(&receipt) {
		t.Fatal("TryEnqueue() accepted a Receipt above the byte budget")
	}
	if got := <-validated; got.ReceiptID != receipt.ReceiptID {
		t.Fatalf("validated Receipt = %s, want %s", got.ReceiptID, receipt.ReceiptID)
	}
	select {
	case got := <-validated:
		t.Fatalf("invalid Receipt reached validated diagnostics: %+v", got)
	default:
	}
	result := publisher.Shutdown(context.Background())
	if sends != 0 || result.Status != ReceiptDrainWithDrop || result.Enqueued != 0 || result.Acked != 0 ||
		result.Drops.EncodeFailed != 1 || result.Drops.QueueBytes != 1 {
		t.Fatalf("Shutdown() = %#v, sends=%d", result, sends)
	}
}

func TestReceiptPublisherClassifiesBrokerACKDropWithoutRetrying(t *testing.T) {
	t.Parallel()

	want := errors.New("broker ACK failed")
	sends := 0
	producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
		sends++
		return -1, -1, want
	}}
	receipt := messageReceiptGolden(t)
	payload, err := contract.EncodeMessageReceiptV1(&receipt)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := newReceiptPublisher(
		"alarmd-message-receipt-shadow", producer, &fakeCloser{},
		ReceiptPublisherLimits{MaxQueuedMessages: 1, MaxQueuedBytes: len(payload)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !publisher.TryEnqueue(&receipt) {
		t.Fatal("TryEnqueue() = false")
	}
	result := publisher.Shutdown(context.Background())
	if sends != 1 || result.Status != ReceiptDrainWithDrop || result.Enqueued != 1 || result.Acked != 0 ||
		result.Drops.BrokerACKFailed != 1 || !errors.Is(result.Err, want) {
		t.Fatalf("Shutdown() = %#v, sends=%d", result, sends)
	}
}

func TestReceiptPublisherReportsQueuedAndACKedLifecycle(t *testing.T) {
	t.Parallel()

	receipt := messageReceiptGolden(t)
	payload, err := contract.EncodeMessageReceiptV1(&receipt)
	if err != nil {
		t.Fatal(err)
	}
	type lifecycleEvidence struct {
		outcome string
		count   uint64
	}
	evidence := make(chan lifecycleEvidence, 3)
	publisher, err := newReceiptPublisherWithDiagnostics(
		"alarmd-message-receipt-shadow",
		&fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) { return 0, 0, nil }},
		&fakeCloser{}, ReceiptPublisherLimits{MaxQueuedMessages: 1, MaxQueuedBytes: len(payload)},
		ReceiptPublisherDiagnostics{
			OnValidated: func(*contract.MessageReceiptV1) { evidence <- lifecycleEvidence{outcome: "validated", count: 1} },
			OnQueued:    func(count uint64) { evidence <- lifecycleEvidence{outcome: "queued", count: count} },
			OnACKed:     func(count uint64) { evidence <- lifecycleEvidence{outcome: "acked", count: count} },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !publisher.TryEnqueue(&receipt) {
		t.Fatal("TryEnqueue() = false")
	}
	if result := publisher.Shutdown(context.Background()); result.Status != ReceiptDrainSuccess {
		t.Fatalf("Shutdown() = %+v, want SUCCESS", result)
	}
	got := map[string]uint64{}
	for range 3 {
		item := <-evidence
		got[item.outcome] += item.count
	}
	if got["validated"] != 1 || got["queued"] != 1 || got["acked"] != 1 {
		t.Fatalf("lifecycle evidence = %v, want validated=1 queued=1 acked=1", got)
	}
}

func TestReceiptPublisherEmitsEvidenceForEveryEnqueueDropClass(t *testing.T) {
	t.Parallel()

	receipt := messageReceiptGolden(t)
	payload, err := contract.EncodeMessageReceiptV1(&receipt)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan error, 1)
	evidence := make(chan ReceiptDropEvidence, 4)
	publisher, err := newReceiptPublisherWithDiagnostics(
		"alarmd-message-receipt-shadow",
		&fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
			close(started)
			return 0, 0, <-release
		}},
		&fakeCloser{},
		ReceiptPublisherLimits{MaxQueuedMessages: 1, MaxQueuedBytes: len(payload)},
		ReceiptPublisherDiagnostics{OnDrop: func(drop ReceiptDropEvidence) { evidence <- drop }},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !publisher.TryEnqueue(&receipt) {
		t.Fatal("first TryEnqueue() = false")
	}
	<-started
	if publisher.TryEnqueue(&receipt) {
		t.Fatal("TryEnqueue() exceeded message budget")
	}
	invalid := receipt
	invalid.ReceiptID = "invalid"
	if publisher.TryEnqueue(&invalid) {
		t.Fatal("TryEnqueue() accepted invalid Receipt")
	}
	release <- nil
	if result := publisher.Shutdown(context.Background()); result.Status != ReceiptDrainWithDrop {
		t.Fatalf("Shutdown() = %+v, want WITH_DROP", result)
	}
	if publisher.TryEnqueue(&receipt) {
		t.Fatal("TryEnqueue() accepted Receipt after shutdown")
	}
	for _, want := range []ReceiptDropKind{ReceiptDropQueueMessages, ReceiptDropEncodeFailed, ReceiptDropClosed} {
		expectReceiptDropEvidence(t, evidence, want, 1)
	}
	expectNoReceiptDropEvidence(t, evidence)

	byteEvidence := make(chan ReceiptDropEvidence, 1)
	bytePublisher, err := newReceiptPublisherWithDiagnostics(
		"alarmd-message-receipt-shadow", &fakeSyncProducer{}, &fakeCloser{},
		ReceiptPublisherLimits{MaxQueuedMessages: 1, MaxQueuedBytes: len(payload) - 1},
		ReceiptPublisherDiagnostics{OnDrop: func(drop ReceiptDropEvidence) { byteEvidence <- drop }},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytePublisher.TryEnqueue(&receipt) {
		t.Fatal("TryEnqueue() exceeded byte budget")
	}
	expectReceiptDropEvidence(t, byteEvidence, ReceiptDropQueueBytes, 1)
	_ = bytePublisher.Shutdown(context.Background())
	expectNoReceiptDropEvidence(t, byteEvidence)
}

func TestReceiptPublisherEmitsEvidenceForAsyncAndShutdownDrops(t *testing.T) {
	t.Parallel()

	receipt := messageReceiptGolden(t)
	payload, err := contract.EncodeMessageReceiptV1(&receipt)
	if err != nil {
		t.Fatal(err)
	}
	brokerEvidence := make(chan ReceiptDropEvidence, 1)
	brokerPublisher, err := newReceiptPublisherWithDiagnostics(
		"alarmd-message-receipt-shadow",
		&fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
			return -1, -1, errors.New("broker ACK failed")
		}},
		&fakeCloser{}, ReceiptPublisherLimits{MaxQueuedMessages: 1, MaxQueuedBytes: len(payload)},
		ReceiptPublisherDiagnostics{OnDrop: func(drop ReceiptDropEvidence) { brokerEvidence <- drop }},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !brokerPublisher.TryEnqueue(&receipt) {
		t.Fatal("TryEnqueue() = false")
	}
	_ = brokerPublisher.Shutdown(context.Background())
	expectReceiptDropEvidence(t, brokerEvidence, ReceiptDropBrokerACKFailed, 1)
	expectNoReceiptDropEvidence(t, brokerEvidence)

	sendStarted := make(chan struct{})
	clientClosed := make(chan struct{})
	timeoutEvidence := make(chan ReceiptDropEvidence, 1)
	timeoutPublisher, err := newReceiptPublisherWithDiagnostics(
		"alarmd-message-receipt-shadow",
		&fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
			close(sendStarted)
			<-clientClosed
			return -1, -1, sarama.ErrClosedClient
		}},
		&fakeCloser{close: func() error { close(clientClosed); return nil }},
		ReceiptPublisherLimits{MaxQueuedMessages: 1, MaxQueuedBytes: len(payload)},
		ReceiptPublisherDiagnostics{OnDrop: func(drop ReceiptDropEvidence) { timeoutEvidence <- drop }},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !timeoutPublisher.TryEnqueue(&receipt) {
		t.Fatal("TryEnqueue() = false")
	}
	<-sendStarted
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_ = timeoutPublisher.Shutdown(ctx)
	if got := <-timeoutEvidence; got.Kind != ReceiptDropShutdownTimeout || got.Count != 1 {
		t.Fatalf("timeout drop evidence = %+v", got)
	}
}

func TestReceiptPublisherShutdownTimeoutDropsPendingWithoutBlocking(t *testing.T) {
	t.Parallel()

	sendStarted := make(chan struct{})
	clientClosed := make(chan struct{})
	producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
		close(sendStarted)
		<-clientClosed
		return -1, -1, sarama.ErrClosedClient
	}}
	client := &fakeCloser{close: func() error {
		close(clientClosed)
		return nil
	}}
	receipt := messageReceiptGolden(t)
	payload, err := contract.EncodeMessageReceiptV1(&receipt)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := newReceiptPublisher(
		"alarmd-message-receipt-shadow", producer, client,
		ReceiptPublisherLimits{MaxQueuedMessages: 1, MaxQueuedBytes: len(payload)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !publisher.TryEnqueue(&receipt) {
		t.Fatal("TryEnqueue() = false")
	}
	select {
	case <-sendStarted:
	case <-time.After(time.Second):
		t.Fatal("Receipt send did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := publisher.Shutdown(ctx)
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Shutdown() exceeded deadline: %s", elapsed)
	}
	if result.Status != ReceiptDrainTimedOut || result.PendingMessages != 1 || result.PendingBytes != len(payload) ||
		result.Drops.ShutdownTimeout != 1 || !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() = %#v", result)
	}
}

func TestOpenReceiptPublisherPrimesOnlyReceiptTopic(t *testing.T) {
	broker := sarama.NewMockBroker(t, 1)
	defer broker.Close()

	coordinates := validDecisionSinkConfig()
	coordinates.Brokers = []string{broker.Addr()}
	coordinates.OutputTopic = "alarmd-message-receipt-shadow"
	coordinates.AllowedOutputTopics = []string{coordinates.OutputTopic}
	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"MetadataRequest": sarama.NewMockMetadataResponse(t).
			SetBroker(broker.Addr(), broker.BrokerID()).
			SetLeader(coordinates.OutputTopic, 0, broker.BrokerID()),
	})

	publisher, err := OpenReceiptPublisher(coordinates, ReceiptPublisherLimits{
		MaxQueuedMessages: 1,
		MaxQueuedBytes:    1024,
	})
	if err != nil {
		t.Fatalf("OpenReceiptPublisher() error = %v", err)
	}
	result := publisher.Shutdown(context.Background())
	if result.Status != ReceiptDrainSuccess {
		t.Fatalf("Shutdown() = %#v", result)
	}

	history := broker.History()
	if len(history) < 1 {
		t.Fatal("broker did not receive receipt topic metadata request")
	}
	metadata, ok := history[0].Request.(*sarama.MetadataRequest)
	if !ok {
		t.Fatalf("first broker request = %T, want *sarama.MetadataRequest", history[0].Request)
	}
	if len(metadata.Topics) != 1 || metadata.Topics[0] != coordinates.OutputTopic {
		t.Fatalf("metadata topics = %v, want only %q", metadata.Topics, coordinates.OutputTopic)
	}
}

func TestReceiptPublisherReportsOwnedResourceCloseFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("producer close failed")
	producer := &fakeSyncProducer{
		send:  func(*sarama.ProducerMessage) (int32, int64, error) { return 0, 0, nil },
		close: func() error { return want },
	}
	evidence := make(chan ReceiptDropEvidence, 1)
	publisher, err := newReceiptPublisherWithDiagnostics(
		"alarmd-message-receipt-shadow", producer, &fakeCloser{},
		ReceiptPublisherLimits{MaxQueuedMessages: 1, MaxQueuedBytes: 1024},
		ReceiptPublisherDiagnostics{OnDrop: func(drop ReceiptDropEvidence) { evidence <- drop }},
	)
	if err != nil {
		t.Fatal(err)
	}
	result := publisher.Shutdown(context.Background())
	if result.Status != ReceiptDrainFailed || result.Drops.CloseFailed != 1 || !errors.Is(result.Err, want) {
		t.Fatalf("Shutdown() = %#v", result)
	}
	if got := <-evidence; got.Kind != ReceiptDropCloseFailed || got.Count != 1 {
		t.Fatalf("close drop evidence = %+v", got)
	}
}

func TestNewReceiptPublisherRejectsInvalidDependenciesAndLimits(t *testing.T) {
	t.Parallel()

	valid := ReceiptPublisherLimits{MaxQueuedMessages: 1, MaxQueuedBytes: 1024}
	if _, err := newReceiptPublisher("alarmd-message-receipt-shadow", nil, &fakeCloser{}, valid); err == nil {
		t.Fatal("newReceiptPublisher() accepted a nil producer")
	}
	if _, err := newReceiptPublisher("alarmd-message-receipt-shadow", &fakeSyncProducer{}, nil, valid); err == nil {
		t.Fatal("newReceiptPublisher() accepted a nil client")
	}
	if _, err := newReceiptPublisher(
		"alarmd-message-receipt-shadow", &fakeSyncProducer{}, &fakeCloser{},
		ReceiptPublisherLimits{MaxQueuedMessages: 0, MaxQueuedBytes: 1024},
	); err == nil {
		t.Fatal("newReceiptPublisher() accepted a zero message budget")
	}
	if _, err := newReceiptPublisher(
		"alarmd-message-receipt-shadow", &fakeSyncProducer{}, &fakeCloser{},
		ReceiptPublisherLimits{MaxQueuedMessages: 1, MaxQueuedBytes: 0},
	); err == nil {
		t.Fatal("newReceiptPublisher() accepted a zero byte budget")
	}
}

func TestReceiptPublisherRejectsNilReceipt(t *testing.T) {
	t.Parallel()

	publisher, err := newReceiptPublisher(
		"alarmd-message-receipt-shadow", &fakeSyncProducer{}, &fakeCloser{},
		ReceiptPublisherLimits{MaxQueuedMessages: 1, MaxQueuedBytes: 1024},
	)
	if err != nil {
		t.Fatal(err)
	}
	if publisher.TryEnqueue(nil) {
		t.Fatal("TryEnqueue() accepted a nil Receipt")
	}
	result := publisher.Shutdown(context.Background())
	if result.Status != ReceiptDrainWithDrop || result.Drops.EncodeFailed != 1 {
		t.Fatalf("Shutdown() = %#v", result)
	}
}

func messageReceiptGolden(t testing.TB) contract.MessageReceiptV1 {
	t.Helper()
	payload, err := os.ReadFile("../contract/testdata/go-v2/message_receipt_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := contract.DecodeMessageReceiptV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	built, err := contract.BuildMessageReceiptV1(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	return *built
}

func expectReceiptDropEvidence(
	t testing.TB,
	evidence <-chan ReceiptDropEvidence,
	wantKind ReceiptDropKind,
	wantCount uint64,
) {
	t.Helper()
	select {
	case got := <-evidence:
		if got.Kind != wantKind || got.Count != wantCount {
			t.Fatalf("drop evidence = %+v, want %s/%d", got, wantKind, wantCount)
		}
	case <-time.After(time.Second):
		t.Fatalf("missing drop evidence %s/%d", wantKind, wantCount)
	}
}

func expectNoReceiptDropEvidence(t testing.TB, evidence <-chan ReceiptDropEvidence) {
	t.Helper()
	select {
	case got := <-evidence:
		t.Fatalf("unexpected duplicate drop evidence: %+v", got)
	default:
	}
}
