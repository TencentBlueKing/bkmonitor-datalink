// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package cliauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func frontRequest(m *Manager, method, body string, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, frontLinkdTokenPath, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	m.FrontHandler().ServeHTTP(w, r)
	return w
}

// alarmd reads the link through its control plane, never through the
// admin: an admin handing a token over is told none is kept, and only with
// the administrator key.
func TestTheFrontSurfaceKeepsNoLinkToken(t *testing.T) {
	client := startRedis(t)
	if client == nil {
		t.Skip("no redis-server")
	}
	m := newTestManager(t, client)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if w := frontRequest(m, method, `{"token":"x"}`, testAdminKey); w.Code != http.StatusNotFound ||
			!strings.Contains(w.Body.String(), "linkd_token_unused") {
			t.Fatalf("%s answered %d %s", method, w.Code, w.Body)
		}
	}
	if w := frontRequest(m, http.MethodGet, "", ""); w.Code == http.StatusNotFound {
		t.Fatal("answered without the administrator key")
	}
}
