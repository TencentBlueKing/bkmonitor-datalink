// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/blueking"
	"linkd/internal/config"
)

type tenantReaderFunc func(context.Context) ([]blueking.Tenant, error)

func (f tenantReaderFunc) ListTenants(ctx context.Context) ([]blueking.Tenant, error) {
	return f(ctx)
}

func TestTenantDirectoryAPI(t *testing.T) {
	for _, tc := range []struct {
		name, query, token string
		configured, failed bool
		status, calls      int
	}{
		{"success", "", "jwt", true, false, 200, 1},
		{"empty", "", "jwt", true, false, 200, 1},
		{"unauthenticated", "", "", true, false, 401, 0},
		{"worker token", "", "worker", true, false, 401, 0},
		{"unconfigured", "", "jwt", false, false, 503, 0},
		{"upstream failure", "", "jwt", true, true, 502, 1},
		{"tenant override", "?bk_tenant_id=other", "jwt", true, false, 400, 0},
		{"url override", "?url=https://other", "jwt", true, false, 400, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			a := &API{Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}, WorkerToken: "worker"}}
			if tc.configured {
				a.Tenants = tenantReaderFunc(func(ctx context.Context) ([]blueking.Tenant, error) {
					calls++
					if _, ok := ctx.Deadline(); !ok {
						t.Error("missing request deadline")
					}
					if tc.failed {
						return nil, fmt.Errorf("sensitive-upstream-body")
					}
					if tc.name == "empty" {
						return []blueking.Tenant{}, nil
					}
					return []blueking.Tenant{{ID: "system", Name: "Blueking", Status: "enabled"}}, nil
				})
			}
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/tenants"+tc.query, nil)
			if tc.token == "jwt" {
				r.Header.Set("Internal-Token", testJWT(t, "admin"))
			} else if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != tc.status || calls != tc.calls || strings.Contains(w.Body.String(), "sensitive-upstream-body") {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
			}
			if tc.status != 401 && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("directory may be cached")
			}
			if tc.status == 200 {
				want := "[{\"id\":\"system\",\"name\":\"Blueking\",\"status\":\"enabled\"}]\n"
				if tc.name == "empty" {
					want = "[]\n"
				}
				if w.Body.String() != want || w.Header().Get("Content-Type") != "application/json" {
					t.Fatalf("unexpected response: %s", w.Body.String())
				}
			}
		})
	}
}
