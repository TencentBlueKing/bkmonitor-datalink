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
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type ReceiptPublisherLimits struct {
	MaxQueuedMessages int
	MaxQueuedBytes    int
}

func (limits ReceiptPublisherLimits) validate() error {
	if limits.MaxQueuedMessages <= 0 || limits.MaxQueuedBytes <= 0 {
		return errors.New("kafka receipt publisher: message and byte budgets must be positive")
	}
	return nil
}

type ReceiptDrainStatus string

const (
	ReceiptDrainSuccess  ReceiptDrainStatus = "SUCCESS"
	ReceiptDrainWithDrop ReceiptDrainStatus = "WITH_DROP"
	ReceiptDrainTimedOut ReceiptDrainStatus = "TIMED_OUT"
	ReceiptDrainFailed   ReceiptDrainStatus = "FAILED"
)

type ReceiptDropCounts struct {
	EncodeFailed    uint64
	QueueMessages   uint64
	QueueBytes      uint64
	BrokerACKFailed uint64
	Closed          uint64
	ShutdownTimeout uint64
	CloseFailed     uint64
}

type ReceiptDrainResult struct {
	Status          ReceiptDrainStatus
	Enqueued        uint64
	Acked           uint64
	PendingMessages int
	PendingBytes    int
	Drops           ReceiptDropCounts
	Err             error
}

type queuedReceipt struct {
	payload []byte
}

// ReceiptPublisher is a best-effort audit output. TryEnqueue never waits for a
// broker ACK and its queue bounds include both buffered and in-flight payloads.
type ReceiptPublisher struct {
	core   *DecisionSink
	limits ReceiptPublisherLimits
	queue  chan queuedReceipt

	mu              sync.Mutex
	accepting       bool
	pendingMessages int
	pendingBytes    int
	enqueued        uint64
	acked           uint64
	drops           ReceiptDropCounts
	firstErr        error
	abandoned       bool

	workerDone chan struct{}
	shutdown   sync.Once
	result     ReceiptDrainResult
}

func OpenReceiptPublisher(
	coordinates DecisionSinkConfig,
	limits ReceiptPublisherLimits,
) (*ReceiptPublisher, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	config, err := NewDecisionProducerConfig(coordinates)
	if err != nil {
		return nil, err
	}
	client, err := sarama.NewClient(coordinates.Brokers, config)
	if err != nil {
		return nil, fmt.Errorf("kafka receipt publisher: open client: %w", err)
	}
	producer, err := newSyncProducerForOutput(client, coordinates.OutputTopic)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("kafka receipt publisher: open producer: %w", err), client.Close())
	}
	publisher, err := newReceiptPublisher(coordinates.OutputTopic, producer, client, limits)
	if err != nil {
		return nil, errors.Join(err, producer.Close(), client.Close())
	}
	return publisher, nil
}

func newReceiptPublisher(
	outputTopic string,
	producer syncMessageProducer,
	client closeableClient,
	limits ReceiptPublisherLimits,
) (*ReceiptPublisher, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	core, err := newDecisionSink(outputTopic, producer, client)
	if err != nil {
		return nil, err
	}
	publisher := &ReceiptPublisher{
		core: core, limits: limits, queue: make(chan queuedReceipt, limits.MaxQueuedMessages),
		accepting: true, workerDone: make(chan struct{}),
	}
	go publisher.run()
	return publisher, nil
}

func (publisher *ReceiptPublisher) TryEnqueue(receipt *contract.MessageReceiptV1) bool {
	if publisher == nil || publisher.core == nil {
		return false
	}
	publisher.mu.Lock()
	if !publisher.accepting {
		publisher.drops.Closed++
		publisher.mu.Unlock()
		return false
	}
	publisher.mu.Unlock()

	payload, err := contract.EncodeMessageReceiptV1(receipt)
	if err != nil {
		publisher.mu.Lock()
		publisher.drops.EncodeFailed++
		publisher.recordError(err)
		publisher.mu.Unlock()
		return false
	}

	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if !publisher.accepting {
		publisher.drops.Closed++
		return false
	}
	if publisher.pendingMessages >= publisher.limits.MaxQueuedMessages {
		publisher.drops.QueueMessages++
		return false
	}
	if len(payload) > publisher.limits.MaxQueuedBytes-publisher.pendingBytes {
		publisher.drops.QueueBytes++
		return false
	}
	publisher.pendingMessages++
	publisher.pendingBytes += len(payload)
	publisher.enqueued++
	publisher.queue <- queuedReceipt{payload: payload}
	return true
}

func (publisher *ReceiptPublisher) Shutdown(ctx context.Context) ReceiptDrainResult {
	if publisher == nil {
		return ReceiptDrainResult{Status: ReceiptDrainSuccess}
	}
	if ctx == nil {
		return ReceiptDrainResult{Status: ReceiptDrainFailed, Drops: ReceiptDropCounts{CloseFailed: 1}, Err: errors.New("kafka receipt publisher: context is required")}
	}
	publisher.shutdown.Do(func() {
		publisher.result = publisher.shutdownWithin(ctx)
	})
	return publisher.result
}

func (publisher *ReceiptPublisher) run() {
	defer close(publisher.workerDone)
	for item := range publisher.queue {
		err := publisher.core.writeMessages(context.Background(), []*sarama.ProducerMessage{{
			Topic: publisher.core.outputTopic, Key: nil, Value: sarama.ByteEncoder(item.payload),
		}})
		publisher.finish(item, err)
	}
}

func (publisher *ReceiptPublisher) finish(item queuedReceipt, err error) {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	publisher.pendingMessages--
	publisher.pendingBytes -= len(item.payload)
	if publisher.abandoned {
		return
	}
	if err != nil {
		publisher.drops.BrokerACKFailed++
		publisher.recordError(err)
		return
	}
	publisher.acked++
}

func (publisher *ReceiptPublisher) shutdownWithin(ctx context.Context) ReceiptDrainResult {
	publisher.mu.Lock()
	publisher.accepting = false
	close(publisher.queue)
	publisher.mu.Unlock()

	select {
	case <-publisher.workerDone:
		closeErr := publisher.core.Shutdown(ctx)
		publisher.mu.Lock()
		if closeErr != nil {
			publisher.drops.CloseFailed++
			publisher.recordError(closeErr)
		}
		result := publisher.snapshotLocked()
		publisher.mu.Unlock()
		if closeErr != nil {
			result.Status = ReceiptDrainFailed
		} else if hasReceiptDrops(result.Drops) {
			result.Status = ReceiptDrainWithDrop
		} else {
			result.Status = ReceiptDrainSuccess
		}
		return result
	case <-ctx.Done():
		publisher.mu.Lock()
		publisher.abandoned = true
		timedOut := publisher.pendingMessages
		if timedOut == 0 {
			timedOut = 1
		}
		publisher.drops.ShutdownTimeout += uint64(timedOut)
		publisher.recordError(ctx.Err())
		result := publisher.snapshotLocked()
		publisher.mu.Unlock()
		closeErr := publisher.core.Shutdown(ctx)
		result.Status = ReceiptDrainTimedOut
		result.Err = errors.Join(result.Err, closeErr)
		return result
	}
}

func (publisher *ReceiptPublisher) snapshotLocked() ReceiptDrainResult {
	return ReceiptDrainResult{
		Enqueued: publisher.enqueued, Acked: publisher.acked,
		PendingMessages: publisher.pendingMessages, PendingBytes: publisher.pendingBytes,
		Drops: publisher.drops, Err: publisher.firstErr,
	}
}

func (publisher *ReceiptPublisher) recordError(err error) {
	if publisher.firstErr == nil && err != nil {
		publisher.firstErr = fmt.Errorf("kafka receipt publisher: %w", err)
	}
}

func hasReceiptDrops(drops ReceiptDropCounts) bool {
	return drops != (ReceiptDropCounts{})
}
