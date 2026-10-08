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
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const channelVersion = "alarmd-ob/v1"
const sessionScope = "deployment_ops_readonly"
const maxResponse = 8 << 20

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
	client, u, release, err := a.environmentClient(p)
	defer release()
	if err != nil {
		return nil, 0, err
	}
	u.Path += endpoint
	var payload []byte
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, 0, errors.New("cannot encode request")
		}
	}
	req, err := http.NewRequest(method, u.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, 0, errors.New("cannot construct request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "alarmd-cli/"+a.Version)
	if p.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.AccessToken)
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
		switch {
		case errors.As(err, &dns):
			return nil, 0, errors.New("DNS resolution failed; check the deployment hostname and DNS access")
		case connectionRefused(err):
			return nil, 0, errors.New("connection refused; check the deployment address, port and listener")
		case errors.As(err, &hostname):
			return nil, 0, errors.New("TLS certificate hostname mismatch; check the entry URL or log in with --insecure-tls for this environment")
		case errors.As(err, &authority):
			return nil, 0, errors.New("TLS certificate is not trusted; log in with --ca-cert or --insecure-tls for this environment")
		case errors.As(err, &certificate):
			return nil, 0, errors.New("TLS certificate is invalid; check its validity or log in with --insecure-tls for this environment")
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
	if err != nil || u != expected || out.EnvironmentID != p.EnvironmentID || out.EnvironmentName == "" || out.SessionID == "" || out.Scope != sessionScope {
		return Profile{}, errors.New("session environment, entry URL or readonly scope does not match")
	}
	if _, err := time.Parse(time.RFC3339, out.ExpiresAt); err != nil {
		return Profile{}, errors.New("server returned an invalid session expiry")
	}
	if !validSecret(out.AccessToken) {
		return Profile{}, errors.New("server returned an invalid access token")
	}
	out.PublicBaseURL = u
	out.CACert = p.CACert
	out.InsecureTLS = p.InsecureTLS
	return out, nil
}

func validateStatus(m map[string]any, p Profile) (string, error) {
	if stringField(m, "environment_id") != p.EnvironmentID || stringField(m, "scope") != sessionScope || stringField(m, "session_id") != p.SessionID {
		return "", errors.New("session status environment, readonly scope or session ID does not match")
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
