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

type ReceiptDropKind string

const (
	ReceiptDropEncodeFailed         ReceiptDropKind = "encode_failed"
	ReceiptDropQueueMessages        ReceiptDropKind = "queue_messages"
	ReceiptDropQueueBytes           ReceiptDropKind = "queue_bytes"
	ReceiptDropBrokerACKFailed      ReceiptDropKind = "broker_ack_failed"
	ReceiptDropClosed               ReceiptDropKind = "closed"
	ReceiptDropShutdownTimeout      ReceiptDropKind = "shutdown_timeout"
	ReceiptDropCloseFailed          ReceiptDropKind = "close_failed"
	ReceiptDropPublisherUnavailable ReceiptDropKind = "publisher_unavailable"
)

type ReceiptDropEvidence struct {
	Kind  ReceiptDropKind
	Count uint64
}

type ReceiptPublisherDiagnostics struct {
	OnValidated func(*contract.MessageReceiptV1)
	OnQueued    func(uint64)
	OnACKed     func(uint64)
	OnDrop      func(ReceiptDropEvidence)
}

// ObserveValidated reports one Receipt only after its official encoder or
// validator has completed contract validation successfully.
func (diagnostics ReceiptPublisherDiagnostics) ObserveValidated(receipt *contract.MessageReceiptV1) {
	if diagnostics.OnValidated == nil || receipt == nil {
		return
	}
	defer func() { _ = recover() }()
	diagnostics.OnValidated(receipt)
}

func (diagnostics ReceiptPublisherDiagnostics) queued(count uint64) {
	diagnostics.observeCount(diagnostics.OnQueued, count)
}

func (diagnostics ReceiptPublisherDiagnostics) acked(count uint64) {
	diagnostics.observeCount(diagnostics.OnACKed, count)
}

func (diagnostics ReceiptPublisherDiagnostics) observeCount(callback func(uint64), count uint64) {
	if callback == nil || count == 0 {
		return
	}
	defer func() { _ = recover() }()
	callback(count)
}

func (diagnostics ReceiptPublisherDiagnostics) drop(kind ReceiptDropKind, count uint64) {
	diagnostics.ObserveDrop(ReceiptDropEvidence{Kind: kind, Count: count})
}

// ObserveDrop reports one already-classified publisher loss without allowing
// diagnostics failures to affect the evaluation path.
func (diagnostics ReceiptPublisherDiagnostics) ObserveDrop(evidence ReceiptDropEvidence) {
	if diagnostics.OnDrop == nil || evidence.Count == 0 {
		return
	}
	defer func() { _ = recover() }()
	diagnostics.OnDrop(evidence)
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

type CoverageReceiptCounts struct {
	Enqueued, Acked, Dropped      uint64
	PendingMessages, PendingBytes int
}

type ReceiptDrainResult struct {
	Coverage        CoverageReceiptCounts
	Status          ReceiptDrainStatus
	Enqueued        uint64
	Acked           uint64
	PendingMessages int
	PendingBytes    int
	Drops           ReceiptDropCounts
	Err             error
}

type queuedReceipt struct {
	coverage bool
	payload  []byte
}

// ReceiptPublisher is a best-effort audit output. TryEnqueue never waits for a
// broker ACK and its queue bounds include both buffered and in-flight payloads.
type ReceiptPublisher struct {
	core        *DecisionSink
	limits      ReceiptPublisherLimits
	queue       chan queuedReceipt
	diagnostics ReceiptPublisherDiagnostics

	mu              sync.Mutex
	accepting       bool
	pendingMessages int
	pendingBytes    int
	enqueued        uint64
	acked           uint64
	coverage        CoverageReceiptCounts
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
	return OpenReceiptPublisherWithDiagnostics(coordinates, limits, ReceiptPublisherDiagnostics{})
}

func OpenReceiptPublisherWithDiagnostics(
	coordinates DecisionSinkConfig,
	limits ReceiptPublisherLimits,
	diagnostics ReceiptPublisherDiagnostics,
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
	publisher, err := newReceiptPublisherWithDiagnostics(coordinates.OutputTopic, producer, client, limits, diagnostics)
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
	return newReceiptPublisherWithDiagnostics(outputTopic, producer, client, limits, ReceiptPublisherDiagnostics{})
}

func newReceiptPublisherWithDiagnostics(
	outputTopic string,
	producer syncMessageProducer,
	client closeableClient,
	limits ReceiptPublisherLimits,
	diagnostics ReceiptPublisherDiagnostics,
) (*ReceiptPublisher, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	core, err := newDecisionSink(outputTopic, producer, client)
	if err != nil {
		return nil, err
	}
	publisher := &ReceiptPublisher{
		core: core, limits: limits, queue: make(chan queuedReceipt, limits.MaxQueuedMessages), diagnostics: diagnostics,
		accepting: true, workerDone: make(chan struct{}),
	}
	go publisher.run()
	return publisher, nil
}

func (publisher *ReceiptPublisher) TryEnqueue(receipt *contract.MessageReceiptV1) bool {
	if publisher == nil || publisher.core == nil {
		return false
	}

	payload, err := contract.EncodeMessageReceiptV1(receipt)
	if err != nil {
		publisher.mu.Lock()
		publisher.drops.EncodeFailed++
		publisher.recordError(err)
		publisher.mu.Unlock()
		publisher.diagnostics.drop(ReceiptDropEncodeFailed, 1)
		return false
	}
	publisher.diagnostics.ObserveValidated(receipt)

	return publisher.enqueuePayload(payload)
}

// TryEnqueueFinalEvidence reuses the same bounded queue and ACK lifecycle.
func (publisher *ReceiptPublisher) TryEnqueueFinalEvidence(evidence *contract.FinalResultEvidenceV1, maxBytes int) bool {
	if publisher == nil || publisher.core == nil {
		return false
	}
	payload, err := contract.EncodeFinalResultEvidenceV1(evidence, maxBytes)
	if err != nil {
		publisher.mu.Lock()
		publisher.drops.EncodeFailed++
		publisher.recordError(err)
		publisher.mu.Unlock()
		publisher.diagnostics.drop(ReceiptDropEncodeFailed, 1)
		return false
	}
	return publisher.enqueuePayload(payload)
}

// TryEnqueueEncodedFinalEvidence accepts only the official codec's immutable
// value, never caller-supplied unvalidated wire. Queue ownership is a byte copy.
func (publisher *ReceiptPublisher) TryEnqueueEncodedFinalEvidence(evidence contract.EncodedFinalResultV1) bool {
	if publisher == nil || publisher.core == nil {
		return false
	}
	payload := evidence.CopyBytes()
	if len(payload) == 0 {
		publisher.mu.Lock()
		publisher.drops.EncodeFailed++
		publisher.mu.Unlock()
		publisher.diagnostics.drop(ReceiptDropEncodeFailed, 1)
		return false
	}
	return publisher.enqueuePayload(payload)
}

func (publisher *ReceiptPublisher) enqueuePayload(payload []byte) bool {
	return publisher.enqueuePayloadKind(payload, false)
}
func (publisher *ReceiptPublisher) enqueuePayloadKind(payload []byte, coverage bool) bool {
	publisher.mu.Lock()
	if !publisher.accepting {
		publisher.drops.Closed++
		if coverage {
			publisher.coverage.Dropped++
		}
		publisher.mu.Unlock()
		if !coverage {
			publisher.diagnostics.drop(ReceiptDropClosed, 1)
		}
		return false
	}
	if publisher.pendingMessages >= publisher.limits.MaxQueuedMessages {
		publisher.drops.QueueMessages++
		if coverage {
			publisher.coverage.Dropped++
		}
		publisher.mu.Unlock()
		if !coverage {
			publisher.diagnostics.drop(ReceiptDropQueueMessages, 1)
		}
		return false
	}
	if len(payload) > publisher.limits.MaxQueuedBytes-publisher.pendingBytes {
		publisher.drops.QueueBytes++
		if coverage {
			publisher.coverage.Dropped++
		}
		publisher.mu.Unlock()
		if !coverage {
			publisher.diagnostics.drop(ReceiptDropQueueBytes, 1)
		}
		return false
	}
	publisher.pendingMessages++
	publisher.pendingBytes += len(payload)
	publisher.enqueued++
	if coverage {
		publisher.coverage.Enqueued++
		publisher.coverage.PendingMessages++
		publisher.coverage.PendingBytes += len(payload)
	}
	publisher.queue <- queuedReceipt{payload: payload, coverage: coverage}
	publisher.mu.Unlock()
	if !coverage {
		publisher.diagnostics.queued(1)
	}
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
	publisher.pendingMessages--
	publisher.pendingBytes -= len(item.payload)
	if item.coverage {
		publisher.coverage.PendingMessages--
		publisher.coverage.PendingBytes -= len(item.payload)
	}
	if publisher.abandoned {
		publisher.mu.Unlock()
		return
	}
	if err != nil {
		publisher.drops.BrokerACKFailed++
		if item.coverage {
			publisher.coverage.Dropped++
		}
		publisher.recordError(err)
		publisher.mu.Unlock()
		if !item.coverage {
			publisher.diagnostics.drop(ReceiptDropBrokerACKFailed, 1)
		}
		return
	}
	publisher.acked++
	if item.coverage {
		publisher.coverage.Acked++
	}
	publisher.mu.Unlock()
	if !item.coverage {
		publisher.diagnostics.acked(1)
	}
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
			publisher.diagnostics.drop(ReceiptDropCloseFailed, 1)
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
		publisher.drops.ShutdownTimeout += uint64(timedOut)
		publisher.coverage.Dropped += uint64(publisher.coverage.PendingMessages)
		publisher.recordError(ctx.Err())
		result := publisher.snapshotLocked()
		publisher.mu.Unlock()
		publisher.diagnostics.drop(ReceiptDropShutdownTimeout, uint64(timedOut))
		closeErr := publisher.core.Shutdown(ctx)
		result.Status = ReceiptDrainTimedOut
		result.Err = errors.Join(result.Err, closeErr)
		return result
	}
}

func (publisher *ReceiptPublisher) snapshotLocked() ReceiptDrainResult {
	return ReceiptDrainResult{
		Coverage: publisher.coverage,
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

// TryEnqueueBusinessAbnormal shares outstanding bytes/count and broker ACKs
// with the existing immutable final publisher; no second transport or queue.
func (publisher *ReceiptPublisher) TryEnqueueBusinessAbnormal(e contract.EncodedBusinessAbnormalV1) bool {
	if publisher == nil || publisher.core == nil {
		return false
	}
	payload := e.CopyBytes()
	if len(payload) == 0 {
		publisher.mu.Lock()
		publisher.drops.EncodeFailed++
		publisher.mu.Unlock()
		publisher.diagnostics.drop(ReceiptDropEncodeFailed, 1)
		return false
	}
	return publisher.enqueuePayload(payload)
}

// TryEnqueueCoverageReceipt reuses the same queue and outstanding resource
// reservation. Typed delivery counts do not masquerade as final Event metrics.
func (p *ReceiptPublisher) TryEnqueueCoverageReceipt(e contract.EncodedGoCoverageV1) bool {
	if p == nil || p.core == nil {
		return false
	}
	wire := e.CopyBytes()
	if len(wire) == 0 {
		p.mu.Lock()
		p.drops.EncodeFailed++
		p.coverage.Dropped++
		p.mu.Unlock()
		return false
	}
	return p.enqueuePayloadKind(wire, true)
}

// Snapshot is a locked read of actual queue/ACK state. It does not close a
// validation Epoch or assert that an in-flight Receipt will be acknowledged.
func (p *ReceiptPublisher) Snapshot() ReceiptDrainResult {
	if p == nil {
		return ReceiptDrainResult{Status: ReceiptDrainFailed}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked()
}
