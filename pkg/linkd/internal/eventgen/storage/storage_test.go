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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"linkd/internal/config"
	"linkd/internal/eventgen"
)

func esConfig(endpoint string) config.StorageConfig {
	return config.StorageConfig{Repository: config.RepositoryTypeElasticsearch, Elasticsearch: &config.ElasticsearchConfig{Addresses: []string{endpoint}}}
}

func TestElasticsearchWireFormatAndTenantQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/_search"):
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			ranges := request["query"].(map[string]any)["range"].(map[string]any)["id"].(map[string]any)
			if ranges["gte"] != "tenant." || ranges["lt"] != "tenant.~" {
				t.Error("missing tenant range", ranges)
			}
			_, _ = w.Write([]byte(`{"hits":{"hits":[{"_source":{"payload":{"id":"a"}}}]}}`))
		case strings.Contains(r.URL.Path, "/_doc/"):
			_, _ = w.Write([]byte(`{"_seq_no":2,"_primary_term":1,"_source":{"payload":{"id":"a"}}}`))
		default:
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"type":"resource_already_exists_exception"}}`))
		}
	}))
	defer server.Close()
	s, err := Open(t.Context(), esConfig(server.URL), "wire-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	b, v, err := s.Get(t.Context(), "records", "tenant.a")
	if err != nil || string(b) != `{"id":"a"}` || v != "2:1" {
		t.Fatal(string(b), v, err)
	}
	rows, err := s.List(t.Context(), "records", "tenant.", "tenant.", 10)
	if err != nil || len(rows) != 1 || string(rows[0]) != `{"id":"a"}` {
		t.Fatal(rows, err)
	}
}

func TestElasticsearchEventgenContract(t *testing.T) {
	endpoint := os.Getenv("LINKD_TEST_ELASTICSEARCH_URL")
	if endpoint == "" {
		t.Skip("set LINKD_TEST_ELASTICSEARCH_URL for real storage tests")
	}
	s, err := Open(t.Context(), esConfig(endpoint), "eventgen-"+eventgen.NewRunID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	defer func() {
		for _, kind := range []string{"records", "runs"} {
			if _, _, err := s.request(t.Context(), http.MethodDelete, "/"+s.table(kind), nil); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, kind := range []string{"records", "runs"} {
		for _, tenant := range []string{"tenant-a.", "tenant-b."} {
			if err = s.Put(t.Context(), kind, tenant+"task", "", json.RawMessage(fmt.Sprintf(`{"tenant":%q}`, tenant))); err != nil {
				t.Fatal(err)
			}
		}
		b, v, err := s.Get(t.Context(), kind, "tenant-a.task")
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Put(t.Context(), kind, "tenant-a.task", v, b); err != nil {
			t.Fatal(err)
		}
		if err = s.Put(t.Context(), kind, "tenant-a.task", v, b); !errors.Is(err, eventgen.ErrConflict) {
			t.Fatal(err)
		}
		rows, err := s.List(t.Context(), kind, "tenant-a.", "tenant-a.", 10)
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		if !strings.Contains(string(rows[0]), "tenant-a.") {
			t.Fatal("tenant isolation failed")
		}
	}
}

func TestMySQLEventgenContract(t *testing.T) {
	dsn := os.Getenv("LINKD_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set LINKD_TEST_MYSQL_DSN for isolated MySQL storage tests")
	}
	parsed, err := driver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	s, err := Open(ctx, config.StorageConfig{Repository: "mysql", MySQL: &config.MySQLConfig{Address: parsed.Addr, Database: parsed.DBName, Username: parsed.User, Password: parsed.Passwd}}, eventgen.NewRunID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	defer func() {
		for _, kind := range []string{"records", "runs"} {
			//nolint:gosec // G202: kind 仅来自 records/runs 两个固定值，namespace 使用绑定参数。
			if _, err := s.db.ExecContext(ctx, "DELETE FROM "+s.table(kind)+" WHERE namespace=?", s.namespace); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, kind := range []string{"records", "runs"} {
		for _, tenant := range []string{"tenant-a.", "tenant-b."} {
			if err = s.Put(ctx, kind, tenant+"task", "", json.RawMessage(`{"name":"test"}`)); err != nil {
				t.Fatal(err)
			}
		}
		body, version, err := s.Get(ctx, kind, "tenant-a.task")
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Put(ctx, kind, "tenant-a.task", version, body); err != nil {
			t.Fatal(err)
		}
		if err = s.Put(ctx, kind, "tenant-a.task", version, body); !errors.Is(err, eventgen.ErrConflict) {
			t.Fatal(err)
		}
		rows, err := s.List(ctx, kind, "tenant-a.", "tenant-a.", 10)
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
	}
}
