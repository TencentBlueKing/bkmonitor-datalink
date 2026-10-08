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
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"linkd/internal/actiondelivery"
	actionstore "linkd/internal/actiondelivery/storage"
	"linkd/internal/config"
	"linkd/internal/domain"
	mergeflow "linkd/internal/merge"
	"linkd/internal/policy"
	"linkd/internal/projection"
	projectionstore "linkd/internal/projection/storage"
	"linkd/internal/shieldcheck"
	"linkd/internal/suppressioncheck"
)

// TestAllInOneConsolePoliciesE2E 启动真实 Console 和 Chrome，不拦截浏览器业务接口。
// 每个后端仅使用本次隔离数据；浏览器操作结束后核对实际 Alert、请求和 Kafka action。
func TestAllInOneConsolePoliciesE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" || os.Getenv("LINKD_E2E_CONSOLE") != "1" {
		t.Skip("set LINKD_E2E=1 and LINKD_E2E_CONSOLE=1; requires installed Chrome and Console dependencies")
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "linkd")
	buildLinkd(t, t.Context(), root, binary)
	build, stop := context.WithTimeout(t.Context(), 2*time.Minute)
	defer stop()
	command := exec.CommandContext(build, "pnpm", "build")
	command.Dir = filepath.Join(root, "console")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build real console: %v\n%s", err, out)
	}
	for _, backend := range []string{"elasticsearch", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			h := startPolicyHarnessWithTimeout(t, root, binary, backend, 10*time.Minute)
			fixture := h.prepareConsolePolicies()
			fixture.Backend = backend
			console, address := h.startConsole(root)
			fixture.BaseURL = address + "/real-console"
			// 结果目录只包含本次合成身份和截图；凭据配置始终留在自动清理的 TempDir。
			output := filepath.Join(root, "console", "test-results", "real-"+backend+"-"+h.names.Token)
			if err := os.MkdirAll(output, 0o700); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(fixture)
			if err != nil {
				t.Fatal(err)
			}
			manifest := filepath.Join(t.TempDir(), "fixture.json")
			if err := os.WriteFile(manifest, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			run, cancel := context.WithTimeout(h.ctx, 4*time.Minute)
			defer cancel()
			command := exec.CommandContext(run, "pnpm", "exec", "playwright", "test", "--config", "playwright.real.config.ts")
			command.Dir = filepath.Join(root, "console")
			command.Env = consoleTestEnv("LINKD_CONSOLE_REAL_FIXTURE="+manifest, "LINKD_CONSOLE_REAL_OUTPUT="+output)
			out, err := command.CombinedOutput()
			if copyErr := os.WriteFile(filepath.Join(output, "fixture.json"), raw, 0o600); copyErr != nil {
				t.Error(copyErr)
			}
			t.Logf("real Console browser %s: %s", backend, out)
			if err != nil {
				t.Fatalf("real Console browser: %v; artifacts=%s", err, output)
			}
			t.Logf("real Console artifacts: %s", output)
			h.verifyConsolePolicies(fixture)
			if err := console.stop(); err != nil {
				t.Fatal(err)
			}
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
		})
	}
}

type consolePolicyFixture struct {
	Backend           string   `json:"backend"`
	BaseURL           string   `json:"base_url"`
	ClipWindow        string   `json:"clip_window"`
	ClipEvent         string   `json:"clip_event"`
	AggregationWindow string   `json:"aggregation_window"`
	AggregationEvent  string   `json:"aggregation_event"`
	AggregationOwner  string   `json:"aggregation_owner"`
	ShieldAlert       string   `json:"shield_alert"`
	ShieldRevision    int64    `json:"shield_revision"`
	MergeDecision     string   `json:"merge_decision"`
	MergeParent       string   `json:"merge_parent"`
	MergeChildren     []string `json:"merge_children"`
	MergeWindow       string   `json:"merge_window"`
	MergeWaiting      string   `json:"merge_waiting"`
	ActionTask        string   `json:"action_task"`
	ActionVersion     string   `json:"action_version"`
	ActionHash        string   `json:"action_hash"`
	ProjectionTask    string   `json:"projection_task"`
	ProjectionVersion string   `json:"projection_version"`
	ProjectionHash    string   `json:"projection_hash"`
}

func (h *policyHarness) prepareConsolePolicies() consolePolicyFixture {
	end := time.Now().Add(10 * time.Minute)
	h.publish("clip", policy.Suppression, map[string]any{"policy": condition("clip"), "scheme": []any{map[string]any{"type": "clip", "count": 3, "duration": 600, "duration_type": "second"}}}, end)
	h.send("clip", "policy-a", "browser-clip-1", "clip", "clip", "host", "warning", "triggered")
	clip := h.send("clip", "policy-a", "browser-clip-2", "clip", "clip", "host", "warning", "triggered")
	assertNoAlert(h.t, clip)
	clipWindow := h.suppressionWindows("clip", "clip")[0]
	h.publish("aggregation", policy.Suppression, map[string]any{"policy": condition("aggregation"), "scheme": []any{map[string]any{"type": "aggregation", "duration": 600, "duration_type": "second", "fields": []string{"object"}}}}, end)
	main := onlyAlert(h.t, h.send("aggregation", "policy-a", "browser-agg-main", "main", "aggregation", "shared", "warning", "triggered"))
	child := h.send("aggregation", "policy-b", "browser-agg-child", "child", "aggregation", "shared", "warning", "triggered")
	if onlyAlert(h.t, child) != main {
		h.t.Fatal("browser fixture aggregation owner mismatch")
	}
	aggWindow := h.suppressionWindows("aggregation", "aggregation")[0]
	h.expect(main, "firing")
	h.publish("shield", policy.Shield, map[string]any{"policy": condition("shield"), "shield_type": "time_shield", "model_id": "cmdb.host", "target_descriptor": map[string]any{"schema_version": 1, "model_id": "cmdb.host", "selectors": []any{map[string]any{"type": "instances", "instances": []any{map[string]any{"model_id": "cmdb.host", "model_inst_id": "host-1", "entity_uid": "cmdb.host|host-1"}}}}}}, end)
	shield := onlyAlert(h.t, h.send("shield", "policy-a", "browser-shield", "shield", "shield", "host", "warning", "triggered"))
	blocked := h.alert(shield, func(a domain.Alert) bool { return a.Shield.Active && a.Admission.AdmittedAt == nil })
	h.expect(shield)
	h.publish("merge", policy.Merge, map[string]any{"policy": []any{condition("merge-a"), condition("merge-b")}, "merge_cycle": 600, "is_cycle_merge": false, "aggregate_fields": []string{"object"}, "new_alarm_config": []any{map[string]any{"key": "name", "value": "browser aggregate ${alarm_num}"}, map[string]any{"key": "content", "value": "browser members ${alarm_num}"}, map[string]any{"key": "level", "value": "warning"}}}, end)
	first := onlyAlert(h.t, h.send("merge", "policy-a", "browser-merge-a", "a", "merge-a", "shared", "warning", "triggered"))
	second := onlyAlert(h.t, h.send("merge", "policy-b", "browser-merge-b", "b", "merge-b", "shared", "warning", "triggered"))
	var parent domain.Alert
	h.until("browser fixture merge parent", func() bool {
		for _, a := range h.alerts() {
			if a.BKTenantID == "merge" && a.EventSourceID == "builtin_alarm_merge" && a.Merge != nil && a.Merge.RelationsReady && a.MergeChange == nil && a.Admission.AdmittedAt != nil {
				parent = a
				return true
			}
		}
		return false
	})
	for _, id := range []string{first, second} {
		h.alert(id, func(a domain.Alert) bool {
			return a.Merge != nil && len(a.Merge.RelationIDs) == 1 && a.MergeChange == nil
		})
		h.expect(id)
	}
	h.expect(parent.AlertID, "firing", "close")
	waiting := onlyAlert(h.t, h.send("merge", "policy-a", "browser-waiting", "waiting", "merge-a", "waiting-group", "warning", "triggered"))
	wait := h.alert(waiting, func(a domain.Alert) bool { return a.Merge != nil && len(a.Merge.Pending) == 1 })
	h.expect(waiting)
	queued := h.prepareConsoleProjection(blocked)
	action := h.prepareConsoleAction(h.alert(main, func(a domain.Alert) bool { return a.AdmittedActiveMain() }))
	return consolePolicyFixture{ActionTask: action.Task.ID, ActionVersion: action.Version, ActionHash: action.Task.Request.Hash(), ClipWindow: clipWindow.ID, ClipEvent: clip.Event.EventID, AggregationWindow: aggWindow.ID, AggregationEvent: child.Event.EventID, AggregationOwner: main, ShieldAlert: shield, ShieldRevision: blocked.Revision, MergeDecision: parent.Merge.RelationIDs[0], MergeParent: parent.AlertID, MergeChildren: []string{first, second}, MergeWindow: wait.Merge.Pending[0].WindowID, MergeWaiting: waiting, ProjectionTask: queued.Task.ID, ProjectionVersion: queued.Version, ProjectionHash: queued.Task.Request.ContentHash}
}

// prepareConsoleProjection 只在本次部署集合中构造失败任务，测试管理链路。
// 来源绑定与正式投递器尚未装配，不修改真实 Alert，不声称由正式生产器产生任务。
func (h *policyHarness) prepareConsoleProjection(alert domain.Alert) projection.StoredTask {
	h.t.Helper()
	cfg, err := config.Load(h.configPath, config.Overrides{})
	if err != nil {
		h.t.Fatal(err)
	}
	tasks, err := projectionstore.Open(h.ctx, *cfg.Storage, cfg.Dispatch.WithDefaults().Deployment)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = tasks.Close() }()
	snapshot := alert.Clone()
	snapshot.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: alert.EventSourceVersion, RequiredRevision: alert.Revision}}}
	task, err := projection.NewTask(snapshot, "kac", time.Now())
	if err != nil {
		h.t.Fatal(err)
	}
	row, err := tasks.Put(h.ctx, task, "")
	if err != nil {
		h.t.Fatal(err)
	}
	attempt := row.Task.Clone()
	deadline := attempt.CreatedAt.Add(30 * time.Second)
	attempt.Progress.State = "sending"
	attempt.Progress.Attempts, attempt.Progress.TotalAttempts = 1, 1
	attempt.Progress.DueAt, attempt.Progress.LeaseUntil = nil, &deadline
	row, err = tasks.Put(h.ctx, attempt, row.Version)
	if err != nil {
		h.t.Fatal(err)
	}
	failed := row.Task.Clone()
	failed.Progress.State, failed.Progress.ErrorCode = "failed", "remote_unauthorized"
	failed.Progress.LeaseUntil = nil
	row, err = tasks.Put(h.ctx, failed, row.Version)
	if err != nil {
		h.t.Fatal(err)
	}
	return row
}

// prepareConsoleAction 仅以真实已获准 Alert 的快照构造隔离失败任务，验证管理读取/恢复。
// 它不修改原 Alert，不代表正式来源出口已启用，也不发送真实 KAC HTTP 处置。
func (h *policyHarness) prepareConsoleAction(alert domain.Alert) actiondelivery.StoredTask {
	h.t.Helper()
	cfg, err := config.Load(h.configPath, config.Overrides{})
	if err != nil {
		h.t.Fatal(err)
	}
	tasks, err := actionstore.Open(h.ctx, *cfg.Storage, cfg.Dispatch.WithDefaults().Deployment)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = tasks.Close() }()
	snapshot := alert.Clone()
	snapshot.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: alert.EventSourceVersion, RequiredRevision: alert.Revision}}}
	task, err := actiondelivery.NewTask(snapshot, "kac", actiondelivery.Cause{Type: alert.Admission.CauseType, ID: alert.Admission.CauseID}, time.Now())
	if err != nil {
		h.t.Fatal(err)
	}
	row, err := tasks.Put(h.ctx, task, "")
	if err != nil {
		h.t.Fatal(err)
	}
	attempt := row.Task.Clone()
	deadline := attempt.CreatedAt.Add(30 * time.Second)
	attempt.Progress.State = "sending"
	attempt.Progress.Attempts, attempt.Progress.TotalAttempts = 1, 1
	attempt.Progress.DueAt, attempt.Progress.LeaseUntil = nil, &deadline
	row, err = tasks.Put(h.ctx, attempt, row.Version)
	if err != nil {
		h.t.Fatal(err)
	}
	failed := row.Task.Clone()
	failed.Progress.State, failed.Progress.ErrorCode = "failed", "response_invalid"
	failed.Progress.LeaseUntil = nil
	row, err = tasks.Put(h.ctx, failed, row.Version)
	if err != nil {
		h.t.Fatal(err)
	}
	return row
}

func (h *policyHarness) startConsole(root string, extraEnv ...string) (*linkdProcess, string) {
	h.t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(h.ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		h.t.Fatal(err)
	}
	address := listener.Addr().String()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		h.t.Fatal(err)
	}
	logPath := filepath.Join(h.t.TempDir(), "console.log")
	//nolint:gosec // G304: 文件只在本测试创建的 TempDir 中。
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		h.t.Fatal(err)
	}
	//nolint:gosec // G204: 配置来自本测试 writeConfig 的隔离目录，执行文件和入口固定。
	command := exec.CommandContext(context.Background(), "node", "dist-server/server/index.js", "--config", h.configPath)
	command.Dir = filepath.Join(root, "console")
	command.Env = consoleTestEnv("NODE_ENV=production", "LINKD_CONSOLE_HOST=127.0.0.1", "LINKD_CONSOLE_PORT="+port, "LINKD_CONSOLE_BASE_PATH=/real-console", "LINKD_CONSOLE_TIMEOUT_MILLISECONDS=15000")
	command.Env = append(command.Env, extraEnv...)
	command.Stdout = log
	command.Stderr = log
	if err := command.Start(); err != nil {
		_ = log.Close()
		h.t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	process := &linkdProcess{command: command, wait: wait, logFile: log, logPath: logPath}
	h.t.Cleanup(func() {
		_ = process.stop()
		if h.t.Failed() {
			//nolint:gosec // G304: 日志路径来自本测试。
			raw, err := os.ReadFile(logPath)
			if err == nil {
				if len(raw) > 24000 {
					raw = raw[len(raw)-24000:]
				}
				h.t.Logf("real Console log: %s", raw)
			}
		}
	})
	client := &http.Client{Timeout: 2 * time.Second}
	h.until("real Console ready", func() bool {
		if exited, err := process.checkExited(); exited {
			h.t.Fatalf("Console exited before ready: %v", err)
		}
		request, err := http.NewRequestWithContext(h.ctx, http.MethodGet, "http://"+address+"/real-console/local-api/version", nil)
		if err != nil {
			h.t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		defer func() { _ = response.Body.Close() }()
		return response.StatusCode == 200
	})
	return process, "http://" + address
}

func consoleTestEnv(extra ...string) []string {
	result := []string{}
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "LINKD_") && !strings.HasPrefix(item, "NODE_ENV=") {
			result = append(result, item)
		}
	}
	return append(result, extra...)
}

func (h *policyHarness) verifyConsolePolicies(f consolePolicyFixture) {
	var mergeRequests struct {
		Items []mergeflow.RetryRequest `json:"items"`
	}
	h.until("real Console merge request persisted", func() bool {
		h.call(http.MethodGet, "/api/v1/policy-runtime/merge/relations/"+f.MergeDecision+"/requests?bk_tenant_id=merge", nil, &mergeRequests)
		return len(mergeRequests.Items) == 1 && mergeRequests.Items[0].State == "completed"
	})
	mergeRequest := mergeRequests.Items[0]
	if mergeRequest.Validate() != nil || mergeRequest.Command.OperatorID != "console-local" || mergeRequest.Command.Reason != "真实 Console 复核活动合并关系" || mergeRequest.Result.Reason != "no_progress" || !mergeRequest.Result.StepAttempted {
		h.t.Fatal("invalid real Console merge request", mergeRequest)
	}

	var checks struct {
		Items []suppressioncheck.Request `json:"items"`
	}
	checkBase := "/api/v1/policy-runtime/suppression/clip/" + f.ClipWindow + "/requests"
	h.call(http.MethodGet, checkBase+"?bk_tenant_id=clip", nil, &checks)
	if len(checks.Items) != 1 {
		h.t.Fatal("missing browser suppression request", checks)
	}
	var checked suppressioncheck.Request
	h.call(http.MethodGet, checkBase+"/"+checks.Items[0].ID+"?bk_tenant_id=clip", nil, &checked)
	if checked.Validate() != nil || checked.State != "completed" || checked.Result.Outcome != "retained" || checked.Result.Reason != "unbound_counter" || checked.Result.Changed || checked.Command.OperatorID != "console-local" || checked.Command.Reason != "真实 Console 复核当前计数" {
		h.t.Fatal("browser suppression check changed new counter", checked)
	}
	var task struct {
		Progress    projection.Progress `json:"progress"`
		ContentHash string              `json:"content_hash"`
	}
	h.call(http.MethodGet, "/api/v1/projection-tasks/"+f.ProjectionTask+"?bk_tenant_id=shield", nil, &task)
	p := task.Progress
	if p.State != "pending" || p.Generation != 2 || p.TotalAttempts != 1 || p.Attempts != 0 || p.Receipt != nil || p.LastRetry == nil || p.LastRetry.Command.OperatorID != "console-local" || p.LastRetry.Command.ExpectedVersion != f.ProjectionVersion || p.LastRetry.Command.Reason != "真实 Console 验证恢复原任务" || task.ContentHash != f.ProjectionHash {
		h.t.Fatal("browser projection retry did not preserve snapshot/audit or attempted unexpected delivery")
	}
	var actionTask struct {
		Progress    actiondelivery.Progress `json:"progress"`
		RequestHash string                  `json:"request_hash"`
	}
	h.call(http.MethodGet, "/api/v1/action-deliveries/"+f.ActionTask+"?bk_tenant_id=aggregation", nil, &actionTask)
	ap := actionTask.Progress
	if ap.State != "pending" || ap.Generation != 2 || ap.TotalAttempts != 1 || ap.Attempts != 0 || ap.Receipt != nil || ap.LastRetry == nil || ap.LastRetry.Command.OperatorID != "console-local" || ap.LastRetry.Command.ExpectedVersion != f.ActionVersion || ap.LastRetry.Command.Reason != "真实 Console 恢复原动作" || actionTask.RequestHash != f.ActionHash {
		h.t.Fatal("browser action retry changed original request or lost audit")
	}
	var requests struct {
		Items []shieldcheck.Request `json:"items"`
	}
	h.call(http.MethodGet, "/api/v1/policy-runtime/shield/alerts/"+f.ShieldAlert+"/requests?bk_tenant_id=shield", nil, &requests)
	if len(requests.Items) != 1 || requests.Items[0].State != "completed" || requests.Items[0].Command.OperatorID != "console-local" || requests.Items[0].Result == nil || requests.Items[0].Result.Report.Outcome != "retained" {
		h.t.Fatalf("browser check was not durably executed: %+v", requests)
	}
	h.alert(f.ShieldAlert, func(a domain.Alert) bool {
		return a.Shield.Active && a.Revision == f.ShieldRevision && a.Admission.AdmittedAt == nil
	})
	for _, id := range f.MergeChildren {
		a := h.alert(id, func(a domain.Alert) bool { return !a.Merge.Blocking() && a.MergeChange == nil })
		if a.Status != domain.AlertStatusActive || a.Admission.AdmittedAt != nil {
			h.t.Fatal("browser parent close admitted or recovered child")
		}
	}
	h.alert(f.MergeParent, func(a domain.Alert) bool { return a.Status == domain.AlertStatusClosed })
	next := h.send("merge", "policy-a", "browser-after-unlink", "a", "standalone", "shared", "warning", "triggered")
	if onlyAlert(h.t, next) != f.MergeChildren[0] {
		h.t.Fatal("next event changed child identity")
	}
	h.alert(f.MergeChildren[0], func(a domain.Alert) bool { return a.Admission.CauseID == next.Event.EventID })
	h.expect(f.MergeChildren[0], "firing")
	if windows := h.suppressionWindows("clip", "clip"); len(windows) != 1 || windows[0].Count == nil || *windows[0].Count != 2 {
		h.t.Fatal("browser reads changed clip count")
	}
	if windows := h.suppressionWindows("aggregation", "aggregation"); len(windows) != 1 || windows[0].MemberCount != 2 || windows[0].OwnerAlertID != f.AggregationOwner {
		h.t.Fatal("browser reads changed aggregation")
	}
}

func TestE2EConfigPreservesCredentialStrings(t *testing.T) {
	for _, value := range []string{"", "null", "1234", "quote\"colon: #\nnext", "{{REDIS_ADDRESS}}"} {
		t.Run(fmt.Sprintf("bytes-%d", len(value)), func(t *testing.T) {
			env := e2eEnvironment{ElasticsearchURL: "http://localhost:9200", RedisAddress: "localhost:6379", RedisPassword: value, KafkaBroker: "localhost:9092"}
			path := writeConfig(t, repositoryRoot(t), t.TempDir(), "config.mysql.template.yaml", env, newResourceNames(), "source", map[string]string{"{{MYSQL_ADDRESS}}": "localhost:3306", "{{MYSQL_DATABASE}}": "isolated", "{{MYSQL_USERNAME}}": value, "{{MYSQL_PASSWORD}}": value})
			//nolint:gosec // G304: path 由 writeConfig 在 TempDir 内创建。
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Storage struct {
					Redis map[string]any `yaml:"redis"`
					MySQL map[string]any `yaml:"mysql"`
				} `yaml:"storage"`
			}
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			for _, actual := range []any{doc.Storage.Redis["password"], doc.Storage.MySQL["password"], doc.Storage.MySQL["username"]} {
				if actual != value {
					t.Fatalf("credential type/value changed: type %T", actual)
				}
			}
		})
	}
}
