// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
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
	"strconv"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"linkd/internal/consume"
	"linkd/internal/kafkaclient"
)

type kafkaClient interface {
	PollRecords(ctx context.Context, maxPollRecords int) kgo.Fetches
	CommitRecords(ctx context.Context, records ...*kgo.Record) error
	AllowRebalance()
	PauseFetchPartitions(topicPartitions map[string][]int32) map[string][]int32
	ResumeFetchPartitions(topicPartitions map[string][]int32)
	PauseFetchTopics(topics ...string) []string
	ResumeFetchTopics(topics ...string)
	Close()
}

type receiptData struct {
	record *kgo.Record
	lane   string
}

type ownershipBridge struct {
	mu       sync.RWMutex
	events   chan consume.OwnershipEvent
	owned    map[string]bool
	enforced bool
}

func newOwnershipBridge() *ownershipBridge {
	return &ownershipBridge{events: make(chan consume.OwnershipEvent, 8), owned: make(map[string]bool)}
}

// Session 把 Kafka partition offset 映射为 lane 级累计确认。
// Runtime 可以接管多个有界 poll 结果；每个 partition 只允许提交连续 Receipt 前缀。
type Session struct {
	client kafkaClient
	config Config
	issuer *consume.ReceiptIssuer

	mu         sync.Mutex
	receipts   map[uint64]receiptData
	laneTokens map[string][]uint64
	laneParts  map[string]map[string][]int32
	flowPaused bool
	closed     bool
	ownership  *ownershipBridge
}

// NewSession 创建并验证 Kafka consumer。调用方负责在 Runtime 结束时关闭 Session。
func NewSession(config Config) (*Session, error) {
	config = config.WithDefaults()
	if err := config.validateStatic(); err != nil {
		return nil, fmt.Errorf("create kafka session: %w", err)
	}

	options, err := kafkaclient.ClientOptions(config.Brokers, config.ClientID, config.Security)
	if err != nil {
		return nil, fmt.Errorf("create kafka session options: %w", err)
	}
	ownership := newOwnershipBridge()
	options = append(options,
		kgo.ConsumeTopics(config.Topic),
		kgo.ConsumerGroup(config.ConsumerGroup),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.FetchMaxBytes(config.MaxFetchBytes),
		kgo.OnPartitionsAssigned(ownership.assigned),
		kgo.OnPartitionsRevoked(ownership.revoked),
		kgo.OnPartitionsLost(ownership.lost),
	)
	client, err := kgo.NewClient(options...)
	if err != nil {
		return nil, fmt.Errorf("create kafka client: %w", err)
	}
	session := newSession(config, client)
	session.ownership = ownership
	return session, nil
}

func newSession(config Config, client kafkaClient) *Session {
	return &Session{
		client:     client,
		config:     config,
		issuer:     consume.NewReceiptIssuer(),
		receipts:   make(map[uint64]receiptData),
		laneTokens: make(map[string][]uint64),
		laneParts:  make(map[string]map[string][]int32),
	}
}

// Capabilities 返回 Kafka 的累计确认与 partition 暂停能力。
func (s *Session) Capabilities() consume.Capabilities {
	return consume.Capabilities{Settlement: consume.SettlementCumulative, CanPauseLane: true}
}

// Receive 拉取一个有界批次；Runtime 通过全局和 lane 上限控制多个 poll 的在途规模。
func (s *Session) Receive(ctx context.Context, limits consume.ReceiveLimits) ([]consume.Delivery, error) {
	if limits.MaxMessages <= 0 || limits.MaxBytes <= 0 {
		return nil, fmt.Errorf("kafka receive: invalid limits: %+v", limits)
	}
	paused, err := s.receiveState()
	if err != nil {
		return nil, err
	}
	pollCtx := ctx
	cancelPoll := func() {}
	if paused {
		// 全 Flow 暂停时仍需周期 Poll，以免超过 Kafka max.poll.interval；
		// topic fetch 已暂停，因此该 Poll 只推进 Consumer Group 协议，不接管新记录。
		pollCtx, cancelPoll = context.WithTimeout(ctx, time.Second)
	}
	fetches := s.client.PollRecords(pollCtx, limits.MaxMessages)
	cancelPoll()
	if fetchErrors := fetches.Errors(); len(fetchErrors) > 0 {
		if paused && errors.Is(fetchErrors[0].Err, context.DeadlineExceeded) && ctx.Err() == nil {
			s.allowRebalanceIfIdle()
			return nil, nil
		}
		s.allowRebalanceIfIdle()
		return nil, fmt.Errorf("kafka poll: topic=%q partition=%d: %w", fetchErrors[0].Topic, fetchErrors[0].Partition, fetchErrors[0].Err)
	}
	records := fetches.Records()
	if len(records) == 0 {
		s.allowRebalanceIfIdle()
		return nil, nil
	}

	totalBytes := 0
	for _, record := range records {
		totalBytes += len(record.Value)
	}
	if totalBytes > limits.MaxBytes {
		s.allowRebalanceIfIdle()
		return nil, fmt.Errorf("kafka receive: %w: payload bytes %d exceed %d", consume.ErrReceiveLimitExceeded, totalBytes, limits.MaxBytes)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, consume.ErrSessionClosed
	}
	deliveries := make([]consume.Delivery, 0, len(records))
	for _, record := range records {
		lane := laneName(record.Topic, record.Partition)
		receipt, token := s.issuer.Issue()
		s.receipts[token] = receiptData{record: record, lane: lane}
		s.laneTokens[lane] = append(s.laneTokens[lane], token)
		s.laneParts[lane] = map[string][]int32{record.Topic: {record.Partition}}
		deliveries = append(deliveries, consume.Delivery{
			Message: consume.Message{
				ID:         valueOrTransportID(record, s.config.MessageIDHeader),
				TenantID:   headerValue(record, s.config.TenantIDHeader),
				OrderKey:   headerValue(record, s.config.OrderKeyHeader),
				Body:       append([]byte(nil), record.Value...),
				Headers:    recordHeaders(record),
				EnqueuedAt: record.Timestamp,
			},
			Receipt: receipt,
			Meta: consume.DeliveryMeta{
				Transport: "kafka",
				Lane:      lane,
				Position:  strconv.FormatInt(record.Offset, 10),
				Attempt:   1,
			},
		})
	}
	s.mu.Unlock()
	return deliveries, nil
}

// Confirm 累计提交每个 partition 中由运行时证明连续完成的最高 offset。
func (s *Session) Confirm(ctx context.Context, receipts []consume.Receipt) error {
	if len(receipts) == 0 {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return consume.ErrSessionClosed
	}
	tokensByLane := make(map[string][]uint64)
	highest := make(map[string]*kgo.Record)
	for _, receipt := range receipts {
		token, ok := s.issuer.Resolve(receipt)
		if !ok {
			s.mu.Unlock()
			return fmt.Errorf("kafka confirm: receipt belongs to another session")
		}
		data, exists := s.receipts[token]
		if !exists {
			s.mu.Unlock()
			return fmt.Errorf("kafka confirm: unknown receipt token")
		}
		if s.ownership != nil && !s.ownership.isOwned(data.lane) {
			s.mu.Unlock()
			return fmt.Errorf("kafka confirm: lane %q is no longer owned", data.lane)
		}
		tokensByLane[data.lane] = append(tokensByLane[data.lane], token)
		highest[data.lane] = data.record
	}
	for lane, tokens := range tokensByLane {
		ordered := s.laneTokens[lane]
		if len(tokens) > len(ordered) {
			s.mu.Unlock()
			return fmt.Errorf("kafka confirm: lane %q receipt count exceeds outstanding count", lane)
		}
		for index, token := range tokens {
			if ordered[index] != token {
				s.mu.Unlock()
				return fmt.Errorf("kafka confirm: lane %q receipts are not a continuous prefix", lane)
			}
		}
	}
	records := make([]*kgo.Record, 0, len(highest))
	for _, record := range highest {
		records = append(records, record)
	}
	s.mu.Unlock()

	if err := s.client.CommitRecords(ctx, records...); err != nil {
		return fmt.Errorf("kafka commit records: %w", err)
	}

	s.mu.Lock()
	for lane, tokens := range tokensByLane {
		for _, token := range tokens {
			delete(s.receipts, token)
		}
		s.laneTokens[lane] = s.laneTokens[lane][len(tokens):]
		if len(s.laneTokens[lane]) == 0 {
			delete(s.laneTokens, lane)
		}
	}
	idle := len(s.receipts) == 0
	s.mu.Unlock()
	if idle {
		s.client.AllowRebalance()
	}
	return nil
}

// Pause 暂停指定 partition 的后续 fetch。
func (s *Session) Pause(_ context.Context, lane string) error {
	s.mu.Lock()
	partitions := s.laneParts[lane]
	s.mu.Unlock()
	if len(partitions) == 0 {
		return fmt.Errorf("kafka pause: unknown lane %q", lane)
	}
	s.client.PauseFetchPartitions(partitions)
	return nil
}

// Resume 恢复指定 partition 的后续 fetch。
func (s *Session) Resume(_ context.Context, lane string) error {
	s.mu.Lock()
	partitions := s.laneParts[lane]
	s.mu.Unlock()
	if len(partitions) == 0 {
		return fmt.Errorf("kafka resume: unknown lane %q", lane)
	}
	s.client.ResumeFetchPartitions(partitions)
	return nil
}

// PauseFlow 暂停本 Session topic 的数据 fetch；重复调用是幂等的。
func (s *Session) PauseFlow(_ context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return consume.ErrSessionClosed
	}
	if s.flowPaused {
		s.mu.Unlock()
		return nil
	}
	s.flowPaused = true
	s.mu.Unlock()
	s.client.PauseFetchTopics(s.config.Topic)
	return nil
}

// ResumeFlow 恢复本 Session topic 的数据 fetch；lane 级暂停不受影响。
func (s *Session) ResumeFlow(_ context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return consume.ErrSessionClosed
	}
	if !s.flowPaused {
		s.mu.Unlock()
		return nil
	}
	s.flowPaused = false
	s.mu.Unlock()
	s.client.ResumeFetchTopics(s.config.Topic)
	return nil
}

func (s *Session) OwnershipEvents() <-chan consume.OwnershipEvent {
	if s.ownership == nil {
		return nil
	}
	return s.ownership.events
}

func (s *Session) AllowOwnershipChanges() { s.client.AllowRebalance() }

// Close 关闭 consumer group Session，未确认 offset 将由后续 owner 重放。
func (s *Session) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return consume.ErrSessionClosed
	}
	s.closed = true
	s.mu.Unlock()
	s.client.AllowRebalance()

	done := make(chan struct{})
	go func() {
		s.client.Close()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (s *Session) allowRebalanceIfIdle() {
	s.mu.Lock()
	idle := len(s.receipts) == 0
	s.mu.Unlock()
	if idle {
		s.client.AllowRebalance()
	}
}

func (b *ownershipBridge) assigned(ctx context.Context, _ *kgo.Client, partitions map[string][]int32) {
	lanes := partitionLanes(partitions)
	b.mu.Lock()
	b.enforced = true
	for _, lane := range lanes {
		b.owned[lane] = true
	}
	b.mu.Unlock()
	b.send(ctx, consume.OwnershipAssigned, lanes, nil)
}

func (b *ownershipBridge) revoked(ctx context.Context, _ *kgo.Client, partitions map[string][]int32) {
	lanes := partitionLanes(partitions)
	b.send(ctx, consume.OwnershipRevoked, lanes, func() {
		b.mu.Lock()
		for _, lane := range lanes {
			delete(b.owned, lane)
		}
		b.mu.Unlock()
	})
}

func (b *ownershipBridge) lost(ctx context.Context, _ *kgo.Client, partitions map[string][]int32) {
	lanes := partitionLanes(partitions)
	b.mu.Lock()
	b.enforced = true
	for _, lane := range lanes {
		delete(b.owned, lane)
	}
	b.mu.Unlock()
	b.send(ctx, consume.OwnershipLost, lanes, nil)
}

func (b *ownershipBridge) send(ctx context.Context, kind consume.OwnershipEventKind, lanes []string, after func()) {
	done := make(chan struct{})
	var once sync.Once
	complete := func() {
		once.Do(func() {
			if after != nil {
				after()
			}
			close(done)
		})
	}
	event := consume.OwnershipEvent{Kind: kind, Lanes: append([]string(nil), lanes...), Complete: complete}
	select {
	case b.events <- event:
	case <-ctx.Done():
		complete()
		return
	}
	select {
	case <-done:
	case <-ctx.Done():
		complete()
	}
}

func (b *ownershipBridge) isOwned(lane string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return !b.enforced || b.owned[lane]
}

func partitionLanes(partitions map[string][]int32) []string {
	lanes := make([]string, 0)
	for topic, values := range partitions {
		for _, partition := range values {
			lanes = append(lanes, laneName(topic, partition))
		}
	}
	return lanes
}

func (s *Session) receiveState() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, consume.ErrSessionClosed
	}
	return s.flowPaused, nil
}

func laneName(topic string, partition int32) string {
	return topic + "/" + strconv.FormatInt(int64(partition), 10)
}

func valueOrTransportID(record *kgo.Record, name string) string {
	if value := headerValue(record, name); value != "" {
		return value
	}
	return laneName(record.Topic, record.Partition) + "/" + strconv.FormatInt(record.Offset, 10)
}

func headerValue(record *kgo.Record, name string) string {
	for index := len(record.Headers) - 1; index >= 0; index-- {
		if record.Headers[index].Key == name {
			return string(record.Headers[index].Value)
		}
	}
	return ""
}

func recordHeaders(record *kgo.Record) map[string][]byte {
	headers := make(map[string][]byte, len(record.Headers))
	for _, header := range record.Headers {
		headers[header.Key] = append([]byte(nil), header.Value...)
	}
	return headers
}

var _ consume.Session = (*Session)(nil)

var _ consume.LanePauser = (*Session)(nil)

var _ consume.LaneController = (*Session)(nil)

var _ consume.FlowController = (*Session)(nil)

var _ consume.OwnershipSession = (*Session)(nil)
