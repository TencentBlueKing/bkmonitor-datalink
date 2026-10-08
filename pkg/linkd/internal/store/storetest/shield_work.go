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
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// ShieldBinding 构造不依赖远端策略数据源的持久化关系，用于验证仓储边界。
func ShieldBinding(at time.Time) domain.ShieldBinding {
	return domain.ShieldBinding{BindingID: strings.Repeat("b", 64), ActivationID: strings.Repeat("c", 64), Policy: domain.PolicyVersion{ID: "shield", Version: 1, Digest: strings.Repeat("a", 64)}, Type: "time_shield", SourceEventID: "opening", Severity: "warning", BoundAt: at, Reason: "maintenance"}
}

func runShieldWorkContract(t *testing.T, factory Factory) {
	t.Run("shield work survives state change and preserves tenant cursor", func(t *testing.T) {
		repo := factory(t)
		work, ok := repo.(store.ShieldWorkStore)
		if !ok {
			t.Fatal("repository must implement shield work")
		}
		now := time.Date(2026, 9, 1, 0, 1, 0, 0, time.UTC)
		create := func(tenant, id string, future bool) store.StoredAlert {
			t.Helper()
			a := Alert(tenant, id, "opening", id, "warning")
			next := now
			if future {
				next = now.Add(time.Hour)
			}
			a.Shield = domain.AlertShield{Active: true, Bindings: []domain.ShieldBinding{ShieldBinding(a.CreateAt)}, NextCheckAt: &next}
			a.PolicyTags = []int64{2, 8}
			created, err := repo.CreateAlert(t.Context(), a)
			if err != nil {
				t.Fatal(err)
			}
			return created.StoredAlert
		}
		first := create("tenant-a", "alert-a", false)
		create("tenant-a", "alert-b", true)
		pending := create("tenant-b", "alert-a", false)
		changed := pending.Alert.Clone()
		changed.Shield = domain.AlertShield{}
		changed.UpdateAt = changed.UpdateAt.Add(time.Second)
		changed.PolicyChange = &domain.AlertPolicyChange{OperationID: "stable-shield-operation", EffectiveAt: changed.UpdateAt, Before: pending.Alert.Shield.Clone().Bindings}
		saved, err := repo.CompareAndSetAlert(t.Context(), changed.BKTenantID, changed.AlertID, pending.Version, changed)
		if err != nil {
			t.Fatal(err)
		}
		page, err := work.ListShieldWork(t.Context(), store.ShieldWorkCursor{}, now, 1)
		if err != nil || len(page.Alerts) != 1 || page.Alerts[0].Alert.BKTenantID != "tenant-a" {
			t.Fatalf("first page %+v %v", page, err)
		}
		page, err = work.ListShieldWork(t.Context(), page.Next, now, 1)
		if err != nil || len(page.Alerts) != 0 || page.Next.AlertID != "alert-b" {
			t.Fatalf("future row blocked cursor %+v %v", page, err)
		}
		page, err = work.ListShieldWork(t.Context(), page.Next, now, 1)
		if err != nil || len(page.Alerts) != 1 || page.Alerts[0].Alert.PolicyChange == nil || page.Alerts[0].Alert.BKTenantID != "tenant-b" {
			t.Fatalf("released pending work lost %+v %v", page, err)
		}
		if !reflect.DeepEqual(page.Alerts[0].Alert, saved.Alert) {
			t.Fatal("shield metadata did not round-trip")
		}
		completed := saved.Alert.Clone()
		completed.PolicyChange = nil
		if _, err := repo.CompareAndSetAlert(t.Context(), completed.BKTenantID, completed.AlertID, saved.Version, completed); err != nil {
			t.Fatal("completion changed no business fields but was rejected", err)
		}
		rescheduled := first.Alert.Clone()
		next := now.Add(time.Hour)
		rescheduled.Shield.NextCheckAt = &next
		if _, err := repo.CompareAndSetAlert(t.Context(), first.Alert.BKTenantID, first.Alert.AlertID, first.Version, rescheduled); err != nil {
			t.Fatal("check metadata rejected", err)
		}
		page, err = work.ListShieldWork(t.Context(), store.ShieldWorkCursor{}, now, 16)
		if err != nil || len(page.Alerts) != 0 {
			t.Fatalf("completed/future work still due %+v %v", page, err)
		}
		if _, err := work.ListShieldWork(t.Context(), store.ShieldWorkCursor{AlertID: "orphan"}, now, 16); err == nil {
			t.Fatal("unscoped cursor accepted")
		}
	})
}
