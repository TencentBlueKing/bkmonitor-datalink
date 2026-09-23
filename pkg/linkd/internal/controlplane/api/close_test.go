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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	lifecycleprocess "linkd/internal/lifecycle/process"
)

type closeFunc func(context.Context, lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error)

func (f closeFunc) CloseAlert(ctx context.Context, command lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error) {
	return f(ctx, command)
}

func TestCloseAlertManagementBoundary(t *testing.T) {
	calls := 0
	var failure error
	api := &API{Config: config.DispatchConfig{APIToken: "admin-token", WorkerToken: "worker-token"}, AlertCloser: closeFunc(func(ctx context.Context, command lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("request has no deadline")
		}
		if command.BKTenantID != "tenant-a" || command.AlertID != "alert-a" || command.OperatorKind != domain.OperatorKindUser || command.OperationID != "stable-operation" || command.Reason != "verified" {
			t.Fatalf("command = %#v", command)
		}
		return lifecycle.CloseAlertResult{Alert: domain.Alert{AlertID: command.AlertID, BKTenantID: command.BKTenantID, Status: domain.AlertStatusClosed}}, failure
	})}
	body := `{"bk_tenant_id":"tenant-a","operation_id":"stable-operation","operator_id":"admin","reason":"verified","effective_at":"2026-09-23T00:00:00Z"}`
	request := func(token, payload string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/alerts/alert-a/close", strings.NewReader(payload))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request("worker-token", body); w.Code != 401 || calls != 0 {
		t.Fatalf("worker access = %d", w.Code)
	}
	if w := request("admin-token", body); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"closed"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("success = %d %s", w.Code, w.Body.String())
	}
	for _, payload := range []string{strings.Replace(body, `"reason"`, `"unexpected"`, 1), body + `{}`} {
		if w := request("admin-token", payload); w.Code != 400 {
			t.Fatalf("bad payload = %d", w.Code)
		}
	}
	failure = &lifecycleprocess.CloseError{Status: 409, Message: "state changed"}
	if w := request("admin-token", body); w.Code != 409 {
		t.Fatalf("conflict = %d", w.Code)
	}
	failure = errors.New("secret connection details")
	if w := request("admin-token", body); w.Code != 502 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("unknown failure = %d %s", w.Code, w.Body.String())
	}
}
