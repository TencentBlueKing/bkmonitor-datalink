// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The CLI is paired once: the exchange returns a short session and a
// renewal credential, kept in the 0600 profile. When the session runs out
// the credential is spent for a new session and the next credential; the
// administrator key is asked for again only when the pairing expired
// (unused for 30 days), was revoked, or the key was rotated.

// renewalMargin is how close to its expiry a session is renewed before use.
const renewalMargin = time.Minute

// exchangeFailure names why an exchange did not return a session, and what
// to do: only a code the server refused is a code to replace.
func exchangeFailure(result map[string]any, status int, err error) (string, string) {
	code := stringField(objectField(result, "error"), "code")
	switch {
	case err != nil && status == 0:
		return "exchange_unreachable", err.Error() + "; if no response arrived the code may still be unused - retry once, then request a new code"
	case status == http.StatusNotFound && code == "":
		return "exchange_not_answered", "the entry answered without alarmd's CLI route (for example a replica without the CLI during a rollout); the code was not used - retry"
	case err != nil && code == "":
		// Anything else that is not alarmd's answer - a proxy's 5xx page, a
		// cut body - may have come after the exchange ran.
		return "exchange_outcome_unknown", "the entry did not return alarmd's answer (HTTP " + strconv.Itoa(status) + "); the outcome is unknown - retry once, then request a new code"
	case code == "grant_invalid_or_expired":
		return code, "the code is invalid, expired or already used; request a new code from the authorization page"
	case code == "auth_rate_limited" || code == "auth_busy":
		return code, "the authorization service is busy; the code stays valid until it expires - retry shortly"
	case code == "auth_store_unavailable":
		return code, "alarmd's authorization store did not answer and the outcome is unknown; retry once, then request a new code"
	case code != "":
		return code, "the server refused the exchange (" + code + ")"
	}
	return "exchange_failed", "the exchange failed with HTTP " + strconv.Itoa(status)
}

// readPairing takes the renewal credential from an exchange or a renewal.
// A server that pairs nothing (an older build, or the bound full) leaves the
// profile without one, which is a session that ends in an hour.
func readPairing(result map[string]any, p *Profile) string {
	refresh := stringField(result, "refresh_token")
	if refresh != "" && validSecret(refresh) {
		p.RefreshToken, p.PairingID, p.Pairing = refresh, stringField(result, "pairing_id"), "paired"
		return p.Pairing
	}
	p.RefreshToken, p.PairingID, p.Pairing = "", "", "not_offered"
	if state := stringField(result, "pairing"); state != "" && state != "paired" {
		p.Pairing = state
	}
	return p.Pairing
}

func needsRenewal(p Profile, now time.Time) bool {
	if !validSecret(p.AccessToken) {
		return true
	}
	expiry, err := time.Parse(time.RFC3339, p.ExpiresAt)
	return err != nil || !expiry.After(now.Add(renewalMargin))
}

// ensureSession returns a profile whose session can be used now: renewed
// from the pairing when it is about to run out, or paired when the session
// is live and has none (one exchanged before the server paired).
func (a *App) ensureSession(env string, force bool) (Profile, error) {
	var out Profile
	var gone error
	err := a.Store.locked(func(c *config) (bool, error) {
		p, ok := c.Profiles[env]
		if !ok || (p.AccessToken == "" && p.RefreshToken == "") {
			return false, errors.New("environment has no session; open the authorization page and run auth listen, or auth login")
		}
		// Renewal happens under the profile lock: two commands at once spend
		// the credential once, and the second reads what the first saved.
		if p.RefreshToken != "" && (force || needsRenewal(p, time.Now())) {
			next, err := a.renew(p)
			if err != nil {
				var expired *pairingGone
				if errors.As(err, &expired) {
					// Written, then reported: a pairing the server ended is gone
					// here too.
					p.AccessToken, p.SessionID, p.ExpiresAt, p.RefreshToken, p.PairingID, p.Pairing = "", "", "", "", "", ""
					c.Profiles[env] = p
					gone = err
					return true, nil
				}
				return false, err
			}
			c.Profiles[env] = next
			out = next
			return true, nil
		}
		// A session from before this client is offered for pairing once; the
		// answer is kept, so an older server is not asked on every command.
		if p.RefreshToken == "" && p.Pairing == "" && !needsRenewal(p, time.Now()) {
			refresh, id, ok := a.upgrade(p)
			p.Pairing = "upgrade_refused"
			if ok {
				p.RefreshToken, p.PairingID, p.Pairing = refresh, id, "paired"
			}
			c.Profiles[env] = p
			out = p
			return true, nil
		}
		out = p
		return false, nil
	})
	if err == nil && gone != nil {
		return Profile{}, gone
	}
	return out, err
}

type pairingGone struct{ code string }

func (e *pairingGone) Error() string {
	if e.code == "renewal_admin_key_rotated" {
		return "the deployment's administrator key changed since this CLI was paired"
	}
	return "the pairing has expired or was revoked"
}

// credentialsLapsedCode is what every lapse is reported as, whatever the
// server called it: a renewal credential past its idle life, revoked, from
// before an epoch change or a key rotation, or a session ended with no
// renewal credential to replace it. The reader has one thing to do in each.
const credentialsLapsedCode = "credentials_expired"

// lapsed reports that this environment's credentials no longer work and how
// to get new ones: the login page under the environment's own address, and
// the command. Nothing held locally can help, so none of it is offered.
func (a *App) lapsed(p Profile, cause string) int {
	page := "the deployment's login page"
	if base, err := baseURL(p.PublicBaseURL); err == nil {
		page = base.String() + "cli"
	}
	env := p.EnvironmentID
	if env == "" {
		env = "<environment_id>"
	}
	return a.fail(credentialsLapsedCode, fmt.Sprintf("凭据已失效，请重新登录. The credentials for %s are no longer valid (%s). "+
		"Log in again: open %s, enter the administrator key and run the auth listen command it shows; or run: alarmd-cli auth login --env %s",
		env, cause, page, env), 1)
}

// emitOrLapsed emits a channel answer, except that an answer the server
// refused for want of a valid session -- after any renewal was already
// tried -- is reported as lapsed credentials with the way back in.
func (a *App) emitOrLapsed(p Profile, raw map[string]any, secrets []string, status int) int {
	if status == http.StatusUnauthorized {
		cause := stringField(objectField(raw, "error"), "code")
		if cause == "" {
			cause = "the server refused the session"
		}
		return a.lapsed(p, cause)
	}
	return a.emitResponse(raw, secrets, status)
}

func (a *App) renew(p Profile) (Profile, error) {
	anonymous := p
	anonymous.AccessToken = ""
	body := map[string]any{"environment_id": p.EnvironmentID, "refresh_token": p.RefreshToken}
	result, status, err := a.request(anonymous, http.MethodPost, "api/cli/auth/refresh", body)
	if err != nil && (status == 0 || result == nil) {
		// No reply, or a reply that could not be read (a status and no body
		// to decode, such as a truncated one): the renewal may have happened.
		// The server keeps its answer for the spent credential for a moment,
		// so asking again with the same credential gets the same pair rather
		// than losing the pairing.
		result, status, err = a.request(anonymous, http.MethodPost, "api/cli/auth/refresh", body)
	}
	if err != nil {
		return Profile{}, fmt.Errorf("session renewal failed: %w", err)
	}
	if status == http.StatusUnauthorized {
		return Profile{}, &pairingGone{code: stringField(objectField(result, "error"), "code")}
	}
	if status < 200 || status >= 300 {
		code, message := exchangeFailure(result, status, nil)
		return Profile{}, fmt.Errorf("session renewal refused (%s): %s", code, message)
	}
	next, err := validateExchange(result, p)
	if err != nil {
		return Profile{}, err
	}
	if readPairing(result, &next) != "paired" {
		return Profile{}, errors.New("session renewal returned no next renewal credential")
	}
	return next, nil
}

// upgrade asks the server to pair a live session. Any refusal - an older
// server, a full bound, already paired - leaves the session as it is.
func (a *App) upgrade(p Profile) (string, string, bool) {
	result, status, err := a.request(p, http.MethodPost, "api/cli/auth/pair", nil)
	if err != nil || status != http.StatusOK {
		return "", "", false
	}
	refresh := stringField(result, "refresh_token")
	if !validSecret(refresh) {
		return "", "", false
	}
	fmt.Fprintln(a.Err, "This session is now paired: it will renew without the administrator key.")
	return refresh, stringField(result, "pairing_id"), true
}

func (a *App) forget(p Profile) bool {
	if p.RefreshToken == "" {
		return true
	}
	anonymous := p
	anonymous.AccessToken = ""
	_, status, err := a.request(anonymous, http.MethodPost, "api/cli/auth/forget", map[string]any{"environment_id": p.EnvironmentID, "refresh_token": p.RefreshToken})
	return err == nil && status == http.StatusOK
}

func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func validState(s string) bool {
	if len(s) < 16 || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// listener is one loopback login: it answers the authorization page on
// 127.0.0.1 only, gives its state back to the page's probe, and takes one
// code, exchanged with the verifier only this process holds.
type listener struct {
	app       *App
	profile   Profile
	origin    string
	state     string
	verifier  string
	challenge string
	rebind    bool

	mu     sync.Mutex
	busy   bool
	result map[string]any
	done   chan struct{}
}

func (l *listener) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (l *listener) refuse(w http.ResponseWriter, status int, code, message string) {
	l.writeJSON(w, status, map[string]any{"status": "error", "error": map[string]string{"code": code, "message": message}})
}

func (l *listener) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only the configured entry's page may read or call this process: the
	// page's origin is the one the environment's URL names.
	if r.Header.Get("Origin") != l.origin {
		l.refuse(w, http.StatusForbidden, "origin_denied", "only the environment's authorization page may call this CLI")
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", l.origin)
	w.Header().Set("Vary", "Origin")
	if r.Method == http.MethodOptions {
		method := r.Header.Get("Access-Control-Request-Method")
		if method != http.MethodGet && method != http.MethodPost {
			l.refuse(w, http.StatusForbidden, "method_not_allowed", "only GET /ready and POST /callback are served")
			return
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "60")
		// Chrome's private network access: a public page reaching the
		// loopback address asks first, and is let through only on this.
		if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	l.mu.Lock()
	finished := l.result != nil
	l.mu.Unlock()
	switch {
	case r.URL.Path == "/ready" && r.Method == http.MethodGet:
		if finished {
			l.refuse(w, http.StatusConflict, "already_completed", "this CLI has logged in and is exiting")
			return
		}
		if r.URL.Query().Get("state") != l.state {
			l.refuse(w, http.StatusForbidden, "state_mismatch", "this CLI waits for another page")
			return
		}
		l.writeJSON(w, http.StatusOK, map[string]any{"state": l.state, "code_challenge": l.challenge})
	case r.URL.Path == "/callback" && r.Method == http.MethodPost:
		l.callback(w, r)
	default:
		l.refuse(w, http.StatusNotFound, "not_found", "only GET /ready and POST /callback are served")
	}
}

func (l *listener) callback(w http.ResponseWriter, r *http.Request) {
	var input struct {
		State string `json:"state"`
		Code  string `json:"code"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		l.refuse(w, http.StatusBadRequest, "invalid_request", "the callback must be a JSON object with state and code")
		return
	}
	if input.State != l.state {
		l.refuse(w, http.StatusForbidden, "state_mismatch", "this CLI waits for another page")
		return
	}
	l.mu.Lock()
	if l.result != nil || l.busy {
		l.mu.Unlock()
		l.refuse(w, http.StatusConflict, "already_completed", "this CLI has already taken a code")
		return
	}
	l.busy = true
	l.mu.Unlock()
	session, failure, message := l.app.exchange(input.Code, l.profile, l.verifier, l.rebind)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.busy = false
	if failure != "" {
		// A failed exchange leaves the listener waiting: the page can ask for
		// another code.
		l.refuse(w, http.StatusBadGateway, failure, message)
		return
	}
	l.result = session
	l.writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "environment_id": session["environment_id"], "expires_at": session["expires_at"], "pairing": session["pairing"]})
	close(l.done)
}

// exchange spends a code for a session and saves it. The verifier is empty
// for a pasted code.
func (a *App) exchange(code string, expected Profile, verifier string, rebind bool) (map[string]any, string, string) {
	b, err := parseBundle(code)
	if err != nil {
		return nil, "invalid_input", err.Error()
	}
	if expected.EnvironmentID != "" && b.EnvironmentID != expected.EnvironmentID {
		return nil, "environment_mismatch", "the code is for another environment"
	}
	if expected.PublicBaseURL != "" && b.PublicBaseURL != expected.PublicBaseURL {
		return nil, "environment_mismatch", "the code is for another entry"
	}
	p := expected
	p.EnvironmentID, p.EnvironmentName, p.PublicBaseURL = b.EnvironmentID, b.EnvironmentName, b.PublicBaseURL
	if err := a.Store.checkBinding(p, rebind); err != nil {
		return nil, "configuration_error", err.Error()
	}
	body := map[string]any{"environment_id": b.EnvironmentID, "grant_secret": b.GrantSecret}
	if verifier != "" {
		body["code_verifier"] = verifier
	}
	anonymous := p
	anonymous.AccessToken, anonymous.RefreshToken = "", ""
	result, status, err := a.request(anonymous, http.MethodPost, "api/cli/auth/exchange", body)
	if err != nil || status < 200 || status >= 300 {
		failure, message := exchangeFailure(result, status, err)
		return nil, failure, message
	}
	// A loopback login takes only a grant bound to its own verifier; the
	// server refuses the rest, and this checks the answer says so before the
	// session is kept.
	if verifier != "" {
		switch bound, stated := result["bound_to_challenge"]; {
		case stated && bound != true:
			return nil, "grant_not_bound", "the code was not issued for this CLI's challenge; authorize the listening CLI from the page, or paste a copied code into auth login"
		case !stated:
			// A server from before the binding check does not say; its
			// answer is taken, and the operator is told what it lacks.
			fmt.Fprintln(a.Err, "Note: the server predates the challenge binding check; upgrade alarmd to get this protection for page logins.")
		}
	}
	session, err := validateExchange(result, p)
	if err != nil {
		return nil, "protocol_error", err.Error()
	}
	pairing := readPairing(result, &session)
	if err := a.Store.save(session, rebind); err != nil {
		return nil, "configuration_error", err.Error()
	}
	return map[string]any{"environment_id": session.EnvironmentID, "expires_at": session.ExpiresAt, "session_id": session.SessionID, "pairing": pairing}, "", ""
}

// listen runs one loopback login and returns its exit code.
func (a *App) listen(o options) int {
	// The page's callback runs on the listener's goroutine and writes to
	// stderr beside this one; one lock keeps the two writers apart.
	restore := a.Err
	a.Err = &lockedWriter{w: restore}
	defer func() { a.Err = restore }()
	var p Profile
	switch {
	case o.env != "" && o.url != "":
		return a.fail("invalid_input", "auth listen takes --env or --url, not both", 2)
	case o.env != "":
		var err error
		p, err = a.Store.profile(o.env)
		if err != nil {
			return a.fail("configuration_error", err.Error(), 1)
		}
	case o.url != "":
		entry, err := normalizedURL(o.url)
		if err != nil {
			return a.fail("invalid_input", err.Error(), 2)
		}
		p = Profile{PublicBaseURL: entry, CACert: o.caCert, InsecureTLS: o.insecureTLS}
	default:
		return a.fail("invalid_input", "auth listen requires --env <environment_id> or --url <entry URL>", 2)
	}
	port, err := strconv.Atoi(o.port)
	if err != nil || port < 1024 || port > 65535 {
		return a.fail("invalid_input", "--port must be the number the authorization page shows (1024-65535)", 2)
	}
	if !validState(o.state) {
		return a.fail("invalid_input", "--state must be the value the authorization page shows", 2)
	}
	entry, err := baseURL(p.PublicBaseURL)
	if err != nil {
		return a.fail("configuration_error", err.Error(), 1)
	}
	verifier, err := randomToken()
	if err != nil {
		return a.fail("internal_error", "cannot generate a verifier", 1)
	}
	sum := sha256.Sum256([]byte(verifier))
	l := &listener{app: a, profile: p, origin: browserOrigin(entry), state: o.state, verifier: verifier,
		challenge: base64.RawURLEncoding.EncodeToString(sum[:]), rebind: o.rebind, done: make(chan struct{})}
	socket, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return a.fail("listen_failed", "cannot listen on 127.0.0.1:"+strconv.Itoa(port)+"; reload the authorization page for another port", 1)
	}
	server := &http.Server{Handler: l, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second}
	go func() { _ = server.Serve(socket) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	timeout := o.timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	fmt.Fprintf(a.Err, "Waiting on 127.0.0.1:%d for the authorization page at %s (up to %s)...\n", port, (&url.URL{Scheme: entry.Scheme, Host: entry.Host, Path: entry.Path}).String(), timeout)
	select {
	case <-l.done:
	case <-time.After(timeout):
		return a.fail("listen_timeout", "no authorization arrived in time; run the command from the authorization page again", 1)
	}
	l.mu.Lock()
	result := l.result
	l.mu.Unlock()
	return a.print(map[string]any{"status": "ok", "summary": "Logged in from the authorization page. Run discover with explicit --env.", "session": result})
}

// browserOrigin is the entry's origin as a browser sends it: lowercase host,
// no default port. The page's Origin header is compared with it exactly.
func browserOrigin(entry *url.URL) string {
	host := strings.ToLower(entry.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := entry.Port()
	if port != "" && !(entry.Scheme == "http" && port == "80") && !(entry.Scheme == "https" && port == "443") {
		host += ":" + port
	}
	return strings.ToLower(entry.Scheme) + "://" + host
}

// lockedWriter serializes writes from the listener's goroutines.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
