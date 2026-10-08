// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package merge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/onemodel"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	policyruntime "linkd/internal/policy/runtime"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type executorTargets struct{}

func (executorTargets) ResolveScope(_ context.Context, tenant, space string) (onemodel.TargetScope, error) {
	return onemodel.TargetScope{TenantID: tenant, SpaceCode: space, BusinessIDs: []int64{2}}, nil
}

func (executorTargets) Resolve(context.Context, string, string, onemodel.TargetDescriptor) (onemodel.TargetResult, error) {
	return onemodel.TargetResult{}, fmt.Errorf("unexpected target selection")
}

// 真实 Redis 覆盖 Worker 入窗、独立裁决、内部 Mailbox、关系准入和父终态；业务仓储使用 Memory。
func TestRedisExecutorRunsWorkerMergeThroughRealParentAndRelationEnd(t *testing.T) {
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("set LINKD_TEST_REDIS_ADDRESS")
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), ContextTimeoutEnabled: true, MaxRetries: 0})
	t.Cleanup(func() { _ = client.Close() })
	deployment := fmt.Sprintf("merge-executor-%d-%d", os.Getpid(), time.Now().UnixNano())
	prefix := "linkd-merge-executor-test:" + deployment
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, pattern := range []string{prefix + "*", "linkd:policies:" + hash("deployment", deployment) + "*"} {
			var cursor uint64
			for {
				keys, next, err := client.Scan(ctx, cursor, pattern, 100).Result()
				if err != nil {
					t.Error(err)
					break
				}
				if len(keys) > 0 {
					if err := client.Del(ctx, keys...).Err(); err != nil {
						t.Error(err)
					}
				}
				cursor = next
				if cursor == 0 {
					break
				}
			}
		}
	})
	state, err := redisstate.New(client, deployment)
	if err != nil {
		t.Fatal(err)
	}
	lc := config.LifecycleConfig{}.WithDefaults()
	lc.Mailbox.KeyPrefix = prefix + ":mailbox"
	lc.Signal.Stream = prefix + ":signals"
	inbox, err := mailbox.NewStore(client, lc.MailboxConfig())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _, source, level := renderFixture(t)
	policies := executorPolicies{fixture.Policy}
	catalog := policy.NewCatalog(policies)
	loader := &policyruntime.Suppressor{Catalog: catalog, Releases: policies, Targets: executorTargets{}}
	repo := memory.New()
	journal, _ := newJournal(t)
	action := &relationHook{}
	p, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "action", Purpose: "action", Hook: action}}, config.DefaultSeverityConfig(), journalClock{at: fixture.FrozenAt}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithPolicySnapshotter(policyruntime.Snapshotter{Catalog: catalog}), lifecycle.WithMergeEvaluator(&policyruntime.Merger{Loader: loader, State: state}), lifecycle.WithMergeRelations(journal))
	if err != nil {
		t.Fatal(err)
	}
	engine := &Executor{Journal: journal, Publisher: &Publisher{Journal: journal, Events: repo, Mailboxes: inbox}, Judge: &policyruntime.MergeJudge{Loader: loader, Windows: state, CurrentAlert: repo.GetAlert}, Windows: state, Policies: policies, Operations: p, CurrentAlert: repo.GetAlert, Event: repo.GetEvent, Source: func(context.Context) (eventsource.Release, error) { return source, nil }, Severity: func() runtimeconfig.Snapshot { return level }}
	children := []string{}
	for _, name := range []string{"a", "b"} {
		event := storetest.Event(fixture.TenantID, "event-"+name, "fp-"+name, "warning")
		event.EventSourceID = "source-" + name
		event.ExtraData["bk_biz_id"] = json.RawMessage("2")
		event.ExtraData["meta_info"] = json.RawMessage(`{"v":false}`)
		created, err := repo.CreateEvent(t.Context(), event)
		if err != nil {
			t.Fatal(err)
		}
		result, err := p.ProcessEvent(t.Context(), created.StoredEvent)
		if err != nil || len(result.AlertIDs) != 1 {
			t.Fatal("worker did not create waiting member", err)
		}
		children = append(children, result.AlertIDs[0])
	}
	page, err := repo.ListMergeWork(t.Context(), store.MergeWorkCursor{}, 16)
	if err != nil || len(page.Items) != 2 || page.Items[0].WindowID != page.Items[1].WindowID || len(action.inputs) != 0 {
		t.Fatal("worker merge entry did not block actions", err)
	}
	if err := engine.CheckWork(t.Context(), page.Items[0], fixture.FrozenAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	d, err := journal.GetByWindow(t.Context(), fixture.TenantID, page.Items[0].WindowID)
	if err != nil {
		t.Fatal(err)
	}
	for range 40 {
		d, err = journal.Get(t.Context(), d.Decision.TenantID, d.Decision.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Decision.Progress.WindowFinished {
			break
		}
		if d.Decision.Progress.Phase == "waiting_parent" {
			event, err := repo.GetEvent(t.Context(), fixture.TenantID, d.Decision.Progress.ParentEvent.EventID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.ProcessEvent(t.Context(), event); err != nil {
				t.Fatal(err)
			}
		}
		if err := engine.StepDecision(t.Context(), d.Decision.TenantID, d.Decision.ID, fixture.FrozenAt.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if !d.Decision.Progress.WindowFinished || d.Decision.Progress.ParentAlertID == "" || len(action.inputs) != 1 {
		t.Fatal("merge did not finish exactly one parent action")
	}
	parent := action.inputs[0].Alert
	if parent.EventSourceID != domain.BuiltinMergeEventSourceID || parent.Merge.Role != "aggregate" || !parent.Merge.RelationsReady {
		t.Fatal("parent bypassed real lifecycle/readiness")
	}
	hints, err := state.ListMergeDue(t.Context(), fixture.TenantID, fixture.Deadline, 32)
	if err != nil || len(hints) != 0 {
		t.Fatal("completed window hint leaked", err)
	}
	if _, err := p.CloseAlert(t.Context(), lifecycle.CloseAlertCommand{OperationID: "manual-parent-close", BKTenantID: fixture.TenantID, AlertID: parent.AlertID, OperatorKind: domain.OperatorKindUser, OperatorID: "tester", Reason: "manual", EffectiveAt: fixture.FrozenAt.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	actions := len(action.inputs)
	for range 8 {
		if err := engine.CheckRelation(t.Context(), fixture.TenantID, d.Decision.ID, fixture.FrozenAt.Add(4*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range children {
		a, err := repo.GetAlert(t.Context(), fixture.TenantID, id)
		if err != nil || a.Alert.Status != domain.AlertStatusActive || a.Alert.Merge.Blocking() || a.Alert.Admission.AdmittedAt != nil {
			t.Fatal("ending parent changed member lifecycle", err)
		}
	}
	if len(action.inputs) != actions {
		t.Fatal("relation ending sent child action")
	}
}
