// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

const (
	optionalConsulFlagsKey = "optional:uq:data:feature_flag"
	optionalConsulRules    = `{"startup-flag":{"variations":{"enabled":true,"disabled":false},"defaultRule":{"variation":"enabled"}}}`
	builtInDefaultsMessage = "using built-in defaults"
)

// rootProcess is a cold-started root command whose logs are read after it stops.
type rootProcess struct {
	t       *testing.T
	baseURL string
	logPath string
	process *exec.Cmd
	done    chan error
	stopped bool
}

// startRootProcess writes config as the only config file and starts the real root
// command in a helper process. Extra environment entries are appended verbatim.
func startRootProcess(t *testing.T, redis *miniredis.Miniredis, consul map[string]any, storageSource string, env ...string) *rootProcess {
	t.Helper()
	dir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpPort := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	redisPort, err := strconv.Atoi(redis.Port())
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{
		"redis": map[string]any{
			"mode": "standalone", "host": redis.Host(), "port": redisPort,
			"kv_base_path": "optional:uq", "dial_timeout": "1s", "read_timeout": "1s",
		},
		"influxdb": map[string]any{
			"space_router_prefix":            "optional:spaces",
			"space_router_bbolt_path":        filepath.Join(dir, "space.db"),
			"space_router_bbolt_bucket_name": "optional", "is_cache": false,
		},
		"http":   map[string]any{"address": "127.0.0.1", "port": httpPort},
		"jwt":    map[string]any{"enabled": false},
		"trace":  map[string]any{"enable": false},
		"logger": map[string]any{"level": "info", "path": filepath.Join(dir, "uq.log")},
		"bbolt":  map[string]any{"default_path": filepath.Join(dir, "default.db")},
	}
	if consul != nil {
		settings["consul"] = consul
	}
	if storageSource != "" {
		settings["storage"] = map[string]any{"source": storageSource}
	}
	configData, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "startup.json")
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := os.Create(filepath.Join(dir, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })

	p := &rootProcess{
		t:       t,
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", httpPort),
		logPath: filepath.Join(dir, "uq.log"),
		process: exec.Command(os.Args[0], "-test.run=^TestRedisOnlyStartupHelper$"),
		done:    make(chan error, 1),
	}
	p.process.Dir = dir
	for _, entry := range os.Environ() {
		upper := strings.ToUpper(entry)
		if !strings.HasPrefix(upper, "UNIFY-QUERY_") && !strings.HasPrefix(upper, "CONSUL_") &&
			!strings.HasPrefix(entry, "UQ_REDIS_STARTUP_HELPER_CONFIG=") && !strings.HasPrefix(entry, "GOPS_CONFIG_DIR=") {
			p.process.Env = append(p.process.Env, entry)
		}
	}
	p.process.Env = append(p.process.Env, "UQ_REDIS_STARTUP_HELPER_CONFIG="+configPath, "GOPS_CONFIG_DIR="+filepath.Join(dir, "gops"))
	p.process.Env = append(p.process.Env, env...)
	p.process.Stdout, p.process.Stderr = output, output
	if err := p.process.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.done <- p.process.Wait() }()
	t.Cleanup(func() {
		if !p.stopped {
			_ = p.process.Process.Kill()
			<-p.done
		}
		if t.Failed() {
			for _, path := range []string{output.Name(), p.logPath} {
				data, _ := os.ReadFile(path)
				t.Logf("%s:\n%s", filepath.Base(path), data)
			}
		}
	})

	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-p.done:
			p.stopped = true
			t.Fatalf("root command exited before becoming ready: %v", err)
		default:
		}
		response, err := client.Head(p.baseURL + "/")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return p
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("root command did not become ready")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// featureFlags reads the diagnostic page; refresh runs one reconcile in the running process.
func (p *rootProcess) featureFlags(refresh bool) string {
	p.t.Helper()
	url := p.baseURL + "/ff?c=startup-flag&t=bool"
	if refresh {
		url += "&r=1"
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Get(url)
	if err != nil {
		p.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK {
		p.t.Fatalf("feature flag page returned %d: %s %v", response.StatusCode, data, err)
	}
	return string(data)
}

// stop sends SIGTERM and returns the log lines written by the process.
func (p *rootProcess) stop() []string {
	p.t.Helper()
	if err := p.process.Process.Signal(syscall.SIGTERM); err != nil {
		p.t.Fatal(err)
	}
	select {
	case err := <-p.done:
		p.stopped = true
		if err != nil {
			p.t.Errorf("root command did not stop cleanly: %v", err)
		}
	case <-time.After(5 * time.Second):
		p.t.Fatal("root command did not stop after SIGTERM")
	}
	data, err := os.ReadFile(p.logPath)
	if err != nil {
		p.t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func logLines(lines []string, level, contains string) []string {
	var matched []string
	for _, line := range lines {
		if strings.Contains(line, "\t"+level+"\t") && strings.Contains(line, contains) {
			matched = append(matched, line)
		}
	}
	return matched
}

// countingConsul answers every request with 503 and counts it.
func countingConsul(t *testing.T, listener net.Listener) *atomic.Int32 {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "no Consul request is expected", http.StatusServiceUnavailable)
	}))
	_ = server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return &requests
}

// Without a Consul address UQ must not build a Consul client: a missing Redis key
// means the built-in defaults, reported once, and a written key is picked up by
// the next reconcile.
func TestFeatureFlagsWithoutConsulAddressUseDefaults(t *testing.T) {
	for _, tc := range []struct {
		name       string
		consul     map[string]any
		wantErrors []string
	}{
		{name: "address-omitted"},
		{name: "address-empty", consul: map[string]any{"consul_address": ""}},
		{name: "address-without-host", consul: map[string]any{"consul_address": ":8500"}, wantErrors: []string{"consul_address_missing_host"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			redis := miniredis.RunT(t)
			envListener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			// consul/api falls back to CONSUL_HTTP_ADDR for an empty address.
			envRequests := countingConsul(t, envListener)
			var legacyRequests *atomic.Int32
			if legacyListener, err := net.Listen("tcp", "127.0.0.1:8500"); err == nil {
				legacyRequests = countingConsul(t, legacyListener)
			} else {
				t.Logf("127.0.0.1:8500 is in use, requests to the former default address are not counted: %v", err)
			}

			p := startRootProcess(t, redis, tc.consul, "redis", "CONSUL_HTTP_ADDR="+envListener.Addr().String())
			for i := 0; i < 3; i++ {
				page := p.featureFlags(true)
				if strings.Contains(page, "refresh feature flags err") || !strings.Contains(page, "false => 100") {
					t.Fatalf("missing key without Consul must apply built-in defaults without errors, reconcile %d: %s", i, page)
				}
			}
			if redis.Exists(optionalConsulFlagsKey) {
				t.Fatal("defaults must not be written to Redis")
			}

			if err := redis.Set(optionalConsulFlagsKey, optionalConsulRules); err != nil {
				t.Fatal(err)
			}
			if page := p.featureFlags(true); !strings.Contains(page, "true => 100") {
				t.Fatalf("written rules must take effect on the next reconcile: %s", page)
			}
			// The key is the only source: deleting it restores the defaults on every instance.
			redis.Del(optionalConsulFlagsKey)
			if page := p.featureFlags(true); !strings.Contains(page, "false => 100") || strings.Contains(page, "refresh feature flags err") {
				t.Fatalf("deleted rules must fall back to built-in defaults: %s", page)
			}
			if redis.Exists(optionalConsulFlagsKey) {
				t.Fatal("defaults must not be written to Redis after the key is deleted")
			}

			lines := p.stop()
			errorLines := logLines(lines, "error", "")
			if len(errorLines) != len(tc.wantErrors) {
				t.Errorf("want %d error lines naming %v, got %d:\n%s", len(tc.wantErrors), tc.wantErrors, len(errorLines), strings.Join(errorLines, "\n"))
			}
			for i, reason := range tc.wantErrors {
				if i < len(errorLines) && !strings.Contains(errorLines[i], reason) {
					t.Errorf("error line %d must name %s: %s", i, reason, errorLines[i])
				}
			}
			if defaults := logLines(lines, "info", builtInDefaultsMessage); len(defaults) != 1 {
				t.Errorf("built-in defaults must be reported exactly once, got %d:\n%s", len(defaults), strings.Join(defaults, "\n"))
			}
			if calls := envRequests.Load(); calls != 0 {
				t.Errorf("no Consul client may be built from the environment address, got %d requests", calls)
			}
			if legacyRequests != nil && legacyRequests.Load() != 0 {
				t.Errorf("no Consul client may target the former default address, got %d requests", legacyRequests.Load())
			}
		})
	}
}

// A configured Consul that cannot be reached keeps the current contract: the
// error is reported on every reconcile, the last rules stay effective and nothing
// is written to Redis.
func TestFeatureFlagsWithUnreachableConsulKeepRules(t *testing.T) {
	redis := miniredis.RunT(t)
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := "http://" + closed.Addr().String()
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := redis.Set(optionalConsulFlagsKey, optionalConsulRules); err != nil {
		t.Fatal(err)
	}

	p := startRootProcess(t, redis, map[string]any{"consul_address": address}, "redis")
	if page := p.featureFlags(false); !strings.Contains(page, "true => 100") {
		t.Fatalf("existing Redis rules must apply at startup: %s", page)
	}
	redis.Del(optionalConsulFlagsKey)
	for i := 0; i < 2; i++ {
		page := p.featureFlags(true)
		if !strings.Contains(page, "refresh feature flags err") || !strings.Contains(page, "true => 100") {
			t.Fatalf("an unreachable Consul must fail the reconcile and keep the last rules, reconcile %d: %s", i, page)
		}
	}
	if redis.Exists(optionalConsulFlagsKey) {
		t.Fatal("a failed migration must not write Redis")
	}

	lines := p.stop()
	if failures := logLines(lines, "error", "get feature flags failed"); len(failures) < 2 {
		t.Errorf("every failed reconcile must be reported, got %d:\n%s", len(failures), strings.Join(lines, "\n"))
	}
	if defaults := logLines(lines, "info", builtInDefaultsMessage); len(defaults) != 0 {
		t.Errorf("a configured Consul must not be replaced by defaults:\n%s", strings.Join(defaults, "\n"))
	}
}

// Consul Storage needs a Consul address; without one the startup reports the reason by name.
func TestConsulStorageWithoutAddressIsReportedByName(t *testing.T) {
	for _, tc := range []struct {
		name   string
		consul map[string]any
		reason string
	}{
		{name: "address-omitted", reason: "consul_address_not_configured"},
		{name: "address-without-host", consul: map[string]any{"consul_address": "http://:8500"}, reason: "consul_address_missing_host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := startRootProcess(t, miniredis.RunT(t), tc.consul, "")
			lines := p.stop()
			var storage []string
			for _, line := range logLines(lines, "error", tc.reason) {
				if strings.Contains(line, "storage.source") {
					storage = append(storage, line)
				}
			}
			// Both Storage readers (TSDB and InfluxDB) report it once at load.
			if len(storage) != 2 {
				t.Errorf("want one named Storage error per reader, got %d:\n%s", len(storage), strings.Join(lines, "\n"))
			}
		})
	}
}
