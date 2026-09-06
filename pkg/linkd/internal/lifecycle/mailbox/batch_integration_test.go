// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mailbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
)

type integrationMailboxClient struct {
	redisClient
	lose bool
}

func (c *integrationMailboxClient) Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd {
	r := c.redisClient.Eval(ctx, script, keys, args...)
	if c.lose && r.Err() == nil {
		c.lose = false
		return redis.NewCmdResult(nil, errors.New("response lost"))
	}
	return r
}

func TestMailboxBatchRedisIntegration(t *testing.T) {
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
	client := redis.NewClient(&redis.Options{Addr: address, DB: db, Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD")})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, scenario := range []string{"success", "wrong_type", "capacity", "lost_response", "wrong_stream"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			prefix := fmt.Sprintf("linkd-mailbox-test-%d", time.Now().UnixNano())
			wrapped := &integrationMailboxClient{redisClient: client, lose: scenario == "lost_response"}
			s, _ := newStore(wrapped, Config{KeyPrefix: prefix, SignalStream: prefix + ":signals", MaxPendingPerMailbox: 2})
			events := []domain.Event{mailboxEvent("one"), mailboxEvent("two"), mailboxEvent("three")}
			events[1].BKTenantID = "tenant-2"
			events[2].EventSourceID = "source-2"
			keys := []string{s.config.SignalStream}
			for _, e := range events {
				keys = append(keys, s.eventsKey(CorrelationKey(e.BKTenantID, e.EventSourceID, e.Fingerprint)))
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := client.Del(ctx, keys...).Err(); err != nil {
					t.Error(err)
				}
			})
			switch scenario {
			case "wrong_type":
				if err := client.Set(ctx, keys[2], "wrong", 0).Err(); err != nil {
					t.Fatal(err)
				}
			case "capacity":
				if err := client.RPush(ctx, keys[2], "old-1", "old-2").Err(); err != nil {
					t.Fatal(err)
				}
			case "wrong_stream":
				if err := client.Set(ctx, keys[0], "wrong", 0).Err(); err != nil {
					t.Fatal(err)
				}
			}
			results, err := s.EnqueueBatch(ctx, events)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "wrong_type", "capacity":
				if results[0].Err != nil || results[1].Err == nil || results[2].Err == nil {
					t.Fatal(results)
				}
				if n := client.Exists(ctx, keys[3]).Val(); n != 0 {
					t.Fatal("tail executed")
				}
				if n := client.XLen(ctx, keys[0]).Val(); n != 1 {
					t.Fatalf("signals=%d", n)
				}
			case "wrong_stream":
				for _, r := range results {
					if r.Err == nil {
						t.Fatal("unexpected success")
					}
				}
				if n := client.Exists(ctx, keys[1:]...).Val(); n != 0 {
					t.Fatal("created mailbox without signal")
				}
			default:
				if scenario == "lost_response" {
					for _, r := range results {
						if r.Err == nil {
							t.Fatal("unknown result reported success")
						}
					}
					results, err = s.EnqueueBatch(ctx, events)
					if err != nil {
						t.Fatal(err)
					}
				}
				for i, r := range results {
					if r.Err != nil {
						t.Fatal(r.Err)
					}
					list, err := client.LRange(ctx, keys[i+1], 0, -1).Result()
					if err != nil || len(list) == 0 || list[0] != events[i].EventID {
						t.Fatalf("list=%v err=%v", list, err)
					}
					want := int64(1)
					if scenario == "lost_response" {
						want = 2
					}
					if int64(len(list)) != want {
						t.Fatal(list)
					}
				}
				if n := client.XLen(ctx, keys[0]).Val(); n != 3 {
					t.Fatalf("duplicate or missing signal: %d", n)
				}
			}
		})
	}
}
