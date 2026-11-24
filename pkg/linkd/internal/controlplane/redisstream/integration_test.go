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
	"os"
	"strconv"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
)

const redisIntegrationAddressEnv = "LINKD_TEST_REDIS_ADDRESS"

func TestRedisStreamManagerMultiBatchTrimIntegration(t *testing.T) {
	address := os.Getenv(redisIntegrationAddressEnv)
	if address == "" {
		t.Skipf("set %s to run Redis Stream manager integration", redisIntegrationAddressEnv)
	}
	database := 0
	if raw := os.Getenv("LINKD_TEST_REDIS_DATABASE"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("parse LINKD_TEST_REDIS_DATABASE: %v", err)
		}
		database = parsed
	}
	client := redis.NewClient(&redis.Options{
		Addr: address, Username: os.Getenv("LINKD_TEST_REDIS_USERNAME"),
		Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), DB: database,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("connect Redis: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Logf("close Redis client: %v", err)
		}
	})

	key := "linkd:test:stream-manager:" + strconv.Itoa(os.Getpid()) + ":" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Cleanup(func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = client.Del(cleanup, key).Err()
	})
	for range 400 {
		if err := client.XAdd(ctx, &redis.XAddArgs{Stream: key, Values: map[string]any{"signal": "test"}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	for _, group := range []string{"lifecycle", "audit"} {
		if err := client.XGroupCreate(ctx, key, group, "0").Err(); err != nil {
			t.Fatal(err)
		}
		streams, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: group, Consumer: "consumer-1", Streams: []string{key, ">"}, Count: 350,
		}).Result()
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, 350)
		for _, stream := range streams {
			for _, message := range stream.Messages {
				ids = append(ids, message.ID)
			}
		}
		if len(ids) != 350 {
			t.Fatalf("group %q read %d entries, want 350", group, len(ids))
		}
		if acknowledged, err := client.XAck(ctx, key, group, ids...).Result(); err != nil || acknowledged != 350 {
			t.Fatalf("group %q XACK=%d,%v", group, acknowledged, err)
		}
	}

	observer := &recordingObserver{}
	manager, err := NewManager(client, Config{
		Stream: key, ExpectedGroup: "lifecycle", ReconcileInterval: time.Minute,
		OperationTimeout: 5 * time.Second, MaxEntries: 100, TrimBatchSize: 100, MaxTrimEntriesPerCycle: 300,
	}, observer)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	length, err := client.XLen(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	if observer.trimmed <= 100 || observer.trimmed > 300 || length > 100 {
		t.Fatalf("trimmed=%d length=%d", observer.trimmed, length)
	}
	groups, err := client.XInfoGroups(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups=%#v", groups)
	}
	for _, group := range groups {
		if group.Pending != 0 || group.Lag != 50 {
			t.Fatalf("group %q pending=%d lag=%d", group.Name, group.Pending, group.Lag)
		}
	}
}
