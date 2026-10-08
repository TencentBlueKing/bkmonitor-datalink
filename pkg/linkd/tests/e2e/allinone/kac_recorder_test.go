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
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"linkd/internal/actiondelivery"
	actionstore "linkd/internal/actiondelivery/storage"
	"linkd/internal/config"
	controlapi "linkd/internal/controlplane/api"
	"linkd/internal/controlplane/taskstate"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle"
	"linkd/internal/runtimeconfig"
	storeassembly "linkd/internal/store/assembly"
	"linkd/internal/store/storetest"
	"linkd/internal/taskdispatch"
)

// TestAllInOneKACWorkerRecorderE2E 不配置接收端凭据，确保补扫/投递关闭时 Worker 和关闭 API 自己完成入队。
// 目标绑定仍由显式 fixture Processor 注入；后续升级来自真实 Kafka/Cleaner/Worker，不修改绑定规则。
func TestAllInOneKACWorkerRecorderE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1", e2eEnabledEnv)
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "linkd")
	buildLinkd(t, t.Context(), root, binary)
	for _, backend := range []string{"elasticsearch", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			h := startPolicyHarness(t, root, binary, backend)
			cfg, err := config.Load(h.configPath, config.Overrides{})
			if err != nil {
				t.Fatal(err)
			}
			var source eventsource.Record
			h.call(http.MethodGet, "/api/v1/event-sources/policy-a", nil, &source)
			var hooks []config.HookConfig
			var legacy config.HookConfig
			for _, hook := range source.Spec.Hooks {
				if hook.Type != config.HookTypeKAC {
					hooks = append(hooks, hook)
				} else {
					legacy = hook
				}
			}
			if legacy.Name == "" {
				t.Fatal("missing legacy KAC hook fixture")
			}
			source.Spec.Hooks = hooks
			h.call(http.MethodPut, "/api/v1/event-sources/policy-a", controlapi.Mutation{Expected: source.Revision, Spec: source.Spec}, &source)
			openingVersion := source.Published
			waitSource := func() {
				h.until("recorder source release running", func() bool {
					var state taskdispatch.State
					h.call(http.MethodGet, "/api/v1/runtime", nil, &state)
					for _, task := range state.Tasks {
						if task.Source == "policy-a" && task.Role == "lifecycle" && task.Version == source.Published && task.Phase == "running" {
							return true
						}
					}
					return false
				})
			}
			waitSource()
			business, err := storeassembly.OpenExisting(h.ctx, *cfg.Storage, 4)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = business.Close() })
			processor, err := lifecycle.NewProcessor(business.Repository, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, runtimeconfig.NewSeverity(cfg.Severity), lifecycle.SystemClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithInitialProjectionTargets(map[string]bool{"kac": true}), lifecycle.WithActionRecorder(interruptedKACRecorder{}))
			if err != nil {
				t.Fatal(err)
			}
			event := storetest.Event("delivery", "", "worker-recorder", "warning")
			event.EventSourceID = "policy-a"
			event.EventSourceVersion = openingVersion
			at := time.Now().UTC().Truncate(time.Second)
			event.OccurredAt, event.ProducedAt, event.ReceivedAt, event.CreateAt = at, at, at, at
			event.EventID, err = domain.GenerateEventID(event.BKTenantID, event.EventSourceID, "worker-opening", at)
			if err != nil {
				t.Fatal(err)
			}
			created, err := business.Repository.CreateEvent(h.ctx, event)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := processor.ProcessEvent(h.ctx, created.StoredEvent); err == nil {
				t.Fatal("missing interrupted enqueue")
			}
			saved, err := business.Repository.GetEvent(h.ctx, event.BKTenantID, event.EventID)
			if err != nil || saved.Processing.Plan == nil || len(saved.Processing.Plan.Mutations) != 1 {
				t.Fatal("missing opening plan", err)
			}
			id := saved.Processing.Plan.Mutations[0].Alert.AlertID
			tasks, err := actionstore.OpenExisting(h.ctx, *cfg.Storage, cfg.Dispatch.WithDefaults().Deployment)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tasks.Close() })
			assertPending := func(count int) {
				t.Helper()
				rows, err := tasks.List(h.ctx, actiondelivery.Query{TenantID: "delivery", Limit: 4})
				if err != nil || len(rows) != count {
					t.Fatal("immediate action count", len(rows), count, err)
				}
				seen := map[int64]bool{}
				for _, row := range rows {
					if row.Task.Request.AlertID != id || row.Task.SourceVersion != openingVersion || row.Task.Progress.State != "pending" || row.Task.Progress.Attempts != 0 || row.Task.Progress.Receipt != nil {
						t.Fatal("recorder changed target or performed delivery")
					}
					seen[row.Task.Request.Revision] = true
				}
				for revision := 1; revision <= count; revision++ {
					if !seen[int64(revision)] {
						t.Fatal("lost original action order", revision)
					}
				}
			}
			assertPending(0)
			// 初始 fixture 使用 NoopRecentAlertCache，先等它对真实 Worker 的活动查询可见。
			// 正式 Worker 自身的创建路径使用共享近期缓存，不依赖此测试等待。
			h.alert(id, func(a domain.Alert) bool { return a.ActionPending != nil && a.Severity == "warning" })
			// 来源切换到旧 KAC Hook 后，已有可靠动作绑定仍必须阻止旧通道重复处置。
			// 两个 Release 都不声明 kac_targets；此处只验证保存的绑定，不选择未来迁移规则。
			source.Spec.Hooks = append(source.Spec.Hooks, legacy)
			h.call(http.MethodPut, "/api/v1/event-sources/policy-a", controlapi.Mutation{Expected: source.Revision, Spec: source.Spec}, &source)
			waitSource()
			upgraded := h.send("delivery", "policy-a", "worker-upgrade", "worker-recorder", "upgraded", "host", "critical", "triggered")
			if onlyAlert(t, upgraded) != id {
				t.Fatal("upgrade created a different alert")
			}
			a := h.alert(id, func(a domain.Alert) bool {
				return a.Severity == "critical" && a.ActionPending == nil && a.Revision == 2
			})
			if a.Title != event.Title {
				t.Fatal("upgrade refreshed opening snapshot")
			}
			assertPending(2)
			var closed struct {
				Alert domain.Alert `json:"alert"`
			}
			h.call(http.MethodPost, "/api/v1/alerts/"+id+"/close", map[string]any{"bk_tenant_id": "delivery", "operation_id": "worker-recorder-close", "operator_id": "e2e", "reason": "验证无后台补扫的即时入队", "effective_at": time.Now().UTC()}, &closed)
			if closed.Alert.ActionPending != nil || closed.Alert.Revision != 3 || closed.Alert.Status != domain.AlertStatusClosed {
				t.Fatal("close did not finish action intent")
			}
			assertPending(3)
			var catalog taskstate.Snapshot
			h.call(http.MethodGet, "/api/v1/control-plane/tasks", nil, &catalog)
			phases := 0
			for _, task := range catalog.Tasks {
				if task.ID == "projection-producer" || task.ID == "projection-delivery" || task.ID == "action-enqueue" || task.ID == "action-delivery" {
					if task.Enabled || task.Active {
						t.Fatal("background loop invalidated immediate recorder proof")
					}
					phases++
				}
			}
			if phases != 4 {
				t.Fatal("missing delivery task activation evidence")
			}
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
			outputs := consumeOutputs(h.ctx, t, h.broker, h.names, 2)
			seen := map[int64]domain.AlertStatus{}
			for _, output := range outputs {
				if output.AlertID != id || output.Alert.Projection.Targets["kac"].SourceVersion != openingVersion {
					t.Fatal("state output lost the bound alert")
				}
				seen[output.Alert.Revision] = output.Alert.Status
			}
			if seen[2] != domain.AlertStatusActive || seen[3] != domain.AlertStatusClosed {
				t.Fatal("reliable action binding suppressed ordinary state output")
			}
		})
	}
}
