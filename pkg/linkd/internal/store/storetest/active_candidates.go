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
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func runActiveCandidatesContract(t *testing.T, factory Factory) {
	t.Run("active candidate pages are ordered isolated and include blocked", func(t *testing.T) {
		repo := factory(t)
		reader, ok := repo.(store.ActiveAlertReader)
		if !ok {
			t.Fatal("missing active candidate reader")
		}
		for _, item := range []struct {
			tenant, id        string
			terminal, blocked bool
		}{{"tenant", "a", false, false}, {"tenant", "b", false, true}, {"tenant", "c", true, false}, {"other", "a", false, false}} {
			a := Alert(item.tenant, item.id, "opening", item.id, "warning")

			if item.blocked {
				b := ShieldBinding(a.UpdateAt)
				b.Type = "rely_shield"
				b.Mode = "custom_shield"
				b.MainAlertID = "parent"
				b.MainCandidate = &domain.DependencyMain{AlertID: "parent", EventID: "event-main", EventSourceID: "other-source", Fingerprint: "main-fp", Severity: "warning"}
				next := a.UpdateAt
				a.Shield = domain.AlertShield{Active: true, Bindings: []domain.ShieldBinding{b}, NextCheckAt: &next}
			}
			created, err := repo.CreateAlert(t.Context(), a)
			if err != nil {
				t.Fatal(err)
			}
			if item.terminal {
				a = created.Alert.Clone()
				a.UpdateAt = a.UpdateAt.Add(time.Second)
				at := a.UpdateAt
				a.Status = domain.AlertStatusRecovered
				a.EndAt = &at
				a.EndType = domain.AlertEndTypeSource
				a.EndReason = "resolved"
				if _, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, created.Version, a); err != nil {
					t.Fatal(err)
				}
			}
		}
		p, err := reader.ListActiveAlerts(t.Context(), "tenant", "", 1)
		if err != nil || len(p.Alerts) != 1 || p.Alerts[0].Alert.AlertID != "a" || p.Next != "a" {
			t.Fatalf("first page %+v %v", p, err)
		}
		p, err = reader.ListActiveAlerts(t.Context(), "tenant", p.Next, 1)
		if err != nil || len(p.Alerts) != 1 || p.Alerts[0].Alert.AlertID != "b" || p.Next != "" {
			t.Fatalf("second page %+v %v", p, err)
		}
		original, err := repo.GetAlert(t.Context(), "tenant", "b")
		if err != nil || !reflect.DeepEqual(original.Alert, p.Alerts[0].Alert) {
			t.Fatal("dependency reference did not round trip", err)
		}
		p.Alerts[0].Alert.Shield.Bindings[0].MainCandidate.EventID = "modified"
		again, _ := repo.GetAlert(t.Context(), "tenant", "b")
		if again.Alert.Shield.Bindings[0].MainCandidate.EventID != "event-main" {
			t.Fatal("candidate clone leaked into persistence")
		}
		if _, err := reader.ListActiveAlerts(t.Context(), "", "", 1); err == nil {
			t.Fatal("unscoped scan accepted")
		}
		if _, err := reader.ListActiveAlerts(t.Context(), "tenant", "", 33); err == nil {
			t.Fatal("unbounded scan accepted")
		}
	})
}
