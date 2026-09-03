// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redisstream

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/consume"
)

type redisClient interface {
	XReadGroup(ctx context.Context, args *redis.XReadGroupArgs) *redis.XStreamSliceCmd
	XAutoClaim(ctx context.Context, args *redis.XAutoClaimArgs) *redis.XAutoClaimCmd
	XAck(ctx context.Context, stream, group string, ids ...string) *redis.IntCmd
	XGroupCreateMkStream(ctx context.Context, stream, group, start string) *redis.StatusCmd
	Close() error
}

type receiptData struct {
	streamID string
}

// Session 将 Redis PEL 中的 Stream ID 封装为逐条确认 Receipt，并在正常读取前
// 使用 XAUTOCLAIM 接管超过最小空闲时间的崩溃遗留消息。
type Session struct {
	client redisClient
	config Config
	issuer *consume.ReceiptIssuer

	mu          sync.Mutex
	receipts    map[uint64]receiptData
	outstanding map[string]uint64
	claimStart  string
	closed      bool
}

// NewSession 创建 Redis client，并按显式配置选择是否创建 Consumer Group。
func NewSession(config Config) (*Session, error) {
	config = config.WithDefaults()
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("create redis streams session: %w", err)
	}
	options := &redis.Options{
		Addr:     config.Address,
		Username: config.Username,
		Password: config.Password,
		DB:       config.DB,
	}
	if config.UseTLS {
		options.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	client := redis.NewClient(options)
	if config.CreateGroup {
		err := client.XGroupCreateMkStream(context.Background(), config.Stream, config.Group, "0").Err()
		if err != nil && !redis.HasErrorPrefix(err, "BUSYGROUP") {
			_ = client.Close()
			return nil, fmt.Errorf("create redis streams consumer group: %w", err)
		}
	}
	return newSession(config, client), nil
}

func newSession(config Config, client redisClient) *Session {
	return &Session{
		client:      client,
		config:      config,
		issuer:      consume.NewReceiptIssuer(),
		receipts:    make(map[uint64]receiptData),
		outstanding: make(map[string]uint64),
		claimStart:  "0-0",
	}
}

// Capabilities 返回 Redis Streams 的逐条 XACK 能力。
func (s *Session) Capabilities() consume.Capabilities {
	return consume.Capabilities{Settlement: consume.SettlementIndividual}
}

// ValidateRuntime 防止 XAUTOCLAIM 在本地处理和重试预算尚未结束时接管消息。
func (s *Session) ValidateRuntime(config consume.Config) error {
	return s.config.validateRuntime(config)
}

// Receive 优先接管超时 Pending，再读取当前 Consumer Group 的新消息。
func (s *Session) Receive(ctx context.Context, limits consume.ReceiveLimits) ([]consume.Delivery, error) {
	if limits.MaxMessages <= 0 || limits.MaxBytes <= 0 {
		return nil, fmt.Errorf("redis streams receive: invalid limits: %+v", limits)
	}
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}

	messages, redelivered, err := s.claim(ctx, limits.MaxMessages)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		streams, readErr := s.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    s.config.Group,
			Consumer: s.config.Consumer,
			Streams:  []string{s.config.Stream, ">"},
			Count:    int64(limits.MaxMessages),
			Block:    s.config.ReadBlock,
			NoAck:    false,
		}).Result()
		if readErr != nil && !errors.Is(readErr, redis.Nil) {
			return nil, fmt.Errorf("redis streams read group: %w", readErr)
		}
		for _, stream := range streams {
			messages = append(messages, stream.Messages...)
		}
	}
	if len(messages) == 0 {
		return nil, nil
	}

	totalBytes := 0
	for _, message := range messages {
		body, bodyErr := s.messageBody(message)
		if bodyErr != nil {
			return nil, bodyErr
		}
		totalBytes += len(body)
	}
	if totalBytes > limits.MaxBytes {
		return nil, fmt.Errorf("redis streams receive: %w: payload bytes %d exceed %d", consume.ErrReceiveLimitExceeded, totalBytes, limits.MaxBytes)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, consume.ErrSessionClosed
	}
	deliveries := make([]consume.Delivery, 0, len(messages))
	for _, message := range messages {
		if _, exists := s.outstanding[message.ID]; exists {
			continue
		}
		body, bodyErr := s.messageBody(message)
		if bodyErr != nil {
			return nil, bodyErr
		}
		receipt, token := s.issuer.Issue()
		s.receipts[token] = receiptData{streamID: message.ID}
		s.outstanding[message.ID] = token
		messageID := fieldString(message.Values, s.config.MessageIDField)
		if messageID == "" {
			messageID = message.ID
		}
		deliveries = append(deliveries, consume.Delivery{
			Message: consume.Message{
				ID:       messageID,
				TenantID: fieldString(message.Values, s.config.TenantIDField),
				OrderKey: fieldString(message.Values, s.config.OrderKeyField),
				Body:     body,
				Headers:  valueHeaders(message.Values),
			},
			Receipt: receipt,
			Meta: consume.DeliveryMeta{
				Transport: "redis_streams",
				Lane:      s.config.Stream + "/" + s.config.Group,
				Position:  message.ID,
				Attempt:   1,
				Redeliver: redelivered,
			},
		})
	}
	return deliveries, nil
}

func (s *Session) claim(ctx context.Context, maxMessages int) ([]redis.XMessage, bool, error) {
	s.mu.Lock()
	start := s.claimStart
	s.mu.Unlock()
	messages, next, err := s.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   s.config.Stream,
		Group:    s.config.Group,
		Consumer: s.config.Consumer,
		MinIdle:  s.config.ClaimMinIdle,
		Start:    start,
		Count:    int64(maxMessages),
	}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, false, fmt.Errorf("redis streams auto claim: %w", err)
	}
	if next == "" {
		next = "0-0"
	}
	s.mu.Lock()
	s.claimStart = next
	s.mu.Unlock()
	return messages, len(messages) > 0, nil
}

func (s *Session) messageBody(message redis.XMessage) ([]byte, error) {
	if value, exists := message.Values[s.config.BodyField]; exists {
		switch typed := value.(type) {
		case string:
			return []byte(typed), nil
		case []byte:
			return append([]byte(nil), typed...), nil
		default:
			encoded, err := json.Marshal(typed)
			if err != nil {
				return nil, fmt.Errorf("redis streams encode body field for %s: %w", message.ID, err)
			}
			return encoded, nil
		}
	}
	encoded, err := json.Marshal(message.Values)
	if err != nil {
		return nil, fmt.Errorf("redis streams encode message %s: %w", message.ID, err)
	}
	return encoded, nil
}

// Confirm 使用一次 XACK 批量确认已经逐条完成的 Stream ID。XACK 对已确认 ID
// 返回零但不报错，因此确认结果不确定后的重试仍可安全收敛。
func (s *Session) Confirm(ctx context.Context, receipts []consume.Receipt) error {
	if len(receipts) == 0 {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return consume.ErrSessionClosed
	}
	ids := make([]string, 0, len(receipts))
	tokens := make([]uint64, 0, len(receipts))
	for _, receipt := range receipts {
		token, ok := s.issuer.Resolve(receipt)
		if !ok {
			s.mu.Unlock()
			return fmt.Errorf("redis streams confirm: receipt belongs to another session")
		}
		data, exists := s.receipts[token]
		if !exists {
			s.mu.Unlock()
			return fmt.Errorf("redis streams confirm: unknown receipt token")
		}
		ids = append(ids, data.streamID)
		tokens = append(tokens, token)
	}
	s.mu.Unlock()

	if err := s.client.XAck(ctx, s.config.Stream, s.config.Group, ids...).Err(); err != nil {
		return fmt.Errorf("redis streams ack: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, token := range tokens {
		delete(s.receipts, token)
		delete(s.outstanding, ids[index])
	}
	return nil
}

// Close 关闭 Redis client；PEL 中未确认消息保留给后续 XAUTOCLAIM。
func (s *Session) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return consume.ErrSessionClosed
	}
	s.closed = true
	s.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- s.client.Close() }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("close redis streams session: %w", err)
		}
		return nil
	}
}

func (s *Session) ensureOpen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return consume.ErrSessionClosed
	}
	return nil
}

func fieldString(values map[string]any, field string) string {
	value, exists := values[field]
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

func valueHeaders(values map[string]any) map[string][]byte {
	headers := make(map[string][]byte, len(values))
	for key, value := range values {
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

var _ consume.RuntimeValidator = (*Session)(nil)
