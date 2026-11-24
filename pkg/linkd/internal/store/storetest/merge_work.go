// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storetest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func runMergeWorkContract(t *testing.T, factory Factory) {
	t.Run("merge work pages within one alert and across tenants after mutation", func(t *testing.T) {
		repo := factory(t)
		work, ok := repo.(store.MergeWorkStore)
		if !ok {
			t.Fatal("repository must implement merge work")
		}
		create := func(tenant string, n int) store.StoredAlert {
			t.Helper()
			a := Alert(tenant, "shared-alert", "opening", "fp", "warning")
			a.Merge = &domain.AlertMerge{Role: "original", State: "pending"}
			for i := n; i > 0; i-- {
				a.Merge.Pending = append(a.Merge.Pending, domain.MergeWait{WindowID: fmt.Sprintf("%064x", i), Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("b", 64)}, GroupKey: strings.Repeat("c", 64), MemberEventID: a.TriggerEventID, Severity: a.Severity, Groups: []int{0}, StartedAt: a.UpdateAt, Deadline: a.UpdateAt.Add(time.Minute)})
			}
			result, err := repo.CreateAlert(t.Context(), a)
			if err != nil {
				t.Fatal(err)
			}
			return result.StoredAlert
		}
		first := create("tenant-a", 32)
		create("tenant-b", 1)
		page, err := work.ListMergeWork(t.Context(), store.MergeWorkCursor{}, 1)
		if err != nil || len(page.Items) != 1 || page.Items[0].WindowID != fmt.Sprintf("%064x", 1) || page.Next.TenantID != "tenant-a" {
			t.Fatal("first window missing", page, err)
		}
		// 处理第一页后删除该窗口，后续游标仍需留在同一 Alert，不能漏过其余 31 个窗口。
		next := first.Alert.Clone()
		next.Merge, _ = next.Merge.ReleaseWindow(page.Items[0].WindowID)
		next.UpdateAt = next.UpdateAt.Add(time.Second)
		if _, err := repo.CompareAndSetAlert(t.Context(), next.BKTenantID, next.AlertID, first.Version, next); err != nil {
			t.Fatal(err)
		}
		after := page.Next
		got := []string{}
		for range 40 {
			page, err = work.ListMergeWork(t.Context(), after, 1)
			if err != nil || len(page.Items) > 1 {
				t.Fatal(err)
			}
			if page.Next.TenantID != "" && page.Next.Compare(after) <= 0 {
				t.Fatal("cursor failed to advance")
			}
			for _, item := range page.Items {
				got = append(got, item.Alert.Alert.BKTenantID+":"+item.WindowID)
			}
			after = page.Next
			if after.TenantID == "" {
				break
			}
		}
		want := []string{}
		for i := 2; i <= 32; i++ {
			want = append(want, "tenant-a:"+fmt.Sprintf("%064x", i))
		}
		want = append(want, "tenant-b:"+fmt.Sprintf("%064x", 1))
		if after.TenantID != "" || !reflect.DeepEqual(got, want) {
			t.Fatalf("lost or repeated windows: %v", got)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := work.ListMergeWork(ctx, store.MergeWorkCursor{}, 1); !errors.Is(err, context.Canceled) {
			t.Fatal("cancel ignored", err)
		}
		for _, cursor := range []store.MergeWorkCursor{{AlertID: "orphan"}, {TenantID: "tenant-a", AlertID: "shared-alert", WindowID: "invalid"}} {
			if _, err := work.ListMergeWork(t.Context(), cursor, 1); err == nil {
				t.Fatal("invalid cursor accepted")
			}
		}
	})
	t.Run("terminal merge intent remains work until bookkeeping completes", func(t *testing.T) {
		repo := factory(t)
		work := repo.(store.MergeWorkStore)
		a := Alert("tenant", "parent", "opening", "fp", "warning")
		relation := strings.Repeat("a", 64)
		a.EventSourceID = domain.BuiltinMergeEventSourceID
		a.Merge = &domain.AlertMerge{Role: "aggregate", State: "none", OperationID: relation}
		created, err := repo.CreateAlert(t.Context(), a)
		if err != nil {
			t.Fatal(err)
		}
		next := a.Clone()
		next.UpdateAt = next.UpdateAt.Add(time.Second)
		next.Status = domain.AlertStatusRecovered
		next.EndAt = &next.UpdateAt
		next.EndType = domain.AlertEndTypeSystem
		next.EndReason = "merge_members_ended"
		next.MergeChange = &domain.AlertMergeChange{Kind: "parent_recover", RelationID: relation, WindowID: strings.Repeat("b", 64), OperationID: "recover", EffectiveAt: next.UpdateAt, Before: a.Merge.Clone(), After: next.Merge.Clone()}
		saved, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, created.Version, next)
		if err != nil {
			t.Fatal(err)
		}
		page, err := work.ListMergeWork(t.Context(), store.MergeWorkCursor{}, 1)
		if err != nil || len(page.Items) != 1 || page.Items[0].WindowID != "" || page.Items[0].Alert.Alert.MergeChange == nil {
			t.Fatal("terminal pending intent was lost", err)
		}
		next = saved.Alert.Clone()
		next.MergeChange = nil
		if _, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, saved.Version, next); err != nil {
			t.Fatal(err)
		}
		page, err = work.ListMergeWork(t.Context(), store.MergeWorkCursor{}, 1)
		if err != nil || len(page.Items) != 0 {
			t.Fatal("completed intent still marked as work", err)
		}
	})
}
