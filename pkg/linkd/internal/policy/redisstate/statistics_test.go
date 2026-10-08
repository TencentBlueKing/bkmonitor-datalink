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
	"sync"
	"testing"
	"time"

	"linkd/internal/policy"
)

func TestRedisPolicyStatisticsBucketsIsolationFailureAndCapacity(t *testing.T) {
	s := redisClipStore(t)
	scope := policy.Scope{TenantID: "tenant", Kind: policy.Suppression}
	at := time.Date(2026, 10, 8, 12, 30, 0, 0, time.UTC)
	for _, sample := range []struct {
		scope      policy.Scope
		id, result string
		at         time.Time
	}{{scope, "p", "matched", at}, {scope, "p", "unavailable", at}, {scope, "p", "execution_skipped", at}, {scope, "p", "not_matched", at.Add(-2 * time.Hour)}, {policy.Scope{TenantID: "other", Kind: policy.Suppression}, "p", "matched", at}, {policy.Scope{TenantID: "tenant", Kind: policy.Merge}, "p", "matched", at}} {
		if err := s.RecordPolicyObservation(t.Context(), sample.scope, sample.id, sample.result, sample.at); err != nil {
			t.Fatal(err)
		}
	}
	q := policy.StatisticsQuery{Scope: scope, IDs: []string{"p", "zero"}, Hours: 1}
	one, err := s.PolicyStatistics(t.Context(), q, at)
	if err != nil {
		t.Fatal(err)
	}
	if one.Items[0] != (policy.PolicyStatistics{ID: "p", Matched: 1, Unavailable: 1, ExecutionSkipped: 1}) || one.Items[1].Matched != 0 || !one.From.Equal(at.Truncate(time.Hour)) {
		t.Fatalf("counts %+v", one)
	}
	q.Hours = 6
	six, err := s.PolicyStatistics(t.Context(), q, at)
	if err != nil || six.Items[0].NotMatched != 1 {
		t.Fatalf("history %+v %v", six, err)
	}
	ttl, err := s.client.TTL(t.Context(), s.statisticsKey(scope, at)).Result()
	if err != nil || ttl <= 24*time.Hour || ttl > 25*time.Hour {
		t.Fatalf("retention %s %v", ttl, err)
	}
	if err := s.client.HSet(t.Context(), s.statisticsKey(scope, at), "p:matched", "bad").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PolicyStatistics(t.Context(), q, at); err == nil {
		t.Fatal("corruption shown as zero")
	}
	q.IDs = []string{"p", "p"}
	if _, err := s.PolicyStatistics(t.Context(), q, at); !errors.Is(err, policy.ErrInvalid) {
		t.Fatal("duplicate allowed")
	}
	q.IDs = []string{"p"}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.PolicyStatistics(ctx, q, at); err == nil {
		t.Fatal("cancel ignored")
	}
	for range cap(s.statisticsSlots) {
		s.statisticsSlots <- struct{}{}
	}
	if _, err := s.PolicyStatistics(t.Context(), q, at); !errors.Is(err, policy.ErrPreviewCapacity) {
		t.Fatal("read budget ignored")
	}
}

func TestRedisPolicyStatisticsConcurrentCountsAndBoundedIdentities(t *testing.T) {
	s := redisClipStore(t)
	scope := policy.Scope{TenantID: "tenant", Kind: policy.Merge}
	at := time.Now()
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if err := s.RecordPolicyObservation(t.Context(), scope, "p", "matched", at); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	page, err := s.PolicyStatistics(t.Context(), policy.StatisticsQuery{Scope: scope, IDs: []string{"p"}, Hours: 1}, at)
	if err != nil || page.Items[0].Matched != 32 {
		t.Fatalf("lost observations %+v %v", page, err)
	}
	fields := map[string]any{}
	for i := range 20480 {
		fields[time.Unix(int64(i), 0).Format(time.RFC3339)] = 1
	}
	if err := s.client.HSet(t.Context(), s.statisticsKey(scope, at), fields).Err(); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPolicyObservation(t.Context(), scope, "new", "matched", at); !errors.Is(err, ErrBudget) {
		t.Fatalf("identity budget %v", err)
	}
	if err := s.RecordPolicyObservation(t.Context(), scope, "p", "matched", at); err != nil {
		t.Fatal("existing identity blocked", err)
	}
}

type stateInitObserver struct{ calls []string }

func (o *stateInitObserver) ObservePolicyStateInit(_ context.Context, scheme, result string) {
	o.calls = append(o.calls, scheme+":"+result)
}

func TestRedisPolicyInitializationDoesNotCountReplays(t *testing.T) {
	s := redisClipStore(t)
	o := &stateInitObserver{}
	s.SetObserver(o)
	r := clipFixture()
	mustClip(t, s, r)
	mustClip(t, s, r)
	keys, _ := s.clipKeys(r)
	if err := s.client.Del(t.Context(), keys[0]).Err(); err != nil {
		t.Fatal(err)
	}
	r.EventID = "second"
	mustClip(t, s, r)
	if len(o.calls) != 2 || o.calls[0] != "clip:created" || o.calls[1] != "clip:repaired" {
		t.Fatalf("initializations %v", o.calls)
	}
}
