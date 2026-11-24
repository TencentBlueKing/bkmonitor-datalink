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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/policy"
)

func aggregationFixture() AggregationRequest {
	c := clipFixture()
	return AggregationRequest{Identity: c.Identity, PolicyID: c.PolicyID, Version: c.Version, Digest: c.Digest, GroupKey: strings.Repeat("b", 64), EventID: c.EventID, CandidateAlertID: "candidate-1", At: c.At, Duration: c.Duration}
}

func claimAggregation(t *testing.T, s *Store, r AggregationRequest) AggregationDecision {
	t.Helper()
	d, err := s.ClaimAggregation(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRedisAggregationClaimCommitAndFirstSuppressionResult(t *testing.T) {
	s := redisClipStore(t)
	r := aggregationFixture()
	first := claimAggregation(t, s, r)
	if first.Role != "candidate" || first.OwnerAlertID != r.CandidateAlertID {
		t.Fatalf("first %+v", first)
	}
	other := r
	other.SourceID = "source-b"
	other.Fingerprint = "another"
	other.EventID = "event-2"
	other.CandidateAlertID = "alert-2"
	other.At = other.At.Add(time.Second)
	pending := claimAggregation(t, s, other)
	if pending.Role != "pending" || pending.OwnerAlertID != first.OwnerAlertID {
		t.Fatal("uncommitted candidate suppressed another event")
	}
	if _, ok, err := s.ConfirmAggregationSuppression(t.Context(), other, pending); err == nil || ok {
		t.Fatal("pending accepted as main")
	}
	if ok, err := s.CommitAggregationOwner(t.Context(), r, first); err != nil || !ok {
		t.Fatal("cannot commit owner", err)
	}
	owner := claimAggregation(t, s, other)
	if owner.Role != "owner" || owner.OwnerSourceID != r.SourceID || owner.OwnerFingerprint != r.Fingerprint {
		t.Fatalf("cross-source owner mismatch %+v", owner)
	}
	confirmed, ok, err := s.ConfirmAggregationSuppression(t.Context(), other, owner)
	if err != nil || !ok || confirmed.Role != "suppressed" || confirmed.Replayed {
		t.Fatalf("confirm %+v %v %v", confirmed, ok, err)
	}
	if removed, err := s.ClearAggregationOwner(t.Context(), r.TenantID, r.CandidateAlertID); err != nil || len(removed) != 1 {
		t.Fatal("cleanup failed", err)
	}
	// 同一操作复用原裁决，即使主 Alert 已终结；不会在失败重试中变成一次新的触发。
	other.At = other.At.Add(10 * time.Minute)
	replay := claimAggregation(t, s, other)
	if !replay.Replayed || replay.Role != "suppressed" || replay.ExpiresAtMillis != first.ExpiresAtMillis {
		t.Fatalf("changed retry %+v", replay)
	}
	other.CandidateAlertID = "changed-candidate"
	if _, err := s.ClaimAggregation(t.Context(), other); !errors.Is(err, policy.ErrConflict) {
		t.Fatal("operation signature changed", err)
	}
}

func TestRedisAggregationConcurrentFirstOwner(t *testing.T) {
	s := redisClipStore(t)
	base := aggregationFixture()
	type result struct {
		r   AggregationRequest
		d   AggregationDecision
		err error
	}
	out := make(chan result, 32)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			r := base
			r.SourceID = fmt.Sprintf("source-%d", i)
			r.EventID = fmt.Sprintf("event-%d", i)
			r.CandidateAlertID = fmt.Sprintf("alert-%d", i)
			d, err := s.ClaimAggregation(t.Context(), r)
			out <- result{r, d, err}
		})
	}
	wg.Wait()
	close(out)
	all := []result{}
	var winner result
	candidates := 0
	for v := range out {
		if v.err != nil {
			t.Fatal(v.err)
		}
		if v.d.Role == "candidate" {
			winner = v
			candidates++
		} else if v.d.Role != "pending" {
			t.Fatalf("uncommitted owner admitted %+v", v.d)
		}
		all = append(all, v)
	}
	if candidates != 1 {
		t.Fatalf("first-owner race produced %d candidates", candidates)
	}
	if ok, err := s.CommitAggregationOwner(t.Context(), winner.r, winner.d); err != nil || !ok {
		t.Fatal(err)
	}
	for _, v := range all {
		if v.r.EventID == winner.r.EventID {
			continue
		}
		d := claimAggregation(t, s, v.r)
		if d.Role != "owner" || d.OwnerAlertID != winner.r.CandidateAlertID {
			t.Fatalf("split owner %+v", d)
		}
		if _, ok, err := s.ConfirmAggregationSuppression(t.Context(), v.r, d); err != nil || !ok {
			t.Fatal("owner changed during confirm", err)
		}
	}
}

func TestRedisAggregationFixedWindowAndOldOwnerCannotClearNew(t *testing.T) {
	s := redisClipStore(t)
	r := aggregationFixture()
	first := claimAggregation(t, s, r)
	if ok, err := s.CommitAggregationOwner(t.Context(), r, first); err != nil || !ok {
		t.Fatal(err)
	}
	other := r
	other.EventID = "at-boundary"
	other.CandidateAlertID = "boundary-alert"
	other.At = r.At.Add(r.Duration)
	d := claimAggregation(t, s, other)
	if d.Role != "owner" {
		t.Fatal("closed end boundary excluded")
	}
	if _, ok, err := s.ConfirmAggregationSuppression(t.Context(), other, d); err != nil || !ok {
		t.Fatal(err)
	}
	other.EventID = "after-boundary"
	other.CandidateAlertID = "new-alert"
	other.At = other.At.Add(time.Millisecond)
	next := claimAggregation(t, s, other)
	if next.Role != "candidate" || next.StartedAtMillis != other.At.UnixMilli() {
		t.Fatal("late member extended old window")
	}
	if ok, err := s.CommitAggregationOwner(t.Context(), other, next); err != nil || !ok {
		t.Fatal(err)
	}
	if ok, err := s.CommitAggregationOwner(t.Context(), r, first); err != nil || ok {
		t.Fatal("old owner reclaimed new epoch", err)
	}
	if ok, err := s.ReleaseAggregation(t.Context(), r.TenantID, first); err != nil || ok {
		t.Fatal("old generation removed new owner", err)
	}
	if count, err := s.ClearAggregationOwner(t.Context(), r.TenantID, r.CandidateAlertID); err != nil || len(count) != 0 {
		t.Fatal("old reverse removed new owner", err)
	}
	outsider := other
	outsider.EventID = "outsider"
	outsider.CandidateAlertID = "outsider-alert"
	if got := claimAggregation(t, s, outsider); got.OwnerAlertID != other.CandidateAlertID || got.Role != "owner" {
		t.Fatal("new owner disappeared")
	}
}

func TestRedisAggregationScopeCancellationAndPartialLoss(t *testing.T) {
	s := redisClipStore(t)
	r := aggregationFixture()
	first := claimAggregation(t, s, r)
	tenant := r
	tenant.TenantID = "other"
	if d := claimAggregation(t, s, tenant); d.Role != "candidate" {
		t.Fatal("tenant leaked")
	}
	group := r
	group.GroupKey = strings.Repeat("c", 64)
	if d := claimAggregation(t, s, group); d.Role != "candidate" {
		t.Fatal("group collision")
	}
	if ok, err := s.ReleaseAggregation(t.Context(), r.TenantID, first); err != nil || !ok {
		t.Fatal("cancel reservation failed", err)
	}
	next := r
	next.EventID = "next"
	next.CandidateAlertID = "next-alert"
	candidate := claimAggregation(t, s, next)
	if candidate.Role != "candidate" {
		t.Fatal("cancel retained reservation")
	}
	// 模拟进程停顿导致占位过期；测试只编辑自己的隔离命名空间。
	keys, _, _ := s.aggregationKeys(next)
	raw, err := s.client.Get(t.Context(), keys[0]).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	record["pending_until"] = 1
	raw, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.client.Set(t.Context(), keys[0], raw, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CommitAggregationOwner(t.Context(), next, candidate); err != nil || ok {
		t.Fatal("expired reservation committed", err)
	}
	third := next
	third.EventID = "third"
	third.CandidateAlertID = "third-alert"
	replacement := claimAggregation(t, s, third)
	if replacement.Role != "candidate" || replacement.Epoch == candidate.Epoch {
		t.Fatal("expired owner not replaced")
	}
	if err := s.client.Del(t.Context(), keys[0]).Err(); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CommitAggregationOwner(t.Context(), third, replacement); err != nil || ok {
		t.Fatal("lost window force-rebuilt", err)
	}
	if d := claimAggregation(t, s, r); d.Role != "candidate" {
		t.Fatal("window loss stopped normal claim")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.ClaimAggregation(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation swallowed", err)
	}
}

func TestRedisAggregationRecheckAndReverseCorruption(t *testing.T) {
	s := redisClipStore(t)
	r := aggregationFixture()
	first := claimAggregation(t, s, r)
	if ok, err := s.CommitAggregationOwner(t.Context(), r, first); err != nil || !ok {
		t.Fatal(err)
	}
	other := r
	other.EventID = "other"
	other.CandidateAlertID = "other-alert"
	seen := claimAggregation(t, s, other)
	if ok, err := s.ReleaseAggregation(t.Context(), r.TenantID, first); err != nil || !ok {
		t.Fatal(err)
	}
	if _, ok, err := s.ConfirmAggregationSuppression(t.Context(), other, seen); err != nil || ok {
		t.Fatal("stale read was accepted", err)
	}
	replacement := claimAggregation(t, s, other)
	keys, _, _ := s.aggregationKeys(other)
	if err := s.client.SAdd(t.Context(), keys[1], "foreign:key").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClearAggregationOwner(t.Context(), other.TenantID, other.CandidateAlertID); !errors.Is(err, ErrState) {
		t.Fatal("corrupt reverse accepted", err)
	}
	if got := claimAggregation(t, s, other); got.Epoch != replacement.Epoch {
		t.Fatal("cleanup partially deleted valid owner")
	}
}
