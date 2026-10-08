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
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"linkd/internal/actiondelivery"
	actionstore "linkd/internal/actiondelivery/storage"
	"linkd/internal/config"
	controlapi "linkd/internal/controlplane/api"
	"linkd/internal/controlplane/taskstate"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle"
	"linkd/internal/projection"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store"
	storeassembly "linkd/internal/store/assembly"
	"linkd/internal/store/storetest"
)

type interruptedKACRecorder struct{}

func (interruptedKACRecorder) RecordAction(context.Context, domain.Alert, domain.AlertActionIntent) error {
	return errors.New("fixture: interrupted before action enqueue")
}

type kacDeliveryReceiver struct {
	t                  *testing.T
	allowProjection    atomic.Bool
	allowAction        atomic.Bool
	projectionAttempts atomic.Int64
	actionAttempts     atomic.Int64
	wrongRoute         atomic.Int64
	mu                 sync.Mutex
	projections        map[string]projection.Receipt
	projectionAlerts   map[string]domain.Alert
	actions            map[string]actiondelivery.Receipt
}

func (s *kacDeliveryReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("Internal-Token") != "Bearer e2e-delivery-secret" {
		s.t.Error("invalid receiver request/auth")
		w.WriteHeader(401)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/original/action":
		s.actionAttempts.Add(1)
		var q actiondelivery.Request
		if json.NewDecoder(r.Body).Decode(&q) != nil || q.Validate() != nil || r.Header.Get("X-Bk-Tenant-Id") != q.TenantID {
			s.t.Error("invalid action request")
			w.WriteHeader(400)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		proof, exists := s.projections[q.AlertID]
		if !exists || actiondelivery.ValidateProjection(q, proof) != nil {
			s.t.Error("action bypassed projection")
			w.WriteHeader(409)
			return
		}
		if !s.allowAction.Load() {
			w.WriteHeader(401)
			return
		}
		ack, exists := s.actions[q.ActionID]
		if !exists {
			ack = actiondelivery.Receipt{SchemaVersion: actiondelivery.SchemaVersion, TenantID: q.TenantID, TargetID: q.TargetID, AlertID: q.AlertID, AlarmID: q.AlarmID, ActionID: q.ActionID, RequestHash: q.Hash(), Outcome: "accepted", AppliedRevision: proof.AppliedRevision, AppliedStatus: proof.AppliedStatus, SearchVisible: true, AcceptanceID: "accepted-" + q.ActionID}
			s.actions[q.ActionID] = ack
		} else if ack.RequestHash != q.Hash() {
			s.t.Error("same action changed frozen payload")
			w.WriteHeader(409)
			return
		}
		_ = json.NewEncoder(w).Encode(ack)
	default:
		s.wrongRoute.Add(1)
		w.WriteHeader(404)
	}
}

// TestAllInOneKACDeliveryE2E 使用真实生产进程、存储、Redis、来源 API 和 HTTP 发送。
// 初始目标绑定由显式测试 Processor 提供；不替代尚待确认的来源迁移/Worker 自动绑定。
// 接收端为进程内协议模拟，幂等账本不代表实际 KAC 受理实现。
func TestAllInOneKACDeliveryE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1", e2eEnabledEnv)
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "linkd")
	buildLinkd(t, t.Context(), root, binary)
	consoleDelivery := os.Getenv("LINKD_E2E_CONSOLE_DELIVERY") == "1"
	if consoleDelivery {
		buildDeliveryConsole(t, root)
	}
	for _, backend := range []string{"elasticsearch", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			receiver := &kacDeliveryReceiver{t: t, projections: map[string]projection.Receipt{}, actions: map[string]actiondelivery.Receipt{}}
			remote := httptest.NewServer(receiver)
			t.Cleanup(remote.Close)
			plugins := kacESPlugin(t, receiver, remote.URL)
			var metricsListen, prometheusURL, instance string
			if consoleDelivery {
				metricsListen, prometheusURL, instance = startDeliveryPrometheus(t, backend)
			}
			h := startPolicyHarnessWithTimeout(t, root, binary, backend, 8*time.Minute, func(path string) {
				//nolint:gosec // G304: 路径仅来自测试创建的配置文件。
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var doc map[string]any
				if err := yaml.Unmarshal(raw, &doc); err != nil {
					t.Fatal(err)
				}
				doc["plugins"] = plugins
				if consoleDelivery {
					doc["telemetry"] = config.TelemetryConfig{Metrics: config.TelemetryMetricsConfig{Exporter: config.TelemetryExporterPrometheus, Prometheus: config.TelemetryPrometheusConfig{ListenAddress: metricsListen}}}
				}
				raw, err = yaml.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			})
			cfg, err := config.Load(h.configPath, config.Overrides{})
			if err != nil {
				t.Fatal(err)
			}
			spec := config.EventSource{EventSourceID: "source", RelatedTenantID: "delivery", Enabled: false,
				Storage: config.EventSourceStorageConfig{Type: "kafka", Kafka: config.KafkaStorageConfig{Brokers: []string{h.broker}, Topic: h.names.RawTopic + "-unused", ConsumerGroup: "unused"}},
			}.WithDefaults()
			var source eventsource.Record
			h.call(http.MethodPut, "/api/v1/event-sources/source", controlapi.Mutation{Spec: spec}, &source)
			business, err := storeassembly.OpenExisting(h.ctx, *cfg.Storage, 4)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = business.Close() })
			processor, err := lifecycle.NewProcessor(business.Repository, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, runtimeconfig.NewSeverity(cfg.Severity), lifecycle.SystemClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithInitialProjectionTargets(map[string]bool{"kac": true}), lifecycle.WithActionRecorder(interruptedKACRecorder{}))
			if err != nil {
				t.Fatal(err)
			}
			event := storetest.Event("delivery", "", "delivery-fp", "warning")
			at := time.Now().UTC().Truncate(time.Second)
			event.OccurredAt, event.ProducedAt, event.ReceivedAt, event.CreateAt = at, at, at, at
			event.EventID, err = domain.GenerateEventID("delivery", "source", "opening-delivery", at)
			if err != nil {
				t.Fatal(err)
			}
			created, err := business.Repository.CreateEvent(h.ctx, event)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := processor.ProcessEvent(h.ctx, created.StoredEvent); err == nil {
				t.Fatal("fixture enqueue interruption missing")
			}
			saved, err := business.Repository.GetEvent(h.ctx, "delivery", event.EventID)
			if err != nil || saved.Processing.Plan == nil || len(saved.Processing.Plan.Mutations) != 1 {
				t.Fatal("opening plan missing", err)
			}
			alertID := saved.Processing.Plan.Mutations[0].Alert.AlertID
			current := business.Repository.GetAlert
			if fast, ok := business.Repository.(store.LifecycleAlertStore); ok {
				current = fast.GetAlertCurrent
			}
			actions, err := actionstore.Open(h.ctx, *cfg.Storage, cfg.Dispatch.WithDefaults().Deployment)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = actions.Close() })
			var first actiondelivery.StoredTask
			h.until("automatic enqueue and projection retry", func() bool {
				row, err := current(h.ctx, "delivery", alertID)
				if err != nil {
					t.Fatal(err)
				}
				items, err := actions.List(h.ctx, actiondelivery.Query{TenantID: "delivery", Limit: 4})
				if err != nil {
					t.Fatal(err)
				}
				if len(items) == 1 {
					first = items[0]
				}
				return row.Alert.ActionPending == nil && len(items) == 1 && receiver.projectionAttempts.Load() > 0
			})
			if receiver.actionAttempts.Load() != 0 {
				t.Fatal("action sent before projection visibility")
			}
			h.call(http.MethodPut, "/api/v1/event-sources/source", controlapi.Mutation{Expected: source.Revision, Spec: spec}, &source)
			h.call(http.MethodPut, "/api/v1/event-sources/source", controlapi.Mutation{Expected: source.Revision, Spec: spec}, &source)
			receiver.allowProjection.Store(true)
			h.until("automatic action failure persisted", func() bool {
				first, err = actions.Get(h.ctx, "delivery", first.Task.ID)
				if err != nil {
					t.Fatal(err)
				}
				return first.Task.Progress.State == "failed"
			})
			if first.Task.Progress.ErrorCode != "remote_unauthorized" || first.Task.SourceVersion != 1 {
				t.Fatal("wrong original action route/failure", first.Task.Progress.ErrorCode)
			}
			originalHash := first.Task.Request.Hash()
			receiver.allowAction.Store(true)
			h.call(http.MethodPost, "/api/v1/action-deliveries/"+first.Task.ID+"/retry", map[string]any{"bk_tenant_id": "delivery", "operation_id": "retry-original", "expected_version": first.Version, "operator_id": "e2e", "reason": "验证生产循环恢复原动作"}, &map[string]any{})
			h.until("automatic action retry accepted", func() bool {
				first, err = actions.Get(h.ctx, "delivery", first.Task.ID)
				if err != nil {
					t.Fatal(err)
				}
				return first.Task.Progress.State == "succeeded"
			})
			if first.Task.Request.Hash() != originalHash || first.Task.Progress.Generation != 2 || first.Task.Progress.LastRetry == nil {
				t.Fatal("manual retry changed action snapshot/audit")
			}
			var closed struct {
				Alert         domain.Alert `json:"alert"`
				AlreadyClosed bool         `json:"already_closed"`
			}
			closeCommand := map[string]any{"bk_tenant_id": "delivery", "operation_id": "delivery-close", "operator_id": "e2e", "reason": "验证正式关闭立即入队", "effective_at": time.Now().UTC()}
			h.call(http.MethodPost, "/api/v1/alerts/"+alertID+"/close", closeCommand, &closed)
			if closed.Alert.Status != domain.AlertStatusClosed || closed.Alert.ActionPending != nil || closed.Alert.Revision != 2 {
				t.Fatal("close returned before durable action enqueue")
			}
			// API 返回时立即读取任务和排序可见性，不用后续后台补扫替代关闭调用本身的入队保证。
			rows, err := actions.List(h.ctx, actiondelivery.Query{TenantID: "delivery", Limit: 4})
			if err != nil || len(rows) != 2 {
				t.Fatal("terminal action not visible when close returned", err)
			}
			h.call(http.MethodPost, "/api/v1/alerts/"+alertID+"/close", closeCommand, &closed)
			if !closed.AlreadyClosed || closed.Alert.Revision != 2 {
				t.Fatal("same close command was not idempotent")
			}
			h.until("terminal projection and action automatically accepted", func() bool {
				items, err := actions.List(h.ctx, actiondelivery.Query{TenantID: "delivery", Limit: 4})
				if err != nil {
					t.Fatal(err)
				}
				if len(items) != 2 {
					return false
				}
				for _, item := range items {
					if item.Task.Progress.State != "succeeded" {
						return false
					}
				}
				row, err := current(h.ctx, "delivery", alertID)
				if err != nil {
					t.Fatal(err)
				}
				return row.Alert.ActionPending == nil && row.Alert.Projection.Targets["kac"].SyncedRevision == 2
			})
			h.until("production loop task observations", func() bool {
				var snapshot taskstate.Snapshot
				h.call(http.MethodGet, "/api/v1/control-plane/tasks", nil, &snapshot)
				matched := 0
				for _, task := range snapshot.Tasks {
					if task.ID == "projection-producer" || task.ID == "projection-delivery" || task.ID == "action-enqueue" || task.ID == "action-delivery" {
						if !task.Enabled || !task.Active || task.Execution.Succeeded == 0 {
							return false
						}
						matched++
					}
				}
				raw, _ := json.Marshal(snapshot)
				if strings.Contains(string(raw), "e2e-delivery-secret") || strings.Contains(string(raw), remote.URL) {
					t.Fatal("task directory exposed credential/address")
				}
				return matched == 4
			})
			if consoleDelivery {
				before := receiver.actionAttempts.Load()
				h.verifyConsoleDelivery(root, backend, prometheusURL, instance, alertID, first.Task.ID)
				if receiver.actionAttempts.Load() != before {
					t.Fatal("Console observation caused action redelivery")
				}
			}
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			if receiver.wrongRoute.Load() != 0 {
				t.Fatal("old task followed changed source")
			}
			receiver.mu.Lock()
			defer receiver.mu.Unlock()
			if len(receiver.actions) != 2 || len(receiver.projections) != 1 || receiver.projections[alertID].AppliedStatus != domain.AlertStatusClosed {
				t.Fatal("duplicate actions/projections or lost terminal state")
			}
		})
	}
}
