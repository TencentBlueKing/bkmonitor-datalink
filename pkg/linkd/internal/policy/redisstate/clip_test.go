// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redisstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/policy"
)

func clipFixture() ClipRequest {
	return ClipRequest{Identity: Identity{TenantID: "tenant", SourceID: "source", Fingerprint: "fingerprint"}, PolicyID: "policy", Version: 1, Digest: strings.Repeat("a", 64), EventID: "event-1", At: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), Duration: time.Minute, Threshold: 3}
}

func TestClipRejectsInvalidRequestsBeforeRedis(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", ContextTimeoutEnabled: true})
	defer func() { _ = client.Close() }()
	s, err := New(client, "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*ClipRequest){func(r *ClipRequest) { r.TenantID = "" }, func(r *ClipRequest) { r.SourceID = "" }, func(r *ClipRequest) { r.Fingerprint = "" }, func(r *ClipRequest) { r.EventID = "" }, func(r *ClipRequest) { r.Version = 0 }, func(r *ClipRequest) { r.Digest = "bad" }, func(r *ClipRequest) { r.Duration = 0 }, func(r *ClipRequest) { r.Threshold = 10001 }} {
		request := clipFixture()
		change(&request)
		if _, err := s.Clip(t.Context(), request); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	a, id := s.clipKeys(clipFixture())
	r := clipFixture()
	r.SourceID = "other"
	b, other := s.clipKeys(r)
	if a[0] == b[0] || id == other {
		t.Fatal("source identity collision")
	}
}

func redisClipStore(t *testing.T) *Store {
	t.Helper()
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("set LINKD_TEST_REDIS_ADDRESS")
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), ContextTimeoutEnabled: true, PoolSize: 8, MaxRetries: 0})
	if err := client.Ping(t.Context()).Err(); err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	s, err := New(client, fmt.Sprintf("clip-test-%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var cursor uint64
		for {
			keys, next, err := client.Scan(ctx, cursor, s.namespace+":*", 100).Result()
			if err != nil {
				t.Error(err)
				break
			}
			if len(keys) > 0 {
				if err := client.Del(ctx, keys...).Err(); err != nil {
					t.Error(err)
					break
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
		_ = client.Close()
	})
	return s
}

func mustClip(t *testing.T, s *Store, r ClipRequest) ClipDecision {
	t.Helper()
	d, err := s.Clip(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRedisClipSlidingClosedIntervalAndStableRetry(t *testing.T) {
	for _, scenario := range []struct {
		name            string
		seconds, counts []int
	}{{"sliding", []int{0, 50, 70, 80}, []int{1, 2, 2, 3}}, {"closed boundary", []int{0, 30, 60}, []int{1, 2, 3}}} {
		t.Run(scenario.name, func(t *testing.T) {
			s := redisClipStore(t)
			r := clipFixture()
			base := r.At
			first := ClipDecision{}
			for i, second := range scenario.seconds {
				r.EventID = fmt.Sprintf("event-%d", i)
				r.At = base.Add(time.Duration(second) * time.Second)
				decision := mustClip(t, s, r)
				if decision.Count != scenario.counts[i] || decision.Allowed != (scenario.counts[i] >= 3) {
					t.Fatalf("at=%d %+v", second, decision)
				}
				if i == 0 {
					first = decision
				}
			}
			r.EventID = "event-0"
			r.At = base.Add(90 * time.Second)
			replay := mustClip(t, s, r)
			if !replay.Replayed || replay.Count != first.Count || replay.EvaluatedAtMillis != first.EvaluatedAtMillis {
				t.Fatalf("retry changed first decision %+v", replay)
			}
			r.Threshold = 4
			if _, err := s.Clip(t.Context(), r); !errors.Is(err, policy.ErrConflict) {
				t.Fatalf("operation parameters changed: %v", err)
			}
		})
	}
}

func TestRedisClipConcurrentDistinctAndDuplicateEvents(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(fmt.Sprint(same), func(t *testing.T) {
			s := redisClipStore(t)
			start := make(chan struct{})
			out := make(chan ClipDecision, 32)
			errs := make(chan error, 32)
			var wg sync.WaitGroup
			for i := 0; i < 32; i++ {
				wg.Go(func() {
					r := clipFixture()
					if !same {
						r.EventID = fmt.Sprintf("event-%d", i)
					}
					<-start
					d, err := s.Clip(t.Context(), r)
					if err != nil {
						errs <- err
					} else {
						out <- d
					}
				})
			}
			close(start)
			wg.Wait()
			close(out)
			close(errs)
			for err := range errs {
				t.Error(err)
			}
			allowed := 0
			counts := map[int]int{}
			epoch := ""
			for result := range out {
				if result.Allowed {
					allowed++
				}
				counts[result.Count]++
				if epoch == "" {
					epoch = result.Epoch
				}
				if epoch != result.Epoch {
					t.Fatal("concurrent generation split")
				}
			}
			if same {
				if allowed != 0 || counts[1] != 32 {
					t.Fatalf("duplicates counted allowed=%d counts=%v", allowed, counts)
				}
			} else {
				if allowed != 30 || len(counts) != 32 {
					t.Fatalf("distinct events lost allowed=%d counts=%v", allowed, counts)
				}
			}
		})
	}
}

func TestRedisClipIdentityIsolationAndOwnerSafeTerminalCleanup(t *testing.T) {
	s := redisClipStore(t)
	r := clipFixture()
	r.Threshold = 1
	first := mustClip(t, s, r)
	if err := s.BindClipOwner(t.Context(), r, first, "old-alert"); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"", "other-alert"} {
		if n, err := s.ClearClipIdentity(t.Context(), r.Identity, owner); err != nil || len(n) != 0 {
			t.Fatalf("wrong owner cleared %v %v", n, err)
		}
	}
	other := r
	other.SourceID = "other"
	differentSource := mustClip(t, s, other)
	if differentSource.Count != 1 || differentSource.CounterID == first.CounterID {
		t.Fatal("source scope mixed")
	}
	other.TenantID = "other"
	differentTenant := mustClip(t, s, other)
	if differentTenant.Count != 1 || differentTenant.CounterID != differentSource.CounterID {
		t.Fatal("tenant should scope keys independently from opaque counter ID")
	}
	if n, err := s.ClearClipIdentity(t.Context(), r.Identity, "old-alert"); err != nil || len(n) != 1 {
		t.Fatalf("terminal cleanup %v %v", n, err)
	}
	newer := r
	newer.EventID = "event-next"
	newer.At = r.At.Add(time.Second)
	fresh := mustClip(t, s, newer)
	if fresh.Count != 1 || fresh.Epoch == first.Epoch {
		t.Fatal("new lifecycle reused old counter generation")
	}
	if err := s.BindClipOwner(t.Context(), newer, fresh, "new-alert"); err != nil {
		t.Fatal(err)
	}
	if err := s.BindClipOwner(t.Context(), r, first, "old-alert"); err != nil {
		t.Fatal(err)
	} // 旧代次补绑只允许 no-op。
	if n, err := s.ClearClipIdentity(t.Context(), r.Identity, "old-alert"); err != nil || len(n) != 0 {
		t.Fatal("old closure deleted new generation")
	}
	keys, _ := s.clipKeys(newer)
	owner, err := s.client.HGet(t.Context(), keys[1], "owner").Result()
	if err != nil || owner != "new-alert" {
		t.Fatalf("owner changed %q %v", owner, err)
	}
}

func TestRedisClipPartialLossRecountsAndOrphanTerminalClears(t *testing.T) {
	s := redisClipStore(t)
	r := clipFixture()
	first := mustClip(t, s, r)
	keys, _ := s.clipKeys(r)
	if err := s.client.Del(t.Context(), keys[1]).Err(); err != nil {
		t.Fatal(err)
	}
	r.EventID = "after-loss"
	r.At = r.At.Add(time.Second)
	after := mustClip(t, s, r)
	if after.Count != 1 || after.Epoch == first.Epoch {
		t.Fatalf("partial loss not recounted %+v", after)
	}
	if n, err := s.ClearClipIdentity(t.Context(), r.Identity, ""); err != nil || len(n) != 1 {
		t.Fatalf("no-alert recovery did not clear %v %v", n, err)
	}
	r.EventID = "after-clear"
	again := mustClip(t, s, r)
	if again.Count != 1 {
		t.Fatal("old count survived orphan cleanup")
	}
}

func TestRedisClipInvalidReverseIndexCannotDeleteAnotherIdentity(t *testing.T) {
	s := redisClipStore(t)
	a := clipFixture()
	mustClip(t, s, a)
	b := a
	b.SourceID = "other"
	mustClip(t, s, b)
	keysA, _ := s.clipKeys(a)
	keysB, _ := s.clipKeys(b)
	if err := s.client.SAdd(t.Context(), keysA[2], keysB[0]).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClearClipIdentity(t.Context(), a.Identity, ""); !errors.Is(err, ErrState) {
		t.Fatal("foreign reverse entry accepted")
	}
	if exists, err := s.client.Exists(t.Context(), keysB[0]).Result(); err != nil || exists != 1 {
		t.Fatal("foreign counter removed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Clip(ctx, a); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored %v", err)
	}
}
