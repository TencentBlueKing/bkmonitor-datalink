// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package cliauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

const (
	grantsPath    = "/api/cli/auth/grants"
	exchangePath  = "/api/cli/auth/exchange"
	refreshPath   = "/api/cli/auth/refresh"
	pairPath      = "/api/cli/auth/pair"
	forgetPath    = "/api/cli/auth/forget"
	revokeAllPath = "/api/cli/auth/revoke-all"
	sessionPath   = "/api/cli/session"
	maxBodyBytes  = 64 << 10
)

// Paths is every route this handler serves, for the router in front of it.
var Paths = []string{grantsPath, exchangePath, refreshPath, pairPath, forgetPath, revokeAllPath, sessionPath}

// Handler serves only the four auth operations. The surrounding server remains
// responsible for bounded request read time and request routing. It must
// not expose administrator credentials through access logs or configuration evidence.
func (m *Manager) Handler() http.Handler { return http.HandlerFunc(m.serveHTTP) }

func (m *Manager) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	select {
	case m.httpSlots <- struct{}{}:
		defer func() { <-m.httpSlots }()
	default:
		// Do not let net/http wait for an unaccepted request body while
		// writing the busy response. No Redis or handler slot is needed.
		defer boundBodyRead(w, r, 0)()
		writeError(w, failure("auth_busy", "Too many concurrent authorization requests.", 429))
		return
	}
	// The same listener also serves long-lived h2c/gRPC streams. Limit only
	// this auth request, including body draining after an early rejection.
	defer boundBodyRead(w, r, 3*time.Second)()
	var err error
	switch r.URL.Path {
	case grantsPath:
		err = m.handleGrants(w, r)
	case exchangePath:
		err = m.handleExchange(w, r)
	case refreshPath:
		err = m.handleRefresh(w, r)
	case pairPath:
		err = m.handlePair(w, r)
	case forgetPath:
		err = m.handleForget(w, r)
	case revokeAllPath:
		err = m.handleRevokeAll(w, r)
	case sessionPath:
		err = m.handleSession(w, r)
	default:
		err = failure("not_found", "Unknown authorization route.", 404)
	}
	if err != nil {
		writeError(w, err)
	}
}

func boundBodyRead(w http.ResponseWriter, r *http.Request, timeout time.Duration) func() {
	controller := http.NewResponseController(w)
	deadlineSet := controller.SetReadDeadline(time.Now().Add(timeout)) == nil
	return func() {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		if deadlineSet {
			_ = controller.SetReadDeadline(time.Time{})
		}
	}
}

// authorizeAdmin checks the deployment administrator key, the one credential
// that can issue a grant.
func (m *Manager) authorizeAdmin(r *http.Request) error {
	if !m.adminConfigured {
		return failure("admin_not_configured", "Deployment authorization is not configured.", 503)
	}
	authorization, single := singleHeader(r, "Authorization")
	scheme, key, separated := strings.Cut(authorization, " ")
	hash := sha256.Sum256([]byte(key))
	keyMatches := subtle.ConstantTimeCompare(hash[:], m.adminHash[:]) == 1
	if !single || !separated || !strings.EqualFold(scheme, "Bearer") || !keyMatches {
		return failure("admin_unauthorized", "Deployment administrator authorization is required.", 403)
	}
	return nil
}

func singleHeader(r *http.Request, name string) (string, bool) {
	values := r.Header.Values(name)
	if len(values) != 1 || values[0] == "" {
		return "", false
	}
	return values[0], true
}

type grantPreview struct {
	EnvironmentID     string `json:"environment_id"`
	EnvironmentName   string `json:"environment_name"`
	PublicBaseURL     string `json:"public_base_url"`
	Scope             string `json:"scope"`
	GrantTTLSeconds   int    `json:"grant_ttl_seconds"`
	SessionTTLSeconds int    `json:"session_ttl_seconds"`
}

type authorizationPackage struct {
	Version         string    `json:"version"`
	EnvironmentID   string    `json:"environment_id"`
	EnvironmentName string    `json:"environment_name"`
	PublicBaseURL   string    `json:"public_base_url"`
	GrantSecret     string    `json:"grant_secret"`
	GrantExpiresAt  time.Time `json:"grant_expires_at"`
}

func (m *Manager) handleGrants(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return methodNotAllowed(w, "GET, POST")
	}
	if err := m.authorizeAdmin(r); err != nil {
		return err
	}
	preview := grantPreview{EnvironmentID: m.environmentID, EnvironmentName: m.environmentName,
		PublicBaseURL: m.publicBaseURL, Scope: ScopeReadonly,
		GrantTTLSeconds: int(GrantLifetime.Seconds()), SessionTTLSeconds: int(SessionLifetime.Seconds())}
	if r.Method == http.MethodGet {
		return writeJSON(w, preview)
	}
	origin, single := singleHeader(r, "Origin")
	if !single || origin != m.origin {
		return failure("origin_denied", "A grant must be requested from the configured deployment origin.", 403)
	}
	var input struct {
		Confirm bool `json:"confirm"`
		// A loopback login: the CLI listening on the operator's machine holds
		// the verifier of CodeChallenge, and the grant answers only it.
		CodeChallenge       string `json:"code_challenge,omitempty"`
		CodeChallengeMethod string `json:"code_challenge_method,omitempty"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		return err
	}
	if !input.Confirm {
		return failure("invalid_request", "Explicit authorization confirmation is required.", 400)
	}
	if (input.CodeChallenge != "" || input.CodeChallengeMethod != "") && (input.CodeChallengeMethod != "S256" || !validSecret(input.CodeChallenge)) {
		return failure("invalid_code_challenge", "A code challenge must be an S256 challenge of 43 URL-safe characters.", 400)
	}
	if !m.allow(&m.grantWindow, 6) {
		return failure("auth_rate_limited", "The grant issuance limit was reached; try again in the next minute.", 429)
	}
	secret, err := randomSecret()
	if err != nil {
		return err
	}
	record, _ := json.Marshal(storedRecord{EnvironmentID: m.environmentID, Scope: ScopeReadonly, CodeChallenge: input.CodeChallenge})
	result, err := m.run(r.Context(), issueScript, []string{m.prefix + "grant:" + digest(secret), m.epochKey()}, string(record), GrantLifetime.Milliseconds())
	if err != nil {
		return err
	}
	issued, _, err := resultRecord(result)
	if err != nil {
		return err
	}
	m.count(CountGrantsIssued)
	expiresAt := time.UnixMilli(issued.ExpiresAtMS).UTC()
	code, _ := json.Marshal(authorizationPackage{Version: "alarmd-login/v1", EnvironmentID: m.environmentID,
		EnvironmentName: m.environmentName, PublicBaseURL: m.publicBaseURL,
		GrantSecret: secret, GrantExpiresAt: expiresAt})
	return writeJSON(w, struct {
		grantPreview
		AuthorizationCode string    `json:"authorization_code"`
		GrantExpiresAt    time.Time `json:"grant_expires_at"`
		// Bound says the grant answers only the verifier's holder.
		Bound bool `json:"bound_to_challenge"`
	}{preview, "alarmd-login-v1." + base64.RawURLEncoding.EncodeToString(code), expiresAt, input.CodeChallenge != ""})
}

// loginResponse is what an exchange and a renewal both return: the session
// and, when the environment holds a pairing for it, the next renewal
// credential. Pairing names why there is none.
type loginResponse struct {
	EnvironmentID      string    `json:"environment_id"`
	EnvironmentName    string    `json:"environment_name"`
	PublicBaseURL      string    `json:"public_base_url"`
	AccessToken        string    `json:"access_token"`
	SessionID          string    `json:"session_id"`
	ExpiresAt          time.Time `json:"expires_at"`
	Scope              string    `json:"scope"`
	RefreshToken       string    `json:"refresh_token,omitempty"`
	PairingID          string    `json:"pairing_id,omitempty"`
	PairingIdleSeconds int64     `json:"pairing_idle_seconds,omitempty"`
	Pairing            string    `json:"pairing"`
	// BoundToChallenge says the exchanged grant was bound to the verifier
	// that exchanged it: a loopback login checks it.
	BoundToChallenge bool `json:"bound_to_challenge"`
}

func (m *Manager) login(record storedRecord, token, refresh string) loginResponse {
	response := loginResponse{EnvironmentID: m.environmentID, EnvironmentName: m.environmentName, PublicBaseURL: m.publicBaseURL,
		AccessToken: token, SessionID: record.SessionID, ExpiresAt: time.UnixMilli(record.ExpiresAtMS).UTC(), Scope: ScopeReadonly,
		Pairing: "paired"}
	if refresh == "" {
		response.Pairing = "pairing_limit_reached"
		return response
	}
	response.RefreshToken, response.PairingID = refresh, record.PairingID
	response.PairingIdleSeconds = int64(PairingIdleLifetime.Seconds())
	return response
}

func (m *Manager) handleExchange(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		return methodNotAllowed(w, "POST")
	}
	if !m.allow(&m.exchangeWindow, 60) {
		return failure("auth_rate_limited", "The grant exchange limit was reached; try again in the next minute.", 429)
	}
	var input struct {
		EnvironmentID string `json:"environment_id"`
		GrantSecret   string `json:"grant_secret"`
		CodeVerifier  string `json:"code_verifier,omitempty"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		return err
	}
	challenge := ""
	if input.CodeVerifier != "" {
		if !validSecret(input.CodeVerifier) {
			m.count(CountExchangeRejected)
			return failure("grant_invalid_or_expired", "The grant is invalid, expired, or already used; obtain a new code.", 401)
		}
		sum := sha256.Sum256([]byte(input.CodeVerifier))
		challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	}
	if input.EnvironmentID != m.environmentID || !validSecret(input.GrantSecret) {
		m.count(CountExchangeRejected)
		return failure("grant_invalid_or_expired", "The grant is invalid, expired, or already used; obtain a new code.", 401)
	}
	var secrets [4]string
	for i := range secrets {
		value, err := randomSecret()
		if err != nil {
			return err
		}
		secrets[i] = value
	}
	token, id, refresh, pairingID := secrets[0], secrets[1], secrets[2], secrets[3]
	result, err := m.run(r.Context(), exchangeScript,
		[]string{m.prefix + "grant:" + digest(input.GrantSecret), m.prefix + "session:" + digest(token),
			pairingKey(m.prefix, refresh), m.pairingsKey(), m.epochKey()},
		m.environmentID, id, ScopeReadonly, SessionLifetime.Milliseconds(),
		pairingID, PairingIdleLifetime.Milliseconds(), MaxPairings, m.adminBinding(), challenge)
	if err != nil {
		m.count(CountStoreUnavailable)
		return err
	}
	if status, _ := firstStatus(result); status == 3 {
		m.count(CountExchangeRejected)
		return failure("grant_not_bound", "A loopback login takes only a code issued for its own challenge; this code was issued for copying. Use auth login with it, or authorize the listening CLI from the page.", 401)
	}
	record, _, err := resultRecord(result)
	if ErrorCode(err) == "auth_expired_or_revoked" {
		m.count(CountExchangeRejected)
		return failure("grant_invalid_or_expired", "The grant is invalid, expired, or already used; obtain a new code.", 401)
	}
	if err != nil {
		return err
	}
	m.count(CountExchanged)
	// The one success reply is five long; its length is checked before any
	// index is read, so a later branch of the script cannot panic this.
	var paired, bound int64
	if len(result) == 5 {
		paired, _ = result[3].(int64)
		bound, _ = result[4].(int64)
	}
	if paired != 1 {
		m.count(CountPairingsRefused)
		refresh = ""
	} else {
		m.count(CountPairingsIssued)
	}
	response := m.login(record, token, refresh)
	response.BoundToChallenge = bound == 1
	return writeJSON(w, response)
}

// handleRefresh spends a renewal credential for a new session and the next
// credential. The body carries the credential, never a header, so it is not
// confused with a session bearer.
func (m *Manager) handleRefresh(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		return methodNotAllowed(w, "POST")
	}
	if !m.allow(&m.refreshWindow, 60) {
		return failure("auth_rate_limited", "The renewal limit was reached; try again in the next minute.", 429)
	}
	var input struct {
		EnvironmentID string `json:"environment_id"`
		RefreshToken  string `json:"refresh_token"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		return err
	}
	if input.EnvironmentID != m.environmentID {
		m.count(CountRenewalExpired)
		return renewalFailure()
	}
	renewal, err := m.Refresh(r.Context(), input.RefreshToken)
	if err != nil {
		return err
	}
	record := storedRecord{SessionID: renewal.Session.ID, ExpiresAtMS: renewal.Session.ExpiresAt.UnixMilli(), PairingID: renewal.PairingID}
	return writeJSON(w, m.login(record, renewal.AccessToken, renewal.RefreshToken))
}

// handlePair gives a live session without a pairing its renewal credential.
func (m *Manager) handlePair(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		return methodNotAllowed(w, "POST")
	}
	bearer, err := requestBearer(r)
	if err != nil {
		return err
	}
	refresh, id, err := m.Upgrade(r.Context(), bearer)
	if err != nil {
		return err
	}
	return writeJSON(w, struct {
		RefreshToken       string `json:"refresh_token"`
		PairingID          string `json:"pairing_id"`
		PairingIdleSeconds int64  `json:"pairing_idle_seconds"`
	}{refresh, id, int64(PairingIdleLifetime.Seconds())})
}

// handleForget revokes the caller's own renewal credential.
func (m *Manager) handleForget(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		return methodNotAllowed(w, "POST")
	}
	var input struct {
		EnvironmentID string `json:"environment_id"`
		RefreshToken  string `json:"refresh_token"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		return err
	}
	if input.EnvironmentID != m.environmentID {
		return renewalFailure()
	}
	if err := m.Forget(r.Context(), input.RefreshToken); err != nil {
		return err
	}
	return writeJSON(w, struct {
		Forgotten bool `json:"forgotten"`
	}{true})
}

// handleRevokeAll ends every CLI session and pairing of the environment. It
// takes the administrator key and the configured origin, as issuing does.
func (m *Manager) handleRevokeAll(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		return methodNotAllowed(w, "POST")
	}
	if err := m.authorizeAdmin(r); err != nil {
		return err
	}
	origin, single := singleHeader(r, "Origin")
	if !single || origin != m.origin {
		return failure("origin_denied", "A revocation must be requested from the configured deployment origin.", 403)
	}
	var input struct {
		Confirm bool `json:"confirm"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		return err
	}
	if !input.Confirm {
		return failure("invalid_request", "Explicit revocation confirmation is required.", 400)
	}
	pairings, err := m.RevokeAll(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, struct {
		RevokedPairings int64 `json:"revoked_pairings"`
		SessionsRevoked bool  `json:"sessions_revoked"`
	}{pairings, true})
}

func (m *Manager) handleSession(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodGet && r.Method != http.MethodDelete {
		return methodNotAllowed(w, "GET, DELETE")
	}
	bearer, err := requestBearer(r)
	if err != nil {
		return err
	}
	action := "read"
	if r.Method == http.MethodDelete {
		action = "delete"
	}
	session, err := m.sessionOperation(r.Context(), digest(bearer), "", action)
	if err != nil {
		return err
	}
	if r.Method == http.MethodDelete {
		return writeJSON(w, struct {
			SessionID string `json:"session_id"`
			Revoked   bool   `json:"revoked"`
		}{session.ID, true})
	}
	return writeJSON(w, session)
}

func requestBearer(r *http.Request) (string, error) {
	header, single := singleHeader(r, "Authorization")
	parts := strings.Split(header, " ")
	if !single || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !validSecret(parts[1]) {
		return "", expired()
	}
	return parts[1], nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dest interface{}) error {
	contentType, single := singleHeader(r, "Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if !single || err != nil || mediaType != "application/json" {
		return failure("invalid_content_type", "Content-Type must be application/json.", 415)
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(dest)
	if err == nil {
		var trailing interface{}
		if trailingErr := decoder.Decode(&trailing); trailingErr != io.EOF {
			err = trailingErr
			if err == nil {
				err = errors.New("trailing JSON")
			}
		}
	}
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return failure("request_too_large", "Authorization request body exceeds 64 KiB.", 413)
		}
		return failure("invalid_request", "The authorization request must match the JSON input schema.", 400)
	}
	return nil
}

func methodNotAllowed(w http.ResponseWriter, methods string) error {
	w.Header().Set("Allow", methods)
	return failure("method_not_allowed", "This method is not supported for the authorization route.", 405)
}

func writeJSON(w http.ResponseWriter, value interface{}) error {
	// All response shapes contain only serializable fields. A disconnected
	// client does not trigger a second write or repeat a credential mutation.
	_ = json.NewEncoder(w).Encode(value)
	return nil
}

func writeError(w http.ResponseWriter, err error) {
	var public *Error
	if !errors.As(err, &public) {
		public = failure("internal_error", "Authorization request failed.", 500)
	}
	w.WriteHeader(public.HTTPStatus)
	_ = json.NewEncoder(w).Encode(struct {
		Status string `json:"status"`
		Error  *Error `json:"error"`
	}{"error", public})
}
