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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/projection"
	"linkd/internal/projection/redislock"
	"linkd/internal/store"
	es "linkd/internal/store/elasticsearch"
	mysqlstore "linkd/internal/store/mysql"
	"linkd/internal/store/storetest"
	"linkd/internal/testkit/projectionfixture"
)

type deliveryAlerts struct {
	store.Repository
	failACK  bool
	blockACK atomic.Bool
}

func (r *deliveryAlerts) GetAlertCurrent(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	if fast, ok := r.Repository.(interface {
		GetAlertCurrent(context.Context, string, string) (store.StoredAlert, error)
	}); ok {
		return fast.GetAlertCurrent(ctx, tenant, id)
	}
	return r.GetAlert(ctx, tenant, id)
}

func (r *deliveryAlerts) CompareAndSetAlert(ctx context.Context, tenant, id string, version store.VersionToken, a domain.Alert) (store.StoredAlert, error) {
	if r.blockACK.Load() {
		return store.StoredAlert{}, errors.New("injected ACK hold")
	}
	if r.failACK {
		r.failACK = false
		return store.StoredAlert{}, errors.New("injected local ACK interruption")
	}
	return r.Repository.CompareAndSetAlert(ctx, tenant, id, version, a)
}

type destinationResolver struct{ endpoint string }

func (r destinationResolver) ResolveProjection(_ context.Context, tenant, source string, version int64, target string) (projection.Destination, error) {
	if tenant != "delivery" || source != "source" || version != 4 || target != "kac" {
		return projection.Destination{}, projection.ErrInvalid
	}
	return projection.Destination{TargetID: target}, nil
}

func deliveryRepository(t *testing.T, s *Store) *deliveryAlerts {
	t.Helper()
	if s.db != nil {
		repo, err := mysqlstore.New(s.db)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.EnsureSchema(t.Context()); err != nil {
			t.Fatal(err)
		}
		return &deliveryAlerts{Repository: repo}
	}
	router, err := es.NewStaticRouter(s.index + "-business")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := es.New(s.transport, router, es.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureSchema(t.Context(), router.SchemaConfig()); err != nil {
		t.Fatal(err)
	}
	for _, spec := range router.SchemaConfig().Templates() {
		if err := repo.EnsureIndex(t.Context(), spec.Name, spec.Entity); err != nil {
			t.Fatal(err)
		}
		if code, _, err := s.request(t.Context(), http.MethodPut, "/"+spec.Name+"/_settings", []byte(`{"index":{"refresh_interval":"1ms","number_of_replicas":0}}`)); err != nil || code != 200 {
			t.Fatal("test settings", code, err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, target := range router.Targets() {
			if code, _, err := s.request(ctx, http.MethodDelete, "/"+target, nil); err != nil || code != 200 {
				t.Error("cleanup own business index", code, err)
			}
		}
		for _, spec := range router.SchemaConfig().Templates() {
			if code, _, err := s.request(ctx, http.MethodDelete, "/_index_template/"+spec.Name, nil); err != nil || code != 200 {
				t.Error("cleanup own template", code, err)
			}
		}
	})
	return &deliveryAlerts{Repository: repo}
}

// runDeliveryContract 将真实任务仓储、真实 Alert CAS、真实 Redis lease 与模拟 HTTP 接收端组合。
// 来源解析使用固定测试引用，不代表生产来源目标配置已经装配。
func runDeliveryContract(t *testing.T, s *Store, reopen func() *Store) {
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("set LINKD_TEST_REDIS_ADDRESS for combined delivery contract")
	}
	alerts := deliveryRepository(t, s)
	a := storetest.Alert("delivery", "delivery-alert", "opening", "delivery-fp", "warning")
	a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 4, RequiredRevision: 1}}}
	if _, err := alerts.CreateAlert(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), ContextTimeoutEnabled: true, MaxRetries: -1, PoolSize: 4})
	defer func() { _ = client.Close() }()
	lock, err := redislock.New(client, s.namespace+"-delivery")
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	var stableID string
	var receiverMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Internal-Token") != "Bearer test-internal" {
			t.Error("authentication missing")
			w.WriteHeader(401)
			return
		}
		if requests.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		var q projection.Request
		if json.NewDecoder(r.Body).Decode(&q) != nil || q.Validate() != nil {
			t.Error("invalid wire request")
			w.WriteHeader(400)
			return
		}
		receiverMu.Lock()
		if stableID == "" {
			stableID = q.AlarmID
		} else if stableID != q.AlarmID {
			t.Error("terminal created another identity")
		}
		receiverMu.Unlock()
		var body struct{ Status domain.AlertStatus }
		if json.Unmarshal(q.Alert, &body) != nil {
			t.Error("invalid alert")
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
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	service, err := projection.New(s, alerts, destinationResolver{server.URL}, sender, lock, clock)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := service.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := service.Deliver(t.Context(), a.BKTenantID, pending.Task.ID)
	if err != nil || retry.Task.Progress.State != "retry" || retry.Task.Progress.ErrorCode != "remote_unavailable" || requests.Load() != 1 {
		t.Fatal("HTTP failure did not persist retry", err)
	}
	now = *retry.Task.Progress.DueAt
	alerts.failACK = true
	if _, err := service.Deliver(t.Context(), a.BKTenantID, pending.Task.ID); err == nil {
		t.Fatal("local ACK failure ignored")
	}
	restored := reopen()
	persisted, err := restored.Get(t.Context(), a.BKTenantID, pending.Task.ID)
	if err != nil || persisted.Task.Progress.State != "delivered" {
		t.Fatal("receipt not durable after reopen", err)
	}
	restarted, err := projection.New(restored, alerts, destinationResolver{server.URL}, sender, lock, clock)
	if err != nil {
		t.Fatal(err)
	}
	done, err := restarted.Deliver(t.Context(), a.BKTenantID, pending.Task.ID)
	if err != nil || done.Task.Progress.State != "succeeded" || requests.Load() != 2 {
		t.Fatal("restart resent confirmed HTTP", err)
	}
	current, err := alerts.GetAlertCurrent(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Alert.Revision != 1 || current.Alert.Projection.Targets["kac"].SyncedRevision != 1 {
		t.Fatal("ACK not persisted")
	}
	terminal := current.Alert.Clone()
	terminal.UpdateAt = terminal.UpdateAt.Add(time.Second)
	terminal.Status = domain.AlertStatusClosed
	terminal.EndAt = &terminal.UpdateAt
	terminal.EndType = domain.AlertEndTypeUser
	terminal.EndReason = "manual"
	if _, err := alerts.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, current.Version, terminal); err != nil {
		t.Fatal(err)
	}
	next, err := restarted.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac")
	if err != nil {
		t.Fatal(err)
	}
	if next.Task.ID == pending.Task.ID || next.Task.Request.Revision != 2 {
		t.Fatal("terminal task revision")
	}
	if _, err := restarted.Deliver(t.Context(), a.BKTenantID, next.Task.ID); err != nil {
		t.Fatal(err)
	}
	final, err := alerts.GetAlertCurrent(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil || final.Alert.Status != domain.AlertStatusClosed || final.Alert.Projection.Targets["kac"].SyncedRevision != 2 || requests.Load() != 3 {
		t.Fatal("terminal sync failed", err)
	}
	work, err := restored.List(t.Context(), projection.Query{TenantID: a.BKTenantID, WorkOnly: true, Limit: 16})
	if err != nil || len(work) != 0 {
		t.Fatal("completed delivery still pending", err)
	}
}
