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
	"slices"
	"strings"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/policy"
)

func TestRedisSuppressionClipQueryDoesNotMutateAndTerminalClearRemovesIndex(t *testing.T) {
	s := redisClipStore(t)
	r := clipFixture()
	r.At = time.Now().Add(-time.Second)
	for i := 0; i < 3; i++ {
		r.EventID = fmt.Sprintf("event-%d", i)
		mustClip(t, s, r)
	}
	d := mustClip(t, s, r)
	if err := s.BindClipOwner(t.Context(), r, d, "owner"); err != nil {
		t.Fatal(err)
	}
	keys, id := s.clipKeys(r)
	runtimeID := clipRuntimeID(r, id)
	before := suppressionDump(t, s, keys)
	page, err := s.ListSuppressionWindows(t.Context(), r.TenantID, "clip", "", 1)
	if err != nil || len(page.Items) != 1 || page.Next != "" {
		t.Fatal(page, err)
	}
	v := page.Items[0]
	if v.ID != runtimeID || v.Policy.ID != r.PolicyID || v.SourceID != r.SourceID || v.Fingerprint != r.Fingerprint || v.Count == nil || *v.Count != 3 || v.Threshold != 3 || v.MemberCount != 3 || v.OwnerAlertID != "owner" {
		t.Fatalf("incorrect current view: %+v", v)
	}
	first, err := s.ListSuppressionMembers(t.Context(), r.TenantID, "clip", v.ID, v.Epoch, "", 2)
	if err != nil || len(first.Items) != 2 || first.Next == "" {
		t.Fatal(first, err)
	}
	last, err := s.ListSuppressionMembers(t.Context(), r.TenantID, "clip", v.ID, v.Epoch, first.Next, 2)
	if err != nil || len(last.Items) != 1 || last.Next != "" || first.Items[0].SourceID != r.SourceID || first.Items[0].EventID == last.Items[0].EventID {
		t.Fatal(last, err)
	}
	assertSuppressionUnchanged(t, s, keys, before)
	other, err := s.ListSuppressionWindows(t.Context(), "other", "clip", "", 1)
	if err != nil || len(other.Items) != 0 {
		t.Fatal("tenant leaked", err)
	}
	if _, found, err := s.ReadSuppressionWindow(t.Context(), "other", "clip", v.ID); err != nil || found {
		t.Fatal("cross tenant detail", err)
	}
	if n, err := s.ClearClipIdentity(t.Context(), r.Identity, "owner"); err != nil || len(n) != 1 {
		t.Fatal(n, err)
	}
	page, err = s.ListSuppressionWindows(t.Context(), r.TenantID, "clip", "", 1)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("closed owner still listed", err)
	}
	if _, found, err := s.ReadSuppressionWindow(t.Context(), r.TenantID, "clip", v.ID); err != nil || found {
		t.Fatal("clear retained counter", err)
	}
	if replay := mustClip(t, s, r); !replay.Replayed || replay.Count != 3 {
		t.Fatal("query or cleanup changed frozen result")
	}
	page, err = s.ListSuppressionWindows(t.Context(), r.TenantID, "clip", "", 1)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("replay rebuilt a cleared live counter", err)
	}
}

type suppressionCopy struct {
	value string
	ttl   time.Duration
}

func suppressionDump(t *testing.T, s *Store, keys []string) map[string]suppressionCopy {
	t.Helper()
	out := map[string]suppressionCopy{}
	for _, key := range keys {
		kind, err := s.client.Type(t.Context(), key).Result()
		if err != nil {
			t.Fatal(err)
		}
		var data any
		switch kind {
		case "none":
			continue
		case "string":
			data, err = s.client.Get(t.Context(), key).Result()
		case "hash":
			data, err = s.client.HGetAll(t.Context(), key).Result()
		case "zset":
			data, err = s.client.ZRangeWithScores(t.Context(), key, 0, -1).Result()
		case "set":
			var values []string
			values, err = s.client.SMembers(t.Context(), key).Result()
			slices.Sort(values)
			data = values
		default:
			t.Fatalf("unexpected fixture type %s", kind)
		}
		if err != nil {
			t.Fatal(err)
		}
		// Redis 只读操作也可能推进内部 hash rehash；比较业务字段而非 DUMP 的物理编码顺序。
		value, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		ttl, err := s.client.PTTL(t.Context(), key).Result()
		if err != nil {
			t.Fatal(err)
		}
		out[key] = suppressionCopy{string(value), ttl}
	}
	return out
}

func assertSuppressionUnchanged(t *testing.T, s *Store, keys []string, before map[string]suppressionCopy) {
	t.Helper()
	after := suppressionDump(t, s, keys)
	if len(after) != len(before) {
		t.Fatal("query changed key count")
	}
	for key, v := range before {
		if after[key].value != v.value || after[key].ttl > v.ttl {
			t.Fatal("query changed value or extended TTL", key)
		}
	}
}

func TestRedisSuppressionQueriesKeepZeroAndPartialStateDistinct(t *testing.T) {
	s := redisClipStore(t)
	r := clipFixture()
	r.At = time.Now().Add(-2 * time.Minute)
	d := mustClip(t, s, r)
	keys, _ := s.clipKeys(r)
	id := clipRuntimeID(r, d.CounterID)
	v, found, err := s.ReadSuppressionWindow(t.Context(), r.TenantID, "clip", id)
	if err != nil || !found || v.Count == nil || *v.Count != 0 || v.MemberCount != 1 {
		t.Fatal("expired observations are not zero", v, err)
	}
	if err := s.client.Del(t.Context(), keys[0]).Err(); err != nil {
		t.Fatal(err)
	}
	before := suppressionDump(t, s, keys)
	if _, found, err := s.ReadSuppressionWindow(t.Context(), r.TenantID, "clip", id); !errors.Is(err, ErrState) || found {
		t.Fatal("partial state reported absent/zero", err)
	}
	assertSuppressionUnchanged(t, s, keys, before)
}

func TestRedisSuppressionAggregationMembersStayInTheirGeneration(t *testing.T) {
	s := redisClipStore(t)
	r := aggregationFixture()
	r.At = time.Now().Add(-time.Second)
	d := claimAggregation(t, s, r)
	keys, id, _ := s.aggregationKeys(r)
	pending, found, err := s.ReadSuppressionWindow(t.Context(), r.TenantID, "aggregation", id)
	if err != nil || !found || pending.State != "pending" || pending.Policy.ID != r.PolicyID || pending.MemberCount != 1 {
		t.Fatal(pending, err)
	}
	if ok, err := s.CommitAggregationOwner(t.Context(), r, d); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for i := 1; i < 4; i++ {
		x := r
		x.EventID = fmt.Sprintf("event-%d", i+1)
		x.SourceID = "other-source"
		x.CandidateAlertID = fmt.Sprintf("alert-%d", i)
		x.Fingerprint = fmt.Sprint(i)
		x.At = x.At.Add(time.Duration(i) * time.Millisecond)
		owner := claimAggregation(t, s, x)
		if _, ok, err := s.ConfirmAggregationSuppression(t.Context(), x, owner); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	before := suppressionDump(t, s, keys)
	v, found, err := s.ReadSuppressionWindow(t.Context(), r.TenantID, "aggregation", id)
	if err != nil || !found || v.State != "admitted" || v.MemberCount != 4 || v.OwnerSourceID != r.SourceID || v.GroupKey != r.GroupKey {
		t.Fatal(v, err)
	}
	page, err := s.ListSuppressionMembers(t.Context(), r.TenantID, "aggregation", id, v.Epoch, "", 2)
	if err != nil || len(page.Items) != 2 || page.Items[0].EventID != r.EventID || page.Items[1].SourceID != "other-source" {
		t.Fatal(page, err)
	}
	assertSuppressionUnchanged(t, s, keys, before)
	if _, err := s.ClearAggregationOwner(t.Context(), r.TenantID, r.CandidateAlertID); err != nil {
		t.Fatal(err)
	}
	listed, err := s.ListSuppressionWindows(t.Context(), r.TenantID, "aggregation", "", 4)
	if err != nil || len(listed.Items) != 0 {
		t.Fatal("terminal owner remains listed", err)
	}
	if n, err := s.client.Exists(t.Context(), keys[4]).Result(); err != nil || n != 0 {
		t.Fatal("terminal owner left members", err)
	}
	r.EventID = "new-owner"
	r.CandidateAlertID = "new-alert"
	next := claimAggregation(t, s, r)
	if _, err := s.ListSuppressionMembers(t.Context(), r.TenantID, "aggregation", id, v.Epoch, page.Next, 2); !errors.Is(err, policy.ErrConflict) {
		t.Fatal("old cursor used new generation", err)
	}
	if removed, err := s.ReleaseAggregation(t.Context(), r.TenantID, d); err != nil || removed {
		t.Fatal("old owner removed new window", err)
	}
	newPage, err := s.ListSuppressionMembers(t.Context(), r.TenantID, "aggregation", id, next.Epoch, "", 16)
	if err != nil || len(newPage.Items) != 1 || newPage.Items[0].EventID != "new-owner" {
		t.Fatal("old members mixed into new owner", newPage, err)
	}
}

func TestRedisSuppressionIndexPagesAndBudget(t *testing.T) {
	s := redisClipStore(t)
	r := clipFixture()
	r.At = time.Now().Add(-time.Second)
	d := mustClip(t, s, r)
	index := s.suppressionIndex(r.TenantID, "clip")
	for start := 0; start < 65535; start += 256 {
		entries := []redis.Z{}
		for i := start; i < min(start+256, 65535); i++ {
			entries = append(entries, redis.Z{Score: float64(time.Now().Add(time.Hour).UnixMilli()), Member: strings.Repeat("f", 64) + ":" + fmt.Sprintf("%064x", i)})
		}
		if err := s.client.ZAdd(t.Context(), index, entries...).Err(); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ListSuppressionWindows(t.Context(), r.TenantID, "clip", "", 4)
	if err != nil || len(page.Items) != 1 || page.Next == "" {
		t.Fatal(page, err)
	}
	second, err := s.ListSuppressionWindows(t.Context(), r.TenantID, "clip", page.Next, 4)
	if err != nil || len(second.Items) != 0 || second.Next == "" || second.Next == page.Next {
		t.Fatal("empty stale page lost cursor", second, err)
	}
	x := r
	x.Fingerprint = "new-subject"
	x.EventID = "new-event"
	if _, err := s.Clip(t.Context(), x); !errors.Is(err, ErrBudget) {
		t.Fatal("unbounded runtime registry", err)
	}
	keys, _ := s.clipKeys(x)
	if exists, err := s.client.Exists(t.Context(), keys[0], keys[1], keys[3]).Result(); err != nil || exists != 0 {
		t.Fatal("budget failure partially counted", err)
	}
	r.EventID = "next-event"
	if next := mustClip(t, s, r); next.Count != 2 || next.CounterID != d.CounterID {
		t.Fatal("full registry blocked existing counter")
	}
	if err := s.client.ZRem(t.Context(), index, page.Next).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListSuppressionWindows(t.Context(), r.TenantID, "clip", page.Next, 4); !errors.Is(err, policy.ErrConflict) {
		t.Fatal("lost cursor silently restarted", err)
	}
}

func TestRedisSuppressionQueryRejectsCorruptScopeWithoutRepair(t *testing.T) {
	s := redisClipStore(t)
	r := clipFixture()
	d := mustClip(t, s, r)
	keys, _ := s.clipKeys(r)
	var descriptor suppressionDescriptor
	if json.Unmarshal([]byte(r.descriptor()), &descriptor) != nil {
		t.Fatal("fixture")
	}
	descriptor.SourceID = "foreign-source"
	raw, _ := json.Marshal(descriptor)
	if err := s.client.HSet(t.Context(), keys[1], "descriptor", string(raw)).Err(); err != nil {
		t.Fatal(err)
	}
	before := suppressionDump(t, s, keys)
	if _, found, err := s.ReadSuppressionWindow(t.Context(), r.TenantID, "clip", clipRuntimeID(r, d.CounterID)); !errors.Is(err, ErrState) || found {
		t.Fatal("foreign descriptor returned", err)
	}
	assertSuppressionUnchanged(t, s, keys, before)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.ListSuppressionWindows(ctx, r.TenantID, "clip", "", 1); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
	for _, q := range [][3]string{{"", "clip", clipRuntimeID(r, d.CounterID)}, {r.TenantID, "other", d.CounterID}, {r.TenantID, "clip", "bad"}} {
		if _, _, err := s.ReadSuppressionWindow(t.Context(), q[0], q[1], q[2]); !errors.Is(err, policy.ErrInvalid) {
			t.Fatal("invalid query", q, err)
		}
	}
}

func TestRedisAggregationMemberBudgetKeepsFrozenResultsAndFixedRetention(t *testing.T) {
	s := redisClipStore(t)
	r := aggregationFixture()
	r.At = time.Now().Add(-time.Second)
	d := claimAggregation(t, s, r)
	if ok, err := s.CommitAggregationOwner(t.Context(), r, d); err != nil || !ok {
		t.Fatal(ok, err)
	}
	keys, id, _ := s.aggregationKeys(r)
	x := r
	x.EventID = "confirmed"
	x.CandidateAlertID = "other-alert"
	x.SourceID = "other-source"
	owner := claimAggregation(t, s, x)
	if _, ok, err := s.ConfirmAggregationSuppression(t.Context(), x, owner); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for start := 0; start < 9998; start += 256 {
		members := []redis.Z{}
		for i := start; i < min(start+256, 9998); i++ {
			m := x
			m.EventID = fmt.Sprintf("member-%d", i)
			members = append(members, redis.Z{Score: float64(m.At.UnixMilli()), Member: m.member()})
		}
		if err := s.client.ZAdd(t.Context(), keys[4], members...).Err(); err != nil {
			t.Fatal(err)
		}
	}
	before := suppressionDump(t, s, keys)
	if d, ok, err := s.ConfirmAggregationSuppression(t.Context(), x, owner); err != nil || !ok || !d.Replayed {
		t.Fatal("full member set lost frozen retry", err)
	}
	x.EventID = "overflow"
	fresh := claimAggregation(t, s, x)
	if _, ok, err := s.ConfirmAggregationSuppression(t.Context(), x, fresh); !errors.Is(err, ErrBudget) || ok {
		t.Fatal("member set exceeded 10000", err)
	}
	overflowKeys, _, _ := s.aggregationKeys(x)
	if n, err := s.client.Exists(t.Context(), overflowKeys[2]).Result(); err != nil || n != 0 {
		t.Fatal("overflow cached a successful decision", err)
	}
	page, err := s.ListSuppressionMembers(t.Context(), r.TenantID, "aggregation", id, d.Epoch, "", 16)
	if err != nil || len(page.Items) != 16 || page.Next != "16" {
		t.Fatal("bounded page failed at member limit", err)
	}
	assertSuppressionUnchanged(t, s, keys, before)
	if count, err := s.client.ZCard(t.Context(), keys[4]).Result(); err != nil || count != 10000 {
		t.Fatal("overflow changed members", err)
	}
}
