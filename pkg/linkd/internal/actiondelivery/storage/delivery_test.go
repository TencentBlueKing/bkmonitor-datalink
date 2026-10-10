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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/actiondelivery"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/internaltoken"
	"linkd/internal/projection"
	"linkd/internal/projection/redislock"
	projectionstore "linkd/internal/projection/storage"
	"linkd/internal/store"
	es "linkd/internal/store/elasticsearch"
	mysqlstore "linkd/internal/store/mysql"
	"linkd/internal/store/storetest"
	"linkd/internal/testkit/projectionfixture"
)

type actionAlerts struct{ store.Repository }

func (a *actionAlerts) GetAlertCurrent(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	if r, ok := a.Repository.(store.LifecycleAlertStore); ok {
		return r.GetAlertCurrent(ctx, tenant, id)
	}
	return a.GetAlert(ctx, tenant, id)
}

func deliveryRepository(t *testing.T, s *Store) *actionAlerts {
	t.Helper()
	if s.db != nil {
		repo, err := mysqlstore.New(s.db)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.EnsureSchema(t.Context()); err != nil {
			t.Fatal(err)
		}
		return &actionAlerts{Repository: repo}
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
	return &actionAlerts{Repository: repo}
}

type actionResolver struct{ endpoint string }

func (r actionResolver) ResolveAction(_ context.Context, _, source string, version int64, target string) (actiondelivery.Destination, error) {
	if source != "source" || version != 4 || target != "kac" {
		return actiondelivery.Destination{}, actiondelivery.ErrInvalid
	}
	return actiondelivery.Destination{Endpoint: r.endpoint + "/action", JWTSecretKey: "fixture-token"}, nil
}

func (r actionResolver) ResolveProjection(_ context.Context, _, source string, version int64, target string) (projection.Destination, error) {
	if source != "source" || version != 4 || target != "kac" {
		return projection.Destination{}, projection.ErrInvalid
	}
	return projection.Destination{TargetID: target}, nil
}

type failureAfterAcceptance struct {
	actiondelivery.Store
	fail bool
}

func (s *failureAfterAcceptance) Put(ctx context.Context, t actiondelivery.Task, v string) (actiondelivery.StoredTask, error) {
	if s.fail && t.Progress.State == "succeeded" {
		s.fail = false
		return actiondelivery.StoredTask{}, errors.New("injected local result failure")
	}
	return s.Store.Put(ctx, t, v)
}

type simulatedActionReceiver struct {
	mu                           sync.Mutex
	latest                       *projection.Request
	visible                      bool
	actions                      map[string]actiondelivery.Receipt
	actionCalls, projectionCalls int
}

func (r *simulatedActionReceiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// 投影夹具保留原 HTTP 协议；正式动作投递必须验证动态签发的 JWT。
	authenticated := req.Header.Get("Internal-Token") == "Bearer fixture-token"
	if req.URL.Path != "/projection" {
		verifier, err := internaltoken.New("fixture-token", nil)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		username, err := verifier.VerifyHeader(req.Header)
		authenticated = err == nil && username == "admin"
	}
	if req.Method != "POST" || !authenticated {
		w.WriteHeader(401)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if req.URL.Path == "/projection" {
		var q projection.Request
		if json.NewDecoder(req.Body).Decode(&q) != nil || q.Validate() != nil {
			w.WriteHeader(400)
			return
		}
		r.projectionCalls++
		if r.latest != nil && r.latest.Revision == q.Revision && r.latest.ContentHash != q.ContentHash {
			w.WriteHeader(409)
			return
		}
		if r.latest == nil || q.Revision > r.latest.Revision {
			copy := q.Clone()
			r.latest = &copy
		}
		actual := r.latest
		var a struct {
			Status domain.AlertStatus `json:"status"`
		}
		_ = json.Unmarshal(actual.Alert, &a)
		_ = json.NewEncoder(w).Encode(projection.Receipt{SchemaVersion: projection.SchemaVersion, TenantID: q.TenantID, TargetID: q.TargetID, AlertID: q.AlertID, AlarmID: q.AlarmID, AppliedRevision: actual.Revision, ContentHash: actual.ContentHash, AppliedStatus: a.Status, SearchVisible: r.visible, DocumentRef: "simulated-kac/fixed-document"})
		return
	}
	if req.URL.Path != "/action" {
		w.WriteHeader(404)
		return
	}
	var q actiondelivery.Request
	if json.NewDecoder(req.Body).Decode(&q) != nil || q.Validate() != nil {
		w.WriteHeader(400)
		return
	}
	r.actionCalls++
	if !r.visible || r.latest == nil || r.latest.Revision < q.Revision {
		w.WriteHeader(503)
		return
	}
	receipt := actiondelivery.Receipt{SchemaVersion: actiondelivery.SchemaVersion, TenantID: q.TenantID, TargetID: q.TargetID, AlertID: q.AlertID, AlarmID: q.AlarmID, ActionID: q.ActionID, RequestHash: q.Hash(), Outcome: "queued", TaskID: fmt.Sprintf("celery-%d", r.actionCalls)}
	if receipt.ValidateFor(q) != nil {
		w.WriteHeader(409)
		return
	}
	r.actions[q.ActionID] = receipt
	_ = json.NewEncoder(w).Encode(receipt)
}

func runCombinedActionDelivery(t *testing.T, s *Store, reopen func() *Store, cfg config.StorageConfig, deployment string) {
	t.Helper()
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("set LINKD_TEST_REDIS_ADDRESS")
	}
	business := deliveryRepository(t, s)
	proofDeployment := deployment + "-proof"
	proofs, e := projectionstore.Open(t.Context(), cfg, proofDeployment)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = proofs.Close() })
	if s.transport != nil {
		name := cfg.WithDefaults().Elasticsearch.IndexPrefix + "-projection-" + fmt.Sprintf("%x", sha256.Sum256([]byte(proofDeployment)))
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if code, _, e := s.request(ctx, http.MethodDelete, "/"+name, nil); e != nil || code != 200 {
				t.Error("cleanup owned projection fixture", code, e)
			}
		})
		if code, _, e := s.request(t.Context(), http.MethodPut, "/"+name+"/_settings", []byte(`{"index":{"refresh_interval":"1ms","number_of_replicas":0}}`)); e != nil || code != 200 {
			t.Fatal(code, e)
		}
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), ContextTimeoutEnabled: true, MaxRetries: -1, PoolSize: 4})
	defer func() { _ = client.Close() }()
	locker, e := redislock.New(client, deployment)
	if e != nil {
		t.Fatal(e)
	}
	receiver := &simulatedActionReceiver{actions: map[string]actiondelivery.Receipt{}}
	server := httptest.NewServer(receiver)
	defer server.Close()
	sender, e := actiondelivery.NewHTTPSender()
	if e != nil {
		t.Fatal(e)
	}
	defer sender.Close()
	projectionSender, e := projectionfixture.New(server.URL+"/projection", "fixture-token")
	if e != nil {
		t.Fatal(e)
	}
	defer projectionSender.Close()
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	resolver := actionResolver{endpoint: server.URL}
	p, e := projection.New(proofs, business, resolver, projectionSender, locker, clock)
	if e != nil {
		t.Fatal(e)
	}
	gate, e := actiondelivery.NewProjectionGate(business, proofs)
	if e != nil {
		t.Fatal(e)
	}
	fault := &failureAfterAcceptance{Store: s}
	svc, e := actiondelivery.New(fault, gate, resolver, sender, locker, clock)
	if e != nil {
		t.Fatal(e)
	}
	a := storetest.Alert("delivery", "shared-alert", "opening", "fp", "warning")
	at := a.UpdateAt
	a.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: a.Severity, CauseType: "source_event", CauseID: a.LatestEventID}
	a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 4, RequiredRevision: 1}}}
	created, e := business.CreateAlert(t.Context(), a)
	if e != nil {
		t.Fatal(e)
	}
	a = created.Alert
	task, e := svc.Record(t.Context(), a, "kac", actiondelivery.Cause{Type: "source_event", ID: a.LatestEventID})
	if e != nil {
		t.Fatal(e)
	}
	if row, e := svc.Deliver(t.Context(), a.BKTenantID, task.Task.ID); !actiondelivery.CanDefer(e) || row.Task.Progress.Attempts != 0 {
		t.Fatal("unsynced action sent", row, e)
	}
	projected, e := p.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac")
	if e != nil {
		t.Fatal(e)
	}
	pending, e := p.Deliver(t.Context(), a.BKTenantID, projected.Task.ID)
	if e != nil || pending.Task.Progress.ErrorCode != "visibility_pending" {
		t.Fatal("visibility gate missing", e)
	}
	receiver.mu.Lock()
	if receiver.actionCalls != 0 {
		t.Error("action preceded visible projection")
	}
	receiver.visible = true
	receiver.mu.Unlock()
	now = now.Add(time.Second)
	if _, e = p.Deliver(t.Context(), a.BKTenantID, projected.Task.ID); e != nil {
		t.Fatal(e)
	}
	fault.fail = true
	if _, e = svc.Deliver(t.Context(), a.BKTenantID, task.Task.ID); e == nil {
		t.Fatal("local action result fault hidden")
	}
	restored := reopen()
	stored, e := restored.Get(t.Context(), a.BKTenantID, task.Task.ID)
	if e != nil || stored.Task.Progress.State != "sending" {
		t.Fatal(stored, e)
	}
	restarted, e := actiondelivery.New(restored, gate, resolver, sender, locker, clock)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(31 * time.Second)
	done, e := restarted.Deliver(t.Context(), a.BKTenantID, task.Task.ID)
	if e != nil || done.Task.Progress.State != "succeeded" || !done.Task.Progress.PreviousUnconfirmed {
		t.Fatal(done, e)
	}
	receiver.mu.Lock()
	if receiver.actionCalls != 2 || len(receiver.actions) != 1 {
		t.Error("retry changed action identity", receiver.actionCalls, len(receiver.actions))
	}
	receiver.mu.Unlock()
	current, e := business.GetAlertCurrent(t.Context(), a.BKTenantID, a.AlertID)
	if e != nil {
		t.Fatal(e)
	}
	upgrade := current.Alert.Clone()
	upgrade.Severity = "critical"
	upgrade.UpdateAt = upgrade.UpdateAt.Add(time.Second)
	upgrade.LatestEventID = "upgrade"
	admittedAt := upgrade.UpdateAt
	upgrade.Admission = domain.AlertAdmission{AdmittedAt: &admittedAt, Severity: "critical", CauseType: "source_event", CauseID: "upgrade"}
	changed, e := business.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, current.Version, upgrade)
	if e != nil {
		t.Fatal(e)
	}
	oldTrigger, e := restarted.Record(t.Context(), changed.Alert, "kac", actiondelivery.Cause{Type: "source_event", ID: "upgrade"})
	if e != nil {
		t.Fatal(e)
	}
	terminal := changed.Alert.Clone()
	terminal.UpdateAt = terminal.UpdateAt.Add(time.Second)
	terminal.Status = domain.AlertStatusClosed
	end := terminal.UpdateAt
	terminal.EndAt = &end
	terminal.EndType = domain.AlertEndTypeUser
	terminal.EndReason = "manual"
	closed, e := business.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, changed.Version, terminal)
	if e != nil {
		t.Fatal(e)
	}
	closing, e := restarted.Record(t.Context(), closed.Alert, "kac", actiondelivery.Cause{Type: "user_operation", ID: "manual-close"})
	if e != nil {
		t.Fatal(e)
	}
	finalProjection, e := p.Produce(t.Context(), a.BKTenantID, a.AlertID, "kac")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Deliver(t.Context(), a.BKTenantID, finalProjection.Task.ID); e != nil {
		t.Fatal(e)
	}
	skipped, e := restarted.Deliver(t.Context(), a.BKTenantID, closing.Task.ID)
	if e != nil || skipped.Task.ID != oldTrigger.Task.ID || skipped.Task.Progress.State != "skipped" || skipped.Task.Progress.Attempts != 0 {
		t.Fatal("stale trigger was not skipped before newer close", skipped, e)
	}
	final, e := restarted.Deliver(t.Context(), a.BKTenantID, closing.Task.ID)
	if e != nil || final.Task.Progress.State != "succeeded" {
		t.Fatal(final, e)
	}
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	if receiver.actionCalls != 3 || len(receiver.actions) != 2 {
		t.Fatal("unexpected receiver side effects", receiver.actionCalls, len(receiver.actions))
	}
	t.Logf("verified 3 action HTTP calls, 2 unique action identities, and 1 stale trigger skipped; projection HTTP calls=%d", receiver.projectionCalls)
}

func TestElasticsearchCombinedActionDelivery(t *testing.T) {
	actionStoreFixture(t, "elasticsearch", func(s *Store, reopen func() *Store, c config.StorageConfig, d string) {
		runCombinedActionDelivery(t, s, reopen, c, d)
	})
}

func TestMySQLCombinedActionDelivery(t *testing.T) {
	actionStoreFixture(t, "mysql", func(s *Store, reopen func() *Store, c config.StorageConfig, d string) {
		runCombinedActionDelivery(t, s, reopen, c, d)
	})
}
