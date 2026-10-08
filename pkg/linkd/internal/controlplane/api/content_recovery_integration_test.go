// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/consume/redisstream"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/assembly"
	"linkd/internal/enrich/description"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/redisclient"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
	"linkd/internal/taskdispatch"
)

// recoveryFacts 在同一冻结规则下模拟稳定事实补齐；生产 MySQL 绑定另由 live 测试验收。
type recoveryFacts struct {
	ready atomic.Bool
	calls atomic.Int64
}

func (f *recoveryFacts) Resolve(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (description.Facts, error) {
	f.calls.Add(1)
	if !f.ready.Load() {
		return description.Facts{}, &description.Error{Code: "observed_value_missing"}
	}
	value, err := description.ParseNumber(json.RawMessage(`10`))
	return description.Facts{ItemName: "CPU", Unit: "percent", Value: value, Connector: "and", Algorithms: []description.Algorithm{{Type: "Threshold", Groups: [][]description.Condition{{{Method: "gt", Threshold: 5}}}}}}, err
}

// schedulerProcessor 仅记录真实 Processor 的调用顺序，不替换处理或持久化语义。
type schedulerProcessor struct {
	processor *lifecycle.Processor
	mu        sync.Mutex
	order     []string
}

func (p *schedulerProcessor) ProcessEvent(ctx context.Context, event store.StoredEvent) (lifecycle.ProcessResult, error) {
	p.mu.Lock()
	p.order = append(p.order, event.Event.EventID)
	p.mu.Unlock()
	return p.processor.ProcessEvent(ctx, event)
}

func TestContentFailureSignalResumeIntegration(t *testing.T) {
	address := os.Getenv("LINKD_TEST_DISPATCH_REDIS")
	if address == "" {
		t.Skip("set LINKD_TEST_DISPATCH_REDIS for explicit isolated Redis integration")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: address})
	defer func() { _ = client.Close() }()
	namespace := "content-recovery-" + uuid.NewString()
	stream, group, prefix := namespace+":signal", namespace+":group", namespace+":mailbox"
	defer client.Del(context.Background(), stream)
	release, _ := integrationRelease()
	release.Version = 7
	release.Spec.Version = 7
	release.Spec.RelatedTenantID = "tenant-a"
	release.Spec.Enrich = config.EnrichConfig{ContentMode: config.ContentModeBKMonitorDescription}
	sources := eventsource.New(agentSources{release: release}, config.DefaultSeverityConfig())
	controller, err := newIntegrationController(ctx, client, sources, namespace)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Del(context.Background(), controller.key, controller.leader)
	repo := memory.New()
	first := storetest.Event("tenant-a", "opening", "fingerprint", "warning")
	first.EventSourceVersion = 7
	first.Content = "source opening"
	second := storetest.Event("tenant-a", "later", "fingerprint", "warning")
	second.EventSourceVersion = 7
	second.Content = "source later"
	second.OccurredAt = first.OccurredAt.Add(time.Minute)
	for _, event := range []domain.Event{first, second} {
		if _, err := repo.CreateEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	mailboxes, err := mailbox.NewStore(client, mailbox.Config{KeyPrefix: prefix, SignalStream: stream, MaxPendingPerMailbox: 16})
	if err != nil {
		t.Fatal(err)
	}
	items, err := mailboxes.EnqueueBatch(ctx, []domain.Event{first, second})
	if err != nil || len(items) != 2 || items[0].Err != nil || items[1].Err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	mailboxID := items[0].MailboxID
	defer client.Del(context.Background(), prefix+":"+mailboxID+":events")
	facts := &recoveryFacts{}
	router, err := assembly.NewRouter([]config.EventSource{release.Spec}, enrich.Sources{}, assembly.WithDescriptionFacts(facts))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, router, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, logger, lifecycle.WithAlertContentBuilder(router))
	if err != nil {
		t.Fatal(err)
	}
	ordered := &schedulerProcessor{processor: processor}
	lockConfig := scheduler.DefaultConfig()
	lockConfig.LockKeyPrefix = namespace + ":lock"
	locker, err := scheduler.NewRedisLocker(client, lockConfig)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := scheduler.NewHandler(repo, mailboxes, ordered, locker, lockConfig, logger)
	if err != nil {
		t.Fatal(err)
	}
	handler.BindSource(release.ID)
	rc := consume.DefaultConfig()
	rc.ShutdownDrainTimeout = taskdispatch.DrainTimeout
	cfg := config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "synthetic-admin"}, WorkerToken: "synthetic-worker"}
	server := httptest.NewServer((&API{Sources: sources, Controller: controller, Config: cfg}).Handler())
	defer server.Close()
	cfg.URL = server.URL
	var started atomic.Int64
	agent := taskdispatch.Agent{Config: cfg, Logger: logger, Runtime: taskdispatch.WorkerRuntime{Lifecycle: &taskdispatch.TaskBudget{Concurrency: rc.WorkerCount, InflightBytes: int64(rc.MaxInflightBytes)}}, Roles: []string{"lifecycle"}, RunTask: func(work context.Context, task taskdispatch.Task, _ config.EventSource) error {
		started.Add(1)
		session, err := redisstream.NewSession(redisstream.Config{Connection: redisclient.Options{Address: address}, Stream: stream, Group: group, Consumer: taskdispatch.ConsumerName(task), CreateGroup: true, RetiredConsumers: task.Retired})
		if err != nil {
			return err
		}
		err = consume.New(rc, session, handler).Run(work)
		var failure interface{ PermanentContentFailure() string }
		if errors.As(err, &failure) {
			return taskdispatch.RequireTaskRepair(err)
		}
		return err
	}}
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("agent failed to stop")
		}
	}()
	plan := func() {
		t.Helper()
		if err := controller.update(ctx, func(state *taskdispatch.State) error {
			taskdispatch.Reconcile(state, []eventsource.Release{release}, time.Now())
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	var failed taskdispatch.Task
	waitContentRecovery(t, ctx, func() bool {
		plan()
		snapshot, err := controller.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range snapshot.Tasks {
			if task.Blocked && task.Phase == "stopped" {
				failed = task
				return true
			}
		}
		return false
	})
	pending, err := client.XPending(ctx, stream, group).Result()
	if err != nil || pending.Count != 1 {
		t.Fatal("failed signal not retained in PEL", err)
	}
	head, err := mailboxes.Peek(ctx, mailboxID)
	if err != nil || head != first.EventID {
		t.Fatal("failed mailbox head advanced", err)
	}
	stored, err := repo.GetEvent(ctx, first.BKTenantID, first.EventID)
	if err != nil || stored.Processing.Plan != nil || stored.Processing.State != domain.EventProcessStateUnprocessed {
		t.Fatal("failed content persisted a plan", err)
	}
	key := store.ActiveAlertKey{BKTenantID: first.BKTenantID, EventSourceID: first.EventSourceID, Fingerprint: first.Fingerprint}
	if _, err := repo.FindActiveAlert(ctx, key); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("failed content created an Alert")
	}
	// 超过原调度退避并反复对账，仍不能自动分配下一代。
	until := time.Now().Add(6 * time.Second)
	waitContentRecovery(t, ctx, func() bool {
		plan()
		if started.Load() != 1 {
			t.Fatal("permanent content failure restarted automatically")
		}
		return !time.Now().Before(until)
	})
	facts.ready.Store(true)
	body := `{"expected_source_version":7,"expected_epoch":` + strconv.FormatInt(failed.Epoch, 10) + `}`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/v1/event-sources/source/tasks/"+failed.ID+"/resume", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Internal-Token", testJWT(t, "synthetic-admin"))
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 204 {
		t.Fatalf("resume status %d", response.StatusCode)
	}
	waitContentRecovery(t, ctx, func() bool {
		plan()
		head, err := mailboxes.Peek(ctx, mailboxID)
		if err != nil {
			t.Fatal(err)
		}
		pending, err := client.XPending(ctx, stream, group).Result()
		if err != nil {
			t.Fatal(err)
		}
		return head == "" && pending.Count == 0
	})
	alert, err := repo.FindActiveAlert(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if alert.Alert.Content != "CPU > 5.0, 当前值10%" || alert.Alert.TriggerEventID != first.EventID || started.Load() != 2 || facts.calls.Load() != 2 {
		t.Fatal("resumed content or task identity changed")
	}
	ordered.mu.Lock()
	order := slices.Clone(ordered.order)
	ordered.mu.Unlock()
	if !slices.Equal(order, []string{first.EventID, first.EventID, second.EventID}) {
		t.Fatalf("mailbox order %v", order)
	}
	for i, event := range []domain.Event{first, second} {
		saved, err := repo.GetEvent(ctx, event.BKTenantID, event.EventID)
		expected := domain.EventProcessStateAccepted
		if i == 1 {
			expected = domain.EventProcessStateSuppressed
		}
		if err != nil || saved.Event.Content != event.Content || saved.Processing.State != expected || saved.Event.EnrichStatus != domain.EnrichStatusSucceeded || !slices.Equal(saved.Event.RelatedAlertIDs, []string{alert.Alert.AlertID}) {
			t.Fatalf("source Event changed or recovery incomplete: event=%s state=%s error=%v", event.EventID, saved.Processing.State, err)
		}
		// 同级重复触发完成处理并关联原Alert，但不重复放行处置；不能将该抑制误判为恢复失败。
		if i == 1 && saved.Processing.ReasonCode != "duplicate_trigger" {
			t.Fatalf("unexpected repeated trigger reason %q", saved.Processing.ReasonCode)
		}
	}
	if _, err := processor.ProcessEvent(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if facts.calls.Load() != 2 {
		t.Fatal("terminal replay generated content again")
	}
}

func waitContentRecovery(t *testing.T, ctx context.Context, ready func() bool) {
	t.Helper()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatal("content recovery deadline", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
