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

func runActionWorkContract(t *testing.T, factory Factory) {
	t.Run("durable action intent survives CAS failure and terminal state", func(t *testing.T) {
		repo := factory(t)
		work, ok := repo.(store.ActionWorkStore)
		if !ok {
			t.Fatal("action work missing")
		}
		create := func(tenant string) store.StoredAlert {
			a := Alert(tenant, "same-alert", "opening", "fp", "warning")
			a.Projection.Targets = map[string]domain.ProjectionTargetState{"kac": {ActionEnabled: true, SourceVersion: 1, RequiredRevision: 1}}
			at := a.UpdateAt
			a.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: a.Severity, CauseType: "source_event", CauseID: a.LatestEventID}
			var err error
			a.ActionPending, err = domain.NewAlertActionIntent(a, "source_event", a.LatestEventID)
			if err != nil {
				t.Fatal(err)
			}
			result, err := repo.CreateAlert(t.Context(), a)
			if err != nil {
				t.Fatal(err)
			}
			return result.StoredAlert
		}
		first := create("tenant-a")
		create("tenant-b")
		page, err := work.ListActionWork(t.Context(), store.ActionWorkCursor{}, 1)
		if err != nil || len(page.Items) != 1 || page.Items[0].Alert.BKTenantID != "tenant-a" || page.Next.TenantID != "tenant-a" {
			t.Fatal("first intent missing", page, err)
		}
		next := first.Alert.Clone()
		next.ActionPending = nil
		next.UpdateAt = next.UpdateAt.Add(time.Second)
		if _, err := repo.CompareAndSetAlert(t.Context(), "tenant-a", "same-alert", first.Version, next); !errors.Is(err, store.ErrInvalidTransition) {
			t.Fatal("business update bypassed intent", err)
		}
		next = first.Alert.Clone()
		next.Projection, _, err = next.Projection.Acknowledge("kac", 1, 1, next.UpdateAt.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		acked, err := repo.CompareAndSetAlert(t.Context(), "tenant-a", "same-alert", first.Version, next)
		if err != nil {
			t.Fatal(err)
		}
		next = acked.Alert.Clone()
		next.ActionPending = nil
		if _, err = repo.CompareAndSetAlert(t.Context(), "tenant-a", "same-alert", first.Version, next); !errors.Is(err, store.ErrVersionConflict) {
			t.Fatal("stale completion accepted", err)
		}
		done, err := repo.CompareAndSetAlert(t.Context(), "tenant-a", "same-alert", acked.Version, next)
		if err != nil || done.Alert.Revision != 1 {
			t.Fatal(err)
		}
		page, err = work.ListActionWork(t.Context(), page.Next, 1)
		if err != nil || len(page.Items) != 1 || page.Items[0].Alert.BKTenantID != "tenant-b" {
			t.Fatal("cross tenant cursor lost intent", err)
		}
		next = done.Alert.Clone()
		next.Status = domain.AlertStatusClosed
		next.UpdateAt = next.UpdateAt.Add(time.Second)
		next.EndAt = &next.UpdateAt
		next.EndType = domain.AlertEndTypeUser
		next.EndReason = "manual"
		next.Revision++
		next.Projection, err = next.Projection.RequireRevision(next.Revision)
		if err != nil {
			t.Fatal(err)
		}
		next.ActionPending, err = domain.NewAlertActionIntent(next, "user_operation", "close")
		if err != nil {
			t.Fatal(err)
		}
		terminal, err := repo.CompareAndSetAlert(t.Context(), "tenant-a", "same-alert", done.Version, next)
		if err != nil {
			t.Fatal(err)
		}
		page, err = work.ListActionWork(t.Context(), store.ActionWorkCursor{}, 16)
		if err != nil || len(page.Items) != 2 || page.Items[0].Alert.Status != domain.AlertStatusClosed {
			t.Fatal("terminal intent lost", page, err)
		}
		next = terminal.Alert.Clone()
		next.ActionPending = nil
		if _, err = repo.CompareAndSetAlert(t.Context(), "tenant-a", "same-alert", terminal.Version, next); err != nil {
			t.Fatal(err)
		}
		page, err = work.ListActionWork(t.Context(), store.ActionWorkCursor{}, 16)
		if err != nil || len(page.Items) != 1 || page.Items[0].Alert.BKTenantID != "tenant-b" {
			t.Fatal("completion did not clear work", page, err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err = work.ListActionWork(ctx, store.ActionWorkCursor{}, 16); !errors.Is(err, context.Canceled) {
			t.Fatal("cancel ignored", err)
		}
		for _, limit := range []int{0, 17} {
			if _, err = work.ListActionWork(t.Context(), store.ActionWorkCursor{}, limit); err == nil {
				t.Fatal("invalid limit accepted")
			}
		}
		if _, err = work.ListActionWork(t.Context(), store.ActionWorkCursor{AlertID: "orphan"}, 1); err == nil {
			t.Fatal("incomplete cursor accepted")
		}
	})
}
