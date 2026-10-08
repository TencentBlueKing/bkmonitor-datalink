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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	controlapi "linkd/internal/controlplane/api"
	"linkd/internal/domain"
	"linkd/internal/internaltoken"
	"linkd/internal/projection"
	"linkd/internal/projection/redislock"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
	"linkd/internal/telemetry"
	"linkd/internal/testkit/projectionfixture"
)

func projectionUntil(t *testing.T, condition string, ready func() bool) {
	t.Helper()
	timeout := time.NewTimer(20 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-timeout.C:
			t.Fatal("timeout: " + condition)
		case <-ticker.C:
		}
	}
}

func startProjectionRunners(t *testing.T, runners ...*projection.Runner) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, len(runners))
	for _, r := range runners {
		go func() { done <- r.Run(ctx) }()
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			for range runners {
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(5 * time.Second):
					t.Error("projection runner shutdown timeout")
				}
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// runAutonomousContract 使用真实任务/业务仓储和 Redis；接收端是明确的协议模拟，不代表 KAC 已接通。
func runAutonomousContract(t *testing.T, s *Store, reopen func() *Store) {
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("set LINKD_TEST_REDIS_ADDRESS for autonomous delivery contract")
	}
	alerts := deliveryRepository(t, s)
	work, ok := alerts.Repository.(store.ProjectionWorkStore)
	if !ok {
		t.Fatal("missing projection scan")
	}
	a := storetest.Alert("delivery", "runner-alert", "opening", "runner-fp", "warning")
	a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 4, RequiredRevision: 1}}}
	rejected := a.Clone()
	rejected.AlertID = "runner-rejected"
	rejected.Fingerprint = "rejected-fp"
	for _, v := range []domain.Alert{a, rejected} {
		if _, err := alerts.CreateAlert(t.Context(), v); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		if !t.Failed() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		rows, err := s.List(ctx, projection.Query{TenantID: "delivery", Limit: 16})
		if err != nil {
			t.Logf("diagnostic task read failed: %T", err)
			return
		}
		for _, v := range rows {
			t.Logf("diagnostic task alert=%s revision=%d state=%s attempts=%d due=%v lease=%v code=%s", v.Task.Request.AlertID, v.Task.Request.Revision, v.Task.Progress.State, v.Task.Progress.Attempts, v.Task.Progress.DueAt, v.Task.Progress.LeaseUntil, v.Task.Progress.ErrorCode)
		}
		for _, id := range []string{a.AlertID, rejected.AlertID} {
			row, err := alerts.GetAlertCurrent(ctx, "delivery", id)
			if err != nil {
				t.Logf("diagnostic alert read failed: %T", err)
				continue
			}
			t.Logf("diagnostic alert id=%s status=%s revision=%d projection=%+v", id, row.Alert.Status, row.Alert.Revision, row.Alert.Projection)
		}
	}()
	expected, err := projection.NewTask(a, "kac", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	bad, err := projection.NewTask(rejected, "kac", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), ContextTimeoutEnabled: true, MaxRetries: -1, PoolSize: 4})
	defer func() { _ = client.Close() }()
	locker, err := redislock.New(client, s.namespace+"-runner")
	if err != nil {
		t.Fatal(err)
	}
	var mainCalls, badCalls atomic.Int64
	var allowBad atomic.Bool
	var mu sync.Mutex
	stable := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Internal-Token") != "Bearer test-internal" {
			t.Error("missing authentication")
			w.WriteHeader(401)
			return
		}
		var q projection.Request
		if json.NewDecoder(r.Body).Decode(&q) != nil || q.Validate() != nil {
			t.Error("invalid wire request")
			w.WriteHeader(400)
			return
		}
		switch q.AlertID {
		case rejected.AlertID:
			badCalls.Add(1)
			if !allowBad.Load() {
				w.WriteHeader(401)
				return
			}
		case a.AlertID:
			if mainCalls.Add(1) == 1 {
				w.WriteHeader(503)
				return
			}
			mu.Lock()
			if stable == "" {
				stable = q.AlarmID
			} else if stable != q.AlarmID {
				t.Error("terminal identity changed")
			}
			mu.Unlock()
		default:
			t.Error("unexpected alert reached receiver")
			w.WriteHeader(400)
			return
		}
		var body struct {
			Status domain.AlertStatus `json:"status"`
		}
		if json.Unmarshal(q.Alert, &body) != nil {
			t.Error("invalid snapshot")
		}
		ack := projection.Receipt{SchemaVersion: projection.SchemaVersion, TenantID: q.TenantID, TargetID: q.TargetID, AlertID: q.AlertID, AlarmID: q.AlarmID, AppliedRevision: q.Revision, ContentHash: q.ContentHash, AppliedStatus: body.Status, SearchVisible: true, DocumentRef: "alarm-000001/" + q.AlarmID}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ack)
	}))
	defer server.Close()
	sender, err := projectionfixture.New(server.URL, "test-internal")
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	runtime, err := telemetry.Start(t.Context(), config.TelemetryConfig{Metrics: config.TelemetryMetricsConfig{Exporter: config.TelemetryExporterPrometheus, Prometheus: config.TelemetryPrometheusConfig{ListenAddress: "127.0.0.1:0"}}}, telemetry.RoleControlPlane, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	newRunner := func(tasks *Store) (*projection.Service, *projection.Runner) {
		service, err := projection.New(tasks, alerts, destinationResolver{server.URL}, sender, locker, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		runner, err := projection.NewRunner(work, tasks, service, runtime.ProjectionRunnerObserver(nil), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		return service, runner
	}
	alerts.blockACK.Store(true)
	_, one := newRunner(s)
	_, two := newRunner(reopen())
	stop := startProjectionRunners(t, one, two)
	projectionUntil(t, "durable receipt before local ACK", func() bool {
		row, err := s.Get(t.Context(), a.BKTenantID, expected.ID)
		return err == nil && row.Task.Progress.State == "delivered"
	})
	projectionUntil(t, "permanent failure isolated", func() bool {
		row, err := s.Get(t.Context(), a.BKTenantID, bad.ID)
		return err == nil && row.Task.Progress.State == "failed"
	})
	stop()
	if mainCalls.Load() != 2 || badCalls.Load() != 1 {
		t.Fatal("duplicate send or missing automatic retry", mainCalls.Load(), badCalls.Load())
	}
	current, err := alerts.GetAlertCurrent(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil || current.Alert.Projection.Targets["kac"].SyncedRevision != 0 {
		t.Fatal("ACK preceded durable receipt", err)
	}
	restored := reopen()
	row, err := restored.Get(t.Context(), a.BKTenantID, expected.ID)
	if err != nil || row.Task.Progress.Receipt == nil || row.Task.Progress.State != "delivered" {
		t.Fatal("receipt lost after reopen", err)
	}
	alerts.blockACK.Store(false)
	_, resumed := newRunner(restored)
	stopResumed := startProjectionRunners(t, resumed)
	defer stopResumed()
	projectionUntil(t, "restart only finishes local ACK", func() bool {
		row, err := restored.Get(t.Context(), a.BKTenantID, expected.ID)
		return err == nil && row.Task.Progress.State == "succeeded"
	})
	if mainCalls.Load() != 2 || badCalls.Load() != 1 {
		t.Fatal("restart resent confirmed or failed task")
	}
	current, err = alerts.GetAlertCurrent(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	terminal := current.Alert.Clone()
	terminal.Status = domain.AlertStatusClosed
	terminal.UpdateAt = time.Now().UTC()
	terminal.EndAt = &terminal.UpdateAt
	terminal.EndType = domain.AlertEndTypeUser
	terminal.EndReason = "manual"
	if _, err := alerts.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, current.Version, terminal); err != nil {
		t.Fatal(err)
	}
	projectionUntil(t, "automatic terminal projection", func() bool {
		row, err := alerts.GetAlertCurrent(t.Context(), a.BKTenantID, a.AlertID)
		return err == nil && row.Alert.Status == domain.AlertStatusClosed && row.Alert.Projection.Targets["kac"].SyncedRevision == 2
	})
	if mainCalls.Load() != 3 {
		t.Fatal("terminal was lost or sent twice", mainCalls.Load())
	}
	failed, err := restored.Get(t.Context(), a.BKTenantID, bad.ID)
	if err != nil {
		t.Fatal(err)
	}
	allowBad.Store(true)
	retrier, err := projection.NewRetrier(restored, locker, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer((&controlapi.API{ProjectionTasks: controlapi.NewProjectionTasks(restored, retrier), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "projection-test"}}}).Handler())
	defer api.Close()
	signer, err := internaltoken.New("projection-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Sign("test-operator")
	if err != nil {
		t.Fatal(err)
	}
	command := map[string]string{"bk_tenant_id": a.BKTenantID, "expected_version": failed.Version, "operation_id": "explicit-retry", "operator_id": "tester", "reason": "恢复测试投影"}
	call := func(method, path string, body any, want int) map[string]json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		q, err := http.NewRequestWithContext(t.Context(), method, api.URL+"/api/v1/projection-tasks/"+bad.ID+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		q.Header.Set(internaltoken.HeaderName, token)
		q.Header.Set("Content-Type", "application/json")
		response, err := api.Client().Do(q)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != want {
			t.Fatalf("management %s %s status=%d want=%d", method, path, response.StatusCode, want)
		}
		if want >= 400 {
			return nil
		}
		var data map[string]json.RawMessage
		if err := json.NewDecoder(response.Body).Decode(&data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	before := call("GET", "/snapshot?bk_tenant_id="+a.BKTenantID, nil, 200)
	call("GET", "?bk_tenant_id=other", nil, 404)
	accepted := call("POST", "/retry", command, 202)
	var audit projection.Progress
	if json.Unmarshal(accepted["progress"], &audit) != nil || audit.LastRetry == nil || audit.LastRetry.Command.ExpectedVersion != failed.Version || audit.LastRetry.Command.OperatorID != "tester" || audit.LastRetry.Command.Reason != command["reason"] {
		t.Fatal("management retry lost audit")
	}
	projectionUntil(t, "requested retry is delivered automatically", func() bool {
		row, err := restored.Get(t.Context(), a.BKTenantID, bad.ID)
		return err == nil && row.Task.Progress.State == "succeeded" && row.Task.Progress.Generation == 2 && row.Task.Progress.TotalAttempts == 2
	})
	// 精确 GET 可以先观察到成功 CAS；等工作索引也完成可见性确认，再取消运行器，
	// 避免测试在 refresh=wait_for 尚未返回时主动打断写方，随后把保守旧计数误判为残留任务。
	projectionUntil(t, "completed tasks leave searchable work index", func() bool {
		n, err := restored.CountWork(t.Context(), a.BKTenantID, projection.MaxPendingPerTenant)
		return err == nil && n == 0
	})
	stopResumed()
	// 同一恢复命令在已完成后重投只返回现状；不得新开一轮或重发远端。
	replayed := call("POST", "/retry", command, 200)
	var replay projection.Progress
	if json.Unmarshal(replayed["progress"], &replay) != nil || replay.Generation != 2 || replay.LastRetry == nil || *replay.LastRetry != *audit.LastRetry {
		t.Fatal("retry replay changed durable audit")
	}
	command["reason"] = "同一操作篡改原因"
	call("POST", "/retry", command, 409)
	after := call("GET", "/snapshot?bk_tenant_id="+a.BKTenantID, nil, 200)
	if !bytes.Equal(before["alert"], after["alert"]) || !bytes.Equal(before["content_hash"], after["content_hash"]) {
		t.Fatal("retry mutated frozen snapshot")
	}
	if mainCalls.Load() != 3 || badCalls.Load() != 2 {
		t.Fatal("unexpected receiver attempts", mainCalls.Load(), badCalls.Load())
	}
	if n, err := restored.CountWork(t.Context(), a.BKTenantID, projection.MaxPendingPerTenant); err != nil || n != 0 {
		t.Fatal("completed queue capacity not released", n, err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+runtime.PrometheusListenAddress()+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode != 200 {
		t.Fatal("projection metrics scrape", err)
	}
	body := string(raw)
	for _, phase := range []string{"projection-producer", "projection-delivery"} {
		for _, name := range []string{"linkd_projection_runner_active", "linkd_projection_runner_inflight"} {
			found := false
			for line := range strings.SplitSeq(body, "\n") {
				if strings.HasPrefix(line, name+"{") && strings.Contains(line, `linkd_task="`+phase+`"`) && strings.HasSuffix(line, " 0") {
					found = true
				}
			}
			if !found {
				t.Fatal("runner gauge did not clear", name, phase)
			}
		}
	}
	for _, name := range []string{"linkd_projection_runner_rounds_total", "linkd_projection_last_page_observed_at_seconds", "linkd_projection_work_observations_total"} {
		if !strings.Contains(body, name+"{") {
			t.Fatal("missing actual runner metrics", name)
		}
	}
	for _, private := range []string{"bk_tenant_id=", a.AlertID, "test-internal"} {
		if strings.Contains(body, private) {
			t.Fatal("private data in projection metrics")
		}
	}
	t.Log("actual projection loops exported bounded metrics; both phases returned to zero after stop")
}
