// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package activeindex

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	"linkd/internal/redisclient"
)

func integrationCache(t *testing.T) *RedisCache {
	t.Helper()
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("LINKD_TEST_REDIS_ADDRESS is not set")
	}
	client, err := redisclient.New(redisclient.Options{Address: address, Username: os.Getenv("LINKD_TEST_REDIS_USERNAME"), Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), Database: 8, PoolSize: 4, ContextTimeoutEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	prefix := "linkd-test:index:" + uuid.NewString()
	c := NewRedisCache(client, prefix, 1000, 1<<20)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, pattern := range []string{prefix + "*", MetadataPrefix(prefix) + "*"} {
			var cursor uint64
			for {
				keys, next, err := client.Scan(ctx, cursor, pattern, 100).Result()
				if err != nil {
					t.Error(err)
					break
				}
				if len(keys) > 0 {
					if err := client.Unlink(ctx, keys...).Err(); err != nil {
						t.Error(err)
					}
				}
				cursor = next
				if cursor == 0 {
					break
				}
			}
		}
		_ = client.Close()
	})
	return c
}

func TestRedisProjectionIntegration(t *testing.T) {
	c := integrationCache(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	s := Scope{"tenant", "123"}
	key := s.Key(c.prefix)
	sub := c.client.Subscribe(ctx, c.prefix+":changes")
	defer func() { _ = sub.Close() }()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Enqueue(ctx, s); err != nil {
		t.Fatal(err)
	}
	p, err := c.Pending(ctx, 10)
	if err != nil || len(p) != 1 {
		t.Fatalf("pending=%v %v", p, err)
	}
	lease, err := c.Acquire(ctx, s, time.Minute)
	if err != nil || lease == "" {
		t.Fatal(err)
	}
	if other, err := c.Acquire(ctx, s, time.Minute); err != nil || other != "" {
		t.Fatal("two writers acquired lease")
	}
	if err := c.Publish(ctx, p[0], lease, []string{"fp", "fp"}); err != nil {
		t.Fatal(err)
	}
	// 重复提交模拟“服务端成功但响应丢失”，不产生重复变更通知。
	if err := c.Publish(ctx, p[0], lease, []string{"fp"}); err != nil {
		t.Fatal(err)
	}
	if ttl, err := c.client.TTL(ctx, key).Result(); err != nil || ttl != -1 {
		t.Fatalf("formal set TTL=%v %v", ttl, err)
	}
	if err := c.Enqueue(ctx, s); err != nil {
		t.Fatal(err)
	}
	p, err = c.Pending(ctx, 10)
	if err != nil || len(p) != 1 {
		t.Fatal(err)
	}
	if err := c.Enqueue(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish(ctx, p[0], lease, []string{"fp"}); err != nil {
		t.Fatal(err)
	}
	newPending, err := c.Pending(ctx, 10)
	if err != nil || len(newPending) != 1 || newPending[0].Token == p[0].Token {
		t.Fatal("concurrent hint was lost")
	}
	// metadata 损坏必须在正式集合修改前拒绝。
	status := c.scopeKey(s, "status")
	if err := c.client.Del(ctx, status).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.client.Set(ctx, status, "wrong-type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish(ctx, newPending[0], lease, nil); err == nil {
		t.Fatal("invalid metadata accepted")
	}
	if n, err := c.client.SCard(ctx, key).Result(); err != nil || n != 1 {
		t.Fatal("partial failure erased formal set")
	}
	if err := c.client.Del(ctx, status).Err(); err != nil {
		t.Fatal(err)
	}
	// 模拟失租和新持有者，旧任务不能发布也不能释放继任者。
	if err := c.client.Set(ctx, c.scopeKey(s, "lease"), "replacement", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish(ctx, newPending[0], lease, nil); err == nil {
		t.Fatal("stale writer published")
	}
	if err := c.Release(ctx, s, lease); err != nil {
		t.Fatal(err)
	}
	if got, err := c.client.Get(ctx, c.scopeKey(s, "lease")).Result(); err != nil || got != "replacement" {
		t.Fatal("stale release removed successor")
	}
	if err := c.Publish(ctx, newPending[0], "replacement", nil); err != nil {
		t.Fatal(err)
	}
	if n, err := c.client.Exists(ctx, key).Result(); err != nil || n != 0 {
		t.Fatal("empty snapshot not published")
	}
	if err := c.client.Publish(ctx, c.prefix+":changes", "barrier").Err(); err != nil {
		t.Fatal(err)
	}
	count := 0
	for {
		message, err := sub.ReceiveMessage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if message.Payload == "barrier" {
			break
		}
		count++
		var notice map[string]string
		if err := json.Unmarshal([]byte(message.Payload), &notice); err != nil {
			t.Fatal(err)
		}
		if len(notice) != 2 || notice["bk_tenant_id"] != "tenant" || notice["strategy_id"] != "123" {
			t.Fatalf("notice=%v", notice)
		}
	}
	if count != 2 {
		t.Fatalf("notices=%d", count)
	}
	state, err := c.ReadStatus(ctx, s)
	if err != nil || state.LastSuccess == "" || state.Members != 0 {
		t.Fatalf("status=%v %v", state, err)
	}
}

func TestRedisConcurrentManagersRecoverIntegration(t *testing.T) {
	c := integrationCache(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	s := Scope{"tenant", "123"}
	orphan := Scope{"tenant", "orphan"}
	if err := c.client.SAdd(ctx, orphan.Key(c.prefix), "old").Err(); err != nil {
		t.Fatal(err)
	}
	rows := []Row{activeRow("source-a", "tenant", "123", "fp"), activeRow("source-b", "tenant", "123", "fp")}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			manager := testManager(t, &fakeReader{rows: rows}, c)
			manager.settings.OperationTimeout = time.Second
			if err := manager.Step(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	manager := testManager(t, &fakeReader{rows: rows}, c)
	manager.settings.OperationTimeout = time.Second
	if err := manager.Step(ctx); err != nil {
		t.Fatal(err)
	}
	members, err := c.client.SMembers(ctx, s.Key(c.prefix)).Result()
	if err != nil || !slices.Equal(members, []string{"fp"}) {
		t.Fatalf("members=%v %v", members, err)
	}
	if n, err := c.client.Exists(ctx, orphan.Key(c.prefix)).Result(); err != nil || n != 0 {
		t.Fatal("orphan not repaired")
	}
	// 局部读失败保留成员，后续任务重启且无提示仍能恢复。
	failed := testManager(t, &fakeReader{fail: true}, c)
	failed.settings.OperationTimeout = time.Second
	_ = c.Enqueue(ctx, s)
	_ = failed.Step(ctx)
	if n, err := c.client.SCard(ctx, s.Key(c.prefix)).Result(); err != nil || n != 1 {
		t.Fatal("failed read erased members")
	}
	// 失败已退避；显式让测试提示到期，不等待壁钟。
	if err := c.client.ZAdd(ctx, HintKeys(c.prefix)[0], redis.Z{Score: 0, Member: s.Key(c.prefix)}).Err(); err != nil {
		t.Fatal(err)
	}
	restarted := testManager(t, &fakeReader{}, c)
	restarted.settings.OperationTimeout = time.Second
	if err := restarted.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := c.client.Exists(ctx, s.Key(c.prefix)).Result(); err != nil || n != 0 {
		t.Fatal("restart did not recover")
	}
}

func TestRedisRenamePermissionFailurePreservesSnapshotIntegration(t *testing.T) {
	c := integrationCache(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	s := Scope{"tenant", "123"}
	key := s.Key(c.prefix)
	if err := c.client.SAdd(ctx, key, "old").Err(); err != nil {
		t.Fatal(err)
	}
	user := "linkd-test-" + uuid.NewString()
	password := uuid.NewString()
	if err := c.client.Do(ctx, "ACL", "SETUSER", user, "on", ">"+password, "~*", "&*", "+@all", "-rename").Err(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = c.client.Do(cleanup, "ACL", "DELUSER", user).Err()
	}()
	opts := *c.client.Options()
	opts.Username = user
	opts.Password = password
	client := redis.NewClient(&opts)
	defer func() { _ = client.Close() }()
	limited := NewRedisCache(client, c.prefix, 1000, 1<<20)
	if err := limited.Enqueue(ctx, s); err != nil {
		t.Fatal(err)
	}
	p, err := limited.Pending(ctx, 1)
	if err != nil || len(p) != 1 {
		t.Fatal(err)
	}
	lease, err := limited.Acquire(ctx, s, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := limited.Publish(ctx, p[0], lease, []string{"new"}); err == nil {
		t.Fatal("rename denial ignored")
	}
	values, err := c.client.SMembers(ctx, key).Result()
	if err != nil || !slices.Equal(values, []string{"old"}) {
		t.Fatal("failed rename removed current snapshot")
	}
}
