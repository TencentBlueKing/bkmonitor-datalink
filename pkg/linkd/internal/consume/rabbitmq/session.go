// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
	"linkd/internal/consume"
)

type amqpChannel interface {
	Qos(prefetchCount, prefetchSize int, global bool) error
	ConsumeWithContext(ctx context.Context, queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error)
	Close() error
}

type amqpConnection interface {
	Close() error
}

// Session 将 RabbitMQ Channel 内 delivery tag 封装为 Session 作用域 Receipt。
type Session struct {
	config     Config
	connection amqpConnection
	channel    amqpChannel
	deliveries <-chan amqp.Delivery
	cancel     context.CancelFunc
	issuer     *consume.ReceiptIssuer

	mu       sync.Mutex
	receipts map[uint64]amqp.Delivery
	buffered *amqp.Delivery
	closed   bool
}

// NewSession 建立 RabbitMQ 连接、设置 prefetch 并启动 manual ACK consumer。
func NewSession(config Config) (*Session, error) {
	config = config.WithDefaults()
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("create rabbitmq session: %w", err)
	}
	connection, err := amqp.DialConfig(config.URL, amqp.Config{})
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}
	channel, err := connection.Channel()
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("open rabbitmq channel: %w", err)
	}
	if err := channel.Qos(config.Prefetch, 0, false); err != nil {
		_ = channel.Close()
		_ = connection.Close()
		return nil, fmt.Errorf("configure rabbitmq qos: %w", err)
	}
	consumeCtx, cancel := context.WithCancel(context.Background())
	deliveries, err := channel.ConsumeWithContext(
		consumeCtx,
		config.Queue,
		config.ConsumerTag,
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		cancel()
		_ = channel.Close()
		_ = connection.Close()
		return nil, fmt.Errorf("consume rabbitmq queue: %w", err)
	}
	return newSession(config, connection, channel, deliveries, cancel), nil
}

func newSession(
	config Config,
	connection amqpConnection,
	channel amqpChannel,
	deliveries <-chan amqp.Delivery,
	cancel context.CancelFunc,
) *Session {
	return &Session{
		config:     config,
		connection: connection,
		channel:    channel,
		deliveries: deliveries,
		cancel:     cancel,
		issuer:     consume.NewReceiptIssuer(),
		receipts:   make(map[uint64]amqp.Delivery),
	}
}

// Capabilities 返回 RabbitMQ 逐条 ACK 能力。
func (s *Session) Capabilities() consume.Capabilities {
	return consume.Capabilities{Settlement: consume.SettlementIndividual}
}

// Receive 从 push consumer channel 桥接出有界批次。
func (s *Session) Receive(ctx context.Context, limits consume.ReceiveLimits) ([]consume.Delivery, error) {
	if limits.MaxMessages <= 0 || limits.MaxBytes <= 0 {
		return nil, fmt.Errorf("rabbitmq receive: invalid limits: %+v", limits)
	}
	first, err := s.nextDelivery(ctx)
	if err != nil {
		return nil, err
	}
	raw := []amqp.Delivery{first}
	totalBytes := len(first.Body)
	if totalBytes > limits.MaxBytes {
		return nil, fmt.Errorf("rabbitmq receive: %w: message payload bytes %d exceed %d", consume.ErrReceiveLimitExceeded, totalBytes, limits.MaxBytes)
	}

	for len(raw) < limits.MaxMessages {
		next, ok, err := s.tryNextDelivery()
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		if totalBytes+len(next.Body) > limits.MaxBytes {
			s.mu.Lock()
			s.buffered = &next
			s.mu.Unlock()
			break
		}
		totalBytes += len(next.Body)
		raw = append(raw, next)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, consume.ErrSessionClosed
	}
	deliveries := make([]consume.Delivery, 0, len(raw))
	for _, delivery := range raw {
		messageID := delivery.MessageId
		if messageID == "" {
			messageID = tableString(delivery.Headers, s.config.MessageIDHeader)
		}
		if messageID == "" {
			return nil, fmt.Errorf("rabbitmq receive: delivery tag %d has no stable message id", delivery.DeliveryTag)
		}
		receipt, token := s.issuer.Issue()
		s.receipts[token] = delivery
		deliveries = append(deliveries, consume.Delivery{
			Message: consume.Message{
				ID:         messageID,
				TenantID:   tableString(delivery.Headers, s.config.TenantIDHeader),
				OrderKey:   tableString(delivery.Headers, s.config.OrderKeyHeader),
				Body:       append([]byte(nil), delivery.Body...),
				Headers:    tableHeaders(delivery.Headers),
				EnqueuedAt: delivery.Timestamp,
			},
			Receipt: receipt,
			Meta: consume.DeliveryMeta{
				Transport: "rabbitmq",
				Lane:      s.config.Queue,
				Position:  strconv.FormatUint(delivery.DeliveryTag, 10),
				Attempt:   1,
				Redeliver: delivery.Redelivered,
			},
		})
	}
	return deliveries, nil
}

func (s *Session) nextDelivery(ctx context.Context) (amqp.Delivery, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return amqp.Delivery{}, consume.ErrSessionClosed
	}
	if s.buffered != nil {
		delivery := *s.buffered
		s.buffered = nil
		s.mu.Unlock()
		return delivery, nil
	}
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return amqp.Delivery{}, ctx.Err()
	case delivery, ok := <-s.deliveries:
		if !ok {
			return amqp.Delivery{}, consume.ErrSessionClosed
		}
		return delivery, nil
	}
}

func (s *Session) tryNextDelivery() (amqp.Delivery, bool, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return amqp.Delivery{}, false, consume.ErrSessionClosed
	}
	s.mu.Unlock()
	select {
	case delivery, ok := <-s.deliveries:
		if !ok {
			return amqp.Delivery{}, false, consume.ErrSessionClosed
		}
		return delivery, true, nil
	default:
		return amqp.Delivery{}, false, nil
	}
}

// Confirm 始终使用 multiple=false 逐条确认，避免误确认仍在并发处理的前序消息。
func (s *Session) Confirm(_ context.Context, receipts []consume.Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return consume.ErrSessionClosed
	}
	for _, receipt := range receipts {
		token, ok := s.issuer.Resolve(receipt)
		if !ok {
			return fmt.Errorf("rabbitmq confirm: receipt belongs to another session")
		}
		delivery, exists := s.receipts[token]
		if !exists {
			return fmt.Errorf("rabbitmq confirm: unknown receipt token")
		}
		if err := delivery.Ack(false); err != nil {
			return fmt.Errorf("rabbitmq ack delivery tag %d: %w", delivery.DeliveryTag, err)
		}
		delete(s.receipts, token)
	}
	return nil
}

// Close 关闭 Channel 和 Connection；未确认 delivery 将由 RabbitMQ 重投。
func (s *Session) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return consume.ErrSessionClosed
	}
	s.closed = true
	s.cancel()
	s.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		channelErr := s.channel.Close()
		connectionErr := s.connection.Close()
		done <- errors.Join(channelErr, connectionErr)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("close rabbitmq session: %w", err)
		}
		return nil
	}
}

func tableString(table amqp.Table, key string) string {
	value, exists := table[key]
	if !exists {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func tableHeaders(table amqp.Table) map[string][]byte {
	headers := make(map[string][]byte, len(table))
	for key, value := range table {
		switch typed := value.(type) {
		case string:
			headers[key] = []byte(typed)
		case []byte:
			headers[key] = append([]byte(nil), typed...)
		default:
			encoded, err := json.Marshal(typed)
			if err == nil {
				headers[key] = encoded
			}
		}
	}
	return headers
}

var _ consume.Session = (*Session)(nil)
