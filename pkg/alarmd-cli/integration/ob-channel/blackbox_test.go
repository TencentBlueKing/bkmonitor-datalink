// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package blackbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obevidence"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const environment = "blackbox-env"

type harness struct {
	t                            *testing.T
	cli, dir, profileDir, caFile string
	transportMode, publicBaseURL string
	redis                        *redis.Client
	server                       *httptest.Server
	manager                      *cliauth.Manager
	secrets                      []string
	steps                        []step
	requestMu                    sync.Mutex
	requests                     []string
}
type step struct {
	Name       string   `json:"name"`
	Command    []string `json:"command"`
	ExitCode   int      `json:"exit_code"`
	OutputFile string   `json:"output_file"`
	ResultFile string   `json:"result_file,omitempty"`
}

func TestActualCLIOverDeploymentTransportsAndRedis(t *testing.T) {
	for _, mode := range []string{"private_ca", "insecure_tls", "http"} {
		t.Run(mode, func(t *testing.T) { runBlackbox(t, mode) })
	}
}

func runBlackbox(t *testing.T, mode string) {
	h := newHarness(t, mode)
	defer h.report()
	grant := h.grant()
	login := []string{"auth", "login"}
	if mode != "http" {
		beforeTLS := h.requestCount()
		h.run("login-untrusted-ca", 1, grant+"\n", login...)
		if h.requestCount() != beforeTLS {
			t.Fatal("untrusted TLS sent an HTTP credential exchange")
		}
		if mode == "private_ca" {
			login = append(login, "--ca-cert", h.caFile)
		} else {
			login = append(login, "--insecure-tls")
		}
	}
	h.run("login", 0, grant+"\n", login...)
	token := h.profileToken()
	h.secrets = append(h.secrets, token)
	discovery := h.run("discover", 0, "", "discover", "--env", environment)
	meta, _ := discovery["meta"].(map[string]any)
	transport, _ := meta["client_transport"].(map[string]any)
	scheme, verification := "https", "private_ca"
	if mode == "http" {
		scheme, verification = "http", "not_applicable"
	} else if mode == "insecure_tls" {
		verification = "disabled_explicitly"
	}
	if transport["scheme"] != scheme || transport["encrypted"] != (scheme == "https") || transport["tls_verification"] != verification {
		t.Fatal("evidence receipt did not identify the actual deployment transport")
	}
	for _, operation := range []string{"strategy.config", "store.inspect", "runtime.get", "fleet.get"} {
		if !containsJSON(discovery, operation) {
			t.Fatalf("discover omitted operation %s", operation)
		}
	}
	described := h.run("describe", 0, "", "describe", "strategy.config", "--env", environment)
	if !containsJSON(described, "input_schema") || !containsJSON(described, "output_schema") {
		t.Fatal("describe omitted schemas")
	}
	before := h.requestCount()
	source := h.run("source-threshold", 0, "", "invoke", "strategy.config", "--env", environment, "--input", `{"view":"source","strategy_id":"7"}`)
	if !containsJSON(source, `"threshold":80`) {
		t.Fatal("source projection lost the concrete threshold")
	}
	if !containsJSON(source, `"omitted"`) {
		t.Fatal("source projection omitted its omissions")
	}
	if h.requestCount()-before != 2 {
		t.Fatal("invoke did not perform exactly describe then invoke")
	}
	missing := h.run("redis-missing", 0, "", "invoke", "store.inspect", "--env", environment, "--input", `{"family":"source_strategy","strategy_id":"8"}`)
	if !containsJSON(missing, `"status":"missing"`) || !containsJSON(missing, `"complete":true`) || !containsJSON(missing, `"ttl_ms":-2`) {
		t.Fatal("confirmed missing Redis evidence was not explicit and complete")
	}
	wrongType := h.run("redis-wrong-type", 3, "", "invoke", "store.inspect", "--env", environment, "--input", `{"family":"source_strategy","strategy_id":"9"}`)
	if !containsJSON(wrongType, `"status":"wrong_type"`) || !containsJSON(wrongType, `"complete":false`) {
		t.Fatal("wrong Redis type did not produce partial evidence")
	}
	runtime := h.run("runtime-fixture", 0, "", "invoke", "runtime.get", "--env", environment)
	if !containsJSON(runtime, `"scope":"answering_replica"`) || !containsJSON(runtime, `"gomaxprocs":2`) {
		t.Fatal("runtime fixture did not cross the real channel")
	}
	fleet := h.run("native-fleet-degraded", 0, "", "invoke", "fleet.get", "--env", environment)
	if !containsJSON(fleet, "degraded") || textField(fleet, "status") != "ok" {
		t.Fatal("business degraded state was confused with evidence failure")
	}
	strategy := h.run("native-strategy", 0, "", "invoke", "strategy.get", "--env", environment, "--input", `{"strategy_id":"7"}`)
	if !containsJSON(strategy, `"operation":"strategy.config"`) || !containsJSON(strategy, `"operation":"object.get"`) {
		t.Fatal("native strategy omitted executable next calls")
	}
	object := h.run("native-object-partial", 3, "", "invoke", "object.get", "--env", environment, "--input", `{"query_group":"fixture-qg","records":20}`)
	if !containsJSON(object, "records_status") || textField(object, "status") != "partial" {
		t.Fatal("native incomplete evidence did not remain partial")
	}
	h.run("status", 0, "", "auth", "status", "--env", environment)
	h.run("logout", 0, "", "auth", "logout", "--env", environment)
	if _, err := h.manager.Authenticate(context.Background(), token); cliauth.ErrorCode(err) != "auth_expired_or_revoked" {
		t.Fatal("logout did not revoke the real Redis session")
	}
	h.run("after-logout", 1, "", "discover", "--env", environment)
	h.assertNoSecrets()
}

func newHarness(t *testing.T, mode string) *harness {
	t.Helper()
	cli := os.Getenv("BLACKBOX_CLI_BIN")
	if cli == "" {
		t.Fatal("run via run.sh or set BLACKBOX_CLI_BIN to the actual built CLI")
	}
	dir := os.Getenv("BLACKBOX_RESULT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	runDir, err := os.MkdirTemp(dirParent(t, dir), "run-")
	if err != nil {
		t.Fatal("cannot create output directory")
	}
	h := &harness{t: t, cli: cli, dir: runDir, profileDir: filepath.Join(runDir, "profile"), transportMode: mode}
	for _, path := range []string{h.profileDir, filepath.Join(h.dir, "outputs")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal("cannot create private fixture path")
		}
	}
	h.redis = startRedis(t)
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal("cannot generate fixture admin credential")
	}
	adminKey := base64.RawURLEncoding.EncodeToString(key[:])
	h.secrets = []string{adminKey, "fixture-source-password", "fixture-source-header", "fixture-extension-secret"}
	seed := map[string]any{
		"id": 7, "bk_biz_id": 2, "bk_tenant_id": "fixture-tenant", "name": strings.Repeat("fixture-threshold-", 2200),
		"password": h.secrets[1], "unknown_extension": map[string]string{"safe_looking": h.secrets[3]},
		"items":   []any{map[string]any{"id": 1, "query_configs": []any{map[string]any{"metric_field": "cpu", "agg_interval": 60, "headers": map[string]string{"Authorization": h.secrets[2]}}}, "algorithms": []any{map[string]any{"level": 1, "type": "Threshold", "config": []any{[]any{map[string]any{"method": "gte", "threshold": 80}}}}}}},
		"detects": []any{map[string]any{"level": 1, "trigger_config": map[string]int{"count": 2, "check_window": 3}}},
	}
	seedBytes, _ := json.Marshal(seed)
	if err := h.redis.Set(context.Background(), "fixture.strategy_7", seedBytes, 5*time.Minute).Err(); err != nil {
		t.Fatal("cannot seed fixture Redis")
	}
	if err := h.redis.HSet(context.Background(), "fixture.strategy_9", "unexpected", "hash").Err(); err != nil {
		t.Fatal("cannot seed wrong-type fixture")
	}
	var channel *obchannel.Channel
	var manager *cliauth.Manager
	h.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/alarmd")

		h.requestMu.Lock()
		h.requests = append(h.requests, r.Method+" "+r.URL.Path)
		h.requestMu.Unlock()
		if r.URL.Path == "/api/cli/channel" {
			channel.ServeHTTP(w, r)
			return
		}
		manager.Handler().ServeHTTP(w, r)
	}))
	if mode == "http" {
		h.server.Start()
	} else {
		h.server.StartTLS()
		h.caFile = filepath.Join(runDir, "fixture-ca.pem")
		if err := os.WriteFile(h.caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: h.server.Certificate().Raw}), 0600); err != nil {
			t.Fatal("cannot save fixture CA")
		}
	}
	t.Cleanup(h.server.Close)
	h.publicBaseURL = h.server.URL + "/alarmd/"
	if mode == "insecure_tls" {
		if h.server.Certificate().VerifyHostname("localhost") == nil {
			t.Fatal("fixture hostname unexpectedly trusted")
		}
		h.publicBaseURL = strings.Replace(h.publicBaseURL, "127.0.0.1", "localhost", 1)
	}
	manager, err = cliauth.New(cliauth.Options{Redis: h.redis, Prefix: "fixture-auth", EnvironmentID: environment, EnvironmentName: "Blackbox fixture", PublicBaseURL: h.publicBaseURL, AdminKey: adminKey})
	if err != nil {
		t.Fatal("cannot initialize real auth manager")
	}
	h.manager = manager
	service := obevidence.New(obevidence.Options{SourceStrategy: obevidence.RedisBinding{Client: h.redis, Location: obevidence.Location{Role: "strategy_cache", Address: h.redis.Options().Addr, Mode: "single", DB: 0, Prefix: "fixture"}}})
	operations := append(obchannel.NativeOperations(http.HandlerFunc(nativeFixture)), obchannel.StoreOperations(service)...)
	operations = append(operations, obchannel.Operation{ID: "runtime.get", Summary: "Fixture answering-process runtime facts; transport acceptance only.", Fields: map[string]obchannel.Field{}, OutputSchema: obchannel.SchemaOf(observability.RuntimeConfigFacts{}), Run: func(context.Context, obchannel.Params) obchannel.Outcome {
		return obchannel.Outcome{Complete: true, Value: map[string]any{"scope": "answering_replica", "config": observability.RuntimeConfigFacts{Profile: "fixture", Source: "blackbox-fixture", GOMAXPROCS: 2, Digest: "fixture-digest"}}}
	}})
	channel, err = obchannel.New(obchannel.Options{Auth: manager, EnvironmentID: environment, Replica: "fixture-replica", Build: "blackbox-fixture", Concurrency: 1, Operations: operations})
	if err != nil {
		t.Fatal("cannot initialize real OB channel")
	}
	h.checkGrantAuthorization()
	return h
}

func nativeFixture(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var result any
	switch r.URL.Path {
	case "/api/health":
		result = map[string]any{"complete": true, "health": "degraded", "line": "Fixture business health is degraded; evidence is complete.", "gaps": []any{}}
	case "/api/strategies/7":
		result = map[string]any{"complete": true, "strategy_id": "7", "line": "Fixture accepted strategy.", "plans": []any{map[string]any{"query_group": "fixture-qg", "existence": "present"}}}
	case "/api/objects/fixture-qg":
		result = map[string]any{"complete": true, "query_group": "fixture-qg", "records_status": "unavailable", "line": "Fixture retained records are unavailable."}
	default:
		w.WriteHeader(404)
		result = map[string]any{"error": "fixture route not defined"}
	}
	_ = json.NewEncoder(w).Encode(result)
}

// The entrance only strips the deployment URL prefix. It never authenticates
// users or injects credentials; alarmd must enforce the deployment admin key.
func (h *harness) checkGrantAuthorization() {
	h.t.Helper()
	u, _ := url.Parse(h.publicBaseURL)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, credential := range []string{"", "Bearer incorrect-admin-key"} {
			r, _ := http.NewRequest(method, h.server.URL+"/alarmd/api/cli/auth/grants", strings.NewReader(`{"confirm":true}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", u.Scheme+"://"+u.Host)
			r.Header.Set("Authorization", credential)
			response, err := h.server.Client().Do(r)
			if err != nil {
				h.t.Fatal("admin-key rejection request failed")
			}
			response.Body.Close()
			if response.StatusCode != http.StatusUnauthorized && response.StatusCode != http.StatusForbidden {
				h.t.Fatalf("%s grant route accepted missing or incorrect admin key: %d", method, response.StatusCode)
			}
		}
	}
	r, _ := http.NewRequest(http.MethodGet, h.server.URL+"/alarmd/api/cli/auth/grants", nil)
	r.Header.Set("Origin", u.Scheme+"://"+u.Host)
	r.Header.Set("Authorization", "Bearer "+h.secrets[0])
	response, err := h.server.Client().Do(r)
	if err != nil {
		h.t.Fatal("admin-key preview request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		h.t.Fatalf("direct admin-key preview rejected: %d", response.StatusCode)
	}
}

func (h *harness) grant() string {
	h.t.Helper()
	endpoint := h.server.URL + "/alarmd/api/cli/auth/grants"
	r, _ := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(`{"confirm":true}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+h.secrets[0])
	u, _ := url.Parse(h.publicBaseURL)
	r.Header.Set("Origin", u.Scheme+"://"+u.Host)
	response, err := h.server.Client().Do(r)
	if err != nil {
		h.t.Fatal("fixture grant request failed")
	}
	defer response.Body.Close()
	var body struct {
		AuthorizationCode string `json:"authorization_code"`
	}
	if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&body) != nil || body.AuthorizationCode == "" {
		h.t.Fatal("real manager did not issue grant")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(body.AuthorizationCode, "alarmd-login-v1."))
	if err != nil {
		h.t.Fatal("grant package cannot be decoded")
	}
	var secret struct {
		GrantSecret string `json:"grant_secret"`
	}
	if json.Unmarshal(raw, &secret) != nil {
		h.t.Fatal("grant package is invalid")
	}
	h.secrets = append(h.secrets, body.AuthorizationCode, secret.GrantSecret)
	return body.AuthorizationCode
}

func (h *harness) profileToken() string {
	raw, err := os.ReadFile(filepath.Join(h.profileDir, "profiles.json"))
	if err != nil {
		h.t.Fatal("login did not create profile")
	}
	var profiles struct {
		Profiles map[string]struct {
			AccessToken string `json:"access_token"`
		} `json:"profiles"`
	}
	if json.Unmarshal(raw, &profiles) != nil || profiles.Profiles[environment].AccessToken == "" {
		h.t.Fatal("profile did not contain test session")
	}
	return profiles.Profiles[environment].AccessToken
}

func (h *harness) run(name string, want int, stdin string, args ...string) map[string]any {
	h.t.Helper()
	command := exec.Command(h.cli, args...)
	command.Env = append(os.Environ(), "ALARMD_CLI_CONFIG_DIR="+h.profileDir, "SSL_CERT_FILE=", "HTTPS_PROXY=", "HTTP_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost")
	command.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			h.t.Fatalf("%s: CLI process could not start", name)
		}
	}
	for _, secret := range h.secrets {
		if secret != "" && (bytes.Contains(stdout.Bytes(), []byte(secret)) || bytes.Contains(stderr.Bytes(), []byte(secret))) {
			h.t.Fatalf("%s: credential leaked into process output", name)
		}
	}
	outputPath := filepath.Join(h.dir, "outputs", name+".json")
	if err := os.WriteFile(outputPath, stdout.Bytes(), 0600); err != nil {
		h.t.Fatal("cannot save safe CLI output")
	}
	if err := os.WriteFile(filepath.Join(h.dir, "outputs", name+".stderr.txt"), stderr.Bytes(), 0600); err != nil {
		h.t.Fatal("cannot save CLI progress")
	}
	var result map[string]any
	if json.Unmarshal(stdout.Bytes(), &result) != nil {
		h.t.Fatalf("%s: CLI stdout was not JSON (exit %d)", name, code)
	}
	resultFile := ""
	if meta, ok := result["meta"].(map[string]any); ok {
		resultFile, _ = meta["result_file"].(string)
	}
	h.steps = append(h.steps, step{Name: name, Command: args, ExitCode: code, OutputFile: outputPath, ResultFile: resultFile})
	if code != want {
		h.t.Fatalf("%s: exit %d, want %d; safe output saved in %s", name, code, want, outputPath)
	}
	if len(stdout.Bytes()) > 20<<10 {
		h.t.Fatalf("%s: stdout exceeded 20 KiB", name)
	}
	if name == "source-threshold" && result["result_omitted"] != true {
		h.t.Fatal("large source response did not direct the agent to result_file")
	}
	if resultFile != "" {
		if !filepath.IsAbs(resultFile) || !strings.HasPrefix(resultFile, h.profileDir+string(filepath.Separator)) {
			h.t.Fatalf("%s: result_file escaped the isolated profile", name)
		}
		raw, err := os.ReadFile(resultFile)
		if err != nil {
			h.t.Fatalf("%s: result_file missing", name)
		}
		for _, secret := range h.secrets {
			if secret != "" && bytes.Contains(raw, []byte(secret)) {
				h.t.Fatalf("%s: credential leaked into result_file", name)
			}
		}
		if json.Unmarshal(raw, &result) != nil {
			h.t.Fatalf("%s: result_file invalid JSON", name)
		}
		info, err := os.Stat(resultFile)
		if err != nil || info.Mode().Perm() != 0600 {
			h.t.Fatalf("%s: evidence file is not mode 0600", name)
		}
	}
	h.t.Logf("%s: exit=%d result_file=%t", name, code, resultFile != "")
	return result
}

func (h *harness) requestCount() int {
	h.requestMu.Lock()
	defer h.requestMu.Unlock()
	return len(h.requests)
}
func containsJSON(value any, want string) bool {
	raw, _ := json.Marshal(value)
	return strings.Contains(string(raw), want)
}
func textField(value map[string]any, key string) string { text, _ := value[key].(string); return text }
func dirParent(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal("cannot create results root")
	}
	return path
}

func (h *harness) assertNoSecrets() {
	h.t.Helper()
	profile, err := os.ReadFile(filepath.Join(h.profileDir, "profiles.json"))
	if err != nil || bytes.Contains(profile, []byte(h.secrets[0])) {
		h.t.Fatal("deployment admin key was persisted in CLI profile")
	}
	for _, dir := range []string{filepath.Join(h.dir, "outputs"), filepath.Join(h.profileDir, "results")} {
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, secret := range h.secrets {
				if secret != "" && bytes.Contains(raw, []byte(secret)) {
					return fmt.Errorf("credential found in output")
				}
			}
			return nil
		})
		if err != nil {
			h.t.Fatal("credential-free artifact check failed")
		}
	}
}

func (h *harness) report() {
	status := "passed"
	if h.t.Failed() {
		status = "failed"
	}
	data, _ := json.MarshalIndent(map[string]any{"status": status, "steps": h.steps, "transport_mode": h.transportMode, "server_packages": []string{"cliauth", "obchannel", "obevidence"}, "fixture_boundary": "Native Fleet and runtime payloads are constructed fixtures, not live alarmd business acceptance.", "cli_sha256": fileDigest(h.cli), "server_source": sourceState(os.Getenv("BLACKBOX_SERVER_REPO")), "cli_source": sourceState(os.Getenv("BLACKBOX_CLI_REPO"))}, "", "  ")
	_ = os.WriteFile(filepath.Join(h.dir, "report.json"), append(data, '\n'), 0600)
	h.t.Logf("blackbox report: %s", filepath.Join(h.dir, "report.json"))
}
func fileDigest(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "unavailable"
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func gitHead(path string) string {
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = path
	out, err := command.Output()
	if err != nil {
		return "unavailable"
	}
	return strings.TrimSpace(string(out))
}

func sourceState(path string) map[string]any {
	command := exec.Command("git", "status", "--porcelain")
	command.Dir = path
	status, err := command.Output()
	return map[string]any{"head": gitHead(path), "dirty": err != nil || len(status) > 0}
}

func startRedis(t *testing.T) *redis.Client {
	t.Helper()
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Fatal("redis-server is required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("cannot allocate local Redis port")
	}
	address := listener.Addr().String()
	_ = listener.Close()
	_, port, _ := net.SplitHostPort(address)
	command := exec.Command(executable, "--bind", "127.0.0.1", "--port", port, "--save", "", "--appendonly", "no", "--dir", t.TempDir(), "--loglevel", "warning")
	if err := command.Start(); err != nil {
		t.Fatal("cannot start fixture Redis")
	}
	client := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close(); _ = command.Process.Kill(); _ = command.Wait() })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if client.Ping(context.Background()).Err() == nil {
			return client
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fixture Redis did not become ready")
	return nil
}
