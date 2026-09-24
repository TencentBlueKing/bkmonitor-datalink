// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package cliauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

type loginBody struct {
	AccessToken        string `json:"access_token"`
	SessionID          string `json:"session_id"`
	RefreshToken       string `json:"refresh_token"`
	PairingID          string `json:"pairing_id"`
	PairingIdleSeconds int64  `json:"pairing_idle_seconds"`
	Pairing            string `json:"pairing"`
}

func decodeLogin(t *testing.T, w *httptest.ResponseRecorder) loginBody {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var body loginBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func refresh(m *Manager, token string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"environment_id": m.environmentID, "refresh_token": token})
	return authRequest(m, http.MethodPost, refreshPath, string(body), false)
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.Error.Code
}

func pairedLogin(t *testing.T, m *Manager) loginBody {
	t.Helper()
	return decodeLogin(t, exchange(m, issue(t, m)))
}

// The key is entered once: the exchange carries a renewal credential, and
// spending it returns a working session and the next credential while the
// spent one is gone.
func TestTheRenewalCredentialRollsAndTheSpentOneIsGone(t *testing.T) {
	m := newTestManager(t, startRedis(t))
	ctx := context.Background()
	login := pairedLogin(t, m)
	if login.Pairing != "paired" || !validSecret(login.RefreshToken) || login.PairingID == "" ||
		login.PairingIdleSeconds != int64(PairingIdleLifetime.Seconds()) {
		t.Fatalf("exchange %+v", login)
	}
	next := decodeLogin(t, refresh(m, login.RefreshToken))
	if next.RefreshToken == login.RefreshToken || next.AccessToken == login.AccessToken || next.PairingID != login.PairingID {
		t.Fatalf("renewal %+v", next)
	}
	if _, err := m.Authenticate(ctx, next.AccessToken); err != nil {
		t.Fatalf("the renewed session does not work: %v", err)
	}
	// A renewal whose reply was lost is asked again with the spent credential
	// and gets the same pair within the grace; past it, the credential is gone.
	again := decodeLogin(t, refresh(m, login.RefreshToken))
	if again.AccessToken != next.AccessToken || again.RefreshToken != next.RefreshToken || again.SessionID != next.SessionID {
		t.Fatalf("a replay within the grace returned another pair: %+v / %+v", again, next)
	}
	sealed, _ := m.client.Get(ctx, m.prefix+"spent:"+digest(login.RefreshToken)).Result()
	if sealed == "" || strings.Contains(sealed, next.RefreshToken) || strings.Contains(sealed, next.AccessToken) {
		t.Fatalf("the replay is not sealed: %q", sealed)
	}
	m.client.Del(ctx, m.prefix+"spent:"+digest(login.RefreshToken))
	if w := refresh(m, login.RefreshToken); w.Code != 401 || errorCode(t, w) != "renewal_expired_or_revoked" {
		t.Fatalf("a spent credential renewed past the grace: %d %s", w.Code, w.Body)
	}
	decodeLogin(t, refresh(m, next.RefreshToken))
	if n, err := m.ActivePairings(ctx); err != nil || n != 1 {
		t.Fatalf("one device, one pairing across renewals: %d %v", n, err)
	}
	stats := m.Stats()
	if stats.Counts[CountRenewed] != 2 || stats.Counts[CountRenewalReplayed] != 1 || stats.Counts[CountRenewalExpired] != 1 || stats.Counts[CountPairingsIssued] != 1 ||
		stats.Counts[CountExchanged] != 1 || stats.Counts[CountGrantsIssued] != 1 {
		t.Fatalf("counts %v", stats.Counts)
	}
}

// Revoking the environment ends every session and renewal credential, from
// before the revocation, and only those.
func TestRevokingAllEndsEverySessionAndPairing(t *testing.T) {
	client := startRedis(t)
	m := newTestManager(t, client)
	ctx := context.Background()
	first, second := pairedLogin(t, m), pairedLogin(t, m)
	renewed := decodeLogin(t, refresh(m, first.RefreshToken))
	pending := issue(t, m)
	w := authRequest(m, http.MethodPost, revokeAllPath, `{"confirm":true}`, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"revoked_pairings":2`) {
		t.Fatalf("revoke %d %s", w.Code, w.Body)
	}
	for _, login := range []loginBody{first, second} {
		if _, err := m.Authenticate(ctx, login.AccessToken); ErrorCode(err) != "auth_expired_or_revoked" {
			t.Errorf("a session outlived the revocation: %v", err)
		}
		if w := refresh(m, login.RefreshToken); w.Code != 401 {
			t.Errorf("a pairing outlived the revocation: %d", w.Code)
		}
	}
	if n, _ := m.ActivePairings(ctx); n != 0 {
		t.Errorf("pairings after revocation: %d", n)
	}
	// Nothing issued before the revocation survives it: not the pair a
	// replay would return, not a code that was never exchanged.
	if w := refresh(m, first.RefreshToken); w.Code != 401 || errorCode(t, w) != "renewal_expired_or_revoked" {
		t.Errorf("a replay outlived the revocation: %d %s", w.Code, w.Body)
	}
	if _, err := m.Authenticate(ctx, renewed.AccessToken); err == nil {
		t.Error("a renewed session outlived the revocation")
	}
	if w := exchange(m, pending); w.Code != 401 || errorCode(t, w) != "grant_invalid_or_expired" {
		t.Errorf("a code issued before the revocation: %d %s", w.Code, w.Body)
	}
	after := pairedLogin(t, m)
	if _, err := m.Authenticate(ctx, after.AccessToken); err != nil {
		t.Errorf("a login after the revocation: %v", err)
	}
	decodeLogin(t, refresh(m, after.RefreshToken))
	// The administrator key and the origin, as issuing takes them.
	revoke := func(origin, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, revokeAllPath, strings.NewReader(`{"confirm":true}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		m.Handler().ServeHTTP(rec, r)
		return rec
	}
	if rec := revoke("https://elsewhere.test", testAdminKey); rec.Code != 403 || errorCode(t, rec) != "origin_denied" {
		t.Errorf("revocation from another origin: %d %s", rec.Code, rec.Body)
	}
	if rec := revoke(m.origin, strings.Repeat("w", 40)); rec.Code < 400 || errorCode(t, rec) != "admin_unauthorized" {
		t.Errorf("revocation with a wrong key: %d %s", rec.Code, rec.Body)
	}
	if _, err := m.Authenticate(ctx, after.AccessToken); err != nil {
		t.Errorf("a refused revocation revoked: %v", err)
	}
}

// A pairing made under one administrator key does not renew under another.
func TestRotatingTheAdministratorKeyEndsThePairings(t *testing.T) {
	client := startRedis(t)
	m := newTestManager(t, client)
	login := pairedLogin(t, m)
	rotated, err := New(Options{Redis: client, Prefix: "test-alarmd", EnvironmentID: "test-env",
		EnvironmentName: "Test environment", PublicBaseURL: "https://example.test/alarmd/",
		AdminKey: strings.Repeat("r", 40), Now: func() time.Time { return time.Unix(1, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	if w := refresh(rotated, login.RefreshToken); w.Code != 401 || errorCode(t, w) != "renewal_admin_key_rotated" {
		t.Fatalf("renewal under a rotated key: %d %s", w.Code, w.Body)
	}
	if w := refresh(m, login.RefreshToken); w.Code != 401 || errorCode(t, w) != "renewal_expired_or_revoked" {
		t.Fatalf("the refused credential is gone: %d %s", w.Code, w.Body)
	}
	if rotated.Stats().Counts[CountRenewalKeyRotated] != 1 {
		t.Errorf("counts %v", rotated.Stats().Counts)
	}
}

// Past the bound an exchange still logs in, without renewal, and says so; a
// session without a pairing - this one, or one from before this build - is
// paired once there is room, and only once.
func TestTheBoundAndTheUpgradeOfAnUnpairedSession(t *testing.T) {
	client := startRedis(t)
	m := newTestManager(t, client)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	for i := 0; i < MaxPairings; i++ {
		client.ZAdd(ctx, m.pairingsKey(), &redis.Z{Score: float64(now), Member: fmt.Sprintf("held-%d", i)})
	}
	login := pairedLogin(t, m)
	if login.Pairing != "pairing_limit_reached" || login.RefreshToken != "" {
		t.Fatalf("an exchange past the bound: %+v", login)
	}
	if _, err := m.Authenticate(ctx, login.AccessToken); err != nil {
		t.Fatalf("the unpaired session does not work: %v", err)
	}
	pair := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, pairPath, nil)
		r.Header.Set("Authorization", "Bearer "+login.AccessToken)
		w := httptest.NewRecorder()
		m.Handler().ServeHTTP(w, r)
		return w
	}
	if w := pair(); w.Code != 409 || errorCode(t, w) != "pairing_limit_reached" {
		t.Fatalf("an upgrade past the bound: %d %s", w.Code, w.Body)
	}
	// Pairings unused for the idle lifetime do not count.
	client.ZAdd(ctx, m.pairingsKey(), &redis.Z{Score: float64(now - PairingIdleLifetime.Milliseconds() - 1000), Member: "held-0"})
	w := pair()
	var paired struct {
		RefreshToken string `json:"refresh_token"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &paired) != nil || !validSecret(paired.RefreshToken) {
		t.Fatalf("the upgrade: %d %s", w.Code, w.Body)
	}
	decodeLogin(t, refresh(m, paired.RefreshToken))
	if w := pair(); w.Code != 409 || errorCode(t, w) != "already_paired" {
		t.Fatalf("a second upgrade of one session: %d %s", w.Code, w.Body)
	}
}

// A grant bound to a challenge is exchanged only with its verifier; a wrong
// or missing verifier leaves it for the holder.
func TestAChallengeBoundGrantNeedsItsVerifier(t *testing.T) {
	m := newTestManager(t, startRedis(t))
	verifier, _ := randomSecret()
	wrong, _ := randomSecret()
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	w := authRequest(m, http.MethodPost, grantsPath, `{"confirm":true,"code_challenge":"`+challenge+`","code_challenge_method":"S256"}`, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"bound_to_challenge":true`) {
		t.Fatalf("bound grant %d %s", w.Code, w.Body)
	}
	var issued struct {
		AuthorizationCode string `json:"authorization_code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &issued)
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(issued.AuthorizationCode, "alarmd-login-v1."))
	var grant authorizationPackage
	_ = json.Unmarshal(raw, &grant)
	try := func(verifier string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"environment_id": grant.EnvironmentID, "grant_secret": grant.GrantSecret, "code_verifier": verifier})
		if verifier == "" {
			body, _ = json.Marshal(map[string]string{"environment_id": grant.EnvironmentID, "grant_secret": grant.GrantSecret})
		}
		return authRequest(m, http.MethodPost, exchangePath, string(body), false)
	}
	for _, wrong := range []string{"", wrong} {
		if w := try(wrong); w.Code != 401 || errorCode(t, w) != "grant_invalid_or_expired" {
			t.Fatalf("verifier %q: %d %s", wrong, w.Code, w.Body)
		}
	}
	bound := try(verifier)
	if !strings.Contains(bound.Body.String(), `"bound_to_challenge":true`) {
		t.Fatalf("a bound exchange does not say so: %s", bound.Body)
	}
	decodeLogin(t, bound)
	if w := try(verifier); w.Code != 401 {
		t.Fatalf("the grant exchanged twice: %d", w.Code)
	}
	// A verifier offered for a code issued for copying: refused by name, and
	// the code stays for its holder.
	copied := issue(t, m)
	body, _ := json.Marshal(map[string]string{"environment_id": copied.EnvironmentID, "grant_secret": copied.GrantSecret, "code_verifier": verifier})
	if w := authRequest(m, http.MethodPost, exchangePath, string(body), false); w.Code != 401 || errorCode(t, w) != "grant_not_bound" {
		t.Fatalf("a verifier for an unbound code: %d %s", w.Code, w.Body)
	}
	if w := exchange(m, copied); w.Code != 200 || !strings.Contains(w.Body.String(), `"bound_to_challenge":false`) {
		t.Fatalf("the copied code after the refused loopback: %d %s", w.Code, w.Body)
	}
	for _, body := range []string{`{"confirm":true,"code_challenge":"short","code_challenge_method":"S256"}`,
		`{"confirm":true,"code_challenge":"` + challenge + `","code_challenge_method":"plain"}`,
		`{"confirm":true,"code_challenge":"` + challenge + `"}`} {
		if w := authRequest(m, http.MethodPost, grantsPath, body, true); w.Code != 400 || errorCode(t, w) != "invalid_code_challenge" {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
}

// Logging out forgets the device's own pairing; an idle one expires.
func TestForgottenAndIdlePairingsDoNotRenew(t *testing.T) {
	client := startRedis(t)
	m := newTestManager(t, client)
	ctx := context.Background()
	login := pairedLogin(t, m)
	body, _ := json.Marshal(map[string]string{"environment_id": m.environmentID, "refresh_token": login.RefreshToken})
	if w := authRequest(m, http.MethodPost, forgetPath, string(body), false); w.Code != 200 {
		t.Fatalf("forget %d %s", w.Code, w.Body)
	}
	if w := refresh(m, login.RefreshToken); w.Code != 401 {
		t.Fatalf("a forgotten pairing renewed: %d", w.Code)
	}
	if n, _ := m.ActivePairings(ctx); n != 0 {
		t.Fatalf("forgotten pairing still counted: %d", n)
	}
	idle := pairedLogin(t, m)
	client.PExpire(ctx, pairingKey(m.prefix, idle.RefreshToken), time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	if w := refresh(m, idle.RefreshToken); w.Code != 401 || errorCode(t, w) != "renewal_expired_or_revoked" {
		t.Fatalf("an idle pairing renewed: %d %s", w.Code, w.Body)
	}
	if m.Stats().Counts[CountPairingsForgotten] != 1 {
		t.Errorf("counts %v", m.Stats().Counts)
	}
}
