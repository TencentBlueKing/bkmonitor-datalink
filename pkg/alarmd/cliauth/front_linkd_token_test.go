// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package cliauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdtoken"
)

// adminToken is a token shaped as the admin's cliTokens().issue writes it,
// issued at issued: 90 days, replaced in the last 7.
func adminToken(issued time.Time) string {
	exp := issued.Add(90 * 24 * time.Hour).Unix()
	claims, _ := json.Marshal(map[string]any{"v": 1, "sub": "admin", "iss": "admin/", "iat": issued.Unix(),
		"exp": exp, "rfa": exp - 7*86400, "cv": "fingerprint", "jti": "jti"})
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"cli+jwt"}`)) + "." +
		base64.RawURLEncoding.EncodeToString(claims) + "." + strings.Repeat("s", 43)
}

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

// The admin hands the token over and reads its state with the
// administrator key; the answer is the state and expiry, never the token.
func TestTheAdminHandsTheLinkTokenOverOnTheFrontSurface(t *testing.T) {
	client := startRedis(t)
	if client == nil {
		t.Skip("no redis-server")
	}
	m := newTestManager(t, client)
	now := time.Unix(1_800_000_000, 0)
	store, err := linkdtoken.New(client, "test-alarmd", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if w := frontRequest(m, http.MethodGet, "", testAdminKey); w.Code != http.StatusNotFound ||
		!strings.Contains(w.Body.String(), "linkd_token_unused") {
		t.Fatalf("a deployment keeping no token answered %d %s", w.Code, w.Body)
	}
	m.ServeLinkdTokens(store)
	if w := frontRequest(m, http.MethodGet, "", testAdminKey); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"state":"none"}` {
		t.Fatalf("before a hand-over: %d %s", w.Code, w.Body)
	}
	token := adminToken(now)
	body, _ := json.Marshal(map[string]string{"token": token})
	w := frontRequest(m, http.MethodPost, string(body), testAdminKey)
	want := `{"state":"valid","expires_at":"` + now.Add(90*24*time.Hour).UTC().Format(time.RFC3339) + `"}`
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != want {
		t.Fatalf("hand-over answered %d %s, want %s", w.Code, w.Body, want)
	}
	if got, err := store.Token(context.Background()); err != nil || got != token {
		t.Fatalf("the handed token is not the one used: %v", err)
	}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"no key":        frontRequest(m, http.MethodPost, string(body), ""),
		"another key":   frontRequest(m, http.MethodGet, "", "another-key"),
		"invalid token": frontRequest(m, http.MethodPost, `{"token":"a.b"}`, testAdminKey),
		"extra field":   frontRequest(m, http.MethodPost, `{"token":"a.b.c","rfa":1}`, testAdminKey),
		"wrong method":  frontRequest(m, http.MethodDelete, "", testAdminKey),
	} {
		wantCode := map[string]int{"no key": 403, "another key": 403, "invalid token": 400, "extra field": 400, "wrong method": 405}[name]
		if w.Code != wantCode {
			t.Errorf("%s answered %d %s, want %d", name, w.Code, w.Body, wantCode)
		}
		if strings.Contains(w.Body.String(), token) {
			t.Errorf("%s echoed the token", name)
		}
	}
	if w := frontRequest(m, http.MethodPost, `{"token":"a.b"}`, testAdminKey); !strings.Contains(w.Body.String(), `"invalid_token"`) {
		t.Fatalf("an invalid token is not named invalid_token: %s", w.Body)
	}
}
