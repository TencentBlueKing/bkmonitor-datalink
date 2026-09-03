// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redisstream

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/consume"
)

func TestSessionClaimsPendingAndAcknowledgesStreamID(t *testing.T) {
	t.Parallel()

	client := &fakeRedisClient{
		claimed: []redis.XMessage{{
			ID: "1000-0",
			Values: map[string]any{
				"payload":      `{"title":"alert"}`,
				"message_id":   "message-a",
				"bk_tenant_id": "tenant-a",
				"order_key":    "order-a",
			},
		}},
	}
	session := newSession(testRedisConfig(), client)
	deliveries, err := session.Receive(context.Background(), consume.ReceiveLimits{MaxMessages: 1, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("Receive() deliveries = %d, want 1", len(deliveries))
	}
	delivery := deliveries[0]
	if delivery.Message.ID != "message-a" || delivery.Message.TenantID != "tenant-a" || !delivery.Meta.Redeliver {
		t.Fatalf("Receive() delivery = %#v", delivery)
	}
	if err := session.Confirm(context.Background(), []consume.Receipt{delivery.Receipt}); err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if got := client.ackedIDs(); !slices.Equal(got, []string{"1000-0"}) {
		t.Fatalf("XAck ids = %v", got)
	}
}

func TestSessionReadsNewMessagesAfterEmptyClaim(t *testing.T) {
	t.Parallel()

	client := &fakeRedisClient{streams: []redis.XStream{{
		Stream: "raw-events",
		Messages: []redis.XMessage{{
			ID:     "2000-0",
			Values: map[string]any{"payload": "body", "bk_tenant_id": "tenant-a"},
		}},
	}}}
	session := newSession(testRedisConfig(), client)
	deliveries, err := session.Receive(context.Background(), consume.ReceiveLimits{MaxMessages: 1, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].Message.ID != "2000-0" || deliveries[0].Meta.Redeliver {
		t.Fatalf("Receive() deliveries = %#v", deliveries)
	}
}

func TestSessionConfirmDoesNotAcknowledgeNewerSignal(t *testing.T) {
	t.Parallel()

	client := &fakeRedisClient{streams: []redis.XStream{{
		Stream: "raw-events",
		Messages: []redis.XMessage{
			{ID: "1000-0", Values: map[string]any{"payload": "first", "bk_tenant_id": "tenant-a"}},
			{ID: "1001-0", Values: map[string]any{"payload": "second", "bk_tenant_id": "tenant-a"}},
		},
	}}}
	session := newSession(testRedisConfig(), client)
	deliveries, err := session.Receive(context.Background(), consume.ReceiveLimits{MaxMessages: 2, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if len(deliveries) != 2 {
		t.Fatalf("Receive() deliveries = %d, want 2", len(deliveries))
	}
	if err := session.Confirm(context.Background(), []consume.Receipt{deliveries[0].Receipt}); err != nil {
		t.Fatalf("Confirm(first) error = %v", err)
	}
	if got := client.ackedIDs(); !slices.Equal(got, []string{"1000-0"}) {
		t.Fatalf("XAck after first receipt = %v", got)
	}
	if err := session.Confirm(context.Background(), []consume.Receipt{deliveries[1].Receipt}); err != nil {
		t.Fatalf("Confirm(second) error = %v", err)
	}
	if got := client.ackedIDs(); !slices.Equal(got, []string{"1000-0", "1001-0"}) {
		t.Fatalf("XAck after both receipts = %v", got)
	}
}

func TestSessionValidatesClaimAgainstRuntimeBudget(t *testing.T) {
	t.Parallel()

	config := testRedisConfig()
	config.ClaimMinIdle = 2 * time.Second
	session := newSession(config, &fakeRedisClient{})
	runtimeConfig := consume.DefaultConfig()
	runtimeConfig.ProcessTimeout = time.Second
	runtimeConfig.RetryMaxElapsed = 2 * time.Second
	if err := session.ValidateRuntime(runtimeConfig); err == nil {
		t.Fatal("ValidateRuntime() error = nil, want unsafe claim_min_idle")
	}
	config.ClaimMinIdle = 4 * time.Second
	session = newSession(config, &fakeRedisClient{})
	if err := session.ValidateRuntime(runtimeConfig); err != nil {
		t.Fatalf("ValidateRuntime() error = %v", err)
	}
}

func testRedisConfig() Config {
	config := (Config{
		Address:  "redis:6379",
		Stream:   "raw-events",
		Group:    "linkd",
		Consumer: "linkd-1",
	}).WithDefaults()
	config.ClaimMinIdle = 5 * time.Minute
	return config
}

type fakeRedisClient struct {
	mu      sync.Mutex
	claimed []redis.XMessage
	streams []redis.XStream
	acked   []string
	closed  bool
}

func (c *fakeRedisClient) XReadGroup(ctx context.Context, _ *redis.XReadGroupArgs) *redis.XStreamSliceCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	command := redis.NewXStreamSliceCmd(ctx)
	command.SetVal(append([]redis.XStream(nil), c.streams...))
	c.streams = nil
	return command
}

func (c *fakeRedisClient) XAutoClaim(ctx context.Context, _ *redis.XAutoClaimArgs) *redis.XAutoClaimCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	command := redis.NewXAutoClaimCmd(ctx)
	command.SetVal(append([]redis.XMessage(nil), c.claimed...), "0-0")
	c.claimed = nil
	return command
}

func (c *fakeRedisClient) XAck(ctx context.Context, _, _ string, ids ...string) *redis.IntCmd {
	c.mu.Lock()
	c.acked = append(c.acked, ids...)
	c.mu.Unlock()
	command := redis.NewIntCmd(ctx)
	command.SetVal(int64(len(ids)))
	return command
}

func (c *fakeRedisClient) XGroupCreateMkStream(ctx context.Context, _, _, _ string) *redis.StatusCmd {
	command := redis.NewStatusCmd(ctx)
	command.SetErr(errors.New("not implemented"))
	return command
}

func (c *fakeRedisClient) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

func (c *fakeRedisClient) ackedIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.acked...)
}

var _ redisClient = (*fakeRedisClient)(nil)
