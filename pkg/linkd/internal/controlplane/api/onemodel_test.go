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
	"linkd/internal/onemodel"
	"linkd/internal/onemodel/queryservice"
)

type apiPages struct{ calls int }

func (p *apiPages) Search(_ context.Context, tenant string, q onemodel.PageQuery) (onemodel.Page, error) {
	p.calls++
	return onemodel.Page{Instances: []onemodel.Instance{{TenantID: tenant, ModelCode: q.ModelID, InstanceID: "1"}}}, nil
}

func (p *apiPages) Close(context.Context, string, string) error { p.calls++; return nil }

type apiRelations struct{}

func (apiRelations) Search(context.Context, string, onemodel.Query) ([]onemodel.Instance, error) {
	return nil, nil
}

func (apiRelations) Related(context.Context, string, []onemodel.Instance, string, string, onemodel.Query) ([]onemodel.Instance, error) {
	return nil, nil
}

func TestOneModelAPIAuthenticationAndStrictInput(t *testing.T) {
	pages := &apiPages{}
	handler := (&API{OneModel: queryservice.New(pages, apiRelations{}), Config: config.DispatchConfig{APIToken: "admin", WorkerToken: "worker"}}).Handler()
	for _, tc := range []struct {
		name, path, token, body string
		code                    int
	}{
		{"search", "search", "admin", `{"bk_tenant_id":"t","model_id":"host"}`, 200},
		{"worker denied", "search", "worker", `{}`, 401},
		{"anonymous denied", "search", "", `{}`, 401},
		{"connections rejected", "search", "admin", `{"bk_tenant_id":"t","model_id":"host","addresses":["http://untrusted"]}`, 400},
		{"nested unknown rejected", "search", "admin", `{"bk_tenant_id":"t","model_id":"host","where":{"typo":true}}`, 400},
		{"trailing JSON", "search", "admin", `{} {}`, 400},
		{"close", "close", "admin", `{"bk_tenant_id":"t","cursor":"token"}`, 200},
		{"unknown operation", "write", "admin", `{}`, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/onemodel/"+tc.path, strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+tc.token)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.code {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.code == 200 && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("cacheable query")
			}
		})
	}
	if pages.calls != 2 {
		t.Fatalf("unexpected calls %d", pages.calls)
	}
}

func TestOneModelAPIRejectsMissingResourceAndOversizedInput(t *testing.T) {
	handler := (&API{OneModel: queryservice.New(nil, nil), Config: config.DispatchConfig{APIToken: "admin"}}).Handler()
	for _, body := range []string{`{"bk_tenant_id":"t","model_id":"host"}`, `{"model_id":"` + strings.Repeat("x", 1<<20) + `"}`} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/onemodel/search", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer admin")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := 503
		if len(body) > 1<<20 {
			want = 400
		}
		if w.Code != want {
			t.Fatalf("status=%d", w.Code)
		}
	}
}
