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
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kmsg"
	"go.yaml.in/yaml/v3"
	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/taskdispatch"
	"linkd/internal/telemetry"
)

type drillWorker struct {
	name, role, pool, metrics string
	explicit                  bool
	process                   *linkdProcess
	offline                   atomic.Bool
	proxy                     *httptest.Server
}

type schedulingDrill struct {
	t                            *testing.T
	ctx                          context.Context
	root, binary, temp, evidence string
	cfg                          config.Config
	source                       config.EventSource
	revision                     int64
	client                       taskdispatch.Client
	workers                      map[string]*drillWorker
	snapshots, steps             *os.File
	last                         taskdispatch.State
}

// TestSchedulingDrillE2E 实现文档 S01-S13；默认跳过，所有服务数据保留供复核。
func TestSchedulingDrillE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skip("set LINKD_E2E=1 for explicit scheduling drill")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 13*time.Minute)
	defer cancel()
	root := repositoryRoot(t)
	env := loadEnvironment(t)
	names := newResourceNames()
	es := newElasticsearchClient(t, env.ElasticsearchURL)
	esVersion := es.version(ctx, t)
	rc := redis.NewClient(&redis.Options{Addr: env.RedisAddress, Password: env.RedisPassword, DB: env.RedisDatabase})
	defer func() { _ = rc.Close() }()
	if err := rc.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	kafka := newKafkaClient(t, env.KafkaBroker, "dispatch-drill-"+names.Token)
	defer kafka.Close()
	createTopics(ctx, t, kafka, names.RawTopic, names.OutputTopic)
	temp := t.TempDir()
	binary := filepath.Join(temp, "linkd")
	buildLinkd(t, ctx, root, binary)
	dataset := loadGeneratedDataset(t, root)
	path := writeConfig(t, root, temp, "config.elasticsearch.template.yaml", env, names, dataset.Config.EventSourceID, nil)
	cfg, err := config.Load(path, config.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := os.MkdirTemp(os.Getenv("LINKD_DISPATCH_DRILL_DIR"), "linkd-dispatch-drill-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("evidence=%s deployment=%s es=%s raw_topic=%s", evidence, names.Token, esVersion, names.RawTopic)
	d := &schedulingDrill{t: t, ctx: ctx, root: root, binary: binary, temp: temp, evidence: evidence, cfg: cfg, source: cfg.EventSources[0], workers: map[string]*drillWorker{}, client: taskdispatch.Client{URL: cfg.Dispatch.URL, Token: cfg.Dispatch.APIToken}}
	d.snapshots = d.file("states.jsonl")
	defer func() { _ = d.snapshots.Close() }()
	d.steps = d.file("steps.jsonl")
	defer func() { _ = d.steps.Close() }()
	t.Cleanup(func() {
		for _, w := range d.workers {
			w.offline.Store(false)
		}
		for _, w := range d.workers {
			if w.name != "all" {
				d.stop(w)
			}
		}
		if w := d.workers["all"]; w != nil {
			d.stop(w)
		}
		for _, w := range d.workers {
			if w.proxy != nil {
				w.proxy.Close()
			}
		}
	})
	d.step("S01", func() {
		d.start("all", "all-in-one", "a", false)
		d.wait(30*time.Second, func(s taskdispatch.State) bool { return len(s.Workers) == 1 })
		d.start("c1", "cleaner", "a", false)
		d.start("c2", "cleaner", "b", false)
		d.start("c3", "cleaner", "b", true)
		d.start("c4", "cleaner", "a", false)
		d.start("l1", "lifecycle", "a", false)
		d.wait(30*time.Second, func(s taskdispatch.State) bool { return len(s.Workers) == 6 })
		d.publish()
		s := d.counts(3, 2)
		d.wait(30*time.Second, func(s taskdispatch.State) bool {
			n := 0
			for _, task := range s.Tasks {
				if task.Role == "cleaner" && task.Phase == "running" && len(task.Partitions) > 0 {
					n++
				}
			}
			return n == 3
		})
		for _, task := range s.Tasks {
			if task.Phase != "stopped" && s.Workers[task.Worker].Labels["drill"] == "c3" {
				t.Fatal("explicit worker accepted empty selector")
			}
		}
	})
	d.step("S02", func() {
		d.source.Scheduling.Cleaner.Selector = map[string]string{"pool": "b"}
		d.publish()
		s := d.counts(2, 2)
		for _, task := range s.Tasks {
			if task.Role == "cleaner" && task.Phase == "running" && s.Workers[task.Worker].Labels["pool"] != "b" {
				t.Fatal("selector mismatch")
			}
		}
		d.source.Scheduling.Cleaner.Selector = map[string]string{"pool": "a"}
		d.publish()
		s = d.counts(3, 2)
		roles := map[string]bool{}
		for _, task := range s.Tasks {
			if s.Workers[task.Worker].Labels["drill"] == "all" && task.Phase == "running" {
				roles[task.Role] = true
			}
		}
		if !roles["cleaner"] || !roles["lifecycle"] {
			t.Fatal("all-in-one roles do not coexist")
		}
		d.source.Scheduling.Cleaner.Selector = nil
		for _, n := range []int{1, 0} {
			d.source.Scheduling.Cleaner.Replicas.Number = &n
			d.publish()
			d.counts(n, 2)
		}
		d.source.Scheduling.Cleaner.Replicas.Number = nil
		d.publish()
		d.counts(3, 2)
		zero := 0
		d.source.Scheduling.Lifecycle.Replicas.Number = &zero
		d.publish()
		d.counts(3, 0)
		d.source.Scheduling.Lifecycle.Replicas.Number = nil
		d.publish()
		d.counts(3, 2)
	})
	d.step("S03", func() { d.source.DefaultSeverity = "info"; d.publish(); d.versions(3, 2) })
	d.step("S04", func() {
		before := d.state()
		req := kmsg.NewPtrCreatePartitionsRequest()
		req.Topics = []kmsg.CreatePartitionsRequestTopic{{Topic: names.RawTopic, Count: 5}}
		response, err := req.RequestWith(ctx, kafka)
		if err != nil {
			t.Fatal(err)
		}
		for _, topic := range response.Topics {
			if topic.ErrorCode != 0 {
				t.Fatalf("partition expansion error=%d", topic.ErrorCode)
			}
		}
		after := d.counts(4, 2)
		for id, task := range before.Tasks {
			if task.Phase == "running" && after.Tasks[id].Epoch != task.Epoch {
				t.Fatal("partition expansion replaced healthy task")
			}
		}
	})
	d.step("S05", func() {
		before := d.state()
		d.source.Storage.Kafka.Security.Protocol = "ssl"
		d.publish()
		d.wait(30*time.Second, func(s taskdispatch.State) bool { return s.Metadata[d.source.EventSourceID].Error != "" })
		after := d.state()
		for id, task := range before.Tasks {
			if task.Role == "cleaner" && task.Phase == "running" && (after.Tasks[id].Epoch != task.Epoch || after.Tasks[id].Phase != "running") {
				t.Fatal("failed preflight disrupted old cleaner")
			}
		}
		if d.metric(d.workers["all"], "linkd_dispatch_operations_total", `linkd_operation="kafka_probe"`, `linkd_outcome="failed"`) < 1 {
			t.Fatal("missing probe failure metric")
		}
		d.source.Storage.Kafka.Security.Protocol = "plaintext"
		d.publish()
		d.wait(30*time.Second, func(state taskdispatch.State) bool { return state.Metadata[d.source.EventSourceID].Error == "" })
		d.counts(4, 2)
		// 恢复原执行摘要时保留旧 Cleaner Release 是正确行为，不能断言它等于最新编辑版本。
		restored := d.state()
		for id, task := range before.Tasks {
			if task.Role == "cleaner" && task.Phase == "running" && restored.Tasks[id].Epoch != task.Epoch {
				t.Fatal("restoring unchanged execution config restarted cleaner")
			}
		}
		d.source.Scheduling.Cleaner.Selector = map[string]string{"pool": "b"}
		d.publish()
		d.counts(2, 2)
	})
	d.step("S06", func() {
		old := d.task("c2")
		d.stop(d.workers["c2"])
		d.start("c2", "cleaner", "b", false)
		d.counts(2, 2)
		fresh := d.task("c2")
		if fresh.Epoch <= old.Epoch || fresh.Worker == old.Worker {
			t.Fatal("restart reused old execution or session")
		}
	})
	d.step("S07", func() {
		// all 会把滚动启动的新会话计为新增副本；固定 2 才是在验证接管而不是扩容。
		before := d.metric(d.workers["all"], "linkd_dispatch_operations_total", `linkd_operation="reconcile"`, `linkd_outcome="succeeded"`)
		replicas := 2
		d.source.Scheduling.Cleaner.Replicas.Number = &replicas
		d.publish()
		d.wait(15*time.Second, func(taskdispatch.State) bool {
			return d.metric(d.workers["all"], "linkd_dispatch_operations_total", `linkd_operation="reconcile"`, `linkd_outcome="succeeded"`) >= before+2
		})
		d.counts(2, 2)
		old := d.task("c2")
		w := d.workers["c2"]
		if err := w.process.command.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		<-w.process.wait
		w.process.exited = true
		w.process.exitErr = nil
		_ = w.process.logFile.Close()
		d.start("c2", "cleaner", "b", false)
		expiry := old.Expires
		d.wait(100*time.Second, func(s taskdispatch.State) bool {
			oldTask := s.Tasks[old.ID]
			if oldTask.Epoch == old.Epoch && oldTask.Expires.After(expiry) {
				expiry = oldTask.Expires
			}
			for _, task := range s.Tasks {
				if task.Role == "cleaner" && task.Phase != "stopped" && task.Worker != old.Worker && s.Workers[task.Worker].Labels["drill"] == "c2" {
					if time.Now().Before(expiry.Add(taskdispatch.SafetyMargin)) {
						t.Fatal("replacement assigned before old authorization plus margin")
					}
					return task.Phase == "running" && task.Epoch > old.Epoch
				}
			}
			return false
		})
		d.counts(2, 2)
		if d.metric(d.workers["all"], "linkd_dispatch_transitions_total", `linkd_reason="authorization_expired"`) != 1 {
			t.Fatal("timeout takeover metric must count exactly once")
		}
	})
	d.step("S08", func() {
		old := d.task("c2")
		w := d.workers["c2"]
		w.offline.Store(true)
		deadline := time.Now().Add(6 * time.Second)
		for d.metric(w, "linkd_dispatch_worker_heartbeat_failures") == 0 {
			if time.Now().After(deadline) {
				t.Fatal("heartbeat failure not observed")
			}
			d.pause(200 * time.Millisecond)
		}
		if d.metric(w, "linkd_dispatch_worker_admission_paused") == 0 {
			t.Fatal("failed heartbeat did not pause admission")
		}
		w.offline.Store(false)
		d.wait(8*time.Second, func(s taskdispatch.State) bool { return d.metric(w, "linkd_dispatch_worker_heartbeat_failures") == 0 })
		if d.state().Tasks[old.ID].Epoch != old.Epoch {
			t.Fatal("short disconnect changed epoch")
		}
	})
	d.step("S09", func() {
		old := d.task("c2")
		w := d.workers["c2"]
		w.offline.Store(true)
		d.wait(35*time.Second, func(s taskdispatch.State) bool {
			return d.metric(w, "linkd_dispatch_worker_tasks", `linkd_task_role="cleaner"`, `linkd_task_phase="stopped"`) > 0
		})
		if exited, _ := w.process.checkExited(); exited {
			t.Fatal("worker exited instead of reporting task self-stop")
		}
		if d.metric(w, "linkd_dispatch_transitions_total", `linkd_reason="disconnected"`) < 1 {
			t.Fatal("missing disconnected self-stop metric")
		}
		if d.metric(w, "linkd_dispatch_worker_tasks", `linkd_task_role="cleaner"`, `linkd_task_phase="running"`) != 0 {
			t.Fatal("disconnected worker still running")
		}
		w.offline.Store(false)
		d.wait(100*time.Second, func(s taskdispatch.State) bool {
			return s.Tasks[old.ID].Epoch > old.Epoch && s.Tasks[old.ID].Phase == "running"
		})
		d.counts(2, 2)
	})
	d.step("S10", func() {
		d.source.Cleaner.Runtime = &config.CleanerRuntimeConfig{WorkerCount: 129}
		d.publish()
		s := d.counts(0, 2)
		found := false
		for _, status := range s.Statuses {
			if status.Role == "cleaner" && status.Target == 2 && strings.Contains(status.Reason, "capacity pending") {
				found = true
			}
		}
		if !found {
			t.Fatal("budget shortage not reported")
		}
		if d.metric(d.workers["all"], "linkd_dispatch_controller_replicas", `linkd_task_role="cleaner"`, `linkd_replica_kind="shortage"`) != 2 {
			t.Fatal("budget shortage metric missing")
		}
		d.source.Cleaner.Runtime = nil
		d.publish()
		d.versions(2, 2)
	})
	d.step("S11", func() {
		for _, severity := range []string{"critical", "info", "warning"} {
			d.source.DefaultSeverity = severity
			d.publish()
		}
		d.versions(2, 2)
	})
	d.step("S12", func() {
		p := d.workers["all"].process
		produceDataset(ctx, t, env.KafkaBroker, names.RawTopic, dataset)
		events := waitForAcceptedEvents(ctx, t, p, es, names.EventIndex, dataset.Expected)
		assertEvents(t, events, dataset)
		alerts := waitForAlertsVisible(ctx, t, p, es, names.AlertIndex, dataset.Expected.Alerts)
		assertAlerts(t, alerts, dataset.Expected.Alerts)
		logs := waitForAlertLogsVisible(ctx, t, p, es, names.AlertLogIndex, dataset.Expected.OperationCounts)
		assertAlertLogs(t, logs, dataset.Expected.OperationCounts)
		outputs := consumeOutputs(ctx, t, env.KafkaBroker, names, dataset.Expected.OutputMessages)
		assertOutputs(t, outputs, events, dataset.Expected.OutputMessages)
		names.SignalStream = mailbox.SourceStream(names.SignalStream, names.Token, d.source.EventSourceID)
		waitForRedisDrain(ctx, t, rc, names)
	})
	d.step("S13", func() {
		before := d.state()
		d.source.Enabled = false
		d.publish()
		d.wait(90*time.Second, func(state taskdispatch.State) bool {
			cleanersActive := false
			for _, task := range state.Tasks {
				if task.Role == "cleaner" && task.Phase != "stopped" {
					cleanersActive = true
				}
			}
			if cleanersActive {
				for id, task := range before.Tasks {
					if task.Role == "lifecycle" && task.Phase == "running" && state.Tasks[id].Phase != "running" {
						t.Fatal("lifecycle stopped before cleaners")
					}
				}
			}
			return drillCounts(state, 0, 0)
		})
		d.source.Enabled = true
		d.publish()
		d.counts(2, 2)
		var record eventsource.Record
		if err := d.client.Call(ctx, http.MethodDelete, "/api/v1/event-sources/"+d.source.EventSourceID, map[string]int64{"expected_revision": d.revision}, &record); err != nil {
			t.Fatal(err)
		}
		d.counts(0, 0)
		var release eventsource.Release
		if err := d.client.Call(ctx, http.MethodGet, "/api/v1/event-sources/"+d.source.EventSourceID+"/releases/1", nil, &release); err != nil || release.Version != 1 {
			t.Fatal("old release missing", err)
		}
		d.pause(300 * time.Millisecond)
		if d.metric(d.workers["all"], "linkd_dispatch_worker_tasks", `linkd_task_phase="running"`) != 0 {
			t.Fatal("worker gauge did not reset")
		}
	})
	t.Log("S01-S13 completed; test service data retained; child processes stopped during cleanup")
}

func (d *schedulingDrill) file(name string) *os.File {
	d.t.Helper()
	//nolint:gosec // G304: evidence 是本轮 MkdirTemp 创建的专属目录，name 来自固定场景/进程枚举。
	f, err := os.OpenFile(filepath.Join(d.evidence, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		d.t.Fatal(err)
	}
	return f
}

func drillAddress(t *testing.T) string {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func (d *schedulingDrill) start(name, role, pool string, explicit bool) {
	d.t.Helper()
	w := d.workers[name]
	if w == nil {
		w = &drillWorker{name: name, role: role, pool: pool, explicit: explicit}
		d.workers[name] = w
		target, err := url.Parse(d.cfg.Dispatch.URL)
		if err != nil {
			d.t.Fatal(err)
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorHandler = func(rw http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(rw, "test control unavailable", http.StatusBadGateway)
		}
		w.proxy = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			if w.offline.Load() {
				http.Error(rw, "test control unavailable", http.StatusServiceUnavailable)
				return
			}
			proxy.ServeHTTP(rw, r)
		}))
	}
	w.metrics = drillAddress(d.t)
	cfg := d.cfg
	cfg.Dispatch.URL = w.proxy.URL
	cfg.Worker = config.WorkerConfig{Labels: map[string]string{"pool": pool, "drill": name}, RequireExplicitSelector: explicit}
	cfg.Telemetry = &telemetry.Config{Metrics: telemetry.MetricsConfig{Exporter: telemetry.ExporterPrometheus, Prometheus: telemetry.PrometheusConfig{ListenAddress: w.metrics}}}
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		d.t.Fatal(err)
	}
	configPath := filepath.Join(d.temp, name+".yaml")
	if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
		d.t.Fatal(err)
	}
	log := d.file(name + ".log")
	//nolint:gosec // G204: binary 是本测试编译结果，role 来自固定枚举，configPath 属于本轮 TempDir。
	// 子进程由 stop 的 SIGTERM/强杀预算回收，不能让测试 Context 取消绕过正常退出断言。
	command := exec.CommandContext(context.WithoutCancel(d.ctx), d.binary, "run", role, "--config", configPath)
	command.Env = append(os.Environ(), "LINKD_CONTROL_PLANE_URL="+w.proxy.URL, "LINKD_API_TOKEN="+cfg.Dispatch.APIToken, "LINKD_WORKER_TOKEN="+cfg.Dispatch.WorkerToken)
	command.Dir = d.root
	command.Stdout = log
	command.Stderr = log
	if err := command.Start(); err != nil {
		d.t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	w.process = &linkdProcess{command: command, wait: done, logFile: log, logPath: log.Name()}
}

func (d *schedulingDrill) stop(w *drillWorker) {
	if w == nil || w.process == nil {
		return
	}
	if err := w.process.stop(); err != nil {
		d.t.Errorf("stop %s: %v", w.name, err)
	}
}

func (d *schedulingDrill) state() taskdispatch.State {
	d.t.Helper()
	var state taskdispatch.State
	call, cancel := context.WithTimeout(d.ctx, 2*time.Second)
	defer cancel()
	if err := d.client.Call(call, http.MethodGet, "/api/v1/runtime", nil, &state); err != nil {
		return taskdispatch.State{}
	}
	if err := json.NewEncoder(d.snapshots).Encode(struct {
		At    time.Time
		State taskdispatch.State
	}{time.Now().UTC(), state}); err != nil {
		d.t.Fatal(err)
	}
	seen := map[string]bool{}
	for id, task := range state.Tasks {
		if task.Phase == "stopped" {
			continue
		}
		key := task.Source + "/" + task.Role + "/" + task.Worker
		if seen[key] {
			d.t.Fatalf("duplicate worker/source/role: %s", key)
		}
		seen[key] = true
		if old, ok := d.last.Tasks[id]; ok && task.Epoch < old.Epoch {
			d.t.Fatal("epoch went backwards")
		}
	}
	d.last = state
	return state
}

func (d *schedulingDrill) wait(timeout time.Duration, ready func(taskdispatch.State) bool) taskdispatch.State {
	d.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		s := d.state()
		if s.Tasks != nil && ready(s) {
			return s
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("condition timed out after %s; evidence=%s", timeout, d.evidence)
		}
		d.pause(250 * time.Millisecond)
	}
}

func (d *schedulingDrill) pause(duration time.Duration) {
	d.t.Helper()
	select {
	case <-d.ctx.Done():
		d.t.Fatal(d.ctx.Err())
	case <-time.After(duration):
	}
}

func drillCounts(s taskdispatch.State, c, l int) bool {
	if s.Tasks == nil {
		return false
	}
	counts := map[string]int{}
	for _, task := range s.Tasks {
		if task.Phase == "running" {
			counts[task.Role]++
		} else if task.Phase != "stopped" {
			return false
		}
	}
	return counts["cleaner"] == c && counts["lifecycle"] == l
}

func (d *schedulingDrill) counts(c, l int) taskdispatch.State {
	d.t.Helper()
	return d.wait(90*time.Second, func(s taskdispatch.State) bool { return drillCounts(s, c, l) })
}

func (d *schedulingDrill) versions(c, l int) {
	d.t.Helper()
	d.wait(90*time.Second, func(s taskdispatch.State) bool {
		if !drillCounts(s, c, l) {
			return false
		}
		for _, task := range s.Tasks {
			if task.Phase == "running" && task.Version != d.revision {
				return false
			}
		}
		return true
	})
}

func (d *schedulingDrill) publish() {
	d.t.Helper()
	var record eventsource.Record
	if err := d.client.Call(d.ctx, http.MethodPut, "/api/v1/event-sources/"+d.source.EventSourceID, taskdispatch.Mutation{Expected: d.revision, Spec: d.source}, &record); err != nil {
		d.t.Fatal(err)
	}
	d.revision = record.Revision
}

func (d *schedulingDrill) task(name string) taskdispatch.Task {
	d.t.Helper()
	for _, task := range d.state().Tasks {
		if task.Role == "cleaner" && task.Phase == "running" && d.last.Workers[task.Worker].Labels["drill"] == name {
			return task
		}
	}
	d.t.Fatalf("worker %s has no running cleaner", name)
	return taskdispatch.Task{}
}

func (d *schedulingDrill) scrape(w *drillWorker) string {
	d.t.Helper()
	ctx, cancel := context.WithTimeout(d.ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+w.metrics+"/metrics", nil)
	if err != nil {
		d.t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		d.t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		d.t.Fatal(err)
	}
	return string(b)
}

func (d *schedulingDrill) metric(w *drillWorker, name string, labels ...string) float64 {
	d.t.Helper()
	value := 0.0
	for _, line := range strings.Split(d.scrape(w), "\n") {
		if !strings.HasPrefix(line, name+"{") && !strings.HasPrefix(line, name+" ") {
			continue
		}
		match := true
		for _, label := range labels {
			if !strings.Contains(line, label) {
				match = false
			}
		}
		if !match {
			continue
		}
		fields := strings.Fields(line)
		n, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			d.t.Fatal(err)
		}
		value += n
	}
	return value
}

func (d *schedulingDrill) step(name string, run func()) {
	d.t.Helper()
	started := time.Now()
	d.t.Logf("%s started", name)
	defer func() {
		outcome := "passed"
		if d.t.Failed() {
			outcome = "failed"
		}
		elapsed := time.Since(started).Seconds()
		if err := json.NewEncoder(d.steps).Encode(struct {
			Name, Outcome string
			Started       time.Time
			Seconds       float64
		}{name, outcome, started.UTC(), elapsed}); err != nil {
			d.t.Error(err)
		}
		d.t.Logf("%s %s %.3fs", name, outcome, elapsed)
	}()
	run()
	for _, w := range d.workers {
		if exited, _ := w.process.checkExited(); exited {
			continue
		}
		metrics := d.scrape(w)
		if err := os.WriteFile(filepath.Join(d.evidence, fmt.Sprintf("%s-%s.prom", name, w.name)), []byte(metrics), 0o600); err != nil {
			d.t.Fatal(err)
		}
	}
}
