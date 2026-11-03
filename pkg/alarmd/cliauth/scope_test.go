// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package cliauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A grant is the one deployment operations session, whether the page or CLI
// asks for nothing or, as an earlier build did, for the execution session:
// the code names no scope, the renewal credential has no prefix, and the
// session renews as the one scope.
func TestEveryGrantIsTheOneOperationsSession(t *testing.T) {
	m := newTestManager(t, startRedis(t))
	for _, body := range []string{`{"confirm":true}`, `{"confirm":true,"scope":"deployment_ops_exec"}`} {
		t.Run(body, func(t *testing.T) {
			issued := authRequest(m, http.MethodPost, grantsPath, body, true)
			var grant struct {
				AuthorizationCode string   `json:"authorization_code"`
				Scope             string   `json:"scope"`
				SupportedScopes   []string `json:"supported_scopes"`
			}
			if issued.Code != 200 || json.Unmarshal(issued.Body.Bytes(), &grant) != nil || grant.Scope != ScopeReadonly ||
				len(grant.SupportedScopes) != 1 || grant.SupportedScopes[0] != ScopeReadonly {
				t.Fatalf("grant: %d %s", issued.Code, issued.Body)
			}
			raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(grant.AuthorizationCode, "alarmd-login-v1."))
			if err != nil {
				t.Fatal(err)
			}
			var code authorizationPackage
			if json.Unmarshal(raw, &code) != nil || strings.Contains(string(raw), `"scope"`) {
				t.Fatalf("the code names a scope: %s", raw)
			}
			login := decodeLogin(t, exchange(m, code))
			if !validSecret(login.RefreshToken) || strings.HasPrefix(login.RefreshToken, "exec-v1.") {
				t.Fatalf("renewal credential %q, want one without a prefix", login.RefreshToken)
			}
			next := decodeLogin(t, refresh(m, login.RefreshToken))
			renewed, err := m.Authenticate(context.Background(), next.AccessToken)
			if err != nil || renewed.Scope != ScopeReadonly || !AllowsScope(renewed.Scope, ScopeReadonly) {
				t.Fatalf("renewed: %+v %v", renewed, err)
			}
		})
	}
}

// No running deployment ever issued an execution session or pairing, so none
// is read: a session stored with that scope does not authenticate, a pairing
// stored with it does not renew and is deleted as any foreign pairing is,
// and a renewal credential carrying the prefix those builds gave it is
// refused before Redis is asked. An operation may require only the one scope.
func TestAnExecutionScopeNoBuildIssuedIsNotRead(t *testing.T) {
	m := newTestManager(t, startRedis(t))
	ctx := context.Background()
	token, _ := randomSecret()
	record := storedRecord{SessionID: "never-issued", EnvironmentID: m.environmentID, Scope: requestedExecScope, ExpiresAtMS: time.Now().Add(time.Hour).UnixMilli()}
	raw, _ := json.Marshal(record)
	if err := m.client.Set(ctx, m.prefix+"session:"+digest(token), raw, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if session, err := m.Authenticate(ctx, token); err == nil {
		t.Fatalf("a session stored with the execution scope authenticated: %+v", session)
	}

	refreshSecret, _ := randomSecret()
	now := time.Now().UnixMilli()
	pairing, _ := json.Marshal(map[string]any{"pairing_id": "never-issued", "environment_id": m.environmentID, "scope": requestedExecScope, "epoch": 0,
		"admin_binding": m.adminBinding(), "created_at_ms": now, "last_used_at_ms": now})
	if err := m.client.Set(ctx, pairingKey(m.prefix, refreshSecret), pairing, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Refresh(ctx, refreshSecret); ErrorCode(err) != "renewal_expired_or_revoked" {
		t.Fatalf("a pairing stored with the execution scope: err=%v, want renewal_expired_or_revoked", err)
	}
	if exists, _ := m.client.Exists(ctx, pairingKey(m.prefix, refreshSecret)).Result(); exists != 0 {
		t.Fatal("the foreign pairing was kept")
	}
	if _, err := m.Refresh(ctx, "exec-v1."+refreshSecret); ErrorCode(err) != "renewal_expired_or_revoked" {
		t.Fatalf("a prefixed credential: err=%v, want renewal_expired_or_revoked", err)
	}
	if AllowsScope(requestedExecScope, ScopeReadonly) || AllowsScope("", ScopeReadonly) || ValidScope(requestedExecScope) {
		t.Fatal("a scope other than the one is allowed, or an operation may require another")
	}
}
