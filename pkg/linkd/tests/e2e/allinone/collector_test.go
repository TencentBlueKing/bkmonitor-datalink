// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package allinone_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"go.yaml.in/yaml/v3"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle/mailbox"
)

// TestCollectorPushElasticsearchE2E 不绕过接收、Python、Kafka、Cleaner 或 Lifecycle。
// 仅操作本轮随机命名空间，普通测试不连接外部服务。
func TestCollectorPushElasticsearchE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skip("set LINKD_E2E=1")
	}
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%t", shared), func(t *testing.T) { runCollectorPushElasticsearch(t, shared) })
	}
}

func runCollectorPushElasticsearch(t *testing.T, shared bool) {
	root, environment, names := repositoryRoot(t), loadEnvironment(t), newResourceNames()
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	directory := t.TempDir()
	pythonAddress := startCollectorPython(t, ctx, root, directory)
	es, client := prepareCollectorServices(t, ctx, environment, names)
	sourceID, tenant := "collector-e2e", "collector-tenant"
	binary := filepath.Join(directory, "linkd")
	buildLinkd(t, ctx, root, binary)
	path := writeConfig(t, root, directory, "config.elasticsearch.template.yaml", environment, names, sourceID, nil)
	cfg, err := config.Load(path, config.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Collector = &config.CollectorConfig{Endpoint: "http://" + pythonAddress + "/execute", Token: "collector-e2e-token", TimeoutSeconds: 10, MaxRequestBytes: 1 << 20, MaxResponseBytes: 8 << 20, RecoveryBatchSize: 2, MaxConcurrentRequests: 4}
	cfg.EventSources[0].RelatedTenantID = tenant
	cfg.EventSources[0].Hooks = nil
	cfg.EventSources[0].Collection = &config.SourceCollection{Plugin: "rest_api_push", Mode: "push", Secret: "collector-source-secret"}
	if shared {
		cfg.EventSources[0].RelatedTenantID = ""
		cfg.EventSources[0].Collection = &config.SourceCollection{Plugin: "rest_api_push", Mode: "push", TenantBindings: []config.CollectionTenantBinding{
			{TenantID: tenant, Enabled: true, Secret: "collector-source-secret"},
			{TenantID: "second-tenant", Enabled: true, Secret: "second-source-secret"},
		}}
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	process := startAllInOne(t, root, binary, path, directory)
	t.Cleanup(func() {
		if err := process.stop(); err != nil {
			t.Error(err)
		}
		if t.Failed() {
			if logs, err := os.ReadFile(process.logPath); err == nil {
				t.Logf("collector all-in-one log:\n%s", logs)
			}
		}
	})
	names.SignalStream = mailbox.SourceStream(names.SignalStream, names.Token, sourceID)
	importSources(ctx, t, process, path)
	waitUntilReady(ctx, t, process, es, client, names)
	started := time.Now().UTC().Add(-2 * time.Second).Truncate(time.Second)
	endpoint := cfg.Dispatch.URL + "/api/v1/ingest/sources/" + sourceID
	push := func(action, secret string, status int, occurred time.Time) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"fingerprint": "runtime-alarm", "title": "Runtime E2E", "bk_tenant_id": "spoofed-tenant", "occurred_at": occurred.Format(time.RFC3339), "evaluations": []map[string]string{{"action": action, "severity": "warning"}}})
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Secret", secret)
		request.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		var result struct {
			Result bool `json:"result"`
			Code   int  `json:"code"`
			Data   struct {
				Count int `json:"received_alarm_count"`
			} `json:"data"`
		}
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != status {
			t.Fatalf("push status=%d expected=%d code=%d", response.StatusCode, status, result.Code)
		}
		if status == http.StatusOK && (!result.Result || result.Code != 1200 || result.Data.Count != 1) {
			t.Fatalf("push outcome=%+v", result)
		}
	}
	push("triggered", "wrong-secret", http.StatusUnauthorized, started)
	if n, err := client.ZCard(ctx, names.Token+":collector:push-pending").Result(); err != nil || n != 0 {
		t.Fatalf("unauthorized request persisted: %d %v", n, err)
	}
	push("triggered", "collector-source-secret", http.StatusOK, started)
	waitAlert := func(wantedTenant string, status domain.AlertStatus) domain.Alert {
		t.Helper()
		for {
			alerts := loadAlerts(ctx, t, es, names.AlertIndex)
			for _, alert := range alerts {
				if alert.BKTenantID == wantedTenant && alert.Status == status {
					return alert
				}
			}
			if exited, err := process.checkExited(); exited {
				t.Fatalf("Linkd exited: %v", err)
			}
			select {
			case <-ctx.Done():
				t.Fatalf("waiting for alert %s: %v", status, ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	active := waitAlert(tenant, domain.AlertStatusActive)
	if active.BKTenantID != tenant || active.EventSourceID != sourceID || active.Fingerprint != "runtime-alarm" {
		t.Fatalf("wrong stored alert identity: %+v", active)
	}
	if shared {
		push("triggered", "second-source-secret", http.StatusOK, started)
		second := waitAlert("second-tenant", domain.AlertStatusActive)
		if second.AlertID == active.AlertID || second.EventSourceID != sourceID || second.Fingerprint != active.Fingerprint {
			t.Fatal("shared source crossed tenant identity")
		}
	}
	push("resolved", "collector-source-secret", http.StatusOK, started.Add(time.Second))
	recovered := waitAlert(tenant, domain.AlertStatusRecovered)
	if recovered.AlertID != active.AlertID {
		t.Fatal("recovery created another alert")
	}
	expectedEvents := 2
	if shared {
		expectedEvents = 3
		_ = waitAlert("second-tenant", domain.AlertStatusActive)
	}
	for {
		events := loadEvents(ctx, t, es, names.EventIndex)
		logs := loadAlertLogs(ctx, t, es, names.AlertLogIndex)
		trigger, recover, secondTrigger := false, false, !shared
		allAccepted := len(events) == expectedEvents
		for _, event := range events {
			allAccepted = allAccepted && event.Processing.State == domain.EventProcessStateAccepted
		}
		for _, log := range logs {
			trigger = trigger || (log.BKTenantID == tenant && log.OperationKind == domain.OperationKindTrigger)
			recover = recover || (log.BKTenantID == tenant && log.OperationKind == domain.OperationKindRecover)
			secondTrigger = secondTrigger || (log.BKTenantID == "second-tenant" && log.OperationKind == domain.OperationKindTrigger)
		}
		if allAccepted && trigger && recover && secondTrigger {
			for _, event := range events {
				if (event.Event.BKTenantID != tenant && (!shared || event.Event.BKTenantID != "second-tenant")) || event.Event.Fingerprint != "runtime-alarm" {
					t.Fatal("event scope changed")
				}
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("events/trigger/recover logs not persisted")
		case <-time.After(100 * time.Millisecond):
		}
	}
	finalAlerts := loadAlerts(ctx, t, es, names.AlertIndex)
	expectedAlerts := 1
	if shared {
		expectedAlerts = 2
	}
	if len(finalAlerts) != expectedAlerts {
		t.Fatalf("unexpected alert count: %d", len(finalAlerts))
	}
	for _, alert := range finalAlerts {
		if alert.BKTenantID == tenant && alert.Status != domain.AlertStatusRecovered {
			t.Fatal("primary alert not recovered")
		}
		if alert.BKTenantID == "second-tenant" && alert.Status != domain.AlertStatusActive {
			t.Fatal("other tenant was recovered")
		}
	}
	if n, err := client.ZCard(ctx, names.Token+":collector:push-pending").Result(); err != nil || n != 0 {
		t.Fatalf("completed request pending: %d %v", n, err)
	}
	if err := process.stop(); err != nil {
		t.Fatal(err)
	}
	t.Logf("real HTTP -> Python (2 processes x 2 threads) -> Kafka -> Cleaner -> Lifecycle -> Elasticsearch: trigger/recovery passed; shared=%t events=%d", shared, expectedEvents)
}

func startCollectorPython(t *testing.T, ctx context.Context, root, directory string, extraArgs ...string) string {
	t.Helper()
	address := drillAddress(t)
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(directory, "runtime-token")
	if err := os.WriteFile(token, []byte("collector-e2e-token"), 0600); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G304: directory 来自本测试的 TempDir，日志文件名固定。
	log, err := os.OpenFile(filepath.Join(directory, "python.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	python := envOrDefault("LINKD_RUNTIME_PYTHON", "python3")
	args := []string{"-m", "linkd_runtime", "serve", "--host", "127.0.0.1", "--port", port, "--token-file", token, "--processes", "2", "--threads", "2", "--execution-timeout", "5", "--read-timeout", "2"}
	args = append(args, extraArgs...)
	//nolint:gosec // G204: 参数来自本测试固定选项与隔离目录。
	command := exec.CommandContext(context.WithoutCancel(ctx), python, args...)
	command.Env = append(os.Environ(), "PYTHONPATH="+filepath.Join(root, "python"))
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	process := &linkdProcess{command: command, wait: done, logFile: log, logPath: log.Name()}
	t.Cleanup(func() {
		if err := process.stop(); err != nil {
			t.Error(err)
		}
		if t.Failed() {
			if data, err := os.ReadFile(process.logPath); err == nil {
				t.Logf("Python log:\n%s", data)
			}
		}
	})
	for {
		conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", address)
		if err == nil {
			_ = conn.Close()
			return address
		}
		if exited, err := process.checkExited(); exited {
			t.Fatalf("Python exited: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatal(fmt.Errorf("Python startup: %w", ctx.Err()))
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func prepareCollectorServices(t *testing.T, ctx context.Context, environment e2eEnvironment, names resourceNames) (*elasticsearchClient, *redis.Client) {
	t.Helper()
	es := newElasticsearchClient(t, environment.ElasticsearchURL)
	_ = es.version(ctx, t)
	t.Cleanup(func() {
		cleanupElasticsearch(t, es, names)
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		// 来源配置集合使用既有部署摘要命名，单独清理本测试创建的两个集合。
		namespace := fmt.Sprintf("%x", sha256.Sum256([]byte(names.Token)))
		for _, kind := range []string{"records", "releases"} {
			if _, err := es.do(cleanup, http.MethodDelete, "/linkd_event_source_"+namespace+"_"+kind, nil, nil); err != nil {
				t.Logf("source fixture cleanup: %v", err)
			}
		}
	})
	client := redis.NewClient(&redis.Options{Addr: environment.RedisAddress, Password: environment.RedisPassword, DB: environment.RedisDatabase})
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupRedis(t, client, names)
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		var cursor uint64
		for {
			keys, next, err := client.Scan(cleanup, cursor, "*"+names.Token+"*", 100).Result()
			if err != nil {
				t.Error(err)
				break
			}
			if len(keys) > 0 {
				if err := client.Del(cleanup, keys...).Err(); err != nil {
					t.Error(err)
					break
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
		_ = client.Close()
	})
	kafka := newKafkaClient(t, environment.KafkaBroker, "collector-e2e-"+names.Token)
	createTopics(ctx, t, kafka, names.RawTopic, names.OutputTopic)
	t.Cleanup(func() { cleanupKafkaTopics(t, kafka, names.RawTopic, names.OutputTopic); kafka.Close() })
	return es, client
}
