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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"
)

type App struct {
	In       io.Reader
	Out, Err io.Writer
	Version  string
	Store    Store
	HTTP     *http.Client
}

func New(in io.Reader, out, stderr io.Writer, version string) *App {
	return &App{In: in, Out: out, Err: stderr, Version: version, HTTP: &http.Client{}}
}

type options struct {
	env, input, caCert string
	url, port, state   string
	timeout            time.Duration
	expectBuild        string
	window             time.Duration
	hasWindow          bool
	hasInput, rebind   bool
	insecureTLS        bool
	args               []string
}

func parse(args []string) (options, error) {
	var o options
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, equal := strings.Cut(arg, "=")
		if name == "--env" || name == "--input" || name == "--ca-cert" || name == "--url" || name == "--port" || name == "--state" || name == "--timeout" ||
			name == "--expect-build" || name == "--window" {
			if seen[name] {
				return o, errors.New("flags may only be provided once")
			}
			seen[name] = true
			if !equal {
				i++
				if i == len(args) {
					return o, errors.New("flag requires a value")
				}
				value = args[i]
			}
			if value == "" {
				return o, errors.New("flag requires a nonempty value")
			}
			if name == "--expect-build" {
				o.expectBuild = value
			} else if name == "--window" {
				d, err := time.ParseDuration(value)
				if err != nil || d < 0 || d > time.Hour {
					return o, errors.New("--window must be a duration from 0 up to 1h, such as 5m")
				}
				o.window, o.hasWindow = d, true
			} else if name == "--env" {
				o.env = value
			} else if name == "--url" {
				o.url = value
			} else if name == "--port" {
				o.port = value
			} else if name == "--state" {
				o.state = value
			} else if name == "--timeout" {
				d, err := time.ParseDuration(value)
				if err != nil || d <= 0 || d > time.Hour {
					return o, errors.New("--timeout must be a duration up to 1h, such as 5m")
				}
				o.timeout = d
			} else if name == "--ca-cert" {
				o.caCert = value
			} else {
				o.input = value
				o.hasInput = true
			}
		} else if arg == "--insecure-tls" {
			if o.insecureTLS {
				return o, errors.New("--insecure-tls may only be provided once")
			}
			o.insecureTLS = true
		} else if arg == "--rebind" {
			if o.rebind {
				return o, errors.New("--rebind may only be provided once")
			}
			o.rebind = true
		} else if strings.HasPrefix(arg, "-") {
			return o, errors.New("unknown flag; run --help")
		} else {
			o.args = append(o.args, arg)
		}
	}
	return o, nil
}

func (a *App) Run(args []string) int {
	if len(args) == 1 && args[0] == "--version" {
		return a.print(map[string]any{"name": "alarmd-cli", "version": a.Version, "channel_version": channelVersion})
	}
	for i, arg := range args {
		if arg == "--help" || arg == "-h" || (i == 0 && arg == "help") {
			return a.help(args)
		}
	}
	if len(args) == 0 {
		return a.help(nil)
	}
	o, err := parse(args)
	if err != nil {
		return a.fail("invalid_input", err.Error(), 2)
	}
	if len(o.args) == 0 {
		return a.fail("invalid_input", "a command is required; run --help", 2)
	}
	command := o.args[0]
	if (command == "auth" || command == "profile") && len(o.args) >= 2 {
		command += " " + o.args[1]
		o.args = o.args[2:]
	} else {
		o.args = o.args[1:]
	}
	if o.rebind && command != "auth login" && command != "auth listen" {
		return a.fail("invalid_input", "--rebind is only valid with auth login or auth listen", 2)
	}
	if (o.url != "" || o.port != "" || o.state != "" || o.timeout != 0) && command != "auth listen" {
		return a.fail("invalid_input", "--url, --port, --state and --timeout are only valid with auth listen", 2)
	}
	if (o.expectBuild != "" || o.hasWindow) && command != "accept" {
		return a.fail("invalid_input", "--expect-build and --window are only valid with accept", 2)
	}
	if o.hasInput && command != "invoke" {
		return a.fail("invalid_input", "--input is only valid with invoke", 2)
	}
	login := command == "auth login" || (command == "auth listen" && o.url != "")
	if o.caCert != "" && (!login || !filepath.IsAbs(o.caCert)) {
		return a.fail("invalid_input", "--ca-cert requires an absolute PEM file path and is only valid with auth login or auth listen --url", 2)
	}
	if o.insecureTLS && (!login || o.caCert != "") {
		return a.fail("invalid_input", "--insecure-tls is only valid with auth login or auth listen --url and cannot be combined with --ca-cert", 2)
	}
	needOperation := command == "describe" || command == "invoke" || command == "profile use"
	if (needOperation && len(o.args) != 1) || (!needOperation && len(o.args) != 0) {
		return a.fail("invalid_input", "incorrect command arguments; run the command with --help", 2)
	}
	switch command {
	case "auth login", "auth listen", "profile list", "profile use":
	case "auth status", "auth logout", "discover", "describe", "invoke", "diagnose", "accept":
		if o.env == "" {
			return a.fail("invalid_input", "remote commands require explicit --env <environment_id>; run profile list", 2)
		}
	default:
		return a.fail("invalid_input", "unknown command; run --help", 2)
	}
	if (command == "profile list" || command == "profile use") && o.env != "" {
		return a.fail("invalid_input", "profile commands do not accept --env", 2)
	}
	if a.Store.Dir == "" {
		dir := os.Getenv("ALARMD_CLI_CONFIG_DIR")
		if dir == "" {
			dir, err = os.UserConfigDir()
			if err != nil {
				return a.fail("configuration_error", "cannot locate user configuration directory", 1)
			}
			dir = filepath.Join(dir, "alarmd-cli")
		}
		a.Store.Dir, err = filepath.Abs(dir)
		if err != nil {
			return a.fail("configuration_error", "cannot resolve configuration directory", 1)
		}
	}
	switch command {
	case "auth login":
		return a.login(o)
	case "auth listen":
		return a.listen(o)
	case "profile list":
		result, err := a.Store.list()
		if err != nil {
			return a.fail("configuration_error", err.Error(), 1)
		}
		return a.print(result)
	case "profile use":
		if err := a.Store.use(o.args[0]); err != nil {
			return a.fail("configuration_error", err.Error(), 1)
		}
		return a.print(map[string]any{"status": "ok", "default_environment": o.args[0], "summary": "Human preference recorded; remote commands still require --env."})
	}
	var params map[string]any
	if command == "invoke" {
		params, err = inputObject(o)
		if err != nil {
			return a.fail("invalid_input", err.Error(), 2)
		}
	}
	p, err := a.Store.get(o.env)
	if err != nil {
		return a.fail("configuration_error", err.Error(), 1)
	}
	if _, err := baseURL(p.PublicBaseURL); err != nil {
		return a.fail("configuration_error", err.Error(), 1)
	}
	if command != "auth logout" {
		renewed, err := a.ensureSession(o.env, false)
		if err != nil {
			var gone *pairingGone
			if errors.As(err, &gone) {
				// The stored profile still says where the environment is;
				// the failed renewal returned none.
				return a.lapsed(p, gone.code+": "+err.Error())
			}
			return a.fail("renewal_failed", err.Error(), 1)
		}
		p = renewed
	}
	if p.EnvironmentID != o.env || (p.AccessToken != "" && p.Scope != sessionScope) || (p.AccessToken == "" && p.RefreshToken == "") {
		return a.fail("configuration_error", "stored profile environment, token or scope is invalid; log in again", 1)
	}
	if command == "auth status" || command == "auth logout" {
		return a.session(command, p)
	}
	if command == "diagnose" {
		return a.diagnose(p)
	}
	if command == "accept" {
		window := acceptDefaultWindow
		if o.hasWindow {
			window = o.window
		}
		return a.accept(p, o.expectBuild, window)
	}
	operation := ""
	if len(o.args) > 0 {
		operation = o.args[0]
	}
	return a.channel(command, operation, params, p)
}

func inputObject(o options) (map[string]any, error) {
	if !o.hasInput {
		return map[string]any{}, nil
	}
	data := []byte(o.input)
	if strings.HasPrefix(o.input, "@") {
		f, err := os.Open(strings.TrimPrefix(o.input, "@"))
		if err != nil {
			return nil, errors.New("cannot read --input file")
		}
		defer f.Close()
		data, err = io.ReadAll(io.LimitReader(f, (1<<20)+1))
		if err != nil {
			return nil, errors.New("cannot read --input file")
		}
	}
	if len(data) > 1<<20 {
		return nil, errors.New("--input exceeds 1 MiB")
	}
	result, err := decodeObject(data)
	if err != nil {
		return nil, errors.New("--input must contain exactly one JSON object")
	}
	return result, nil
}

type loginBundle struct {
	Version         string `json:"version"`
	EnvironmentID   string `json:"environment_id"`
	EnvironmentName string `json:"environment_name"`
	PublicBaseURL   string `json:"public_base_url"`
	GrantSecret     string `json:"grant_secret"`
	GrantExpiresAt  string `json:"grant_expires_at"`
}

func parseBundle(code string) (loginBundle, error) {
	var b loginBundle
	encoded, ok := strings.CutPrefix(strings.TrimSpace(code), "alarmd-login-v1.")
	if !ok {
		return b, errors.New("expected an alarmd-login-v1 authorization code from the trusted OB page")
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return b, errors.New("authorization code is not valid base64url")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&b); err != nil {
		return b, errors.New("authorization code has an invalid schema")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return b, errors.New("authorization code must contain exactly one object")
	}
	if b.Version != "alarmd-login/v1" || b.EnvironmentID == "" || b.EnvironmentName == "" || !validSecret(b.GrantSecret) {
		return b, errors.New("authorization code has an invalid version, environment or secret")
	}
	if _, err := time.Parse(time.RFC3339, b.GrantExpiresAt); err != nil {
		return b, errors.New("authorization code has an invalid expiry")
	}
	b.PublicBaseURL, err = normalizedURL(b.PublicBaseURL)
	return b, err
}

func (a *App) login(o options) int {
	var data []byte
	var err error
	if f, ok := a.In.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(a.Err, "Paste the authorization code from your trusted OB page (input hidden): ")
		data, err = term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(a.Err)
	} else {
		data, err = io.ReadAll(io.LimitReader(a.In, (64<<10)+1))
	}
	if err != nil {
		return a.fail("invalid_input", "cannot read authorization code", 2)
	}
	if len(data) > 64<<10 {
		return a.fail("invalid_input", "authorization code exceeds 64 KiB", 2)
	}
	b, err := parseBundle(string(data))
	if err != nil {
		return a.fail("invalid_input", err.Error(), 2)
	}
	if o.env != "" && o.env != b.EnvironmentID {
		return a.fail("invalid_input", "--env does not match authorization code", 2)
	}
	p := Profile{EnvironmentID: b.EnvironmentID, EnvironmentName: b.EnvironmentName, PublicBaseURL: b.PublicBaseURL, CACert: o.caCert, InsecureTLS: o.insecureTLS}
	entry, _ := baseURL(p.PublicBaseURL)
	if entry.Scheme == "http" && (o.insecureTLS || o.caCert != "") {
		return a.fail("invalid_input", "TLS options apply only to HTTPS environments", 2)
	}
	if err := a.Store.checkBinding(p, o.rebind); err != nil {
		return a.fail("configuration_error", err.Error(), 1)
	}
	if p.InsecureTLS {
		fmt.Fprintln(a.Err, "Certificate and hostname verification explicitly disabled for this environment; HTTPS remains enabled.")
	}
	fmt.Fprintf(a.Err, "Exchanging one-time authorization code over %s...\n", strings.ToUpper(entry.Scheme))
	session, failure, message := a.exchange(string(data), p, "", o.rebind)
	if failure != "" {
		return a.fail(failure, message, 1)
	}
	return a.print(map[string]any{"status": "ok", "summary": "Environment imported. Run discover with explicit --env.", "session": session})
}

func (a *App) session(command string, p Profile) int {
	method := http.MethodGet
	if command == "auth logout" {
		method = http.MethodDelete
	}
	var result map[string]any
	var status int
	var err error
	if p.AccessToken != "" {
		result, status, err = a.request(p, method, "api/cli/session", nil)
	} else {
		err = errors.New("no session to revoke")
	}
	if command == "auth logout" {
		confirmed := err == nil && status >= 200 && status < 300
		if confirmed {
			err = validateRevocation(result, p)
			confirmed = err == nil
		}
		// The pairing goes with the session: logging out is this device
		// leaving, not only this hour's session ending.
		forgotten := a.forget(p)
		confirmed = (confirmed || p.AccessToken == "") && forgotten
		cleared, clearErr := a.Store.clear(p)
		if clearErr != nil {
			return a.fail("configuration_error", "local session cleanup failed; remote revocation may already have succeeded", 1)
		}
		exit := 0
		summary := "Remote revocation confirmed; matching local credentials cleared."
		if !confirmed {
			exit = 1
			summary = "Matching local credentials cleared; remote revocation is unconfirmed."
		}
		if !cleared {
			summary += " A newer or already removed local session was left unchanged."
		}
		state := "ok"
		if !confirmed {
			state = "error"
		}
		a.print(map[string]any{"status": state, "summary": summary, "environment_id": p.EnvironmentID, "remote_revocation_confirmed": confirmed, "local_credentials_cleared": cleared})
		return exit
	}
	if err != nil {
		return a.fail("request_failed", err.Error(), 1)
	}
	if status < 200 || status >= 300 {
		return a.emitOrLapsed(p, result, []string{p.AccessToken}, status)
	}
	expiry, err := validateStatus(result, p)
	if err != nil {
		return a.fail("protocol_error", err.Error(), 1)
	}
	if err := a.Store.updateExpiry(p, expiry); err != nil {
		fmt.Fprintln(a.Err, "Warning: server response received, but local expiry hint could not be saved.")
	}
	return a.print(map[string]any{"status": "ok", "session": redact(result, []string{p.AccessToken})})
}

func (a *App) channel(mode, operation string, params map[string]any, p Profile) int {
	revision := ""
	if mode == "invoke" {
		fmt.Fprintln(a.Err, "Reading the current operation contract...")
		m, status, err := a.channelRequest("describe", operation, nil, "", p)
		if err != nil {
			return a.fail("protocol_error", err.Error(), 1)
		}
		if status < 200 || status >= 300 || stringField(m, "status") != "ok" {
			if code, reported := a.channelFailure(m, status); reported {
				return code
			}
			return a.emitOrLapsed(p, m, []string{p.AccessToken}, status)
		}
		revision = stringField(objectField(m, "meta"), "catalog_revision")
	}
	fmt.Fprintln(a.Err, "Calling the OB channel...")
	m, status, err := a.channelRequest(mode, operation, params, revision, p)
	if err != nil {
		return a.fail("request_failed", err.Error(), 1)
	}
	// A session the server ended before its local expiry is renewed once from
	// the pairing; an unpaired one is reported as it is.
	if status == http.StatusUnauthorized && p.RefreshToken != "" {
		if renewed, renewErr := a.ensureSession(p.EnvironmentID, true); renewErr == nil {
			p = renewed
			m, status, err = a.channelRequest(mode, operation, params, revision, p)
			if err != nil {
				return a.fail("request_failed", err.Error(), 1)
			}
		}
	}
	if code, reported := a.channelFailure(m, status); reported {
		return code
	}
	return a.emitOrLapsed(p, m, []string{p.AccessToken, p.RefreshToken}, status)
}

func (a *App) channelRequest(mode, operation string, params map[string]any, revision string, p Profile) (map[string]any, int, error) {
	body := map[string]any{"channel_version": channelVersion, "mode": mode}
	if operation != "" {
		body["operation"] = operation
	}
	if mode == "invoke" {
		body["params"] = params
		body["expected_catalog_revision"] = revision
		body["renew_if_due"] = true
	}
	m, status, err := a.request(p, http.MethodPost, "api/cli/channel", body)
	if err != nil {
		return m, status, err
	}
	if status < 200 || status >= 300 {
		return m, status, nil
	}
	if err := validateChannel(m, p); err != nil {
		return nil, status, protocolError(err)
	}
	s := objectField(objectField(m, "meta"), "session")
	if err := a.Store.updateExpiry(p, stringField(s, "expires_at")); err != nil {
		fmt.Fprintln(a.Err, "Warning: server response received, but local expiry hint could not be saved.")
	}
	return m, status, nil
}
