// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package cliauth provides deployment-key authorization and short-lived CLI grants and sessions.
package cliauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-redis/redis/v8"
)

const (
	ScopeReadonly    = "deployment_ops_readonly"
	GrantLifetime    = 5 * time.Minute
	SessionLifetime  = time.Hour
	RenewalThreshold = 10 * time.Minute
	redisTimeout     = time.Second
	// PairingIdleLifetime is how long a renewal credential lives unused. A
	// credential is spent and replaced at every renewal, so this bounds only a
	// device that stopped asking: a month covers a holiday and not a
	// forgotten laptop.
	PairingIdleLifetime = 30 * 24 * time.Hour
	// MaxPairings bounds the renewal credentials one environment holds. Past
	// it an exchange still logs in, without renewal, and says so.
	MaxPairings = 64
)

// Options uses an independently budgeted Redis client supplied by the caller.
// Now only controls the process-local HTTP rate window; all credentials use
// Redis TIME, including issuance and expiry checks.
type Options struct {
	Redis           redis.UniversalClient
	Prefix          string
	EnvironmentID   string
	EnvironmentName string
	PublicBaseURL   string
	AdminKey        string
	Now             func() time.Time
}

type Session struct {
	ID            string    `json:"session_id"`
	EnvironmentID string    `json:"environment_id"`
	Scope         string    `json:"scope"`
	ExpiresAt     time.Time `json:"expires_at"`
	Renewed       bool      `json:"renewed"`
	TokenHash     string    `json:"-"`
	pairingID     string
}

// Error contains a safe public message, never an underlying Redis error or a
// credential. HTTPStatus is also available to the channel handler.
type Error struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"-"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var authError *Error
	if errors.As(err, &authError) {
		return authError.Code
	}
	return "internal_error"
}

func failure(code, message string, status int) *Error {
	return &Error{Code: code, Message: message, HTTPStatus: status}
}

func expired() *Error {
	return failure("auth_expired_or_revoked", "The CLI session has expired or was revoked; authorize again from the authorization page.", 401)
}

func storeUnavailable() *Error {
	return failure("auth_store_unavailable", "The authorization store is unavailable; exchange outcomes may be unknown. Obtain a new code instead of retrying an exchange.", 503)
}

type Manager struct {
	client          redis.UniversalClient
	prefix          string
	environmentID   string
	environmentName string
	publicBaseURL   string
	origin          string
	adminHash       [sha256.Size]byte
	adminConfigured bool
	now             func() time.Time
	httpSlots       chan struct{}
	limitMu         sync.Mutex
	grantWindow     rateWindow
	exchangeWindow  rateWindow
	refreshWindow   rateWindow
	counts          counters
}

// New validates deployment coordinates without contacting Redis. An empty
// administrator key disables issuance; it never enables anonymous authorization.
func New(o Options) (*Manager, error) {
	if o.Redis == nil {
		return nil, errors.New("cliauth: Redis is required")
	}
	if !validText(o.Prefix, 256) || strings.ContainsAny(o.Prefix, "{}") {
		return nil, errors.New("cliauth: Prefix must be nonempty and contain no braces or control characters")
	}
	if !validText(o.EnvironmentID, 256) || !validText(o.EnvironmentName, 256) {
		return nil, errors.New("cliauth: environment identity and name are required, at most 256 bytes")
	}
	u, err := url.Parse(o.PublicBaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("cliauth: PublicBaseURL must be an HTTP(S) URL without userinfo, query or fragment")
	}
	if strings.Contains(u.Path, "\\") {
		return nil, errors.New("cliauth: PublicBaseURL path is invalid")
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." || strings.ContainsAny(segment, "\r\n\x00") {
			return nil, errors.New("cliauth: PublicBaseURL path is invalid")
		}
	}
	if o.AdminKey != "" && (len(o.AdminKey) < 32 || len(o.AdminKey) > 256 || !validAdminKey(o.AdminKey)) {
		return nil, errors.New("cliauth: AdminKey must contain 32 to 256 printable ASCII bytes without spaces when configured")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	// Browsers serialize origins with lowercase hosts and without default ports.
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "http" && u.Port() == "80") || (u.Scheme == "https" && u.Port() == "443") {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/"
	u.RawPath = ""
	return &Manager{
		client:        o.Redis,
		prefix:        o.Prefix + ".cli:{" + digest(o.EnvironmentID) + "}:",
		environmentID: o.EnvironmentID, environmentName: o.EnvironmentName,
		publicBaseURL: u.String(), origin: u.Scheme + "://" + u.Host,
		adminHash: sha256.Sum256([]byte(o.AdminKey)), adminConfigured: o.AdminKey != "",
		now: o.Now, httpSlots: make(chan struct{}, 4),
	}, nil
}

func validAdminKey(key string) bool {
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e {
			return false
		}
	}
	return true
}

func validText(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomSecret() (string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", failure("internal_error", "Unable to generate a credential.", 500)
	}
	return base64.RawURLEncoding.EncodeToString(secret[:]), nil
}

func validSecret(secret string) bool {
	if len(secret) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(secret)
	return err == nil && len(decoded) == 32
}

type storedRecord struct {
	SessionID     string `json:"session_id,omitempty"`
	EnvironmentID string `json:"environment_id"`
	Scope         string `json:"scope"`
	ExpiresAtMS   int64  `json:"expires_at_ms"`
	// Epoch is the environment's revocation epoch the credential was made
	// in; PairingID the renewal credential a session was issued with.
	Epoch     int64  `json:"epoch,omitempty"`
	PairingID string `json:"pairing_id,omitempty"`
	// CodeChallenge is a grant's PKCE challenge when the CLI asked for one:
	// the exchange must then carry the verifier. Never on a session.
	CodeChallenge string `json:"code_challenge,omitempty"`
}

func (r storedRecord) session(hash string, renewed bool) Session {
	return Session{ID: r.SessionID, EnvironmentID: r.EnvironmentID,
		Scope: r.Scope, ExpiresAt: time.UnixMilli(r.ExpiresAtMS).UTC(), Renewed: renewed, TokenHash: hash, pairingID: r.PairingID}
}

func (m *Manager) run(ctx context.Context, script string, keys []string, args ...interface{}) ([]interface{}, error) {
	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()
	// EVAL executes once rather than doing the EVALSHA/NOSCRIPT fallback round
	// trip. The injected client must disable automatic retries for auth writes.
	result, err := m.client.Eval(ctx, script, keys, args...).Slice()
	if err != nil {
		return nil, storeUnavailable()
	}
	return result, nil
}

func resultRecord(result []interface{}) (storedRecord, bool, error) {
	if len(result) < 1 {
		return storedRecord{}, false, storeUnavailable()
	}
	status, ok := result[0].(int64)
	if !ok {
		return storedRecord{}, false, storeUnavailable()
	}
	if status == 0 {
		return storedRecord{}, false, expired()
	}
	if len(result) < 3 || status != 1 {
		return storedRecord{}, false, storeUnavailable()
	}
	raw, ok := result[1].(string)
	renewed, renewOK := result[2].(int64)
	var record storedRecord
	if !ok || !renewOK || json.Unmarshal([]byte(raw), &record) != nil {
		return storedRecord{}, false, storeUnavailable()
	}
	return record, renewed == 1, nil
}

// Authenticate checks a raw bearer token against shared storage without renewal.
func (m *Manager) Authenticate(ctx context.Context, bearer string) (Session, error) {
	if !validSecret(bearer) {
		return Session{}, expired()
	}
	return m.sessionOperation(ctx, digest(bearer), "", "read")
}

// Admit must run after scope, input and execution budget checks, immediately
// before invoking the handler. It rechecks shared storage so expiry or logout
// between the initial authentication and admission cannot be bypassed.
func (m *Manager) Admit(ctx context.Context, session Session, renew bool) (Session, error) {
	if session.ID == "" || session.EnvironmentID != m.environmentID || len(session.TokenHash) != 64 {
		return Session{}, expired()
	}
	if _, err := hex.DecodeString(session.TokenHash); err != nil {
		return Session{}, expired()
	}
	action := "read"
	if renew {
		action = "renew"
	}
	return m.sessionOperation(ctx, session.TokenHash, session.ID, action)
}

func (m *Manager) sessionOperation(ctx context.Context, hash, expectedID, action string) (Session, error) {
	result, err := m.run(ctx, sessionScript, []string{m.prefix + "session:" + hash, m.epochKey()},
		m.environmentID, expectedID, action, ScopeReadonly, SessionLifetime.Milliseconds(), RenewalThreshold.Milliseconds())
	if err != nil {
		return Session{}, err
	}
	record, renewed, err := resultRecord(result)
	if err != nil {
		return Session{}, err
	}
	return record.session(hash, renewed), nil
}

type rateWindow struct {
	minute int64
	count  int
}

func (m *Manager) allow(window *rateWindow, limit int) bool {
	m.limitMu.Lock()
	defer m.limitMu.Unlock()
	minute := m.now().Unix() / 60
	if window.minute != minute {
		*window = rateWindow{minute: minute}
	}
	if window.count >= limit {
		return false
	}
	window.count++
	return true
}
