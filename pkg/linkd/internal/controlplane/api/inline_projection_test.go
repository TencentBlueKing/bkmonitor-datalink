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
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

type inlineProjectionFunc func(context.Context, string, string) (store.StoredAlert, error)

func (f inlineProjectionFunc) ProjectCurrent(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	return f(ctx, tenant, id)
}

func TestInlineProjectionScopeAuthenticationAndDeferredResult(t *testing.T) {
	for _, name := range []string{"confirmed", "deferred", "lookup failed", "bad tenant", "wrong token", "oversize", "unconfigured"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			api := &API{Config: config.DispatchConfig{WorkerToken: "worker-secret"}}
			api.InlineProjection = inlineProjectionFunc(func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
				calls++
				if tenant != "tenant" || id != "alert" {
					t.Fatal("unscoped read")
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded projection")
				}
				if name == "lookup failed" {
					return store.StoredAlert{}, errors.New("private failure")
				}
				row := store.StoredAlert{Alert: storetest.Alert(tenant, id, "event", "fp", "warning"), Version: store.NewVersionToken("v")}
				if name == "deferred" {
					return row, errors.New("compensation queued")
				}
				return row, nil
			})
			body := `{"bk_tenant_id":"tenant"}`
			want := http.StatusOK
			switch name {
			case "deferred":
				want = http.StatusAccepted
			case "lookup failed":
				want = http.StatusBadGateway
			case "bad tenant":
				body = `{"bk_tenant_id":""}`
				want = http.StatusBadRequest
			case "wrong token":
				want = http.StatusUnauthorized
			case "oversize":
				body = strings.Repeat(" ", 1025) + body
				want = http.StatusBadRequest
			case "unconfigured":
				api.InlineProjection = nil
				want = http.StatusServiceUnavailable
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/alerts/alert/projection", strings.NewReader(body))
			token := "worker-secret"
			if name == "wrong token" {
				token = "other"
			}
			req.Header.Set("Authorization", "Bearer "+token)
			out := httptest.NewRecorder()
			api.Handler().ServeHTTP(out, req)
			if out.Code != want || strings.Contains(out.Body.String(), "private failure") {
				t.Fatal(out.Code, out.Body.String())
			}
			if (name == "bad tenant" || name == "wrong token" || name == "oversize" || name == "unconfigured") && calls != 0 {
				t.Fatal("invalid request executed")
			}
			if name == "deferred" && !strings.Contains(out.Body.String(), `"deferred":true`) {
				t.Fatal("queued misreported as confirmed")
			}
		})
	}
}
