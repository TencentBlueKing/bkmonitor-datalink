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
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func runShieldQueryContract(t *testing.T, factory Factory) {
	t.Run("shield management tenant pages include future and pending output", func(t *testing.T) {
		repo := factory(t)
		reader, ok := repo.(store.ShieldAlertReader)
		if !ok {
			t.Fatal("missing shield query port")
		}
		create := func(tenant, id string, shield bool) store.StoredAlert {
			t.Helper()
			a := Alert(tenant, id, "opening", id, "warning")
			if shield {
				next := a.CreateAt.Add(time.Hour)
				a.Shield = domain.AlertShield{Active: true, Bindings: []domain.ShieldBinding{ShieldBinding(a.CreateAt)}, NextCheckAt: &next}
			}
			v, err := repo.CreateAlert(t.Context(), a)
			if err != nil {
				t.Fatal(err)
			}
			return v.StoredAlert
		}
		first := create("tenant-a", "alert-a", true)
		pending := create("tenant-a", "alert-b", true)
		create("tenant-a", "not-shielded", false)
		create("tenant-b", "alert-a", true)
		a := pending.Alert.Clone()
		a.Shield = domain.AlertShield{}
		a.UpdateAt = a.UpdateAt.Add(time.Second)
		a.PolicyChange = &domain.AlertPolicyChange{OperationID: "stable-check", EffectiveAt: a.UpdateAt, Before: pending.Alert.Shield.Bindings}
		saved, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, pending.Version, a)
		if err != nil {
			t.Fatal(err)
		}
		page, err := reader.ListShieldAlerts(t.Context(), "tenant-a", "", 1)
		if err != nil || len(page.Alerts) != 1 || page.Alerts[0].Alert.AlertID != first.Alert.AlertID || page.Next != first.Alert.AlertID {
			t.Fatalf("first page %+v %v", page, err)
		}
		page, err = reader.ListShieldAlerts(t.Context(), "tenant-a", page.Next, 1)
		if err != nil || len(page.Alerts) != 1 || page.Alerts[0].Alert.PolicyChange == nil || page.Alerts[0].Alert.Shield.Active || page.Next != "" {
			t.Fatalf("pending page %+v %v", page, err)
		}
		done := saved.Alert.Clone()
		done.PolicyChange = nil
		if _, err := repo.CompareAndSetAlert(t.Context(), done.BKTenantID, done.AlertID, saved.Version, done); err != nil {
			t.Fatal(err)
		}
		page, err = reader.ListShieldAlerts(t.Context(), "tenant-a", "", 16)
		if err != nil || len(page.Alerts) != 1 {
			t.Fatal("completed release retained in current list", page, err)
		}
		for _, q := range []struct {
			tenant string
			limit  int
		}{{"", 1}, {"tenant-a", 0}, {"tenant-a", 17}} {
			if _, err := reader.ListShieldAlerts(t.Context(), q.tenant, "", q.limit); !errors.Is(err, store.ErrInvalidArgument) {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := reader.ListShieldAlerts(ctx, "tenant-a", "", 1); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}
