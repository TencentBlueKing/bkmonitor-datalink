// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

// Package notifier receives Kafka messages and marks offsets only after the
// window confirms persistence.
package notifier

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	utilsKafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Pipeline is session-owned. One notifier worker serializes these calls across
// assigned partitions, preserving batch order before offsets are marked.
type Pipeline interface {
	Reset()
	Add(context.Context, []byte) (int, error)
	Flush(context.Context) error
}

// KafkaConfig contains transport connection values, never message business identities.
type KafkaConfig struct {
	Brokers                   []string
	Topic, Username, Password string
}

// Consumer runs one group, with one serialized writer for all assigned partitions.
type Consumer struct {
	dataID   string
	kafka    KafkaConfig
	options  core.Options
	pipeline Pipeline
}

// New creates a consumer without establishing network connections.
func New(dataID uint, kafka KafkaConfig, options core.Options, pipeline Pipeline) (*Consumer, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if dataID == 0 || len(kafka.Brokers) == 0 || kafka.Topic == "" || pipeline == nil {
		return nil, fmt.Errorf("DataID, brokers, topic and pipeline are required")
	}
	if (kafka.Username == "") != (kafka.Password == "") {
		return nil, fmt.Errorf("both Kafka username and password are required for SASL")
	}
	return &Consumer{strconv.FormatUint(uint64(dataID), 10), kafka, options, pipeline}, nil
}

// Run recreates sessions after normal rebalances. Processing/storage failures are
// returned to the caller, with failed messages left unmarked for replay.
func (c *Consumer) Run(ctx context.Context) error {
	config := sarama.NewConfig()
	config.Version = sarama.V0_10_2_0
	config.ChannelBufferSize = 1
	config.Consumer.Offsets.Initial = sarama.OffsetNewest
	config.Consumer.Return.Errors = true
	config.Net.DialTimeout = c.options.DBTimeout
	config.Net.ReadTimeout = c.options.DBTimeout
	config.Net.WriteTimeout = c.options.DBTimeout
	config.Consumer.Group.Rebalance.Timeout = c.options.DrainTimeout + 2*c.options.DBTimeout
	if c.kafka.Username != "" {
		config.Net.SASL.Enable = true
		config.Net.SASL.User = c.kafka.Username
		config.Net.SASL.Password = c.kafka.Password
		config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
		config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient { return &utilsKafka.XDGSCRAMClient{HashGeneratorFcn: utilsKafka.SHA512} }
	}
	group, err := sarama.NewConsumerGroup(c.kafka.Brokers, "metric-metadata-"+c.dataID, config)
	if err != nil {
		logger.Errorf("metric metadata Kafka consumer exiting dataid=%s topic=%q reason=consumer_group_create_error err=%v", c.dataID, c.kafka.Topic, err)
		return fmt.Errorf("create metric metadata Kafka group: %w", err)
	}
	consumeCtx, cancel := context.WithCancel(ctx)
	watchCtx, stopWatch := context.WithCancel(context.Background())
	done := make(chan struct{})
	failures := make(chan error, 1)
	go func() {
		defer close(done)
		for {
			select {
			case err, ok := <-group.Errors():
				if !ok {
					return
				}
				if err != nil {
					select {
					case failures <- err:
					default:
					}
					cancel()
				}
			case <-watchCtx.Done():
				return
			}
		}
	}()
	defer func() {
		cancel()
		if closeErr := group.Close(); closeErr != nil {
			logger.Warnf("metric metadata Kafka consumer group close failed dataid=%s err=%v", c.dataID, closeErr)
		}
		stopWatch()
		<-done
	}()
	h := &handler{pipeline: c.pipeline, options: c.options, dataID: c.dataID}
	for consumeCtx.Err() == nil {
		err := group.Consume(consumeCtx, []string{c.kafka.Topic}, h)
		if h.err != nil {
			logger.Errorf("metric metadata Kafka consumer exiting dataid=%s topic=%q reason=session_processing_error err=%v", c.dataID, c.kafka.Topic, h.err)
			return fmt.Errorf("process metric metadata: %w", h.err)
		}
		if err != nil {
			select {
			case groupErr := <-failures:
				logger.Errorf("metric metadata Kafka consumer exiting dataid=%s topic=%q reason=consumer_group_error err=%v", c.dataID, c.kafka.Topic, groupErr)
				return fmt.Errorf("consume metric metadata Kafka group: %w", groupErr)
			default:
			}
			if ctx.Err() != nil {
				logger.Infof("metric metadata Kafka consumer stopped dataid=%s topic=%q reason=context_canceled err=%v", c.dataID, c.kafka.Topic, ctx.Err())
				return ctx.Err()
			}
			logger.Errorf("metric metadata Kafka consumer exiting dataid=%s topic=%q reason=consume_error err=%v", c.dataID, c.kafka.Topic, err)
			return fmt.Errorf("consume metric metadata: %w", err)
		}
	}
	select {
	case groupErr := <-failures:
		logger.Errorf("metric metadata Kafka consumer exiting dataid=%s topic=%q reason=consumer_group_error err=%v", c.dataID, c.kafka.Topic, groupErr)
		return fmt.Errorf("consume metric metadata Kafka group: %w", groupErr)
	default:
	}
	if err := ctx.Err(); err != nil {
		logger.Infof("metric metadata Kafka consumer stopped dataid=%s topic=%q reason=context_canceled err=%v", c.dataID, c.kafka.Topic, err)
		return err
	}
	if err := consumeCtx.Err(); err != nil {
		logger.Errorf("metric metadata Kafka consumer exiting dataid=%s topic=%q reason=consumer_context_canceled err=%v", c.dataID, c.kafka.Topic, err)
		return err
	}
	return nil
}

type handler struct {
	pipeline Pipeline
	options  core.Options
	dataID   string
	ctx      context.Context
	cancel   context.CancelFunc
	queue    *messageQueue
	done     chan struct{}
	err      error
}

func (h *handler) Setup(session sarama.ConsumerGroupSession) error {
	// A rebalance starts a fresh window session and queue; no uncommitted
	// records or offsets from the previous assignment carry over.
	h.pipeline.Reset()
	h.ctx, h.cancel = context.WithCancel(session.Context())
	h.queue = newMessageQueue(h.options.QueueMessages, h.options.QueueBytes)
	h.done = make(chan struct{})
	h.err = nil
	logger.Debugf("metric metadata Kafka session started dataid=%s claims=%d", h.dataID, len(session.Claims()))
	go func() {
		defer close(h.done)
		h.err = h.work(session)
		if h.err != nil {
			metrics.MetricMetadataMessages.WithLabelValues(h.dataID, "failed").Inc()
			h.cancel()
		}
	}()
	return nil
}

func (h *handler) ConsumeClaim(_ sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case message, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			q := queuedMessage{message: message, queueSize: queuedMessageSize(message)}
			metrics.MetricMetadataMessages.WithLabelValues(h.dataID, "received").Inc()
			metrics.MetricMetadataQueueMessages.WithLabelValues(h.dataID).Inc()
			metrics.MetricMetadataQueueBytes.WithLabelValues(h.dataID).Add(float64(q.queueSize))
			if err := h.queue.Push(h.ctx, q); err != nil {
				h.release(q)
				return err
			}
		case <-h.ctx.Done():
			return nil
		}
	}
}

func (h *handler) Cleanup(sarama.ConsumerGroupSession) error {
	// Sarama calls Cleanup after every claim has stopped, and commits marked
	// offsets after it returns. The worker may finish a bounded drain here.
	h.cancel()
	h.queue.Close()
	<-h.done
	for _, q := range h.queue.Drain() {
		h.release(q)
	}
	return h.err
}

func (h *handler) release(q queuedMessage) {
	h.queue.Release(q)
	h.releaseMetrics(q)
}

func (h *handler) releaseMetrics(q queuedMessage) {
	metrics.MetricMetadataQueueMessages.WithLabelValues(h.dataID).Dec()
	metrics.MetricMetadataQueueBytes.WithLabelValues(h.dataID).Sub(float64(q.queueSize))
}

func (h *handler) work(session sarama.ConsumerGroupSession) error {
	// ConsumeClaim goroutines only enqueue. This worker alone mutates the
	// pipeline and marks messages, so processing and offset order stay aligned.
	var pending []queuedMessage
	records, bytes := 0, 0
	defer func() {
		for _, q := range pending {
			h.release(q)
		}
	}()
	var windowTimer *time.Timer
	var windowTimerC <-chan time.Time
	stopWindow := func() {
		if windowTimer == nil {
			return
		}
		if !windowTimer.Stop() {
			select {
			case <-windowTimer.C:
			default:
			}
		}
		windowTimer = nil
		windowTimerC = nil
	}
	startWindow := func() {
		// 第一条消息启动窗口；条数或字节阈值仍可提前提交。
		if windowTimer == nil {
			windowTimer = time.NewTimer(h.options.BatchWait)
			windowTimerC = windowTimer.C
		}
	}
	defer stopWindow()
	flush := func(ctx context.Context) error {
		started := time.Now()
		messageCount, recordCount := len(pending), records
		byteCount := bytes
		if err := h.pipeline.Flush(ctx); err != nil {
			logger.Errorf("metric metadata Kafka batch failed dataid=%s messages=%d records=%d bytes=%d duration=%s err=%v", h.dataID, messageCount, recordCount, byteCount, time.Since(started), err)
			return fmt.Errorf("flush metric metadata Kafka batch: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// Mark offsets only after the window confirms all pending DB writes.
		for _, q := range pending {
			session.MarkMessage(q.message, "")
			metrics.MetricMetadataMessages.WithLabelValues(h.dataID, "completed").Inc()
			h.release(q)
		}
		pending = nil
		records = 0
		bytes = 0
		stopWindow()
		return nil
	}
	add := func(ctx context.Context, q queuedMessage) error {
		n, err := h.pipeline.Add(ctx, q.message.Value)
		if err != nil {
			logger.Errorf("metric metadata Kafka message processing failed dataid=%s topic=%s partition=%d offset=%d pending_messages=%d pending_records=%d pending_bytes=%d err=%v", h.dataID, q.message.Topic, q.message.Partition, q.message.Offset, len(pending), records, bytes, err)
			return fmt.Errorf("process Kafka message partition=%d offset=%d: %w", q.message.Partition, q.message.Offset, err)
		}
		if len(pending) == 0 {
			startWindow()
		}
		pending = append(pending, q)
		records += n
		bytes += int(q.queueSize)
		return nil
	}
	drain := func(retry *queuedMessage) error {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(session.Context()), h.options.DrainTimeout)
		defer cancel()
		if retry != nil {
			if err := add(ctx, *retry); err != nil {
				h.release(*retry)
				return err
			}
		}
		for {
			q, ok, err := h.queue.Receive(ctx)
			if err != nil {
				return err
			}
			if !ok {
				return flush(ctx)
			}
			if err := add(ctx, q); err != nil {
				h.release(q)
				return err
			}
			if records >= h.options.BatchRecords || bytes >= h.options.BatchBytes || h.queue.UnderPressure() {
				if err := flush(ctx); err != nil {
					return err
				}
			}
		}
	}
	for {
		select {
		case <-h.queue.wake:
			q, ok, err := h.queue.Receive(h.ctx)
			if err != nil {
				if h.ctx.Err() != nil {
					return drain(nil)
				}
				return err
			}
			if !ok {
				return drain(nil)
			}
			if err := add(h.ctx, q); err != nil {
				if h.ctx.Err() != nil {
					return drain(&q)
				}
				h.release(q)
				return err
			}
			if records >= h.options.BatchRecords || bytes >= h.options.BatchBytes || h.queue.UnderPressure() {
				if err := flush(h.ctx); err != nil {
					if h.ctx.Err() != nil {
						return drain(nil)
					}
					return err
				}
			}
		case <-h.queue.pressure:
			if len(pending) > 0 {
				if err := flush(h.ctx); err != nil {
					if h.ctx.Err() != nil {
						return drain(nil)
					}
					return err
				}
			}
		case <-windowTimerC:
			if err := flush(h.ctx); err != nil {
				if h.ctx.Err() != nil {
					return drain(nil)
				}
				return err
			}
		case <-h.ctx.Done():
			return drain(nil)
		}
	}
}

type queuedMessage struct {
	message   *sarama.ConsumerMessage
	queueSize int64
}

// queuedMessageSize accounts for retained payloads and per-message/header
// bookkeeping so queue.maxBytes tracks memory more closely than Value alone.
func queuedMessageSize(message *sarama.ConsumerMessage) int64 {
	const messageOverhead = 256
	const headerOverhead = 32
	size := int64(messageOverhead + len(message.Key) + len(message.Value))
	for _, header := range message.Headers {
		size += headerOverhead + int64(len(header.Key)+len(header.Value))
	}
	return size
}

type messageQueue struct {
	mu       sync.Mutex
	items    []queuedMessage
	head     int
	bytes    int64
	count    int
	maxCount int
	maxBytes int64
	closed   bool
	blocked  bool
	wake     chan struct{}
	pressure chan struct{}
	space    chan struct{}
}

func newMessageQueue(maxCount int, maxBytes int64) *messageQueue {
	return &messageQueue{maxCount: maxCount, maxBytes: maxBytes, wake: make(chan struct{}, 1), pressure: make(chan struct{}, 1), space: make(chan struct{}, 1)}
}

func (q *messageQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *messageQueue) Push(ctx context.Context, message queuedMessage) error {
	if message.queueSize > q.maxBytes {
		return fmt.Errorf("Kafka message size %d exceeds queue byte limit %d", message.queueSize, q.maxBytes)
	}
	for {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			return context.Canceled
		}
		if q.count < q.maxCount && q.bytes+message.queueSize <= q.maxBytes {
			q.items = append(q.items, message)
			q.bytes += message.queueSize
			q.count++
			q.signal()
			q.mu.Unlock()
			return nil
		}
		q.blocked = true
		select {
		case q.pressure <- struct{}{}:
		default:
		}
		space := q.space
		q.mu.Unlock()
		select {
		case <-space:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (q *messageQueue) Receive(ctx context.Context) (queuedMessage, bool, error) {
	for {
		q.mu.Lock()
		if q.head < len(q.items) {
			message := q.items[q.head]
			q.items[q.head] = queuedMessage{}
			q.head++
			if q.head == len(q.items) {
				q.items = nil
				q.head = 0
			} else if q.head >= 1024 && q.head*2 >= len(q.items) {
				q.items = append([]queuedMessage(nil), q.items[q.head:]...)
				q.head = 0
			}
			if q.head < len(q.items) {
				q.signal()
			}
			q.mu.Unlock()
			return message, true, nil
		}
		if q.closed {
			q.mu.Unlock()
			return queuedMessage{}, false, nil
		}
		q.mu.Unlock()
		select {
		case <-q.wake:
		case <-ctx.Done():
			return queuedMessage{}, false, ctx.Err()
		}
	}
}

func (q *messageQueue) UnderPressure() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.blocked
}

// Release 在处理完成或放弃时归还队列和待提交窗口共用的预算。
func (q *messageQueue) Release(message queuedMessage) {
	q.mu.Lock()
	q.count--
	q.blocked = false
	q.bytes -= message.queueSize
	close(q.space)
	q.space = make(chan struct{})
	q.mu.Unlock()
}

func (q *messageQueue) Close() {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		q.signal()
		close(q.space)
		q.space = make(chan struct{})
	}
	q.mu.Unlock()
}

func (q *messageQueue) Drain() []queuedMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	remaining := append([]queuedMessage(nil), q.items[q.head:]...)
	q.items = nil
	q.head = 0
	return remaining
}
