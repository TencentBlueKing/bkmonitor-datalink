// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

func TestEventPlanRetryIgnoresProjectionACKWithoutAddingRevision(t *testing.T) {
	base := memory.New()
	repo := &failCloseLogs{Repository: base}
	a := storetest.Alert("tenant", "existing", "opening", "fp", "warning")
	a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 1, RequiredRevision: 1}}}
	if _, err := repo.CreateAlert(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	p := newTestProcessor(t, repo, NoopFinalHook{})
	e := storetest.Event(a.BKTenantID, "next-event", a.Fingerprint, a.Severity)
	created, err := repo.CreateEvent(t.Context(), e)
	if err != nil {
		t.Fatal(err)
	}
	repo.fail = true
	if _, err := p.ProcessEvent(t.Context(), created.StoredEvent); err == nil {
		t.Fatal("log failure ignored")
	}
	current, err := base.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil || current.Alert.Revision != 2 || current.Alert.Projection.Targets["kac"].RequiredRevision != 2 {
		t.Fatal("source mutation not committed with projection requirement", err)
	}
	next := current.Alert.Clone()
	next.Projection, _, err = next.Projection.Acknowledge("kac", 1, 2, current.Alert.UpdateAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	acknowledged, err := base.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, current.Version, next)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := base.GetEvent(t.Context(), a.BKTenantID, e.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ProcessEvent(t.Context(), pending); err != nil {
		t.Fatal("ACK broke frozen plan retry", err)
	}
	final, err := base.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil || final.Version != acknowledged.Version || final.Alert.Revision != 2 || final.Alert.Projection.Targets["kac"].SyncedRevision != 2 {
		t.Fatal("retry overwrote ACK or added business revision", err)
	}
}

func TestEventPlanRetryPreservesLaterActiveControlMutation(t *testing.T) {
	for _, create := range []bool{false, true} {
		t.Run(map[bool]string{false: "update", true: "create"}[create], func(t *testing.T) {
			base := memory.New()
			repo := &failCloseLogs{Repository: base}
			a := storetest.Alert("tenant", "existing", "opening", "fp", "warning")
			if !create {
				if _, err := base.CreateAlert(t.Context(), a); err != nil {
					t.Fatal(err)
				}
			}
			p := newTestProcessor(t, repo, NoopFinalHook{})
			e := storetest.Event(a.BKTenantID, "source-event", a.Fingerprint, a.Severity)
			stored, err := base.CreateEvent(t.Context(), e)
			if err != nil {
				t.Fatal(err)
			}
			repo.fail = true
			if _, err := p.ProcessEvent(t.Context(), stored.StoredEvent); err == nil {
				t.Fatal("log failure ignored")
			}
			pending, err := base.GetEvent(t.Context(), e.BKTenantID, e.EventID)
			if err != nil || pending.Processing.Plan == nil {
				t.Fatal("frozen plan lost", err)
			}
			target := pending.Processing.Plan.Mutations[0].Alert
			current, err := base.GetAlert(t.Context(), e.BKTenantID, target.AlertID)
			if err != nil {
				t.Fatal(err)
			}
			later := current.Alert.Clone()
			later.UpdateAt = later.UpdateAt.Add(time.Second)
			later.PolicyTags = []int64{9}
			updated, err := base.CompareAndSetAlert(t.Context(), e.BKTenantID, target.AlertID, current.Version, later)
			if err != nil {
				t.Fatal(err)
			}
			result, err := p.ProcessEvent(t.Context(), pending)
			if err != nil || result.EventState != domain.EventProcessStateAccepted {
				t.Fatal("later active state broke source retry", err)
			}
			final, err := base.GetAlert(t.Context(), e.BKTenantID, target.AlertID)
			if err != nil || final.Version != updated.Version || final.Alert.Revision != target.Revision+1 || len(final.Alert.PolicyTags) != 1 {
				t.Fatal("old source plan overwrote control mutation", err)
			}
		})
	}
}
