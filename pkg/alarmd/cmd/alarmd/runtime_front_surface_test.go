// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
)

// The admin's own address for the CLI, as it passes it with each grant.
const adminCLIBase = "https://admin.example.test/base/alarmd/"

func frontCall(h http.Handler, cfg config.Config, method, path string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, reader)
	r.Header.Set("Authorization", "Bearer "+cfg.CLI.AdminKey)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func errorCodeOf(w *httptest.ResponseRecorder) string {
	var answer struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &answer)
	return answer.Error.Code
}

func syncRoster(t *testing.T, h http.Handler, cfg config.Config, digest string, operators ...string) map[string]any {
	t.Helper()
	w := frontCall(h, cfg, http.MethodPut, "/api/front/v1/roster", map[string]any{"digest": digest, "operators": operators})
	var answer map[string]any
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &answer) != nil {
		t.Fatalf("roster %s = %d %s", digest, w.Code, w.Body.String())
	}
	return answer
}

func namedGrant(h http.Handler, cfg config.Config, operator string) *httptest.ResponseRecorder {
	return frontCall(h, cfg, http.MethodPost, "/api/front/v1/grants",
		map[string]any{"operator": operator, "public_base_url": adminCLIBase, "confirm": true})
}

type namedLogin struct {
	loginAnswer
	PublicBaseURL string `json:"public_base_url"`
}

// exchangeCode is the CLI's side of a code: it exchanges it on the address
// the code names.
func exchangeCode(t *testing.T, h http.Handler, issued *httptest.ResponseRecorder) namedLogin {
	t.Helper()
	var answer struct {
		AuthorizationCode string `json:"authorization_code"`
	}
	if issued.Code != http.StatusOK || json.Unmarshal(issued.Body.Bytes(), &answer) != nil {
		t.Fatalf("grant = %d %s", issued.Code, issued.Body.String())
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(answer.AuthorizationCode, "alarmd-login-v1."))
	var pkg struct {
		EnvironmentID string `json:"environment_id"`
		GrantSecret   string `json:"grant_secret"`
		PublicBaseURL string `json:"public_base_url"`
	}
	if err != nil || json.Unmarshal(raw, &pkg) != nil {
		t.Fatalf("authorization code: %v", err)
	}
	if pkg.PublicBaseURL != adminCLIBase {
		t.Fatalf("the code names %q, want the admin's address", pkg.PublicBaseURL)
	}
	body, _ := json.Marshal(map[string]string{"environment_id": pkg.EnvironmentID, "grant_secret": pkg.GrantSecret})
	r := httptest.NewRequest(http.MethodPost, "/api/cli/auth/exchange", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var login namedLogin
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &login) != nil || login.AccessToken == "" || login.RefreshToken == "" {
		t.Fatalf("exchange = %d %s", w.Code, w.Body.String())
	}
	return login
}

// exchangeStatus is the status an exchange of the code answers with.
func exchangeStatus(t *testing.T, h http.Handler, issued *httptest.ResponseRecorder) int {
	t.Helper()
	var answer struct {
		AuthorizationCode string `json:"authorization_code"`
	}
	if json.Unmarshal(issued.Body.Bytes(), &answer) != nil {
		t.Fatalf("grant = %d %s", issued.Code, issued.Body.String())
	}
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(answer.AuthorizationCode, "alarmd-login-v1."))
	var pkg struct {
		EnvironmentID string `json:"environment_id"`
		GrantSecret   string `json:"grant_secret"`
	}
	_ = json.Unmarshal(raw, &pkg)
	body, _ := json.Marshal(map[string]string{"environment_id": pkg.EnvironmentID, "grant_secret": pkg.GrantSecret})
	r := httptest.NewRequest(http.MethodPost, "/api/cli/auth/exchange", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

// readsFleet is whether a session can still read the deployment.
func readsFleet(t *testing.T, h http.Handler, token string) bool {
	t.Helper()
	out := cliCall(t, h, token, map[string]any{"channel_version": obchannel.Version, "mode": "discover"})
	return out.Status == "ok"
}

// A named grant waits for the admin's first roster and names only the
// operators on it; the session it buys keeps the admin's address through
// renewal.
func TestAFrontGrantWaitsForTheRosterAndNamesOnlyItsOperators(t *testing.T) {
	h, cfg, _ := restrictedCLI(t)
	before := namedGrant(h, cfg, "ops-a")
	if before.Code != http.StatusConflict || errorCodeOf(before) != "roster_not_synced" {
		t.Fatalf("grant before any roster = %d %s, want 409 roster_not_synced", before.Code, before.Body.String())
	}
	syncRoster(t, h, cfg, "admin-config-1", "ops-a", "admin")
	off := namedGrant(h, cfg, "ops-b")
	if off.Code != http.StatusForbidden || errorCodeOf(off) != "operator_not_on_roster" {
		t.Fatalf("grant off the roster = %d %s, want 403 operator_not_on_roster", off.Code, off.Body.String())
	}
	login := exchangeCode(t, h, namedGrant(h, cfg, "ops-a"))
	if login.PublicBaseURL != adminCLIBase || !readsFleet(t, h, login.AccessToken) {
		t.Fatalf("named session: base %q, reads %v", login.PublicBaseURL, readsFleet(t, h, login.AccessToken))
	}
	renewed := refreshCLISession(h, cfg, login.RefreshToken)
	var again namedLogin
	if renewed.Code != http.StatusOK || json.Unmarshal(renewed.Body.Bytes(), &again) != nil || again.PublicBaseURL != adminCLIBase {
		t.Fatalf("renewal = %d %s, want the admin's address kept", renewed.Code, renewed.Body.String())
	}
}

// Dropping an operator from the roster ends their sessions and pairings at
// the next use; a pairing made with the key on the authorization page is
// not touched; the operator added back pairs again while the old pairing
// stays dead.
func TestTheRosterRevokesDroppedOperatorsOnly(t *testing.T) {
	h, cfg, _ := restrictedCLI(t)
	syncRoster(t, h, cfg, "admin-config-1", "ops-a", "ops-b")
	first := exchangeCode(t, h, namedGrant(h, cfg, "ops-a"))
	// ops-a renews once before the drop: the renewed session and pairing
	// are ops-a's too.
	var a namedLogin
	if w := refreshCLISession(h, cfg, first.RefreshToken); w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &a) != nil {
		t.Fatalf("ops-a's renewal before the drop = %d %s", w.Code, w.Body.String())
	}
	b := exchangeCode(t, h, namedGrant(h, cfg, "ops-b"))
	keyed := openCLISession(t, h, cfg)
	// A code issued to ops-a before the roster drops them, exchanged after.
	pending := namedGrant(h, cfg, "ops-a")

	applied := syncRoster(t, h, cfg, "admin-config-2", "ops-b")
	if applied["applied"] != true || applied["revoked_pairings"] != float64(1) || len(applied["revoked_operators"].([]any)) != 1 || applied["revoked_operators"].([]any)[0] != "ops-a" {
		t.Fatalf("applying the roster without ops-a = %v", applied)
	}
	if readsFleet(t, h, a.AccessToken) || readsFleet(t, h, first.AccessToken) {
		t.Fatal("a session of ops-a's, exchanged or renewed, still reads after ops-a left the roster")
	}
	if w := refreshCLISession(h, cfg, a.RefreshToken); w.Code != http.StatusUnauthorized {
		t.Fatalf("ops-a's renewal = %d %s, want refused", w.Code, w.Body.String())
	}
	if code := exchangeStatus(t, h, pending); code != http.StatusUnauthorized {
		t.Fatalf("a code issued to ops-a before the drop exchanged with %d, want refused", code)
	}
	if !readsFleet(t, h, b.AccessToken) || refreshCLISession(h, cfg, b.RefreshToken).Code != http.StatusOK {
		t.Fatal("ops-b, still on the roster, lost its session or pairing")
	}
	if !readsFleet(t, h, keyed.AccessToken) || refreshCLISession(h, cfg, keyed.RefreshToken).Code != http.StatusOK {
		t.Fatal("the pairing made with the key on the authorization page was revoked by the roster")
	}

	syncRoster(t, h, cfg, "admin-config-3", "ops-a", "ops-b")
	back := exchangeCode(t, h, namedGrant(h, cfg, "ops-a"))
	if !readsFleet(t, h, back.AccessToken) || refreshCLISession(h, cfg, back.RefreshToken).Code != http.StatusOK {
		t.Fatal("ops-a, back on the roster, cannot use a new pairing")
	}
	if w := refreshCLISession(h, cfg, a.RefreshToken); w.Code != http.StatusUnauthorized {
		t.Fatalf("ops-a's old pairing = %d after ops-a came back, want still refused", w.Code)
	}
}

// A digest is applied once: the same digest and list again does nothing,
// the same digest with another list is refused.
func TestARosterDigestIsAppliedOnce(t *testing.T) {
	h, cfg, _ := restrictedCLI(t)
	syncRoster(t, h, cfg, "admin-config-1", "ops-a", "ops-b")
	a := exchangeCode(t, h, namedGrant(h, cfg, "ops-a"))
	// The same list in another order and with a repeat is the same roster.
	if again := syncRoster(t, h, cfg, "admin-config-1", "ops-b", "ops-a", "ops-a"); again["applied"] != false {
		t.Fatalf("the same digest again = %v, want not applied", again)
	}
	conflict := frontCall(h, cfg, http.MethodPut, "/api/front/v1/roster", map[string]any{"digest": "admin-config-1", "operators": []string{"ops-b"}})
	if conflict.Code != http.StatusConflict || errorCodeOf(conflict) != "roster_digest_conflict" {
		t.Fatalf("same digest, another list = %d %s, want 409 roster_digest_conflict", conflict.Code, conflict.Body.String())
	}
	if !readsFleet(t, h, a.AccessToken) {
		t.Fatal("a refused roster revoked ops-a")
	}
	standing := frontCall(h, cfg, http.MethodGet, "/api/front/v1/roster", nil)
	var read struct {
		Synced bool `json:"synced"`
		Roster struct {
			Digest    string `json:"digest"`
			Operators int    `json:"operators"`
		} `json:"roster"`
		NamedPairings int `json:"named_pairings"`
	}
	if standing.Code != http.StatusOK || json.Unmarshal(standing.Body.Bytes(), &read) != nil || !read.Synced ||
		read.Roster.Digest != "admin-config-1" || read.Roster.Operators != 2 || read.NamedPairings != 1 {
		t.Fatalf("roster read = %d %s", standing.Code, standing.Body.String())
	}
}

// Nothing under /api/cli/ reaches the front surface, whatever the path
// says, so a forwarder of /api/cli/* cannot be used to issue or sync.
func TestTheFrontSurfaceIsNotReachableThroughTheCLIForward(t *testing.T) {
	h, cfg, _ := restrictedCLI(t)
	syncRoster(t, h, cfg, "admin-config-1", "ops-a")
	body := map[string]any{"operator": "ops-a", "public_base_url": adminCLIBase, "confirm": true}
	for _, path := range []string{"/api/cli/front/v1/grants", "/api/cli/../front/v1/grants", "/api/cli/%2e%2e/front/v1/grants",
		"/api/cli/api/front/v1/grants", "/api/cli//api/front/v1/grants"} {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, "http://example.test"+path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+cfg.CLI.AdminKey)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "authorization_code") {
			t.Errorf("POST %s = %d %.200s: the front surface answered under /api/cli/", path, w.Code, w.Body.String())
		}
	}
}

// The front read serves alarmd's own data and refuses the cluster-wide
// reads and every operation that writes or executes.
func TestTheFrontReadRefusesClusterReadsAndEveryWrite(t *testing.T) {
	h, cfg, _ := restrictedCLI(t)
	session := openCLISession(t, h, cfg)
	discovered := cliCall(t, h, session.AccessToken, map[string]any{"channel_version": obchannel.Version, "mode": "discover"})
	raw, _ := json.Marshal(discovered.Result)
	var catalog struct {
		Operations []struct {
			Operation string `json:"operation"`
			Effect    string `json:"effect"`
		} `json:"operations"`
	}
	_ = json.Unmarshal(raw, &catalog)
	refused := []string{"pod.logs", "configmap.get", "secret.get", "k8s.list", "store.inspect", "slot.query"}
	for _, op := range catalog.Operations {
		if op.Effect != "read" {
			refused = append(refused, op.Operation)
		}
	}
	if len(refused) == 6 {
		t.Log("no writing operation is registered in this fixture; the named ones are checked")
	}
	for _, op := range refused {
		w := frontCall(h, cfg, http.MethodPost, "/api/front/v1/invoke", map[string]any{"channel_version": obchannel.Version, "mode": "invoke", "operation": op, "params": map[string]any{}})
		if w.Code != http.StatusForbidden || errorCodeOf(w) != "op_not_permitted_for_front" {
			t.Errorf("front read of %s = %d %.200s, want 403 op_not_permitted_for_front", op, w.Code, w.Body.String())
		}
	}
	w := frontCall(h, cfg, http.MethodPost, "/api/front/v1/invoke", map[string]any{"channel_version": obchannel.Version, "mode": "invoke", "operation": "fleet.get", "params": map[string]any{}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), plantedAddress) {
		t.Fatalf("front read of fleet.get = %d %.300s, want the fleet", w.Code, w.Body.String())
	}
	sessionAsKey := httptest.NewRequest(http.MethodPost, "/api/front/v1/invoke", strings.NewReader(`{"channel_version":"`+obchannel.Version+`","mode":"invoke","operation":"fleet.get","params":{}}`))
	sessionAsKey.Header.Set("Authorization", "Bearer "+session.AccessToken)
	sessionAsKey.Header.Set("Content-Type", "application/json")
	refusal := httptest.NewRecorder()
	h.ServeHTTP(refusal, sessionAsKey)
	if refusal.Code != http.StatusForbidden {
		t.Fatalf("front read with a CLI session = %d, want 403: the front surface takes the key", refusal.Code)
	}
}

// A roster is about 64 bytes an operator; a hundred thousand of them is an
// ordinary sync, and the last one on it can be granted.
func TestARosterOfAHundredThousandOperatorsApplies(t *testing.T) {
	h, cfg, _ := restrictedCLI(t)
	operators := make([]string, 100000)
	for i := range operators {
		operators[i] = fmt.Sprintf("ops-%06d", i)
	}
	syncRoster(t, h, cfg, "admin-config-large", operators...)
	if grant := namedGrant(h, cfg, operators[len(operators)-1]); grant.Code != http.StatusOK {
		t.Fatalf("grant to the last operator of a large roster = %d %s", grant.Code, grant.Body.String())
	}
}
