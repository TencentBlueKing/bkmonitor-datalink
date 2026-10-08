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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/projection"
	"linkd/internal/store/storetest"
)

func testTask(t *testing.T, tenant string) projection.Task {
	t.Helper()
	a := storetest.Alert(tenant, "alert", "opening", "fp", "warning")
	a.ExtraData = domain.JSONObject{"nested": json.RawMessage(`{"a":1,"b":[false,0]}`)}
	a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 4, RequiredRevision: 1}}}
	task, err := projection.NewTask(a, "kac", time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func runContract(t *testing.T, s *Store, reopen func() *Store) {
	t.Run("real storage and Redis delivery recovery", func(t *testing.T) { runDeliveryContract(t, s, reopen) })
	t.Run("autonomous production and delivery", func(t *testing.T) { runAutonomousContract(t, s, reopen) })
	ctx := t.Context()
	a, b := testTask(t, "tenant-a"), testTask(t, "tenant-b")
	first, err := s.Put(ctx, a, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, a, ""); !errors.Is(err, projection.ErrConflict) {
		t.Fatal("create overwrote existing task", err)
	}
	if _, err := s.Put(ctx, b, ""); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"tenant-a", "tenant-b", "absent"} {
		want := 1
		if scope == "absent" {
			want = 0
		}
		if n, err := s.CountWork(ctx, scope, projection.MaxPendingPerTenant); err != nil || n != want {
			t.Fatal("scoped pending count", scope, n, err)
		}
	}
	if _, err := s.Get(ctx, "tenant-b", a.ID); !errors.Is(err, projection.ErrNotFound) {
		t.Fatal("cross tenant get", err)
	}
	if _, err := s.List(ctx, projection.Query{Limit: 1}); err == nil {
		t.Fatal("unscoped management query accepted")
	}
	after := ""
	seen := map[string]bool{}
	for range 3 {
		rows, err := s.List(ctx, projection.Query{WorkOnly: true, After: after, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		if rows[0].Task.ID <= after || seen[rows[0].Task.ID] {
			t.Fatal("cursor not increasing")
		}
		after = rows[0].Task.ID
		seen[after] = true
	}
	if len(seen) != 2 {
		t.Fatal("task work lost")
	}
	stored, err := reopen().Get(ctx, a.Request.TenantID, a.ID)
	if err != nil || !reflect.DeepEqual(stored.Task, a) {
		t.Fatal("task restart lost frozen content", err)
	}
	attempt := a.Clone()
	lease := a.CreatedAt.Add(30 * time.Second)
	attempt.Progress.State = "sending"
	attempt.Progress.Attempts = 1
	attempt.Progress.TotalAttempts = 1
	attempt.Progress.DueAt = nil
	attempt.Progress.LeaseUntil = &lease
	var successes atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := s.Put(ctx, attempt, first.Version); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, projection.ErrConflict) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("CAS had multiple winners", successes.Load())
	}
	current, err := s.Get(ctx, a.Request.TenantID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	changed := current.Task.Clone()
	changed.Request.Alert = append(changed.Request.Alert, ' ')
	if _, err := s.Put(ctx, changed, current.Version); err == nil {
		t.Fatal("task content digest ignored")
	}
	failed := current.Task.Clone()
	failed.Progress.State = "failed"
	failed.Progress.ErrorCode = "revision_conflict"
	failed.Progress.LeaseUntil = nil
	saved, err := s.Put(ctx, failed, current.Version)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountWork(ctx, "tenant-a", 1); err != nil || n != 0 {
		t.Fatal("failed state retained capacity", n, err)
	}
	work, err := s.List(ctx, projection.Query{WorkOnly: true, Limit: 16})
	if err != nil || len(work) != 1 || work[0].Task.Request.TenantID != "tenant-b" {
		t.Fatal("failed task still scheduled", err)
	}
	admin, err := s.List(ctx, projection.Query{TenantID: a.Request.TenantID, Limit: 16})
	if err != nil || len(admin) != 1 || admin[0].Task.Progress.State != "failed" {
		t.Fatal("failed task deleted", err)
	}
	retry := saved.Task.Clone()
	retry.Progress.State = "pending"
	retry.Progress.ErrorCode = ""
	retry.Progress.Generation = 2
	retry.Progress.Attempts = 0
	retry.Progress.LastRetry = &projection.RetryRecord{Command: projection.RetryCommand{TenantID: a.Request.TenantID, TaskID: a.ID, ExpectedVersion: saved.Version, OperationID: "manual-1", OperatorID: "tester", Reason: "恢复测试投影"}}
	at := a.CreatedAt.Add(time.Second)
	retry.Progress.LastRetry.RequestedAt = at
	retry.Progress.UpdatedAt = at
	retry.Progress.DueAt = &at
	if _, err := s.Put(ctx, retry, saved.Version); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.List(ctx, projection.Query{WorkOnly: true, Limit: 16}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored", err)
	}
	for i := range 2 {
		a := storetest.Alert("quota", fmt.Sprintf("alert-%d", i), "opening", fmt.Sprint(i), "warning")
		a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 4, RequiredRevision: 1}}}
		task, err := projection.NewTask(a, "kac", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Put(t.Context(), task, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, limit := range []int{1, 2, projection.MaxPendingPerTenant} {
		if n, err := s.CountWork(t.Context(), "quota", limit); err != nil || n != min(2, limit) {
			t.Fatal("bounded count differs", limit, n, err)
		}
	}
}

func TestElasticsearchProjectionTaskContract(t *testing.T) {
	endpoint := os.Getenv("LINKD_TEST_ELASTICSEARCH_URL")
	if endpoint == "" {
		t.Skip("set LINKD_TEST_ELASTICSEARCH_URL")
	}
	deployment := fmt.Sprintf("projection-%d-%d", os.Getpid(), time.Now().UnixNano())
	cfg := config.StorageConfig{Repository: config.RepositoryTypeElasticsearch, Elasticsearch: &config.ElasticsearchConfig{Addresses: []string{endpoint}}}
	s, err := Open(t.Context(), cfg, deployment)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		code, _, err := s.request(ctx, http.MethodDelete, "/"+s.index, nil)
		if err != nil || code != 200 {
			t.Error("cleanup own index", code, err)
		}
	}()
	if code, _, err := s.request(t.Context(), http.MethodPut, "/"+s.index+"/_settings", []byte(`{"index":{"refresh_interval":"1ms","number_of_replicas":0}}`)); err != nil || code != 200 {
		t.Fatal("test index settings", code, err)
	}
	runContract(t, s, func() *Store {
		again, err := Open(t.Context(), cfg, deployment)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = again.Close() })
		return again
	})
}

func TestMySQLProjectionTaskContract(t *testing.T) {
	dsn := os.Getenv("LINKD_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set LINKD_TEST_MYSQL_DSN")
	}
	parsed, err := driver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.DBName = ""
	db, err := sql.Open("mysql", parsed.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	name := fmt.Sprintf("linkd_projection_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := db.ExecContext(t.Context(), "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := db.ExecContext(ctx, "DROP DATABASE "+name); err != nil {
			t.Error(err)
		}
	}()
	cfg := config.StorageConfig{Repository: config.RepositoryTypeMySQL, MySQL: &config.MySQLConfig{Address: parsed.Addr, Database: name, Username: parsed.User, Password: parsed.Passwd}}
	s, err := Open(t.Context(), cfg, "deployment-a")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	runContract(t, s, func() *Store {
		again, err := Open(t.Context(), cfg, "deployment-a")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = again.Close() })
		return again
	})
	other, err := Open(t.Context(), cfg, "deployment-b")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	a := testTask(t, "tenant-a")
	if _, err := other.Get(t.Context(), a.Request.TenantID, a.ID); !errors.Is(err, projection.ErrNotFound) {
		t.Fatal("cross deployment task read", err)
	}
}

func TestProjectionSearchRejectsPartialResults(t *testing.T) {
	for _, body := range []string{`{}`, `{"timed_out":true,"hits":{"hits":[]}}`, `{"_shards":{"failed":1},"hits":{"hits":[]}}`, `{"hits":{"hits":[{}]}}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					_, _ = w.Write([]byte(`{}`))
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			cfg := config.StorageConfig{Repository: config.RepositoryTypeElasticsearch, Elasticsearch: &config.ElasticsearchConfig{Addresses: []string{server.URL}}}
			s, err := Open(t.Context(), cfg, "partial")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			if _, err := s.List(t.Context(), projection.Query{WorkOnly: true, Limit: 1}); err == nil {
				t.Fatal("partial/invalid query accepted")
			}
		})
	}
}
