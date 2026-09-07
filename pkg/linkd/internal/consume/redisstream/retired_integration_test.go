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
	"testing"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	"linkd/internal/consume"
	"linkd/internal/redisclient"
)

func TestRetiredConsumerClaimIntegration(t *testing.T) {
	address := os.Getenv("LINKD_TEST_DISPATCH_REDIS")
	if address == "" {
		t.Skip("set LINKD_TEST_DISPATCH_REDIS for explicit Redis integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: address})
	defer func() { _ = client.Close() }()
	stream := "linkd:claim-test:" + uuid.NewString()
	defer client.Del(context.Background(), stream)
	if err := client.XGroupCreateMkStream(ctx, stream, "group", "0").Err(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"retired-message", "healthy-message"} {
		if err := client.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"message_id": id, "bk_tenant_id": "tenant", "order_key": id, "payload": "{}"}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	for _, consumer := range []string{"retired", "healthy"} {
		if err := client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "group", Consumer: consumer, Streams: []string{stream, ">"}, Count: 1}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	session, err := NewSession(Config{Connection: redisclient.Options{Address: address}, Stream: stream, Group: "group", Consumer: "replacement", RetiredConsumers: []string{"retired"}, ClaimMinIdle: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close(context.Background()) }()
	deliveries, err := session.Receive(ctx, consume.ReceiveLimits{MaxMessages: 10, MaxBytes: 65536})
	if err != nil || len(deliveries) != 1 || deliveries[0].Message.ID != "retired-message" {
		t.Fatalf("retired recovery = %+v, %v", deliveries, err)
	}
	pending, err := client.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: stream, Group: "group", Start: "-", End: "+", Count: 10, Consumer: "healthy"}).Result()
	if err != nil || len(pending) != 1 {
		t.Fatal("healthy consumer message was stolen", err)
	}
}
