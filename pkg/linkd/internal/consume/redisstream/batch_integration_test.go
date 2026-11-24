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
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/consume"
)

type lostAckResponse struct {
	redisClient
	lost bool
}

func (c *lostAckResponse) XAck(ctx context.Context, stream, group string, ids ...string) *redis.IntCmd {
	result := c.redisClient.XAck(ctx, stream, group, ids...)
	if !c.lost && result.Err() == nil {
		c.lost = true
		return redis.NewIntResult(0, errors.New("simulated lost acknowledgment response"))
	}
	return result
}

func TestRedisBatchConfirmIntegration(t *testing.T) {
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("LINKD_TEST_REDIS_ADDRESS is not set")
	}
	db := 0
	if raw := os.Getenv("LINKD_TEST_REDIS_DATABASE"); raw != "" {
		var err error
		db, err = strconv.Atoi(raw)
		if err != nil {
			t.Fatal(err)
		}
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), Username: os.Getenv("LINKD_TEST_REDIS_USERNAME"), DB: db})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cfg := testRedisConfig()
	cfg.Stream = fmt.Sprintf("linkd-test-ack-%d", time.Now().UnixNano())
	cfg.Group = "primary"
	t.Cleanup(func() {
		if err := client.Del(context.Background(), cfg.Stream).Err(); err != nil {
			t.Error(err)
		}
	})
	for _, group := range []string{cfg.Group, "other"} {
		if err := client.XGroupCreateMkStream(ctx, cfg.Stream, group, "0").Err(); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 3 {
		if err := client.XAdd(ctx, &redis.XAddArgs{Stream: cfg.Stream, Values: map[string]any{"payload": "body", "bk_tenant_id": "test", "message_id": fmt.Sprint(i)}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "other", Consumer: "other", Streams: []string{cfg.Stream, ">"}, Count: 3}).Err(); err != nil {
		t.Fatal(err)
	}
	s := newSession(cfg, &lostAckResponse{redisClient: client})
	deliveries, err := s.Receive(ctx, consume.ReceiveLimits{MaxMessages: 3, MaxBytes: 4096})
	if err != nil || len(deliveries) != 3 {
		t.Fatalf("read count=%d error=%v", len(deliveries), err)
	}
	if !s.Capabilities().BatchIndividualConfirm {
		t.Fatal("missing batch capability")
	}
	receipts := []consume.Receipt{deliveries[2].Receipt, deliveries[0].Receipt}
	if err := s.Confirm(ctx, receipts); err == nil {
		t.Fatal("expected lost response")
	}
	if len(s.receipts) != 3 {
		t.Fatal("released receipts after unknown result")
	}
	if err := s.Confirm(ctx, receipts); err != nil {
		t.Fatalf("retry already acknowledged IDs: %v", err)
	}
	pending, err := client.XPending(ctx, cfg.Stream, cfg.Group).Result()
	if err != nil || pending.Count != 1 {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	other, err := client.XPending(ctx, cfg.Stream, "other").Result()
	if err != nil || other.Count != 3 {
		t.Fatalf("other group changed: %v %v", other, err)
	}
	if err := s.Confirm(ctx, []consume.Receipt{deliveries[1].Receipt}); err != nil {
		t.Fatal(err)
	}
	if len(s.receipts) != 0 {
		t.Fatal("receipts remain")
	}
}
