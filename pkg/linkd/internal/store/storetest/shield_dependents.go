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
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func runShieldDependentsContract(t *testing.T, factory Factory) {
	t.Run("current shield dependents cross source tenant pages and release", func(t *testing.T) {
		repo := factory(t)
		reader, ok := repo.(store.ShieldDependencyReader)
		if !ok {
			t.Fatal("missing current shield dependents port")
		}
		binding := func(at time.Time, main string) domain.ShieldBinding {
			b := ShieldBinding(at)
			b.Type = "rely_shield"
			b.ActivationID = ""
			b.Mode = "custom_shield"
			b.MainAlertID = main
			return b
		}
		create := func(tenant, id, source, main string) store.StoredAlert {
			a := Alert(tenant, id, "opening", id, "warning")
			a.EventSourceID = source
			next := a.CreateAt.Add(time.Hour)
			a.Shield = domain.AlertShield{Active: true, Bindings: []domain.ShieldBinding{binding(a.CreateAt, main)}, NextCheckAt: &next}
			v, err := repo.CreateAlert(t.Context(), a)
			if err != nil {
				t.Fatal(err)
			}
			return v.StoredAlert
		}
		first := create("tenant-a", "child-00", "source-a", "main")
		for i := 1; i < 17; i++ {
			create("tenant-a", fmt.Sprintf("child-%02d", i), "source-b", "main")
		}
		create("tenant-a", "other-main", "source-a", "other")
		create("tenant-b", "child-00", "source-b", "main")
		page, err := reader.ListShieldDependents(t.Context(), "tenant-a", "main", "", 16)
		if err != nil || len(page.Alerts) != 16 || page.Next != "child-15" {
			t.Fatal("wrong main page", len(page.Alerts), page.Next, err)
		}
		next, err := reader.ListShieldDependents(t.Context(), "tenant-a", "main", page.Next, 16)
		if err != nil || len(next.Alerts) != 1 || next.Alerts[0].Alert.AlertID != "child-16" || next.Next != "" {
			t.Fatal("incomplete pagination", next, err)
		}
		replacement := first.Alert.Clone()
		replacement.Shield = domain.AlertShield{}
		replacement.UpdateAt = replacement.UpdateAt.Add(time.Second)
		replacement.PolicyChange = &domain.AlertPolicyChange{OperationID: "release", EffectiveAt: replacement.UpdateAt, Before: first.Alert.Shield.Bindings}
		if _, err := repo.CompareAndSetAlert(t.Context(), first.Alert.BKTenantID, first.Alert.AlertID, first.Version, replacement); err != nil {
			t.Fatal(err)
		}
		// 已解除但待写流水的历史绑定不再属于主的活动子关系，定时工作仍能补齐输出。
		page, err = reader.ListShieldDependents(t.Context(), "tenant-a", "main", "", 16)
		if err != nil || len(page.Alerts) != 16 || page.Alerts[0].Alert.AlertID != "child-01" || page.Next != "" {
			t.Fatal("released relation remained", len(page.Alerts), err)
		}
		page, err = reader.ListShieldDependents(t.Context(), "tenant-b", "main", "", 16)
		if err != nil || len(page.Alerts) != 1 || page.Alerts[0].Alert.BKTenantID != "tenant-b" {
			t.Fatal("tenant isolation", err)
		}
		for _, q := range []struct {
			tenant, main, after string
			limit               int
		}{{"", "main", "", 1}, {"tenant-a", "", "", 1}, {"tenant-a", strings.Repeat("m", 161), "", 1}, {"tenant-a", "main", "", 17}, {"tenant-a", "main", "", 0}} {
			if _, err := reader.ListShieldDependents(t.Context(), q.tenant, q.main, q.after, q.limit); !errors.Is(err, store.ErrInvalidArgument) {
				t.Fatal("invalid dependency query accepted", err)
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := reader.ListShieldDependents(ctx, "tenant-a", "main", "", 1); !errors.Is(err, context.Canceled) {
			t.Fatal("cancel ignored", err)
		}
	})
}
