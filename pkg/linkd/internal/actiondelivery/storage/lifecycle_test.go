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
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/actiondelivery"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/projection"
	"linkd/internal/projection/redislock"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

type enqueueResponseFault struct {
	actiondelivery.Store
	fail bool
}

func (f *enqueueResponseFault) Put(ctx context.Context, t actiondelivery.Task, v string) (actiondelivery.StoredTask, error) {
	result, err := f.Store.Put(ctx, t, v)
	if err == nil && f.fail && v == "" {
		f.fail = false
		return actiondelivery.StoredTask{}, errors.New("injected enqueue response loss")
	}
	return result, err
}

type pendingGate struct{}

func (pendingGate) Check(context.Context, actiondelivery.Task) (projection.Receipt, error) {
	return projection.Receipt{}, actiondelivery.ErrBusy
}

type intentSeverity struct{}

func (intentSeverity) Priority(s string) (int, bool) {
	switch s {
	case "critical":
		return 1, true
	case "warning":
		return 2, true
	default:
		return 0, false
	}
}

func runLifecycleActionIntent(t *testing.T, s *Store, reopen func() *Store, deployment string) {
	t.Helper()
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("set LINKD_TEST_REDIS_ADDRESS")
	}
	business := deliveryRepository(t, s)
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), ContextTimeoutEnabled: true, MaxRetries: -1, PoolSize: 4})
	defer func() { _ = client.Close() }()
	locker, err := redislock.New(client, deployment)
	if err != nil {
		t.Fatal(err)
	}
	sender, err := actiondelivery.NewHTTPSender()
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	fault := &enqueueResponseFault{Store: s, fail: true}
	service, err := actiondelivery.New(fault, pendingGate{}, actionResolver{}, sender, locker, time.Now)
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
	p := makeProcessor(service)
	e := storetest.Event("intent-tenant", "opening", "fp", "warning")
	e.EventSourceVersion = 4
	original, err := business.CreateEvent(t.Context(), e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.ProcessEvent(t.Context(), original.StoredEvent); err == nil {
		t.Fatal("lost enqueue response hidden")
	}
	savedEvent, err := business.GetEvent(t.Context(), e.BKTenantID, e.EventID)
	if err != nil || savedEvent.Processing.Plan == nil {
		t.Fatal("frozen plan lost", err)
	}
	id := savedEvent.Processing.Plan.Mutations[0].Alert.AlertID
	current, err := business.GetAlertCurrent(t.Context(), e.BKTenantID, id)
	if err != nil || current.Alert.ActionPending == nil || current.Alert.Revision != 1 {
		t.Fatal("action intent not atomic", err)
	}
	// 关闭重建任务连接后，只凭 Alert 上的持久意图补齐入队，不重跑 Enrich 或策略。
	restored := reopen()
	again, err := actiondelivery.New(restored, pendingGate{}, actionResolver{}, sender, locker, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	restarted := makeProcessor(again)
	drained, err := restarted.FinishActionDelivery(t.Context(), e.BKTenantID, id)
	if err != nil || drained.Alert.ActionPending != nil || drained.Alert.Revision != 1 {
		t.Fatal("restart did not finish enqueue", err)
	}
	tasks, err := restored.List(t.Context(), actiondelivery.Query{TenantID: e.BKTenantID, Limit: 16})
	if err != nil || len(tasks) != 1 || tasks[0].Task.Request.Revision != 1 || tasks[0].Task.SourceVersion != 4 || tasks[0].Task.Request.Cause.ID != e.EventID {
		t.Fatal("replay duplicated/changed action", err)
	}
	if _, err = restarted.ProcessEvent(t.Context(), savedEvent); err != nil {
		t.Fatal("plan replay failed", err)
	}
	cmd := lifecycle.CloseAlertCommand{OperationID: "close-without-event", BKTenantID: e.BKTenantID, AlertID: id, OperatorKind: domain.OperatorKindUser, OperatorID: "tester", Reason: "manual", EffectiveAt: time.Now().UTC().Add(time.Second)}
	fault.fail = true
	if _, err = p.CloseAlert(t.Context(), cmd); err == nil {
		t.Fatal("terminal enqueue response loss hidden")
	}
	current, err = business.GetAlertCurrent(t.Context(), e.BKTenantID, id)
	if err != nil || current.Alert.Status != domain.AlertStatusClosed || current.Alert.ActionPending == nil {
		t.Fatal("terminal intent lost", err)
	}
	work := business.Repository.(store.ActionWorkStore)
	deadline := time.Now().Add(3 * time.Second)
	for {
		page, e := work.ListActionWork(t.Context(), store.ActionWorkCursor{}, 16)
		if e != nil {
			t.Fatal(e)
		}
		if len(page.Items) == 1 && page.Items[0].Alert.ActionPending.Action == "close" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal action not discoverable")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = restarted.FinishActionDelivery(t.Context(), e.BKTenantID, id); err != nil {
		t.Fatal(err)
	}
	done, err := restarted.CloseAlert(t.Context(), cmd)
	if err != nil || !done.AlreadyClosed {
		t.Fatal(err)
	}
	tasks, err = restored.List(t.Context(), actiondelivery.Query{TenantID: e.BKTenantID, Limit: 16})
	if err != nil || len(tasks) != 2 {
		t.Fatal("expected only firing and close", len(tasks), err)
	}
	head, err := restored.OldestUnsettled(t.Context(), e.BKTenantID, id, "kac")
	if err != nil || head.Task.Request.Revision != 1 {
		t.Fatal("restart changed action ordering", err)
	}
	t.Log("actual Lifecycle committed/recovered firing and manual-close intents; two immutable tasks, no action HTTP before visible projection")
}

func TestElasticsearchLifecycleActionIntent(t *testing.T) {
	actionStoreFixture(t, "elasticsearch", func(s *Store, reopen func() *Store, _ config.StorageConfig, d string) {
		runLifecycleActionIntent(t, s, reopen, d)
	})
}

func TestMySQLLifecycleActionIntent(t *testing.T) {
	actionStoreFixture(t, "mysql", func(s *Store, reopen func() *Store, _ config.StorageConfig, d string) {
		runLifecycleActionIntent(t, s, reopen, d)
	})
}
