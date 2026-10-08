// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/policy"
	"linkd/internal/policy/simulation"
)

type statisticsFixture struct {
	calls int
	err   error
}

func (s *statisticsFixture) PolicyStatistics(_ context.Context, q policy.StatisticsQuery, at time.Time) (policy.StatisticsPage, error) {
	s.calls++
	items := []policy.PolicyStatistics{}
	for _, id := range q.IDs {
		items = append(items, policy.PolicyStatistics{ID: id, Matched: 2})
	}
	return policy.StatisticsPage{Scope: q.Scope, From: at.Truncate(time.Hour), To: at, Hours: q.Hours, Mode: "execution_observations", Items: items}, s.err
}

func TestPolicySimulationAndStatisticsManagementAPI(t *testing.T) {
	stats := &statisticsFixture{}
	a := &API{PolicySimulator: simulation.New(nil, nil, nil, nil, ""), PolicyStatistics: stats, Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}, WorkerToken: "worker"}}
	h := a.Handler()
	q := simulation.Request{Scope: policy.Scope{TenantID: "tenant", Kind: policy.Suppression}, Spec: json.RawMessage(`{"name":"test","is_enable":true,"updated_at":"2026-09-30T00:00:00Z","space_code":"bkcc__2","activate_times":[],"policy":{"expression":"A","A":{"condition":"term","target_key":"source_id","target_value":"source"}},"scheme":[{"type":"clip","count":2,"duration":60,"duration_type":"second"}]}`), Steps: []simulation.Step{{At: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)}}}
	raw, _ := json.Marshal(q)
	response := policyHTTP(t, h, http.MethodPost, "/api/v1/policies/simulate", string(raw))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "state_simulation") {
		t.Fatalf("simulate %d %s", response.Code, response.Body.String())
	}
	for _, body := range []string{`{}`, strings.Replace(string(raw), `"steps":`, `"unknown":true,"steps":`, 1), strings.Replace(string(raw), `"bk_tenant_id":"tenant"`, `"bk_tenant_id":"tenant","bk_tenant_id":"other"`, 1)} {
		if w := policyHTTP(t, h, http.MethodPost, "/api/v1/policies/simulate", body); w.Code != 400 {
			t.Fatalf("invalid simulation %d", w.Code)
		}
	}
	path := "/api/v1/policies/statistics?bk_tenant_id=tenant&type=merge&ids=p,q&hours=6"
	w := policyHTTP(t, h, http.MethodGet, path, "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || stats.calls != 1 {
		t.Fatalf("statistics %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/v1/policies/statistics?bk_tenant_id=tenant&type=merge&ids=p,p&hours=6", "/api/v1/policies/statistics?bk_tenant_id=tenant&type=merge&ids=p&hours=7", path + "&hours=1", path + "&extra=1"} {
		if w := policyHTTP(t, h, http.MethodGet, path, ""); w.Code != 400 {
			t.Fatalf("invalid query %d", w.Code)
		}
	}
	stats.err = policy.ErrPreviewCapacity
	if w := policyHTTP(t, h, http.MethodGet, path, ""); w.Code != 429 {
		t.Fatalf("capacity %d", w.Code)
	}
}
