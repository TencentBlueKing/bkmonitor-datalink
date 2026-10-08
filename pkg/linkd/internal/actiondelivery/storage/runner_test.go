// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/actiondelivery"
	actionproducer "linkd/internal/actiondelivery/producer"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/projection"
	"linkd/internal/projection/redislock"
	projectionstore "linkd/internal/projection/storage"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
	"linkd/internal/telemetry"
	"linkd/internal/testkit/projectionfixture"
)

type interruptedEnqueue struct{}

func (interruptedEnqueue) RecordAction(context.Context, domain.Alert, domain.AlertActionIntent) error {
	return errors.New("injected crash before enqueue")
}

type atomicReceiptFault struct {
	actiondelivery.Store
	armed atomic.Bool
}

func (f *atomicReceiptFault) Put(ctx context.Context, task actiondelivery.Task, version string) (actiondelivery.StoredTask, error) {
	if task.Progress.State == "succeeded" && f.armed.CompareAndSwap(true, false) {
		return actiondelivery.StoredTask{}, errors.New("injected receipt persistence failure")
	}
	return f.Store.Put(ctx, task, version)
}

func startActionLoop(t *testing.T, run func(context.Context) error) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error("runner shutdown", err)
				}
			case <-time.After(10 * time.Second):
				t.Error("runner ignored bounded shutdown")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func actionUntil(t *testing.T, label string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for {
		if check() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out:", label)
		}
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// 使用真实业务仓储、Redis 指纹租约和两个运行器；接收端与来源目标解析仍是固定测试夹具。
// 接收端受理账本只存于本测试进程，不能据此声称已完成 KAC 生产接入。
func runActionAutomaticLoops(t *testing.T, s *Store, reopen func() *Store, cfg config.StorageConfig, deployment string) {
	t.Helper()
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("set LINKD_TEST_REDIS_ADDRESS")
	}
	business := deliveryRepository(t, s)
	proofDeployment := deployment + "-proof"
	proofs, err := projectionstore.Open(t.Context(), cfg, proofDeployment)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proofs.Close() })
	if s.transport != nil {
		name := cfg.WithDefaults().Elasticsearch.IndexPrefix + "-projection-" + fmt.Sprintf("%x", sha256.Sum256([]byte(proofDeployment)))
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if code, _, err := s.request(ctx, http.MethodDelete, "/"+name, nil); err != nil || code != 200 {
				t.Error("cleanup own proof index", code, err)
			}
		})
		if code, _, err := s.request(t.Context(), http.MethodPut, "/"+name+"/_settings", []byte(`{"index":{"refresh_interval":"1ms","number_of_replicas":0}}`)); err != nil || code != 200 {
			t.Fatal(code, err)
		}
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), ContextTimeoutEnabled: true, MaxRetries: -1, PoolSize: 8})
	t.Cleanup(func() { _ = client.Close() })
	locker, err := redislock.New(client, deployment)
	if err != nil {
		t.Fatal(err)
	}
	workerLocker, err := scheduler.NewRedisLocker(client, (config.LifecycleConfig{}).ForSource(deployment, "source").SchedulerConfig())
	if err != nil {
		t.Fatal(err)
	}
	receiver := &simulatedActionReceiver{actions: map[string]actiondelivery.Receipt{}}
	var deny atomic.Bool
	var denied atomic.Int64
	deny.Store(true)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/action" && deny.Load() {
			denied.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		receiver.ServeHTTP(w, r)
	}))
	t.Cleanup(remote.Close)
	sender, err := actiondelivery.NewHTTPSender()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sender.Close)
	projectionSender, err := projectionfixture.New(remote.URL+"/projection", "fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(projectionSender.Close)
	runtime, err := telemetry.Start(t.Context(), config.TelemetryConfig{Metrics: config.TelemetryMetricsConfig{Exporter: config.TelemetryExporterPrometheus, Prometheus: config.TelemetryPrometheusConfig{ListenAddress: "127.0.0.1:0"}}}, telemetry.RoleControlPlane, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	var offset atomic.Int64
	clock := func() time.Time { return time.Now().UTC().Add(time.Duration(offset.Load()) * time.Second) }
	resolver := actionResolver{endpoint: remote.URL}
	gate, err := actiondelivery.NewProjectionGate(business, proofs)
	if err != nil {
		t.Fatal(err)
	}
	makeProcessor := func(rec lifecycle.ActionRecorder) *lifecycle.Processor {
		p, err := lifecycle.NewProcessor(business.Repository, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, intentSeverity{}, lifecycle.SystemClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithActionRecorder(rec), lifecycle.WithInitialProjectionTargets(map[string]bool{"kac": true}), lifecycle.WithSeverityUpgradePolicy("update_current"))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	newService := func(tasks actiondelivery.Store) *actiondelivery.Service {
		service, err := actiondelivery.New(tasks, gate, resolver, sender, locker, clock)
		if err != nil {
			t.Fatal(err)
		}
		return service
	}
	newRunner := func(tasks actiondelivery.Store) (*lifecycle.Processor, *actiondelivery.Runner) {
		service := newService(tasks)
		p := makeProcessor(service)
		producer, err := actionproducer.New(config.LifecycleConfig{}, deployment, client, business, p)
		if err != nil {
			t.Fatal(err)
		}
		runner, err := actiondelivery.NewRunner(business.Repository.(store.ActionWorkStore), tasks, producer, service, runtime.ActionRunnerObserver(nil), clock)
		if err != nil {
			t.Fatal(err)
		}
		return p, runner
	}
	event := storetest.Event("runner-private-tenant", "opening", "runner-fp", "warning")
	event.EventSourceVersion = 4
	// 源事件和人工关闭与后台补扫共用生产指纹租约。
	withWorkerLease := func(run func(context.Context) error) error {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		var lease scheduler.Lease
		for {
			var err error
			lease, err = workerLocker.Acquire(ctx, scheduler.CorrelationKey(event.BKTenantID, event.EventSourceID, event.Fingerprint))
			if err == nil {
				break
			}
			if !errors.Is(err, scheduler.ErrLockBusy) {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
		err := run(ctx)
		release, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer stop()
		return errors.Join(err, workerLocker.Release(release, lease))
	}
	original, err := business.CreateEvent(t.Context(), event)
	if err != nil {
		t.Fatal(err)
	}
	initial := makeProcessor(interruptedEnqueue{})
	err = withWorkerLease(func(ctx context.Context) error { _, err := initial.ProcessEvent(ctx, original.StoredEvent); return err })
	if err == nil {
		t.Fatal("injected first enqueue failure hidden")
	}
	frozen, err := business.GetEvent(t.Context(), event.BKTenantID, event.EventID)
	if err != nil || frozen.Processing.Plan == nil {
		t.Fatal("missing pending event plan", err)
	}
	alertID := frozen.Processing.Plan.Mutations[0].Alert.AlertID
	current, err := business.GetAlertCurrent(t.Context(), event.BKTenantID, alertID)
	if err != nil || current.Alert.ActionPending == nil {
		t.Fatal("missing atomic intent", err)
	}
	originalTask, err := actiondelivery.NewTask(current.Alert, "kac", actiondelivery.Cause{Type: "source_event", ID: event.EventID}, clock())
	if err != nil {
		t.Fatal(err)
	}
	fault := &atomicReceiptFault{Store: s}
	p, one := newRunner(fault)
	_, two := newRunner(fault)
	projectionService, err := projection.New(proofs, business, resolver, projectionSender, locker, clock)
	if err != nil {
		t.Fatal(err)
	}
	projectionRunner, err := projection.NewRunner(business.Repository.(store.ProjectionWorkStore), proofs, projectionService, nil, clock)
	if err != nil {
		t.Fatal(err)
	}
	stopProjection := startActionLoop(t, projectionRunner.Run)
	stopOne, stopTwo := startActionLoop(t, one.Run), startActionLoop(t, two.Run)
	actionUntil(t, "sweep clears intent without new Event and waits for visibility", func() bool {
		row, e := s.Get(t.Context(), event.BKTenantID, originalTask.ID)
		a, ae := business.GetAlertCurrent(t.Context(), event.BKTenantID, alertID)
		return e == nil && ae == nil && a.Alert.ActionPending == nil && row.Task.Progress.State == "waiting_projection" && row.Task.Progress.Attempts == 0
	})
	if denied.Load() != 0 {
		t.Fatal("HTTP action preceded visible projection")
	}
	receiver.mu.Lock()
	receiver.visible = true
	receiver.mu.Unlock()
	actionUntil(t, "automatic permanent failure", func() bool {
		row, e := s.Get(t.Context(), event.BKTenantID, originalTask.ID)
		return e == nil && row.Task.Progress.State == "failed" && row.Task.Progress.ErrorCode == "remote_unauthorized" && row.Task.Progress.Attempts == 1
	})
	failed, err := s.Get(t.Context(), event.BKTenantID, originalTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	retrier, err := actiondelivery.NewRetrier(s, locker, clock)
	if err != nil {
		t.Fatal(err)
	}
	// API 层由 all-in-one 真实进程用例覆盖；本存储用例直接验证相同 Retrier 的原命令去重。
	command := actiondelivery.RetryCommand{TenantID: event.BKTenantID, TaskID: originalTask.ID, ExpectedVersion: failed.Version, OperationID: "retry-original", OperatorID: "tester", Reason: "恢复投递"}
	retry := func() actiondelivery.Progress {
		t.Helper()
		row, err := retrier.Retry(t.Context(), command)
		if err != nil {
			t.Fatal(err)
		}
		return row.Task.Progress
	}
	deny.Store(false)
	fault.armed.Store(true)
	audit := retry()
	if audit.LastRetry == nil || audit.LastRetry.Command.ExpectedVersion != failed.Version {
		t.Fatal("missing original command audit")
	}
	actionUntil(t, "remote accepted but local receipt save interrupted", func() bool {
		row, e := s.Get(t.Context(), event.BKTenantID, originalTask.ID)
		receiver.mu.Lock()
		accepted := len(receiver.actions)
		receiver.mu.Unlock()
		return e == nil && !fault.armed.Load() && accepted == 1 && row.Task.Progress.State == "sending" && row.Task.Progress.Generation == 2 && row.Task.Progress.Attempts == 1
	})
	stopOne()
	stopTwo()
	restored := reopen()
	// 已停止旧运行器后推进测试时钟，越过持久发送预留期限；不修改真实 Redis TTL。
	offset.Store(31)
	p, resumed := newRunner(restored)
	stopResumed := startActionLoop(t, resumed.Run)
	actionUntil(t, "restart repeats original request and receiver deduplicates", func() bool {
		row, e := restored.Get(t.Context(), event.BKTenantID, originalTask.ID)
		return e == nil && row.Task.Progress.State == "succeeded" && row.Task.Progress.Generation == 2 && row.Task.Progress.Attempts == 2 && row.Task.Progress.TotalAttempts == 3 && row.Task.Progress.PreviousUnconfirmed && row.Task.Request.Hash() == originalTask.Request.Hash()
	})
	actionUntil(t, "successful action leaves work index", func() bool {
		n, e := restored.CountWork(t.Context(), event.BKTenantID, actiondelivery.MaxPendingPerTenant)
		return e == nil && n == 0
	})
	stopResumed()
	replay := retry()
	if replay.Generation != 2 || replay.LastRetry == nil || *replay.LastRetry != *audit.LastRetry {
		t.Fatal("replayed recovery changed original audit")
	}
	if err := withWorkerLease(func(ctx context.Context) error { _, e := p.ProcessEvent(ctx, frozen); return e }); err != nil {
		t.Fatal("replay frozen plan", err)
	}
	upgrade := storetest.Event(event.BKTenantID, "upgrade", event.Fingerprint, "critical")
	upgrade.EventSourceVersion = 4
	upgrade.OccurredAt = event.OccurredAt.Add(time.Minute)
	nextEvent, err := business.CreateEvent(t.Context(), upgrade)
	if err != nil {
		t.Fatal(err)
	}
	if err := withWorkerLease(func(ctx context.Context) error { _, e := p.ProcessEvent(ctx, nextEvent.StoredEvent); return e }); err != nil {
		t.Fatal("actual upgrade", err)
	}
	closeCommand := lifecycle.CloseAlertCommand{OperationID: "close-without-new-event", BKTenantID: event.BKTenantID, AlertID: alertID, OperatorKind: domain.OperatorKindUser, OperatorID: "tester", Reason: "manual", EffectiveAt: time.Now().UTC()}
	if err := withWorkerLease(func(ctx context.Context) error { _, e := p.CloseAlert(ctx, closeCommand); return e }); err != nil {
		t.Fatal("actual manual close", err)
	}
	actionUntil(t, "terminal projection visible before resuming old firing", func() bool {
		row, e := business.GetAlertCurrent(t.Context(), event.BKTenantID, alertID)
		return e == nil && row.Alert.Revision == 3 && row.Alert.Projection.Targets["kac"].SyncedRevision == 3
	})
	_, last := newRunner(restored)
	stopLast := startActionLoop(t, last.Run)
	actionUntil(t, "stale firing skipped and terminal action accepted", func() bool {
		rows, e := restored.List(t.Context(), actiondelivery.Query{TenantID: event.BKTenantID, Limit: 16})
		if e != nil || len(rows) != 3 {
			return false
		}
		for _, r := range rows {
			want := "succeeded"
			if r.Task.Request.Revision == 2 {
				want = "skipped"
			}
			if r.Task.Progress.State != want {
				return false
			}
		}
		n, e := restored.CountWork(t.Context(), event.BKTenantID, actiondelivery.MaxPendingPerTenant)
		return e == nil && n == 0
	})
	stopLast()
	stopProjection()
	receiver.mu.Lock()
	calls, accepted := receiver.actionCalls, len(receiver.actions)
	receiver.mu.Unlock()
	if calls != 3 || accepted != 2 || denied.Load() != 1 {
		t.Fatal("wrong actual receiver counts", calls, accepted, denied.Load())
	}
	current, err = business.GetAlertCurrent(t.Context(), event.BKTenantID, alertID)
	if err != nil || current.Alert.ActionPending != nil || current.Alert.Status != domain.AlertStatusClosed || current.Alert.Revision != 3 {
		t.Fatal("incorrect final lifecycle", err)
	}
	req, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+runtime.PrometheusListenAddress()+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 200 {
		t.Fatal("metrics", resp.StatusCode, err)
	}
	text := string(raw)
	if strings.Contains(text, event.BKTenantID) || strings.Contains(text, alertID) || strings.Contains(text, originalTask.ID) {
		t.Fatal("identity leaked into metrics")
	}
	for _, outcome := range []string{"enqueued", "waiting_projection", "accepted", "skipped", "failed"} {
		if !strings.Contains(text, `linkd_outcome="`+outcome+`"`) {
			t.Fatal("missing actual outcome metric", outcome)
		}
	}
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, "linkd_action_runner_active{") || strings.HasPrefix(line, "linkd_action_runner_inflight{") {
			if !strings.HasSuffix(line, " 0") {
				t.Fatal("runner left active metric", line)
			}
		}
	}
	t.Log("two automatic runners + real fingerprint/target leases: sweep without Event, projection gate, manual retry, interrupted receipt + reopen/dedup, stale firing skipped, manual close accepted; 4 action HTTP requests, 2 unique acceptances")
}

func TestElasticsearchActionAutomaticLoops(t *testing.T) {
	actionStoreFixture(t, "elasticsearch", func(s *Store, reopen func() *Store, c config.StorageConfig, d string) {
		runActionAutomaticLoops(t, s, reopen, c, d)
	})
}

func TestMySQLActionAutomaticLoops(t *testing.T) {
	actionStoreFixture(t, "mysql", func(s *Store, reopen func() *Store, c config.StorageConfig, d string) {
		runActionAutomaticLoops(t, s, reopen, c, d)
	})
}
