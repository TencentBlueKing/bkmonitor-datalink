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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/policy"
)

type policyDocument struct {
	raw     json.RawMessage
	version int
}

type policyDocuments struct {
	rows map[string]policyDocument
	fail bool
}

func (d *policyDocuments) Get(_ context.Context, collection, key string) (json.RawMessage, string, error) {
	if d.fail {
		return nil, "", errors.New("secret-driver-details")
	}
	v, ok := d.rows[collection+"/"+key]
	if !ok {
		return nil, "", policy.ErrNotFound
	}
	return bytes.Clone(v.raw), fmt.Sprint(v.version), nil
}

func (d *policyDocuments) Put(_ context.Context, collection, key, expected string, raw json.RawMessage) error {
	key = collection + "/" + key
	v, ok := d.rows[key]
	if (expected == "" && ok) || (expected != "" && (!ok || expected != fmt.Sprint(v.version))) {
		return policy.ErrConflict
	}
	d.rows[key] = policyDocument{bytes.Clone(raw), v.version + 1}
	return nil
}

func (d *policyDocuments) List(_ context.Context, collection, prefix, after string, limit int) ([]json.RawMessage, error) {
	keys := []string{}
	for key := range d.rows {
		if strings.HasPrefix(key, collection+"/"+prefix) && strings.TrimPrefix(key, collection+"/") > after {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	result := make([]json.RawMessage, 0, len(keys))
	for _, key := range keys {
		result = append(result, bytes.Clone(d.rows[key].raw))
	}
	return result, nil
}

func policyRequestBody() string {
	return `{"schema_version":1,"bk_tenant_id":"tenant","type":"suppression","id":"p1","expected_version":0,"operation_id":"create","spec":{"name":"test","is_enable":true,"updated_at":"2026-09-30T00:00:00Z","space_code":"bkcc__2","activate_times":[],"policy":{"expression":"A","A":{"condition":"term","target_key":"source_id","target_value":"source"}},"scheme":[{"type":"clip","count":2,"duration":60,"duration_type":"second"}]}}`
}

func policyHTTP(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	r.Header.Set("Internal-Token", testJWT(t, "admin"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPolicyHTTPPublicationAndTenantScope(t *testing.T) {
	docs := &policyDocuments{rows: map[string]policyDocument{}}
	handler := (&API{Policies: policy.NewService(docs), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}, WorkerToken: "worker"}}).Handler()
	create := policyRequestBody()
	w := policyHTTP(t, handler, http.MethodPut, "/api/v1/policies/suppression/p1", create)
	if w.Code != 200 {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	w = policyHTTP(t, handler, http.MethodPut, "/api/v1/policies/suppression/p1", create)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, test := range []struct {
		path string
		code int
	}{
		{"/api/v1/policies/suppression/p1?bk_tenant_id=tenant", 200},
		{"/api/v1/policies/suppression/p1?bk_tenant_id=other", 404},
		{"/api/v1/policies/shield/p1?bk_tenant_id=tenant", 404},
		{"/api/v1/policies/suppression/p1", 400},
		{"/api/v1/policies/suppression/p1?bk_tenant_id=tenant&bk_tenant_id=other", 400},
		{"/api/v1/policies/suppression/p1/releases/1?bk_tenant_id=tenant", 200},
		{"/api/v1/policies/suppression/p1/releases/2?bk_tenant_id=tenant", 404},
		{"/api/v1/policies/suppression/p1/releases/0?bk_tenant_id=tenant", 400},
		{"/api/v1/policies?bk_tenant_id=tenant&type=suppression&limit=17", 400},
		{"/api/v1/policies?bk_tenant_id=tenant&type=suppression&is_enable=yes", 400},
	} {
		w := policyHTTP(t, handler, http.MethodGet, test.path, "")
		if w.Code != test.code {
			t.Fatalf("%s: %d %s", test.path, w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable policy")
		}
	}
	// 过滤后的空页仍返回原始扫描游标，不能让后续页面永久不可达。
	w = policyHTTP(t, handler, http.MethodGet, "/api/v1/policies?bk_tenant_id=tenant&type=suppression&is_enable=false&limit=1", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"next":"p1"`) || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatalf("filtered cursor %s", w.Body.String())
	}
	deleted := `{"bk_tenant_id":"tenant","schema_version":1,"expected_version":1,"operation_id":"delete"}`
	for range 2 {
		w = policyHTTP(t, handler, http.MethodDelete, "/api/v1/policies/suppression/p1", deleted)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"deleted":true`) {
			t.Fatalf("delete %d %s", w.Code, w.Body.String())
		}
	}
	w = policyHTTP(t, handler, http.MethodPut, "/api/v1/policies/suppression/p1", strings.Replace(create, `"operation_id":"create"`, `"operation_id":"stale"`, 1))
	if w.Code != 409 {
		t.Fatalf("stale %d", w.Code)
	}
	docs.fail = true
	w = policyHTTP(t, handler, http.MethodGet, "/api/v1/policies/suppression/p1?bk_tenant_id=tenant", "")
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("unsafe failure %d %s", w.Code, w.Body.String())
	}
}

func TestPolicyHTTPRejectsInvalidBodiesAndWorkerCredentials(t *testing.T) {
	docs := &policyDocuments{rows: map[string]policyDocument{}}
	handler := (&API{Policies: policy.NewService(docs), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}, WorkerToken: "worker"}}).Handler()
	body := policyRequestBody()
	for _, bad := range []string{strings.Replace(body, `"id":"p1"`, `"id":"p2"`, 1), strings.Replace(body, `"type":"suppression"`, `"type":"merge"`, 1), strings.Replace(body, `"bk_tenant_id":"tenant"`, `"bk_tenant_id":"tenant","bk_tenant_id":"other"`, 1), strings.Replace(body, `"name":"test"`, `"name":"test","name":"other"`, 1), strings.Replace(body, `"schema_version":1`, `"schema_version":1,"unknown":true`, 1), body + `{}`, `{"padding":"` + strings.Repeat("x", 3<<20) + `"}`} {
		w := policyHTTP(t, handler, http.MethodPut, "/api/v1/policies/suppression/p1", bad)
		if w.Code != 400 {
			t.Fatalf("invalid body accepted %d", w.Code)
		}
	}
	if len(docs.rows) != 0 {
		t.Fatal("invalid request wrote documents")
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/v1/policies/suppression/p1", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer worker")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	unavailable := (&API{Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}}).Handler()
	w = policyHTTP(t, unavailable, http.MethodGet, "/api/v1/policies?bk_tenant_id=tenant&type=suppression", "")
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}
