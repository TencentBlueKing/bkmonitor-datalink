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
	"slices"
	"testing"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/policy"
)

func TestRedisMergeReadOnlyWindowPages(t *testing.T) {
	s := redisClipStore(t)
	r := mergeFixture()
	ids := []string{}
	for _, id := range []string{"first", "second", "third"} {
		v := r
		v.Member.EventID = id
		v.GroupKey = digest(id)
		w := joinMerge(t, s, v)
		ids = append(ids, w.WindowID)
	}
	slices.Sort(ids)
	page, err := s.ListMergeWindows(t.Context(), r.TenantID, "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != ids[0] || page.Next != ids[0] {
		t.Fatal(page, err)
	}
	// 遗留登记不因查询而被清理，也不能造成空页停滞。
	if err := s.client.Del(t.Context(), s.mergeWindowKey(r.TenantID, ids[1])).Err(); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListMergeWindows(t.Context(), r.TenantID, page.Next, 1)
	if err != nil || len(page.Items) != 0 || page.Next != ids[1] {
		t.Fatal(page, err)
	}
	page, err = s.ListMergeWindows(t.Context(), r.TenantID, page.Next, 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != ids[2] || page.Next != "" {
		t.Fatal(page, err)
	}
	if n, err := s.client.ZCard(t.Context(), s.mergeDueKey(r.TenantID)).Result(); err != nil || n != 3 {
		t.Fatal("query mutated registration", n, err)
	}
	other, err := s.ListMergeWindows(t.Context(), "other", "", 4)
	if err != nil || len(other.Items) != 0 {
		t.Fatal("tenant leak", other, err)
	}
	for _, limit := range []int{0, 17} {
		if _, err := s.ListMergeWindows(t.Context(), r.TenantID, "", limit); !errors.Is(err, policy.ErrInvalid) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.ListMergeWindows(ctx, r.TenantID, "", 1); err == nil {
		t.Fatal("ignored cancellation")
	}
	if err := s.client.Del(t.Context(), s.mergeDueKey(r.TenantID)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := s.client.Set(t.Context(), s.mergeDueKey(r.TenantID), "wrong type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListMergeWindows(t.Context(), r.TenantID, "", 1); !errors.Is(err, ErrState) {
		t.Fatal("corruption accepted", err)
	}
	if err := s.client.Del(t.Context(), s.mergeDueKey(r.TenantID)).Err(); err != nil {
		t.Fatal(err)
	}
	oversized := make([]redis.Z, 4097)
	for i := range oversized {
		oversized[i] = redis.Z{Score: float64(i), Member: fmt.Sprintf("%064d", i)}
	}
	if err := s.client.ZAdd(t.Context(), s.mergeDueKey(r.TenantID), oversized...).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListMergeWindows(t.Context(), r.TenantID, "", 1); !errors.Is(err, ErrState) {
		t.Fatal("over-budget registry silently truncated", err)
	}
}
