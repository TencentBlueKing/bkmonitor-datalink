// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package cliauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdtoken"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

// The front surface is what an admin server in front of the deployment
// calls, server to server, with the administrator key: grants issued to its
// users by name, the roster of those users, and a fixed set of reads (served
// by the channel, see obchannel.FrontReads). It lives outside /api/cli/ so a
// forwarder that passes /api/cli/* through never reaches it.
const (
	frontGrantsPath = "/api/front/v1/grants"
	frontRosterPath = "/api/front/v1/roster"
	// frontLinkdTokenPath is where the admin hands over the token alarmd
	// calls the link's API with, and reads whether alarmd still holds a
	// usable one (linkdtoken).
	frontLinkdTokenPath = "/api/front/v1/linkd-token"
	// maxLinkdTokenBodyBytes holds the admin's longest token in its body.
	maxLinkdTokenBodyBytes = 8 << 10
	// FrontReadPath is the front surface's read, served by the channel.
	FrontReadPath = "/api/front/v1/invoke"
	// MaxRosterOperators bounds one roster: about 64 bytes an operator, so
	// the largest roster is a few megabytes in one Redis set.
	MaxRosterOperators = 100000
	// maxRosterBodyBytes holds a roster of MaxRosterOperators names.
	maxRosterBodyBytes = 8 << 20
	// maxRevokedListed is how many revoked operators an answer names.
	maxRevokedListed = 20
	// revocationKept is how long an operator's revocation is kept: past it,
	// every credential issued to them before it was deleted at its next use
	// or has idled out.
	revocationKept = PairingIdleLifetime + SessionLifetime
)

// FrontPaths is every front route this package serves.
var FrontPaths = []string{frontGrantsPath, frontRosterPath, frontLinkdTokenPath}

// operatorName is an admin user name: letters, digits and . _ @ -, starting
// and ending with a letter or digit, at most 64 bytes.
var operatorName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._@-]{0,62}[A-Za-z0-9])?$`)

// ValidOperator is whether name can be an operator.
func ValidOperator(name string) bool { return operatorName.MatchString(name) }

// AuthorizeAdmin checks the administrator key on a front request.
func (m *Manager) AuthorizeAdmin(r *http.Request) error { return m.authorizeAdmin(r) }

// FrontHandler serves the front grants and roster routes.
func (m *Manager) FrontHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		defer boundBodyRead(w, r, 3*time.Second)()
		var err error
		switch r.URL.Path {
		case frontGrantsPath:
			err = m.handleFrontGrants(w, r)
		case frontRosterPath:
			err = m.handleRoster(w, r)
		case frontLinkdTokenPath:
			err = m.handleLinkdToken(w, r)
		default:
			err = failure("not_found", "Unknown front route.", 404)
		}
		if err != nil {
			writeError(w, err)
		}
	})
}

// issue stores a grant record under secret's digest and returns it as
// stored. A grant to an operator is refused until the first roster arrives
// and for an operator the roster does not name.
func (m *Manager) issue(ctx context.Context, secret string, record []byte) (storedRecord, error) {
	result, err := m.run(ctx, issueScript, []string{m.prefix + "grant:" + digest(secret), m.epochKey(), m.rosterKey(), m.membersKey()},
		string(record), GrantLifetime.Milliseconds())
	if err != nil {
		return storedRecord{}, err
	}
	switch status, _ := firstStatus(result); status {
	case 7:
		m.count(CountFrontRefusedNotSynced)
		return storedRecord{}, failure("roster_not_synced", "No operator roster has been synced yet; the admin syncs it when it starts.", 409)
	case 8:
		m.count(CountFrontRefusedNotOnRoster)
		return storedRecord{}, failure("operator_not_on_roster", "The operator is not on the synced roster.", 403)
	}
	issued, _, err := m.resultRecord(result)
	return issued, err
}

func (m *Manager) handleFrontGrants(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		return methodNotAllowed(w, "POST")
	}
	if err := m.authorizeAdmin(r); err != nil {
		return err
	}
	var input struct {
		Operator            string `json:"operator"`
		PublicBaseURL       string `json:"public_base_url"`
		Confirm             bool   `json:"confirm"`
		CodeChallenge       string `json:"code_challenge,omitempty"`
		CodeChallengeMethod string `json:"code_challenge_method,omitempty"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		return err
	}
	if !input.Confirm {
		return failure("invalid_request", "Explicit authorization confirmation is required.", 400)
	}
	if !ValidOperator(input.Operator) {
		return failure("operator_invalid", "operator must be 1-64 letters, digits or . _ @ -, starting and ending with a letter or digit.", 400)
	}
	baseURL, _, err := canonicalBaseURL(input.PublicBaseURL)
	if err != nil {
		return failure("invalid_public_base_url", "public_base_url "+err.Error()+".", 400)
	}
	if (input.CodeChallenge != "" || input.CodeChallengeMethod != "") && (input.CodeChallengeMethod != "S256" || !validSecret(input.CodeChallenge)) {
		return failure("invalid_code_challenge", "A code challenge must be an S256 challenge of 43 URL-safe characters.", 400)
	}
	if !m.allow(&m.frontGrantWindow, AuthEventsPerMinute) {
		return failure("front_rate_limited", "The named grant limit was reached; try again in the next minute.", 429)
	}
	secret, err := randomSecret()
	if err != nil {
		return err
	}
	record, _ := json.Marshal(storedRecord{EnvironmentID: m.environmentID, Scope: ScopeReadonly, CodeChallenge: input.CodeChallenge,
		Operator: input.Operator, IssuedVia: IssuedViaFront, PublicBaseURL: baseURL})
	issued, err := m.issue(r.Context(), secret, record)
	if err != nil {
		return err
	}
	m.count(CountFrontGrantsIssued)
	expiresAt := time.UnixMilli(issued.ExpiresAtMS).UTC()
	code, _ := json.Marshal(authorizationPackage{Version: "alarmd-login/v1", EnvironmentID: m.environmentID,
		EnvironmentName: m.environmentName, PublicBaseURL: baseURL, GrantSecret: secret, GrantExpiresAt: expiresAt})
	preview := grantPreview{EnvironmentID: m.environmentID, EnvironmentName: m.environmentName, PublicBaseURL: baseURL,
		Scope: ScopeReadonly, SupportedScopes: []string{ScopeReadonly},
		GrantTTLSeconds: int(GrantLifetime.Seconds()), SessionTTLSeconds: int(SessionLifetime.Seconds())}
	return writeJSON(w, struct {
		grantPreview
		AuthorizationCode string    `json:"authorization_code"`
		GrantExpiresAt    time.Time `json:"grant_expires_at"`
		Bound             bool      `json:"bound_to_challenge"`
		Operator          string    `json:"operator"`
		IssuedVia         string    `json:"issued_via"`
	}{preview, "alarmd-login-v1." + base64.RawURLEncoding.EncodeToString(code), expiresAt, input.CodeChallenge != "", input.Operator, IssuedViaFront})
}

// Roster is the operator roster as applied.
type Roster struct {
	Digest          string    `json:"digest"`
	OperatorsDigest string    `json:"operators_digest"`
	Operators       int       `json:"operators"`
	AppliedAt       time.Time `json:"applied_at"`
}

// RosterApplied is what applying a roster did.
type RosterApplied struct {
	Roster
	Applied              bool     `json:"applied"`
	RevokedOperators     []string `json:"revoked_operators"`
	RevokedOperatorCount int      `json:"revoked_operator_count"`
	RevokedPairings      int64    `json:"revoked_pairings"`
}

type storedRoster struct {
	Digest          string `json:"digest"`
	OperatorsDigest string `json:"operators_digest"`
	Operators       int    `json:"operators"`
	AppliedAtMS     int64  `json:"applied_at_ms"`
}

func (s storedRoster) roster() Roster {
	return Roster{Digest: s.Digest, OperatorsDigest: s.OperatorsDigest, Operators: s.Operators, AppliedAt: time.UnixMilli(s.AppliedAtMS).UTC()}
}

// operatorsDigest is the digest of a roster's operators, sorted and
// deduplicated, one per line.
func operatorsDigest(operators []string) string {
	sum := sha256.Sum256([]byte(strings.Join(operators, "\n")))
	return hex.EncodeToString(sum[:])
}

func rosterFailure(message string) *Error {
	return failure("roster_invalid", message, 400)
}

// ApplyRoster applies the admin's roster once per digest: the operators it
// drops lose their pairings and sessions; pairings made on the authorization
// page are not touched.
func (m *Manager) ApplyRoster(ctx context.Context, digestValue string, operators []string) (RosterApplied, error) {
	if !validText(digestValue, 128) || strings.ContainsAny(digestValue, " ") {
		m.count(CountRosterRejected)
		return RosterApplied{}, rosterFailure("digest must be 1-128 printable characters without spaces.")
	}
	if len(operators) == 0 || len(operators) > MaxRosterOperators {
		m.count(CountRosterRejected)
		return RosterApplied{}, rosterFailure(fmt.Sprintf("operators must name 1 to %d operators.", MaxRosterOperators))
	}
	sorted := slices.Clone(operators)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	for _, operator := range sorted {
		if !ValidOperator(operator) {
			m.count(CountRosterRejected)
			return RosterApplied{}, rosterFailure("every operator must be 1-64 letters, digits or . _ @ -, starting and ending with a letter or digit.")
		}
	}
	args := []interface{}{digestValue, operatorsDigest(sorted), revocationKept.Milliseconds()}
	for _, operator := range sorted {
		args = append(args, operator)
	}
	result, err := m.run(ctx, rosterScript, []string{m.rosterKey(), m.membersKey(), m.namedKey(), m.revokedKey(), m.pairingsKey()}, args...)
	if err != nil {
		m.count(CountStoreUnavailable)
		return RosterApplied{}, err
	}
	malformed := func() (RosterApplied, error) {
		return RosterApplied{}, m.storeFailed(redisfailure.MalformedReply, "the store answered in a shape no script returns")
	}
	status, _ := firstStatus(result)
	if status == 3 {
		m.count(CountRosterRejected)
		return RosterApplied{}, failure("roster_digest_conflict", "This digest was applied with another operator list.", 409)
	}
	if (status != 1 && status != 2) || len(result) < 2 {
		return malformed()
	}
	raw, _ := result[1].(string)
	var stored storedRoster
	if json.Unmarshal([]byte(raw), &stored) != nil {
		return malformed()
	}
	applied := RosterApplied{Roster: stored.roster(), RevokedOperators: []string{}}
	if status == 2 {
		m.count(CountRosterUnchanged)
		return applied, nil
	}
	if len(result) < 3 {
		return malformed()
	}
	applied.Applied = true
	applied.RevokedPairings, _ = result[2].(int64)
	for _, value := range result[3:] {
		if name, ok := value.(string); ok {
			applied.RevokedOperatorCount++
			if len(applied.RevokedOperators) < maxRevokedListed {
				applied.RevokedOperators = append(applied.RevokedOperators, name)
			}
		}
	}
	m.count(CountRosterApplied)
	m.countN(CountRosterPairingsRevoked, uint64(applied.RevokedPairings))
	if m.onRosterApplied != nil {
		m.onRosterApplied(applied.AppliedAt)
	}
	return applied, nil
}

// RosterStanding is the applied roster, if any, and how many pairings were
// issued to operators.
type RosterStanding struct {
	Synced        bool    `json:"synced"`
	Roster        *Roster `json:"roster,omitempty"`
	NamedPairings int64   `json:"named_pairings"`
}

// ReadRoster reads the applied roster.
func (m *Manager) ReadRoster(ctx context.Context) (RosterStanding, error) {
	result, err := m.run(ctx, rosterReadScript, []string{m.rosterKey(), m.namedKey()})
	if err != nil {
		return RosterStanding{}, err
	}
	if len(result) != 3 {
		return RosterStanding{}, m.storeFailed(redisfailure.MalformedReply, "the store answered in a shape no script returns")
	}
	raw, _ := result[1].(string)
	standing := RosterStanding{}
	standing.NamedPairings, _ = result[2].(int64)
	if raw != "" {
		var stored storedRoster
		if json.Unmarshal([]byte(raw), &stored) != nil {
			return RosterStanding{}, m.storeFailed(redisfailure.MalformedReply, "the store answered in a shape no script returns")
		}
		roster := stored.roster()
		standing.Synced, standing.Roster = true, &roster
	}
	return standing, nil
}

func (m *Manager) handleRoster(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		return methodNotAllowed(w, "GET, PUT")
	}
	if err := m.authorizeAdmin(r); err != nil {
		return err
	}
	if r.Method == http.MethodGet {
		standing, err := m.ReadRoster(r.Context())
		if err != nil {
			return err
		}
		return writeJSON(w, standing)
	}
	var input struct {
		Digest    string   `json:"digest"`
		Operators []string `json:"operators"`
	}
	if err := decodeJSONWithin(w, r, &input, maxRosterBodyBytes); err != nil {
		return err
	}
	applied, err := m.ApplyRoster(r.Context(), input.Digest, input.Operators)
	if err != nil {
		return err
	}
	return writeJSON(w, applied)
}

// LinkdTokens is the deployment's token for the link's API (linkdtoken.Store).
type LinkdTokens interface {
	Put(ctx context.Context, token string) (linkdtoken.Answer, error)
	State(ctx context.Context) (linkdtoken.Answer, error)
}

// ServeLinkdTokens makes the front surface take and report the token for the
// link's API; without it the route answers that this deployment keeps none.
func (m *Manager) ServeLinkdTokens(tokens LinkdTokens) { m.linkdTokens = tokens }

// handleLinkdToken answers the token's state, and on a POST first keeps the
// token handed over. The answer never carries the token, and no failure text
// carries it either.
func (m *Manager) handleLinkdToken(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return methodNotAllowed(w, "GET, POST")
	}
	if err := m.authorizeAdmin(r); err != nil {
		return err
	}
	if m.linkdTokens == nil {
		return failure("linkd_token_unused", "This deployment reads no link through the admin, and keeps no token for it.", 404)
	}
	var answer linkdtoken.Answer
	var err error
	if r.Method == http.MethodPost {
		var input struct {
			Token string `json:"token"`
		}
		if err := decodeJSONWithin(w, r, &input, maxLinkdTokenBodyBytes); err != nil {
			return err
		}
		answer, err = m.linkdTokens.Put(r.Context(), input.Token)
		if errors.Is(err, linkdtoken.ErrInvalidToken) {
			return failure("invalid_token", "The token is not one the admin issues, or it has expired.", 400)
		}
	} else {
		answer, err = m.linkdTokens.State(r.Context())
	}
	if err != nil {
		return failure("store_unavailable", "The runtime store did not answer.", 503)
	}
	return json.NewEncoder(w).Encode(answer)
}
