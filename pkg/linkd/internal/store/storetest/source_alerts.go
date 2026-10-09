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
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func runSourceAlertsContract(t *testing.T, factory Factory) {
	t.Run("source alert query preserves identity and isolates scopes", func(t *testing.T) {
		repo := factory(t)
		reader, ok := repo.(store.SourceAlertReader)
		if !ok {
			t.Fatal("missing source alert reader")
		}
		for _, item := range []struct {
			tenant, source, id string
			ended              bool
		}{{"tenant", "source", "a", false}, {"tenant", "source", "b", true}, {"tenant", "other", "c", false}, {"other", "source", "a", false}} {
			a := Alert(item.tenant, item.id, "opening", item.id, "warning")
			a.EventSourceID, a.SourceEventID, a.SourceAlertID = item.source, "A123", "A123"
			created, err := repo.CreateAlert(t.Context(), a)
			if err != nil {
				t.Fatal(err)
			}
			if item.ended {
				a = created.Alert.Clone()
				a.UpdateAt = a.UpdateAt.Add(time.Second)
				at := a.UpdateAt
				a.Status, a.EndAt, a.EndType, a.EndReason = domain.AlertStatusRecovered, &at, domain.AlertEndTypeSource, "resolved"
				if _, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, created.Version, a); err != nil {
					t.Fatal(err)
				}
			}
		}
		q := store.SourceAlertQuery{TenantID: "tenant", EventSourceID: "source", Limit: 1}
		p, err := reader.ListSourceAlerts(t.Context(), q)
		if err != nil || len(p.Alerts) != 1 || p.Alerts[0].Alert.AlertID != "a" || p.Next != "a" || p.Alerts[0].Alert.SourceEventID != "A123" {
			t.Fatalf("first %+v %v", p, err)
		}
		q.After = p.Next
		p, err = reader.ListSourceAlerts(t.Context(), q)
		if err != nil || len(p.Alerts) != 1 || p.Alerts[0].Alert.AlertID != "b" || p.Next != "" {
			t.Fatalf("next %+v %v", p, err)
		}
		q.After, q.Status = "", domain.AlertStatusActive
		p, err = reader.ListSourceAlerts(t.Context(), q)
		if err != nil || len(p.Alerts) != 1 || p.Next != "" {
			t.Fatalf("active %+v %v", p, err)
		}
		for _, invalid := range []store.SourceAlertQuery{{TenantID: "tenant", Limit: 1}, {EventSourceID: "source", Limit: 1}, {TenantID: "tenant", EventSourceID: "source", Limit: 33}, {TenantID: "tenant", EventSourceID: "source", Limit: 1, Status: "shielded"}} {
			if _, err := reader.ListSourceAlerts(t.Context(), invalid); err == nil {
				t.Fatal("invalid scope accepted", invalid)
			}
		}
	})
}
