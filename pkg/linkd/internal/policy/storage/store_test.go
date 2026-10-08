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
	"strings"
	"sync"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"linkd/internal/config"
	"linkd/internal/policy"
)

func esConfig(endpoint string) config.StorageConfig {
	return config.StorageConfig{Repository: config.RepositoryTypeElasticsearch, Elasticsearch: &config.ElasticsearchConfig{Addresses: []string{endpoint}}}
}

// 仅给测试独立的合并索引建立查询屏障；GET/CAS 不用此屏障，配置集合也不允许用它掩盖可见性回归。
func refreshMergeFixture(t *testing.T, s *Store, kind string) {
	t.Helper()
	if s.transport == nil || !strings.HasPrefix(kind, "merge_") {
		return
	}
	if code, _, err := s.request(t.Context(), http.MethodPost, "/"+s.table(kind)+"/_refresh", nil); err != nil || code != 200 {
		t.Fatal("refresh owned merge fixture", code, err)
	}
}

func TestListRejectsIncompleteSearch(t *testing.T) {
	for _, body := range []string{`{}`, `{"hits":{"hits":[{"_source":{"payload":{}}},{"_source":{"payload":{}}}]}}`, `{"timed_out":true,"hits":{"hits":[]}}`, `{"_shards":{"failed":1},"hits":{"hits":[]}}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPut {
					_, _ = w.Write([]byte(`{}`))
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			s, err := Open(t.Context(), esConfig(server.URL), "partial")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			if _, err := s.List(t.Context(), "records", "", "", 1); err == nil {
				t.Fatal("partial list accepted")
			}
		})
	}
}

// TestElasticsearchPolicyContract 只创建并清理本次测试独立部署的配置与合并执行索引。
func TestElasticsearchPolicyContract(t *testing.T) { WithElasticsearchTestStore(t, runContract) }

// WithElasticsearchTestStore 仅供外部集成测试复用同一隔离索引与清理边界。
func WithElasticsearchTestStore(t *testing.T, run func(*testing.T, *Store, func() *Store)) {
	endpoint := os.Getenv("LINKD_TEST_ELASTICSEARCH_URL")
	if endpoint == "" {
		t.Skip("set LINKD_TEST_ELASTICSEARCH_URL")
	}
	deployment := fmt.Sprintf("policy-contract-%d-%d", os.Getpid(), time.Now().UnixNano())
	cfg := esConfig(endpoint)
	s, err := Open(t.Context(), cfg, deployment)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, kind := range policyCollections() {
			code, _, err := s.request(ctx, http.MethodDelete, "/"+s.table(kind), nil)
			if err != nil || code != 200 {
				t.Errorf("cleanup %s: %d %v", kind, code, err)
			}
		}
	}()
	for _, kind := range policyCollections() {
		code, _, err := s.request(t.Context(), http.MethodPut, "/"+s.table(kind)+"/_settings", []byte(`{"index":{"refresh_interval":"1ms","number_of_replicas":0}}`))
		if err != nil || code != 200 {
			t.Fatalf("test settings %d %v", code, err)
		}
	}
	run(t, s, func() *Store {
		reopened, err := Open(t.Context(), cfg, deployment)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		return reopened
	})
}

// TestMySQLPolicyContract 使用独立测试数据库，绝不在传入 DSN 的数据库清理表。
func TestMySQLPolicyContract(t *testing.T) { WithMySQLTestStore(t, runContract) }

// WithMySQLTestStore 仅供外部集成测试复用同一独立数据库与清理边界。
func WithMySQLTestStore(t *testing.T, run func(*testing.T, *Store, func() *Store)) {
	dsn := os.Getenv("LINKD_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set LINKD_TEST_MYSQL_DSN")
	}
	parsed, err := driver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.DBName = ""
	admin, err := sql.Open("mysql", parsed.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	name := fmt.Sprintf("linkd_policy_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE `"+name+"`"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP DATABASE `"+name+"`"); err != nil {
			t.Error(err)
		}
	}()
	cfg := config.StorageConfig{Repository: config.RepositoryTypeMySQL, MySQL: &config.MySQLConfig{Address: parsed.Addr, Database: name, Username: parsed.User, Password: parsed.Passwd}}
	s, err := Open(t.Context(), cfg, "policy-contract")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	run(t, s, func() *Store {
		reopened, err := Open(t.Context(), cfg, "policy-contract")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		return reopened
	})
	other, err := Open(t.Context(), cfg, "other-deployment")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if _, _, err := other.Get(t.Context(), "records", "scope:a"); !errors.Is(err, policy.ErrNotFound) {
		t.Fatalf("deployment leaked: %v", err)
	}
}

func runContract(t *testing.T, s *Store, reopen func() *Store) {
	t.Helper()
	runSuppressionCleanupContract(t, s, reopen)
	runSuppressionRequestContract(t, s, reopen)
	runMergeJournalContract(t, s, reopen)
	runMergeRelationContract(t, s, reopen)
	for _, kind := range []string{"records", "releases", "operations", "merge_decisions", "merge_members", "merge_relations", "merge_relation_refs"} {
		for _, id := range []string{"scope:a", "scope:b", "scopex:c"} {
			if err := s.Put(t.Context(), kind, id, "", json.RawMessage(`{"value":1}`)); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Put(t.Context(), kind, "scope:a", "", json.RawMessage(`{"value":2}`)); !errors.Is(err, policy.ErrConflict) {
			t.Fatal(err)
		}
		_, token, err := s.Get(t.Context(), kind, "scope:a")
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 6)
		var wg sync.WaitGroup
		for range 6 {
			wg.Go(func() { <-start; results <- s.Put(t.Context(), kind, "scope:a", token, json.RawMessage(`{"value":2}`)) })
		}
		close(start)
		wg.Wait()
		close(results)
		successes := 0
		for err := range results {
			if err == nil {
				successes++
			} else if !errors.Is(err, policy.ErrConflict) {
				t.Fatal(err)
			}
		}
		if successes != 1 {
			t.Fatalf("CAS successes %d", successes)
		}
		refreshMergeFixture(t, s, kind)
		rows, err := s.List(t.Context(), kind, "scope:", "", 1)
		if err != nil || len(rows) != 1 {
			t.Fatalf("first page %s %v", rows, err)
		}
		rows, err = s.List(t.Context(), kind, "scope:", "scope:a", 2)
		if err != nil || len(rows) != 1 {
			t.Fatalf("prefix leaked %s %v", rows, err)
		}
		rows, err = s.List(t.Context(), kind, "", "", 3)
		if err != nil || len(rows) != 3 {
			t.Fatalf("global recovery scan %s %v", rows, err)
		}
		if _, _, err := s.Get(t.Context(), kind, "missing"); !errors.Is(err, policy.ErrNotFound) {
			t.Fatal(err)
		}
	}
	reopened := reopen()
	raw, _, err := reopened.Get(t.Context(), "records", "scope:a")
	if err != nil || string(raw) != `{"value":2}` {
		t.Fatalf("reopen %s %v", raw, err)
	}
	// 服务级不可变发布跨重开、删除和旧操作重试也必须遵守同一后端契约。
	service := policy.NewService(s)
	request := policy.ApplyRequest{Scope: policy.Scope{TenantID: "tenant", Kind: policy.Suppression}, SchemaVersion: 1, ID: "p1", OperationID: "create", Spec: json.RawMessage(`{"name":"test","is_enable":true,"updated_at":"2026-09-30T00:00:00Z","space_code":"bkcc__2","activate_times":[],"policy":{"expression":"A","A":{"condition":"term","target_key":"source_id","target_value":"source"}},"scheme":[{"type":"clip","count":2,"duration":60,"duration_type":"second"}]}`)}
	first, err := service.Apply(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	recovered := policy.NewService(reopened)
	if _, err := recovered.Delete(t.Context(), request.Scope, request.ID, 1, "delete"); err != nil {
		t.Fatal(err)
	}
	retry, err := recovered.Apply(t.Context(), request)
	if err != nil || retry.Version != 1 || retry.RequestDigest != first.RequestDigest || !retry.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("retry %+v %v", retry, err)
	}
	rows, err := recovered.List(t.Context(), request.Scope, "", 2)
	if err != nil || len(rows) != 1 || !rows[0].Deleted {
		t.Fatalf("tombstone %+v %v", rows, err)
	}
	other := request.Scope
	other.TenantID = "other"
	if _, err := recovered.Get(t.Context(), other, request.ID); !errors.Is(err, policy.ErrNotFound) {
		t.Fatalf("tenant leak: %v", err)
	}
}
