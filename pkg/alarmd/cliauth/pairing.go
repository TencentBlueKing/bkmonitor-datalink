// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package cliauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// A pairing is the durable half of an authorization: the administrator key
// is entered once, the exchange returns a short session and a renewal
// credential, and the CLI spends the credential for a new pair whenever its
// session runs out. The credential rolls - every use replaces it and the old
// one is gone - and it dies when unused for PairingIdleLifetime, when the
// environment's grants are revoked, or when the administrator key changes.
// Only its digest is stored.

func (m *Manager) epochKey() string    { return m.prefix + "epoch" }
func (m *Manager) pairingsKey() string { return m.prefix + "pairings" }
func pairingKey(prefix, secret string) string {
	return prefix + "pairing:" + digest(secret)
}

// adminBinding ties a pairing to the administrator key it was made under,
// without storing anything the key could be recovered from.
func (m *Manager) adminBinding() string {
	sum := sha256.Sum256(append([]byte("cli-pairing-binding:"), m.adminHash[:]...))
	return hex.EncodeToString(sum[:16])
}

// Counter names, closed.
const (
	CountGrantsIssued      = "grants_issued"
	CountExchanged         = "exchanged"
	CountExchangeRejected  = "exchange_rejected"
	CountPairingsIssued    = "pairings_issued"
	CountPairingsRefused   = "pairings_refused_full"
	CountRenewed           = "pairings_renewed"
	CountRenewalExpired    = "renewal_expired_or_revoked"
	CountRenewalKeyRotated = "renewal_admin_key_rotated"
	CountPairingsForgotten = "pairings_forgotten"
	CountRevokedAll        = "revoked_all"
	CountRenewalReplayed   = "renewal_replayed"
	CountStoreUnavailable  = "store_unavailable"
)

// Counts is every counter name, in the order a reader lists them.
var Counts = []string{CountGrantsIssued, CountExchanged, CountExchangeRejected, CountPairingsIssued, CountPairingsRefused,
	CountRenewed, CountRenewalReplayed, CountRenewalExpired, CountRenewalKeyRotated, CountPairingsForgotten, CountRevokedAll, CountStoreUnavailable}

type counters struct {
	mu     sync.Mutex
	values map[string]uint64
	last   map[string]time.Time
}

func (c *counters) add(name string, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.values == nil {
		c.values, c.last = map[string]uint64{}, map[string]time.Time{}
	}
	c.values[name]++
	c.last[name] = at
}

// Stats is this process's account of authorization since it started: every
// counter, zero included, and when each last moved.
type Stats struct {
	Counts map[string]uint64
	LastAt map[string]time.Time
}

// Stats reads the counters.
func (m *Manager) Stats() Stats {
	m.counts.mu.Lock()
	defer m.counts.mu.Unlock()
	stats := Stats{Counts: make(map[string]uint64, len(Counts)), LastAt: map[string]time.Time{}}
	for _, name := range Counts {
		stats.Counts[name] = m.counts.values[name]
		if at, ok := m.counts.last[name]; ok {
			stats.LastAt[name] = at
		}
	}
	return stats
}

func (m *Manager) count(name string) { m.counts.add(name, m.now()) }

// ActivePairings is how many renewal credentials were used within the idle
// lifetime, as the store holds them now.
func (m *Manager) ActivePairings(ctx context.Context) (int64, error) {
	result, err := m.run(ctx, activeScript, []string{m.pairingsKey()}, PairingIdleLifetime.Milliseconds())
	if err != nil {
		return 0, err
	}
	if len(result) != 2 {
		return 0, storeUnavailable()
	}
	count, ok := result[1].(int64)
	if !ok {
		return 0, storeUnavailable()
	}
	return count, nil
}

// Renewal is what a spent renewal credential buys: a session and the next
// credential.
type Renewal struct {
	Session      Session
	AccessToken  string
	RefreshToken string
	PairingID    string
}

func renewalFailure() *Error {
	return failure("renewal_expired_or_revoked", "The CLI pairing has expired or was revoked; authorize again from the authorization page.", 401)
}

// Refresh spends a renewal credential.
func (m *Manager) Refresh(ctx context.Context, refreshToken string) (Renewal, error) {
	if !validSecret(refreshToken) {
		m.count(CountRenewalExpired)
		return Renewal{}, renewalFailure()
	}
	token, err := randomSecret()
	if err != nil {
		return Renewal{}, err
	}
	next, err := randomSecret()
	if err != nil {
		return Renewal{}, err
	}
	id, err := randomSecret()
	if err != nil {
		return Renewal{}, err
	}
	sealed, err := sealRenewal(refreshToken, replayedRenewal{AccessToken: token, RefreshToken: next, SessionID: id})
	if err != nil {
		return Renewal{}, err
	}
	result, err := m.run(ctx, refreshScript,
		[]string{pairingKey(m.prefix, refreshToken), pairingKey(m.prefix, next), m.prefix + "session:" + digest(token), m.pairingsKey(), m.epochKey(),
			m.prefix + "spent:" + digest(refreshToken)},
		m.environmentID, ScopeReadonly, m.adminBinding(), id, SessionLifetime.Milliseconds(), PairingIdleLifetime.Milliseconds(),
		sealed, RenewalReplayGrace.Milliseconds())
	if err != nil {
		m.count(CountStoreUnavailable)
		return Renewal{}, err
	}
	if status, ok := firstStatus(result); ok && status == 6 {
		return m.replay(ctx, refreshToken, result)
	}
	if status, ok := firstStatus(result); ok && status == 4 {
		m.count(CountRenewalKeyRotated)
		return Renewal{}, failure("renewal_admin_key_rotated", "The deployment's administrator key changed since this CLI was paired; authorize again from the authorization page.", 401)
	}
	record, _, err := resultRecord(result)
	if ErrorCode(err) == "auth_expired_or_revoked" {
		m.count(CountRenewalExpired)
		return Renewal{}, renewalFailure()
	}
	if err != nil {
		return Renewal{}, err
	}
	m.count(CountRenewed)
	return Renewal{Session: record.session(digest(token), false), AccessToken: token, RefreshToken: next, PairingID: record.PairingID}, nil
}

// Upgrade pairs a live session that has none: one exchanged before this
// build, or one whose exchange found the bound full.
func (m *Manager) Upgrade(ctx context.Context, bearer string) (string, string, error) {
	if !validSecret(bearer) {
		return "", "", expired()
	}
	refresh, err := randomSecret()
	if err != nil {
		return "", "", err
	}
	id, err := randomSecret()
	if err != nil {
		return "", "", err
	}
	result, err := m.run(ctx, upgradeScript,
		[]string{m.prefix + "session:" + digest(bearer), pairingKey(m.prefix, refresh), m.pairingsKey(), m.epochKey()},
		m.environmentID, ScopeReadonly, id, m.adminBinding(), PairingIdleLifetime.Milliseconds(), MaxPairings)
	if err != nil {
		m.count(CountStoreUnavailable)
		return "", "", err
	}
	switch status, _ := firstStatus(result); status {
	case 5:
		return "", "", failure("already_paired", "This session already has a pairing; its renewal credential is on the device that exchanged it.", 409)
	case 3:
		m.count(CountPairingsRefused)
		return "", "", pairingsFull()
	}
	if _, _, err := resultRecord(result); err != nil {
		return "", "", err
	}
	m.count(CountPairingsIssued)
	return refresh, id, nil
}

func pairingsFull() *Error {
	return failure("pairing_limit_reached", "This environment holds the most CLI pairings it keeps; revoke them from the authorization page and pair again.", 409)
}

// Forget revokes one renewal credential: its holder logging out.
func (m *Manager) Forget(ctx context.Context, refreshToken string) error {
	if !validSecret(refreshToken) {
		return nil
	}
	result, err := m.run(ctx, forgetScript, []string{pairingKey(m.prefix, refreshToken), m.pairingsKey()}, m.environmentID)
	if err != nil {
		m.count(CountStoreUnavailable)
		return err
	}
	if status, _ := firstStatus(result); status == 1 {
		m.count(CountPairingsForgotten)
	}
	return nil
}

// RevokeAll revokes every session and renewal credential of the
// environment, returning how many pairings were held.
func (m *Manager) RevokeAll(ctx context.Context) (int64, error) {
	result, err := m.run(ctx, revokeAllScript, []string{m.epochKey(), m.pairingsKey()})
	if err != nil {
		m.count(CountStoreUnavailable)
		return 0, err
	}
	if len(result) != 3 {
		return 0, storeUnavailable()
	}
	pairings, _ := result[2].(int64)
	m.count(CountRevokedAll)
	return pairings, nil
}

func firstStatus(result []interface{}) (int64, bool) {
	if len(result) == 0 {
		return 0, false
	}
	status, ok := result[0].(int64)
	return status, ok
}

// RenewalReplayGrace is how long the answer to a renewal is kept for the
// credential it spent: a reply lost on the way is asked for again with the
// same credential and gets the same pair, not a lost pairing. A reuse inside
// the grace is this replay and not a theft signal; a reuse detector added
// later has to start counting after it.
const RenewalReplayGrace = 30 * time.Second

type replayedRenewal struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	SessionID    string `json:"session_id"`
}

// sealRenewal encrypts a renewal's answer under a key only the spent
// credential's holder can derive: the store holds it for the grace without
// holding anything a reader of the store could use.
func sealRenewal(spent string, answer replayedRenewal) (string, error) {
	plain, _ := json.Marshal(answer)
	block, _ := aes.NewCipher(replayKey(spent))
	aead, _ := cipher.NewGCM(block)
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", failure("internal_error", "Unable to generate a credential.", 500)
	}
	return base64.RawStdEncoding.EncodeToString(aead.Seal(nonce, nonce, plain, nil)), nil
}

func openRenewal(spent, sealed string) (replayedRenewal, bool) {
	raw, err := base64.RawStdEncoding.DecodeString(sealed)
	block, _ := aes.NewCipher(replayKey(spent))
	aead, _ := cipher.NewGCM(block)
	if err != nil || len(raw) < aead.NonceSize() {
		return replayedRenewal{}, false
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
	var answer replayedRenewal
	if err != nil || json.Unmarshal(plain, &answer) != nil {
		return replayedRenewal{}, false
	}
	return answer, true
}

func replayKey(spent string) []byte {
	sum := sha256.Sum256([]byte("cli-renewal-replay:" + spent))
	return sum[:]
}

// replay answers a renewal repeated within the grace with the pair the first
// one returned, if its session still stands: a revocation in between ends it.
func (m *Manager) replay(ctx context.Context, spent string, result []interface{}) (Renewal, error) {
	sealed, _ := result[1].(string)
	answer, ok := openRenewal(spent, sealed)
	if !ok {
		m.count(CountRenewalExpired)
		return Renewal{}, renewalFailure()
	}
	session, err := m.sessionOperation(ctx, digest(answer.AccessToken), answer.SessionID, "read")
	if ErrorCode(err) == "auth_expired_or_revoked" {
		m.count(CountRenewalExpired)
		return Renewal{}, renewalFailure()
	}
	if err != nil {
		return Renewal{}, err
	}
	m.count(CountRenewalReplayed)
	return Renewal{Session: session, AccessToken: answer.AccessToken, RefreshToken: answer.RefreshToken, PairingID: session.pairingID}, nil
}
