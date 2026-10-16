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

// A helper process exercises the real root command without sharing its Viper,
// SDK clients, signal handlers or service singletons with other cmd tests.
func TestRedisOnlyStartupHelper(t *testing.T) {
	configPath := os.Getenv("UQ_REDIS_STARTUP_HELPER_CONFIG")
	if configPath == "" {
		return
	}
	rootCmd.SetArgs([]string{"--config", configPath})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestRedisOnlyFreshStartup(t *testing.T) {
	redis := miniredis.RunT(t)
	var consulRequests, vmRequests atomic.Int32
	consul := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		consulRequests.Add(1)
		http.Error(w, "Consul is unavailable", http.StatusServiceUnavailable)
	}))
	defer consul.Close()

	const start, end = int64(1700000040), int64(1700000100)
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SQL string `json:"sql"`
		}
		var query struct {
			APIType         string   `json:"api_type"`
			ResultTableList []string `json:"result_table_list"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal([]byte(body.SQL), &query); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if query.APIType != "query_range" || len(query.ResultTableList) != 1 || query.ResultTableList[0] != "2_startup_metric" {
			http.Error(w, "expected the seeded VM route", http.StatusBadRequest)
			return
		}
		vmRequests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"result":true,"code":"00","data":{"list":[{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"value"},"values":[[%d,"7"],[%d,"9"]]}]}}]}}`, start, end)
	}))
	defer vm.Close()

	// Only the new Redis sources are populated; no Consul snapshot or local
	// bbolt cache is available to make a partially migrated startup appear valid.
	if err := redis.Set("startup:uq:data:storage:1", fmt.Sprintf(`{"type":"victoria_metrics","address":%q}`, vm.URL)); err != nil {
		t.Fatal(err)
	}
	redis.HSet("startup:spaces:space_to_result_table", "bkcc__2", `{"startup.metric":{"table_id":"startup.metric","filters":[]}}`)
	redis.HSet("startup:spaces:result_table_detail", "startup.metric", `{"table_id":"startup.metric","storage_id":1,"storage_type":"victoria_metrics","storage_name":"vm-test","db":"startup","measurement":"metric","fields":["value"],"measurement_type":"bk_traditional_measurement","vm_rt":"2_startup_metric"}`)
	const featureFlagsKey = "startup:uq:data:feature_flag"
	if redis.Exists(featureFlagsKey) {
		t.Fatal("fixture must start with the Feature Flag key missing")
	}

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
	configData, err := json.Marshal(map[string]any{
		"consul": map[string]any{
			"enabled": false, "consul_address": consul.URL,
			"service_name": "startup-test", "check_ttl": "30s",
		},
		"storage": map[string]any{"source": "redis"},
		"redis": map[string]any{
			"mode": "standalone", "host": redis.Host(), "port": redisPort,
			"kv_base_path": "startup:uq", "dial_timeout": "1s", "read_timeout": "1s",
		},
		"influxdb": map[string]any{
			"space_router_prefix":            "startup:spaces",
			"space_router_bbolt_path":        filepath.Join(dir, "space.db"),
			"space_router_bbolt_bucket_name": "startup", "is_cache": false,
		},
		"http":    map[string]any{"address": "127.0.0.1", "port": httpPort},
		"jwt":     map[string]any{"enabled": false},
		"trace":   map[string]any{"enable": false},
		"logger":  map[string]any{"level": "warning", "path": filepath.Join(dir, "uq.log")},
		"bbolt":   map[string]any{"default_path": filepath.Join(dir, "default.db")},
		"bk_data": map[string]any{"address": vm.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "startup.json")
	if err := os.WriteFile(configPath, configData, 0600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(dir, "process.log")
	output, err := os.Create(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	process := exec.Command(os.Args[0], "-test.run=^TestRedisOnlyStartupHelper$")
	process.Dir = dir
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(entry), "UNIFY-QUERY_") && !strings.HasPrefix(entry, "UQ_REDIS_STARTUP_HELPER_CONFIG=") && !strings.HasPrefix(entry, "GOPS_CONFIG_DIR=") {
			process.Env = append(process.Env, entry)
		}
	}
	process.Env = append(process.Env, "UQ_REDIS_STARTUP_HELPER_CONFIG="+configPath, "GOPS_CONFIG_DIR="+filepath.Join(dir, "gops"))
	process.Stdout, process.Stderr = output, output
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			_ = process.Process.Kill()
			<-done
		}
		if t.Failed() {
			for _, path := range []string{outputPath, filepath.Join(dir, "uq.log")} {
				data, _ := os.ReadFile(path)
				t.Logf("%s:\n%s", filepath.Base(path), data)
			}
		}
	})

	client := &http.Client{Timeout: time.Second}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", httpPort)
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-done:
			stopped = true
			t.Fatalf("root command exited before becoming ready: %v", err)
		default:
		}
		response, err := client.Head(baseURL + "/")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("root command did not become ready")
		}
		time.Sleep(25 * time.Millisecond)
	}

	request, err := http.NewRequest(http.MethodPost, baseURL+"/query/ts/promql", strings.NewReader(fmt.Sprintf(`{"promql":"bkmonitor:startup:metric:value","start":"%d","end":"%d","step":"60s"}`, start, end)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Bk-Scope-Space-Uid", "bkcc__2")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("representative VM query returned %d: %s", response.StatusCode, data)
	}
	var result struct {
		Series []struct {
			Columns []string    `json:"columns"`
			Values  [][]float64 `json:"values"`
		} `json:"series"`
		IsPartial bool `json:"is_partial"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode VM query result: %v; body=%s", err, data)
	}
	if result.IsPartial || len(result.Series) != 1 || len(result.Series[0].Values) != 2 || vmRequests.Load() == 0 {
		t.Fatalf("expected two valid points from the seeded VM route, got %s", data)
	}
	valueColumn, timeColumn := -1, -1
	for index, column := range result.Series[0].Columns {
		if column == "_value" {
			valueColumn = index
		}
		if column == "_time" {
			timeColumn = index
		}
	}
	for index, row := range result.Series[0].Values {
		if valueColumn < 0 || timeColumn < 0 || len(row) <= valueColumn || len(row) <= timeColumn || row[valueColumn] != float64(7+index*2) || row[timeColumn] != float64((start+int64(index)*60)*1000) {
			t.Fatalf("expected VM points at the requested timestamps with values 7 and 9, got %s", data)
		}
	}

	deadline = time.Now().Add(3 * time.Second)
	for !redis.Exists(featureFlagsKey) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if snapshot, err := redis.Get(featureFlagsKey); err != nil || snapshot != "{}" || redis.TTL(featureFlagsKey) != 0 {
		t.Errorf("expected persistent empty Feature Flag snapshot, got value=%q err=%v ttl=%s", snapshot, err, redis.TTL(featureFlagsKey))
	}
	response, err = client.Get(baseURL + "/storage_print")
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(data), "from memory (count: 1)") || !strings.Contains(string(data), vm.URL) {
		t.Fatalf("expected seeded Redis Storage in memory, got status=%d body=%s err=%v", response.StatusCode, data, err)
	}
	response, err = client.Get(baseURL + "/storage_print?source=consul&r=1")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("cross-source storage refresh returned %d, want 400", response.StatusCode)
	}

	if err := process.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		stopped = true
		if err != nil {
			t.Errorf("root command did not stop cleanly: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("root command did not stop after SIGTERM")
	}
	if calls := consulRequests.Load(); calls != 0 {
		t.Errorf("consul.enabled=false made %d Consul requests, want zero", calls)
	}
	t.Logf("root command stopped; VM requests=%d, Consul requests=%d", vmRequests.Load(), consulRequests.Load())
}
