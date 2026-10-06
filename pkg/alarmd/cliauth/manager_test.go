// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package cliauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

const testAdminKey = "fixture-only-administrator-key-0123456789abcdef"

func startRedis(t *testing.T) *redis.Client {
	t.Helper()
	path, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path, "--bind", "127.0.0.1", "--port", port,
		"--save", "", "--appendonly", "no", "--dir", t.TempDir(), "--loglevel", "warning")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	client := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close() })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if client.Ping(context.Background()).Err() == nil {
			return client
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("test Redis did not start")
	return nil
}

func newTestManager(t *testing.T, client redis.UniversalClient) *Manager {
	t.Helper()
	m, err := New(Options{Redis: client, Prefix: "test-alarmd", EnvironmentID: "test-env",
		EnvironmentName: "Test environment", PublicBaseURL: "https://example.test/alarmd/",
		AdminKey: testAdminKey, Now: func() time.Time { return time.Unix(1, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func authRequest(m *Manager, method, path, body string, trusted bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if trusted {
		r.Header.Set("Authorization", "Bearer "+testAdminKey)

		r.Header.Set("Origin", "https://example.test")
	}
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	return w
}

func issue(t *testing.T, m *Manager) authorizationPackage {
	t.Helper()
	w := authRequest(m, http.MethodPost, grantsPath, `{"confirm":true}`, true)
	if w.Code != http.StatusOK {
		t.Fatalf("issue status=%d, body=%s", w.Code, w.Body)
	}
	var response struct {
		AuthorizationCode string `json:"authorization_code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	const prefix = "alarmd-login-v1."
	if !strings.HasPrefix(response.AuthorizationCode, prefix) {
		t.Fatal("missing code protocol prefix")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(response.AuthorizationCode, prefix))
	if err != nil {
		t.Fatal(err)
	}
	var grant authorizationPackage
	if err := json.Unmarshal(raw, &grant); err != nil {
		t.Fatal(err)
	}
	if grant.Version != "alarmd-login/v1" || !validSecret(grant.GrantSecret) {
		t.Fatal("invalid grant package")
	}
	return grant
}

func exchange(m *Manager, grant authorizationPackage) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"environment_id": grant.EnvironmentID, "grant_secret": grant.GrantSecret})
	return authRequest(m, http.MethodPost, exchangePath, string(body), false)
}

type exchangeResponse struct {
	EnvironmentID   string    `json:"environment_id"`
	EnvironmentName string    `json:"environment_name"`
	PublicBaseURL   string    `json:"public_base_url"`
	AccessToken     string    `json:"access_token"`
	SessionID       string    `json:"session_id"`
	ExpiresAt       time.Time `json:"expires_at"`
	Scope           string    `json:"scope"`
}

func login(t *testing.T, m *Manager) exchangeResponse {
	t.Helper()
	w := exchange(m, issue(t, m))
	if w.Code != http.StatusOK {
		t.Fatalf("exchange status=%d body=%s", w.Code, w.Body)
	}
	var response exchangeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func requireCode(t *testing.T, err error, want string) {
	t.Helper()
	if ErrorCode(err) != want {
		t.Fatalf("error code=%q, want=%q, error=%v", ErrorCode(err), want, err)
	}
}

func TestGrantAndSessionRoundTrip(t *testing.T) {
	client := startRedis(t)
	m := newTestManager(t, client)
	ctx := context.Background()
	before, err := client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	grant := issue(t, m)
	if grant.EnvironmentID != m.environmentID || grant.PublicBaseURL != m.publicBaseURL {
		t.Fatal("wrong grant environment")
	}
	if grant.GrantExpiresAt.Before(before.Add(GrantLifetime-time.Second)) || grant.GrantExpiresAt.After(time.Now().Add(GrantLifetime+time.Second)) {
		t.Fatal("grant deadline did not use Redis time")
	}
	grantKey := m.prefix + "grant:" + digest(grant.GrantSecret)
	ttl, err := client.PTTL(ctx, grantKey).Result()
	if err != nil || ttl <= GrantLifetime-time.Second || ttl > GrantLifetime {
		t.Fatalf("grant TTL=%v err=%v", ttl, err)
	}
	w := exchange(m, grant)
	if w.Code != 200 {
		t.Fatalf("exchange=%d %s", w.Code, w.Body)
	}
	var response exchangeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !validSecret(response.AccessToken) || !validSecret(response.SessionID) || response.Scope != ScopeReadonly || response.EnvironmentName != m.environmentName {
		t.Fatal("invalid session response")
	}
	if client.Exists(ctx, grantKey).Val() != 0 {
		t.Fatal("grant was not consumed")
	}
	session, err := m.Authenticate(ctx, response.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != response.SessionID || session.Renewed || !session.ExpiresAt.Equal(response.ExpiresAt) {
		t.Fatalf("session=%+v", session)
	}
	if session.ExpiresAt.Before(before.Add(SessionLifetime - time.Second)) {
		t.Fatal("session used local test clock")
	}
	key := m.prefix + "session:" + digest(response.AccessToken)
	ttl = client.PTTL(ctx, key).Val()
	if ttl <= SessionLifetime-time.Second || ttl > SessionLifetime {
		t.Fatalf("session TTL=%v", ttl)
	}
	keys, err := client.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		raw := client.Get(ctx, key).Val()
		for _, secret := range []string{grant.GrantSecret, response.AccessToken, testAdminKey} {
			if strings.Contains(key, secret) || strings.Contains(raw, secret) {
				t.Fatal("stored raw credential")
			}
		}
	}
	r := httptest.NewRequest(http.MethodGet, sessionPath, nil)
	r.Header.Set("Authorization", "Bearer "+response.AccessToken)
	status := httptest.NewRecorder()
	m.Handler().ServeHTTP(status, r)
	if status.Code != 200 || status.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("session status failed")
	}
	if strings.Contains(status.Body.String(), response.AccessToken) || strings.Contains(status.Body.String(), session.TokenHash) {
		t.Fatal("session status leaked a credential")
	}
	var after Session
	if err := json.Unmarshal(status.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if !after.ExpiresAt.Equal(session.ExpiresAt) {
		t.Fatal("status renewed the session")
	}
	if reused := exchange(m, grant); reused.Code != 401 {
		t.Fatalf("reused grant status=%d", reused.Code)
	}
}

func TestConcurrentExchangeHasOneWinnerAcrossManagers(t *testing.T) {
	client := startRedis(t)
	m := newTestManager(t, client)
	other := newTestManager(t, client)
	grant := issue(t, m)
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			manager := m
			if i%2 == 0 {
				manager = other
			}
			response := exchange(manager, grant)
			if response.Code == 200 {
				winners.Add(1)
			} else if response.Code != 401 && response.Code != 429 {
				t.Errorf("unexpected exchange status=%d body=%s", response.Code, response.Body)
			}
		}(i)
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("successful exchanges=%d", winners.Load())
	}
	keys := client.Keys(context.Background(), m.prefix+"session:*").Val()
	if len(keys) != 1 {
		t.Fatalf("session count=%d", len(keys))
	}
}

func TestFailedSessionCreationDoesNotConsumeGrant(t *testing.T) {
	client := startRedis(t)
	m := newTestManager(t, client)
	grant := issue(t, m)
	ctx := context.Background()
	grantKey := m.prefix + "grant:" + digest(grant.GrantSecret)
	sessionKey := m.prefix + "session:collision"
	if err := client.Set(ctx, sessionKey, "existing", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	result, err := m.run(ctx, exchangeScript, []string{grantKey, sessionKey, m.prefix + "pairing:candidate", m.pairingsKey(), m.epochKey()},
		m.environmentID, "candidate-id", ScopeReadonly, SessionLifetime.Milliseconds(),
		"candidate-pairing", PairingIdleLifetime.Milliseconds(), MaxPairings, m.adminBinding(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0] != int64(2) {
		t.Fatalf("collision result=%v", result)
	}
	if client.Exists(ctx, grantKey).Val() != 1 || client.Get(ctx, sessionKey).Val() != "existing" ||
		client.Exists(ctx, m.prefix+"pairing:candidate").Val() != 0 || client.ZCard(ctx, m.pairingsKey()).Val() != 0 {
		t.Fatal("failed create consumed grant or overwrote session")
	}
	if response := exchange(m, grant); response.Code != 200 {
		t.Fatalf("grant no longer exchangeable: %d", response.Code)
	}
}

// setRemaining changes the persisted deadline, not the process clock. This
// exercises real Redis expiry decisions without a ten-minute sleep.
func setRemaining(t *testing.T, m *Manager, session Session, remaining time.Duration) {
	t.Helper()
	ctx := context.Background()
	const script = `local t = redis.call('TIME')
local record = cjson.decode(redis.call('GET', KEYS[1]))
record.expires_at_ms = tonumber(t[1])*1000 + math.floor(tonumber(t[2])/1000) + tonumber(ARGV[1])
redis.call('SET', KEYS[1], cjson.encode(record), 'PX', 3600000)
return 1`
	if err := m.client.Eval(ctx, script, []string{m.prefix + "session:" + session.TokenHash}, remaining.Milliseconds()).Err(); err != nil {
		t.Fatal(err)
	}
}

func TestAdmissionUsesRedisClockAndNeverResurrects(t *testing.T) {
	client := startRedis(t)
	m := newTestManager(t, client)
	ctx := context.Background()
	response := login(t, m)
	session, err := m.Authenticate(ctx, response.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	setRemaining(t, m, session, RenewalThreshold+time.Minute)
	before, err := m.Authenticate(ctx, response.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	notDue, err := m.Admit(ctx, before, true)
	if err != nil || notDue.Renewed || !notDue.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatalf("early renewal=%+v %v", notDue, err)
	}
	setRemaining(t, m, session, RenewalThreshold)
	due, err := m.Authenticate(ctx, response.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := m.Admit(ctx, due, false)
	if err != nil || unchanged.Renewed || !unchanged.ExpiresAt.Equal(due.ExpiresAt) {
		t.Fatal("renew=false extended the session")
	}
	var renewals atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			renewed, err := m.Admit(ctx, due, true)
			if err != nil {
				t.Error(err)
				return
			}
			if renewed.Renewed {
				renewals.Add(1)
			}
			if renewed.ID != due.ID || renewed.TokenHash != due.TokenHash {
				t.Error("renewal rotated session identity")
			}
		}()
	}
	wg.Wait()
	if renewals.Load() != 1 {
		t.Fatalf("renewals=%d, want 1", renewals.Load())
	}
	renewed, err := m.Authenticate(ctx, response.AccessToken)
	if err != nil || !renewed.ExpiresAt.After(due.ExpiresAt) {
		t.Fatal("same bearer did not survive renewal")
	}
	setRemaining(t, m, session, -time.Millisecond)
	_, err = m.Authenticate(ctx, response.AccessToken)
	requireCode(t, err, "auth_expired_or_revoked")
	_, err = m.Admit(ctx, due, true)
	requireCode(t, err, "auth_expired_or_revoked")
	other := login(t, m)
	if other.SessionID == session.ID {
		t.Fatal("new login reused session id")
	}
	active, err := m.Authenticate(ctx, other.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodDelete, sessionPath, nil)
	r.Header.Set("Authorization", "Bearer "+other.AccessToken)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("revoke=%d %s", w.Code, w.Body)
	}
	_, err = m.Admit(ctx, active, true)
	requireCode(t, err, "auth_expired_or_revoked")
	if client.Exists(ctx, m.prefix+"session:"+active.TokenHash).Val() != 0 {
		t.Fatal("admission recreated revoked key")
	}
	_, err = m.Authenticate(ctx, other.AccessToken)
	requireCode(t, err, "auth_expired_or_revoked")
}

func TestGrantExpiryAndWrongEnvironment(t *testing.T) {
	client := startRedis(t)
	m := newTestManager(t, client)
	grant := issue(t, m)
	wrong := grant
	wrong.EnvironmentID = "other-environment"
	if result := exchange(m, wrong); result.Code != 401 {
		t.Fatal("wrong environment accepted")
	}
	ctx := context.Background()
	key := m.prefix + "grant:" + digest(grant.GrantSecret)
	if client.Exists(ctx, key).Val() != 1 {
		t.Fatal("wrong environment consumed grant")
	}
	now := client.Time(ctx).Val()
	if err := client.PExpireAt(ctx, key, now.Add(-time.Second)).Err(); err != nil {
		t.Fatal(err)
	}
	if result := exchange(m, grant); result.Code != 401 {
		t.Fatal("expired grant accepted")
	}
	if keys := client.Keys(ctx, m.prefix+"session:*").Val(); len(keys) != 0 {
		t.Fatal("expired grant created a session")
	}
	// The record deadline also rejects expiry if a key's Redis TTL is longer.
	grant = issue(t, m)
	key = m.prefix + "grant:" + digest(grant.GrantSecret)
	raw := client.Get(ctx, key).Val()
	var record storedRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatal(err)
	}
	record.ExpiresAtMS = now.Add(-time.Second).UnixMilli()
	encoded, _ := json.Marshal(record)
	if err := client.Set(ctx, key, encoded, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if result := exchange(m, grant); result.Code != 401 {
		t.Fatal("stale grant record accepted")
	}
}

func TestConcurrentRevocationAndRenewalCannotRestoreSession(t *testing.T) {
	client := startRedis(t)
	m := newTestManager(t, client)
	response := login(t, m)
	ctx := context.Background()
	session, err := m.Authenticate(ctx, response.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	setRemaining(t, m, session, time.Minute)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := m.Admit(ctx, session, true)
			if err != nil && ErrorCode(err) != "auth_expired_or_revoked" {
				t.Error(err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		_, err := m.sessionOperation(ctx, session.TokenHash, session.ID, "delete")
		if err != nil {
			t.Error(err)
		}
	}()
	close(start)
	wg.Wait()
	if client.Exists(ctx, m.prefix+"session:"+session.TokenHash).Val() != 0 {
		t.Fatal("renewal restored a concurrently revoked session")
	}
	_, err = m.Authenticate(ctx, response.AccessToken)
	requireCode(t, err, "auth_expired_or_revoked")
}

func TestRedisErrorsAreSafeAndFailClosed(t *testing.T) {
	client := startRedis(t)
	m := newTestManager(t, client)
	response := login(t, m)
	session, err := m.Authenticate(context.Background(), response.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = m.Authenticate(context.Background(), response.AccessToken)
	requireCode(t, err, "auth_store_unavailable")
	_, err = m.Admit(context.Background(), session, true)
	requireCode(t, err, "auth_store_unavailable")
	if strings.Contains(err.Error(), response.AccessToken) || strings.Contains(err.Error(), "redis:") {
		t.Fatal("raw store error or secret leaked")
	}
	var public *Error
	if !errors.As(err, &public) || public.HTTPStatus != 503 {
		t.Fatal("safe error unavailable to caller")
	}
}
