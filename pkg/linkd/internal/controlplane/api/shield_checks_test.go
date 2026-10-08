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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/policy"
	"linkd/internal/shieldcheck"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type diagnosticFixture struct {
	request shieldcheck.Request
	check   shieldcheck.Check
	missing bool
	err     error
	calls   int
}

func (d *diagnosticFixture) Request(_ context.Context, c shieldcheck.Command) (shieldcheck.Request, error) {
	d.calls++
	if d.err != nil {
		return shieldcheck.Request{}, d.err
	}
	return d.request, nil
}

func (d *diagnosticFixture) Latest(context.Context, string, string) (shieldcheck.Check, error) {
	if d.missing {
		return shieldcheck.Check{}, policy.ErrNotFound
	}
	return d.check, d.err
}

func (d *diagnosticFixture) List(context.Context, string, string, string, int) (shieldcheck.Page, error) {
	return shieldcheck.Page{Items: []shieldcheck.Request{d.request}, Next: "next"}, d.err
}

func (d *diagnosticFixture) GetRequest(context.Context, string, string, string) (shieldcheck.StoredRequest, error) {
	return shieldcheck.StoredRequest{Request: d.request, Version: "1"}, d.err
}

func TestShieldCheckAPIExplicitCommandsAndScopedDiagnostics(t *testing.T) {
	repo := memory.New()
	a := storetest.Alert("tenant", "alert", "opening", "fp", "warning")
	if _, err := repo.CreateAlert(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	c := shieldcheck.Command{TenantID: "tenant", AlertID: "alert", OperationID: "op", ExpectedRevision: a.Revision, OperatorID: "user", Reason: "复查"}
	raw, _ := json.Marshal([]string{"shield-check-request", c.TenantID, c.AlertID, c.OperationID})
	id := fmt.Sprintf("%x", sha256.Sum256(raw))
	at := time.Now().UTC()
	d := &diagnosticFixture{request: shieldcheck.Request{ID: id, Command: c, State: "pending", CreatedAt: at}, check: shieldcheck.Check{TenantID: c.TenantID, AlertID: c.AlertID, Trigger: "timer", StartedAt: at, FinishedAt: at, Report: lifecycle.ShieldCheckReport{ObservedRevision: a.Revision, ResultRevision: a.Revision, CheckedAt: at, Outcome: "inactive"}}}
	api := &API{ShieldRuntime: NewShieldRuntime(repo), ShieldDiagnostics: d, ShieldRequests: d, Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}}
	h := api.Handler()
	base := "/api/v1/policy-runtime/shield/alerts/alert"
	body := `{"bk_tenant_id":"tenant","operation_id":"op","expected_revision":1,"operator_id":"user","reason":"复查"}`
	if w := policyHTTP(t, h, "POST", base+"/reconcile", body); w.Code != 202 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, suffix := range []string{"/check", "/requests", "/requests/" + id} {
		if w := policyHTTP(t, h, "GET", base+suffix+"?bk_tenant_id=tenant", ""); w.Code != 200 {
			t.Fatal(suffix, w.Code, w.Body.String())
		}
	}
	d.missing = true
	if w := policyHTTP(t, h, "GET", base+"/check?bk_tenant_id=tenant", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"check":null`) {
		t.Fatal(w.Code, w.Body.String())
	}
	d.missing = false
	for _, bad := range []string{strings.Replace(body, `"reason":"复查"`, `"reason":""`, 1), strings.Replace(body, `"expected_revision":1`, `"expected_revision":0`, 1), body + `{}`, strings.Replace(body, `"reason":"复查"`, `"reason":"复查","force":true`, 1)} {
		if w := policyHTTP(t, h, "POST", base+"/reconcile", bad); w.Code != 400 {
			t.Fatal("invalid command", w.Code)
		}
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{scheduler.ErrLockBusy, 429}, {policy.ErrConflict, 409}, {policy.ErrUnavailable, 503}, {errors.New("private address"), 500}} {
		d.err = tc.err
		w := policyHTTP(t, h, "POST", base+"/reconcile", body)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "private") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	d.err = nil
	d.check.TenantID = "other"
	if w := policyHTTP(t, h, "GET", base+"/check?bk_tenant_id=tenant", ""); w.Code != 403 {
		t.Fatal("scope leak", w.Code)
	}
	d.check.TenantID = "tenant"
	if w := policyHTTP(t, h, "GET", base+"/requests?bk_tenant_id=other", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	api.ShieldRequests = nil
	if w := policyHTTP(t, h, "POST", base+"/reconcile", body); w.Code != 503 {
		t.Fatal(w.Code)
	}
}
