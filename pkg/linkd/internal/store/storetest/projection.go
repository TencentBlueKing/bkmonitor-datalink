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
	"errors"
	"reflect"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func runProjectionRevisionContract(t *testing.T, factory Factory) {
	t.Run("projection work pages remaining targets and tenants", func(t *testing.T) {
		repo := factory(t)
		work, ok := repo.(store.ProjectionWorkStore)
		if !ok {
			t.Fatal("repository must support projection work")
		}
		var first store.StoredAlert
		for _, tenant := range []string{"tenant-a", "tenant-b"} {
			a := Alert(tenant, "shared-id", "opening", "fp", "warning")
			a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"z": {SourceVersion: 1, RequiredRevision: 1}, "a": {SourceVersion: 1, RequiredRevision: 1}, "m": {SourceVersion: 1, RequiredRevision: 1}}}
			saved, err := repo.CreateAlert(t.Context(), a)
			if err != nil {
				t.Fatal(err)
			}
			if tenant == "tenant-a" {
				first = saved.StoredAlert
			}
		}
		page, err := work.ListProjectionWork(t.Context(), store.ProjectionWorkCursor{}, 1)
		if err != nil || len(page.Items) != 1 || page.Items[0].TargetID != "a" || page.Next.TenantID != "tenant-a" {
			t.Fatal("first target page", err)
		}
		next := first.Alert.Clone()
		next.Projection, _, err = next.Projection.Acknowledge("a", 1, 1, next.UpdateAt)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CompareAndSetAlert(t.Context(), next.BKTenantID, next.AlertID, first.Version, next); err != nil {
			t.Fatal(err)
		}
		after := page.Next
		got := []string{}
		for range 10 {
			page, err = work.ListProjectionWork(t.Context(), after, 1)
			if err != nil || len(page.Items) > 1 {
				t.Fatal(err)
			}
			if page.Next.TenantID != "" && page.Next.Compare(after) <= 0 {
				t.Fatal("cursor did not advance")
			}
			for _, item := range page.Items {
				got = append(got, item.Alert.Alert.BKTenantID+":"+item.TargetID)
			}
			after = page.Next
			if after.TenantID == "" {
				break
			}
		}
		want := []string{"tenant-a:m", "tenant-a:z", "tenant-b:a", "tenant-b:m", "tenant-b:z"}
		if !reflect.DeepEqual(got, want) || after.TenantID != "" {
			t.Fatalf("pending targets lost/repeated: %v", got)
		}
		if _, err := work.ListProjectionWork(t.Context(), store.ProjectionWorkCursor{AlertID: "unscoped"}, 1); err == nil {
			t.Fatal("unscoped cursor accepted")
		}
	})
	t.Run("business revisions and per-target ACK roundtrip including terminal", func(t *testing.T) {
		repo := factory(t)
		a := Alert("tenant", "projected", "opening", "projected-fp", "warning")
		a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 2, RequiredRevision: 1}, "audit": {SourceVersion: 2, RequiredRevision: 1}}}
		created, err := repo.CreateAlert(t.Context(), a)
		if err != nil {
			t.Fatal(err)
		}
		if created.Alert.Revision != 1 {
			t.Fatal("initial business revision not one")
		}
		next := created.Alert.Clone()
		next.UpdateAt = next.UpdateAt.Add(time.Second)
		next.Severity = "critical"
		changed, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, created.Version, next)
		if err != nil || changed.Alert.Revision != 2 || changed.Alert.Projection.Targets["kac"].RequiredRevision != 2 || changed.Alert.Projection.Targets["audit"].RequiredRevision != 2 {
			t.Fatal("business CAS omitted version or requirements", err)
		}
		if _, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, created.Version, next); !errors.Is(err, store.ErrVersionConflict) {
			t.Fatal("stale CAS changed revision", err)
		}
		next = changed.Alert.Clone()
		next.Projection, _, err = next.Projection.Acknowledge("kac", 2, 1, changed.Alert.UpdateAt)
		if err != nil {
			t.Fatal(err)
		}
		ack, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, changed.Version, next)
		if err != nil || ack.Alert.Revision != 2 || !ack.Alert.UpdateAt.Equal(changed.Alert.UpdateAt) || ack.Version == changed.Version || ack.Alert.Projection.Targets["audit"].SyncedRevision != 0 {
			t.Fatal("ACK altered business version or another target", err)
		}
		next = ack.Alert.Clone()
		next.UpdateAt = next.UpdateAt.Add(time.Second)
		next.Status = domain.AlertStatusRecovered
		next.EndAt = &next.UpdateAt
		next.EndType = domain.AlertEndTypeSource
		next.EndReason = "resolved"
		terminal, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, ack.Version, next)
		if err != nil || terminal.Alert.Revision != 3 || terminal.Alert.Projection.Targets["kac"].RequiredRevision != 3 || terminal.Alert.Projection.Targets["kac"].SyncedRevision != 1 {
			t.Fatal("terminal did not retain pending sync", err)
		}
		next = terminal.Alert.Clone()
		next.Projection, _, err = next.Projection.Acknowledge("kac", 2, 3, terminal.Alert.UpdateAt)
		if err != nil {
			t.Fatal(err)
		}
		completed, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, terminal.Version, next)
		if err != nil || completed.Alert.Revision != 3 || !completed.Alert.Projection.Pending() {
			t.Fatal("terminal ACK rejected or another target lost", err)
		}
		found, err := repo.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
		if err != nil || !reflect.DeepEqual(found.Alert, completed.Alert) {
			t.Fatal("revision/watermark roundtrip", err)
		}
		bad := found.Alert.Clone()
		bad.Projection.Targets["audit"] = domain.ProjectionTargetState{SourceVersion: 2, RequiredRevision: 3, SyncedRevision: 4, SyncedAt: &bad.UpdateAt}
		if _, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, found.Version, bad); err == nil {
			t.Fatal("future ACK accepted")
		}
	})
	t.Run("alert creation cannot inject future revision or fake ACK", func(t *testing.T) {
		repo := factory(t)
		a := Alert("tenant", "invalid-new", "opening", "invalid-fp", "warning")
		a.Revision = 2
		if _, err := repo.CreateAlert(t.Context(), a); err == nil {
			t.Fatal("future initial revision accepted")
		}
		a.Revision = 1
		a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 1, RequiredRevision: 1, SyncedRevision: 1, SyncedAt: &a.UpdateAt}}}
		if _, err := repo.CreateAlert(t.Context(), a); err == nil {
			t.Fatal("new alert faked projection confirmation")
		}
	})
}
