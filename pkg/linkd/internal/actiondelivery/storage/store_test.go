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
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"linkd/internal/actiondelivery"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

func testAction(t *testing.T, tenant string, revision int64) actiondelivery.Task {
	t.Helper()
	a := storetest.Alert(tenant, "alert", "opening", "fp", "warning")
	a.Revision = revision
	at := a.UpdateAt
	a.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: a.Severity, CauseType: "source_event", CauseID: a.LatestEventID}
	a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 4, RequiredRevision: revision}}}
	task, e := actiondelivery.NewTask(a, "kac", actiondelivery.Cause{Type: "source_event", ID: a.LatestEventID}, time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC))
	if e != nil {
		t.Fatal(e)
	}
	return task
}

func runActionContract(t *testing.T, s *Store, reopen func() *Store) {
	t.Helper()
	first, second := testAction(t, "tenant-a", 1), testAction(t, "tenant-a", 2)
	// 倒序插入验证排序依据是业务 revision，不能依赖创建时间或任务哈希顺序。
	for _, task := range []actiondelivery.Task{second, first, testAction(t, "tenant-b", 1)} {
		if _, e := s.Put(t.Context(), task, ""); e != nil {
			t.Fatal(e)
		}
		if e := s.ConfirmVisible(t.Context(), task); e != nil {
			t.Fatal("queue order is not visible", e)
		}
	}
	if _, e := s.Put(t.Context(), first, ""); !errors.Is(e, actiondelivery.ErrConflict) {
		t.Fatal("create overwrote original", e)
	}
	restored := reopen()
	original, e := restored.Get(t.Context(), "tenant-a", first.ID)
	if e != nil || !reflect.DeepEqual(original.Task, first) {
		t.Fatal("frozen request changed after reopen", e)
	}
	head, e := restored.OldestUnsettled(t.Context(), "tenant-a", first.Request.AlertID, "kac")
	if e != nil || head.Task.ID != first.ID {
		t.Fatal("wrong action order", head, e)
	}
	if _, e := s.Get(t.Context(), "tenant-b", first.ID); !errors.Is(e, actiondelivery.ErrNotFound) {
		t.Fatal("tenant leak", e)
	}
	for _, limit := range []int{1, 2, 1024} {
		n, e := s.CountWork(t.Context(), "tenant-a", limit)
		if e != nil || n != min(2, limit) {
			t.Fatal(n, e)
		}
	}
	sending := first.Clone()
	until := first.CreatedAt.Add(30 * time.Second)
	sending.Progress.State = "sending"
	sending.Progress.Attempts = 1
	sending.Progress.TotalAttempts = 1
	sending.Progress.DueAt = nil
	sending.Progress.LeaseUntil = &until
	claimed, e := s.Put(t.Context(), sending, original.Version)
	if e != nil {
		t.Fatal(e)
	}
	failed := claimed.Task.Clone()
	failed.Progress.State = "failed"
	failed.Progress.LeaseUntil = nil
	failed.Progress.ErrorCode = "projection_unavailable"
	finished, e := s.Put(t.Context(), failed, claimed.Version)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Put(t.Context(), failed, claimed.Version); !errors.Is(e, actiondelivery.ErrConflict) {
		t.Fatal("stale CAS accepted", e)
	}
	head, e = s.OldestUnsettled(t.Context(), "tenant-a", first.Request.AlertID, "kac")
	if e != nil || head.Task.ID != first.ID || head.Task.Progress.State != "failed" {
		t.Fatal("failed barrier disappeared", e)
	}
	if n, e := s.CountWork(t.Context(), "tenant-a", 1024); e != nil || n != 1 {
		t.Fatal("failed task kept automatic slot", n, e)
	}
	if _, e = s.Put(t.Context(), first, finished.Version); e == nil {
		t.Fatal("failed task reset without recovery command")
	}
	rows, e := s.List(t.Context(), actiondelivery.Query{TenantID: "tenant-a", Limit: 16})
	if e != nil || len(rows) != 2 {
		t.Fatal("history missing", e)
	}
	work, e := s.List(t.Context(), actiondelivery.Query{TenantID: "tenant-a", WorkOnly: true, Limit: 16})
	if e != nil || len(work) != 1 || work[0].Task.ID != second.ID {
		t.Fatal("work index wrong", e)
	}
	if _, e = s.List(t.Context(), actiondelivery.Query{Limit: 1}); e == nil {
		t.Fatal("unscoped management list")
	}
	bad := first.Clone()
	bad.SourceVersion++
	if e = s.ConfirmVisible(t.Context(), bad); !errors.Is(e, actiondelivery.ErrConflict) {
		t.Fatal("mismatched visibility accepted", e)
	}
}

func actionStoreFixture(t *testing.T, backend string, run func(*Store, func() *Store, config.StorageConfig, string)) {
	t.Helper()
	deployment := fmt.Sprintf("action-%d-%d", os.Getpid(), time.Now().UnixNano())
	var cfg config.StorageConfig
	if backend == "elasticsearch" {
		endpoint := os.Getenv("LINKD_TEST_ELASTICSEARCH_URL")
		if endpoint == "" {
			t.Skip("set LINKD_TEST_ELASTICSEARCH_URL")
		}
		cfg = config.StorageConfig{Repository: config.RepositoryTypeElasticsearch, Elasticsearch: &config.ElasticsearchConfig{Addresses: []string{endpoint}}}
	} else {
		dsn := os.Getenv("LINKD_TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("set LINKD_TEST_MYSQL_DSN")
		}
		parsed, e := driver.ParseDSN(dsn)
		if e != nil {
			t.Fatal(e)
		}
		parsed.DBName = ""
		admin, e := sql.Open("mysql", parsed.FormatDSN())
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = admin.Close() })
		name := fmt.Sprintf("linkd_actions_%d_%d", os.Getpid(), time.Now().UnixNano())
		if _, e = admin.ExecContext(t.Context(), "CREATE DATABASE "+name); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, e := admin.ExecContext(ctx, "DROP DATABASE "+name); e != nil {
				t.Error(e)
			}
		})
		cfg = config.StorageConfig{Repository: config.RepositoryTypeMySQL, MySQL: &config.MySQLConfig{Address: parsed.Addr, Database: name, Username: parsed.User, Password: parsed.Passwd}}
	}
	s, e := Open(t.Context(), cfg, deployment)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	if s.transport != nil {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if code, _, e := s.request(ctx, http.MethodDelete, "/"+s.index, nil); e != nil || code != 200 {
				t.Error("cleanup owned actions", code, e)
			}
		})
		if code, _, e := s.request(t.Context(), http.MethodPut, "/"+s.index+"/_settings", []byte(`{"index":{"refresh_interval":"1ms","number_of_replicas":0}}`)); e != nil || code != 200 {
			t.Fatal(code, e)
		}
	}
	reopen := func() *Store {
		next, e := OpenExisting(t.Context(), cfg, deployment)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = next.Close() })
		return next
	}
	run(s, reopen, cfg, deployment)
}

func TestElasticsearchActionTaskContract(t *testing.T) {
	actionStoreFixture(t, "elasticsearch", func(s *Store, reopen func() *Store, _ config.StorageConfig, _ string) {
		runActionContract(t, s, reopen)
	})
}

func TestMySQLActionTaskContract(t *testing.T) {
	actionStoreFixture(t, "mysql", func(s *Store, reopen func() *Store, _ config.StorageConfig, _ string) {
		runActionContract(t, s, reopen)
	})
}

func TestActionSearchRejectsPartialResults(t *testing.T) {
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
			s, e := Open(t.Context(), config.StorageConfig{Repository: config.RepositoryTypeElasticsearch, Elasticsearch: &config.ElasticsearchConfig{Addresses: []string{server.URL}}}, "partial")
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = s.Close() }()
			if _, e = s.List(t.Context(), actiondelivery.Query{WorkOnly: true, Limit: 1}); e == nil {
				t.Fatal("partial work page accepted")
			}
			task := testAction(t, "tenant-a", 1)
			if _, e = s.OldestUnsettled(t.Context(), "tenant-a", task.Request.AlertID, "kac"); e == nil {
				t.Fatal("partial order accepted")
			}
			if e = s.ConfirmVisible(t.Context(), task); e == nil {
				t.Fatal("partial search confirmed visibility")
			}
		})
	}
}

func TestActionDecodeRejectsMismatchedDerivedIndexes(t *testing.T) {
	task := testAction(t, "tenant-a", 1)
	raw, _ := json.Marshal(task)
	if _, e := decode(raw, "tenant-a", task.ID, "1", true, true, task.Request.AlertID, "kac", 2); e == nil {
		t.Fatal("wrong revision index accepted")
	}
	if _, e := decode(raw, "tenant-a", task.ID, "1", true, false, task.Request.AlertID, "kac", 1); e == nil {
		t.Fatal("missing ordering barrier accepted")
	}
}
