// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/publicsurface"
	httpservice "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/service/http"
)

const plantedAddress = "redis.internal.example:6379"

// The routes the public surface stops serving when restricted. Each is
// answered by the stand-in API with its own path and the planted address, so
// a response that came from the API says so.
var restrictedRoutes = []string{"/api/objects", "/api/objects/some-object", "/api/strategies", "/api/strategies/1",
	"/api/diagnose", "/api/series", "/api/anything-added-later"}

// standInAPI answers every route as the whole API would: /api/health with a
// health response naming a dependency, the rest with the path they were
// asked on.
func standInAPI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/health" {
			_ = json.NewEncoder(w).Encode(fleet.HealthResponse{Health: fleet.HealthHealthy, Covered: 3, Determined: 3,
				Dependencies: []fleet.Endpoint{{Role: "runtime", Kind: "redis", Address: plantedAddress}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"answered": r.URL.Path, "dependency": plantedAddress})
	})
}

// windowsStandIn marks that the public windows handler answered.
var windowsStandIn = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(299) })

func publicCall(handler http.Handler, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func restrictedCLIConfig(t *testing.T) (config.Config, *redis.Client) {
	t.Helper()
	address, client := startPhaseTwoRedis(t)
	cfg := config.Default()
	cfg.Redis.Address = address
	cfg.PhaseTwo.Worker.ID = "test-worker"
	cfg.CLI = config.CLIConfig{Enabled: true, EnvironmentID: "test", EnvironmentName: "Test",
		PublicBaseURL: "https://ob.example/alarmd/", AdminKey: strings.Repeat("k", 40)}
	return cfg, client
}

func restrictedCLI(t *testing.T) (http.Handler, config.Config, *redis.Client) {
	t.Helper()
	cfg, client := restrictedCLIConfig(t)
	h, closeCLI, restricted := buildPhaseTwoCLI(cfg, standInAPI(), nil, nil, nil, func() *observability.RuntimeConfigFacts { return nil },
		cliControlBinding{Incarnation: "test-process", PublicWindows: windowsStandIn})
	t.Cleanup(func() { _ = closeCLI() })
	if !restricted {
		t.Fatal("a CLI that came up with a key did not restrict the surface")
	}
	return h, cfg, client
}

// With the CLI up and a key, the public surface serves the summary without
// the dependency address, the windows through their public handler, the CLI
// endpoints, and refuses every other route as moved -- naming the login page
// as a link that resolves from where it was refused. A CLI session opened
// through those endpoints reads the same routes whole.
func TestAKeyRestrictsThePublicSurfaceAndTheCLISessionStillReadsIt(t *testing.T) {
	h, cfg, _ := restrictedCLI(t)
	for _, path := range restrictedRoutes {
		got := publicCall(h, http.MethodGet, path)
		var refusal struct {
			Error struct {
				Code      string `json:"code"`
				LoginPage string `json:"login_page"`
				LoginHref string `json:"login_href"`
			} `json:"error"`
		}
		_ = json.Unmarshal(got.Body.Bytes(), &refusal)
		wantHref := strings.Repeat("../", strings.Count(path, "/")-1) + "cli"
		if got.Code != http.StatusForbidden || refusal.Error.Code != publicsurface.RestrictedCode || refusal.Error.LoginPage != "cli" ||
			refusal.Error.LoginHref != wantHref || strings.Contains(got.Body.String(), plantedAddress) || strings.Contains(got.Body.String(), "ob.example") {
			t.Errorf("public %s = %d %.200s, want the restricted refusal with the login page at %s", path, got.Code, got.Body.String(), wantHref)
		}
	}
	summary := publicCall(h, http.MethodGet, "/api/health")
	var health fleet.PublicHealthResponse
	if summary.Code != http.StatusOK || json.Unmarshal(summary.Body.Bytes(), &health) != nil || !health.Restricted ||
		health.Health != fleet.HealthHealthy || health.Covered != 3 || strings.Contains(summary.Body.String(), plantedAddress) {
		t.Fatalf("public /api/health = %d %s, want the summary without the address", summary.Code, summary.Body.String())
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if got := publicCall(h, method, "/api/windows"); got.Code != 299 {
			t.Fatalf("public %s /api/windows = %d, want the public windows handler", method, got.Code)
		}
	}
	session := openCLISession(t, h, cfg)
	assertCLIReadsTheWholeAPI(t, h, session.AccessToken)
}

// The whole way to a credential is public on a restricted surface, taken
// with no session and no credential of any kind: issuing, exchange, pairing,
// renewal, forgetting, revocation, the session check and the channel all
// answer as themselves -- an authorization answer, never the restricted
// refusal. (The login page itself is the listener's, tested with it.)
func TestTheWayToACredentialStaysPublicOnARestrictedSurface(t *testing.T) {
	h, _, _ := restrictedCLI(t)
	paths := append(append([]string{}, cliauth.Paths...), "/api/cli/channel")
	for _, want := range []string{"/api/cli/auth/grants", "/api/cli/auth/exchange", "/api/cli/auth/pair", "/api/cli/auth/refresh",
		"/api/cli/auth/forget", "/api/cli/auth/revoke-all", "/api/cli/session", "/api/cli/channel"} {
		found := false
		for _, path := range paths {
			found = found || path == want
		}
		if !found {
			t.Fatalf("%s is not among the CLI routes; the list below would not cover it", want)
		}
	}
	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			got := publicCall(h, method, path)
			if got.Code == http.StatusForbidden && strings.Contains(got.Body.String(), publicsurface.RestrictedCode) || got.Code == http.StatusNotFound {
				t.Errorf("%s %s = %d %.120s, want the authorization route's own answer", method, path, got.Code, got.Body.String())
			}
			if !strings.HasPrefix(got.Header().Get("Content-Type"), "application/json") {
				t.Errorf("%s %s answered %q, not an authorization answer", method, path, got.Header().Get("Content-Type"))
			}
		}
	}
}

// A credential whose renewal expired, or was revoked, is replaced with the
// administrator key alone: no old session, no old renewal credential. The
// new session reads the restricted routes. Expiry is what the store's TTL
// does -- the session and pairing records go -- and revocation is the
// administrator's revoke-all.
func TestAnExpiredOrRevokedCredentialIsReplacedWithTheKeyAlone(t *testing.T) {
	for _, lapse := range []string{"expired", "revoked"} {
		t.Run(lapse, func(t *testing.T) {
			h, cfg, client := restrictedCLI(t)
			old := openCLISession(t, h, cfg)
			if old.RefreshToken == "" {
				t.Fatal("the login gave no renewal credential")
			}
			renewed := refreshCLISession(h, cfg, old.RefreshToken)
			if renewed.Code != http.StatusOK {
				t.Fatalf("renewal before the lapse = %d %s", renewed.Code, renewed.Body.String())
			}
			var next loginAnswer
			_ = json.Unmarshal(renewed.Body.Bytes(), &next)
			switch lapse {
			case "expired":
				ctx := context.Background()
				for _, pattern := range []string{"*session:*", "*pairing:*"} {
					keys, err := client.Keys(ctx, pattern).Result()
					if err != nil {
						t.Fatal(err)
					}
					if len(keys) > 0 {
						if err := client.Del(ctx, keys...).Err(); err != nil {
							t.Fatal(err)
						}
					}
				}
			case "revoked":
				revoke := httptest.NewRequest(http.MethodPost, "/api/cli/auth/revoke-all", strings.NewReader(`{"confirm":true}`))
				revoke.Header.Set("Authorization", "Bearer "+cfg.CLI.AdminKey)
				revoke.Header.Set("Origin", "https://ob.example")
				revoke.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, revoke)
				if w.Code != http.StatusOK {
					t.Fatalf("revoke-all = %d %s", w.Code, w.Body.String())
				}
			}
			if got := refreshCLISession(h, cfg, next.RefreshToken); got.Code != http.StatusUnauthorized || !strings.Contains(got.Body.String(), "renewal_expired_or_revoked") {
				t.Fatalf("renewal after the lapse = %d %s, want renewal_expired_or_revoked", got.Code, got.Body.String())
			}
			if out := cliCall(t, h, next.AccessToken, map[string]any{"channel_version": obchannel.Version, "mode": "discover"}); out.Status == "ok" {
				t.Fatal("the lapsed session still reads")
			}
			fresh := openCLISession(t, h, cfg)
			assertCLIReadsTheWholeAPI(t, h, fresh.AccessToken)
		})
	}
}

type loginAnswer struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func assertCLIReadsTheWholeAPI(t *testing.T, h http.Handler, token string) {
	t.Helper()
	revision := cliDiscover(t, h, token)
	for op, want := range map[string]string{"fleet.get": plantedAddress, "strategy.list": "/api/strategies"} {
		out := cliInvoke(t, h, token, revision, op)
		raw, _ := json.Marshal(out.Result)
		if out.Status != "ok" || !strings.Contains(string(raw), want) {
			t.Errorf("CLI %s = %s %s, want the whole route (%s)", op, out.Status, raw, want)
		}
	}
}

func refreshCLISession(h http.Handler, cfg config.Config, refreshToken string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"environment_id": cfg.CLI.EnvironmentID, "refresh_token": refreshToken})
	r := httptest.NewRequest(http.MethodPost, "/api/cli/auth/refresh", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// openCLISession goes the way an operator does: the administrator key mints
// a grant, the grant is exchanged for a session and a renewal credential.
func openCLISession(t *testing.T, h http.Handler, cfg config.Config) loginAnswer {
	t.Helper()
	grant := httptest.NewRequest(http.MethodPost, "/api/cli/auth/grants", strings.NewReader(`{"confirm":true}`))
	grant.Header.Set("Authorization", "Bearer "+cfg.CLI.AdminKey)
	grant.Header.Set("Origin", "https://ob.example")
	grant.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, grant)
	var issued struct {
		AuthorizationCode string `json:"authorization_code"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &issued) != nil {
		t.Fatalf("grant = %d %s", w.Code, w.Body.String())
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(issued.AuthorizationCode, "alarmd-login-v1."))
	var pkg struct {
		EnvironmentID string `json:"environment_id"`
		GrantSecret   string `json:"grant_secret"`
	}
	if err != nil || json.Unmarshal(raw, &pkg) != nil {
		t.Fatalf("authorization code: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"environment_id": pkg.EnvironmentID, "grant_secret": pkg.GrantSecret})
	exchange := httptest.NewRequest(http.MethodPost, "/api/cli/auth/exchange", bytes.NewReader(body))
	exchange.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, exchange)
	var session loginAnswer
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &session) != nil || session.AccessToken == "" {
		t.Fatalf("exchange = %d %s", w.Code, w.Body.String())
	}
	return session
}

func cliCall(t *testing.T, h http.Handler, token string, input map[string]any) obchannel.Response {
	t.Helper()
	raw, _ := json.Marshal(input)
	r := httptest.NewRequest(http.MethodPost, "/api/cli/channel", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out obchannel.Response
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("channel answered %d %s", w.Code, w.Body.String())
	}
	return out
}

func cliDiscover(t *testing.T, h http.Handler, token string) string {
	t.Helper()
	out := cliCall(t, h, token, map[string]any{"channel_version": obchannel.Version, "mode": "discover"})
	if out.Meta.Revision == "" {
		t.Fatalf("discover: %+v", out)
	}
	return out.Meta.Revision
}

func cliInvoke(t *testing.T, h http.Handler, token, revision, op string) obchannel.Response {
	t.Helper()
	return cliCall(t, h, token, map[string]any{"channel_version": obchannel.Version, "mode": "invoke", "operation": op,
		"params": map[string]any{}, "expected_catalog_revision": revision})
}

// Without a key, or with the CLI off -- the configuration a deployment with
// no cli block renders -- the public surface is the API itself, byte for
// byte, on every route but one; the public windows handler is not used. The
// one is the diagnosis's first page, which keeps every byte and gains the
// deployment section, so a deployment read without the CLI still sees its
// Redis and Pod findings.
func TestNoKeyLeavesThePublicSurfaceByteForByte(t *testing.T) {
	enabled := config.Default()
	// A CLI that comes up in full without a key: the case that must not
	// restrict, not one that fails before the question is asked.
	enabled.CLI = config.CLIConfig{Enabled: true, EnvironmentID: "test", EnvironmentName: "Test", PublicBaseURL: "https://ob.example/alarmd/"}
	keyOff := config.Default()
	keyOff.CLI = config.CLIConfig{EnvironmentID: "test", EnvironmentName: "Test", AdminKey: strings.Repeat("k", 40)}
	for name, cfg := range map[string]config.Config{"no cli block": config.Default(), "CLI on without a key": enabled, "key with the CLI off": keyOff} {
		h, closeCLI, restricted := buildPhaseTwoCLI(cfg, standInAPI(), nil, nil, nil, func() *observability.RuntimeConfigFacts { return nil },
			cliControlBinding{Incarnation: "test-process", PublicWindows: windowsStandIn})
		if restricted {
			t.Errorf("%s: restricted", name)
		}
		if name == "CLI on without a key" {
			if got := publicCall(h, http.MethodGet, "/api/cli/auth/grants"); got.Code != http.StatusServiceUnavailable || !strings.Contains(got.Body.String(), "admin_not_configured") {
				t.Fatalf("%s: the CLI did not come up: %d %s", name, got.Code, got.Body.String())
			}
		}
		for _, path := range append(append([]string{}, restrictedRoutes...), "/api/health", "/api/windows") {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				got, want := publicCall(h, method, path), publicCall(standInAPI(), method, path)
				if path == "/api/diagnose" && method == http.MethodGet {
					assertOnlyTheDeploymentSectionAdded(t, name, got, want)
					continue
				}
				if got.Code != want.Code || !bytes.Equal(got.Body.Bytes(), want.Body.Bytes()) {
					t.Errorf("%s: %s %s = %d %.80s, want the API's own %d %.80s", name, method, path, got.Code, got.Body.String(), want.Code, want.Body.String())
				}
			}
		}
		closeCLI()
	}
}

// assertOnlyTheDeploymentSectionAdded holds an unrestricted diagnosis to
// what it may change: the API's page, every byte of it, with one key added.
func assertOnlyTheDeploymentSectionAdded(t *testing.T, name string, got, want *httptest.ResponseRecorder) {
	t.Helper()
	own := bytes.TrimRight(want.Body.Bytes(), " \t\r\n")
	var page, base map[string]json.RawMessage
	if got.Code != want.Code || !bytes.HasPrefix(got.Body.Bytes(), own[:len(own)-1]) ||
		json.Unmarshal(got.Body.Bytes(), &page) != nil || json.Unmarshal(own, &base) != nil ||
		len(page) != len(base)+1 || page["deployment"] == nil {
		t.Errorf("%s: GET /api/diagnose = %d %s, want the API's own %d %s with the deployment section added",
			name, got.Code, got.Body.String(), want.Code, want.Body.String())
	}
}

// A key whose CLI fails to come up leaves the surface open -- restricting it
// would leave no way in -- and says so: the CLI routes answer that the CLI
// is not configured, and the standing names CLI_AUTH_UNAVAILABLE.
func TestAFailedCLILeavesTheSurfaceOpenAndSaysSo(t *testing.T) {
	cfg := config.Default()
	cfg.CLI = config.CLIConfig{Enabled: true, EnvironmentID: "test", EnvironmentName: "Test",
		PublicBaseURL: "not a url", AdminKey: strings.Repeat("k", 40)}
	h, closeCLI, restricted := buildPhaseTwoCLI(cfg, standInAPI(), nil, nil, nil, func() *observability.RuntimeConfigFacts { return nil },
		cliControlBinding{Incarnation: "test-process", PublicWindows: windowsStandIn})
	defer closeCLI()
	if restricted {
		t.Fatal("a CLI that did not come up restricted the surface")
	}
	got, want := publicCall(h, http.MethodGet, "/api/objects"), publicCall(standInAPI(), http.MethodGet, "/api/objects")
	if got.Code != want.Code || !bytes.Equal(got.Body.Bytes(), want.Body.Bytes()) {
		t.Fatalf("public /api/objects = %d %s, want the API unchanged", got.Code, got.Body.String())
	}
	if got := publicCall(h, http.MethodPost, "/api/cli/channel"); got.Code != http.StatusServiceUnavailable || !strings.Contains(got.Body.String(), "cli_not_configured") {
		t.Fatalf("CLI channel = %d %s, want cli_not_configured", got.Code, got.Body.String())
	}
	if standing := publicSurfaceStandingOf(cfg, restricted); !standing.CLIUnavailable || standing.MetricsUnexported {
		t.Fatalf("standing %+v, want the CLI named unavailable", standing)
	}
}

// The standing, over both halves of the switch and the internal listener.
func TestThePublicSurfaceStanding(t *testing.T) {
	key := strings.Repeat("k", 40)
	for _, tc := range []struct {
		name                    string
		enabled                 bool
		key, internal           string
		restricted              bool
		unexported, unavailable bool
	}{
		{name: "no key", enabled: true},
		{name: "key with the CLI off", key: key},
		{name: "restricted with an internal listener", enabled: true, key: key, internal: "0.0.0.0:8081", restricted: true},
		{name: "restricted without one", enabled: true, key: key, restricted: true, unexported: true},
		{name: "CLI failed", enabled: true, key: key, internal: "0.0.0.0:8081", unavailable: true},
		{name: "CLI failed without an internal listener", enabled: true, key: key, unavailable: true},
	} {
		cfg := config.Default()
		cfg.CLI.Enabled, cfg.CLI.AdminKey, cfg.HTTP.InternalListen = tc.enabled, tc.key, tc.internal
		standing := publicSurfaceStandingOf(cfg, tc.restricted)
		if standing.MetricsUnexported != tc.unexported || standing.CLIUnavailable != tc.unavailable {
			t.Errorf("%s: %+v", tc.name, standing)
		}
	}
}

// The production listener starts closed when the configuration asks for a
// restricted surface, and the runtime settles it: open when the CLI did
// not come up, restricted when it did. The internal listener serves
// /metrics throughout.
func TestTheProductionListenerIsSettledByTheRuntime(t *testing.T) {
	for _, requested := range []bool{false, true} {
		runtime, err := defaultPhaseTwoApplicationDependencies().newHTTP(metric.NewRecorder(metric.BuildInfo{}),
			observability.NewHealthTracker(observability.HealthSnapshot{}), httpSurface{Internal: "127.0.0.1:8081", Restricted: requested})
		if err != nil {
			t.Fatal(err)
		}
		server := runtime.(*httpservice.Server)
		want := http.StatusOK
		if requested {
			want = http.StatusForbidden
		}
		if got := publicCall(server.Handler(), http.MethodGet, "/metrics"); got.Code != want {
			t.Errorf("requested=%v before settling: public /metrics = %d, want %d", requested, got.Code, want)
		}
		for _, settled := range []bool{false, true, false} {
			server.SetPublicSurfaceRestricted(settled)
			want := http.StatusOK
			if settled {
				want = http.StatusForbidden
			}
			if got := publicCall(server.Handler(), http.MethodGet, "/metrics"); got.Code != want {
				t.Errorf("settled %v: public /metrics = %d, want %d", settled, got.Code, want)
			}
			if got := publicCall(server.InternalHandler(), http.MethodGet, "/metrics"); got.Code != http.StatusOK {
				t.Errorf("settled %v: internal /metrics = %d", settled, got.Code)
			}
		}
	}
	cfg := config.Default()
	cfg.CLI.Enabled, cfg.CLI.AdminKey = true, strings.Repeat("k", 40)
	if !httpSurfaceOf(cfg).Restricted {
		t.Error("the configuration's request does not reach the listener")
	}
}

// The publisher puts the standing on every snapshot it takes, and the fleet
// reads each half as a named degradation of that replica.
func TestThePublicSurfaceStandingIsPublishedAsDegradations(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(20_000, 0)}
	publisher := fleetPublisher{
		tracker: fleet.NewTracker(nil, "replica-1", clock.now), replica: "replica-1", now: clock.now,
		owned:         func() []execution.QueryGroupIdentity { return nil },
		publicSurface: publicSurfaceStanding{MetricsUnexported: true, CLIUnavailable: true},
	}
	snapshot := publisher.snapshot(context.Background())
	view := fleet.Aggregate(fleet.Expectation{}, []fleet.Snapshot{snapshot}, []string{"replica-1"}, clock.now(), time.Hour)
	named := map[fleet.DegradationKind]bool{}
	for _, degradation := range view.Degradations {
		if degradation.Replica == "replica-1" {
			named[degradation.Kind] = true
		}
	}
	if !named[fleet.DegradationMetricsUnexported] || !named[fleet.DegradationCLIAuthUnavailable] {
		t.Fatalf("degradations %+v, want both named", view.Degradations)
	}
	publisher.publicSurface = publicSurfaceStanding{}
	if snapshot := publisher.snapshot(context.Background()); snapshot.MetricsUnexported || snapshot.CLIUnavailable {
		t.Fatal("a replica with nothing to say said something")
	}
}

// The application hands the listener the bundle's settlement when it
// installs the API, so what the listener serves in public follows whether
// the CLI came up.
func TestTheApplicationSettlesTheListenerFromTheBundle(t *testing.T) {
	for _, restricted := range []bool{false, true} {
		cfg := validGoAccessRuntimeConfig()
		health := newPhaseTwoApplicationHealth()
		control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
		runner := newFakePhaseTwoQueryGroup()
		owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: runner}
		bundle := mustPhaseTwoWorkerBundle(t, cfg, health, control, owner)
		bundle.dependencies.FleetAPI = http.NotFoundHandler()
		bundle.dependencies.PublicSurfaceRestricted = restricted
		listener := &fakeHTTPRuntime{}
		listener.run = func(ctx context.Context, _ string, _ time.Duration) error { <-ctx.Done(); return nil }
		ctx, cancel := context.WithCancel(context.Background())
		dependencies := phaseTwoApplicationDependencies{
			configureCPU: func() (string, error) { return "cpu_quota", nil },
			openBundle: func(context.Context, config.Config, *metric.Recorder, *observability.Logger, *phaseTwoApplicationHealth) (*phaseTwoWorkerBundle, error) {
				return bundle, nil
			},
			newHTTP: func(*metric.Recorder, observability.HealthSource, httpSurface) (httpRuntime, error) {
				return listener, nil
			},
		}
		done := make(chan error, 1)
		go func() {
			done <- runPhaseTwoApplicationWithDependencies(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime), dependencies)
		}()
		waitSignal(t, runner.leaseStarted, "query-group lease maintenance")
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if listener.restricted == nil || *listener.restricted != restricted {
			t.Fatalf("bundle restricted=%v, listener settled %v", restricted, listener.restricted)
		}
	}
}
