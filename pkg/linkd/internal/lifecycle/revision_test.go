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

func TestEventPlanRetryAcceptsEquivalentJSONAfterAlertPersistence(t *testing.T) {
	base := memory.New()
	repo := &failCloseLogs{Repository: base, fail: true}
	p := newTestProcessor(t, repo, NoopFinalHook{})
	e := storetest.Event("tenant", "json-retry", "json-fingerprint", "warning")
	e.ExtraData = domain.JSONObject{"nested": []byte(`{"a":1,"z":2}`)}
	created, err := base.CreateEvent(t.Context(), e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ProcessEvent(t.Context(), created.StoredEvent); err == nil {
		t.Fatal("log failure ignored")
	}
	pending, err := base.GetEvent(t.Context(), e.BKTenantID, e.EventID)
	if err != nil || pending.Processing.Plan == nil || len(pending.Processing.Plan.Mutations) != 1 {
		t.Fatal("create plan not preserved", err)
	}
	target := &pending.Processing.Plan.Mutations[0].Alert
	before, err := base.GetAlert(t.Context(), e.BKTenantID, target.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟计划保留原始 JSON，而告警读取边界已经规范化同一业务对象。
	target.ExtraData["nested"] = []byte(`{ "z": 2, "a": 1 }`)
	result, err := p.ProcessEvent(t.Context(), pending)
	if err != nil || result.EventState != domain.EventProcessStateAccepted {
		t.Fatal("JSON representation broke frozen create plan retry", err)
	}
	after, err := base.GetAlert(t.Context(), e.BKTenantID, target.AlertID)
	if err != nil || after.Version != before.Version || after.Alert.Revision != before.Alert.Revision {
		t.Fatal("retry rewrote committed alert", err)
	}
}
