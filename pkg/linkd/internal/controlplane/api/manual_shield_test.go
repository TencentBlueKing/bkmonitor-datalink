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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/lifecycle"
)

type manualShieldFunc func(context.Context, lifecycle.ShieldCommand) (lifecycle.ShieldCommandResult, error)

func (f manualShieldFunc) BindShield(ctx context.Context, c lifecycle.ShieldCommand) (lifecycle.ShieldCommandResult, error) {
	return f(ctx, c)
}

func TestManualShieldManagementBoundary(t *testing.T) {
	calls := 0
	api := &API{Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "management"}, WorkerToken: "worker"}, ManualShieldBinder: manualShieldFunc(func(ctx context.Context, c lifecycle.ShieldCommand) (lifecycle.ShieldCommandResult, error) {
		calls++
		if c.AlertID != "alert" || c.TenantID != "tenant" || c.OperationID != "stable" || c.OperatorID != "admin" || c.ExpectedRevision != 1 {
			t.Fatal("command scope changed")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing deadline")
		}
		return lifecycle.ShieldCommandResult{}, nil
	})}
	body := `{"bk_tenant_id":"tenant","operation_id":"stable","operator_id":"admin","expected_revision":1,"policy":{"id":"p","version":1,"digest":"` + strings.Repeat("a", 64) + `"},"effective_at":"2026-10-08T00:00:00Z"}`
	for _, tc := range []struct {
		key, body string
		status    int
	}{{"worker", body, 401}, {"management", body, 200}, {"management", strings.Replace(body, `"expected_revision":1`, `"expected_revision":0`, 1), 400}, {"management", strings.Replace(body, `"bk_tenant_id"`, `"unknown"`, 1), 400}, {"management", body + `{}`, 400}} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/alerts/alert/shield", strings.NewReader(tc.body))
		r.Header.Set("Internal-Token", testJWT(t, tc.key))
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatal("invalid command reached use case")
	}
}
