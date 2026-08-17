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
	"sync/atomic"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
)

var (
	ErrDecisionSinkClosed          = errors.New("kafka decision sink: closed")
	ErrDecisionSinkShutdownTimeout = errors.New("kafka decision sink: shutdown timeout")
)

type syncMessageProducer interface {
	SendMessage(*sarama.ProducerMessage) (int32, int64, error)
	Close() error
}

type closeableClient interface {
	Close() error
}

// DecisionSink owns one isolated synchronous producer and its dedicated
// client. It is safe for concurrent use by multiple consumer claims.
type DecisionSink struct {
	outputTopic string
	producer    syncMessageProducer
	client      closeableClient

	mu       sync.Mutex
	closing  bool
	inflight sync.WaitGroup

	producerResource ownedResource
	clientResource   ownedResource

	gracefulOnce sync.Once
	gracefulDone chan struct{}
	gracefulErr  error
	forcedOnce   sync.Once
	forced       atomic.Bool
	forcedDone   chan struct{}
	forcedErr    error
}

// OpenDecisionSink creates the only production DecisionSink implementation.
// Topic existence, ACLs and broker-side limits remain deployment evidence.
func OpenDecisionSink(coordinates DecisionSinkConfig) (*DecisionSink, error) {
	config, err := NewDecisionProducerConfig(coordinates)
	if err != nil {
		return nil, err
	}
	client, err := sarama.NewClient(coordinates.Brokers, config)
	if err != nil {
		return nil, fmt.Errorf("kafka decision sink: open client: %w", err)
	}
	producer, err := sarama.NewSyncProducerFromClient(client)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("kafka decision sink: open producer: %w", err), client.Close())
	}
	sink, err := newDecisionSink(coordinates.OutputTopic, producer, client)
	if err != nil {
		return nil, errors.Join(err, producer.Close(), client.Close())
	}
	return sink, nil
}

func newDecisionSink(outputTopic string, producer syncMessageProducer, client closeableClient) (*DecisionSink, error) {
	if err := validateKafkaTopicName("output_topic", outputTopic); err != nil {
		return nil, err
	}
	if producer == nil || client == nil {
		return nil, errors.New("kafka decision sink: producer and client are required")
	}
	sink := &DecisionSink{
		outputTopic:  outputTopic,
		producer:     producer,
		client:       client,
		gracefulDone: make(chan struct{}),
		forcedDone:   make(chan struct{}),
	}
	sink.producerResource.close = producer.Close
	sink.clientResource.close = client.Close
	return sink, nil
}

// WriteBatch returns success only after the synchronous producer reports the
// broker acknowledgement. Once sending starts, context cancellation cannot
// turn an unknown broker result into an early return.
func (s *DecisionSink) WriteBatch(ctx context.Context, batch *contract.TriggerDecisionBatch) error {
	if s == nil || s.producer == nil {
		return ErrDecisionSinkClosed
	}
	if ctx == nil {
		return errors.New("kafka decision sink: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := contract.EncodeTriggerDecisionBatch(batch)
	if err != nil {
		return fmt.Errorf("kafka decision sink: encode batch: %w", err)
	}
	key, err := batch.PartitionKey()
	if err != nil {
		return fmt.Errorf("kafka decision sink: derive partition key: %w", err)
	}

	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return ErrDecisionSinkClosed
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return err
	}
	s.inflight.Add(1)
	s.mu.Unlock()
	defer s.inflight.Done()

	message := &sarama.ProducerMessage{
		Topic: s.outputTopic,
		Key:   sarama.ByteEncoder(key),
		Value: sarama.ByteEncoder(payload),
	}
	if _, _, err := s.producer.SendMessage(message); err != nil {
		producerErr := fmt.Errorf("kafka decision sink: send batch: %w", err)
		if contextErr := ctx.Err(); contextErr != nil {
			return errors.Join(contextErr, producerErr)
		}
		return producerErr
	}
	return nil
}

// Shutdown prevents new writes and closes the producer before its dedicated
// client after registered sends finish. If the context ends first, it starts
// one client-close attempt that may interrupt an in-flight broker call and
// returns without waiting past the caller's deadline. Resource cleanup can
// continue asynchronously after that bounded return.
func (s *DecisionSink) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("kafka decision sink: context is required")
	}
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()

	select {
	case <-s.gracefulDone:
		return s.gracefulErr
	default:
	}
	if err := ctx.Err(); err != nil {
		s.startForcedShutdown()
		s.startGracefulShutdown()
		return errors.Join(ErrDecisionSinkShutdownTimeout, err, s.knownForcedError())
	}
	s.startGracefulShutdown()
	select {
	case <-s.gracefulDone:
		return s.gracefulErr
	case <-ctx.Done():
		select {
		case <-s.gracefulDone:
			return s.gracefulErr
		default:
		}
		s.startForcedShutdown()
		return errors.Join(ErrDecisionSinkShutdownTimeout, ctx.Err(), s.knownForcedError())
	}
}

// Close waits without a deadline for the shared shutdown attempt.
func (s *DecisionSink) Close() error {
	return s.Shutdown(context.Background())
}

func (s *DecisionSink) startGracefulShutdown() {
	s.gracefulOnce.Do(func() {
		go func() {
			s.inflight.Wait()
			producerErr := s.producerResource.Close()
			if s.forced.Load() && producerErr == sarama.ErrClosedClient {
				producerErr = nil
			}
			s.gracefulErr = errors.Join(producerErr, s.clientResource.Close())
			close(s.gracefulDone)
		}()
	})
}

func (s *DecisionSink) startForcedShutdown() {
	s.forcedOnce.Do(func() {
		s.forced.Store(true)
		go func() {
			s.forcedErr = s.clientResource.Close()
			close(s.forcedDone)
		}()
	})
}

func (s *DecisionSink) knownForcedError() error {
	select {
	case <-s.forcedDone:
		return s.forcedErr
	default:
		return nil
	}
}
