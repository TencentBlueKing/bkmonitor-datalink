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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
)

func TestOpenDecisionSinkPrimesOnlyOutputTopicWithoutTransactionalHandshake(t *testing.T) {
	broker := sarama.NewMockBroker(t, 1)
	defer broker.Close()

	coordinates := validDecisionSinkConfig()
	coordinates.Brokers = []string{broker.Addr()}
	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"MetadataRequest": sarama.NewMockMetadataResponse(t).
			SetBroker(broker.Addr(), broker.BrokerID()).
			SetLeader(coordinates.OutputTopic, 0, broker.BrokerID()),
	})

	sink, err := OpenDecisionSink(coordinates)
	if err != nil {
		t.Fatalf("OpenDecisionSink() error = %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	history := broker.History()
	if len(history) < 1 {
		t.Fatal("broker did not receive output topic metadata request")
	}
	metadata, ok := history[0].Request.(*sarama.MetadataRequest)
	if !ok {
		t.Fatalf("first broker request = %T, want *sarama.MetadataRequest", history[0].Request)
	}
	if len(metadata.Topics) != 1 || metadata.Topics[0] != coordinates.OutputTopic {
		t.Fatalf("metadata topics = %v, want only %q", metadata.Topics, coordinates.OutputTopic)
	}
	for _, request := range history {
		if _, ok := request.Request.(*sarama.InitProducerIDRequest); ok {
			t.Fatal("Shadow producer must not require InitProducerID")
		}
	}
}

func TestDecisionSinkWritesOneOfficiallyEncodedRecordAfterAcknowledgement(t *testing.T) {
	t.Parallel()

	batch := validDecisionBatch(t, 1)
	wantPayload, err := contract.EncodeTriggerDecisionBatch(batch)
	if err != nil {
		t.Fatalf("EncodeTriggerDecisionBatch() error = %v", err)
	}
	wantKey, err := batch.PartitionKey()
	if err != nil {
		t.Fatalf("PartitionKey() error = %v", err)
	}
	started := make(chan *sarama.ProducerMessage, 1)
	release := make(chan error, 1)
	producer := &fakeSyncProducer{send: func(message *sarama.ProducerMessage) (int32, int64, error) {
		started <- message
		return 2, 17, <-release
	}}
	sink := newDecisionSinkForTest(t, producer, &fakeCloser{})

	done := make(chan error, 1)
	go func() { done <- sink.WriteBatch(context.Background(), batch) }()
	message := <-started
	select {
	case err := <-done:
		t.Fatalf("WriteBatch() returned before broker acknowledgement: %v", err)
	default:
	}
	key, err := message.Key.Encode()
	if err != nil {
		t.Fatalf("message key Encode() error = %v", err)
	}
	value, err := message.Value.Encode()
	if err != nil {
		t.Fatalf("message value Encode() error = %v", err)
	}
	if message.Topic != validDecisionSinkConfig().OutputTopic || !bytes.Equal(key, wantKey) || !bytes.Equal(value, wantPayload) {
		t.Fatalf("message = topic=%q key=%x value=%s", message.Topic, key, value)
	}
	release <- nil
	if err := <-done; err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
}

func TestDecisionSinkRejectsInvalidBatchBeforeProducer(t *testing.T) {
	t.Parallel()

	var sends atomic.Int32
	producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
		sends.Add(1)
		return 0, 0, nil
	}}
	sink := newDecisionSinkForTest(t, producer, &fakeCloser{})

	invalid := validDecisionBatch(t, 1)
	invalid.Decisions[0].DecisionID = strings.Repeat("f", 64)
	if err := sink.WriteBatch(context.Background(), invalid); err == nil {
		t.Fatal("WriteBatch() accepted invalid batch")
	}
	oversized := validDecisionBatch(t, 1)
	oversized.BatchID += strings.Repeat("x", contract.MaxTriggerDecisionBytesV1)
	if err := sink.WriteBatch(context.Background(), oversized); err == nil {
		t.Fatal("WriteBatch() accepted oversized batch")
	}
	if sends.Load() != 0 {
		t.Fatalf("producer sends = %d, want 0", sends.Load())
	}
}

func TestDecisionSinkCancellationUsesBrokerResultBoundary(t *testing.T) {
	t.Parallel()

	batch := validDecisionBatch(t, 1)
	preCanceled, cancel := context.WithCancel(context.Background())
	cancel()
	var sends atomic.Int32
	producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
		sends.Add(1)
		return 0, 0, nil
	}}
	sink := newDecisionSinkForTest(t, producer, &fakeCloser{})
	if err := sink.WriteBatch(preCanceled, batch); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled WriteBatch() error = %v", err)
	}
	if sends.Load() != 0 {
		t.Fatalf("pre-canceled sends = %d, want 0", sends.Load())
	}

	started := make(chan struct{})
	release := make(chan error, 1)
	producer.send = func(*sarama.ProducerMessage) (int32, int64, error) {
		close(started)
		return 0, 0, <-release
	}
	duringSend, cancelDuringSend := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sink.WriteBatch(duringSend, batch) }()
	<-started
	cancelDuringSend()
	select {
	case err := <-done:
		t.Fatalf("WriteBatch() returned while broker result was unknown: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	release <- nil
	if err := <-done; err != nil {
		t.Fatalf("acknowledged WriteBatch() error = %v", err)
	}

	failed := errors.New("delivery failed")
	failedStarted := make(chan struct{})
	failedRelease := make(chan struct{})
	producer.send = func(*sarama.ProducerMessage) (int32, int64, error) {
		close(failedStarted)
		<-failedRelease
		return -1, -1, failed
	}
	duringFailure, cancelDuringFailure := context.WithCancel(context.Background())
	failedDone := make(chan error, 1)
	go func() { failedDone <- sink.WriteBatch(duringFailure, batch) }()
	<-failedStarted
	cancelDuringFailure()
	close(failedRelease)
	if err := <-failedDone; !errors.Is(err, context.Canceled) || !errors.Is(err, failed) {
		t.Fatalf("canceled failed WriteBatch() error = %v", err)
	}
}

func TestDecisionSinkFailureIsReplaySafeButNotExactlyOnce(t *testing.T) {
	t.Parallel()

	batch := validDecisionBatch(t, 1)
	uncertain := errors.New("producer response unavailable")
	var mu sync.Mutex
	var messages []*sarama.ProducerMessage
	producer := &fakeSyncProducer{send: func(message *sarama.ProducerMessage) (int32, int64, error) {
		mu.Lock()
		messages = append(messages, message)
		attempt := len(messages)
		mu.Unlock()
		if attempt == 1 {
			return -1, -1, uncertain
		}
		return 1, 2, nil
	}}
	sink := newDecisionSinkForTest(t, producer, &fakeCloser{})
	if err := sink.WriteBatch(context.Background(), batch); !errors.Is(err, uncertain) {
		t.Fatalf("first WriteBatch() error = %v", err)
	}
	if err := sink.WriteBatch(context.Background(), batch); err != nil {
		t.Fatalf("replayed WriteBatch() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 2 || !producerMessagesEqual(t, messages[0], messages[1]) {
		t.Fatal("replay did not preserve topic, key and payload")
	}
}

func TestDecisionSinkCloseWaitsForInflightAndClosesOwnedResourcesOnce(t *testing.T) {
	t.Parallel()

	batch := validDecisionBatch(t, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	var closeOrderMu sync.Mutex
	var closeOrder []string
	producer := &fakeSyncProducer{
		send: func(*sarama.ProducerMessage) (int32, int64, error) {
			close(started)
			<-release
			return 0, 0, nil
		},
		close: func() error {
			closeOrderMu.Lock()
			closeOrder = append(closeOrder, "producer")
			closeOrderMu.Unlock()
			return nil
		},
	}
	client := &fakeCloser{close: func() error {
		closeOrderMu.Lock()
		closeOrder = append(closeOrder, "client")
		closeOrderMu.Unlock()
		return nil
	}}
	sink := newDecisionSinkForTest(t, producer, client)
	writeDone := make(chan error, 1)
	go func() { writeDone <- sink.WriteBatch(context.Background(), batch) }()
	<-started
	closeDone := make(chan error, 2)
	go func() { closeDone <- sink.Close() }()
	waitForDecisionSinkClosing(t, sink)
	if err := sink.WriteBatch(context.Background(), batch); !errors.Is(err, ErrDecisionSinkClosed) {
		t.Fatalf("WriteBatch() after closing error = %v", err)
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Close() returned before inflight send: %v", err)
	default:
	}
	close(release)
	if err := <-writeDone; err != nil {
		t.Fatalf("inflight WriteBatch() error = %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	go func() { closeDone <- sink.Close() }()
	if err := <-closeDone; err != nil {
		t.Fatalf("repeated Close() error = %v", err)
	}
	closeOrderMu.Lock()
	defer closeOrderMu.Unlock()
	if strings.Join(closeOrder, ",") != "producer,client" {
		t.Fatalf("close order = %v, want producer then client once", closeOrder)
	}
}

func TestDecisionSinkShutdownDeadlineStartsClientCloseAttempt(t *testing.T) {
	t.Parallel()

	batch := validDecisionBatch(t, 1)
	sendStarted := make(chan struct{})
	clientClosed := make(chan struct{})
	var closeOrderMu sync.Mutex
	closeOrder := make([]string, 0, 2)
	producer := &fakeSyncProducer{
		send: func(*sarama.ProducerMessage) (int32, int64, error) {
			close(sendStarted)
			<-clientClosed
			return -1, -1, sarama.ErrClosedClient
		},
		close: func() error {
			closeOrderMu.Lock()
			closeOrder = append(closeOrder, "producer")
			closeOrderMu.Unlock()
			return nil
		},
	}
	client := &fakeCloser{close: func() error {
		closeOrderMu.Lock()
		closeOrder = append(closeOrder, "client")
		closeOrderMu.Unlock()
		close(clientClosed)
		return nil
	}}
	sink := newDecisionSinkForTest(t, producer, client)
	writeDone := make(chan error, 1)
	go func() { writeDone <- sink.WriteBatch(context.Background(), batch) }()
	select {
	case <-sendStarted:
	case <-time.After(time.Second):
		t.Fatal("producer send did not start")
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelShutdown()
	started := time.Now()
	err := sink.Shutdown(shutdownContext)
	if !errors.Is(err, ErrDecisionSinkShutdownTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v, want shutdown timeout and context deadline", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Shutdown() exceeded its context deadline: %s", elapsed)
	}
	select {
	case err := <-writeDone:
		if !errors.Is(err, sarama.ErrClosedClient) {
			t.Fatalf("inflight WriteBatch() error = %v, want closed client", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("test producer did not observe the client-close attempt")
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() after forced shutdown error = %v", err)
	}

	closeOrderMu.Lock()
	defer closeOrderMu.Unlock()
	if strings.Join(closeOrder, ",") != "client,producer" {
		t.Fatalf("forced close order = %v, want client then producer", closeOrder)
	}
}

func TestDecisionSinkShutdownDeadlineBoundsBlockedClientClose(t *testing.T) {
	t.Parallel()

	clientCloseStarted := make(chan struct{})
	releaseClientClose := make(chan struct{})
	producer := &fakeSyncProducer{
		send: func(*sarama.ProducerMessage) (int32, int64, error) { return 0, 0, nil },
	}
	client := &fakeCloser{close: func() error {
		close(clientCloseStarted)
		<-releaseClientClose
		return nil
	}}
	sink := newDecisionSinkForTest(t, producer, client)
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelShutdown()
	started := time.Now()
	err := sink.Shutdown(shutdownContext)
	if !errors.Is(err, ErrDecisionSinkShutdownTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v, want shutdown timeout and context deadline", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Shutdown() waited for blocked client close: %s", elapsed)
	}
	select {
	case <-clientCloseStarted:
	case <-time.After(time.Second):
		t.Fatal("client close did not start")
	}
	if err := sink.WriteBatch(context.Background(), validDecisionBatch(t, 1)); !errors.Is(err, ErrDecisionSinkClosed) {
		t.Fatalf("WriteBatch() after shutdown error = %v, want closed sink", err)
	}
	close(releaseClientClose)
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() after blocked client release error = %v", err)
	}
}

func TestDecisionSinkForcedShutdownDoesNotHideProducerCloseFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("producer close failed")
	sendStarted := make(chan struct{})
	clientClosed := make(chan struct{})
	producer := &fakeSyncProducer{
		send: func(*sarama.ProducerMessage) (int32, int64, error) {
			close(sendStarted)
			<-clientClosed
			return -1, -1, sarama.ErrClosedClient
		},
		close: func() error { return errors.Join(sarama.ErrClosedClient, want) },
	}
	client := &fakeCloser{close: func() error {
		close(clientClosed)
		return nil
	}}
	sink := newDecisionSinkForTest(t, producer, client)
	writeDone := make(chan error, 1)
	go func() { writeDone <- sink.WriteBatch(context.Background(), validDecisionBatch(t, 1)) }()
	select {
	case <-sendStarted:
	case <-time.After(time.Second):
		t.Fatal("producer send did not start")
	}
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelShutdown()
	if err := sink.Shutdown(shutdownContext); !errors.Is(err, ErrDecisionSinkShutdownTimeout) {
		t.Fatalf("Shutdown() error = %v, want shutdown timeout", err)
	}
	select {
	case <-writeDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("test producer did not observe the client-close attempt")
	}
	if err := sink.Close(); !errors.Is(err, want) {
		t.Fatalf("Close() error = %v, want producer close failure %v", err, want)
	}
}

func TestDecisionSinkPreCanceledShutdownStillClosesResources(t *testing.T) {
	t.Parallel()

	producer := &fakeSyncProducer{
		send: func(*sarama.ProducerMessage) (int32, int64, error) { return 0, 0, nil },
	}
	client := &fakeCloser{}
	sink := newDecisionSinkForTest(t, producer, client)
	shutdownContext, cancelShutdown := context.WithCancel(context.Background())
	cancelShutdown()
	if err := sink.Shutdown(shutdownContext); !errors.Is(err, ErrDecisionSinkShutdownTimeout) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown() error = %v, want shutdown timeout and context cancellation", err)
	}
	if err := sink.WriteBatch(context.Background(), validDecisionBatch(t, 1)); !errors.Is(err, ErrDecisionSinkClosed) {
		t.Fatalf("WriteBatch() after pre-canceled shutdown error = %v, want closed sink", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() after pre-canceled shutdown error = %v", err)
	}
	producer.mu.Lock()
	producerCloseCount := producer.closeCount
	producer.mu.Unlock()
	client.mu.Lock()
	clientCloseCount := client.closeCount
	client.mu.Unlock()
	if producerCloseCount != 1 || clientCloseCount != 1 {
		t.Fatalf("close counts = producer:%d client:%d, want 1/1", producerCloseCount, clientCloseCount)
	}
}

func TestDecisionSinkCompletedShutdownWinsOverCanceledContext(t *testing.T) {
	t.Parallel()

	producer := &fakeSyncProducer{
		send: func(*sarama.ProducerMessage) (int32, int64, error) { return 0, 0, nil },
	}
	client := &fakeCloser{}
	sink := newDecisionSinkForTest(t, producer, client)
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	shutdownContext, cancelShutdown := context.WithCancel(context.Background())
	cancelShutdown()
	if err := sink.Shutdown(shutdownContext); err != nil {
		t.Fatalf("Shutdown() after completed close error = %v, want completed result", err)
	}
	if sink.forced.Load() {
		t.Fatal("completed shutdown started the forced close path")
	}
}

func TestDecisionSinkSupportsConcurrentClaims(t *testing.T) {
	t.Parallel()

	const claims = 16
	var active atomic.Int32
	var maximum atomic.Int32
	release := make(chan struct{})
	producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
		current := active.Add(1)
		for current > maximum.Load() && !maximum.CompareAndSwap(maximum.Load(), current) {
		}
		<-release
		active.Add(-1)
		return 0, 0, nil
	}}
	sink := newDecisionSinkForTest(t, producer, &fakeCloser{})
	var workers sync.WaitGroup
	workers.Add(claims)
	for index := 0; index < claims; index++ {
		go func() {
			defer workers.Done()
			if err := sink.WriteBatch(context.Background(), validDecisionBatch(t, 1)); err != nil {
				t.Errorf("WriteBatch() error = %v", err)
			}
		}()
	}
	deadline := time.After(time.Second)
	for maximum.Load() < 2 {
		select {
		case <-deadline:
			t.Fatal("producer writes were serialized")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(release)
	workers.Wait()
}

func TestDecisionSinkKeepsMaximumDecisionBatchInOneRecord(t *testing.T) {
	t.Parallel()

	batch := validDecisionBatch(t, contract.MaxTriggerDecisionItemsV1)
	var sends atomic.Int32
	producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
		sends.Add(1)
		return 0, 0, nil
	}}
	sink := newDecisionSinkForTest(t, producer, &fakeCloser{})
	if err := sink.WriteBatch(context.Background(), batch); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	if sends.Load() != 1 {
		t.Fatalf("producer sends = %d, want one record", sends.Load())
	}
}

func TestDecisionSinkConcurrentCloseSharesOneResult(t *testing.T) {
	t.Parallel()

	producerErr := errors.New("producer close")
	clientErr := errors.New("client close")
	producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
		return 0, 0, nil
	}, close: func() error { return producerErr }}
	client := &fakeCloser{close: func() error { return clientErr }}
	sink := newDecisionSinkForTest(t, producer, client)

	const callers = 8
	errorsSeen := make(chan error, callers)
	var workers sync.WaitGroup
	workers.Add(callers)
	for index := 0; index < callers; index++ {
		go func() {
			defer workers.Done()
			errorsSeen <- sink.Close()
		}()
	}
	workers.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if !errors.Is(err, producerErr) || !errors.Is(err, clientErr) {
			t.Fatalf("Close() error = %v", err)
		}
	}
	producer.mu.Lock()
	producerCloseCount := producer.closeCount
	producer.mu.Unlock()
	client.mu.Lock()
	clientCloseCount := client.closeCount
	client.mu.Unlock()
	if producerCloseCount != 1 || clientCloseCount != 1 {
		t.Fatalf("close counts = producer:%d client:%d, want 1/1", producerCloseCount, clientCloseCount)
	}
}

type fakeSyncProducer struct {
	mu         sync.Mutex
	send       func(*sarama.ProducerMessage) (int32, int64, error)
	close      func() error
	closeCount int
}

func (p *fakeSyncProducer) SendMessage(message *sarama.ProducerMessage) (int32, int64, error) {
	p.mu.Lock()
	send := p.send
	p.mu.Unlock()
	return send(message)
}

func (p *fakeSyncProducer) Close() error {
	p.mu.Lock()
	p.closeCount++
	closeFn := p.close
	p.mu.Unlock()
	if closeFn != nil {
		return closeFn()
	}
	return nil
}

type fakeCloser struct {
	mu         sync.Mutex
	close      func() error
	closeCount int
}

func (c *fakeCloser) Close() error {
	c.mu.Lock()
	c.closeCount++
	closeFn := c.close
	c.mu.Unlock()
	if closeFn != nil {
		return closeFn()
	}
	return nil
}

func newDecisionSinkForTest(t *testing.T, producer syncMessageProducer, client closeableClient) *DecisionSink {
	t.Helper()
	sink, err := newDecisionSink(validDecisionSinkConfig().OutputTopic, producer, client)
	if err != nil {
		t.Fatalf("newDecisionSink() error = %v", err)
	}
	return sink
}

func waitForDecisionSinkClosing(t *testing.T, sink *DecisionSink) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		sink.mu.Lock()
		closing := sink.closing
		sink.mu.Unlock()
		if closing {
			return
		}
		select {
		case <-deadline:
			t.Fatal("sink did not enter closing state")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func producerMessagesEqual(t *testing.T, first, second *sarama.ProducerMessage) bool {
	t.Helper()
	firstKey, firstKeyErr := first.Key.Encode()
	secondKey, secondKeyErr := second.Key.Encode()
	firstValue, firstValueErr := first.Value.Encode()
	secondValue, secondValueErr := second.Value.Encode()
	if firstKeyErr != nil || secondKeyErr != nil || firstValueErr != nil || secondValueErr != nil {
		t.Fatal("failed to encode captured producer message")
	}
	return first.Topic == second.Topic && bytes.Equal(firstKey, secondKey) && bytes.Equal(firstValue, secondValue)
}

func validDecisionBatch(t *testing.T, count int) *contract.TriggerDecisionBatch {
	t.Helper()
	strategyRef := contract.StrategyRef{
		StrategyID:    "101",
		ItemID:        "202",
		Generation:    "generation-1",
		ContentSHA256: strings.Repeat("a", 64),
	}
	decisions := make([]contract.TriggerDecision, 0, count)
	for index := 0; index < count; index++ {
		recordID := strings.Repeat("b", 32) + "." + strconv.Itoa(index+1)
		inputID, err := contract.DeriveInputID(contract.InputIdentity{
			TenantID:              "tenant-1",
			Purpose:               contract.PurposeDetect,
			StrategyID:            strategyRef.StrategyID,
			ItemID:                strategyRef.ItemID,
			StrategyContentSHA256: strategyRef.ContentSHA256,
			RecordID:              recordID,
		})
		if err != nil {
			t.Fatalf("DeriveInputID() error = %v", err)
		}
		decisionID, err := contract.DeriveTriggerDecisionID(inputID)
		if err != nil {
			t.Fatalf("DeriveTriggerDecisionID() error = %v", err)
		}
		decisions = append(decisions, contract.TriggerDecision{
			DecisionID:        decisionID,
			InputID:           inputID,
			RecordID:          recordID,
			Outcome:           contract.DecisionOutcomeNoTrigger,
			ReasonCode:        contract.DecisionReasonInputNormal,
			AnomalyTimestamps: []int64{},
		})
	}
	batch := &contract.TriggerDecisionBatch{
		Schema:               contract.Schema{Name: "trigger-decision-batch", Major: 1, Minor: 0},
		RequiredFeatures:     []string{},
		PartitionHashVersion: contract.PartitionHashVersionV1,
		BatchID:              "batch-1",
		TenantID:             "tenant-1",
		Purpose:              contract.PurposeDetect,
		StrategyRef:          strategyRef,
		DecisionAlgorithm:    contract.DecisionAlgorithmV1,
		Decisions:            decisions,
	}
	if err := batch.Validate(); err != nil {
		t.Fatalf("valid decision batch: %v", err)
	}
	return batch
}
