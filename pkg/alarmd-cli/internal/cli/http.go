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
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const channelVersion = "alarmd-ob/v1"

// sessionScope is the one scope a server grants: the deployment's operations
// session, execution and management included. The identifier is historical.
const sessionScope = "deployment_ops_readonly"

// maxResponse is the largest answer the CLI reads: the channel's own
// ceiling, a 32nd of the deployment's pod memory capped at 256 MiB.
const maxResponse = 256<<20 + 64<<10

// maxInputBytes is the largest request the CLI sends: the channel's
// envelope, a whole chart's manifests in one call.
const maxInputBytes = 16 << 20

// requestBound is the largest request the CLI sends for an operation whose
// server allows limit bytes: the smaller of the two, so a newer server's
// larger limit is used, not refused.
func requestBound(limit int64) int64 {
	return min(limit, maxInputBytes)
}

// requestNotSent marks failures that happen before the channel can receive
// an invocation. All other transport or response failures leave exec unknown.
type requestNotSent struct{ error }

func notSent(message string) error { return &requestNotSent{errors.New(message)} }

// entryHeader tells the deployment a request came through its own address
// rather than the entry the session was issued through. The admin forwards
// a fixed list of headers and this is not on it, so it never arrives through
// the admin.
const entryHeader = "X-Alarmd-Entry"
const entryAlarmd = "alarmd"

// Why a request is handed to the fallback entry: the admin application
// itself is down, which is all the fallback is for (the user's ruling of
// 2026-10-19: it shares the admin's domain and covers the admin failing,
// not the network). admin_unreachable, the admin answered that it could not
// reach alarmd; gateway, the ingress answered 502 or 503 with no JSON in it,
// because no admin backend was ready to take the request. A failure to reach
// the domain at all - no address, refused, no route - is never one: on one
// origin the fallback fails the same way. Nor is a 504 (the admin's
// alarmd_timeout): alarmd may have run the call.
const (
	fallbackAdminUnreachable = "admin_unreachable"
	fallbackGateway          = "gateway"
)

// maxFallbackEntries is how many fallback entries a server may declare: the
// deployment's own address, one.
const maxFallbackEntries = 1

// validScope and allowsScope know the one scope. No server a CLI talks to
// ever issued another: the execution scope an earlier build defined was
// gone before any deployment ran it.
func validScope(scope string) bool { return scope == sessionScope }
func allowsScope(granted, required string) bool {
	return validScope(granted) && required == sessionScope
}

func baseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || u.Opaque != "" {
		return nil, errors.New("public_base_url must be HTTP or HTTPS without userinfo, query or fragment")
	}
	// Reject encoded separators as well as dot segments; never clean a supplied
	// path and silently send a secret to the resulting different endpoint.
	if strings.ContainsAny(u.Path, "\\\x00\r\n") || strings.Contains(strings.ToLower(u.EscapedPath()), "%2f") || strings.Contains(strings.ToLower(u.EscapedPath()), "%5c") || strings.Contains(u.Path, "%") {
		return nil, errors.New("public_base_url has an unsafe path")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return nil, errors.New("public_base_url has path traversal")
		}
	}
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/"
	u.RawPath = ""
	return u, nil
}

func normalizedURL(raw string) (string, error) {
	u, err := baseURL(raw)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// environmentClient is the HTTP client an environment's profile allows: its
// entry URL, its TLS verification mode, no redirects, no cookie jar and
// the network deadline. release closes a transport cloned for it.
func (a *App) environmentClient(p Profile) (client http.Client, u *url.URL, release func(), err error) {
	release = func() {}
	u, err = baseURL(p.PublicBaseURL)
	if err != nil {
		return client, nil, release, err
	}
	client = *a.HTTP
	if u.Scheme == "http" && (p.InsecureTLS || p.CACert != "") {
		return client, nil, release, errors.New("TLS options apply only to HTTPS environments; log in without TLS options")
	}
	if p.InsecureTLS && p.CACert != "" {
		return client, nil, release, errors.New("profile cannot combine insecure TLS with a private CA; log in with one option")
	}
	if p.CACert != "" || p.InsecureTLS {
		transport, ok := client.Transport.(*http.Transport)
		if client.Transport == nil {
			transport, ok = http.DefaultTransport.(*http.Transport)
		}
		if !ok {
			return client, nil, release, errors.New("cannot configure environment TLS on this transport")
		}
		transport = transport.Clone()
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		} else {
			transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		}
		if p.CACert != "" {
			roots, err := certPool(p.CACert)
			if err != nil {
				return client, nil, release, err
			}
			transport.TLSClientConfig.RootCAs = roots
		}
		// Only the operator's explicit local login option can enable this.
		// Clone the transport so another environment never inherits it.
		transport.TLSClientConfig.InsecureSkipVerify = p.InsecureTLS
		client.Transport = transport
		release = transport.CloseIdleConnections
	}
	client.Jar = nil
	client.Timeout = 30 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client, u, release, nil
}

func (a *App) request(p Profile, method, endpoint string, body any) (map[string]any, int, error) {
	return a.requestTimeout(p, method, endpoint, body, 30*time.Second)
}

func (a *App) requestTimeout(p Profile, method, endpoint string, body any, timeout time.Duration) (map[string]any, int, error) {
	return a.requestVia(p, method, endpoint, body, timeout, false)
}

// requestVia sends the request through the session's entry and, when the
// admin application behind it is down, through the fallback entry the server
// declared (Profile.FallbackBaseURLs) on the same origin. The server is the
// only source of that entry; there is no option that adds to it, and one on
// another origin is never used. exec is a call that executes something: it
// never moves by itself, because an admin that lost alarmd mid-call, or an
// ingress in front of it, answers the same way whether or not alarmd had
// already started the command; it says where it could go instead, for the
// operator to re-run with --entry alarmd. meta.client_entry says which
// entry answered.
func (a *App) requestVia(p Profile, method, endpoint string, body any, timeout time.Duration, exec bool) (map[string]any, int, error) {
	moves := func(why string) bool { return why != "" && !exec }
	result, status, err, why := a.attempt(p, method, endpoint, body, timeout, p.via)
	entry := map[string]any{"entry": "session", "url": p.PublicBaseURL}
	if p.via != "" {
		entry = map[string]any{"entry": p.via, "url": p.PublicBaseURL, "reason": "requested"}
	}
	if why != "" && !moves(why) && len(p.FallbackBaseURLs) > 0 {
		// An executing call that may have reached alarmd is not repeated;
		// the operator is told where it could go once they have checked.
		hint := fmt.Sprintf("the session's entry did not reach alarmd (%s) and this call executes something that may already have run, so it was not repeated through the deployment's own entry %s; check sre.receipts and re-run with --entry alarmd if it did not run", why, p.FallbackBaseURLs[0])
		fmt.Fprintln(a.Err, "Warning: "+hint+".")
		entry["fallback_available"] = p.FallbackBaseURLs[0]
		if err != nil {
			err = fmt.Errorf("%w; %s", err, hint)
		}
	}
	for _, fallback := range p.FallbackBaseURLs {
		if !moves(why) || !sameOrigin(fallback, p.PublicBaseURL) {
			break
		}
		fmt.Fprintf(a.Err, "Warning: the session's entry did not reach alarmd (%s); calling the deployment's own entry %s.\n", why, fallback)
		via := p
		via.PublicBaseURL = fallback
		entry = map[string]any{"entry": entryAlarmd, "url": fallback, "reason": why}
		result, status, err, why = a.attempt(via, method, endpoint, body, timeout, entryAlarmd)
	}
	if meta := objectField(result, "meta"); meta != nil {
		meta["client_entry"] = entry
	}
	return result, status, err
}

// attempt is one request through p's entry. why is set when the answer
// shows the request did not reach alarmd through it (see requestVia).
func (a *App) attempt(p Profile, method, endpoint string, body any, timeout time.Duration, entry string) (result map[string]any, status int, err error, why string) {
	result, status, err = a.send(p, method, endpoint, body, timeout, entry)
	switch {
	case status == http.StatusBadGateway && err == nil && result["status"] == nil &&
		stringField(objectField(result, "error"), "code") == "alarmd_unreachable":
		why = fallbackAdminUnreachable
	case (status == http.StatusBadGateway || status == http.StatusServiceUnavailable) && result == nil:
		why = fallbackGateway
	}
	return result, status, err, why
}

func (a *App) send(p Profile, method, endpoint string, body any, timeout time.Duration, entry string) (map[string]any, int, error) {
	client, u, release, err := a.environmentClient(p)
	defer release()
	if err != nil {
		return nil, 0, &requestNotSent{error: err}
	}
	client.Timeout = timeout
	u.Path += endpoint
	var payload []byte
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, 0, notSent("cannot encode request")
		}
	}
	req, err := http.NewRequest(method, u.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, 0, notSent("cannot construct request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "alarmd-cli/"+a.Version)
	if p.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.AccessToken)
	}
	if entry != "" {
		req.Header.Set(entryHeader, entry)
	}
	resp, err := client.Do(req)
	if err != nil {
		// Keep actionable connection causes without exposing a URL, proxy
		// credential or header from the underlying transport error.
		var dns *net.DNSError
		var hostname x509.HostnameError
		var authority x509.UnknownAuthorityError
		var certificate x509.CertificateInvalidError
		var timeout net.Error
		var dial *net.OpError
		switch {
		case errors.As(err, &dns):
			return nil, 0, notSent("DNS resolution failed; check the deployment hostname and DNS access")
		case errors.Is(err, syscall.ECONNREFUSED):
			return nil, 0, notSent("connection refused; check the deployment address, port and listener")
		case errors.As(err, &dial) && dial.Op == "dial":
			return nil, 0, notSent("cannot connect to the deployment entry; check its address and network route")
		case errors.As(err, &hostname):
			return nil, 0, notSent("TLS certificate hostname mismatch; check the entry URL or log in with --insecure-tls for this environment")
		case errors.As(err, &authority):
			return nil, 0, notSent("TLS certificate is not trusted; log in with --ca-cert or --insecure-tls for this environment")
		case errors.As(err, &certificate):
			return nil, 0, notSent("TLS certificate is invalid; check its validity or log in with --insecure-tls for this environment")
		case errors.As(err, &timeout) && timeout.Timeout():
			return nil, 0, errors.New("request timed out; check the deployment network and service availability")
		}
		if u.Scheme == "http" || p.InsecureTLS {
			return nil, 0, errors.New("request failed; verify network and environment entry URL (no automatic retry)")
		}
		return nil, 0, errors.New("HTTPS request failed; verify network, trusted CA and hostname (no automatic retry)")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, resp.StatusCode, errors.New("HTTP redirects are forbidden")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, resp.StatusCode, errors.New("response read failed; remote outcome is unknown")
	}
	if len(data) > maxResponse {
		return nil, resp.StatusCode, errors.New("response exceeds the 8 MiB limit; no partial JSON was decoded")
	}
	result, err := decodeObject(data)
	if err != nil {
		return nil, resp.StatusCode, errors.New("server did not return a JSON object; verify channel support and entry URL")
	}
	meta := objectField(result, "meta")
	if meta == nil {
		meta = map[string]any{}
		result["meta"] = meta
	}
	verification := "system"
	if u.Scheme == "http" {
		verification = "not_applicable"
	}
	if p.CACert != "" {
		verification = "private_ca"
	}
	if p.InsecureTLS {
		verification = "disabled_explicitly"
	}
	meta["client_transport"] = map[string]any{"scheme": u.Scheme, "encrypted": u.Scheme == "https", "tls_verification": verification}
	// When to come back, as a refusal said it: the header is not in the
	// body, and a caller that waits needs it.
	if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds >= 0 {
		meta["client_retry_after_seconds"] = seconds
	}
	return result, resp.StatusCode, nil
}

// Private deployment CAs extend, rather than replace, the operating system
// roots. They do not disable either chain or hostname verification.
func certPool(path string) (*x509.CertPool, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("CA certificate path must be absolute")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot read the configured CA certificate file; no request was sent")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("cannot read CA certificate file within its 1 MiB limit; no request was sent")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, errors.New("cannot load system certificate roots; no request was sent")
	}
	if !roots.AppendCertsFromPEM(data) {
		return nil, errors.New("CA certificate file contains no valid PEM certificates; no request was sent")
	}
	return roots, nil
}

func decodeObject(data []byte) (map[string]any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var result map[string]any
	if err := d.Decode(&result); err != nil || result == nil {
		return nil, errors.New("expected a JSON object")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, errors.New("expected exactly one JSON object")
	}
	return result, nil
}

func stringField(m map[string]any, key string) string { s, _ := m[key].(string); return s }
func objectField(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func validateExchange(m map[string]any, p Profile) (Profile, error) {
	var out Profile
	out.EnvironmentID = stringField(m, "environment_id")
	out.EnvironmentName = stringField(m, "environment_name")
	out.PublicBaseURL = stringField(m, "public_base_url")
	out.Scope = stringField(m, "scope")
	out.SessionID = stringField(m, "session_id")
	out.ExpiresAt = stringField(m, "expires_at")
	out.AccessToken = stringField(m, "access_token")
	u, err := normalizedURL(out.PublicBaseURL)
	expected, _ := normalizedURL(p.PublicBaseURL)
	expectedScope := p.Scope
	if expectedScope == "" {
		expectedScope = sessionScope
	}
	if err != nil || u != expected || out.EnvironmentID != p.EnvironmentID || out.EnvironmentName == "" || out.SessionID == "" || !validScope(out.Scope) || out.Scope != expectedScope {
		return Profile{}, errors.New("session environment, entry URL or requested scope does not match")
	}
	if _, err := time.Parse(time.RFC3339, out.ExpiresAt); err != nil {
		return Profile{}, errors.New("server returned an invalid session expiry")
	}
	if !validSecret(out.AccessToken) {
		return Profile{}, errors.New("server returned an invalid access token")
	}
	if out.FallbackBaseURLs, err = fallbackEntries(m["fallback_base_urls"], u); err != nil {
		return Profile{}, err
	}
	out.PublicBaseURL = u
	out.CACert = p.CACert
	out.InsecureTLS = p.InsecureTLS
	return out, nil
}

// fallbackEntries are the fallback entries a server declares: at most one, a
// valid entry URL on the session's own origin (scheme, host and port) with
// another path. Absent is none; anything else refuses the answer, as any
// other malformed field of it would.
func fallbackEntries(raw any, primary string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok || len(list) > maxFallbackEntries {
		return nil, errors.New("server returned an invalid fallback entry list")
	}
	var out []string
	for _, item := range list {
		text, _ := item.(string)
		u, err := normalizedURL(text)
		if err != nil || u == primary || !sameOrigin(u, primary) || slices.Contains(out, u) {
			return nil, errors.New("server returned an invalid fallback entry")
		}
		out = append(out, u)
	}
	return out, nil
}

// sameOrigin says two entry URLs share scheme, host and port.
func sameOrigin(a, b string) bool {
	left, err := baseURL(a)
	if err != nil {
		return false
	}
	right, err := baseURL(b)
	return err == nil && left.Scheme == right.Scheme && left.Host == right.Host
}

func validateStatus(m map[string]any, p Profile) (string, error) {
	if stringField(m, "environment_id") != p.EnvironmentID || stringField(m, "scope") != p.Scope || !validScope(p.Scope) || stringField(m, "session_id") != p.SessionID {
		return "", errors.New("session status environment, scope or session ID does not match")
	}
	expiry := stringField(m, "expires_at")
	if _, err := time.Parse(time.RFC3339, expiry); err != nil {
		return "", errors.New("server returned an invalid session expiry")
	}
	return expiry, nil
}

func validateRevocation(m map[string]any, p Profile) error {
	if stringField(m, "session_id") != p.SessionID || m["revoked"] != true {
		return errors.New("server did not confirm revocation of the requested session")
	}
	return nil
}

func validSecret(s string) bool {
	if s == "" || len(s) > 4096 {
		return false
	}
	for _, c := range s {
		if c <= 32 || c >= 127 {
			return false
		}
	}
	return true
}

func validateChannel(m map[string]any, p Profile) error {
	meta := objectField(m, "meta")
	if stringField(meta, "channel_version") != channelVersion {
		return errors.New("unsupported channel version; upgrade compatibility must be checked")
	}
	if stringField(meta, "environment_id") != p.EnvironmentID {
		return errors.New("response environment does not match --env")
	}
	if stringField(meta, "catalog_revision") == "" {
		return errors.New("response is missing catalog_revision")
	}
	if _, err := time.Parse(time.RFC3339, stringField(meta, "responded_at")); err != nil {
		return errors.New("response is missing a valid responded_at")
	}
	session := objectField(meta, "session")
	if stringField(session, "session_id") != p.SessionID {
		return errors.New("response session does not match the request")
	}
	if _, err := time.Parse(time.RFC3339, stringField(session, "expires_at")); err != nil {
		return errors.New("response is missing a valid session expiry")
	}
	if _, ok := session["renewed"].(bool); !ok {
		return errors.New("response is missing session.renewed")
	}
	status := stringField(m, "status")
	if status != "ok" && status != "partial" && status != "error" {
		return errors.New("response has an invalid status")
	}
	evidence := objectField(m, "evidence")
	complete, ok := evidence["complete"].(bool)
	if !ok {
		return errors.New("response is missing evidence.complete")
	}
	if _, ok := evidence["limitations"].([]any); !ok {
		return errors.New("response is missing evidence.limitations")
	}
	if (status == "ok") != complete {
		return errors.New("response status and evidence completeness disagree")
	}
	return nil
}
