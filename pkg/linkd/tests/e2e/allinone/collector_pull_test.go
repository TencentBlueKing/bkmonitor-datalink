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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"linkd/internal/collector"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/taskdispatch"
)

func TestCollectorPullTakeoverElasticsearchE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skip("set LINKD_E2E=1")
	}
	if runtime.GOOS == "windows" {
		t.Skip("requires SIGSTOP/SIGCONT")
	}
	root, environment, names := repositoryRoot(t), loadEnvironment(t), newResourceNames()
	ctx, cancel := context.WithTimeout(t.Context(), 240*time.Second)
	defer cancel()
	directory := t.TempDir()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseFirst := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseFirst()
	var queries atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		number := queries.Add(1)
		if number == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]int64{"cursor": number})
	}))
	t.Cleanup(upstream.Close)
	script := `import json
import urllib.request
from linkd_runtime.base import BaseClientV2
MODES = ("pull",)
class AlarmClient(BaseClientV2):
    def pull_raw_events(self):
        with urllib.request.urlopen(self.alarm_source_config_dict["endpoint"], timeout=75) as response:
            self.cursor = json.load(response)["cursor"]
        return [{"fingerprint": "pull-alarm", "title": "Scheduled source", "occurred_at": self.alarm_source_obj.received_at,
                 "evaluations": [{"action": "triggered", "severity": "warning"}], "extra_data": {"attempt": self.cursor}}]
    def proposed_state(self):
        return {"meta_info": str(self.cursor)}
`
	pluginPath := filepath.Join(directory, "scheduled_plugin.py")
	if err := os.WriteFile(pluginPath, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(map[string]string{"scheduled_source": pluginPath})
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(directory, "plugins.json")
	if err := os.WriteFile(manifestPath, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	pythonAddress := startCollectorPython(t, ctx, root, directory, "--plugin-manifest", manifestPath, "--execution-timeout", "80")
	es, client := prepareCollectorServices(t, ctx, environment, names)
	sourceID, tenant := "collector-pull", "collector-tenant"
	binary := filepath.Join(directory, "linkd")
	buildLinkd(t, ctx, root, binary)
	path := writeConfig(t, root, directory, "config.elasticsearch.template.yaml", environment, names, sourceID, nil)
	cfg, err := config.Load(path, config.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Collector = &config.CollectorConfig{Endpoint: "http://" + pythonAddress + "/execute", Token: "collector-e2e-token", TimeoutSeconds: 90, MaxRequestBytes: 1 << 20, MaxResponseBytes: 8 << 20, RecoveryBatchSize: 2}
	cfg.EventSources[0].RelatedTenantID = tenant
	cfg.EventSources[0].Hooks = nil
	cfg.EventSources[0].Collection = &config.SourceCollection{Plugin: "scheduled_source", Mode: "pull", IntervalMinutes: 1, Configs: map[string]any{"endpoint": upstream.URL}}
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
			if data, err := os.ReadFile(process.logPath); err == nil {
				t.Logf("control/processing log:\n%s", data)
			}
		}
	})
	names.SignalStream = mailbox.SourceStream(names.SignalStream, names.Token, sourceID)
	importSources(ctx, t, process, path)
	waitUntilReady(ctx, t, process, es, client, names)
	first := startCollectorWorker(t, ctx, root, binary, directory, "first", cfg)
	admin := taskdispatch.Client{URL: cfg.Dispatch.URL, JWTSecretKey: cfg.Dispatch.JWT.SecretKey, JWTUsername: cfg.Dispatch.JWT.Username}
	snapshot := func() taskdispatch.State {
		t.Helper()
		var state taskdispatch.State
		if err := admin.Call(ctx, http.MethodGet, "/api/v1/runtime", nil, &state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	owner := func(state taskdispatch.State) taskdispatch.Task {
		t.Helper()
		for _, task := range state.Tasks {
			if task.Role == "collector" && task.Source == sourceID {
				return task
			}
		}
		t.Fatal("collector task missing")
		return taskdispatch.Task{}
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("scheduled plugin never called upstream")
	}
	before := snapshot()
	oldTask := owner(before)
	if before.Workers[oldTask.Worker].Labels["e2e_collector"] != "first" {
		t.Fatal("unexpected first owner")
	}
	receipts := collector.NewRedisReceipts(client, names.Token, 0)
	ids, err := receipts.PendingIDs(ctx, tenant, sourceID, 10)
	if err != nil || len(ids) != 1 {
		t.Fatalf("initial pending=%v err=%v", ids, err)
	}
	original, err := receipts.LoadRequest(ctx, tenant, sourceID, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if original.RequestID != "pull/"+original.ReceivedAt {
		t.Fatal("scheduled identity not fixed to slot")
	}
	if err := first.command.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	releaseFirst()
	second := startCollectorWorker(t, ctx, root, binary, directory, "second", cfg)
	t.Logf("paused first collector at epoch %d; waiting for existing lease expiry and takeover", oldTask.Epoch)
	progress := collector.NewRedisProgress(client, names.Token)
	for {
		state, err := progress.Load(ctx, tenant, sourceID)
		if err != nil {
			t.Fatal(err)
		}
		if state["meta_info"] == "2" {
			break
		}
		if exited, err := second.checkExited(); exited {
			t.Fatalf("standby worker exited: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("takeover did not commit original request: %v", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	after := snapshot()
	newTask := owner(after)
	if newTask.Epoch <= oldTask.Epoch || newTask.Worker == oldTask.Worker || after.Workers[newTask.Worker].Labels["e2e_collector"] != "second" {
		t.Fatalf("no real worker takeover: old=%+v new=%+v", oldTask, newTask)
	}
	completed, err := receipts.LoadResult(ctx, original)
	if err != nil || completed == nil || !completed.Completed || completed.Result.ProposedState["meta_info"] != "2" {
		t.Fatalf("replay outcome: %+v %v", completed, err)
	}
	if err := first.command.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	// 等待旧进程恢复至少两轮心跳，不能在刚发 SIGCONT 时就宣称它没有覆盖新状态。
	oldSequence := before.Workers[oldTask.Worker].Seq
	for {
		state := snapshot()
		if worker, ok := state.Workers[oldTask.Worker]; ok && worker.Seq >= oldSequence+2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("old worker did not resume heartbeats")
		case <-time.After(100 * time.Millisecond):
		}
	}
	retained, err := receipts.LoadResult(ctx, original)
	if err != nil || !reflect.DeepEqual(completed, retained) {
		t.Fatalf("late old worker changed completed result: %v", err)
	}
	state, err := progress.Load(ctx, tenant, sourceID)
	if err != nil || state["meta_info"] == "1" || state["meta_info"] == nil {
		t.Fatalf("cursor regressed: %v %v", state, err)
	}
	for {
		events := loadEvents(ctx, t, es, names.EventIndex)
		alerts := loadAlerts(ctx, t, es, names.AlertIndex)
		accepted := 0
		for _, event := range events {
			if event.Event.BKTenantID != tenant || event.Event.Fingerprint != "pull-alarm" {
				t.Fatal("pulled event identity changed")
			}
			if string(event.Event.ExtraData["attempt"]) == "1" {
				t.Fatal("late old worker published stale event")
			}
			if event.Processing.State == domain.EventProcessStateAccepted {
				accepted++
			}
		}
		if accepted > 0 && accepted == len(events) && len(alerts) == 1 && alerts[0].Status == domain.AlertStatusActive {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("recovered pull did not reach Lifecycle/Elasticsearch")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := first.stop(); err != nil {
		t.Fatal(err)
	}
	if err := second.stop(); err != nil {
		t.Fatal(err)
	}
	t.Logf("real collector takeover passed: epoch %d -> %d, original slot %s retained; old result did not overwrite cursor", oldTask.Epoch, newTask.Epoch, original.ReceivedAt)
}

func startCollectorWorker(t *testing.T, ctx context.Context, root, binary, directory, name string, cfg config.Config) *linkdProcess {
	t.Helper()
	cfg.Worker.Labels = map[string]string{"e2e_collector": name}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name+".yaml")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G304: 路径属于本测试 TempDir，name 为固定 first/second。
	log, err := os.OpenFile(filepath.Join(directory, name+".log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G204: 仅执行本轮编译的 Linkd 与测试目录配置。
	command := exec.CommandContext(context.WithoutCancel(ctx), binary, "run", "collector", "--config", path)
	command.Dir = root
	command.Stdout = log
	command.Stderr = log
	if err := command.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	process := &linkdProcess{command: command, wait: done, logFile: log, logPath: log.Name()}
	t.Cleanup(func() {
		_ = command.Process.Signal(syscall.SIGCONT)
		if err := process.stop(); err != nil {
			t.Error(err)
		}
		if t.Failed() {
			if data, err := os.ReadFile(process.logPath); err == nil {
				t.Logf("%s worker log:\n%s", name, data)
			}
		}
	})
	return process
}
