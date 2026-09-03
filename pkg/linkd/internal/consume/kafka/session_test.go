// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"linkd/internal/consume"
)

func TestSessionReceiveAndConfirmContinuousPrefix(t *testing.T) {
	t.Parallel()

	client := &fakeKafkaClient{fetches: kafkaFetches(
		kafkaRecord(3, 10, "id-10"),
		kafkaRecord(3, 11, "id-11"),
	)}
	session := newSession(testConfig(), client)
	deliveries, err := session.Receive(context.Background(), consume.ReceiveLimits{MaxMessages: 2, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if len(deliveries) != 2 {
		t.Fatalf("Receive() deliveries = %d, want 2", len(deliveries))
	}
	if deliveries[0].Message.ID != "id-10" || deliveries[0].Message.TenantID != "tenant-a" {
		t.Fatalf("Receive() first message = %#v", deliveries[0].Message)
	}
	if deliveries[0].Meta.Lane != "raw-events/3" || deliveries[0].Meta.Position != "10" {
		t.Fatalf("Receive() first meta = %#v", deliveries[0].Meta)
	}

	err = session.Confirm(context.Background(), []consume.Receipt{deliveries[1].Receipt})
	if err == nil {
		t.Fatal("Confirm() accepted a non-prefix receipt")
	}
	if err := session.Confirm(context.Background(), []consume.Receipt{deliveries[0].Receipt}); err != nil {
		t.Fatalf("Confirm(first) error = %v", err)
	}
	if got := client.committedOffsets(); !slices.Equal(got, []int64{10}) {
		t.Fatalf("committed offsets = %v, want [10]", got)
	}
	if client.allowCalls() != 0 {
		t.Fatalf("AllowRebalance() calls = %d before batch completion", client.allowCalls())
	}
	if err := session.Confirm(context.Background(), []consume.Receipt{deliveries[1].Receipt}); err != nil {
		t.Fatalf("Confirm(second) error = %v", err)
	}
	if got := client.committedOffsets(); !slices.Equal(got, []int64{10, 11}) {
		t.Fatalf("committed offsets = %v, want [10 11]", got)
	}
	if client.allowCalls() != 1 {
		t.Fatalf("AllowRebalance() calls = %d, want 1", client.allowCalls())
	}
}

func TestSessionReceiveFallsBackToTransportMessageID(t *testing.T) {
	t.Parallel()

	record := kafkaRecord(0, 42, "")
	record.Headers = nil
	client := &fakeKafkaClient{fetches: kafkaFetches(record)}
	session := newSession(testConfig(), client)
	deliveries, err := session.Receive(context.Background(), consume.ReceiveLimits{MaxMessages: 1, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if deliveries[0].Message.ID != "raw-events/0/42" {
		t.Fatalf("message id = %q", deliveries[0].Message.ID)
	}
}

func TestSessionCommitFailureKeepsReceipts(t *testing.T) {
	t.Parallel()

	client := &fakeKafkaClient{fetches: kafkaFetches(kafkaRecord(0, 1, "id-1")), commitFailures: 1}
	session := newSession(testConfig(), client)
	deliveries, err := session.Receive(context.Background(), consume.ReceiveLimits{MaxMessages: 1, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if err := session.Confirm(context.Background(), []consume.Receipt{deliveries[0].Receipt}); err == nil {
		t.Fatal("Confirm() error = nil, want commit failure")
	}
	if err := session.Confirm(context.Background(), []consume.Receipt{deliveries[0].Receipt}); err != nil {
		t.Fatalf("Confirm() retry error = %v", err)
	}
}

func TestSessionAllowsMultiplePollResultsInFlight(t *testing.T) {
	t.Parallel()

	client := &fakeKafkaClient{fetches: kafkaFetches(kafkaRecord(0, 10, "id-10"))}
	session := newSession(testConfig(), client)
	first, err := session.Receive(context.Background(), consume.ReceiveLimits{MaxMessages: 1, MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	client.fetches = kafkaFetches(kafkaRecord(0, 11, "id-11"))
	client.mu.Unlock()
	second, err := session.Receive(context.Background(), consume.ReceiveLimits{MaxMessages: 1, MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Confirm(context.Background(), []consume.Receipt{first[0].Receipt}); err != nil {
		t.Fatal(err)
	}
	if client.allowCalls() != 0 {
		t.Fatalf("rebalance released with second poll still in flight")
	}
	if err := session.Confirm(context.Background(), []consume.Receipt{second[0].Receipt}); err != nil {
		t.Fatal(err)
	}
	if got := client.committedOffsets(); !slices.Equal(got, []int64{10, 11}) {
		t.Fatalf("committed offsets=%v", got)
	}
}

func TestSessionPausesAndResumesOnePartition(t *testing.T) {
	t.Parallel()

	client := &fakeKafkaClient{fetches: kafkaFetches(kafkaRecord(3, 10, "id-10"))}
	session := newSession(testConfig(), client)
	if _, err := session.Receive(context.Background(), consume.ReceiveLimits{MaxMessages: 1, MaxBytes: 1024}); err != nil {
		t.Fatal(err)
	}
	lane := laneName("raw-events", 3)
	if err := session.Pause(context.Background(), lane); err != nil {
		t.Fatal(err)
	}
	if err := session.Resume(context.Background(), lane); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if !slices.Equal(client.paused, []string{lane}) || !slices.Equal(client.resumed, []string{lane}) {
		t.Fatalf("paused=%v resumed=%v", client.paused, client.resumed)
	}
}

func TestSessionPausesAndResumesWholeFlow(t *testing.T) {
	t.Parallel()

	client := &fakeKafkaClient{}
	session := newSession(testConfig(), client)
	if err := session.PauseFlow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.PauseFlow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Receive(context.Background(), consume.ReceiveLimits{MaxMessages: 1, MaxBytes: 1024}); err != nil {
		t.Fatal(err)
	}
	if err := session.ResumeFlow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.ResumeFlow(context.Background()); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if !slices.Equal(client.pausedTopics, []string{"raw-events"}) ||
		!slices.Equal(client.resumedTopics, []string{"raw-events"}) || client.polls != 1 {
		t.Fatalf("paused=%v resumed=%v polls=%d", client.pausedTopics, client.resumedTopics, client.polls)
	}
}

func TestOwnershipBridgeFencesRevokedAndLostLanes(t *testing.T) {
	t.Parallel()

	bridge := newOwnershipBridge()
	lane := laneName("raw-events", 3)
	bridge.mu.Lock()
	bridge.enforced = true
	bridge.owned[lane] = true
	bridge.mu.Unlock()

	revokedDone := make(chan struct{})
	go func() {
		bridge.revoked(context.Background(), nil, map[string][]int32{"raw-events": {3}})
		close(revokedDone)
	}()
	revoked := <-bridge.events
	if revoked.Kind != consume.OwnershipRevoked || !bridge.isOwned(lane) {
		t.Fatalf("revoked event=%+v owned=%v", revoked, bridge.isOwned(lane))
	}
	revoked.Complete()
	select {
	case <-revokedDone:
	case <-time.After(time.Second):
		t.Fatal("revoked callback did not finish")
	}
	if bridge.isOwned(lane) {
		t.Fatal("revoked lane remains owned after drain completion")
	}

	bridge.mu.Lock()
	bridge.owned[lane] = true
	bridge.mu.Unlock()
	lostDone := make(chan struct{})
	go func() {
		bridge.lost(context.Background(), nil, map[string][]int32{"raw-events": {3}})
		close(lostDone)
	}()
	lost := <-bridge.events
	if lost.Kind != consume.OwnershipLost || bridge.isOwned(lane) {
		t.Fatalf("lost event=%+v owned=%v", lost, bridge.isOwned(lane))
	}
	lost.Complete()
	select {
	case <-lostDone:
	case <-time.After(time.Second):
		t.Fatal("lost callback did not finish")
	}
}

func testConfig() Config {
	return (Config{
		Brokers:       []string{"kafka:9092"},
		Topic:         "raw-events",
		ConsumerGroup: "linkd",
	}).WithDefaults()
}

func kafkaRecord(partition int32, offset int64, messageID string) *kgo.Record {
	headers := []kgo.RecordHeader{
		{Key: "bk_tenant_id", Value: []byte("tenant-a")},
		{Key: "order_key", Value: []byte("order-a")},
	}
	if messageID != "" {
		headers = append(headers, kgo.RecordHeader{Key: "message_id", Value: []byte(messageID)})
	}
	return &kgo.Record{
		Topic:     "raw-events",
		Partition: partition,
		Offset:    offset,
		Value:     []byte("payload"),
		Headers:   headers,
		Timestamp: time.Unix(100, 0),
	}
}

func kafkaFetches(records ...*kgo.Record) kgo.Fetches {
	partitions := make(map[int32][]*kgo.Record)
	for _, record := range records {
		partitions[record.Partition] = append(partitions[record.Partition], record)
	}
	fetchPartitions := make([]kgo.FetchPartition, 0, len(partitions))
	for partition, partitionRecords := range partitions {
		fetchPartitions = append(fetchPartitions, kgo.FetchPartition{Partition: partition, Records: partitionRecords})
	}
	return kgo.Fetches{{Topics: []kgo.FetchTopic{{Topic: "raw-events", Partitions: fetchPartitions}}}}
}

type fakeKafkaClient struct {
	mu             sync.Mutex
	fetches        kgo.Fetches
	commits        []int64
	allowed        int
	commitFailures int
	paused         []string
	resumed        []string
	pausedTopics   []string
	resumedTopics  []string
	polls          int
	closed         bool
}

func (c *fakeKafkaClient) PollRecords(_ context.Context, _ int) kgo.Fetches {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.polls++
	fetches := c.fetches
	c.fetches = nil
	return fetches
}

func (c *fakeKafkaClient) CommitRecords(_ context.Context, records ...*kgo.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.commitFailures > 0 {
		c.commitFailures--
		return errors.New("commit failed")
	}
	for _, record := range records {
		c.commits = append(c.commits, record.Offset)
	}
	return nil
}

func (c *fakeKafkaClient) AllowRebalance() {
	c.mu.Lock()
	c.allowed++
	c.mu.Unlock()
}

func (c *fakeKafkaClient) PauseFetchPartitions(partitions map[string][]int32) map[string][]int32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	for topic, values := range partitions {
		for _, partition := range values {
			c.paused = append(c.paused, laneName(topic, partition))
		}
	}
	return nil
}

func (c *fakeKafkaClient) ResumeFetchPartitions(partitions map[string][]int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for topic, values := range partitions {
		for _, partition := range values {
			c.resumed = append(c.resumed, laneName(topic, partition))
		}
	}
}

func (c *fakeKafkaClient) PauseFetchTopics(topics ...string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pausedTopics = append(c.pausedTopics, topics...)
	return append([]string(nil), c.pausedTopics...)
}

func (c *fakeKafkaClient) ResumeFetchTopics(topics ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resumedTopics = append(c.resumedTopics, topics...)
}

func (c *fakeKafkaClient) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
}

func (c *fakeKafkaClient) committedOffsets() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int64(nil), c.commits...)
}

func (c *fakeKafkaClient) allowCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.allowed
}

var _ kafkaClient = (*fakeKafkaClient)(nil)
