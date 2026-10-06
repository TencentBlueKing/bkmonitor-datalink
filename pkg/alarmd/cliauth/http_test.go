// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package cliauth

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func noStoreManager(t *testing.T) *Manager {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	return newTestManager(t, client)
}

func trustedRequest(method, body string) *http.Request {
	r := httptest.NewRequest(method, grantsPath, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testAdminKey)

	r.Header.Set("Origin", "https://example.test")
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestGrantTrustAndRequestValidation(t *testing.T) {
	m := noStoreManager(t)
	tests := []struct {
		name   string
		modify func(*http.Request)
		body   string
		status int
		code   string
	}{
		{"no administrator key", func(r *http.Request) { r.Header.Del("Authorization") }, `{"confirm":true}`, 403, "admin_unauthorized"},
		{"wrong administrator key", func(r *http.Request) { r.Header.Set("Authorization", "Bearer incorrect") }, `{"confirm":true}`, 403, "admin_unauthorized"},
		{"two administrator keys", func(r *http.Request) { r.Header.Add("Authorization", "Bearer "+testAdminKey) }, `{"confirm":true}`, 403, "admin_unauthorized"},
		{"joined authorization", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+testAdminKey+", Bearer "+testAdminKey) }, `{"confirm":true}`, 403, "admin_unauthorized"},
		{"wrong authorization scheme", func(r *http.Request) { r.Header.Set("Authorization", "Basic "+testAdminKey) }, `{"confirm":true}`, 403, "admin_unauthorized"},
		{"bearer cannot mint", func(r *http.Request) { r.Header.Set("Authorization", "Bearer credential") }, `{"confirm":true}`, 403, "admin_unauthorized"},
		{"no origin", func(r *http.Request) { r.Header.Del("Origin") }, `{"confirm":true}`, 403, "origin_denied"},
		{"null origin", func(r *http.Request) { r.Header.Set("Origin", "null") }, `{"confirm":true}`, 403, "origin_denied"},
		{"wrong scheme", func(r *http.Request) { r.Header.Set("Origin", "http://example.test") }, `{"confirm":true}`, 403, "origin_denied"},
		{"suffix origin", func(r *http.Request) { r.Header.Set("Origin", "https://example.test.evil.test") }, `{"confirm":true}`, 403, "origin_denied"},
		{"two origins", func(r *http.Request) { r.Header.Add("Origin", "https://example.test") }, `{"confirm":true}`, 403, "origin_denied"},
		{"joined origins", func(r *http.Request) { r.Header.Set("Origin", "https://example.test, https://evil.test") }, `{"confirm":true}`, 403, "origin_denied"},
		{"no MIME", func(r *http.Request) { r.Header.Del("Content-Type") }, `{"confirm":true}`, 415, "invalid_content_type"},
		{"form MIME", func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }, `confirm=true`, 415, "invalid_content_type"},
		{"two MIME", func(r *http.Request) { r.Header.Add("Content-Type", "application/json") }, `{"confirm":true}`, 415, "invalid_content_type"},
		{"no confirmation", nil, `{}`, 400, "invalid_request"},
		{"false confirmation", nil, `{"confirm":false}`, 400, "invalid_request"},
		{"unknown field", nil, `{"confirm":true,"scope":"admin"}`, 400, "invalid_request"},
		{"wrong type", nil, `{"confirm":"true"}`, 400, "invalid_request"},
		{"trailing JSON", nil, `{"confirm":true}{}`, 400, "invalid_request"},
		{"oversize trailing whitespace", nil, `{"confirm":true}` + strings.Repeat(" ", maxBodyBytes), 413, "request_too_large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := trustedRequest(http.MethodPost, tt.body)
			if tt.modify != nil {
				tt.modify(r)
			}
			w := httptest.NewRecorder()
			m.Handler().ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("status=%d, want=%d body=%s", w.Code, tt.status, w.Body)
			}
			var payload struct {
				Status string `json:"status"`
				Error  Error  `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Status != "error" || payload.Error.Code != tt.code {
				t.Fatalf("payload=%+v", payload)
			}
			if strings.Contains(w.Body.String(), testAdminKey) || strings.Contains(w.Body.String(), "credential") {
				t.Fatal("error echoed a request credential")
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("cache or CORS response policy violated")
			}
		})
	}
}

func TestPreviewDoesNotCreateGrantOrUseRedis(t *testing.T) {
	m := noStoreManager(t)
	for i := 0; i < 10; i++ {
		response := authRequest(m, http.MethodGet, grantsPath, "", true)
		if response.Code != 200 || strings.Contains(response.Body.String(), "authorization_code") || strings.Contains(response.Body.String(), testAdminKey) {
			t.Fatal("invalid grant preview")
		}
		var preview grantPreview
		if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
			t.Fatal(err)
		}
		if preview.Scope != ScopeReadonly || preview.SessionTTLSeconds != 3600 || preview.GrantTTLSeconds != 300 {
			t.Fatalf("preview=%+v", preview)
		}
	}
	if m.grantWindow.count != 0 {
		t.Fatal("preview consumed issuance budget")
	}
	m.adminConfigured = false
	response := authRequest(m, http.MethodGet, grantsPath, "", true)
	if response.Code != 503 || !strings.Contains(response.Body.String(), "admin_not_configured") {
		t.Fatal("unconfigured administrator accepted")
	}
}

func TestHTTPBudgetsAndMethodBoundaries(t *testing.T) {
	client := startRedis(t)
	m := newTestManager(t, client)
	for i := 0; i < 6; i++ {
		issue(t, m)
	}
	if seventh := authRequest(m, http.MethodPost, grantsPath, `{"confirm":true}`, true); seventh.Code != 429 {
		t.Fatalf("issuance budget status=%d", seventh.Code)
	}
	if count := len(client.Keys(context.Background(), m.prefix+"grant:*").Val()); count != 6 {
		t.Fatalf("issued %d grants", count)
	}
	m.now = func() time.Time { return time.Unix(61, 0) }
	issue(t, m)
	for i := 0; i < 60; i++ {
		response := authRequest(m, http.MethodPost, exchangePath, `{}`, false)
		if response.Code != 401 {
			t.Fatalf("exchange attempt %d status=%d", i, response.Code)
		}
	}
	if response := authRequest(m, http.MethodPost, exchangePath, `{}`, false); response.Code != 429 {
		t.Fatal("exchange budget not enforced")
	}
	for i := 0; i < cap(m.httpSlots); i++ {
		m.httpSlots <- struct{}{}
	}
	if response := authRequest(m, http.MethodGet, grantsPath, "", true); response.Code != 429 {
		t.Fatal("concurrency budget not enforced")
	}
	for i := 0; i < cap(m.httpSlots); i++ {
		<-m.httpSlots
	}
	for _, path := range []string{grantsPath, exchangePath, sessionPath} {
		if response := authRequest(m, http.MethodOptions, path, "", true); response.Code != 405 || response.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("CORS preflight accepted")
		}
	}
	if response := authRequest(m, http.MethodGet, sessionPath, "", true); response.Code != 401 {
		t.Fatal("administrator key acted as bearer")
	}
	if response := authRequest(m, http.MethodGet, "/api/cli/channel", "", true); response.Code != 404 {
		t.Fatal("auth handler served a channel request")
	}
}

func TestNewValidatesCoordinatesWithoutExposingSecrets(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	defer client.Close()
	base := Options{Redis: client, Prefix: "state", EnvironmentID: "environment", EnvironmentName: "Environment",
		PublicBaseURL: "https://example.test/prefix", AdminKey: testAdminKey}
	m, err := New(base)
	if err != nil || m.publicBaseURL != "https://example.test/prefix/" {
		t.Fatalf("New=%v err=%v", m, err)
	}
	for _, invalidURL := range []string{"", "//example.test", "ftp://example.test/", "https://user:secret@example.test/", "https://example.test/?token=secret", "https://example.test/#fragment", "https://example.test/../bad", "https://example.test/%2e%2e/bad", "https://example.test/a%2f..%2fb", "http://user:secret@example.test/", "http://example.test/?token=secret", "http://example.test/#fragment", "http://example.test/../bad"} {
		t.Run(invalidURL, func(t *testing.T) {
			opts := base
			opts.PublicBaseURL = invalidURL
			if _, err := New(opts); err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid URL result=%v", err)
			}
		})
	}
	for _, mutate := range []func(*Options){
		func(o *Options) { o.Redis = nil },
		func(o *Options) { o.Prefix = "a{wrong-slot}" },
		func(o *Options) { o.EnvironmentID = "" },
		func(o *Options) { o.EnvironmentName = "" },
		func(o *Options) { o.AdminKey = "weak" },
	} {
		opts := base
		mutate(&opts)
		if _, err := New(opts); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
	base.AdminKey = ""
	if _, err := New(base); err != nil {
		t.Fatalf("disabled issuance rejected: %v", err)
	}
	base.EnvironmentID = "env{unexpected-slot}"
	m, err = New(base)
	if err != nil || strings.Count(m.prefix, "{") != 1 || strings.Contains(m.prefix, base.EnvironmentID) {
		t.Fatal("environment changed Redis hash tag")
	}
}

func TestDeploymentURLUsesBrowserOrigin(t *testing.T) {
	client := startRedis(t)
	for _, tt := range []struct {
		configured string
		origin     string
	}{
		{"http://EXAMPLE.TEST:80/alarmd", "http://example.test"},
		{"https://EXAMPLE.TEST:443/alarmd", "https://example.test"},
		{"https://EXAMPLE.TEST/alarmd", "https://example.test"},
		{"http://EXAMPLE.TEST:8080/alarmd", "http://example.test:8080"},
		{"https://EXAMPLE.TEST:8443/alarmd", "https://example.test:8443"},
		{"http://[2001:DB8::1]:80/alarmd", "http://[2001:db8::1]"},
		{"https://[2001:DB8::1]:443/alarmd", "https://[2001:db8::1]"},
		{"https://[2001:DB8::1]:8443/alarmd", "https://[2001:db8::1]:8443"},
	} {
		t.Run(tt.configured, func(t *testing.T) {
			m, err := New(Options{Redis: client, Prefix: "origin-fixture", EnvironmentID: "origin-environment",
				EnvironmentName: "Origin fixture", PublicBaseURL: tt.configured, AdminKey: testAdminKey})
			if err != nil {
				t.Fatal(err)
			}
			wantURL := tt.origin + "/alarmd/"
			if m.publicBaseURL != wantURL || m.origin != tt.origin {
				t.Fatalf("deployment URL=%q origin=%q", m.publicBaseURL, m.origin)
			}
			w := authRequest(m, http.MethodGet, grantsPath, "", true)
			var preview grantPreview
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.PublicBaseURL != wantURL {
				t.Fatal("grant preview did not use the normalized URL")
			}
			crossScheme := strings.Replace(tt.origin, "http://", "https://", 1)
			if strings.HasPrefix(tt.origin, "https://") {
				crossScheme = strings.Replace(tt.origin, "https://", "http://", 1)
			}
			for _, origin := range []string{crossScheme, "http://evil.test", "https://evil.test", tt.origin + ".evil.test"} {
				r := trustedRequest(http.MethodPost, `{"confirm":true}`)
				r.Header.Set("Origin", origin)
				w = httptest.NewRecorder()
				m.Handler().ServeHTTP(w, r)
				var rejected struct {
					Error Error `json:"error"`
				}
				if w.Code != http.StatusForbidden || json.Unmarshal(w.Body.Bytes(), &rejected) != nil || rejected.Error.Code != "origin_denied" {
					t.Fatalf("foreign Origin %q was not rejected", origin)
				}
			}
			r := trustedRequest(http.MethodPost, `{"confirm":true}`)
			r.Header.Set("Origin", tt.origin)
			w = httptest.NewRecorder()
			m.Handler().ServeHTTP(w, r)
			var issued struct {
				AuthorizationCode string `json:"authorization_code"`
				PublicBaseURL     string `json:"public_base_url"`
			}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &issued) != nil || issued.PublicBaseURL != wantURL {
				t.Fatal("browser Origin did not authorize a grant with the normalized URL")
			}
			raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(issued.AuthorizationCode, "alarmd-login-v1."))
			var grant authorizationPackage
			if err != nil || json.Unmarshal(raw, &grant) != nil || grant.PublicBaseURL != wantURL {
				t.Fatal("authorization package did not retain the normalized URL")
			}
			w = exchange(m, grant)
			var exchanged exchangeResponse
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &exchanged) != nil || exchanged.PublicBaseURL != wantURL {
				t.Fatal("exchange did not retain the normalized URL")
			}
		})
	}
}

func TestHTTPDeploymentPreservesURLAndRequiresMatchingOrigin(t *testing.T) {
	client := startRedis(t)
	server := httptest.NewUnstartedServer(nil)
	publicBaseURL := "http://" + server.Listener.Addr().String() + "/alarmd/"
	m, err := New(Options{Redis: client, Prefix: "http-fixture", EnvironmentID: "http-environment",
		EnvironmentName: "HTTP fixture", PublicBaseURL: publicBaseURL, AdminKey: testAdminKey})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = http.StripPrefix("/alarmd", m.Handler())
	server.Start()
	defer server.Close()
	request := func(method, path, origin, body string, trusted bool) (int, []byte) {
		t.Helper()
		r, err := http.NewRequest(method, publicBaseURL+strings.TrimPrefix(path, "/"), strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		if trusted {
			r.Header.Set("Authorization", "Bearer "+testAdminKey)

		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal("HTTP fixture request failed")
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal("cannot read HTTP fixture response")
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("HTTP authorization response lost no-store")
		}
		return response.StatusCode, data
	}
	status, body := request(http.MethodGet, grantsPath, "", "", true)
	var preview grantPreview
	if status != http.StatusOK || json.Unmarshal(body, &preview) != nil || preview.PublicBaseURL != publicBaseURL || preview.Scope != ScopeReadonly {
		t.Fatal("HTTP grant preview did not preserve deployment URL and scope")
	}
	wrongOrigin := strings.Replace(server.URL, "http://", "https://", 1)
	status, body = request(http.MethodPost, grantsPath, wrongOrigin, `{"confirm":true}`, true)
	var rejected struct {
		Error Error `json:"error"`
	}
	if status != http.StatusForbidden || json.Unmarshal(body, &rejected) != nil || rejected.Error.Code != "origin_denied" {
		t.Fatal("HTTPS Origin was accepted for an HTTP deployment")
	}
	status, _ = request(http.MethodPost, grantsPath, server.URL, `{"confirm":true}`, false)
	if status != http.StatusForbidden {
		t.Fatal("HTTP configuration bypassed administrator authentication")
	}
	status, body = request(http.MethodPost, grantsPath, server.URL, `{"confirm":true}`, true)
	var issued struct {
		AuthorizationCode string `json:"authorization_code"`
		PublicBaseURL     string `json:"public_base_url"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &issued) != nil || issued.PublicBaseURL != publicBaseURL {
		t.Fatal("HTTP grant issuance did not preserve deployment URL")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(issued.AuthorizationCode, "alarmd-login-v1."))
	var grant authorizationPackage
	if err != nil || json.Unmarshal(raw, &grant) != nil || grant.PublicBaseURL != publicBaseURL || !validSecret(grant.GrantSecret) {
		t.Fatal("HTTP authorization package did not retain the deployment URL")
	}
	input, _ := json.Marshal(map[string]string{"environment_id": grant.EnvironmentID, "grant_secret": grant.GrantSecret})
	status, body = request(http.MethodPost, exchangePath, "", string(input), false)
	var exchanged exchangeResponse
	if status != http.StatusOK || json.Unmarshal(body, &exchanged) != nil || exchanged.PublicBaseURL != publicBaseURL || !validSecret(exchanged.AccessToken) {
		t.Fatal("HTTP exchange did not preserve the deployment URL or create a token")
	}
	if _, err := m.Authenticate(context.Background(), exchanged.AccessToken); err != nil {
		t.Fatal("HTTP exchange did not create a valid Redis session")
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *deadlineRecorder) SetReadDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func TestBodyReadDeadlineIsLimitedToTheRoute(t *testing.T) {
	m := noStoreManager(t)
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	before := time.Now()
	m.Handler().ServeHTTP(w, trustedRequest(http.MethodPost, `{"confirm":false}`))
	if len(w.deadlines) != 2 || w.deadlines[0].Before(before.Add(2*time.Second)) || w.deadlines[0].After(before.Add(4*time.Second)) || !w.deadlines[1].IsZero() {
		t.Fatalf("deadlines=%v, want a bounded read followed by reset", w.deadlines)
	}
}

func TestSlowBodyReleasesHTTPAdmissionSlot(t *testing.T) {
	m := noStoreManager(t)
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: example.test\r\nOrigin: https://example.test\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: 16\r\n\r\n{", grantsPath, testAdminKey)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatalf("slow body did not receive a bounded response: %v", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("slow body status=%d", response.StatusCode)
	}
	if len(m.httpSlots) != 0 {
		t.Fatal("slow body retained an authorization slot")
	}
}

func TestSaturatedHandlerDoesNotWaitForRejectedBody(t *testing.T) {
	m := noStoreManager(t)
	for i := 0; i < cap(m.httpSlots); i++ {
		m.httpSlots <- struct{}{}
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: example.test\r\nContent-Length: 16\r\n\r\n{", grantsPath)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatalf("busy handler waited for body: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("busy status=%d", response.StatusCode)
	}
}

// Only the Authorization header carries the administrator key; the key is
// never echoed back.
func TestTheGrantPreviewNeedsTheAdministratorKeyAndNeverEchoesIt(t *testing.T) {
	m := noStoreManager(t)
	for _, authenticated := range []bool{false, true} {
		r := httptest.NewRequest(http.MethodGet, grantsPath, nil)
		if authenticated {
			r.Header.Set("Authorization", "Bearer "+testAdminKey)
		}
		w := httptest.NewRecorder()
		m.Handler().ServeHTTP(w, r)
		want := 403
		if authenticated {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("authenticated=%v status=%d", authenticated, w.Code)
		}
		if strings.Contains(w.Body.String(), testAdminKey) {
			t.Fatal("administrator key leaked into response")
		}
	}
}

func TestAdministratorAndSessionKeysAreNotInterchangeable(t *testing.T) {
	client := startRedis(t)
	m := newTestManager(t, client)
	response := login(t, m)
	r := trustedRequest(http.MethodGet, "")
	r.Header.Set("Authorization", "Bearer "+response.AccessToken)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("session token authorized grant issuance")
	}
	if _, err := m.Authenticate(context.Background(), testAdminKey); err == nil {
		t.Fatal("administrator key authorized a session")
	}
}

func TestAdministratorKeyHeaderCompatibleBounds(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	defer client.Close()
	for _, tt := range []struct {
		key string
		ok  bool
	}{
		{strings.Repeat("a", 32), true}, {strings.Repeat("~", 256), true},
		{strings.Repeat("a", 31), false}, {strings.Repeat("a", 257), false},
		{strings.Repeat("a", 32) + " b", false}, {strings.Repeat("a", 32) + "中", false},
		{strings.Repeat("a", 32) + "\x7f", false},
	} {
		m, err := New(Options{Redis: client, Prefix: "bounds", EnvironmentID: "test", EnvironmentName: "test", PublicBaseURL: "http://example.test/", AdminKey: tt.key})
		if (err == nil) != tt.ok {
			t.Fatalf("key length %d ok=%v error=%v", len(tt.key), tt.ok, err)
		}
		if err != nil {
			continue
		}
		r := httptest.NewRequest(http.MethodGet, grantsPath, nil)
		r.Header.Set("Authorization", "Bearer "+tt.key)
		w := httptest.NewRecorder()
		m.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("length %d could not authorize: %d", len(tt.key), w.Code)
		}
	}
}
