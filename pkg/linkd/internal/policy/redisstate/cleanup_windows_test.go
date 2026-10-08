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
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRedisCleanupCapturesAtomicGenerationAndOrphan(t *testing.T) {
	s := redisClipStore(t)
	r := clipFixture()
	r.Threshold = 1
	r.At = time.Now()
	d := mustClip(t, s, r)
	if err := s.BindClipOwner(t.Context(), r, d, "owner"); err != nil {
		t.Fatal(err)
	}
	_, id := s.clipKeys(r)
	removed, err := s.ClearClipIdentity(t.Context(), r.Identity, "owner")
	if err != nil || len(removed) != 1 || removed[0].ID != clipRuntimeID(r, id) || removed[0].Epoch != d.Epoch || removed[0].Missing {
		t.Fatal(removed, err)
	}
	r.EventID = "new-generation"
	d = mustClip(t, s, r)
	keys, _ := s.clipKeys(r)
	if err := s.client.Del(t.Context(), keys[1]).Err(); err != nil {
		t.Fatal(err)
	}
	orphan, err := s.ClearClipIdentity(t.Context(), r.Identity, "")
	if err != nil || len(orphan) != 1 || !orphan[0].Missing || orphan[0].Epoch != "" {
		t.Fatal("unknown epoch fabricated", orphan, err)
	}
	if n, err := s.ClearClipIdentity(t.Context(), r.Identity, ""); err != nil || len(n) != 0 {
		t.Fatal(n, err)
	}
	agg := aggregationFixture()
	agg.At = time.Now()
	owner := claimAggregation(t, s, agg)
	if ok, err := s.CommitAggregationOwner(t.Context(), agg, owner); err != nil || !ok {
		t.Fatal(ok, err)
	}
	windows, err := s.ClearAggregationOwner(t.Context(), agg.TenantID, agg.CandidateAlertID)
	if err != nil || len(windows) != 1 || windows[0].ID != owner.WindowID || windows[0].Epoch != owner.Epoch || windows[0].Missing {
		t.Fatal(windows, err)
	}
}

func TestRedisExactCleanupDoesNotClearReplacement(t *testing.T) {
	for _, kind := range []string{"clip", "aggregation"} {
		t.Run(kind, func(t *testing.T) {
			s := redisClipStore(t)
			tenant, id := "", ""
			if kind == "clip" {
				r := clipFixture()
				r.Threshold = 1
				r.At = time.Now()
				d := mustClip(t, s, r)
				if err := s.BindClipOwner(t.Context(), r, d, "owner"); err != nil {
					t.Fatal(err)
				}
				_, counter := s.clipKeys(r)
				tenant, id = r.TenantID, clipRuntimeID(r, counter)
			} else {
				r := aggregationFixture()
				r.At = time.Now()
				d := claimAggregation(t, s, r)
				if ok, err := s.CommitAggregationOwner(t.Context(), r, d); err != nil || !ok {
					t.Fatal(ok, err)
				}
				tenant, id = r.TenantID, d.WindowID
			}
			old, found, err := s.ReadSuppressionWindow(t.Context(), tenant, kind, id)
			if err != nil || !found {
				t.Fatal(found, err)
			}
			if ok, err := s.DeleteSuppressionWindow(t.Context(), old); err != nil || !ok {
				t.Fatal(ok, err)
			}
			if kind == "clip" {
				r := clipFixture()
				r.Threshold = 1
				r.EventID = "replacement"
				r.At = time.Now()
				d := mustClip(t, s, r)
				if err := s.BindClipOwner(t.Context(), r, d, "replacement-owner"); err != nil {
					t.Fatal(err)
				}
			} else {
				r := aggregationFixture()
				r.EventID = "replacement"
				r.CandidateAlertID = "replacement-owner"
				r.At = time.Now()
				d := claimAggregation(t, s, r)
				if ok, err := s.CommitAggregationOwner(t.Context(), r, d); err != nil || !ok {
					t.Fatal(ok, err)
				}
			}
			if ok, err := s.DeleteSuppressionWindow(t.Context(), old); err != nil || ok {
				t.Fatal("old command deleted replacement", ok, err)
			}
			next, found, err := s.ReadSuppressionWindow(t.Context(), tenant, kind, id)
			if err != nil || !found || next.Epoch == old.Epoch || next.OwnerAlertID != "replacement-owner" {
				t.Fatal(next, found, err)
			}
		})
	}
}

func TestRedisCleanupReturnsBoundedFullPageAndRejectsCorruptReferenceBeforeMutation(t *testing.T) {
	s := redisClipStore(t)
	r := clipFixture()
	r.Threshold = 1
	r.At = time.Now()
	d := mustClip(t, s, r)
	if err := s.BindClipOwner(t.Context(), r, d, "owner"); err != nil {
		t.Fatal(err)
	}
	keys, _ := s.clipKeys(r)
	subject := digest("identity", r.SourceID, r.Fingerprint)
	base := s.base(r.TenantID) + ":clip:" + subject + ":"
	// 全部是本次测试 namespace 内的合成引用，验证反向集合上限，不声称是真实 Event 吞吐。
	for i := 0; i < 511; i++ {
		key := base + fmt.Sprintf("%064x", i)
		if err := s.client.HSet(t.Context(), key+":meta", "scope", subject, "epoch", fmt.Sprintf("e-%d", i), "owner", "owner").Err(); err != nil {
			t.Fatal(err)
		}
		if err := s.client.SAdd(t.Context(), keys[2], key).Err(); err != nil {
			t.Fatal(err)
		}
	}
	refs, err := s.ClearClipIdentity(t.Context(), r.Identity, "owner")
	if err != nil || len(refs) != 512 {
		t.Fatal(len(refs), err)
	}
	for i := 1; i < len(refs); i++ {
		if refs[i].ID <= refs[i-1].ID {
			t.Fatal("unstable detail order")
		}
	}
	r.EventID = "after-capacity"
	d = mustClip(t, s, r)
	if err := s.BindClipOwner(t.Context(), r, d, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.client.SAdd(t.Context(), keys[2], base+strings.Repeat("g", 64)).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClearClipIdentity(t.Context(), r.Identity, "owner"); !errors.Is(err, ErrState) {
		t.Fatal("invalid reference accepted", err)
	}
	if s.client.Exists(t.Context(), keys[0]).Val() != 1 {
		t.Fatal("valid counter deleted before bad reference rejected")
	}
}
