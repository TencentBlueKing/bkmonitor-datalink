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
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type shieldQueryReader struct {
	*memory.Repository
	fail  bool
	cross bool
}

func (r *shieldQueryReader) ListShieldAlerts(ctx context.Context, tenant, after string, limit int) (store.ShieldAlertPage, error) {
	if r.fail {
		return store.ShieldAlertPage{}, errors.New("secret database address")
	}
	page, err := r.Repository.ListShieldAlerts(ctx, tenant, after, limit)
	if r.cross && len(page.Alerts) > 0 {
		page.Alerts[0].Alert.BKTenantID = "other"
	}
	return page, err
}

func TestShieldRuntimeScopedCurrentAndHistoryPages(t *testing.T) {
	repo := &shieldQueryReader{Repository: memory.New()}
	for _, tenant := range []string{"tenant", "other"} {
		for _, id := range []string{"a", "b"} {
			a := storetest.Alert(tenant, id, "opening", id, "warning")
			next := a.CreateAt.Add(time.Hour)
			a.Shield = domain.AlertShield{Active: true, Bindings: []domain.ShieldBinding{storetest.ShieldBinding(a.CreateAt)}, NextCheckAt: &next}
			if _, err := repo.CreateAlert(t.Context(), a); err != nil {
				t.Fatal(err)
			}
		}
	}
	a, _ := repo.GetAlert(t.Context(), "tenant", "a")
	for i, kind := range []domain.OperationKind{domain.OperationKindTrigger, domain.OperationKindShield} {
		log := domain.AlertLog{BKTenantID: "tenant", AlertID: "a", LogID: []string{"first", "second"}[i], OperatorKind: domain.OperatorKindSystem, OperationKind: kind, CreatedTime: a.Alert.CreateAt.Add(time.Duration(i) * time.Second), Params: domain.JSONObject{}}
		if _, err := repo.AppendAlertLog(t.Context(), log); err != nil {
			t.Fatal(err)
		}
	}
	api := &API{ShieldRuntime: NewShieldRuntime(repo), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}}
	h := api.Handler()
	base := "/api/v1/policy-runtime/shield/alerts"
	w := policyHTTP(t, h, "GET", base+"?bk_tenant_id=tenant&policy_id=missing&limit=1", "")
	var page struct {
		Items []shieldAlertView `json:"items"`
		Next  string            `json:"next"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 0 || page.Next == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := policyHTTP(t, h, "GET", base+"?bk_tenant_id=other&policy_id=missing&after="+url.QueryEscape(page.Next), ""); w.Code != 400 {
		t.Fatal("cross-tenant cursor", w.Code)
	}
	for _, path := range []string{base + "?bk_tenant_id=tenant", base + "/a?bk_tenant_id=tenant"} {
		w := policyHTTP(t, h, "GET", path, "")
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "\"other\"") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w = policyHTTP(t, h, "GET", base+"/a/history?bk_tenant_id=tenant&limit=1", "")
	var history struct {
		Items []domain.AlertLog `json:"items"`
		Next  string            `json:"next"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &history) != nil || len(history.Items) != 0 || history.Next == "" {
		t.Fatal("empty filtered history lost cursor", w.Code, w.Body.String())
	}
	w = policyHTTP(t, h, "GET", base+"/a/history?bk_tenant_id=tenant&limit=1&after="+url.QueryEscape(history.Next), "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &history) != nil || len(history.Items) != 1 || history.Items[0].OperationKind != domain.OperationKindShield {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{base, base + "?bk_tenant_id=tenant&limit=17", base + "?bk_tenant_id=tenant&binding_type=invalid", base + "?bk_tenant_id=tenant&url=unknown", base + "/a?bk_tenant_id=tenant&limit=1", base + "/a/history?bk_tenant_id=tenant&policy_id=shield"} {
		if w := policyHTTP(t, h, "GET", path, ""); w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
	for _, method := range []string{"POST", "DELETE", "PUT"} {
		if w := policyHTTP(t, h, method, base+"/a?bk_tenant_id=tenant", ""); w.Code != 405 {
			t.Fatal("write method exposed", w.Code)
		}
	}
	if w := policyHTTP(t, h, "GET", base+"/absent?bk_tenant_id=tenant", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	repo.cross = true
	if w := policyHTTP(t, h, "GET", base+"?bk_tenant_id=tenant", ""); w.Code != 403 {
		t.Fatal("bad response tenant accepted", w.Code)
	}
	repo.cross = false
	repo.fail = true
	if w := policyHTTP(t, h, "GET", base+"?bk_tenant_id=tenant", ""); w.Code != 500 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	repo.fail = false
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, base+"?bk_tenant_id=tenant", nil)
	req.Header.Set("Internal-Token", testJWT(t, "admin"))
	record := httptest.NewRecorder()
	h.ServeHTTP(record, req)
	if record.Code != 408 {
		t.Fatal("cancellation lost", record.Code)
	}
	api.ShieldRuntime.slots <- struct{}{}
	api.ShieldRuntime.slots <- struct{}{}
	if w := policyHTTP(t, h, "GET", base+"?bk_tenant_id=tenant", ""); w.Code != 429 {
		t.Fatal("query capacity ignored", w.Code)
	}
	<-api.ShieldRuntime.slots
	<-api.ShieldRuntime.slots
}
